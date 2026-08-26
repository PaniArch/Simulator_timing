package warp_test

import (
	"errors"
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/warp"
)

func TestRunBudgetBoundariesAndEarlyStops(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		owner := newState(t, initialWarp())
		source := &recordingSource{word: 0x00100093}
		result := executor(t, owner, source).Run(warp.RunOptions{StepBudget: 0})
		var budget *warp.BudgetExceededError
		if result.Outcome != warp.RunBudgetExceeded || result.Attempts != 0 || result.Retired != 0 || result.Last != nil ||
			!errors.As(result.Err, &budget) || budget.Budget != 0 || source.length != 0 {
			t.Fatalf("zero-budget result=%+v source=%+v", result, source)
		}
	})
	t.Run("exact-loop", func(t *testing.T) {
		owner := newState(t, initialWarp())
		m := newMemory(t)
		putWord(t, m, 0x100, 0x0000006f) // jal x0,0
		result := memoryExecutor(t, owner, m).Run(warp.RunOptions{StepBudget: 3})
		if result.Outcome != warp.RunBudgetExceeded || result.Attempts != 3 || result.Retired != 3 || result.Last == nil ||
			result.Last.PC != 0x100 || result.Last.NextPC != 0x100 {
			t.Fatalf("loop budget result=%+v", result)
		}
	})
	t.Run("fault-before-budget", func(t *testing.T) {
		owner := newState(t, initialWarp())
		m := newMemory(t)
		putWord(t, m, 0x100, 0x00100093)
		putWord(t, m, 0x104, 0xffffffff)
		result := memoryExecutor(t, owner, m).Run(warp.RunOptions{StepBudget: 9})
		if result.Outcome != warp.RunFault || result.Attempts != 2 || result.Retired != 1 || result.Last == nil ||
			result.Last.Fault.Kind != warp.FaultIllegalInstruction {
			t.Fatalf("early fault result=%+v", result)
		}
	})
	t.Run("trap", func(t *testing.T) {
		owner := newState(t, initialWarp())
		result := executor(t, owner, &recordingSource{word: 0x00000073}).Run(warp.RunOptions{StepBudget: 4})
		if result.Outcome != warp.RunTrap || result.Attempts != 1 || result.Retired != 0 || result.Last == nil || result.Last.NextPC != 0x180 {
			t.Fatalf("trap result=%+v", result)
		}
	})
	t.Run("deferred", func(t *testing.T) {
		owner := newState(t, initialWarp())
		result := executor(t, owner, &recordingSource{word: catalogWord(t, "wspawn")}).Run(warp.RunOptions{StepBudget: 4})
		if result.Outcome != warp.RunDeferred || result.Attempts != 1 || result.Retired != 0 {
			t.Fatalf("deferred result=%+v", result)
		}
	})
	t.Run("canonical-finished", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask, initial.Lifecycle = 0, state.WarpInactive
		owner := newState(t, initial)
		source := &recordingSource{word: 0x00100093}
		result := executor(t, owner, source).Run(warp.RunOptions{StepBudget: 4})
		if result.Outcome != warp.RunFinished || result.Attempts != 1 || result.Retired != 0 || result.Last == nil ||
			result.Last.Outcome != warp.OutcomeFinished || source.length != 0 {
			t.Fatalf("finished result=%+v source=%+v", result, source)
		}
	})
}

func floatLoadWord(immediate, rs1, rd uint32) uint32 {
	return (immediate&0xfff)<<20 | rs1<<15 | 2<<12 | rd<<7 | 0x07
}

func floatStoreWord(immediate, rs2, rs1 uint32) uint32 {
	return (immediate>>5)<<25 | rs2<<20 | rs1<<15 | 2<<12 | (immediate&0x1f)<<7 | 0x27
}

