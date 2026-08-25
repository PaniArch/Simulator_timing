package memory

import (
	"errors"
	"reflect"
	"testing"
)

func newMemory(t *testing.T, size uint64) *Memory {
	t.Helper()
	memory, err := New(size)
	if err != nil {
		t.Fatal(err)
	}
	return memory
}

func TestReadWriteAndSnapshotIsolation(t *testing.T) {
	memory := newMemory(t, 16)
	if err := memory.Write(3, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}

	got := make([]byte, 4)
	if err := memory.Read(3, got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("read got %v", got)
	}

	snapshot := memory.Snapshot()
	snapshot[3] = 0xff
	value, err := memory.Load8(3)
	if err != nil {
		t.Fatal(err)
	}
	if value != 1 {
		t.Fatalf("snapshot modified memory: got %d", value)
	}
}

func TestLittleEndianLoadsAndStores(t *testing.T) {
	memory := newMemory(t, 16)
	if err := memory.Store8(0, 0xa5); err != nil {
		t.Fatal(err)
	}
	if err := memory.Store16(1, 0x1234); err != nil {
		t.Fatal(err)
	}
	if err := memory.Store32(4, 0x89abcdef); err != nil {
		t.Fatal(err)
	}

	if got, _ := memory.Load8(0); got != 0xa5 {
		t.Fatalf("Load8 got %#x", got)
	}
	if got, _ := memory.Load16(1); got != 0x1234 {
		t.Fatalf("Load16 got %#x", got)
	}
	if got, _ := memory.Load32(4); got != 0x89abcdef {
		t.Fatalf("Load32 got %#x", got)
	}
	if got := memory.Snapshot()[4:8]; !reflect.DeepEqual(got, []byte{0xef, 0xcd, 0xab, 0x89}) {
		t.Fatalf("stored bytes got %v", got)
	}
}

func TestBoundsChecksAreAtomic(t *testing.T) {
	memory := newMemory(t, 8)
	if err := memory.Write(0, []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	before := memory.Snapshot()

	if err := memory.Write(7, []byte{9, 10}); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Write error = %v", err)
	}
	if !reflect.DeepEqual(memory.Snapshot(), before) {
		t.Fatal("out-of-bounds write modified memory")
	}

	dst := []byte{0xaa, 0xbb}
	if err := memory.Read(7, dst); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Read error = %v", err)
	}
	if !reflect.DeepEqual(dst, []byte{0xaa, 0xbb}) {
		t.Fatalf("out-of-bounds read modified destination: %v", dst)
	}

	if _, err := memory.Load32(6); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Load32 error = %v", err)
	}
	if _, err := memory.ReadBytes(0, 1024); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("ReadBytes error = %v", err)
	}
	if err := memory.Store16(7, 0xffff); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Store16 error = %v", err)
	}
}

func TestZero(t *testing.T) {
	memory := newMemory(t, 8)
	if err := memory.Write(0, []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	if err := memory.Zero(2, 4); err != nil {
		t.Fatal(err)
	}
	if got := memory.Snapshot(); !reflect.DeepEqual(got, []byte{1, 2, 0, 0, 0, 0, 7, 8}) {
		t.Fatalf("zero got %v", got)
	}
}

func TestNewRejectsAddressSpaceOverflow(t *testing.T) {
	if _, err := New((uint64(1) << 32) + 1); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("New error = %v", err)
	}
}
