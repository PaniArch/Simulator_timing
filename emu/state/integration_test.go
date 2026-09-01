package state_test

import (
	"errors"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func executeSingle(t *testing.T, w *state.WarpState, word uint32, context state.ReadContext) state.SingleInstructionResult {
	t.Helper()
	result, err := w.ExecuteSingle(word, context)
	if err != nil {
		t.Fatalf("ExecuteSingle(%#08x): %v", word, err)
	}
	return result
}

func TestExecuteSinglePersistsIntegerStateAcrossALUBranchAndJump(t *testing.T) {
	w := newWarp(t)

	add := decoded(t, "add")
	first := executeSingle(t, w, add.Word, state.ReadContext{})
	if first.Decoded.Name != "add" || !first.Apply.Committed {
		t.Fatalf("ADD result=%+v", first)
	}

	// addi x4, x3, 5 consumes x3 written by the preceding independent call.
	const addiX4X3Five = uint32(0x00518213)
	second := executeSingle(t, w, addiX4X3Five, state.ReadContext{})
	if second.Decoded.Name != "addi" || !second.Apply.Committed {
		t.Fatalf("ADDI result=%+v", second)
	}
	x4, _ := w.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	if x4[0] != 8 || x4[2] != 0x40008 || x4[1] != 0x10004 || x4[3] != 0x30004 {
		t.Fatalf("persistent ADD→ADDI register state=%x", x4)
	}

	// beq x4, x4, +8, followed by jal x5, +8.
	branch := executeSingle(t, w, 0x00420463, state.ReadContext{})
	if branch.Decoded.Name != "beq" || !branch.Apply.Committed || snapshot(t, w).PC() != 0x110 {
		t.Fatalf("branch result=%+v pc=%#x", branch, snapshot(t, w).PC())
	}
	jump := executeSingle(t, w, 0x008002ef, state.ReadContext{})
	if jump.Decoded.Name != "jal" || !jump.Apply.Committed || snapshot(t, w).PC() != 0x118 {
		t.Fatalf("jump result=%+v pc=%#x", jump, snapshot(t, w).PC())
	}
	x5, _ := w.ReadRegister(isa.Register{File: isa.Integer, Index: 5})
	if x5[0] != 0x114 || x5[2] != 0x114 || x5[1] == 0x114 || x5[3] == 0x114 {
		t.Fatalf("JAL masked link state=%x", x5)
	}
}

func TestExecuteSingleRV32FUpdatesFPRFlagsAndPC(t *testing.T) {
	initial := baseInitial()
	initial.FCSR = 0
	for lane := range initial.Lanes {
		initial.Lanes[lane].FPR[1] = 0x3f800000 // 1.0
		initial.Lanes[lane].FPR[2] = 0x40400000 // 3.0
	}
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	op := decoded(t, "fdiv.s")
	result := executeSingle(t, w, op.Word, state.ReadContext{})
	if !result.Apply.Committed || result.Effects.FFlags == nil || result.Effects.FFlags.Accumulate&isa.FFlagInexact == 0 {
		t.Fatalf("FDIV result=%+v", result)
	}
	fpr, _ := w.ReadRegister(op.Destinations[0])
	if fpr[0] != 0x3eaaaaab || fpr[2] != 0x3eaaaaab {
		t.Fatalf("FDIV FPR=%x", fpr)
	}
	after := snapshot(t, w)
	if after.FCSR()&uint32(isa.FFlagInexact) == 0 || after.PC() != 0x104 {
		t.Fatalf("FDIV FCSR/PC=%#x/%#x", after.FCSR(), after.PC())
	}
}

func TestExecuteSingleTMCAndPredicateMasks(t *testing.T) {
	t.Run("tmc", func(t *testing.T) {
		initial := baseInitial()
		initial.Lanes[2].GPR[1] = 0b1010
		w, err := state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		result := executeSingle(t, w, decoded(t, "tmc").Word, state.ReadContext{})
		if !result.Apply.Committed || snapshot(t, w).ActiveMask() != 0b1010 {
			t.Fatalf("TMC result=%+v mask=%04b", result, snapshot(t, w).ActiveMask())
		}
	})

	t.Run("pred", func(t *testing.T) {
		initial := baseInitial()
		initial.ActiveMask = isa.AllLanes
		operation := decoded(t, "pred")
		selected, rejected := uint32(1), uint32(0)
		if operation.ConditionNegated {
			selected, rejected = rejected, selected
		}
		initial.Lanes[0].GPR[1], initial.Lanes[1].GPR[1] = selected, rejected
		initial.Lanes[2].GPR[1], initial.Lanes[3].GPR[1] = selected, rejected
		w, err := state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		result := executeSingle(t, w, operation.Word, state.ReadContext{})
		if !result.Apply.Committed || snapshot(t, w).ActiveMask() != 0b0101 {
			t.Fatalf("PRED result=%+v mask=%04b", result, snapshot(t, w).ActiveMask())
		}
	})
}

func TestExecuteSingleSplitAndTwoJoinCallsPersistDivergence(t *testing.T) {
	initial := baseInitial()
	initial.Divergence = state.DivergenceInitial{}
	initial.Lanes[0].GPR[1], initial.Lanes[2].GPR[1] = 1, 0
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}

	split := executeSingle(t, w, decoded(t, "split").Word, state.ReadContext{})
	if !split.Apply.Committed || snapshot(t, w).DivergenceWritePointer() != 1 || snapshot(t, w).ActiveMask() != 1 {
		t.Fatalf("SPLIT result=%+v state=%#v", split, snapshot(t, w))
	}
	joinOp := decoded(t, "join")
	if err := w.WriteRegister(joinOp.Sources[0], isa.AllLanes, isa.LaneValues{}); err != nil {
		t.Fatal(err)
	}
	mark := executeSingle(t, w, joinOp.Word, state.ReadContext{})
	marked, _ := snapshot(t, w).DivergenceRecord(0)
	if !mark.Apply.Committed || !marked.ElseVisited || snapshot(t, w).ActiveMask() != 0b0100 {
		t.Fatalf("JOIN mark result=%+v record=%+v", mark, marked)
	}
	pop := executeSingle(t, w, joinOp.Word, state.ReadContext{})
	after := snapshot(t, w)
	popped, _ := after.DivergenceRecord(0)
	if !pop.Apply.Committed || popped.Valid || after.DivergenceWritePointer() != 0 || after.ActiveMask() != 0b0101 {
		t.Fatalf("JOIN pop result=%+v record=%+v state=%#v", pop, popped, after)
	}
}

