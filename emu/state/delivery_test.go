package state_test

import (
	"errors"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func TestOperandCaptureSamplesEachBankRead(t *testing.T) {
	w := newWarp(t)
	s := snapshot(t, w)
	c, err := state.NewOperandCapture(s, 0x001081b3, state.ReadContext{}) // add x3,x1,x1
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Evaluate(); err == nil {
		t.Fatal("evaluated before reads")
	}
	if err := c.Read(s, 1); err != nil {
		t.Fatal(err)
	}
	old, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	replacement := isa.LaneValues{50, 60, 70, 80}
	if err := w.WriteRegister(isa.Register{File: isa.Integer, Index: 1}, s.ActiveMask(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := c.Read(snapshot(t, w), 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Read(snapshot(t, w), 1); err == nil {
		t.Fatal("duplicate read accepted")
	}
	before := snapshot(t, w)
	e, err := c.Evaluate()
	if err != nil {
		t.Fatal(err)
	}
	requireUnchanged(t, w, before)
	for lane := 0; lane < 4; lane++ {
		if s.ActiveMask().Active(uint8(lane)) && e.RegisterWrites[0].Values[lane] != old[lane]+replacement[lane] {
			t.Fatal("did not retain per-source read values", e)
		}
	}
	if err := w.SetPC(s.PC() + 4); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EvaluateAt(snapshot(t, w), state.ReadContext{}); err == nil {
		t.Fatal("stale context accepted")
	}
}

func TestEffectDeliveryDoesNotRestoreOldState(t *testing.T) {
	w := newWarp(t)
	s := snapshot(t, w)
	op := decoded(t, "add")
	e, err := s.Evaluate(op, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := w.NewEffectDelivery(e)
	if err != nil {
		t.Fatal(err)
	}
	old, err := w.StageEffects(e)
	if err != nil {
		t.Fatal(err)
	}
	requireUnchanged(t, w, s)
	if err := d.Deliver(state.ControlEvent, 0, nil); err != nil {
		t.Fatal(err)
	}
	if snapshot(t, w).PC() != s.PC()+4 {
		t.Fatal("control not visible")
	}
	// A separate owner's newer write must survive a later captured WB.
	marker := isa.Register{File: isa.Integer, Index: 20}
	if err := w.WriteRegister(marker, s.ActiveMask(), isa.LaneValues{99, 99, 99, 99}); err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(state.WritebackEvent, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(state.WritebackEvent, 1, nil); err == nil {
		t.Fatal("repeated WB accepted")
	}
	if err := d.Deliver(state.WritebackEvent, e.RegisterWrites[0].Mask&^1, nil); err != nil {
		t.Fatal(err)
	}
	if snapshot(t, w).PC() != s.PC()+4 {
		t.Fatal("WB restored old PC")
	}
	v, _ := w.ReadRegister(marker)
	if v[0] != 99 || v[2] != 99 {
		t.Fatal("WB restored unrelated registers")
	}
	if err := old.Commit(); err == nil {
		t.Fatal("legacy stale check removed")
	}
	if err := d.Deliver(state.ControlEvent, 0, nil); err == nil {
		t.Fatal("control delivered twice")
	}
}

func TestDeliveryExternalFailureAndRetry(t *testing.T) {
	w := newWarp(t)
	s := snapshot(t, w)
	op := decoded(t, "sw")
	e, err := s.Evaluate(op, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := w.NewEffectDelivery(e)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	failure := errors.New("service busy")
	if err := d.Deliver(state.MemoryEvent, 0, func(isa.InstructionEffects) error { calls++; return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	requireUnchanged(t, w, s)
	if d.Delivered(state.MemoryEvent) {
		t.Fatal("failed event got receipt")
	}
	if err := d.Deliver(state.MemoryEvent, 0, func(got isa.InstructionEffects) error {
		calls++
		if len(got.MemoryRequests) == 0 || got.Control != nil || len(got.RegisterWrites) != 0 {
			t.Fatal("wrong event group")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.Deliver(state.MemoryEvent, 0, func(isa.InstructionEffects) error { calls++; return nil }); err == nil || calls != 2 {
		t.Fatal("external owner called twice")
	}
	requireUnchanged(t, w, s)
	d.Cancel()
	if err := d.Deliver(state.ControlEvent, 0, nil); err == nil {
		t.Fatal("cancelled delivery accepted")
	}
}

func TestByteWriteMergesLiveOwnerAndRetainsStaleCheck(t *testing.T) {
	w := newWarp(t)
	target := isa.Register{File: isa.Float, Index: 3}
	if err := w.WriteRegister(target, 1, isa.LaneValues{0x11223344}); err != nil {
		t.Fatal(err)
	}
	e := isa.InstructionEffects{RegisterWrites: []isa.RegisterWriteEffect{{Destination: target, Mask: 1, ByteMask: 2, Values: isa.LaneValues{0x0000aa00}}}}
	delivery, err := w.NewEffectDelivery(e)
	if err != nil {
		t.Fatal(err)
	}
	old, err := w.StageEffects(e)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.WriteRegister(target, 1, isa.LaneValues{0x55667788}); err != nil {
		t.Fatal(err)
	}
	if err = delivery.Deliver(state.WritebackEvent, 1, nil); err != nil {
		t.Fatal(err)
	}
	value, err := snapshot(t, w).ReadRegister(target)
	if err != nil || value[0] != 0x5566aa88 {
		t.Fatal("lost newer unrelated bytes", value, err)
	}
	if err = old.Commit(); err == nil {
		t.Fatal("stale transaction accepted")
	}
	e.RegisterWrites[0].ByteMask = 16
	before := snapshot(t, w)
	if _, err = w.StageEffects(e); err == nil {
		t.Fatal("invalid byte mask accepted")
	}
	requireUnchanged(t, w, before)
}
