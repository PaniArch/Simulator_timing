package core_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"vortex.local/simulator/core"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/warp"
)

type failingBatchMemory struct {
	*memory.Memory
}

func (*failingBatchMemory) WriteBatch([]uint32, [][]byte) error {
	return errors.New("injected global batch failure")
}

func ctaConfig(id uint32, warps []uint8, threads, lmem uint32) core.CTAConfig {
	return core.CTAConfig{
		ID: id, WarpIDs: warps,
		BlockID: [3]uint32{id, 0, 0}, BlockDimensions: [3]uint32{threads, 1, 1},
		GridDimensions: [3]uint32{4, 1, 1}, Entry: 0x100,
		LocalMemorySize: lmem, ClusterSize: 1,
	}
}

func makeCTAReadyCoreFromInitials(
	t *testing.T,
	initials [isa.FrozenWarpCount]state.WarpInitial,
	words [isa.FrozenWarpCount]uint32,
	ctas *core.CTAManager,
) (*core.Core, [isa.FrozenWarpCount]*state.WarpState, [isa.FrozenWarpCount]*wordSource) {
	t.Helper()
	global, err := memory.New(1)
	if err != nil {
		t.Fatal(err)
	}
	var owners [isa.FrozenWarpCount]*state.WarpState
	var sources [isa.FrozenWarpCount]*wordSource
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		owners[id], err = state.NewWarp(initials[id])
		if err != nil {
			t.Fatal(err)
		}
		sources[id] = &wordSource{word: words[id]}
		if ctas.HasWarp(id) {
			route, routeErr := core.NewCTAMemory(ctas, id, global)
			if routeErr != nil {
				t.Fatal(routeErr)
			}
			executors[id], err = warp.NewWithServices(owners[id], sources[id], route)
		} else {
			executors[id], err = warp.New(owners[id], sources[id])
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := core.NewWithCTAManager(executors, ctas)
	if err != nil {
		t.Fatal(err)
	}
	return manager, owners, sources
}

func TestCTAManagerCanonicalMembershipContextAndAtomicAdmission(t *testing.T) {
	manager := core.NewCTAManager()
	admitted, err := manager.Admit(ctaConfig(0, []uint8{2, 0}, 8, 1))
	if err != nil {
		t.Fatal(err)
	}
	if admitted.LocalMemory.Address != isa.FrozenLocalMemBase || admitted.LocalMemory.Size != 64 ||
		len(admitted.Members) != 2 || admitted.Members[0].WarpID != 2 || admitted.Members[0].Rank != 0 ||
		admitted.Members[1].WarpID != 0 || admitted.Members[1].Rank != 1 {
		t.Fatalf("admitted CTA=%+v", admitted)
	}
	view, err := manager.ViewForWarp(0)
	if err != nil {
		t.Fatal(err)
	}
	if view.ID != 0 || view.Rank != 1 || view.Size != 2 || view.LocalMemoryAddress != isa.FrozenLocalMemBase ||
		view.ThreadCoordinates[0] != (isa.LaneValues{4, 5, 6, 7}) ||
		view.BlockDimensions != [3]uint32{8, 1, 1} || view.Entry != 0x100 {
		t.Fatalf("warp CTA view=%+v", view)
	}

	before, _ := manager.Snapshot(0)
	for name, invalid := range map[string]core.CTAConfig{
		"cross-CTA member":   ctaConfig(1, []uint8{0, 1}, 8, 64),
		"duplicate member":   ctaConfig(1, []uint8{1, 1}, 8, 64),
		"dimension mismatch": ctaConfig(1, []uint8{1}, 8, 64),
		"invalid warp":       ctaConfig(1, []uint8{isa.FrozenWarpCount}, 4, 64),
	} {
		if _, err := manager.Admit(invalid); err == nil {
			t.Fatalf("%s admission succeeded", name)
		}
		after, snapshotErr := manager.Snapshot(0)
		if snapshotErr != nil || !reflect.DeepEqual(after, before) || manager.HasWarp(1) {
			t.Fatalf("%s left partial CTA state: after=%+v err=%v", name, after, snapshotErr)
		}
	}

	// Public observations are detached and cannot rewrite the owner.
	before.Members[0].WarpID = 3
	again, _ := manager.Snapshot(0)
	if again.Members[0].WarpID != 2 {
		t.Fatalf("detached snapshot mutated canonical membership: %+v", again)
	}

	capacity := core.NewCTAManager()
	if _, err := capacity.Admit(ctaConfig(0, []uint8{0}, 4, isa.FrozenLocalMemSize)); err != nil {
		t.Fatal(err)
	}
	if _, err := capacity.Admit(ctaConfig(1, []uint8{1}, 4, 64)); err == nil || capacity.HasWarp(1) {
		t.Fatalf("failed LMEM allocation left membership: err=%v member=%t", err, capacity.HasWarp(1))
	}
	if _, err := capacity.Snapshot(1); err == nil {
		t.Fatal("failed LMEM allocation left a CTA record")
	}
}

func TestCoreRejectsMissingOrDifferentCanonicalCTAMemoryRoute(t *testing.T) {
	contextOwner := core.NewCTAManager()
	contextCTA, err := contextOwner.Admit(ctaConfig(0, []uint8{0}, 4, 64))
	if err != nil {
		t.Fatal(err)
	}
	routeOwner := core.NewCTAManager()
	if _, err := routeOwner.Admit(ctaConfig(1, []uint8{1}, 4, 64)); err != nil {
		t.Fatal(err)
	}
	routeCTA, err := routeOwner.Admit(ctaConfig(0, []uint8{0}, 4, 64))
	if err != nil {
		t.Fatal(err)
	}
	if contextCTA.LocalMemory.Address != isa.FrozenLocalMemBase || routeCTA.LocalMemory.Address != isa.FrozenLocalMemBase {
		t.Fatal("CTA-visible LMEM allocations do not share the frozen virtual base")
	}

	global, _ := memory.New(1)
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		owner, ownerErr := state.NewWarp(initial(id, id == 0))
		if ownerErr != nil {
			t.Fatal(ownerErr)
		}
		source := &wordSource{word: 0x00108093}
		if id == 0 {
			route, routeErr := core.NewCTAMemory(routeOwner, id, global)
			if routeErr != nil {
				t.Fatal(routeErr)
			}
			executors[id], ownerErr = warp.NewWithServices(owner, source, route)
		} else {
			executors[id], ownerErr = warp.New(owner, source)
		}
		if ownerErr != nil {
			t.Fatal(ownerErr)
		}
	}
	if _, err := core.NewWithCTAManager(executors, contextOwner); err == nil {
		t.Fatal("Core accepted CTA context and LMEM routes backed by different managers")
	}

	legacy, _, _ := makeCore(t,
		[isa.FrozenWarpCount]bool{true, false, false, false},
		[isa.FrozenWarpCount]uint32{0x00108093, 0, 0, 0},
	)
	if err := legacy.SetCTAManager(contextOwner); err == nil {
		t.Fatal("Core accepted a CTA member with no CTA memory route")
	}
	step, err := legacy.Step(state.ReadContext{})
	if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired {
		t.Fatalf("failed CTA attachment partially changed Core: step=%+v err=%v", step, err)
	}
}

