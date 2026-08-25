package elf32

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"vortex.local/simulator/support/memory"
)

const (
	elfHeaderSize     = 52
	programHeaderSize = 32
	segmentOffset     = 0x100
)

func testELF(machine uint16, class byte, paddr, filesz, memsz uint32, payload []byte) []byte {
	size := segmentOffset + len(payload)
	image := make([]byte, size)
	copy(image[0:4], []byte{0x7f, 'E', 'L', 'F'})
	image[4] = class
	image[5] = byte(1)
	image[6] = byte(1)

	order := binary.LittleEndian
	order.PutUint16(image[16:18], 2)
	order.PutUint16(image[18:20], machine)
	order.PutUint32(image[20:24], 1)
	order.PutUint32(image[24:28], 0x20)
	order.PutUint32(image[28:32], elfHeaderSize)
	order.PutUint16(image[40:42], elfHeaderSize)
	order.PutUint16(image[42:44], programHeaderSize)
	order.PutUint16(image[44:46], 1)

	program := image[elfHeaderSize : elfHeaderSize+programHeaderSize]
	order.PutUint32(program[0:4], 1)
	order.PutUint32(program[4:8], segmentOffset)
	order.PutUint32(program[8:12], paddr)
	order.PutUint32(program[12:16], paddr)
	order.PutUint32(program[16:20], filesz)
	order.PutUint32(program[20:24], memsz)
	order.PutUint32(program[24:28], 5)
	order.PutUint32(program[28:32], 4)
	copy(image[segmentOffset:], payload)
	return image
}

func testELF64() []byte {
	image := make([]byte, 64)
	copy(image[0:4], []byte{0x7f, 'E', 'L', 'F'})
	image[4] = byte(2)
	image[5] = byte(1)
	image[6] = byte(1)

	order := binary.LittleEndian
	order.PutUint16(image[16:18], 2)
	order.PutUint16(image[18:20], 243)
	order.PutUint32(image[20:24], 1)
	order.PutUint64(image[24:32], 0x20)
	order.PutUint16(image[52:54], 64)
	order.PutUint16(image[54:56], 56)
	return image
}

func TestLoadSegmentAndBSS(t *testing.T) {
	memory, err := memory.New(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Write(0, bytes.Repeat([]byte{0xaa}, 64)); err != nil {
		t.Fatal(err)
	}

	data := testELF(243, 1, 0x10, 4, 8, []byte{1, 2, 3, 4})
	image, err := Load(bytes.NewReader(data), memory)
	if err != nil {
		t.Fatal(err)
	}
	if image.Entry != 0x20 || image.LoadSegments != 1 {
		t.Fatalf("image = %+v", image)
	}
	got, err := memory.ReadBytes(0x0f, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0xaa, 1, 2, 3, 4, 0, 0, 0, 0, 0xaa}
	if !bytes.Equal(got, want) {
		t.Fatalf("loaded bytes = %v, want %v", got, want)
	}
}

func TestRejectsWrongMachineAndClass(t *testing.T) {
	memory, err := memory.New(64)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"machine", testELF(62, 1, 0x10, 1, 1, []byte{1}), "unsupported machine"},
		{"class", testELF64(), "unsupported class"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(bytes.NewReader(test.data), memory); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRejectsSegmentOutsideMemory(t *testing.T) {
	memory, err := memory.New(64)
	if err != nil {
		t.Fatal(err)
	}
	data := testELF(243, 1, 60, 4, 8, []byte{1, 2, 3, 4})
	if _, err := Load(bytes.NewReader(data), memory); err == nil || !strings.Contains(err.Error(), "exceeds memory size") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestRejectsTruncatedSegment(t *testing.T) {
	memory, err := memory.New(64)
	if err != nil {
		t.Fatal(err)
	}
	data := testELF(243, 1, 0x10, 8, 8, []byte{1, 2, 3, 4})
	if _, err := Load(bytes.NewReader(data), memory); err == nil {
		t.Fatal("expected truncated segment error")
	}
}
