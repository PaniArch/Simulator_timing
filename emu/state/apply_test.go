package state_test

import (
	"errors"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func snapshot(t *testing.T, w *state.WarpState) state.WarpSnapshot {
	t.Helper()
	result, err := w.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireUnchanged(t *testing.T, w *state.WarpState, before state.WarpSnapshot) {
	t.Helper()
	after := snapshot(t, w)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("canonical state changed on rejected/deferred effect\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestApplyIntegerRegisterAndPCAtomically(t *testing.T) {
	w := newWarp(t)
	op := decoded(t, "add")
	input, err := w.IntegerInput(op, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := isa.EvaluateInteger(op, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.ApplyEffects(effects)
	if err != nil || !result.Committed || result.AwaitingExternal || result.Pending != nil {
		t.Fatalf("ApplyEffects result=%+v err=%v", result, err)
	}

	got, _ := w.ReadRegister(op.Destinations[0])
	// Only active lanes 0 and 2 are in the evaluator's effect mask.
	if got != (isa.LaneValues{3, 0x10003, 0x40003, 0x30003}) {
		t.Fatalf("masked ADD write=%#x", got)
	}
	after := snapshot(t, w)
	if after.PC() != 0x104 || after.ActiveMask() != 0b0101 {
		t.Fatalf("PC/mask after commit=%#x/%04b", after.PC(), after.ActiveMask())
	}
}

func TestApplyFloatRegisterFlagsAndPC(t *testing.T) {
	initial := baseInitial()
	for lane := range initial.Lanes {
		initial.Lanes[lane].FPR[1] = 0x3f800000 // 1.0
		initial.Lanes[lane].FPR[2] = 0x40000000 // 2.0
	}
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	op := decoded(t, "fadd.s")
	input, err := w.FloatInput(op, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := isa.EvaluateFloat(op, input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := w.ApplyEffects(effects)
	if err != nil || !result.Committed {
		t.Fatalf("ApplyEffects=%+v err=%v", result, err)
	}
	got, _ := w.ReadRegister(op.Destinations[0])
	if got[0] != 0x40400000 || got[2] != 0x40400000 || got[1] == 0x40400000 || got[3] == 0x40400000 {
		t.Fatalf("masked FPR result=%x", got)
	}
	after := snapshot(t, w)
	if after.PC() != 0x104 || after.FCSR() != 0x51 {
		t.Fatalf("FP PC/FCSR=%#x/%#x", after.PC(), after.FCSR())
	}
}

func TestCommitWithExternalCoordinatesOneInfallibleBoundary(t *testing.T) {
	makeStage := func(t *testing.T, w *state.WarpState) *state.EffectStage {
		t.Helper()
		stage, err := w.StageEffects(isa.InstructionEffects{
			Control: &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104},
		})
		if err != nil {
			t.Fatal(err)
		}
		return stage
	}
	t.Run("success", func(t *testing.T) {
		w := newWarp(t)
		before := snapshot(t, w)
		stage := makeStage(t, w)
		calls := 0
		err := stage.CommitWithExternal(func() error {
			calls++
			// A synchronous external callback cannot make the already-validated
			// local replacement stale after it succeeds.
			return w.SetFCSR(0x22)
		})
		if err != nil || calls != 1 {
			t.Fatalf("CommitWithExternal calls=%d err=%v", calls, err)
		}
		after := snapshot(t, w)
		if after.PC() != 0x104 || after.FCSR() != before.FCSR() {
			t.Fatalf("coordinated state pc=%#x fcsr=%#x", after.PC(), after.FCSR())
		}
	})
	t.Run("external-failure", func(t *testing.T) {
		w := newWarp(t)
		before := snapshot(t, w)
		stage := makeStage(t, w)
		wantErr := errors.New("external failure")
		err := stage.CommitWithExternal(func() error {
			if setErr := w.SetFCSR(0x22); setErr != nil {
				return setErr
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("CommitWithExternal error=%v", err)
		}
		requireUnchanged(t, w, before)
	})
	t.Run("stale-before-external", func(t *testing.T) {
		w := newWarp(t)
		stage := makeStage(t, w)
		if err := w.SetFCSR(0x22); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := stage.CommitWithExternal(func() error { called = true; return nil }); err == nil || called {
			t.Fatalf("stale coordinated commit err=%v called=%t", err, called)
		}
		after := snapshot(t, w)
		if after.PC() != 0x100 || after.FCSR() != 0x22 {
			t.Fatalf("stale state pc=%#x fcsr=%#x", after.PC(), after.FCSR())
		}
	})
}

func TestCommitForwardedWithExternalChecksStalenessBeforeOwnerMutation(t *testing.T) {
	w := newWarp(t)
	makeStage := func() *state.EffectStage {
		stage, err := w.StageEffects(isa.InstructionEffects{
			Control:  &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104},
			Barriers: []isa.BarrierEffect{{WarpID: 2, AddressWarp: 0, ID: 0, Kind: isa.BarrierSync}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return stage
	}
	t.Run("success", func(t *testing.T) {
		stage := makeStage()
		calls := 0
		if err := stage.CommitForwardedWithExternal(func() error { calls++; return nil }); err != nil || calls != 1 {
			t.Fatalf("forwarded commit calls=%d err=%v", calls, err)
		}
		if got := snapshot(t, w).PC(); got != 0x104 {
			t.Fatalf("forwarded commit PC=%#x", got)
		}
	})
	t.Run("stale", func(t *testing.T) {
		fresh := newWarp(t)
		stage, err := fresh.StageEffects(isa.InstructionEffects{
			Control:  &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104},
			Barriers: []isa.BarrierEffect{{WarpID: 2, AddressWarp: 0, ID: 0, Kind: isa.BarrierSync}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := fresh.SetFCSR(0x22); err != nil {
			t.Fatal(err)
		}
		called := false
		if err := stage.CommitForwardedWithExternal(func() error { called = true; return nil }); err == nil || called {
			t.Fatalf("stale forwarded commit err=%v called=%t", err, called)
		}
		if got := snapshot(t, fresh); got.PC() != 0x100 || got.FCSR() != 0x22 {
			t.Fatalf("stale forwarded state pc=%#x fcsr=%#x", got.PC(), got.FCSR())
		}
	})
}

func TestApplyRegisterMaskX0AndWGatherException(t *testing.T) {
	t.Run("exact-mask-and-x0", func(t *testing.T) {
		w := newWarp(t)
		register := isa.Register{File: isa.Integer, Index: 9}
		before, _ := w.ReadRegister(register)
		effects := isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{
			{Destination: register, Mask: 1, Values: isa.LaneValues{0xaa, 0xbb, 0xcc, 0xdd}},
			{Destination: isa.Register{File: isa.Integer, Index: 0}, Mask: isa.AllLanes, Values: isa.LaneValues{1, 2, 3, 4}},
		}}
		result, err := w.ApplyEffects(effects)
		if err != nil || !result.Committed {
			t.Fatalf("ApplyEffects=%+v err=%v", result, err)
		}
		after, _ := w.ReadRegister(register)
		if after != (isa.LaneValues{0xaa, before[1], before[2], before[3]}) {
			t.Fatalf("ordinary write escaped effect mask: before=%x after=%x", before, after)
		}
		x0, _ := w.ReadRegister(isa.Register{File: isa.Integer, Index: 0})
		if x0 != (isa.LaneValues{}) {
			t.Fatalf("effect applier changed x0: %x", x0)
		}
	})

	t.Run("wgather-non-source-lanes", func(t *testing.T) {
		w := newWarp(t)
		op := decoded(t, "wgather")
		input, err := w.CustomInput(op, state.ReadContext{})
		if err != nil {
			t.Fatal(err)
		}
		effects, err := isa.EvaluateCustom(op, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(effects.RegisterWrites) != 1 || effects.RegisterWrites[0].Mask != 0b1110 {
			t.Fatalf("WGATHER effect mask=%+v", effects.RegisterWrites)
		}
		if _, err := w.ApplyEffects(effects); err != nil {
			t.Fatal(err)
		}
		got, _ := w.ReadRegister(op.Destinations[0])
		want := effects.RegisterWrites[0].Values
		if got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Fatalf("WGATHER explicit mask was filtered by active lanes: got=%x want=%x", got, want)
		}
		// Lanes 1 and 3 were inactive in the input mask, proving they were not
		// silently removed by the State owner.
		if input.ActiveMask.Active(1) || input.ActiveMask.Active(3) {
			t.Fatal("test precondition: WGATHER destination lanes unexpectedly active")
		}
	})
}

func TestApplyFFlagsAndSoftwareFCSRArbitration(t *testing.T) {
	tests := []struct {
		name  string
		write isa.CSRWriteEffect
		want  uint32
	}{
		{
			name: "software-fflags-overrides-sticky-field",
			write: isa.CSRWriteEffect{Address: 0x001, Scope: isa.CSRScopeWarp, WarpID: 2,
				OldValue: 0x11, Value: 0x02, WriteMask: 0x1f},
			want: 0x42,
		},
		{
			name: "software-frm-preserves-sticky-flags",
			write: isa.CSRWriteEffect{Address: 0x002, Scope: isa.CSRScopeWarp, WarpID: 2,
				OldValue: 2, Value: 4, WriteMask: 0x07},
			want: 0x95,
		},
		{
			name: "software-fcsr-overrides-all-fields",
			write: isa.CSRWriteEffect{Address: 0x003, Scope: isa.CSRScopeWarp, WarpID: 2,
				OldValue: 0x51, Value: 0xa3, WriteMask: 0xff},
			want: 0xa3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := newWarp(t)
			effects := isa.InstructionEffects{
				FFlags:    &isa.FFlagsEffect{Accumulate: isa.FFlagOverflow},
				CSRWrites: []isa.CSRWriteEffect{test.write},
			}
			if _, err := w.ApplyEffects(effects); err != nil {
				t.Fatal(err)
			}
			if got := snapshot(t, w).FCSR(); got != test.want {
				t.Fatalf("FCSR=%#x, want %#x", got, test.want)
			}
		})
	}
}

func TestApplySoftwareWarpCSRAndOldValueResult(t *testing.T) {
	w := newWarp(t)
	// csrrw x3, mstatus, x1
	op, err := isa.Decode(0x300091f3)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := w.ReadRegister(op.Sources[0])
	values[0] = 0xabcdef01 // CSR source is RTL lane zero.
	if err := w.WriteRegister(op.Sources[0], 1, values); err != nil {
		t.Fatal(err)
	}
	input, err := w.SystemInput(op, state.ReadContext{ActiveWarps: 0b0100})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := isa.EvaluateSystem(op, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ApplyEffects(effects); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, w)
	if after.TrapCSRs().MStatus != 0xabcdef01 || after.PC() != 0x104 {
		t.Fatalf("software CSR state=%+v pc=%#x", after.TrapCSRs(), after.PC())
	}
	result, _ := w.ReadRegister(op.Destinations[0])
	if result[0] != 1 || result[2] != 1 || result[1] == 1 || result[3] == 1 {
		t.Fatalf("CSR old-value masked result=%x", result)
	}
}

func TestApplyTMCAndPredicateLifecycle(t *testing.T) {
	for _, name := range []string{"tmc", "pred"} {
		t.Run(name, func(t *testing.T) {
			w := newWarp(t)
			op := decoded(t, name)
			input, err := w.CustomInput(op, state.ReadContext{})
			if err != nil {
				t.Fatal(err)
			}
			effects, err := isa.EvaluateCustom(op, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.ApplyEffects(effects); err != nil {
				t.Fatal(err)
			}
			after := snapshot(t, w)
			want := effects.WarpMasks[0]
			if after.ActiveMask() != want.Mask || (after.Lifecycle() == state.WarpRunning) != want.Active || after.PC() != 0x104 {
				t.Fatalf("%s apply mask/lifecycle/PC=%04b/%d/%#x, effect=%+v", name, after.ActiveMask(), after.Lifecycle(), after.PC(), want)
			}
		})
	}
	t.Run("tmc-terminates", func(t *testing.T) {
		w := newWarp(t)
		op := decoded(t, "tmc")
		values, _ := w.ReadRegister(op.Sources[0])
		values[2] = 0 // highest active lane selects an empty replacement mask
		if err := w.WriteRegister(op.Sources[0], 1<<2, values); err != nil {
			t.Fatal(err)
		}
		input, _ := w.CustomInput(op, state.ReadContext{})
		effects, err := isa.EvaluateCustom(op, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.ApplyEffects(effects); err != nil {
			t.Fatal(err)
		}
		after := snapshot(t, w)
		if after.ActiveMask() != 0 || after.Lifecycle() != state.WarpInactive || after.PC() != 0x104 {
			t.Fatalf("TMC termination mask/lifecycle/PC=%04b/%d/%#x", after.ActiveMask(), after.Lifecycle(), after.PC())
		}
	})
}

func TestApplySplitJoinPushMarkAndPop(t *testing.T) {
	initial := baseInitial()
	// A single live row leaves capacity for the SPLIT push at pointer 1.
	initial.Divergence = state.DivergenceInitial{
		WritePointer: 1,
		Records:      []state.DivergenceRecordInitial{{Pointer: 0, OriginalMask: 0b0011, NextPC: 0x180}},
	}
	// SPLIT's highest active lane (2) is false while lane 0 is true.
	initial.Lanes[2].GPR[1] = 0
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	split := decoded(t, "split")
	input, _ := w.CustomInput(split, state.ReadContext{})
	effects, err := isa.EvaluateCustom(split, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ApplyEffects(effects); err != nil {
		t.Fatal(err)
	}
	afterSplit := snapshot(t, w)
	if afterSplit.DivergenceWritePointer() != 2 || afterSplit.ActiveMask() != 1 || afterSplit.PC() != 0x104 {
		t.Fatalf("after SPLIT ptr/mask/pc=%d/%04b/%#x", afterSplit.DivergenceWritePointer(), afterSplit.ActiveMask(), afterSplit.PC())
	}
	record, _ := afterSplit.DivergenceRecord(1)
	if !record.Valid || record.OriginalMask != 0b0101 || record.NextPC != 0x104 || record.ElseVisited {
		t.Fatalf("SPLIT record=%+v", record)
	}

	join := decoded(t, "join")
	joinPointer := isa.LaneValues{1, 1, 1, 1}
	if err := w.WriteRegister(join.Sources[0], isa.AllLanes, joinPointer); err != nil {
		t.Fatal(err)
	}
	joinInput, _ := w.CustomInput(join, state.ReadContext{})
	joinEffects, err := isa.EvaluateCustom(join, joinInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ApplyEffects(joinEffects); err != nil {
		t.Fatal(err)
	}
	afterMark := snapshot(t, w)
	marked, _ := afterMark.DivergenceRecord(1)
	if !marked.ElseVisited || afterMark.DivergenceWritePointer() != 2 || afterMark.ActiveMask() != 0b0100 || afterMark.PC() != 0x104 {
		t.Fatalf("after JOIN mark record=%+v ptr/mask/pc=%d/%04b/%#x", marked, afterMark.DivergenceWritePointer(), afterMark.ActiveMask(), afterMark.PC())
	}

	joinInput, _ = w.CustomInput(join, state.ReadContext{})
	joinEffects, err = isa.EvaluateCustom(join, joinInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ApplyEffects(joinEffects); err != nil {
		t.Fatal(err)
	}
	afterPop := snapshot(t, w)
	popped, _ := afterPop.DivergenceRecord(1)
	if popped.Valid || afterPop.DivergenceWritePointer() != 1 || afterPop.ActiveMask() != 0b0101 || afterPop.PC() != 0x108 {
		t.Fatalf("after JOIN pop record=%+v ptr/mask/pc=%d/%04b/%#x", popped, afterPop.DivergenceWritePointer(), afterPop.ActiveMask(), afterPop.PC())
	}
}

func TestApplyTrapEntryAndReturn(t *testing.T) {
	t.Run("entry", func(t *testing.T) {
		w := newWarp(t)
		op := decoded(t, "ecall")
		input, err := w.SystemInput(op, state.ReadContext{ActiveWarps: 0b0100})
		if err != nil {
			t.Fatal(err)
		}
		effects, err := isa.EvaluateSystem(op, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.ApplyEffects(effects); err != nil {
			t.Fatal(err)
		}
		after := snapshot(t, w)
		csr := after.TrapCSRs()
		if after.PC() != 0x200 || after.SavedThreadMask() != 0b0101 || csr.MEPC != 0x100 || csr.MCause != isa.TrapCauseEnvironmentCallMMode || csr.MTVal != 0 {
			t.Fatalf("trap entry state pc/mask/csr=%#x/%04b/%+v", after.PC(), after.SavedThreadMask(), csr)
		}
	})

	t.Run("return", func(t *testing.T) {
		initial := baseInitial()
		initial.PC = 0x400
		initial.ActiveMask = 1
		initial.SavedThreadMask = 0b1010
		initial.TrapCSRs.MEPC = 0x123
		w, err := state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		op := decoded(t, "mret")
		input, _ := w.SystemInput(op, state.ReadContext{ActiveWarps: 1})
		effects, err := isa.EvaluateSystem(op, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.ApplyEffects(effects); err != nil {
			t.Fatal(err)
		}
		after := snapshot(t, w)
		if after.PC() != 0x120 || after.ActiveMask() != 0b1010 || after.Lifecycle() != state.WarpRunning || after.SavedThreadMask() != 0b1010 {
			t.Fatalf("trap return state pc/mask/lifecycle/saved=%#x/%04b/%d/%04b", after.PC(), after.ActiveMask(), after.Lifecycle(), after.SavedThreadMask())
		}
	})
}

func TestEveryValidationFailureRollsBackWholeCandidate(t *testing.T) {
	validControl := &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104}
	tests := []struct {
		name    string
		effects isa.InstructionEffects
		field   string
	}{
		{"register-index-after-earlier-write", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{
			{Destination: isa.Register{File: isa.Integer, Index: 1}, Mask: 1, Values: isa.LaneValues{99}},
			{Destination: isa.Register{File: isa.Integer, Index: 32}, Mask: 1},
		}, Control: validControl}, "register-write"},
		{"register-mask", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: isa.Register{File: isa.Integer, Index: 1}, Mask: 0x80}}}, "register-write"},
		{"stale-control-after-register", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: isa.Register{File: isa.Integer, Index: 1}, Mask: 1, Values: isa.LaneValues{99}}}, Control: &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0, NextPC: 4}}, "control"},
		{"wrong-csr-scope", isa.InstructionEffects{CSRWrites: []isa.CSRWriteEffect{{Address: 0x300, Scope: isa.CSRScopeCore, WarpID: 2, OldValue: 1, Value: 2, WriteMask: 0xffffffff}}}, "csr-write"},
		{"stale-csr-old-value", isa.InstructionEffects{CSRWrites: []isa.CSRWriteEffect{{Address: 0x300, Scope: isa.CSRScopeWarp, WarpID: 2, OldValue: 9, Value: 2, WriteMask: 0xffffffff}}}, "csr-write"},
		{"stale-csr-read", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: isa.Register{File: isa.Integer, Index: 1}, Mask: 1, Values: isa.LaneValues{99}}}, CSRReads: []isa.CSRReadEffect{{Address: 0x300, Scope: isa.CSRScopeWarp}}, Control: validControl}, "csr-read"},
		{"wrong-warp", isa.InstructionEffects{WarpMasks: []isa.WarpMaskEffect{{WarpID: 1, Reason: isa.WarpMaskTMC, Mask: 1, Active: true}}, Control: validControl}, "warp-mask"},
		{"contradictory-mask", isa.InstructionEffects{WarpMasks: []isa.WarpMaskEffect{{WarpID: 2, Reason: isa.WarpMaskTMC, Mask: 0, Active: true}}, Control: validControl}, "warp-mask"},
		{"divergence-stale-pointer", isa.InstructionEffects{Divergence: &isa.DivergenceEffect{Action: isa.DivergenceSplit, WarpID: 2, StackPointer: 0, OriginalMask: 0b0101}, Control: validControl}, "divergence"},
		{"reconverge-without-stack", isa.InstructionEffects{Control: &isa.ControlEffect{Reason: isa.PCReconverge, CurrentPC: 0x100, NextPC: 0x180, Target: 0x180, Taken: true, DecisionLane: 2}}, "control"},
		{"trap-mask", isa.InstructionEffects{Trap: &isa.TrapEffect{Kind: isa.TrapEnter, EPC: 0x100, Vector: 0x200, SaveThreadMask: 1}, Control: &isa.ControlEffect{Reason: isa.PCTrap, CurrentPC: 0x100, NextPC: 0x200, Target: 0x200, Taken: true}}, "trap"},
		{"trap-vector-not-canonical-mtvec", isa.InstructionEffects{
			Control: &isa.ControlEffect{Reason: isa.PCTrap, CurrentPC: 0x100, NextPC: 0x400, Target: 0x400, Taken: true},
			CSRWrites: []isa.CSRWriteEffect{
				{Address: 0x341, Scope: isa.CSRScopeWarp, WarpID: 2, OldValue: 0x300, Value: 0x100, WriteMask: 0xffffffff},
				{Address: 0x342, Scope: isa.CSRScopeWarp, WarpID: 2, OldValue: 5, Value: isa.TrapCauseEnvironmentCallMMode, WriteMask: 0xffffffff},
				{Address: 0x343, Scope: isa.CSRScopeWarp, WarpID: 2, OldValue: 6, Value: 0, WriteMask: 0xffffffff},
			},
			Trap: &isa.TrapEffect{
				Kind: isa.TrapEnter, Cause: isa.TrapCauseEnvironmentCallMMode,
				EPC: 0x100, Vector: 0x400, SaveThreadMask: 0b0101,
			},
		}, "trap"},
		{"fault-with-local", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: isa.Register{File: isa.Integer, Index: 1}, Mask: 1}}, Faults: []isa.FaultEffect{{Kind: isa.FaultLoadAccess, Lane: 0}}}, "bundle"},
		{"duplicate-register", isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: isa.Register{File: isa.Float, Index: 1}}, {Destination: isa.Register{File: isa.Float, Index: 1}}}}, "register-write"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := newWarp(t)
			before := snapshot(t, w)
			_, err := w.ApplyEffects(test.effects)
			var validation *state.EffectValidationError
			if !errors.As(err, &validation) || validation.Field != test.field {
				t.Fatalf("error=%T %v, want validation field %q", err, err, test.field)
			}
			requireUnchanged(t, w, before)
		})
	}
}

