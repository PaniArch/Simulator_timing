package core_test

import (
	"encoding/binary"
	"strings"
	"testing"

	"vortex.local/simulator/core"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/warp"
)

type wordSource struct {
	word       uint32
	reads      int
	beforeRead func()
}

func (s *wordSource) Read(_ uint32, destination []byte) error {
	s.reads++
	if s.beforeRead != nil {
		s.beforeRead()
		s.beforeRead = nil
	}
	binary.LittleEndian.PutUint32(destination, s.word)
	return nil
}

func catalogWord(t *testing.T, name string) uint32 {
	t.Helper()
	for _, entry := range isa.Catalog() {
		if entry.Name == name {
			return entry.Example
		}
	}
	t.Fatalf("catalog instruction %q not found", name)
	return 0
}

func initial(warpID uint8, running bool) state.WarpInitial {
	lanes := make([]state.LaneInitial, isa.FrozenLaneCount)
	for lane := range lanes {
		lanes[lane].ID = uint8(lane)
		lanes[lane].GPR[5] = uint32(warpID)<<16 | uint32(lane)
		lanes[lane].FPR[7] = 0x3f800000 + uint32(warpID)<<8 + uint32(lane)
	}
	result := state.WarpInitial{
		Topology: state.FrozenTopology(), WarpID: warpID,
		PC:        0x100 + uint32(warpID)*0x20,
		Lifecycle: state.WarpInactive, Lanes: lanes,
		TrapCSRs: state.TrapCSRState{MScratch: 0x1000 + uint32(warpID)},
	}
	if running {
		result.ActiveMask, result.Lifecycle = isa.AllLanes, state.WarpRunning
		result.Divergence = state.DivergenceInitial{
			WritePointer: 1,
			Records: []state.DivergenceRecordInitial{{
				Pointer: 0, OriginalMask: isa.AllLanes,
				NextPC: 0x200 + uint32(warpID)*4,
			}},
		}
	}
	return result
}

func makeWarp(t *testing.T, value state.WarpInitial, word uint32) (*state.WarpState, *warp.Warp, *wordSource) {
	t.Helper()
	owner, err := state.NewWarp(value)
	if err != nil {
		t.Fatal(err)
	}
	source := &wordSource{word: word}
	executor, err := warp.New(owner, source)
	if err != nil {
		t.Fatal(err)
	}
	return owner, executor, source
}

func makeCore(t *testing.T, running [isa.FrozenWarpCount]bool, words [isa.FrozenWarpCount]uint32) (*core.Core, [isa.FrozenWarpCount]*state.WarpState, [isa.FrozenWarpCount]*wordSource) {
	t.Helper()
	var owners [isa.FrozenWarpCount]*state.WarpState
	var sources [isa.FrozenWarpCount]*wordSource
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		owners[id], executors[id], sources[id] = makeWarp(t, initial(id, running[id]), words[id])
	}
	manager, err := core.New(executors)
	if err != nil {
		t.Fatal(err)
	}
	return manager, owners, sources
}

func makeCoreFromInitials(t *testing.T, initials [isa.FrozenWarpCount]state.WarpInitial, words [isa.FrozenWarpCount]uint32) (*core.Core, [isa.FrozenWarpCount]*state.WarpState, [isa.FrozenWarpCount]*wordSource) {
	t.Helper()
	var owners [isa.FrozenWarpCount]*state.WarpState
	var sources [isa.FrozenWarpCount]*wordSource
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		owners[id], executors[id], sources[id] = makeWarp(t, initials[id], words[id])
	}
	manager, err := core.New(executors)
	if err != nil {
		t.Fatal(err)
	}
	return manager, owners, sources
}

func customSources(t *testing.T, name string) (uint32, []isa.Register) {
	t.Helper()
	word := catalogWord(t, name)
	decoded, err := isa.Decode(word)
	if err != nil {
		t.Fatal(err)
	}
	return word, decoded.Sources
}

