package core_test

import (
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

func TestCoreRunDistinctStopOutcomes(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{catalogWord(t, "tmc"), 0, 0, 0},
		)
		result := manager.Run(core.RunOptions{StepBudget: 4})
		complete, err := manager.Complete()
		if result.Outcome != core.RunComplete || result.Attempts != 1 || result.Retired != 1 ||
			result.Last == nil || result.Last.NextLifecycle != core.WarpFinished || !complete || err != nil {
			t.Fatalf("complete result=%+v complete=%t err=%v", result, complete, err)
		}
	})

	t.Run("budget", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{0x0000006f, 0, 0, 0}, // jal x0,0
		)
		result := manager.Run(core.RunOptions{StepBudget: 3})
		var budget *core.BudgetExceededError
		if result.Outcome != core.RunBudgetExceeded || result.Attempts != 3 || result.Retired != 3 ||
			!errors.As(result.Err, &budget) || budget.Budget != 3 {
			t.Fatalf("budget result=%+v", result)
		}
	})

	t.Run("deferred-then-blocked", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{catalogWord(t, "wsync"), 0, 0, 0},
		)
		if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(uint8) (bool, error) { return true, nil })); err != nil {
			t.Fatal(err)
		}
		deferred := manager.Run(core.RunOptions{StepBudget: 4})
		var deferredErr *core.DeferredError
		if deferred.Outcome != core.RunDeferred || deferred.Attempts != 1 || deferred.Retired != 0 ||
			deferred.Last == nil || deferred.Last.BlockReason != core.BlockPendingWork ||
			!errors.As(deferred.Err, &deferredErr) || deferredErr.WarpID != 0 {
			t.Fatalf("deferred result=%+v", deferred)
		}
		blocked := manager.Run(core.RunOptions{StepBudget: 4})
		var noRunnable *core.NoRunnableError
		if blocked.Outcome != core.RunBlocked || blocked.Attempts != 0 || blocked.Last != nil ||
			!errors.As(blocked.Err, &noRunnable) {
			t.Fatalf("blocked result=%+v", blocked)
		}
		complete, _ := manager.Complete()
		if complete {
			t.Fatal("blocked WSYNC was reported complete")
		}
	})

	t.Run("trap", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{0x00000073, 0, 0, 0}, // ecall
		)
		result := manager.Run(core.RunOptions{StepBudget: 4})
		if result.Outcome != core.RunTrap || result.Attempts != 1 || result.Retired != 0 ||
			result.Last == nil || result.Last.WarpResult.Outcome != warp.OutcomeTrap {
			t.Fatalf("trap result=%+v", result)
		}
	})

	t.Run("fault", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{0xffffffff, 0, 0, 0},
		)
		result := manager.Run(core.RunOptions{StepBudget: 4})
		if result.Outcome != core.RunFault || result.Attempts != 1 || result.Retired != 0 ||
			result.Last == nil || result.Last.WarpResult.Fault == nil ||
			result.Last.WarpResult.Fault.Kind != warp.FaultIllegalInstruction {
			t.Fatalf("fault result=%+v", result)
		}
	})

	t.Run("zero-budget", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{0x0000006f, 0, 0, 0},
		)
		result := manager.Run(core.RunOptions{})
		if result.Outcome != core.RunBudgetExceeded || result.Attempts != 0 || result.Last != nil {
			t.Fatalf("zero-budget result=%+v", result)
		}
	})

	t.Run("pending-provider-fault", func(t *testing.T) {
		manager, _, _ := makeCore(t,
			[isa.FrozenWarpCount]bool{true, false, false, false},
			[isa.FrozenWarpCount]uint32{catalogWord(t, "wsync"), 0, 0, 0},
		)
		providerErr := errors.New("pending owner unavailable")
		if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(uint8) (bool, error) { return false, providerErr })); err != nil {
			t.Fatal(err)
		}
		result := manager.Run(core.RunOptions{StepBudget: 4})
		if result.Outcome != core.RunFault || result.Attempts != 0 || result.Last != nil || !errors.Is(result.Err, providerErr) {
			t.Fatalf("provider fault result=%+v", result)
		}
	})
}