func csrReadWord(address uint16, destination uint8) uint32 {
	return uint32(address)<<20 | 2<<12 | uint32(destination)<<7 | 0x73 // CSRRS rd,csr,x0
}

func TestCoreDerivesCanonicalCTAAndThreadCSRContext(t *testing.T) {
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(core.CTAConfig{
		ID: 2, WarpIDs: []uint8{0, 2}, BlockID: [3]uint32{1, 2, 0},
		BlockDimensions: [3]uint32{8, 1, 1}, GridDimensions: [3]uint32{3, 4, 1},
		Entry: 0x240, LocalMemorySize: 64, ClusterSize: 2,
	}); err != nil {
		t.Fatal(err)
	}
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 2)
	}
	manager, owners, sources := makeCTAReadyCoreFromInitials(t, initials,
		[isa.FrozenWarpCount]uint32{0, 0, csrReadWord(0xcd3, 10), 0}, ctas)
	if _, err := ctas.Admit(ctaConfig(1, []uint8{1}, 4, 64)); err == nil || ctas.HasWarp(1) {
		t.Fatalf("attached CTA manager accepted membership mutation: err=%v", err)
	}
	fake := state.ReadContext{CTA: isa.CTAView{
		ID: 99, Rank: 99, ThreadCoordinates: [3]isa.LaneValues{{99, 99, 99, 99}},
	}}
	step, err := manager.Step(fake)
	if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired {
		t.Fatalf("thread-coordinate CSR step=%+v err=%v", step, err)
	}
	threadX, _ := owners[2].ReadRegister(isa.Register{File: isa.Integer, Index: 10})
	if threadX != (isa.LaneValues{4, 5, 6, 7}) {
		t.Fatalf("canonical thread-x CSR=%v", threadX)
	}

	sources[2].word = csrReadWord(0xcd1, 11)
	step, err = manager.Step(fake)
	rank, _ := owners[2].ReadRegister(isa.Register{File: isa.Integer, Index: 11})
	if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired || rank != (isa.LaneValues{1, 1, 1, 1}) {
		t.Fatalf("canonical rank CSR=%v step=%+v err=%v", rank, step, err)
	}

	sources[2].word = csrReadWord(0xcdf, 12)
	step, err = manager.Step(fake)
	lmem, _ := owners[2].ReadRegister(isa.Register{File: isa.Integer, Index: 12})
	if err != nil || lmem != (isa.LaneValues{isa.FrozenLocalMemBase, isa.FrozenLocalMemBase, isa.FrozenLocalMemBase, isa.FrozenLocalMemBase}) {
		t.Fatalf("canonical LMEM CSR=%v step=%+v err=%v", lmem, step, err)
	}
	for _, test := range []struct {
		address uint16
		want    isa.LaneValues
	}{
		{0xcd0, isa.LaneValues{2, 2, 2, 2}},
		{0xcd2, isa.LaneValues{2, 2, 2, 2}},
		{0xcd4, isa.LaneValues{}}, {0xcd5, isa.LaneValues{}},
		{0xcd6, isa.LaneValues{1, 1, 1, 1}}, {0xcd7, isa.LaneValues{2, 2, 2, 2}}, {0xcd8, isa.LaneValues{}},
		{0xcd9, isa.LaneValues{8, 8, 8, 8}}, {0xcda, isa.LaneValues{1, 1, 1, 1}}, {0xcdb, isa.LaneValues{1, 1, 1, 1}},
		{0xcdc, isa.LaneValues{3, 3, 3, 3}}, {0xcdd, isa.LaneValues{4, 4, 4, 4}}, {0xcde, isa.LaneValues{1, 1, 1, 1}},
		{0xce0, isa.LaneValues{2, 2, 2, 2}}, {0xce1, isa.LaneValues{0x240, 0x240, 0x240, 0x240}},
	} {
		sources[2].word = csrReadWord(test.address, 13)
		step, err = manager.Step(fake)
		got, _ := owners[2].ReadRegister(isa.Register{File: isa.Integer, Index: 13})
		if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired || got != test.want {
			t.Fatalf("canonical CTA CSR %#x=%v want=%v step=%+v err=%v", test.address, got, test.want, step, err)
		}
	}
}