func TestNewRequiresExactFrozenWarpIDCoverage(t *testing.T) {
	var executors []*warp.Warp
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		_, executor, _ := makeWarp(t, initial(id, id == 0), 0x00108093)
		executors = append(executors, executor)
	}
	shuffled := []*warp.Warp{executors[2], executors[0], executors[3], executors[1]}
	manager, err := core.New(shuffled)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := manager.Slots()
	if err != nil || len(slots) != int(isa.FrozenWarpCount) {
		t.Fatalf("slots=%+v err=%v", slots, err)
	}
	for id, slot := range slots {
		if slot.WarpID != uint8(id) {
			t.Fatalf("slot %d carries warp id %d", id, slot.WarpID)
		}
	}

	for name, candidate := range map[string][]*warp.Warp{
		"short":     executors[:3],
		"nil":       {executors[0], executors[1], nil, executors[3]},
		"duplicate": {executors[0], executors[1], executors[1], executors[3]},
	} {
		if _, err := core.New(candidate); err == nil {
			t.Fatalf("%s topology accepted", name)
		}
	}
	badTopology := initial(0, true)
	badTopology.Topology.WarpCount--
	if _, err := state.NewWarp(badTopology); err == nil || !strings.Contains(err.Error(), "topology") {
		t.Fatalf("non-frozen topology error=%v", err)
	}
	badID := initial(isa.FrozenWarpCount, true)
	if _, err := state.NewWarp(badID); err == nil || !strings.Contains(err.Error(), "warp id") {
		t.Fatalf("out-of-range ID error=%v", err)
	}
}

func TestLifecycleDistinguishesAllFourStatesAndTypedReasons(t *testing.T) {
	tmc := catalogWord(t, "tmc")
	manager, _, _ := makeCore(t,
		[isa.FrozenWarpCount]bool{false, true, true, true},
		[isa.FrozenWarpCount]uint32{0, 0x00108093, tmc, 0x00108093},
	)
	if err := manager.Block(1, core.BlockExplicit); err != nil {
		t.Fatal(err)
	}
	step, err := manager.Step(state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	if step.WarpID != 2 || step.NextLifecycle != core.WarpFinished || step.WarpResult.Outcome != warp.OutcomeRetired {
		t.Fatalf("termination step=%+v", step)
	}
	want := []struct {
		lifecycle core.WarpLifecycle
		reason    core.BlockReason
	}{
		{core.WarpInactive, core.BlockNone},
		{core.WarpBlocked, core.BlockExplicit},
		{core.WarpFinished, core.BlockNone},
		{core.WarpRunnable, core.BlockNone},
	}
	slots, err := manager.Slots()
	if err != nil {
		t.Fatal(err)
	}
	for id, expected := range want {
		if slots[id].Lifecycle != expected.lifecycle || slots[id].BlockReason != expected.reason {
			t.Fatalf("slot %d=%+v want lifecycle/reason=%s/%s", id, slots[id], expected.lifecycle, expected.reason)
		}
	}
	if err := manager.Resume(1); err != nil {
		t.Fatal(err)
	}
	resumed, _ := manager.Slot(1)
	if resumed.Lifecycle != core.WarpRunnable || resumed.BlockReason != core.BlockNone {
		t.Fatalf("resumed slot=%+v", resumed)
	}
	if err := manager.Block(3, core.BlockNone); err == nil {
		t.Fatal("untyped block reason accepted")
	}
}

func TestRoundRobinFairnessAndSkippingNonRunnableSlots(t *testing.T) {
	const increment = uint32(0x00108093) // addi x1,x1,1
	manager, owners, sources := makeCore(t,
		[isa.FrozenWarpCount]bool{true, true, true, true},
		[isa.FrozenWarpCount]uint32{increment, increment, increment, increment},
	)
	var selected []uint8
	for range 8 {
		step, err := manager.Step(state.ReadContext{})
		if err != nil {
			t.Fatal(err)
		}
		selected = append(selected, step.WarpID)
	}
	if got := selected; len(got) != 8 || got[0] != 0 || got[1] != 1 || got[2] != 2 || got[3] != 3 || got[4] != 0 || got[5] != 1 || got[6] != 2 || got[7] != 3 {
		t.Fatalf("round-robin order=%v", got)
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		values, _ := owners[id].ReadRegister(isa.Register{File: isa.Integer, Index: 1})
		if values != (isa.LaneValues{2, 2, 2, 2}) || sources[id].reads != 2 {
			t.Fatalf("warp %d values=%v reads=%d", id, values, sources[id].reads)
		}
	}
	if err := manager.Block(1, core.BlockExplicit); err != nil {
		t.Fatal(err)
	}
	if err := manager.Block(3, core.BlockExplicit); err != nil {
		t.Fatal(err)
	}
	for index, want := range []uint8{0, 2, 0, 2} {
		step, err := manager.Step(state.ReadContext{})
		if err != nil || step.WarpID != want {
			t.Fatalf("skip step %d=%+v err=%v want warp %d", index, step, err, want)
		}
	}
}

func TestSingleStepIsolationAndCompletionDoNotStopOtherWarps(t *testing.T) {
	tmc := catalogWord(t, "tmc")
	const increment = uint32(0x00108093)
	manager, owners, _ := makeCore(t,
		[isa.FrozenWarpCount]bool{true, true, true, true},
		[isa.FrozenWarpCount]uint32{tmc, increment, increment, increment},
	)
	var before [isa.FrozenWarpCount]state.WarpSnapshot
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		before[id], _ = owners[id].Snapshot()
	}
	first, err := manager.Step(state.ReadContext{})
	if err != nil || first.WarpID != 0 || first.NextLifecycle != core.WarpFinished {
		t.Fatalf("first step=%+v err=%v", first, err)
	}
	for id := uint8(1); id < isa.FrozenWarpCount; id++ {
		after, _ := owners[id].Snapshot()
		if after != before[id] {
			t.Fatalf("warp 0 completion changed warp %d canonical state", id)
		}
	}
	complete, err := manager.Complete()
	if err != nil || complete {
		t.Fatalf("premature completion=%t err=%v", complete, err)
	}
	second, err := manager.Step(state.ReadContext{})
	if err != nil || second.WarpID != 1 || second.WarpResult.Outcome != warp.OutcomeRetired {
		t.Fatalf("second step=%+v err=%v", second, err)
	}
	values, _ := owners[1].ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	if values != (isa.LaneValues{1, 1, 1, 1}) {
		t.Fatalf("warp 1 did not continue: x1=%v", values)
	}
	for _, id := range []uint8{2, 3} {
		after, _ := owners[id].Snapshot()
		if after != before[id] {
			t.Fatalf("warp 1 step changed warp %d PC/GPR/FPR/mask/CSR/divergence state", id)
		}
	}
}