func TestCoreRunTraceIsSelectedOrderedAndDetached(t *testing.T) {
	manager, owners, _ := makeCore(t,
		[isa.FrozenWarpCount]bool{true, true, false, false},
		[isa.FrozenWarpCount]uint32{0x0000006f, 0x0000006f, 0, 0},
	)
	var selected []uint8
	result := manager.Run(core.RunOptions{
		StepBudget: 4,
		Trace: core.TraceFunc(func(record core.TraceRecord) {
			selected = append(selected, record.WarpID)
			if record.CoreStep != uint64(len(selected)-1) || record.Lifecycle != core.WarpRunnable ||
				record.NextLifecycle != core.WarpRunnable || record.WarpResult.Decoded == nil ||
				record.WarpResult.Decoded.Name != "jal" {
				t.Fatalf("trace record=%+v", record)
			}
			record.WarpID = 3
			record.Lifecycle = core.WarpFinished
			record.WarpResult.Decoded.Name = "mutated"
			record.WarpResult.Decoded.Sources = append(record.WarpResult.Decoded.Sources, isa.Register{File: isa.Float, Index: 31})
			if record.WarpResult.Effects != nil && record.WarpResult.Effects.Control != nil {
				record.WarpResult.Effects.Control.NextPC = 0xdeadbeec
			}
		}),
	})
	if result.Outcome != core.RunBudgetExceeded || !reflect.DeepEqual(selected, []uint8{0, 1, 0, 1}) ||
		result.Last == nil || result.Last.WarpID != 1 || result.Last.WarpResult.Decoded.Name != "jal" ||
		result.Last.WarpResult.Effects.Control.NextPC != result.Last.WarpResult.PC {
		t.Fatalf("trace-mutated run result=%+v selected=%v", result, selected)
	}
	for id := uint8(0); id < 2; id++ {
		after, _ := owners[id].Snapshot()
		if after.PC() != 0x100+uint32(id)*0x20 || after.Lifecycle() != state.WarpRunning {
			t.Fatalf("trace changed warp %d state=%+v", id, after)
		}
	}
}

func setInstructionField(word uint32, shift uint, value uint32) uint32 {
	word &^= 0x1f << shift
	return word | value<<shift
}

func addiWord(rd, rs1 uint32, immediate int32) uint32 {
	return (uint32(immediate)&0xfff)<<20 | rs1<<15 | rd<<7 | 0x13
}

func storeWordForCore(rs2, rs1 uint32) uint32 {
	return rs2<<20 | rs1<<15 | 2<<12 | 0x23
}

func writeProgramWord(t *testing.T, owner *memory.Memory, address, word uint32) {
	t.Helper()
	var bytes [4]byte
	binary.LittleEndian.PutUint32(bytes[:], word)
	if err := owner.Write(address, bytes[:]); err != nil {
		t.Fatal(err)
	}
}

type multiWarpScenario struct {
	firstOutcome   core.RunOutcome
	secondOutcome  core.RunOutcome
	firstAttempts  uint64
	secondAttempts uint64
	firstLastName  string
	states         [isa.FrozenWarpCount]state.WarpSnapshot
	memory         []byte
	trace          []core.TraceRecord
}

