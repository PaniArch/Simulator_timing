package core

import (
	"fmt"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

// ResidencyMemory owns CTA metadata and one physical LMEM array without a
// functional Core. The timing coordinator owns availability and tail draining.
// Explicit offsets expose the dispatcher's fixed-stride allocation policy.
type ResidencyMemory struct {
	manager    *CTAManager
	templates  map[uint32][]WarpMembership
	boundRanks map[uint32]isa.WarpMask
}

func NewResidencyMemory() *ResidencyMemory {
	m := NewCTAManager()
	m.barriers = &BarrierCoordinator{ctas: m, records: make(map[BarrierKey]barrierRecord)}
	return &ResidencyMemory{manager: m, templates: make(map[uint32][]WarpMembership), boundRanks: make(map[uint32]isa.WarpMask)}
}
func (r *ResidencyMemory) Barriers() *BarrierCoordinator { return r.manager.barriers }
func (r *ResidencyMemory) BarrierPending(id uint32) bool { return r.manager.barriers.pendingCTA(id) }
func (r *ResidencyMemory) Admit(config CTAConfig, offset uint32) (CTASnapshot, error) {
	return r.admit(config, offset, false)
}

// Reserve installs CTA metadata and LMEM without reserving physical Warps.
// WarpIDs in config are only rank templates; BindWarp installs actual mappings.
func (r *ResidencyMemory) Reserve(config CTAConfig, offset uint32) (CTASnapshot, error) {
	return r.admit(config, offset, true)
}

func (r *ResidencyMemory) admit(config CTAConfig, offset uint32, incremental bool) (CTASnapshot, error) {
	candidate, err := validateCTAConfig(config)
	if err != nil {
		return CTASnapshot{}, err
	}
	if config.ID >= isa.FrozenWarpCount || offset%64 != 0 || uint64(offset)+uint64(candidate.config.LocalMemorySize) > uint64(isa.FrozenLocalMemSize) {
		return CTASnapshot{}, fmt.Errorf("invalid physical CTA placement")
	}
	templates := append([]WarpMembership(nil), candidate.members...)
	if incremental {
		candidate.members = nil
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
	r.templates[config.ID] = templates
	if !incremental {
		r.boundRanks[config.ID] = isa.WarpMask((1 << len(templates)) - 1)
	}
	for _, member := range candidate.members {
		m.byWarp[member.WarpID] = &warpCTAKey{ctaID: config.ID, rank: member.Rank}
	}
	m.epoch++
	return snapshotCTA(candidate), nil
}

// Release drops slot metadata after hardware retirement. Coordinators must
// retain already-addressed tails through pinned routes before reusing a slot;
// unresolved barrier ownership cannot be released. Bytes remain physical SRAM.
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
	delete(r.templates, id)
	delete(r.boundRanks, id)
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

// BindWarp binds the next logical rank. The coordinator must establish hardware
// availability and retain already-addressed old requests independently before
// DetachWarp/rebinding; complete pipeline quiescence is not an RTL dispatch gate.
func (r *ResidencyMemory) BindWarp(id, rank uint32, wid uint8) (CTASnapshot, error) {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.ctas[id]
	if c == nil || wid >= isa.FrozenWarpCount || rank >= uint32(len(r.templates[id])) || m.byWarp[wid] != nil || r.boundRanks[id]&(1<<rank) != 0 {
		return CTASnapshot{}, ErrCTAResourcesUnavailable
	}
	for _, member := range c.members {
		if member.Rank == rank {
			return CTASnapshot{}, fmt.Errorf("duplicate logical Warp rank")
		}
	}
	r.boundRanks[id] |= 1 << rank
	member := r.templates[id][rank]
	member.WarpID = wid
	c.members = append(c.members, member)
	m.byWarp[wid] = &warpCTAKey{ctaID: id, rank: rank}
	m.epoch++
	return snapshotCTA(c), nil
}

// DetachWarp releases only a physical binding, preserving the CTA's LMEM and
// metadata. Pending barriers conservatively retain their address/membership.
func (r *ResidencyMemory) DetachWarp(wid uint8) (CTASnapshot, error) {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if wid >= isa.FrozenWarpCount || m.byWarp[wid] == nil {
		return CTASnapshot{}, fmt.Errorf("Warp is not bound")
	}
	key := m.byWarp[wid]
	if r.BarrierPending(key.ctaID) {
		return CTASnapshot{}, fmt.Errorf("Warp retains barrier ownership")
	}
	c := m.ctas[key.ctaID]
	for i, member := range c.members {
		if member.WarpID == wid {
			c.members = append(c.members[:i], c.members[i+1:]...)
			m.byWarp[wid] = nil
			m.epoch++
			return snapshotCTA(c), nil
		}
	}
	return CTASnapshot{}, fmt.Errorf("inconsistent Warp binding")
}
