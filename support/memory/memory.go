// Package memory provides bounded, byte-addressable functional memory.
package memory

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// ErrOutOfBounds identifies an access outside the allocated memory.
var ErrOutOfBounds = errors.New("memory access out of bounds")

// ErrInvalidSize identifies a memory size outside the 32-bit address space.
var ErrInvalidSize = errors.New("invalid memory size")

const addressSpaceSize = uint64(1) << 32

// BoundsError describes a rejected memory range.
type BoundsError struct {
	Addr     uint32
	Length   uint64
	Capacity uint64
}

func (e *BoundsError) Error() string {
	return fmt.Sprintf("%v: address=%#x length=%d capacity=%d", ErrOutOfBounds, e.Addr, e.Length, e.Capacity)
}

func (e *BoundsError) Unwrap() error {
	return ErrOutOfBounds
}

// Memory is a contiguous functional memory with a 32-bit byte address.
type Memory struct {
	mu   sync.RWMutex
	data []byte
}

// New allocates zero-initialized memory.
func New(size uint64) (*Memory, error) {
	maxInt := uint64(^uint(0) >> 1)
	if size > addressSpaceSize || size > maxInt {
		return nil, fmt.Errorf("%w: %d", ErrInvalidSize, size)
	}
	return &Memory{data: make([]byte, int(size))}, nil
}

// Size returns the number of addressable bytes.
func (m *Memory) Size() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return uint64(len(m.data))
}

func (m *Memory) checkRange(addr uint32, length uint64) error {
	capacity := uint64(len(m.data))
	start := uint64(addr)
	if start > capacity || length > capacity-start {
		return &BoundsError{Addr: addr, Length: length, Capacity: capacity}
	}
	return nil
}

// Read copies len(dst) bytes beginning at addr into dst.
func (m *Memory) Read(addr uint32, dst []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkRange(addr, uint64(len(dst))); err != nil {
		return err
	}
	copy(dst, m.data[int(addr):int(uint64(addr)+uint64(len(dst)))])
	return nil
}

// ReadBytes returns a copy of length bytes beginning at addr.
func (m *Memory) ReadBytes(addr uint32, length uint64) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := m.checkRange(addr, length); err != nil {
		return nil, err
	}
	maxInt := uint64(^uint(0) >> 1)
	if length > maxInt {
		return nil, &BoundsError{Addr: addr, Length: length, Capacity: uint64(len(m.data))}
	}
	data := make([]byte, int(length))
	copy(data, m.data[int(addr):int(uint64(addr)+length)])
	return data, nil
}

// Write copies src into memory beginning at addr.
func (m *Memory) Write(addr uint32, src []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkRange(addr, uint64(len(src))); err != nil {
		return err
	}
	copy(m.data[int(addr):int(uint64(addr)+uint64(len(src)))], src)
	return nil
}

// Zero clears length bytes beginning at addr.
func (m *Memory) Zero(addr uint32, length uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkRange(addr, length); err != nil {
		return err
	}
	clear(m.data[int(addr):int(uint64(addr)+length)])
	return nil
}

// Snapshot returns a copy of the complete memory image.
func (m *Memory) Snapshot() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]byte, len(m.data))
	copy(result, m.data)
	return result
}

// Load8 reads an 8-bit value.
func (m *Memory) Load8(addr uint32) (uint8, error) {
	var data [1]byte
	if err := m.Read(addr, data[:]); err != nil {
		return 0, err
	}
	return data[0], nil
}

// Load16 reads a little-endian 16-bit value.
func (m *Memory) Load16(addr uint32) (uint16, error) {
	var data [2]byte
	if err := m.Read(addr, data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(data[:]), nil
}

// Load32 reads a little-endian 32-bit value.
func (m *Memory) Load32(addr uint32) (uint32, error) {
	var data [4]byte
	if err := m.Read(addr, data[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data[:]), nil
}

// Store8 writes an 8-bit value.
func (m *Memory) Store8(addr uint32, value uint8) error {
	return m.Write(addr, []byte{value})
}

// Store16 writes a little-endian 16-bit value.
func (m *Memory) Store16(addr uint32, value uint16) error {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], value)
	return m.Write(addr, data[:])
}

// Store32 writes a little-endian 32-bit value.
func (m *Memory) Store32(addr uint32, value uint32) error {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], value)
	return m.Write(addr, data[:])
}