func TestCTALocalMemorySharingIsolationRoutingAndAtomicFailures(t *testing.T) {
	ctas := core.NewCTAManager()
	cta0, err := ctas.Admit(ctaConfig(0, []uint8{0, 1}, 8, 65))
	if err != nil {
		t.Fatal(err)
	}
	cta1, err := ctas.Admit(ctaConfig(1, []uint8{2}, 4, 64))
	if err != nil {
		t.Fatal(err)
	}
	if cta0.LocalMemory.Size != 128 || cta1.LocalMemory.Address != isa.FrozenLocalMemBase {
		t.Fatalf("allocations cta0=%+v cta1=%+v", cta0.LocalMemory, cta1.LocalMemory)
	}
	global, err := memory.New(0x400)
	if err != nil {
		t.Fatal(err)
	}
	routes := make([]*core.CTAMemory, 3)
	for id := uint8(0); id < 3; id++ {
		routes[id], err = core.NewCTAMemory(ctas, id, global)
		if err != nil {
			t.Fatal(err)
		}
	}

	sharedAddress := cta0.LocalMemory.Address + 8
	if err := routes[0].Write(sharedAddress, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	shared := make([]byte, 4)
	if err := routes[1].Read(sharedAddress, shared); err != nil || !bytes.Equal(shared, []byte{1, 2, 3, 4}) {
		t.Fatalf("same-CTA LMEM=%v err=%v", shared, err)
	}
	isolated := make([]byte, 4)
	if err := routes[2].Read(sharedAddress, isolated); err != nil || !bytes.Equal(isolated, make([]byte, 4)) {
		t.Fatalf("same virtual address leaked CTA0 bytes into CTA1: bytes=%v err=%v", isolated, err)
	}
	privateAddress := cta1.LocalMemory.Address + 8
	if err := routes[2].Write(privateAddress, []byte{9, 8, 7, 6}); err != nil {
		t.Fatal(err)
	}
	stillShared := make([]byte, 4)
	if err := routes[0].Read(privateAddress, stillShared); err != nil || !bytes.Equal(stillShared, []byte{1, 2, 3, 4}) {
		t.Fatalf("same virtual address leaked CTA1 bytes into CTA0: bytes=%v err=%v", stillShared, err)
	}

	if err := routes[0].Write(0x20, []byte{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	globalBytes := make([]byte, 4)
	if err := global.Read(0x20, globalBytes); err != nil || !bytes.Equal(globalBytes, []byte{5, 6, 7, 8}) {
		t.Fatalf("global route=%v err=%v", globalBytes, err)
	}

	batchBase := cta0.LocalMemory.Address + 32
	if err := routes[0].WriteBatch([]uint32{batchBase, cta0.LocalMemory.Address + cta0.LocalMemory.Size}, [][]byte{{1, 1, 1, 1}, {2, 2, 2, 2}}); !errors.Is(err, core.ErrLocalMemoryRange) {
		t.Fatalf("invalid local batch error=%v", err)
	}
	unchanged := make([]byte, 4)
	if err := routes[1].Read(batchBase, unchanged); err != nil || !bytes.Equal(unchanged, make([]byte, 4)) {
		t.Fatalf("failed batch partially wrote LMEM=%v err=%v", unchanged, err)
	}
	if err := routes[0].WriteBatch([]uint32{0x24, batchBase}, [][]byte{{3, 3, 3, 3}, {4, 4, 4, 4}}); err != nil {
		t.Fatalf("mixed batch error=%v", err)
	}
	mixedGlobal, mixedLocal := make([]byte, 4), make([]byte, 4)
	_ = global.Read(0x24, mixedGlobal)
	_ = routes[0].Read(batchBase, mixedLocal)
	if !bytes.Equal(mixedGlobal, []byte{3, 3, 3, 3}) || !bytes.Equal(mixedLocal, []byte{4, 4, 4, 4}) {
		t.Fatalf("mixed batch global/local=%v/%v", mixedGlobal, mixedLocal)
	}
	failingGlobalBacking, _ := memory.New(0x400)
	failingRoute, err := core.NewCTAMemory(ctas, 0, &failingBatchMemory{Memory: failingGlobalBacking})
	if err != nil {
		t.Fatal(err)
	}
	localBeforeFailure := make([]byte, 4)
	_ = routes[0].Read(batchBase+8, localBeforeFailure)
	if err := failingRoute.WriteBatch([]uint32{0x28, batchBase + 8}, [][]byte{{5, 5, 5, 5}, {6, 6, 6, 6}}); err == nil {
		t.Fatal("injected mixed batch failure succeeded")
	}
	localAfterFailure := make([]byte, 4)
	_ = routes[0].Read(batchBase+8, localAfterFailure)
	if !bytes.Equal(localBeforeFailure, localAfterFailure) || !bytes.Equal(failingGlobalBacking.Snapshot(), make([]byte, 0x400)) {
		t.Fatal("failed mixed batch partially changed an owner")
	}
	if err := routes[0].Write(isa.FrozenLocalMemBase-1, []byte{1, 2}); !errors.Is(err, core.ErrMemoryCrossing) {
		t.Fatalf("lower window crossing error=%v", err)
	}
	if err := routes[0].Read(isa.FrozenLocalMemBase+isa.FrozenLocalMemSize-2, make([]byte, 4)); !errors.Is(err, core.ErrMemoryCrossing) {
		t.Fatalf("upper window crossing error=%v", err)
	}

	// Exercise the router through the real Warp memory completion path. Warp 0
	// stores and Warp 1 loads the same canonical CTA bytes.
	const sw = uint32(2<<20 | 1<<15 | 2<<12 | 0x23)
	const lw = uint32(1<<15 | 2<<12 | 3<<7 | 0x03)
	var program [4]byte
	binary.LittleEndian.PutUint32(program[:], sw)
	if err := global.Write(0x100, program[:]); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(program[:], lw)
	if err := global.Write(0x120, program[:]); err != nil {
		t.Fatal(err)
	}
	storeInitial := initial(0, true)
	storeInitial.ActiveMask = 1
	storeInitial.Lanes[0].GPR[1] = sharedAddress
	storeInitial.Lanes[0].GPR[2] = 0xdecafbad
	loadInitial := initial(1, true)
	loadInitial.ActiveMask = 1
	loadInitial.Lanes[0].GPR[1] = sharedAddress
	storeOwner, _ := state.NewWarp(storeInitial)
	loadOwner, _ := state.NewWarp(loadInitial)
	storeWarp, _ := warp.NewWithServices(storeOwner, global, routes[0])
	loadWarp, _ := warp.NewWithServices(loadOwner, global, routes[1])
	if result := storeWarp.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
		t.Fatalf("LMEM store result=%+v", result)
	}
	if result := loadWarp.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
		t.Fatalf("LMEM load result=%+v", result)
	}
	loaded, _ := loadOwner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if loaded[0] != 0xdecafbad {
		t.Fatalf("shared LMEM load=%#x", loaded[0])
	}

	// A single two-lane store may legitimately split between global and LMEM;
	// the routed WriteBatch coordinates both byte owners before State commits.
	mixedInitial := initial(0, true)
	mixedInitial.ActiveMask = 0b11
	mixedInitial.Lanes[0].GPR[1], mixedInitial.Lanes[1].GPR[1] = 0x30, cta0.LocalMemory.Address+64
	mixedInitial.Lanes[0].GPR[2], mixedInitial.Lanes[1].GPR[2] = 0xaabbccdd, 0x11223344
	mixedOwner, _ := state.NewWarp(mixedInitial)
	mixedWarp, _ := warp.NewWithServices(mixedOwner, global, routes[0])
	if result := mixedWarp.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
		t.Fatalf("mixed-space Warp store result=%+v", result)
	}
	globalWord, _ := global.Load32(0x30)
	localWordBytes := make([]byte, 4)
	_ = routes[0].Read(cta0.LocalMemory.Address+64, localWordBytes)
	if globalWord != 0xaabbccdd || binary.LittleEndian.Uint32(localWordBytes) != 0x11223344 {
		t.Fatalf("mixed-space Warp store global/local=%#x/%#x", globalWord, binary.LittleEndian.Uint32(localWordBytes))
	}

	// If the global half rejects the external commit, the already-prevalidated
	// local half and canonical WarpState both remain untouched.
	failingMixedInitial := initial(0, true)
	failingMixedInitial.ActiveMask = 0b11
	failingMixedInitial.Lanes[0].GPR[1], failingMixedInitial.Lanes[1].GPR[1] = 0x34, cta0.LocalMemory.Address+72
	failingMixedInitial.Lanes[0].GPR[2], failingMixedInitial.Lanes[1].GPR[2] = 0x55667788, 0x99aabbcc
	failingMixedOwner, _ := state.NewWarp(failingMixedInitial)
	failingMixedWarp, _ := warp.NewWithServices(failingMixedOwner, global, failingRoute)
	failingStateBefore, _ := failingMixedOwner.Snapshot()
	failingLocalBefore := make([]byte, 4)
	_ = routes[0].Read(cta0.LocalMemory.Address+72, failingLocalBefore)
	failedMixedResult := failingMixedWarp.Step(state.ReadContext{})
	failingStateAfter, _ := failingMixedOwner.Snapshot()
	failingLocalAfter := make([]byte, 4)
	_ = routes[0].Read(cta0.LocalMemory.Address+72, failingLocalAfter)
	if failedMixedResult.Outcome != warp.OutcomeFault || failingStateBefore != failingStateAfter ||
		!bytes.Equal(failingLocalBefore, failingLocalAfter) || !bytes.Equal(failingGlobalBacking.Snapshot(), make([]byte, 0x400)) {
		t.Fatalf("failed mixed Warp store result=%+v stateChanged=%t local=%v/%v", failedMixedResult, failingStateBefore != failingStateAfter, failingLocalBefore, failingLocalAfter)
	}

	// One invalid lane leaves both WarpState and the valid lane's LMEM bytes
	// untouched, preserving the existing all-or-nothing instruction contract.
	badInitial := initial(0, true)
	badInitial.ActiveMask = 0b11
	badInitial.Lanes[0].GPR[1] = cta0.LocalMemory.Address + 48
	badInitial.Lanes[1].GPR[1] = cta0.LocalMemory.Address + cta0.LocalMemory.Size
	badInitial.Lanes[0].GPR[2], badInitial.Lanes[1].GPR[2] = 0x11111111, 0x22222222
	badOwner, _ := state.NewWarp(badInitial)
	badWarp, _ := warp.NewWithServices(badOwner, global, routes[0])
	beforeState, _ := badOwner.Snapshot()
	beforeBytes := make([]byte, 4)
	_ = routes[0].Read(cta0.LocalMemory.Address+48, beforeBytes)
	result := badWarp.Step(state.ReadContext{})
	afterState, _ := badOwner.Snapshot()
	afterBytes := make([]byte, 4)
	_ = routes[0].Read(cta0.LocalMemory.Address+48, afterBytes)
	if result.Outcome != warp.OutcomeFault || beforeState != afterState || !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("invalid lane was not atomic result=%+v stateChanged=%t bytes=%v/%v", result, beforeState != afterState, beforeBytes, afterBytes)
	}
	if _, err := warp.NewWithServices(loadOwner, global, routes[0]); err == nil {
		t.Fatal("CTA memory bound to the wrong warp was accepted")
	}
}

