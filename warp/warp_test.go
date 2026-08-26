package warp_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/warp"
)

func initialWarp() state.WarpInitial {
	lanes := make([]state.LaneInitial, isa.FrozenLaneCount)
	for lane := range lanes {
		lanes[lane].ID = uint8(lane)
	}
	return state.WarpInitial{
		Topology: state.FrozenTopology(), WarpID: 1, PC: 0x100,
		ActiveMask: 1, Lifecycle: state.WarpRunning,
		TrapCSRs: state.TrapCSRState{MTVec: 0x180},
		Lanes:    lanes,
	}
}

func newState(t *testing.T, initial state.WarpInitial) *state.WarpState {
	t.Helper()
	owner, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func newMemory(t *testing.T) *memory.Memory {
	t.Helper()
	m, err := memory.New(0x400)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func putWord(t *testing.T, m *memory.Memory, address, word uint32) {
	t.Helper()
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], word)
	if err := m.Write(address, data[:]); err != nil {
		t.Fatal(err)
	}
}

func executor(t *testing.T, owner *state.WarpState, source warp.InstructionSource) *warp.Warp {
	t.Helper()
	executor, err := warp.New(owner, source)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func requireOutcome(t *testing.T, result warp.Result, outcome warp.Outcome) {
	t.Helper()
	if result.Outcome != outcome {
		t.Fatalf("outcome=%s want=%s result=%+v", result.Outcome, outcome, result)
	}
}

type recordingSource struct {
	word    uint32
	address uint32
	length  int
	err     error
}

func (s *recordingSource) Read(address uint32, dst []byte) error {
	s.address, s.length = address, len(dst)
	if s.err != nil {
		return s.err
	}
	binary.LittleEndian.PutUint32(dst, s.word)
	return nil
}

func TestStepFetchesExactlyOneLittleEndianWordAndUsesCanonicalPC(t *testing.T) {
	owner := newState(t, initialWarp())
	source := &recordingSource{word: 0x00500093} // addi x1,x0,5
	result := executor(t, owner, source).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeRetired)
	if source.address != 0x100 || source.length != 4 || !result.RawValid || result.Raw != source.word ||
		result.PC != 0x100 || result.NextPC != 0x104 || result.Decoded == nil || result.Decoded.Name != "addi" {
		t.Fatalf("fetch/result mismatch: source=%+v result=%+v", source, result)
	}
	x1, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	if x1[0] != 5 {
		t.Fatalf("x1=%#x", x1[0])
	}
}

func TestStepSequentialDependenciesBranchesAndJumps(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[5] = 0x140
	owner := newState(t, initial)
	m := newMemory(t)
	program := map[uint32]uint32{
		0x100: 0x00500093, // addi x1,x0,5
		0x104: 0x00308113, // addi x2,x1,3
		0x108: 0x00208463, // beq x1,x2,+8 (not taken)
		0x10c: 0x00210463, // beq x2,x2,+8 (taken)
		0x114: 0x008001ef, // jal x3,+8
		0x11c: 0x00028267, // jalr x4,0(x5)
	}
	for pc, word := range program {
		putWord(t, m, pc, word)
	}
	w := executor(t, owner, m)
	wantPC := []uint32{0x104, 0x108, 0x10c, 0x114, 0x11c, 0x140}
	for step, want := range wantPC {
		result := w.Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		if result.NextPC != want {
			t.Fatalf("step %d next PC=%#x want=%#x", step, result.NextPC, want)
		}
	}
	x2, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 2})
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	x4, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	if x2[0] != 8 || x3[0] != 0x118 || x4[0] != 0x120 {
		t.Fatalf("dependency/link registers x2=%#x x3=%#x x4=%#x", x2[0], x3[0], x4[0])
	}
}

func rType(funct7, rs2, rs1, funct3, rd uint32) uint32 {
	return funct7<<25 | rs2<<20 | rs1<<15 | funct3<<12 | rd<<7 | 0x33
}

func TestStepRetiresRV32MZicondFloatAndWarpCSR(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[1] = 6
	initial.Lanes[0].GPR[2] = 7
	initial.Lanes[0].FPR[1] = 0x3f800000
	initial.Lanes[0].FPR[2] = 0x40000000
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, rType(1, 2, 1, 0, 3)) // mul x3,x1,x2
	putWord(t, m, 0x104, rType(7, 2, 1, 5, 4)) // czero.eqz x4,x1,x2
	putWord(t, m, 0x108, 0x002081d3)           // fadd.s f3,f1,f2,rne
	putWord(t, m, 0x10c, 0x340092f3)           // csrrw x5,mscratch,x1
	w := executor(t, owner, m)
	for range 4 {
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	}
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	x4, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	f3, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 3})
	x5, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 5})
	after, _ := owner.Snapshot()
	if x3[0] != 42 || x4[0] != 6 || f3[0] != 0x40400000 || x5[0] != 0 ||
		after.TrapCSRs().MScratch != 6 || after.PC() != 0x110 {
		t.Fatalf("M/Zicond/FP/CSR state x3=%#x x4=%#x f3=%#x x5=%#x snapshot=%+v", x3[0], x4[0], f3[0], x5[0], after)
	}
}

