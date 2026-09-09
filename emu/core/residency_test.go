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
