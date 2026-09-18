package core

import (
	"fmt"
	"vortex.local/simulator/emu/warp"
)

// PinLocal captures the allocation checked when a Warp is bound. Already
// addressed RTL LMEM requests retain their physical addresses after wid reuse;
// consulting the new wid->CTA table at SRAM service time would misroute them.
// This lease keeps no private bytes: every generation accesses the same SRAM.
func (r *ResidencyMemory) PinLocal(wid uint8) (warp.AtomicMemoryService, error) {
	m := r.manager
	m.mu.RLock()
	defer m.mu.RUnlock()
	if wid >= 4 || m.byWarp[wid] == nil {
		return nil, fmt.Errorf("cannot pin unbound local route")
	}
	c := m.ctas[m.byWarp[wid].ctaID]
	return &localLease{manager: m, allocation: c.allocation, offset: c.backingOffset}, nil
}

type localLease struct {
	manager    *CTAManager
	allocation LocalMemoryAllocation
	offset     uint32
}

func (l *localLease) position(address uint32, size int) (uint32, error) {
	start, end := uint64(address), uint64(address)+uint64(size)
	base, limit := uint64(l.allocation.Address), uint64(l.allocation.Address)+uint64(l.allocation.Size)
	if start < base || end > limit || size != 0 && start == limit {
		return 0, ErrLocalMemoryRange
	}
	return l.offset + address - l.allocation.Address, nil
}
func (l *localLease) Read(address uint32, data []byte) error {
	l.manager.mu.RLock()
	defer l.manager.mu.RUnlock()
	p, err := l.position(address, len(data))
	if err != nil {
		return err
	}
	copy(data, l.manager.localMem[p:p+uint32(len(data))])
	return nil
}
func (l *localLease) Write(address uint32, data []byte) error {
	return l.WriteBatch([]uint32{address}, [][]byte{data})
}
func (l *localLease) WriteBatch(addresses []uint32, data [][]byte) error {
	if len(addresses) != len(data) {
		return fmt.Errorf("invalid local lease batch")
	}
	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()
	positions := make([]uint32, len(addresses))
	for i, a := range addresses {
		p, err := l.position(a, len(data[i]))
		if err != nil {
			return err
		}
		positions[i] = p
		for j := 0; j < i; j++ {
			if uint64(a) < uint64(addresses[j])+uint64(len(data[j])) && uint64(addresses[j]) < uint64(a)+uint64(len(data[i])) {
				return fmt.Errorf("overlapping local lease writes")
			}
		}
	}
	for i, p := range positions {
		copy(l.manager.localMem[p:p+uint32(len(data[i]))], data[i])
	}
	return nil
}
