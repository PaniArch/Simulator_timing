package core

import (
	"testing"
	"vortex.local/simulator/isa"
)

func TestResidencyPlacementAndAtomicConflict(t *testing.T) {
	r := NewResidencyMemory()
	config := CTAConfig{ID: 0, WarpIDs: []uint8{0}, BlockSize: 3, BlockDimensions: [3]uint32{3, 1, 1}, GridDimensions: [3]uint32{2, 1, 1}, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1, LocalMemorySize: 65}
	first, err := r.Admit(config, 256)
	if err != nil {
		t.Fatal(err)
	}
	if first.LocalMemory.Address != isa.FrozenLocalMemBase+256 || first.LocalMemory.Size != 128 || first.Members[0].ActiveMask != 7 {
		t.Fatal(first)
	}
	config.ID = 1
	config.WarpIDs = []uint8{1}
	if _, err = r.Admit(config, 320); err == nil {
		t.Fatal("overlap accepted")
	}
	if _, err = r.ViewForWarp(1); err == nil {
		t.Fatal("failed admission installed membership")
	}
	if _, err = r.Admit(config, isa.FrozenLocalMemSize-64); err == nil {
		t.Fatal("out of bounds accepted")
	}
	if _, err = r.Admit(config, 512); err != nil {
		t.Fatal(err)
	}
	if err = r.Release(0); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ViewForWarp(0); err == nil {
		t.Fatal("release kept membership")
	}
	if _, err = r.ViewForWarp(1); err != nil {
		t.Fatal("release damaged neighbor", err)
	}
}

func TestIncrementalResidencyKeepsLogicalSizeAndLMEM(t *testing.T) {
	r := NewResidencyMemory()
	config := CTAConfig{ID: 0, WarpIDs: []uint8{0, 1}, BlockSize: 6, BlockDimensions: [3]uint32{3, 2, 1}, WarpStep: [3]uint32{1, 1, 0}, GridDimensions: [3]uint32{2, 1, 1}, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1, LocalMemorySize: 64}
	reserved, err := r.Reserve(config, 0)
	if err != nil || len(reserved.Members) != 0 {
		t.Fatal(reserved, err)
	}
	config.ID = 1
	if _, err := r.Reserve(config, 0); err == nil {
		t.Fatal("unbound CTA lost LMEM reservation")
	}
	if _, err := r.Reserve(config, 64); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BindWarp(0, 0, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BindWarp(0, 0, 2); err == nil {
		t.Fatal("duplicate rank")
	}
	view, err := r.ViewForWarp(3)
	if err != nil || view.Rank != 0 || view.Size != 2 {
		t.Fatal(view, err)
	}
	if err := r.manager.writeLocal(3, isa.FrozenLocalMemBase, []byte{42}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.BindWarp(0, 1, 0); err != nil {
		t.Fatal(err)
	}
	// An asynchronous event retains the address Warp even without any waiter.
	key := BarrierKey{CTAID: 0, AddressWarp: 3, ID: 0}
	r.manager.barriers.records[key] = barrierRecord{events: 1}
	if _, err := r.DetachWarp(3); err == nil {
		t.Fatal("barrier address reused")
	}
	delete(r.manager.barriers.records, key)
	if _, err := r.DetachWarp(3); err != nil {
		t.Fatal(err)
	}
	view, err = r.ViewForWarp(0)
	if err != nil || view.Rank != 1 || view.Size != 2 || view.ThreadCoordinates[0] != (isa.LaneValues{1, 2, 0, 1}) {
		t.Fatal(view, err)
	}
	if _, err := r.BindWarp(1, 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := r.manager.writeLocal(3, isa.FrozenLocalMemBase+64, []byte{99}); err != nil {
		t.Fatal(err)
	}
	var got [1]byte
	if err := r.manager.readLocal(0, isa.FrozenLocalMemBase, got[:]); err != nil || got[0] != 42 {
		t.Fatal(got, err)
	}
	if err := r.Release(0); err != nil {
		t.Fatal(err)
	}
	if view, err := r.ViewForWarp(3); err != nil || view.ID != 1 {
		t.Fatal("old release damaged new binding", view, err)
	}
}
