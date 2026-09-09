package state_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func TestLatchedOperandCaptureUsesReadValuesAndTokenContext(t *testing.T) {
	w := newWarp(t)
	initial := snapshot(t, w)
	token := state.InstructionContext{WarpID: 2, PC: 0x140, Mask: 5}
	c, err := state.NewLatchedOperandCapture(initial, 0x001081b3, state.ReadContext{}, token) // add x3,x1,x1
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Read(initial, 1); err != nil {
		t.Fatal(err)
	}
	old, _ := initial.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	replacement := isa.LaneValues{50, 60, 70, 80}
	if err = w.WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 15, replacement); err != nil {
		t.Fatal(err)
	}
	if err = w.SetPC(0x200); err != nil {
		t.Fatal(err)
	}
	if err = w.SetActiveMask(1, state.WarpRunning); err != nil {
		t.Fatal(err)
	}
	current := snapshot(t, w)
	if err = c.Read(current, 2); err != nil {
		t.Fatal(err)
	}
	e, err := c.EvaluateAt(current, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	if e.Control.CurrentPC != 0x140 || e.Control.NextPC != 0x144 || e.RegisterWrites[0].Mask != 5 {
		t.Fatal("canonical context replaced token", e)
	}
	for lane := 0; lane < 4; lane++ {
		if token.Mask.Active(uint8(lane)) && e.RegisterWrites[0].Values[lane] != old[lane]+replacement[lane] {
			t.Fatal("read event values lost", e)
		}
	}
	requireUnchanged(t, w, current)
	// AUIPC makes the latched PC observable in register data, not only control.
	auipc, err := state.NewLatchedOperandCapture(current, 0x00000217, state.ReadContext{}, token)
	if err != nil {
		t.Fatal(err)
	}
	e, err = auipc.EvaluateAt(current, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	if e.RegisterWrites[0].Values[2] != token.PC {
		t.Fatal("AUIPC used canonical PC")
	}
	foreign := baseInitial()
	foreign.WarpID = 1
	other, err := state.NewWarp(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = auipc.EvaluateAt(snapshot(t, other), state.ReadContext{}); err == nil {
		t.Fatal("foreign owner context accepted")
	}
	for _, invalid := range []state.InstructionContext{{WarpID: 1, PC: 0x100, Mask: 5}, {WarpID: 2, PC: 0x101, Mask: 5}, {WarpID: 2, PC: 0x100, Mask: 0}, {WarpID: 2, PC: 0x100, Mask: 16}} {
		if _, err = state.NewLatchedOperandCapture(current, 0x00000217, state.ReadContext{}, invalid); err == nil {
			t.Fatal("invalid instruction context", invalid)
		}
	}
}

func streamEffects(t *testing.T, w *state.WarpState, pc uint32, word uint32) (state.InstructionContext, isa.InstructionEffects) {
	t.Helper()
	context := state.InstructionContext{WarpID: 2, PC: pc, Mask: 5}
	capture, err := state.NewLatchedOperandCapture(snapshot(t, w), word, state.ReadContext{}, context)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := isa.Decode(word)
	if err != nil {
		t.Fatal(err)
	}
	all := uint8((1<<len(decoded.Sources))-1) &^ capture.ReadMask()
	if err = capture.Read(snapshot(t, w), all); err != nil {
		t.Fatal(err)
	}
	e, err := capture.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	return context, e
}

func TestEffectStreamLateWBPreservesNewControlAndOtherWrites(t *testing.T) {
	w := newWarp(t)
	stream, err := state.NewEffectStream(w)
	if err != nil {
		t.Fatal(err)
	}
	olderContext, olderEffects := streamEffects(t, w, 0x100, 0x00700193) // addi x3,x0,7
	older, err := stream.NewDelivery(1, olderContext, olderEffects)
	if err != nil {
		t.Fatal(err)
	}
	newerContext, newerEffects := streamEffects(t, w, 0x104, 0x0000000b) // tmc x0: finish warp
	newer, err := stream.NewDelivery(2, newerContext, newerEffects)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, w)
	if err = newer.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	if stream.ControlOrder() != 2 || snapshot(t, w).PC() != 0x108 || snapshot(t, w).ActiveMask() != 0 {
		t.Fatal("newer control did not become visible")
	}
	marker := isa.Register{File: isa.Integer, Index: 20}
	if err = w.WriteRegister(marker, 5, isa.LaneValues{99, 99, 99, 99}); err != nil {
		t.Fatal(err)
	}
	if err = older.Deliver(state.WritebackEvent, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err = older.Deliver(state.WritebackEvent, 4, nil); err != nil {
		t.Fatal(err)
	}
	if err = older.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, w)
	if after.PC() != 0x108 || after.ActiveMask() != 0 || after.Lifecycle() != state.WarpInactive {
		t.Fatal("late completion restored older control")
	}
	values, _ := after.ReadRegister(marker)
	if values[0] != 99 || values[2] != 99 {
		t.Fatal("late delivery overwrote unrelated registers")
	}
	values, _ = after.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if values[0] != 7 || values[2] != 7 {
		t.Fatal("old WB disappeared with inactive mask")
	}
	if after.FCSR() != before.FCSR() || after.TrapCSRs() != before.TrapCSRs() {
		t.Fatal("unrelated canonical state changed")
	}
	stable := snapshot(t, w)
	if err = older.Deliver(state.WritebackEvent, 1, nil); err == nil {
		t.Fatal("duplicate WB")
	}
	if err = older.Deliver(state.ControlEvent, 0, nil); err == nil {
		t.Fatal("duplicate control receipt")
	}
	requireUnchanged(t, w, stable)
}

func TestEffectStreamLateSequentialAndStaleBranch(t *testing.T) {
	w := newWarp(t)
	stream, err := state.NewEffectStream(w)
	if err != nil {
		t.Fatal(err)
	}
	ctx1, e1 := streamEffects(t, w, 0x100, 0x0040006f) // jal x0,+4
	branch, err := stream.NewDelivery(1, ctx1, e1)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, e2 := streamEffects(t, w, 0x104, 0x00100093)
	second, err := stream.NewDelivery(2, ctx2, e2)
	if err != nil {
		t.Fatal(err)
	}
	if err = second.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, w)
	if err = branch.Deliver(state.ControlEvent, 0, nil); err == nil {
		t.Fatal("stale branch silently overwrote/ignored control")
	}
	requireUnchanged(t, w, before)
	if stream.ControlOrder() != 2 {
		t.Fatal("failed delivery changed frontier")
	}
	// The strict, existing APIs still reject a stale logical PC.
	if _, err = w.NewEffectDelivery(e1); err == nil {
		t.Fatal("legacy validation relaxed")
	}
	if _, err = w.StageEffects(e1); err == nil {
		t.Fatal("legacy stage validation relaxed")
	}
	// Payload cloning and validation must happen at creation without mutations.
	ctx3, e3 := streamEffects(t, w, 0x108, 0x00200113)
	third, err := stream.NewDelivery(3, ctx3, e3)
	if err != nil {
		t.Fatal(err)
	}
	e3.Control.NextPC = 0xdeadbeef
	if err = third.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	if snapshot(t, w).PC() != 0x10c {
		t.Fatal("effects retained mutable caller pointer")
	}
	if _, err = stream.NewDelivery(4, ctx3, e3); err == nil {
		t.Fatal("invalid control relationship accepted")
	}
}