func TestStepCommitsTrapEntryAndReturn(t *testing.T) {
	owner := newState(t, initialWarp())
	m := newMemory(t)
	putWord(t, m, 0x100, 0x00000073) // ecall
	putWord(t, m, 0x180, 0x30200073) // mret
	w := executor(t, owner, m)
	entry := w.Step(state.ReadContext{})
	requireOutcome(t, entry, warp.OutcomeTrap)
	if entry.NextPC != 0x180 {
		t.Fatalf("ECALL next PC=%#x", entry.NextPC)
	}
	trap := w.Step(state.ReadContext{})
	requireOutcome(t, trap, warp.OutcomeTrap)
	after, _ := owner.Snapshot()
	if trap.NextPC != 0x100 || after.TrapCSRs().MEPC != 0x100 || after.TrapCSRs().MCause != isa.TrapCauseEnvironmentCallMMode {
		t.Fatalf("trap return result=%+v CSRs=%+v", trap, after.TrapCSRs())
	}
}

func TestStepFaultsAndFinishedNeverMutateCanonicalState(t *testing.T) {
	t.Run("fetch", func(t *testing.T) {
		owner := newState(t, initialWarp())
		before, _ := owner.Snapshot()
		result := executor(t, owner, &recordingSource{err: errors.New("bus unavailable")}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultInstructionAccess || before != after {
			t.Fatalf("result=%+v state changed=%t", result, before != after)
		}
	})
	t.Run("illegal", func(t *testing.T) {
		owner := newState(t, initialWarp())
		before, _ := owner.Snapshot()
		result := executor(t, owner, &recordingSource{word: 0xffffffff}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultIllegalInstruction || before != after {
			t.Fatalf("result=%+v state changed=%t", result, before != after)
		}
	})
	t.Run("view", func(t *testing.T) {
		owner := newState(t, initialWarp())
		before, _ := owner.Snapshot()
		result := executor(t, owner, &recordingSource{word: 0x00100093}).Step(state.ReadContext{CoreID: isa.FrozenCoreCount})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultView || before != after {
			t.Fatalf("result=%+v state changed=%t", result, before != after)
		}
	})
	t.Run("multi-lane", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 3
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x00100093}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		x1, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
		if x1 != (isa.LaneValues{1, 1, 0, 0}) || result.NextPC != 0x104 {
			t.Fatalf("multi-lane result=%+v x1=%#x", result, x1)
		}
	})
	t.Run("inactive", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask, initial.Lifecycle = 0, state.WarpInactive
		owner := newState(t, initial)
		source := &recordingSource{word: 0x00100093}
		result := executor(t, owner, source).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFinished)
		if source.length != 0 {
			t.Fatalf("inactive warp fetched %d bytes", source.length)
		}
	})
}

func TestStepExecutesFullAndPartialMasksWithoutInactiveWrites(t *testing.T) {
	t.Run("integer-full-mask", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = isa.AllLanes
		for lane := range initial.Lanes {
			initial.Lanes[lane].GPR[1] = uint32(lane + 1)
		}
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x00508193}).Step(state.ReadContext{}) // addi x3,x1,5
		requireOutcome(t, result, warp.OutcomeRetired)
		x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
		if x3 != (isa.LaneValues{6, 7, 8, 9}) || result.Effects.RegisterWrites[0].Mask != isa.AllLanes {
			t.Fatalf("full-mask result=%+v x3=%#x", result, x3)
		}
	})

	t.Run("integer-partial-mask", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 0b1010
		for lane := range initial.Lanes {
			initial.Lanes[lane].GPR[1] = uint32(10 + lane)
			initial.Lanes[lane].GPR[3] = uint32(0xa0 + lane)
		}
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x00508193}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
		if x3 != (isa.LaneValues{0xa0, 16, 0xa2, 18}) || result.Effects.RegisterWrites[0].Mask != 0b1010 {
			t.Fatalf("partial-mask result=%+v x3=%#x", result, x3)
		}
	})

	t.Run("float-partial-mask-and-fflags", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 0b1010
		for lane := range initial.Lanes {
			initial.Lanes[lane].FPR[3] = uint32(0xdead0000 + lane)
		}
		// Inactive signaling NaNs would raise invalid if accidentally evaluated.
		initial.Lanes[0].FPR[1], initial.Lanes[0].FPR[2] = 0x7f800001, 0x3f800000
		initial.Lanes[2].FPR[1], initial.Lanes[2].FPR[2] = 0x7f800001, 0x3f800000
		initial.Lanes[1].FPR[1], initial.Lanes[1].FPR[2] = 0x3f800000, 0x40000000
		initial.Lanes[3].FPR[1], initial.Lanes[3].FPR[2] = 0x40000000, 0x40800000
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x002081d3}).Step(state.ReadContext{}) // fadd.s f3,f1,f2
		requireOutcome(t, result, warp.OutcomeRetired)
		f3, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 3})
		after, _ := owner.Snapshot()
		if f3 != (isa.LaneValues{0xdead0000, 0x40400000, 0xdead0002, 0x40c00000}) || after.FCSR() != 0 {
			t.Fatalf("partial FP result=%+v f3=%#x fcsr=%#x", result, f3, after.FCSR())
		}
	})
}