func TestRunCompleteInstructionStream(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[5] = 0x128
	initial.Lanes[0].GPR[10] = 0x300
	owner := newState(t, initial)
	m := newMemory(t)
	program := map[uint32]uint32{
		0x100: 0x00500093,               // addi x1,x0,5
		0x104: 0x00308113,               // addi x2,x1,3
		0x108: 0x00208463,               // beq x1,x2,+8 (not taken)
		0x10c: 0x00210463,               // beq x2,x2,+8 (taken)
		0x114: 0x008001ef,               // jal x3,+8
		0x11c: 0x00028267,               // jalr x4,0(x5)
		0x128: rType(1, 2, 1, 0, 6),     // mul x6,x1,x2
		0x12c: rType(7, 2, 1, 5, 7),     // czero.eqz x7,x1,x2
		0x130: storeWord(0, 2, 10, 2),   // sw x2,0(x10)
		0x134: loadWord(0, 10, 2, 8),    // lw x8,0(x10)
		0x138: 0x00140493,               // addi x9,x8,1
		0x13c: floatLoadWord(4, 10, 1),  // flw f1,4(x10)
		0x140: 0x00108153,               // fadd.s f2,f1,f1
		0x144: floatStoreWord(8, 2, 10), // fsw f2,8(x10)
		0x148: 0x340095f3,               // csrrw x11,mscratch,x1
		0x14c: 0xffffffff,               // deterministic stop
	}
	for pc, word := range program {
		putWord(t, m, pc, word)
	}
	if err := m.Store32(0x304, 0x3f800000); err != nil {
		t.Fatal(err)
	}
	result := memoryExecutor(t, owner, m).Run(warp.RunOptions{StepBudget: 100})
	if result.Outcome != warp.RunFault || result.Attempts != 16 || result.Retired != 15 || result.Last == nil ||
		result.Last.Fault.Kind != warp.FaultIllegalInstruction || result.Last.PC != 0x14c || result.Last.NextPC != 0x14c {
		t.Fatalf("stream result=%+v", result)
	}
	wantGPR := map[uint8]uint32{1: 5, 2: 8, 3: 0x118, 4: 0x120, 6: 40, 7: 5, 8: 8, 9: 9, 11: 0}
	for register, want := range wantGPR {
		values, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: register})
		if values[0] != want {
			t.Fatalf("x%d=%#x want=%#x", register, values[0], want)
		}
	}
	f1, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 1})
	f2, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 2})
	storedInteger, _ := m.Load32(0x300)
	storedFloat, _ := m.Load32(0x308)
	after, _ := owner.Snapshot()
	if f1[0] != 0x3f800000 || f2[0] != 0x40000000 || storedInteger != 8 || storedFloat != 0x40000000 ||
		after.TrapCSRs().MScratch != 5 || after.PC() != 0x14c {
		t.Fatalf("stream state f1=%#x f2=%#x memory=%#x/%#x csrs=%+v pc=%#x", f1[0], f2[0], storedInteger, storedFloat, after.TrapCSRs(), after.PC())
	}
}

func TestRunFetchAndMemoryFaultsStopWithoutPartialState(t *testing.T) {
	t.Run("fetch", func(t *testing.T) {
		initial := initialWarp()
		owner := newState(t, initial)
		m, err := memory.New(0x102)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := owner.Snapshot()
		executor, err := warp.New(owner, m)
		if err != nil {
			t.Fatal(err)
		}
		result := executor.Run(warp.RunOptions{StepBudget: 2})
		after, _ := owner.Snapshot()
		if result.Outcome != warp.RunFault || result.Attempts != 1 || result.Last.Fault.Kind != warp.FaultInstructionAccess || before != after {
			t.Fatalf("fetch fault result=%+v changed=%t", result, before != after)
		}
	})
	t.Run("memory", func(t *testing.T) {
		initial := initialWarp()
		initial.Lanes[0].GPR[1] = 0x400
		owner := newState(t, initial)
		m := newMemory(t)
		putWord(t, m, 0x100, loadWord(0, 1, 2, 3))
		before, _ := owner.Snapshot()
		result := memoryExecutor(t, owner, m).Run(warp.RunOptions{StepBudget: 2})
		after, _ := owner.Snapshot()
		if result.Outcome != warp.RunFault || result.Attempts != 1 || result.Last.Fault.Kind != warp.FaultArchitectural || before != after {
			t.Fatalf("memory fault result=%+v changed=%t", result, before != after)
		}
	})
}