func TestWSPAWNRejectsCrossCTAMembershipWithoutPartialActivation(t *testing.T) {
	wspawn, sources := customSources(t, "wspawn")
	var initials [isa.FrozenWarpCount]state.WarpInitial
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id == 3)
	}
	for lane := range initials[3].Lanes {
		initials[3].Lanes[lane].GPR[sources[0].Index] = 4
		initials[3].Lanes[lane].GPR[sources[1].Index] = 0x700
	}
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(ctaConfig(0, []uint8{3, 0, 1}, 12, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := ctas.Admit(ctaConfig(1, []uint8{2}, 4, 64)); err != nil {
		t.Fatal(err)
	}
	manager, owners, _ := makeCTAReadyCoreFromInitials(t, initials, [isa.FrozenWarpCount]uint32{0, 0, 0, wspawn}, ctas)
	var before [isa.FrozenWarpCount]state.WarpSnapshot
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		before[id], _ = owners[id].Snapshot()
	}
	step, err := manager.Step(state.ReadContext{})
	if err == nil || step.BlockReason != core.BlockFault {
		t.Fatalf("cross-CTA WSPAWN step=%+v err=%v", step, err)
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		after, _ := owners[id].Snapshot()
		if after != before[id] {
			t.Fatalf("cross-CTA WSPAWN partially changed warp %d", id)
		}
	}
}