func TestDeferredAndFaultingWarpsBlockInsteadOfSpinning(t *testing.T) {
	manager, _, _ := makeCore(t,
		[isa.FrozenWarpCount]bool{true, true, false, false},
		[isa.FrozenWarpCount]uint32{catalogWord(t, "wspawn"), 0xffffffff, 0, 0},
	)
	spawn, err := manager.Step(state.ReadContext{})
	if err != nil || spawn.WarpID != 0 || spawn.NextLifecycle != core.WarpBlocked || spawn.BlockReason != core.BlockWarpSpawn {
		t.Fatalf("spawn step=%+v err=%v", spawn, err)
	}
	fault, err := manager.Step(state.ReadContext{})
	if err != nil || fault.WarpID != 1 || fault.NextLifecycle != core.WarpBlocked || fault.BlockReason != core.BlockFault {
		t.Fatalf("fault step=%+v err=%v", fault, err)
	}
	idle, err := manager.Step(state.ReadContext{})
	if err != nil || idle.Outcome != core.StepIdle {
		t.Fatalf("idle step=%+v err=%v", idle, err)
	}
}

func TestWSPAWNAtomicallyActivatesFrozenTargetsAndPreservesTheirState(t *testing.T) {
	wspawn, sources := customSources(t, "wspawn")
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 3)
		initials[id].SavedThreadMask = isa.LaneMask(id)
		initials[id].FCSR = uint32(id)
		initials[id].TrapCSRs = state.TrapCSRState{
			MStatus: 0x10 + uint32(id), MTVec: 0x200 + uint32(id)*4,
			MScratch: 0x3000 + uint32(id), MEPC: 0x400 + uint32(id)*4,
			MCause: 0x20 + uint32(id), MTVal: 0x30 + uint32(id),
		}
		initials[id].Divergence = state.DivergenceInitial{
			WritePointer: 1,
			Records: []state.DivergenceRecordInitial{{
				Pointer: 0, OriginalMask: isa.AllLanes,
				NextPC: 0x500 + uint32(id)*4, ElseVisited: id&1 != 0,
			}},
		}
	}
	initials[3].TrapCSRs.MScratch = 0xfeedbeef
	for lane := range initials[3].Lanes {
		initials[3].Lanes[lane].GPR[sources[0].Index] = 4
		initials[3].Lanes[lane].GPR[sources[1].Index] = 0x700
	}
	manager, owners, _ := makeCoreFromInitials(t, initials,
		[isa.FrozenWarpCount]uint32{0, 0, 0, wspawn},
	)
	var before [isa.FrozenWarpCount]state.WarpSnapshot
	var gprBefore [isa.FrozenWarpCount]isa.LaneValues
	var fprBefore [isa.FrozenWarpCount]isa.LaneValues
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		before[id], _ = owners[id].Snapshot()
		gprBefore[id], _ = owners[id].ReadRegister(isa.Register{File: isa.Integer, Index: 5})
		fprBefore[id], _ = owners[id].ReadRegister(isa.Register{File: isa.Float, Index: 7})
	}

	step, err := manager.Step(state.ReadContext{})
	if err != nil || step.WarpID != 3 || step.WarpResult.Outcome != warp.OutcomeRetired ||
		step.WarpResult.NextPC != before[3].PC()+4 || step.WarpResult.Effects.WarpSpawn == nil ||
		step.WarpResult.Effects.WarpSpawn.Targets != 0b0111 || step.WarpResult.Effects.WarpSpawn.RequestedCount != 4 {
		t.Fatalf("WSPAWN step=%+v err=%v", step, err)
	}
	for id := uint8(0); id < 3; id++ {
		after, _ := owners[id].Snapshot()
		slot, _ := manager.Slot(id)
		gprAfter, _ := owners[id].ReadRegister(isa.Register{File: isa.Integer, Index: 5})
		fprAfter, _ := owners[id].ReadRegister(isa.Register{File: isa.Float, Index: 7})
		beforeTrap, afterTrap := before[id].TrapCSRs(), after.TrapCSRs()
		beforeRecord, _ := before[id].DivergenceRecord(0)
		afterRecord, _ := after.DivergenceRecord(0)
		if slot.Lifecycle != core.WarpRunnable || !slot.Participated || after.PC() != 0x700 ||
			after.ActiveMask() != 1 || after.Lifecycle() != state.WarpRunning ||
			afterTrap.MScratch != 0xfeedbeef || afterTrap.MStatus != beforeTrap.MStatus ||
			afterTrap.MTVec != beforeTrap.MTVec || afterTrap.MEPC != beforeTrap.MEPC ||
			afterTrap.MCause != beforeTrap.MCause || afterTrap.MTVal != beforeTrap.MTVal ||
			after.FCSR() != before[id].FCSR() || after.SavedThreadMask() != before[id].SavedThreadMask() ||
			after.DivergenceWritePointer() != before[id].DivergenceWritePointer() || afterRecord != beforeRecord ||
			gprAfter != gprBefore[id] || fprAfter != fprBefore[id] {
			t.Fatalf("target %d copied or reset unfrozen state: before=%+v after=%+v slot=%+v", id, before[id], after, slot)
		}
	}
}

