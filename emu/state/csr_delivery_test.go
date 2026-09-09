package state_test

import (
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func TestCombinedCSRFlagsFailureLeavesBothReceipts(t *testing.T) {
	w := newWarp(t)
	snapshot, _ := w.Snapshot()
	old := snapshot.FCSR()
	csr, err := w.NewEffectDelivery(isa.InstructionEffects{CSRWrites: []isa.CSRWriteEffect{{Address: 1, Scope: isa.CSRScopeWarp, WarpID: snapshot.WarpID(), OldValue: old & 31, Value: 2, WriteMask: 31}}})
	if err != nil {
		t.Fatal(err)
	}
	flags, err := w.NewEffectDelivery(isa.InstructionEffects{FFlags: &isa.FFlagsEffect{Accumulate: isa.FFlagOverflow}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SetFCSR(old ^ 1); err != nil {
		t.Fatal(err)
	}
	if err := csr.DeliverCSRWithFlags(flags, nil); err == nil {
		t.Fatal("stale CSR old value accepted")
	}
	current, _ := w.Snapshot()
	if current.FCSR() != old^1 || csr.Delivered(state.CSREvent) || flags.Delivered(state.FFlagsEvent) {
		t.Fatal("failed joint transaction mutated owner or receipts")
	}
	if err := w.SetFCSR(old); err != nil {
		t.Fatal(err)
	}
	if err := csr.DeliverCSRWithFlags(flags, nil); err != nil {
		t.Fatal(err)
	}
	current, _ = w.Snapshot()
	if current.FCSR() != old&^31|2 || !csr.Delivered(state.CSREvent) || !flags.Delivered(state.FFlagsEvent) {
		t.Fatal("joint priority/receipts missing")
	}
	if err := csr.DeliverCSRWithFlags(flags, nil); err == nil {
		t.Fatal("joint transaction replayed")
	}
}