func TestCTAMemberCompletionObservationAndMembershipAwareWSPAWNSuccess(t *testing.T) {
	t.Run("completion-observation", func(t *testing.T) {
		ctas := core.NewCTAManager()
		if _, err := ctas.Admit(ctaConfig(0, []uint8{0}, 4, 64)); err != nil {
			t.Fatal(err)
		}
		var initials [isa.FrozenWarpCount]state.WarpInitial
		for id := uint8(0); id < isa.FrozenWarpCount; id++ {
			initials[id] = initial(id, id == 0)
		}
		manager, _, _ := makeCTAReadyCoreFromInitials(t, initials,
			[isa.FrozenWarpCount]uint32{catalogWord(t, "tmc"), 0, 0, 0}, ctas)
		if step, err := manager.Step(state.ReadContext{}); err != nil || step.NextLifecycle != core.WarpFinished {
			t.Fatalf("completion step=%+v err=%v", step, err)
		}
		snapshot, err := ctas.Snapshot(0)
		if err != nil || len(snapshot.Members) != 1 || !snapshot.Members[0].Finished {
			t.Fatalf("CTA member completion=%+v err=%v", snapshot, err)
		}
	})

	t.Run("wspawn-success", func(t *testing.T) {
		wspawn, sources := customSources(t, "wspawn")
		var initials [isa.FrozenWarpCount]state.WarpInitial
		for id := uint8(0); id < isa.FrozenWarpCount; id++ {
			initials[id] = initial(id, id == 3)
		}
		for lane := range initials[3].Lanes {
			initials[3].Lanes[lane].GPR[sources[0].Index] = 4
			initials[3].Lanes[lane].GPR[sources[1].Index] = 0x700
		}
		ctas := core.NewCTAManager()
		if _, err := ctas.Admit(ctaConfig(0, []uint8{3, 0, 1, 2}, 16, 64)); err != nil {
			t.Fatal(err)
		}
		manager, _, _ := makeCTAReadyCoreFromInitials(t, initials, [isa.FrozenWarpCount]uint32{0, 0, 0, wspawn}, ctas)
		step, err := manager.Step(state.ReadContext{})
		if err != nil || step.WarpResult.Outcome != warp.OutcomeRetired {
			t.Fatalf("CTA-aware WSPAWN step=%+v err=%v", step, err)
		}
		for id := uint8(0); id < 3; id++ {
			slot, slotErr := manager.Slot(id)
			if slotErr != nil || slot.Lifecycle != core.WarpRunnable || !slot.Participated {
				t.Fatalf("CTA WSPAWN target %d slot=%+v err=%v", id, slot, slotErr)
			}
		}
	})
}