func TestFutureOwnerEffectsAreLosslesslyForwardedAndGateCommit(t *testing.T) {
	w := newWarp(t)
	before := snapshot(t, w)
	effects := isa.InstructionEffects{
		Control:        &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104},
		MemoryRequests: []isa.MemoryRequest{{Lane: 0, Kind: isa.MemoryStore, Address: 0x1000, AlignedAddress: 0x1000, Width: 4, ByteMask: 0xf, StoreData: 7}},
		Ordering:       &isa.OrderingEffect{Predecessor: 3, Successor: 5},
		CSRReads: []isa.CSRReadEffect{
			{Address: 0xcc2, Scope: isa.CSRScopeCore, Values: isa.LaneValues{1, 1, 1, 1}},
			{Address: 0xcd0, Scope: isa.CSRScopeCTA, Values: isa.LaneValues{7, 7, 7, 7}},
		},
		WarpSpawn:   &isa.WarpSpawnEffect{SourceWarp: 2, Targets: 0b0011, TargetPC: 0x400, InitialLaneMask: 1},
		WarpDrains:  []isa.WarpDrainEffect{{WarpID: 2, Kind: isa.DrainLSU}},
		Barriers:    []isa.BarrierEffect{{WarpID: 2, AddressWarp: 1, ID: 3, Kind: isa.BarrierSync}},
		PackedLoads: []isa.PackedLoadRequest{{Lane: 1, Element: 0, Address: 0x2000, AlignedAddress: 0x2000, Width: 1}},
	}
	result, err := w.ApplyEffects(effects)
	if err != nil || result.Committed || !result.AwaitingExternal || result.Pending == nil {
		t.Fatalf("ApplyEffects result=%+v err=%v", result, err)
	}
	requireUnchanged(t, w, before)

	forwarded := result.Forwarded
	if !reflect.DeepEqual(forwarded.MemoryRequests, effects.MemoryRequests) || !reflect.DeepEqual(forwarded.Ordering, effects.Ordering) ||
		!reflect.DeepEqual(forwarded.CSRReads, effects.CSRReads) || !reflect.DeepEqual(forwarded.WarpSpawn, effects.WarpSpawn) ||
		!reflect.DeepEqual(forwarded.WarpDrains, effects.WarpDrains) || !reflect.DeepEqual(forwarded.Barriers, effects.Barriers) ||
		!reflect.DeepEqual(forwarded.PackedLoads, effects.PackedLoads) {
		t.Fatalf("forwarded effects lost data\ngot=%+v\nwant=%+v", forwarded, effects)
	}
	if len(forwarded.RegisterWrites) != 0 || forwarded.Control != nil || forwarded.FFlags != nil || forwarded.Trap != nil || len(forwarded.WarpMasks) != 0 || forwarded.Divergence != nil {
		t.Fatalf("local effects incorrectly marked unconsumed: %+v", forwarded)
	}
	if err := result.Pending.Commit(); err == nil {
		t.Fatal("pending stage committed without external success")
	}
	requireUnchanged(t, w, before)

	// Returned forwarding data is a copy and cannot corrupt the pending stage.
	result.Forwarded.MemoryRequests[0].Address = 0
	if got := result.Pending.ForwardedEffects().MemoryRequests[0].Address; got != 0x1000 {
		t.Fatalf("forwarded result aliases stage: %#x", got)
	}
	if err := result.Pending.CommitAfterExternal(); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, w).PC(); got != 0x104 {
		t.Fatalf("PC after external success=%#x", got)
	}
}

