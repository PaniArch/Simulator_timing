// Package elf32 loads little-endian RISC-V ELF32 images into functional memory.
package elf32

import (
	"debug/elf"
	"fmt"
	"io"
	"os"
)

const addressSpaceSize = uint64(1) << 32

// Memory is the minimum destination required by the loader.
type Memory interface {
	Size() uint64
	Write(addr uint32, src []byte) error
	Zero(addr uint32, length uint64) error
}

// Image describes the loaded executable.
type Image struct {
	Entry        uint32
	LoadSegments int
}

// LoadFile opens and loads an ELF file.
func LoadFile(path string, memory Memory) (Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return Image{}, fmt.Errorf("elf32: open %q: %w", path, err)
	}
	defer file.Close()
	return Load(file, memory)
}

// Load parses an ELF32 RISC-V image and writes each PT_LOAD segment by p_paddr.
func Load(reader io.ReaderAt, memory Memory) (Image, error) {
	if reader == nil {
		return Image{}, fmt.Errorf("elf32: nil reader")
	}
	if memory == nil {
		return Image{}, fmt.Errorf("elf32: nil memory")
	}

	file, err := elf.NewFile(reader)
	if err != nil {
		return Image{}, fmt.Errorf("elf32: parse: %w", err)
	}
	defer file.Close()

	if file.Class != elf.ELFCLASS32 {
		return Image{}, fmt.Errorf("elf32: unsupported class %s", file.Class)
	}
	if file.Data != elf.ELFDATA2LSB {
		return Image{}, fmt.Errorf("elf32: unsupported byte order %s", file.Data)
	}
	if file.Machine != elf.EM_RISCV {
		return Image{}, fmt.Errorf("elf32: unsupported machine %s", file.Machine)
	}
	if file.Entry >= addressSpaceSize {
		return Image{}, fmt.Errorf("elf32: entry %#x exceeds 32-bit address space", file.Entry)
	}

	image := Image{Entry: uint32(file.Entry)}
	for index, program := range file.Progs {
		if program.Type != elf.PT_LOAD || program.Memsz == 0 {
			continue
		}
		if program.Filesz > program.Memsz {
			return Image{}, fmt.Errorf("elf32: PT_LOAD[%d] filesz %d exceeds memsz %d", index, program.Filesz, program.Memsz)
		}
		if program.Paddr >= addressSpaceSize || program.Memsz > addressSpaceSize-program.Paddr {
			return Image{}, fmt.Errorf("elf32: PT_LOAD[%d] range [%#x,+%#x) exceeds 32-bit address space", index, program.Paddr, program.Memsz)
		}
		if program.Paddr > memory.Size() || program.Memsz > memory.Size()-program.Paddr {
			return Image{}, fmt.Errorf("elf32: PT_LOAD[%d] range [%#x,+%#x) exceeds memory size %#x", index, program.Paddr, program.Memsz, memory.Size())
		}

		maxInt := uint64(^uint(0) >> 1)
		if program.Filesz > maxInt {
			return Image{}, fmt.Errorf("elf32: PT_LOAD[%d] file image is too large", index)
		}
		segment := make([]byte, int(program.Filesz))
		if _, err := io.ReadFull(program.Open(), segment); err != nil {
			return Image{}, fmt.Errorf("elf32: read PT_LOAD[%d]: %w", index, err)
		}
		addr := uint32(program.Paddr)
		if err := memory.Write(addr, segment); err != nil {
			return Image{}, fmt.Errorf("elf32: write PT_LOAD[%d]: %w", index, err)
		}
		if bssSize := program.Memsz - program.Filesz; bssSize != 0 {
			bssAddr := uint32(program.Paddr + program.Filesz)
			if err := memory.Zero(bssAddr, bssSize); err != nil {
				return Image{}, fmt.Errorf("elf32: zero PT_LOAD[%d] BSS: %w", index, err)
			}
		}
		image.LoadSegments++
	}

	return image, nil
}
