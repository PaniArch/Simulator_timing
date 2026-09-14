package memory_test

import (
	"bytes"
	"errors"
	"testing"

	"vortex.local/simulator/support/memory"
)

func newSparse(t *testing.T) *memory.Sparse {
	t.Helper()
	owner, err := memory.NewSparse(uint64(1)<<32, 4096, 0xbaadf00d)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestSparseFullAddressSpaceAndFill(t *testing.T) {
	owner := newSparse(t)
	if owner.Size() != uint64(1)<<32 || owner.AllocatedPages() != 0 {
		t.Fatalf("size=%#x pages=%d", owner.Size(), owner.AllocatedPages())
	}
	data := make([]byte, 8)
	if err := owner.Read(0xfffffff8, data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{0x0d, 0xf0, 0xad, 0xba, 0x0d, 0xf0, 0xad, 0xba}) || owner.AllocatedPages() != 0 {
		t.Fatalf("fill=%x pages=%d", data, owner.AllocatedPages())
	}
	if err := owner.Write(0xfffffffc, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := owner.Read(0xfffffffc, data[:4]); err != nil || !bytes.Equal(data[:4], []byte{1, 2, 3, 4}) {
		t.Fatalf("read=%x err=%v", data[:4], err)
	}
	if err := owner.Write(0xffffffff, []byte{1, 2}); !errors.Is(err, memory.ErrOutOfBounds) {
		t.Fatalf("overflow error=%v", err)
	}
}

func TestSparseCrossPageZeroAndAtomicBatch(t *testing.T) {
	owner := newSparse(t)
	payload := []byte{1, 2, 3, 4, 5, 6}
	if err := owner.Write(4094, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if err := owner.Read(4094, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("read=%v err=%v", got, err)
	}
	if err := owner.Zero(4095, 4); err != nil {
		t.Fatal(err)
	}
	if err := owner.Read(4094, got); err != nil || !bytes.Equal(got, []byte{1, 0, 0, 0, 0, 6}) {
		t.Fatalf("zeroed=%v err=%v", got, err)
	}
	if err := owner.WriteBatch([]uint32{0x100, 0x108}, [][]byte{{1, 2}, {3, 4}}); err != nil {
		t.Fatal(err)
	}
	before := make([]byte, 4)
	if err := owner.Read(0x100, before); err != nil {
		t.Fatal(err)
	}
	if err := owner.WriteBatch([]uint32{0x100, 0x101}, [][]byte{{9, 9}, {8}}); !errors.Is(err, memory.ErrOverlappingWrites) {
		t.Fatalf("overlap error=%v", err)
	}
	after := make([]byte, 4)
	if err := owner.Read(0x100, after); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("atomicity before=%x after=%x err=%v", before, after, err)
	}
}