func TestStepWarpWideBranchUsesHighestActiveLane(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = 0b1011
	initial.Lanes[0].GPR[1], initial.Lanes[0].GPR[2] = 1, 2
	initial.Lanes[1].GPR[1], initial.Lanes[1].GPR[2] = 3, 4
	initial.Lanes[3].GPR[1], initial.Lanes[3].GPR[2] = 5, 5
	owner := newState(t, initial)
	result := executor(t, owner, &recordingSource{word: 0x00208463}).Step(state.ReadContext{}) // beq x1,x2,+8
	requireOutcome(t, result, warp.OutcomeRetired)
	if result.NextPC != 0x108 || result.Effects.Control == nil || !result.Effects.Control.Taken || result.Effects.Control.DecisionLane != 3 {
		t.Fatalf("branch decision result=%+v", result)
	}
}

func TestStepMultiLaneJALRCSRAndTrapRemainAtomic(t *testing.T) {
	t.Run("jalr", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 0b0101
		initial.Lanes[0].GPR[5], initial.Lanes[2].GPR[5] = 0x120, 0x140
		for lane := range initial.Lanes {
			initial.Lanes[lane].GPR[4] = uint32(0xa0 + lane)
		}
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x00028267}).Step(state.ReadContext{}) // jalr x4,0(x5)
		requireOutcome(t, result, warp.OutcomeRetired)
		x4, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
		if result.NextPC != 0x140 || result.Effects.Control.DecisionLane != 2 ||
			x4 != (isa.LaneValues{0x104, 0xa1, 0x104, 0xa3}) {
			t.Fatalf("JALR result=%+v x4=%#x", result, x4)
		}
	})

	t.Run("lane-valued-csr-read", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 0b1010
		for lane := range initial.Lanes {
			initial.Lanes[lane].GPR[5] = uint32(0xb0 + lane)
		}
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0xf14022f3}).Step(state.ReadContext{}) // csrrs x5,mhartid,x0
		requireOutcome(t, result, warp.OutcomeRetired)
		x5, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 5})
		if x5 != (isa.LaneValues{0xb0, 5, 0xb2, 7}) || result.NextPC != 0x104 {
			t.Fatalf("CSR result=%+v x5=%#x", result, x5)
		}
	})

	t.Run("trap-saves-complete-mask", func(t *testing.T) {
		initial := initialWarp()
		initial.ActiveMask = 0b1101
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: 0x00000073}).Step(state.ReadContext{}) // ecall
		requireOutcome(t, result, warp.OutcomeTrap)
		after, _ := owner.Snapshot()
		if result.NextPC != 0x180 || after.SavedThreadMask() != 0b1101 || after.ActiveMask() != 0b1101 ||
			after.TrapCSRs().MEPC != 0x100 {
			t.Fatalf("trap result=%+v snapshot=%+v", result, after)
		}
	})
}

func catalogWord(t *testing.T, name string) uint32 {
	t.Helper()
	for _, entry := range isa.Catalog() {
		if entry.Name == name {
			return entry.Example
		}
	}
	t.Fatalf("missing catalog instruction %s", name)
	return 0
}

func TestStepDefersUnownedEffectsWithoutPendingLocalCommit(t *testing.T) {
	tests := []struct {
		name string
		word uint32
	}{
		{name: "data-memory", word: 0x00002083}, // lw x1,0(x0)
		{name: "future-owner-custom", word: catalogWord(t, "wspawn")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owner := newState(t, initialWarp())
			before, _ := owner.Snapshot()
			result := executor(t, owner, &recordingSource{word: test.word}).Step(state.ReadContext{})
			requireOutcome(t, result, warp.OutcomeDeferred)
			after, _ := owner.Snapshot()
			if result.Decoded == nil || result.Effects == nil || result.NextPC != 0x100 || before != after {
				t.Fatalf("deferred result=%+v state changed=%t", result, before != after)
			}
		})
	}
}

type mutatingSource struct {
	owner *state.WarpState
	word  uint32
}

func (s mutatingSource) Read(_ uint32, dst []byte) error {
	binary.LittleEndian.PutUint32(dst, s.word)
	return s.owner.SetPC(0x180)
}

func TestEffectValidationFailureDoesNotApplyEvaluatedRegisterOrPC(t *testing.T) {
	owner := newState(t, initialWarp())
	result := executor(t, owner, mutatingSource{owner: owner, word: 0x00500093}).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	x1, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	after, _ := owner.Snapshot()
	if result.Fault.Kind != warp.FaultEffectValidation || result.PC != 0x100 || result.NextPC != 0x180 ||
		x1[0] != 0 || after.PC() != 0x180 {
		t.Fatalf("rollback result=%+v x1=%#x pc=%#x", result, x1[0], after.PC())
	}
}