func TestWSPAWNSingleActiveGateBlocksThenRetriesSameInstruction(t *testing.T) {
	wspawn, sources := customSources(t, "wspawn")
	tmc := catalogWord(t, "tmc")
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 0 || id == 3)
	}
	for lane := range initials[0].Lanes {
		initials[0].Lanes[lane].GPR[sources[0].Index] = 1 // no targets, but the RTL gate still applies
		initials[0].Lanes[lane].GPR[sources[1].Index] = 0x600
	}
	manager, owners, _ := makeCoreFromInitials(t, initials,
		[isa.FrozenWarpCount]uint32{wspawn, 0, 0, tmc},
	)
	sourceBefore, _ := owners[0].Snapshot()
	blocked, err := manager.Step(state.ReadContext{})
	sourceBlocked, _ := owners[0].Snapshot()
	if err != nil || blocked.WarpID != 0 || blocked.NextLifecycle != core.WarpBlocked ||
		blocked.BlockReason != core.BlockWarpSpawn || sourceBlocked != sourceBefore {
		t.Fatalf("gated WSPAWN=%+v err=%v changed=%t", blocked, err, sourceBlocked != sourceBefore)
	}
	terminated, err := manager.Step(state.ReadContext{})
	if err != nil || terminated.WarpID != 3 || terminated.NextLifecycle != core.WarpFinished {
		t.Fatalf("other warp termination=%+v err=%v", terminated, err)
	}
	retried, err := manager.Step(state.ReadContext{})
	if err != nil || retried.WarpID != 0 || retried.WarpResult.Outcome != warp.OutcomeRetired ||
		retried.WarpResult.PC != sourceBefore.PC() || retried.WarpResult.NextPC != sourceBefore.PC()+4 {
		t.Fatalf("retried WSPAWN=%+v err=%v", retried, err)
	}
}