func TestEffectStreamLateCreationAndCancellation(t *testing.T) {
	w := newWarp(t)
	stream, err := state.NewEffectStream(w)
	if err != nil {
		t.Fatal(err)
	}
	ctx1, e1 := streamEffects(t, w, 0x100, 0x00100093)
	ctx2, e2 := streamEffects(t, w, 0x104, 0x00200113)
	newer, err := stream.NewDelivery(2, ctx2, e2)
	if err != nil {
		t.Fatal(err)
	}
	if err = newer.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	// Memory completion may only create its delivery after a newer instruction
	// already changed PC. Its explicit context remains valid for local WB.
	old, err := stream.NewDelivery(1, ctx1, e1)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, w)
	old.Cancel()
	if err = old.Deliver(state.WritebackEvent, 5, nil); err == nil {
		t.Fatal("cancelled delivery applied")
	}
	if !reflect.DeepEqual(before, snapshot(t, w)) || stream.ControlOrder() != 2 {
		t.Fatal("cancel changed visible state")
	}
}

func TestEffectStreamFlagsAccumulateWithoutPCOrMaskRollback(t *testing.T) {
	w := newWarp(t)
	stream, err := state.NewEffectStream(w)
	if err != nil {
		t.Fatal(err)
	}
	ctx, old := streamEffects(t, w, 0x100, 0x00100093)
	old.FFlags = &isa.FFlagsEffect{Accumulate: isa.FFlagDivideByZero}
	d, err := stream.NewDelivery(1, ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	nextCtx, next := streamEffects(t, w, 0x104, 0x00200113)
	n, err := stream.NewDelivery(2, nextCtx, next)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err = w.SetActiveMask(1, state.WarpRunning); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, w)
	if err = d.Deliver(state.FFlagsEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err = d.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, w)
	if after.PC() != before.PC() || after.ActiveMask() != 1 || after.FCSR() != before.FCSR()|uint32(isa.FFlagDivideByZero) {
		t.Fatal("late flags overwrote unrelated control/FRM")
	}
}
