package core

import (
	"testing"
	"vortex.local/simulator/isa"
)

func TestPinnedLocalRouteSurvivesPhysicalWarpReuse(t *testing.T) {
	r := NewResidencyMemory()
	c := CTAConfig{ID: 0, WarpIDs: []uint8{0}, BlockSize: 4, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{2, 1, 1}, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1, LocalMemorySize: 64}
	if _, err := r.Admit(c, 0); err != nil {
		t.Fatal(err)
	}
	old, err := r.PinLocal(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DetachWarp(0); err != nil {
		t.Fatal(err)
	}
	c.ID = 1
	if _, err := r.Admit(c, 64); err != nil {
		t.Fatal(err)
	}
	newOwner, err := r.PinLocal(0)
	if err != nil {
		t.Fatal(err)
	}
	base := uint32(isa.FrozenLocalMemBase)
	if err := old.Write(base, []byte{42}); err != nil {
		t.Fatal(err)
	}
	if err := newOwner.Write(base+64, []byte{99}); err != nil {
		t.Fatal(err)
	}
	var got [1]byte
	if err := old.Read(base, got[:]); err != nil || got[0] != 42 {
		t.Fatal(got, err)
	}
	if err := newOwner.Read(base+64, got[:]); err != nil || got[0] != 99 {
		t.Fatal(got, err)
	}
	if err := old.Read(base+64, got[:]); err == nil {
		t.Fatal("lease escaped original allocation")
	}
	// The lease is an address route, not a private copy of SRAM contents.
	if err := r.Release(0); err != nil {
		t.Fatal(err)
	}
	c.ID, c.WarpIDs = 0, []uint8{1}
	if _, err := r.Admit(c, 0); err != nil {
		t.Fatal(err)
	}
	reused, err := r.PinLocal(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := reused.Write(base, []byte{7}); err != nil {
		t.Fatal(err)
	}
	if err := old.Read(base, got[:]); err != nil || got[0] != 7 {
		t.Fatal("lease copied bytes", got, err)
	}
	if err := old.WriteBatch([]uint32{base, base + 64}, [][]byte{{1}, {2}}); err == nil {
		t.Fatal("out of bounds batch accepted")
	}
	if err := old.Read(base, got[:]); err != nil || got[0] != 7 {
		t.Fatal("failed batch partially wrote", got, err)
	}
}