func TestFaultForwardingAndStalePendingStage(t *testing.T) {
	t.Run("fault-forwarding", func(t *testing.T) {
		w := newWarp(t)
		faults := []isa.FaultEffect{{Kind: isa.FaultLoadAccess, Reason: isa.FaultReasonMemoryService, Lane: 2, Address: 0xdead, Width: 4}}
		result, err := w.ApplyEffects(isa.InstructionEffects{Faults: faults})
		if err != nil || !result.AwaitingExternal || !reflect.DeepEqual(result.Forwarded.Faults, faults) {
			t.Fatalf("fault routing result=%+v err=%v", result, err)
		}
	})

	t.Run("stale-stage", func(t *testing.T) {
		w := newWarp(t)
		stage, err := w.StageEffects(isa.InstructionEffects{
			Control:  &isa.ControlEffect{Reason: isa.PCSequential, CurrentPC: 0x100, NextPC: 0x104},
			Ordering: &isa.OrderingEffect{Predecessor: 1, Successor: 1},
		})
		if err != nil || !stage.RequiresExternalSuccess() {
			t.Fatalf("StageEffects=%+v err=%v", stage, err)
		}
		if err := w.SetFCSR(0x22); err != nil {
			t.Fatal(err)
		}
		if err := stage.CommitAfterExternal(); err == nil {
			t.Fatal("stale stage committed")
		}
		after := snapshot(t, w)
		if after.PC() != 0x100 || after.FCSR() != 0x22 {
			t.Fatalf("stale stage partially applied: pc=%#x fcsr=%#x", after.PC(), after.FCSR())
		}
	})
}