func TestWSPAWNTargetMutationDuringSourceStepFailsWithoutPartialActivation(t *testing.T) {
	wspawn, sources := customSources(t, "wspawn")
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 3)
	}
	for lane := range initials[3].Lanes {
		initials[3].Lanes[lane].GPR[sources[0].Index] = 3
		initials[3].Lanes[lane].GPR[sources[1].Index] = 0x700
	}
	manager, owners, wordSources := makeCoreFromInitials(t, initials,
		[isa.FrozenWarpCount]uint32{0, 0, 0, wspawn},
	)
	sourceBefore, _ := owners[3].Snapshot()
	target0Before, _ := owners[0].Snapshot()
	target2Before, _ := owners[2].Snapshot()
	wordSources[3].beforeRead = func() {
		if err := owners[1].SetPC(0x280); err != nil {
			panic(err)
		}
	}
	step, err := manager.Step(state.ReadContext{})
	if err == nil || step.WarpID != 3 || step.NextLifecycle != core.WarpBlocked || step.BlockReason != core.BlockFault {
		t.Fatalf("stale target WSPAWN step=%+v err=%v", step, err)
	}
	sourceAfter, _ := owners[3].Snapshot()
	target0After, _ := owners[0].Snapshot()
	target1After, _ := owners[1].Snapshot()
	target2After, _ := owners[2].Snapshot()
	if sourceAfter != sourceBefore || target0After != target0Before || target2After != target2Before ||
		target1After.PC() != 0x280 || target1After.Lifecycle() != state.WarpInactive || target1After.ActiveMask() != 0 {
		t.Fatalf("stale WSPAWN partially committed source/targets")
	}
}

func TestWSPAWNSourceMutationDuringSourceStepFailsWithoutPartialActivation(t *testing.T) {
	wspawn, sources := customSources(t, "wspawn")
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 3)
	}
	for lane := range initials[3].Lanes {
		initials[3].Lanes[lane].GPR[sources[0].Index] = 3
		initials[3].Lanes[lane].GPR[sources[1].Index] = 0x700
	}
	manager, owners, wordSources := makeCoreFromInitials(t, initials,
		[isa.FrozenWarpCount]uint32{0, 0, 0, wspawn},
	)
	sourceBefore, _ := owners[3].Snapshot()
	var targetsBefore [isa.FrozenWarpCount - 1]state.WarpSnapshot
	for id := uint8(0); id < isa.FrozenWarpCount-1; id++ {
		targetsBefore[id], _ = owners[id].Snapshot()
	}
	mutatedRegister := isa.Register{File: isa.Integer, Index: 31}
	var sourceMutated state.WarpSnapshot
	wordSources[3].beforeRead = func() {
		if err := owners[3].WriteRegister(mutatedRegister, 1, isa.LaneValues{0xfeedface}); err != nil {
			panic(err)
		}
		var err error
		sourceMutated, err = owners[3].Snapshot()
		if err != nil {
			panic(err)
		}
	}
	step, err := manager.Step(state.ReadContext{})
	if err == nil || step.WarpID != 3 || step.NextLifecycle != core.WarpBlocked || step.BlockReason != core.BlockFault {
		t.Fatalf("stale source WSPAWN step=%+v err=%v", step, err)
	}
	sourceAfter, _ := owners[3].Snapshot()
	mutatedValues, readErr := sourceAfter.ReadRegister(mutatedRegister)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if sourceAfter != sourceMutated || sourceAfter.PC() != sourceBefore.PC() || sourceAfter.Lifecycle() != sourceBefore.Lifecycle() ||
		sourceAfter.ActiveMask() != sourceBefore.ActiveMask() || mutatedValues[0] != 0xfeedface {
		t.Fatalf("stale source WSPAWN committed or lost intervening mutation")
	}
	for id := uint8(0); id < isa.FrozenWarpCount-1; id++ {
		targetAfter, _ := owners[id].Snapshot()
		if targetAfter != targetsBefore[id] {
			t.Fatalf("stale source WSPAWN partially activated target %d", id)
		}
	}
}

