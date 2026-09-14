package memory

import (
	"encoding/binary"
	"fmt"
	"sync"
)

// Sparse is a bounded, page-backed memory suitable for the complete RV32
// address space. Pages are allocated only by writes; untouched bytes expose a
// deterministic repeating fill word.
type Sparse struct {
	mu       sync.RWMutex
	capacity uint64
	pageSize uint32
	fill     [4]byte
	pages    map[uint32][]byte
}

// NewSparse constructs a sparse memory. pageSize must be a non-zero power of
// two and capacity may be at most the complete 32-bit byte address space.
func NewSparse(capacity uint64, pageSize uint32, fillWord uint32) (*Sparse, error) {
	if capacity == 0 || capacity > addressSpaceSize {
		return nil, fmt.Errorf("%w: %d", ErrInvalidSize, capacity)
	}
	if pageSize == 0 || pageSize&(pageSize-1) != 0 || uint64(pageSize) > capacity {
		return nil, fmt.Errorf("memory: invalid sparse page size %d for capacity %d", pageSize, capacity)
	}
	result := &Sparse{capacity: capacity, pageSize: pageSize, pages: make(map[uint32][]byte)}
	binary.LittleEndian.PutUint32(result.fill[:], fillWord)
	return result, nil
}

// Size returns the complete logical capacity, not the allocated-page size.
func (m *Sparse) Size() uint64 {
	if m == nil {
		return 0
	}
	return m.capacity
}

func (m *Sparse) checkRange(address uint32, length uint64) error {
	if m == nil {
		return fmt.Errorf("memory: nil sparse memory")
	}
	start := uint64(address)
	if start > m.capacity || length > m.capacity-start {
		return &BoundsError{Addr: address, Length: length, Capacity: m.capacity}
	}
	return nil
}

func (m *Sparse) readLocked(address uint32, destination []byte) {
	for len(destination) != 0 {
		pageIndex := address / m.pageSize
		pageOffset := address & (m.pageSize - 1)
		count := min(len(destination), int(m.pageSize-pageOffset))
		if page := m.pages[pageIndex]; page != nil {
			copy(destination[:count], page[pageOffset:uint32(count)+pageOffset])
		} else {
			for index := range count {
				destination[index] = m.fill[(uint64(address)+uint64(index))&3]
			}
		}
		address += uint32(count)
		destination = destination[count:]
	}
}

// Read copies bytes from the logical address space without allocating pages.
func (m *Sparse) Read(address uint32, destination []byte) error {
	if err := m.checkRange(address, uint64(len(destination))); err != nil {
		return err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	m.readLocked(address, destination)
	return nil
}

func (m *Sparse) pageLocked(pageIndex uint32) []byte {
	if page := m.pages[pageIndex]; page != nil {
		return page
	}
	page := make([]byte, m.pageSize)
	base := uint64(pageIndex) * uint64(m.pageSize)
	for index := range page {
		page[index] = m.fill[(base+uint64(index))&3]
	}
	m.pages[pageIndex] = page
	return page
}

func (m *Sparse) writeLocked(address uint32, source []byte) {
	for len(source) != 0 {
		pageIndex := address / m.pageSize
		pageOffset := address & (m.pageSize - 1)
		count := min(len(source), int(m.pageSize-pageOffset))
		page := m.pageLocked(pageIndex)
		copy(page[pageOffset:uint32(count)+pageOffset], source[:count])
		address += uint32(count)
		source = source[count:]
	}
}

// Write copies bytes into the logical address space, allocating touched pages.
func (m *Sparse) Write(address uint32, source []byte) error {
	if err := m.checkRange(address, uint64(len(source))); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writeLocked(address, source)
	return nil
}

// WriteBatch applies non-overlapping lane writes atomically after validating
// every address and overlap before the first allocation or byte change.
func (m *Sparse) WriteBatch(addresses []uint32, sources [][]byte) error {
	if len(addresses) != len(sources) {
		return fmt.Errorf("memory: atomic batch has %d addresses and %d sources", len(addresses), len(sources))
	}
	for index, address := range addresses {
		if err := m.checkRange(address, uint64(len(sources[index]))); err != nil {
			return err
		}
		start, end := uint64(address), uint64(address)+uint64(len(sources[index]))
		for previous := 0; previous < index; previous++ {
			otherStart := uint64(addresses[previous])
			otherEnd := otherStart + uint64(len(sources[previous]))
			if start < otherEnd && otherStart < end {
				return fmt.Errorf("%w: entries %d and %d", ErrOverlappingWrites, previous, index)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for index, address := range addresses {
		m.writeLocked(address, sources[index])
	}
	return nil
}

// Zero writes deterministic zero bytes across the selected range.
func (m *Sparse) Zero(address uint32, length uint64) error {
	if err := m.checkRange(address, length); err != nil {
		return err
	}
	const chunkSize = uint64(4096)
	m.mu.Lock()
	defer m.mu.Unlock()
	zeros := make([]byte, min(length, chunkSize))
	for remaining := length; remaining != 0; {
		count := min(remaining, chunkSize)
		m.writeLocked(address, zeros[:count])
		address += uint32(count)
		remaining -= count
	}
	return nil
}

// AllocatedPages returns the number of physically materialized pages.
func (m *Sparse) AllocatedPages() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pages)
}