func TestRunTraceIsCompleteOptionalAndObservational(t *testing.T) {
	makeExecutor := func(t *testing.T) (*warp.Warp, *state.WarpState) {
		t.Helper()
		owner := newState(t, initialWarp())
		m := newMemory(t)
		putWord(t, m, 0x100, 0x00500093)
		putWord(t, m, 0x104, 0x00308113)
		putWord(t, m, 0x108, 0xffffffff)
		return memoryExecutor(t, owner, m), owner
	}
	plainExecutor, plainOwner := makeExecutor(t)
	plain := plainExecutor.Run(warp.RunOptions{StepBudget: 8})

	tracedExecutor, tracedOwner := makeExecutor(t)
	var records []warp.TraceRecord
	contextCalls := 0
	traced := tracedExecutor.Run(warp.RunOptions{
		StepBudget: 8,
		Context: func(step uint64) state.ReadContext {
			if step != uint64(contextCalls) {
				t.Fatalf("context step=%d calls=%d", step, contextCalls)
			}
			contextCalls++
			return state.ReadContext{}
		},
		Trace: warp.TraceFunc(func(record warp.TraceRecord) {
			records = append(records, record)
		}),
	})
	if traced.Outcome != plain.Outcome || traced.Attempts != plain.Attempts || traced.Retired != plain.Retired || contextCalls != 3 || len(records) != 3 {
		t.Fatalf("plain=%+v traced=%+v contexts=%d records=%d", plain, traced, contextCalls, len(records))
	}
	for index, record := range records {
		if record.Step != uint64(index) || record.WarpID != 1 || record.PC != 0x100+uint32(4*index) || !record.RawValid || record.NextPC != record.PC+boolOffset(index < 2, 4) {
			t.Fatalf("trace[%d]=%+v", index, record)
		}
	}
	if records[0].Decoded == nil || records[0].Decoded.Name != "addi" || records[0].Effects == nil || records[0].Effects.Control == nil ||
		records[2].Decoded != nil || records[2].Effects != nil || records[2].Outcome != warp.OutcomeFault {
		t.Fatalf("trace contents=%+v", records)
	}

	mutatingExecutor, mutatingOwner := makeExecutor(t)
	mutating := mutatingExecutor.Run(warp.RunOptions{StepBudget: 8, Trace: warp.TraceFunc(func(record warp.TraceRecord) {
		if record.Decoded != nil {
			record.Decoded.Name = "corrupted"
		}
		if record.Effects != nil && record.Effects.Control != nil {
			record.Effects.Control.NextPC = 0
		}
	})})
	plainX2, _ := plainOwner.ReadRegister(isa.Register{File: isa.Integer, Index: 2})
	tracedX2, _ := tracedOwner.ReadRegister(isa.Register{File: isa.Integer, Index: 2})
	mutatingX2, _ := mutatingOwner.ReadRegister(isa.Register{File: isa.Integer, Index: 2})
	if mutating.Outcome != plain.Outcome || mutating.Attempts != plain.Attempts || mutating.Retired != plain.Retired ||
		mutating.Last.Decoded != nil || plainX2 != tracedX2 || plainX2 != mutatingX2 {
		t.Fatalf("trace altered execution plain=%+v traced=%+v mutating=%+v x2=%x/%x/%x", plain, traced, mutating, plainX2, tracedX2, mutatingX2)
	}
}

func TestRunTraceObservesMemoryIssueAndCompletion(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[1], initial.Lanes[0].GPR[2] = 0x200, 0x11223344
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, storeWord(0, 2, 1, 2))
	var records []warp.TraceRecord
	result := memoryExecutor(t, owner, m).Run(warp.RunOptions{
		StepBudget: 1,
		Trace:      warp.TraceFunc(func(record warp.TraceRecord) { records = append(records, record) }),
	})
	if result.Outcome != warp.RunBudgetExceeded || len(records) != 1 || records[0].IssuedEffects == nil ||
		len(records[0].IssuedEffects.MemoryRequests) != 1 || records[0].Effects == nil || records[0].Effects.Control == nil ||
		records[0].NextPC != 0x104 || records[0].Outcome != warp.OutcomeRetired {
		t.Fatalf("memory trace result=%+v records=%+v", result, records)
	}
	request := records[0].IssuedEffects.MemoryRequests[0]
	if request.Kind != isa.MemoryStore || request.Address != 0x200 || request.Width != 4 || request.ByteMask != 0xf || request.StoreData != 0x11223344 {
		t.Fatalf("memory issue=%+v", request)
	}
}

func boolOffset(condition bool, value uint32) uint32 {
	if condition {
		return value
	}
	return 0
}