func TestWSYNCImmediateAndPendingProviderResume(t *testing.T) {
	wsync := catalogWord(t, "wsync")
	t.Run("immediate", func(t *testing.T) {
		manager, owners, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{wsync, 0, 0, 0},
		)
		if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(uint8) (bool, error) { return false, nil })); err != nil {
			t.Fatal(err)
		}
		before, _ := owners[0].Snapshot()
		step, err := manager.Step(state.ReadContext{PendingPriorWork: true})
		after, _ := owners[0].Snapshot()
		if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired || step.NextLifecycle != core.WarpRunnable ||
			after.PC() != before.PC()+4 || step.WarpResult.NextPC != before.PC()+4 {
			t.Fatalf("immediate WSYNC=%+v before=%+v after=%+v err=%v", step, before, after, err)
		}
	})

	t.Run("pending-then-resume", func(t *testing.T) {
		manager, owners, sources := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{wsync, 0, 0, 0},
		)
		pending := true
		if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(warpID uint8) (bool, error) {
			if warpID != 0 {
				t.Fatalf("pending provider queried warp %d", warpID)
			}
			return pending, nil
		})); err != nil {
			t.Fatal(err)
		}
		before, _ := owners[0].Snapshot()
		blocked, err := manager.Step(state.ReadContext{})
		blockedState, _ := owners[0].Snapshot()
		if err != nil || blocked.WarpResult.Outcome != warp.OutcomeDeferred ||
			blocked.NextLifecycle != core.WarpBlocked || blocked.BlockReason != core.BlockPendingWork || blockedState != before {
			t.Fatalf("pending WSYNC=%+v err=%v changed=%t", blocked, err, blockedState != before)
		}
		pending = false
		resumed, err := manager.Step(state.ReadContext{})
		after, _ := owners[0].Snapshot()
		if err != nil || resumed.WarpID != 0 || resumed.WarpResult.Outcome != warp.OutcomeRetired ||
			resumed.WarpResult.PC != before.PC() || after.PC() != before.PC()+4 ||
			resumed.NextLifecycle != core.WarpRunnable || sources[0].reads != 2 {
			t.Fatalf("resumed WSYNC=%+v after=%+v reads=%d err=%v", resumed, after, sources[0].reads, err)
		}
	})
}

func TestBARInstructionsRemainBlockedOnUnsupportedCoordinator(t *testing.T) {
	for _, name := range []string{"bar", "bar.arrive", "bar.wait"} {
		t.Run(name, func(t *testing.T) {
			manager, owners, _ := makeCore(t,
				[isa.FrozenWarpCount]bool{true, false, false, false},
				[isa.FrozenWarpCount]uint32{catalogWord(t, name), 0, 0, 0},
			)
			before, _ := owners[0].Snapshot()
			step, err := manager.Step(state.ReadContext{})
			after, _ := owners[0].Snapshot()
			if err != nil || step.WarpResult.Outcome != warp.OutcomeDeferred ||
				step.NextLifecycle != core.WarpBlocked || step.BlockReason != core.BlockBarrier || after != before {
				t.Fatalf("%s step=%+v err=%v changed=%t", name, step, err, after != before)
			}
			idle, err := manager.Step(state.ReadContext{})
			if err != nil || idle.Outcome != core.StepIdle {
				t.Fatalf("%s unsupported coordinator did not remain blocked: %+v err=%v", name, idle, err)
			}
		})
	}
}