func TestExecuteSingleCSRRMWTrapEntryAndReturn(t *testing.T) {
	initial := baseInitial()
	initial.Lanes[0].GPR[1] = 0xabcdef01
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}

	// csrrw x3, mstatus, x1
	csr := executeSingle(t, w, 0x300091f3, state.ReadContext{ActiveWarps: 0b0100})
	if !csr.Apply.Committed || snapshot(t, w).TrapCSRs().MStatus != 0xabcdef01 || snapshot(t, w).PC() != 0x104 {
		t.Fatalf("CSR result=%+v state=%#v", csr, snapshot(t, w))
	}
	entry := executeSingle(t, w, decoded(t, "ecall").Word, state.ReadContext{ActiveWarps: 0b0100})
	entryState := snapshot(t, w)
	if !entry.Apply.Committed || entryState.PC() != 0x200 || entryState.TrapCSRs().MEPC != 0x104 || entryState.SavedThreadMask() != 0b0101 {
		t.Fatalf("trap entry result=%+v state=%#v", entry, entryState)
	}
	ret := executeSingle(t, w, decoded(t, "mret").Word, state.ReadContext{ActiveWarps: 0b0100})
	returnState := snapshot(t, w)
	if !ret.Apply.Committed || returnState.PC() != 0x104 || returnState.ActiveMask() != 0b0101 {
		t.Fatalf("trap return result=%+v state=%#v", ret, returnState)
	}
}

func memoryWarp(t *testing.T) (*state.WarpState, isa.Decoded, state.ReadContext) {
	t.Helper()
	initial := baseInitial()
	initial.Lanes[0].GPR[1] = 0x1000
	initial.Lanes[2].GPR[1] = 0x1004
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	op, err := isa.Decode(0x0000a183) // lw x3, 0(x1)
	if err != nil {
		t.Fatal(err)
	}
	bounds := isa.AddressBounds{Base: 0x1000, Size: 8}
	return w, op, state.ReadContext{Bounds: &bounds}
}

