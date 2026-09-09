package core

import (
	"fmt"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

// ResidencyMemory owns CTA metadata and one physical LMEM array without a
// functional Core. The timing coordinator owns availability and tail draining.
// Explicit offsets expose the dispatcher's fixed-stride allocation policy.
type ResidencyMemory struct{ manager *CTAManager }

func NewResidencyMemory() *ResidencyMemory {
	m := NewCTAManager()
	m.barriers = &BarrierCoordinator{ctas: m, records: make(map[BarrierKey]barrierRecord)}
	return &ResidencyMemory{manager: m}
}
func (r *ResidencyMemory) Barriers() *BarrierCoordinator { return r.manager.barriers }
func (r *ResidencyMemory) BarrierPending(id uint32) bool { return r.manager.barriers.pendingCTA(id) }
func (r *ResidencyMemory) Admit(config CTAConfig, offset uint32) (CTASnapshot, error) {
	candidate, err := validateCTAConfig(config)
	if err != nil {
		return CTASnapshot{}, err
	}
	if config.ID >= isa.FrozenWarpCount || offset%64 != 0 || uint64(offset)+uint64(candidate.config.LocalMemorySize) > uint64(isa.FrozenLocalMemSize) {
		return CTASnapshot{}, fmt.Errorf("invalid physical CTA placement")
	}
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctas[config.ID] != nil {
		return CTASnapshot{}, ErrCTAResourcesUnavailable
	}
	for _, member := range candidate.members {
		if m.byWarp[member.WarpID] != nil {
			return CTASnapshot{}, ErrCTAResourcesUnavailable
		}
	}
	for _, old := range m.ctas {
		if candidate.config.LocalMemorySize != 0 && old.allocation.Size != 0 && offset < old.backingOffset+old.allocation.Size && old.backingOffset < offset+candidate.config.LocalMemorySize {
			return CTASnapshot{}, ErrCTAResourcesUnavailable
		}
	}
	candidate.backingOffset = offset
	candidate.allocation = LocalMemoryAllocation{Address: isa.FrozenLocalMemBase + offset, Size: candidate.config.LocalMemorySize}
	m.ctas[config.ID] = candidate
	for _, member := range candidate.members {
		m.byWarp[member.WarpID] = &warpCTAKey{ctaID: config.ID, rank: member.Rank}
	}
	m.epoch++
	return snapshotCTA(candidate), nil
}

// Release requires the coordinator to have drained all member requests and
// control state. Bytes remain physical SRAM contents, not freshly zeroed RAM.
func (r *ResidencyMemory) Release(id uint32) error {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.barriers
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, record := range b.records {
		if key.CTAID == id && (record.arrivals != 0 || record.waiters != 0 || record.participantCount != 0 || record.events != 0 || record.arrivalsComplete) {
			return fmt.Errorf("CTA retains barrier state")
		}
	}
	c := m.ctas[id]
	if c == nil {
		return fmt.Errorf("CTA slot is not resident")
	}
	for _, member := range c.members {
		m.byWarp[member.WarpID] = nil
	}
	delete(m.ctas, id)
	for key := range b.records {
		if key.CTAID == id {
			delete(b.records, key)
		}
	}
	m.epoch++
	return nil
}
func (r *ResidencyMemory) ViewForWarp(id uint8) (isa.CTAView, error) {
	return r.manager.ViewForWarp(id)
}
func (r *ResidencyMemory) Route(id uint8, global warp.MemoryService) (*CTAMemory, error) {
	return NewCTAMemory(r.manager, id, global)
}