func runHandBuiltMultiWarp(t *testing.T, mutateTrace bool) multiWarpScenario {
	t.Helper()
	wspawn, wspawnSources := customSources(t, "wspawn")
	tmcAll := setInstructionField(catalogWord(t, "tmc"), 15, 20)
	tmcZero := setInstructionField(catalogWord(t, "tmc"), 15, 21)
	split := setInstructionField(catalogWord(t, "split"), 7, 23)
	split = setInstructionField(split, 15, 2)
	split &^= 1 << 20
	join := setInstructionField(catalogWord(t, "join"), 15, 23)
	wsync := catalogWord(t, "wsync")

	memoryOwner, err := memory.New(0x900)
	if err != nil {
		t.Fatal(err)
	}
	program := map[uint32]uint32{
		0x100: wspawn,
		0x104: tmcZero,
		0x200: tmcAll,
		0x204: split,
		0x208: addiWord(6, 6, 1),
		0x20c: join,
		0x210: addiWord(5, 5, 1),
		0x214: storeWordForCore(11, 10),
		0x218: wsync,
		0x21c: tmcZero,
	}
	for address, word := range program {
		writeProgramWord(t, memoryOwner, address, word)
	}

	var owners [isa.FrozenWarpCount]*state.WarpState
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initial := initial(id, id == 3)
		if id == 3 {
			initial.PC = 0x100
			for lane := range initial.Lanes {
				initial.Lanes[lane].GPR[wspawnSources[0].Index] = 3
				initial.Lanes[lane].GPR[wspawnSources[1].Index] = 0x200
				initial.Lanes[lane].GPR[21] = 0
			}
		} else {
			initial.PC = 0x300 + uint32(id)*0x20
			initial.TrapCSRs = state.TrapCSRState{
				MStatus: 0x10 + uint32(id), MTVec: 0x400 + uint32(id)*4,
				MScratch: 0x500 + uint32(id), MEPC: 0x600 + uint32(id)*4,
			}
			for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
				initial.Lanes[lane].GPR[20] = uint32(isa.AllLanes)
				initial.Lanes[lane].GPR[21] = 0
				if lane == 0 || lane == 2 {
					initial.Lanes[lane].GPR[2] = 1
				}
				initial.Lanes[lane].GPR[5] = uint32(id)*10 + uint32(lane)
				initial.Lanes[lane].GPR[6] = uint32(id)*100 + uint32(lane)
				initial.Lanes[lane].GPR[10] = 0x500 + uint32(id)*0x40 + uint32(lane)*4
				initial.Lanes[lane].GPR[11] = 0xa0000000 | uint32(id)<<8 | uint32(lane)
				initial.Lanes[lane].FPR[3] = 0x3f800000 + uint32(id)<<8 + uint32(lane)
			}
		}
		owners[id], err = state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		executors[id], err = warp.NewWithMemory(owners[id], memoryOwner)
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := core.New(executors)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(warpID uint8) (bool, error) {
		return warpID == 1 && !released, nil
	})); err != nil {
		t.Fatal(err)
	}

	var records []core.TraceRecord
	sink := core.TraceFunc(func(record core.TraceRecord) {
		if mutateTrace {
			record.WarpID = 3
			record.Lifecycle = core.WarpFinished
			record.WarpResult.NextPC = 0xdeadbeec
			if record.WarpResult.Decoded != nil {
				record.WarpResult.Decoded.Name = "mutated"
			}
			if record.WarpResult.Effects != nil {
				if len(record.WarpResult.Effects.MemoryRequests) != 0 {
					record.WarpResult.Effects.MemoryRequests[0].Address = 0
				}
				if record.WarpResult.Effects.Control != nil {
					record.WarpResult.Effects.Control.NextPC = 0
				}
			}
			return
		}
		records = append(records, record)
	})
	first := manager.Run(core.RunOptions{StepBudget: 100, Trace: sink})
	if first.Last == nil || first.Last.WarpResult.Decoded == nil {
		t.Fatalf("first run lacks last decoded result: %+v", first)
	}
	firstLastName := first.Last.WarpResult.Decoded.Name
	released = true
	second := manager.Run(core.RunOptions{StepBudget: 100, Trace: sink})
	complete, err := manager.Complete()
	if err != nil || !complete {
		t.Fatalf("scenario complete=%t err=%v runs=%+v/%+v", complete, err, first, second)
	}

	result := multiWarpScenario{
		firstOutcome: first.Outcome, secondOutcome: second.Outcome,
		firstAttempts: first.Attempts, secondAttempts: second.Attempts,
		firstLastName: firstLastName, trace: records,
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		result.states[id], _ = owners[id].Snapshot()
	}
	result.memory, err = memoryOwner.ReadBytes(0, memoryOwner.Size())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHandBuiltMultiWarpRunCompletionAndDeterministicTrace(t *testing.T) {
	first := runHandBuiltMultiWarp(t, false)
	second := runHandBuiltMultiWarp(t, false)
	if first.firstOutcome != core.RunDeferred || first.secondOutcome != core.RunComplete ||
		first.firstLastName != "wsync" || first.firstAttempts == 0 || first.secondAttempts == 0 {
		t.Fatalf("scenario outcomes=%s/%s attempts=%d/%d last=%s", first.firstOutcome, first.secondOutcome, first.firstAttempts, first.secondAttempts, first.firstLastName)
	}
	if !reflect.DeepEqual(first.trace, second.trace) {
		t.Fatal("identical Multi-Warp runs produced different scheduler trace")
	}
	wantPrefix := []uint8{3, 0, 1, 2, 3}
	if len(first.trace) < len(wantPrefix) {
		t.Fatalf("trace too short: %d", len(first.trace))
	}
	for index, want := range wantPrefix {
		if first.trace[index].WarpID != want {
			t.Fatalf("trace prefix[%d]=%d want=%d", index, first.trace[index].WarpID, want)
		}
	}
	if first.trace[0].WarpResult.Effects == nil || first.trace[0].WarpResult.Effects.WarpSpawn == nil ||
		first.trace[0].WarpResult.Effects.WarpSpawn.Targets != 0b0111 {
		t.Fatalf("first trace is not WSPAWN activation: %+v", first.trace[0])
	}
	foundEarlyFinish, foundPendingSync := false, false
	for _, record := range first.trace {
		if record.WarpID == 3 && record.NextLifecycle == core.WarpFinished {
			foundEarlyFinish = true
		}
		if record.WarpID == 1 && record.WarpResult.Decoded != nil && record.WarpResult.Decoded.Name == "wsync" &&
			record.NextLifecycle == core.WarpBlocked && record.BlockReason == core.BlockPendingWork {
			foundPendingSync = true
		}
	}
	if !foundEarlyFinish || !foundPendingSync {
		t.Fatalf("trace lacks early finish=%t or pending WSYNC=%t", foundEarlyFinish, foundPendingSync)
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		snapshot := first.states[id]
		if snapshot.Lifecycle() != state.WarpInactive || snapshot.ActiveMask() != 0 {
			t.Fatalf("warp %d did not finish: lifecycle=%d mask=%#x", id, snapshot.Lifecycle(), snapshot.ActiveMask())
		}
		if id < 3 {
			if snapshot.PC() != 0x220 || snapshot.DivergenceWritePointer() != 0 {
				t.Fatalf("target %d PC/divergence=%#x/%d", id, snapshot.PC(), snapshot.DivergenceWritePointer())
			}
			gpr5, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 5})
			gpr6, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 6})
			for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
				if gpr5[lane] != uint32(id)*10+uint32(lane)+1 || gpr6[lane] != uint32(id)*100+uint32(lane)+1 {
					t.Fatalf("target %d lane %d isolation x5/x6=%d/%d", id, lane, gpr5[lane], gpr6[lane])
				}
				address := 0x500 + uint32(id)*0x40 + uint32(lane)*4
				value := binary.LittleEndian.Uint32(first.memory[address : address+4])
				want := uint32(0xa0000000) | uint32(id)<<8 | uint32(lane)
				if value != want {
					t.Fatalf("target %d lane %d memory=%#x want=%#x", id, lane, value, want)
				}
			}
		}
	}

	mutated := runHandBuiltMultiWarp(t, true)
	if mutated.firstOutcome != first.firstOutcome || mutated.secondOutcome != first.secondOutcome ||
		mutated.firstAttempts != first.firstAttempts || mutated.secondAttempts != first.secondAttempts ||
		mutated.firstLastName != first.firstLastName || mutated.states != first.states ||
		!reflect.DeepEqual(mutated.memory, first.memory) {
		t.Fatal("trace mutation changed Core result, WarpState, or memory")
	}
}