func issueMemory(t *testing.T, w *state.WarpState, op isa.Decoded, context state.ReadContext) state.SingleInstructionResult {
	t.Helper()
	before := snapshot(t, w)
	issued := executeSingle(t, w, op.Word, context)
	if !issued.Apply.AwaitingExternal || issued.Apply.Pending == nil || len(issued.Apply.Forwarded.MemoryRequests) != 2 {
		t.Fatalf("memory issue result=%+v", issued)
	}
	requireUnchanged(t, w, before)
	if err := issued.Apply.Pending.CommitAfterExternal(); err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestMemoryCompletionAppliesRegisterAndPCOrForwardsFaultAtomically(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		w, op, context := memoryWarp(t)
		issued := issueMemory(t, w, op, context)
		requests := issued.Apply.Forwarded.MemoryRequests
		responses := []isa.MemoryResponse{
			{Request: requests[0], Data: 0xdeadbeef},
			{Request: requests[1], Data: 0xcafebabe},
		}
		completed, err := w.CompleteMemoryAndApply(op, 0b0101, responses)
		if err != nil || !completed.Apply.Committed {
			t.Fatalf("memory completion=%+v err=%v", completed, err)
		}
		gpr, _ := w.ReadRegister(op.Destinations[0])
		if gpr[0] != 0xdeadbeef || gpr[2] != 0xcafebabe || snapshot(t, w).PC() != 0x104 {
			t.Fatalf("load register/PC=%x/%#x", gpr, snapshot(t, w).PC())
		}
	})

	t.Run("fault", func(t *testing.T) {
		w, op, context := memoryWarp(t)
		issued := issueMemory(t, w, op, context)
		before := snapshot(t, w)
		requests := issued.Apply.Forwarded.MemoryRequests
		responses := []isa.MemoryResponse{
			{Request: requests[0], Data: 0xdeadbeef},
			{Request: requests[1], Fault: isa.FaultLoadAccess, Reason: isa.FaultReasonMemoryService},
		}
		completed, err := w.CompleteMemoryAndApply(op, 0b0101, responses)
		if err != nil || !completed.Apply.AwaitingExternal || len(completed.Apply.Forwarded.Faults) != 1 {
			t.Fatalf("fault completion=%+v err=%v", completed, err)
		}
		requireUnchanged(t, w, before)
	})
}

func TestExecuteSinglePreservesFutureOwnerEffectsWithoutLocalMutation(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*state.WarpState)
		word    uint32
		check   func(state.ApplyResult) bool
	}{
		{
			name: "ordering", word: decoded(t, "fence").Word,
			check: func(apply state.ApplyResult) bool { return apply.Forwarded.Ordering != nil },
		},
		{
			name: "wspawn", word: decoded(t, "wspawn").Word,
			prepare: func(w *state.WarpState) {
				_ = w.WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 1<<2, isa.LaneValues{0, 0, 4})
				_ = w.WriteRegister(isa.Register{File: isa.Integer, Index: 2}, 1<<2, isa.LaneValues{0, 0, 0x400})
			},
			check: func(apply state.ApplyResult) bool { return apply.Forwarded.WarpSpawn != nil },
		},
		{
			name: "barrier", word: decoded(t, "bar").Word,
			check: func(apply state.ApplyResult) bool {
				return len(apply.Forwarded.Barriers) == 1 && len(apply.Forwarded.WarpDrains) == 1
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := newWarp(t)
			if test.prepare != nil {
				test.prepare(w)
			}
			before := snapshot(t, w)
			result := executeSingle(t, w, test.word, state.ReadContext{})
			if !result.Apply.AwaitingExternal || result.Apply.Pending == nil || !test.check(result.Apply) {
				t.Fatalf("future-owner result=%+v", result)
			}
			requireUnchanged(t, w, before)
		})
	}
}

func TestExecuteSingleDecodeFailureHasNoStateAndOwnersRemainIndependent(t *testing.T) {
	w := newWarp(t)
	before := snapshot(t, w)
	_, err := w.ExecuteSingle(0, state.ReadContext{})
	var illegal *isa.IllegalInstructionError
	if !errors.As(err, &illegal) {
		t.Fatalf("illegal decode error=%T %v", err, err)
	}
	requireUnchanged(t, w, before)

	firstInitial := baseInitial()
	secondInitial := baseInitial()
	firstInitial.Lanes[0].GPR[1], firstInitial.Lanes[0].GPR[2] = 1, 2
	secondInitial.Lanes[0].GPR[1], secondInitial.Lanes[0].GPR[2] = 10, 20
	first, _ := state.NewWarp(firstInitial)
	second, _ := state.NewWarp(secondInitial)
	word := decoded(t, "add").Word
	executeSingle(t, first, word, state.ReadContext{})
	executeSingle(t, second, word, state.ReadContext{})
	firstResult, _ := first.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	secondResult, _ := second.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if firstResult[0] != 3 || secondResult[0] != 30 || reflect.DeepEqual(firstResult, secondResult) {
		t.Fatalf("stateless ISA owner isolation first=%x second=%x", firstResult, secondResult)
	}
}
