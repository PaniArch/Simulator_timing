package core

import (
	"fmt"
	"sync"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
)

const (
	frozenBarrierCount = 8
	frozenMaxBarEvents = 32
)

// BarrierKey is the canonical T6 namespace. AddressWarp and ID are the
// physical RTL address fields; CTAID supplies the isolation which the frozen
// single-Core RTL receives from resident CTA ownership.
type BarrierKey struct {
	CTAID       uint32
	AddressWarp uint8
	ID          uint8
}

// BarrierSnapshot is a detached observation of one canonical record.
type BarrierSnapshot struct {
	Key              BarrierKey
	Arrivals         isa.WarpMask
	Waiters          isa.WarpMask
	ParticipantCount uint8
	Events           uint8
	Phase            bool
	ArrivalsComplete bool
}

type barrierRecord struct {
	arrivals         isa.WarpMask
	waiters          isa.WarpMask
	participantCount uint8
	events           uint8
	phase            bool
	arrivalsComplete bool
}

// BarrierCoordinator is the sole writable owner of barrier arrival, wait,
// participant, event, and phase state. Its CTA manager identity is fixed.
type BarrierCoordinator struct {
	mu      sync.Mutex
	ctas    *CTAManager
	records map[BarrierKey]barrierRecord
}

func NewBarrierCoordinator(ctas *CTAManager) (*BarrierCoordinator, error) {
	if ctas == nil {
		return nil, fmt.Errorf("core: barrier coordinator requires a CTA manager")
	}
	ctas.mu.Lock()
	defer ctas.mu.Unlock()
	if len(ctas.ctas) == 0 && !ctas.dynamic {
		return nil, fmt.Errorf("core: cannot bind barriers to an empty CTA manager")
	}
	ctas.sealed = true
	if ctas.barriers != nil {
		return ctas.barriers, nil
	}
	result := &BarrierCoordinator{ctas: ctas, records: make(map[BarrierKey]barrierRecord)}
	ctas.barriers = result
	return result, nil
}

func (b *BarrierCoordinator) empty() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.records) == 0
}

func (b *BarrierCoordinator) pendingCTA(ctaID uint32) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, record := range b.records {
		if key.CTAID == ctaID && (record.arrivals != 0 || record.waiters != 0 ||
			record.participantCount != 0 || record.events != 0 || record.arrivalsComplete) {
			return true
		}
	}
	return false
}

// BarrierResult describes scheduler work which follows a committed request.
type BarrierResult struct {
	Key      BarrierKey
	Block    bool
	Releases isa.WarpMask
}

// BarrierStage is a detached, optimistic coordinator transaction. Commit
// rejects a stale record before changing canonical barrier state.
type BarrierStage struct {
	owner       *BarrierCoordinator
	key         BarrierKey
	before      barrierRecord
	beforeValid bool
	after       barrierRecord
	result      BarrierResult
	committed   bool
}

type barrierView struct {
	ctaID   uint32
	phases  state.BarrierPhaseView
	records [isa.FrozenWarpCount][frozenBarrierCount]BarrierSnapshot
}

func (s *BarrierStage) Result() BarrierResult {
	if s == nil {
		return BarrierResult{}
	}
	return s.result
}

func (s *BarrierStage) Commit() error {
	if s == nil || s.owner == nil {
		return fmt.Errorf("core: nil barrier stage")
	}
	if s.committed {
		return fmt.Errorf("core: barrier stage was already committed")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	current, valid := s.owner.records[s.key]
	if valid != s.beforeValid || (valid && current != s.before) {
		return fmt.Errorf("core: barrier stage is stale because canonical state changed")
	}
	s.owner.records[s.key] = s.after
	s.committed = true
	return nil
}

// Stage validates and stages one instruction request without mutation.
func (b *BarrierCoordinator) Stage(effect isa.BarrierEffect) (*BarrierStage, error) {
	return b.stage(effect, nil)
}

func (b *BarrierCoordinator) stageFromView(effect isa.BarrierEffect, view barrierView) (*BarrierStage, error) {
	if effect.AddressWarp >= isa.FrozenWarpCount || effect.ID >= frozenBarrierCount {
		return nil, fmt.Errorf("core: barrier address exceeds frozen topology")
	}
	expected := view.records[effect.AddressWarp][effect.ID]
	return b.stage(effect, &expected)
}

func (b *BarrierCoordinator) stage(effect isa.BarrierEffect, expected *BarrierSnapshot) (*BarrierStage, error) {
	if b == nil || b.ctas == nil {
		return nil, fmt.Errorf("core: nil barrier coordinator")
	}
	key, memberCount, err := b.keyForEffect(effect)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	before, valid := b.records[key]
	if expected != nil && (key != expected.Key || snapshotBarrier(key, before) != *expected) {
		return nil, fmt.Errorf("core: barrier effect is stale because canonical state changed since phase view")
	}
	after := before
	result := BarrierResult{Key: key}

	if effect.Event {
		if effect.Kind != isa.BarrierArrive || effect.Arrive || effect.Wait || effect.Sync ||
			effect.ReleaseByCoordinator || !effect.Phase || effect.ExpectCount == 0 ||
			effect.ExpectCount > frozenMaxBarEvents ||
			(effect.ExpectCount < frozenMaxBarEvents && effect.ParticipantCount != effect.ExpectCount) ||
			(effect.ExpectCount == frozenMaxBarEvents && effect.ParticipantCount != 0) ||
			effect.SizeMinusOne != uint8((uint32(effect.ParticipantCount)-1)&0x1f) {
			return nil, fmt.Errorf("core: malformed barrier expect-event request")
		}
		if uint16(after.events)+uint16(effect.ExpectCount) > frozenMaxBarEvents {
			return nil, fmt.Errorf("core: barrier event count overflow")
		}
		after.events += effect.ExpectCount
	} else if effect.Kind == isa.BarrierWait {
		if effect.Arrive || !effect.Wait || effect.Sync || effect.ReleaseByCoordinator != effect.Wait ||
			effect.ExpectCount != 0 || effect.Phase != (effect.ParticipantCount&1 != 0) ||
			effect.SizeMinusOne != uint8((uint32(effect.ParticipantCount)-1)&0x1f) {
			// A WAIT consumes only bit zero as phase. The remaining operand
			// bits are preserved in the effect for validation but do not form
			// a participant count in VX_bar_unit's wait branch.
			return nil, fmt.Errorf("core: malformed barrier wait request")
		}
		if effect.Phase == after.phase {
			bit := isa.WarpMask(1 << effect.WarpID)
			if after.waiters&bit != 0 {
				return nil, fmt.Errorf("core: warp %d already waits on barrier", effect.WarpID)
			}
			after.waiters |= bit
			result.Block = true
		}
	} else {
		if !effect.Arrive || effect.Kind != isa.BarrierArrive && effect.Kind != isa.BarrierSync ||
			effect.Sync != (effect.Kind == isa.BarrierSync) || effect.Wait != effect.Sync ||
			effect.ReleaseByCoordinator != effect.Wait || effect.ExpectCount != 0 {
			return nil, fmt.Errorf("core: malformed barrier arrival request")
		}
		count := effect.ParticipantCount
		if count == 0 || count > memberCount || effect.SizeMinusOne != count-1 {
			return nil, fmt.Errorf("core: barrier participant count %d is invalid for CTA size %d", count, memberCount)
		}
		if effect.Phase != (count&1 != 0) {
			return nil, fmt.Errorf("core: barrier phase does not match the participant operand")
		}
		if after.arrivalsComplete {
			return nil, fmt.Errorf("core: barrier arrivals already complete while events remain")
		}
		if after.participantCount != 0 && after.participantCount != count {
			return nil, fmt.Errorf("core: barrier participant count changed from %d to %d", after.participantCount, count)
		}
		bit := isa.WarpMask(1 << effect.WarpID)
		if after.arrivals&bit != 0 {
			return nil, fmt.Errorf("core: duplicate arrival from warp %d", effect.WarpID)
		}
		after.participantCount = count
		after.arrivals |= bit
		if effect.Sync {
			after.waiters |= bit
			result.Block = true
		}
		if uint8(maskCount(after.arrivals)) == count {
			after.arrivalsComplete = true
			if after.events == 0 {
				result.Releases = after.waiters
				result.Block = false
				after.arrivals = 0
				after.waiters = 0
				after.participantCount = 0
				after.arrivalsComplete = false
				after.phase = !after.phase
			}
		}
	}
	return &BarrierStage{owner: b, key: key, before: before, beforeValid: valid, after: after, result: result}, nil
}

// StageEventCompletion models one frozen txbar is_done event. It decrements
// exactly one event and releases waiters only after participant completion.
func (b *BarrierCoordinator) StageEventCompletion(key BarrierKey) (*BarrierStage, error) {
	if b == nil || b.ctas == nil {
		return nil, fmt.Errorf("core: nil barrier coordinator")
	}
	if err := b.validateKey(key); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	before, valid := b.records[key]
	if !valid || before.events == 0 {
		return nil, fmt.Errorf("core: barrier event count underflow")
	}
	after := before
	after.events--
	result := BarrierResult{Key: key}
	if after.events == 0 && after.arrivalsComplete {
		result.Releases = after.waiters
		after.arrivals = 0
		after.waiters = 0
		after.participantCount = 0
		after.arrivalsComplete = false
		after.phase = !after.phase
	}
	return &BarrierStage{owner: b, key: key, before: before, beforeValid: true, after: after, result: result}, nil
}

func (b *BarrierCoordinator) Snapshot(key BarrierKey) (BarrierSnapshot, error) {
	if b == nil || b.ctas == nil {
		return BarrierSnapshot{}, fmt.Errorf("core: nil barrier coordinator")
	}
	if err := b.validateKey(key); err != nil {
		return BarrierSnapshot{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	record := b.records[key]
	return snapshotBarrier(key, record), nil
}

func snapshotBarrier(key BarrierKey, record barrierRecord) BarrierSnapshot {
	return BarrierSnapshot{Key: key, Arrivals: record.arrivals, Waiters: record.waiters,
		ParticipantCount: record.participantCount, Events: record.events,
		Phase: record.phase, ArrivalsComplete: record.arrivalsComplete}
}

func (b *BarrierCoordinator) PhaseView(ctaID uint32) (state.BarrierPhaseView, error) {
	view, err := b.view(ctaID)
	return view.phases, err
}

func (b *BarrierCoordinator) view(ctaID uint32) (barrierView, error) {
	result := barrierView{ctaID: ctaID}
	cta, err := b.ctas.Snapshot(ctaID)
	if err != nil {
		return result, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, member := range cta.Members {
		for id := uint8(0); id < frozenBarrierCount; id++ {
			key := BarrierKey{CTAID: ctaID, AddressWarp: member.WarpID, ID: id}
			record := b.records[key]
			result.phases[member.WarpID][id] = record.phase
			result.records[member.WarpID][id] = snapshotBarrier(key, record)
		}
	}
	return result, nil
}

func (b *BarrierCoordinator) keyForEffect(effect isa.BarrierEffect) (BarrierKey, uint8, error) {
	if effect.WarpID >= isa.FrozenWarpCount || effect.AddressWarp >= isa.FrozenWarpCount || effect.ID >= frozenBarrierCount {
		return BarrierKey{}, 0, fmt.Errorf("core: barrier address exceeds frozen topology")
	}
	if effect.Global {
		return BarrierKey{}, 0, fmt.Errorf("core: global barrier is unsupported by the frozen single-Core USE_GBAR=0 configuration")
	}
	if !effect.DrainLSU || !effect.ReleaseByCoordinator && effect.Wait {
		return BarrierKey{}, 0, fmt.Errorf("core: barrier drain/release metadata is invalid")
	}
	source, err := b.ctas.ViewForWarp(effect.WarpID)
	if err != nil {
		return BarrierKey{}, 0, fmt.Errorf("core: barrier source membership: %w", err)
	}
	address, err := b.ctas.ViewForWarp(effect.AddressWarp)
	if err != nil || address.ID != source.ID {
		return BarrierKey{}, 0, fmt.Errorf("core: barrier AddressWarp %d is not in source CTA %d", effect.AddressWarp, source.ID)
	}
	return BarrierKey{CTAID: source.ID, AddressWarp: effect.AddressWarp, ID: effect.ID}, uint8(source.Size), nil
}

func (b *BarrierCoordinator) validateKey(key BarrierKey) error {
	if key.AddressWarp >= isa.FrozenWarpCount || key.ID >= frozenBarrierCount {
		return fmt.Errorf("core: barrier key exceeds frozen topology")
	}
	cta, err := b.ctas.Snapshot(key.CTAID)
	if err != nil {
		return err
	}
	for _, member := range cta.Members {
		if member.WarpID == key.AddressWarp {
			return nil
		}
	}
	return fmt.Errorf("core: barrier AddressWarp %d is not a member of CTA %d", key.AddressWarp, key.CTAID)
}

func maskCount(mask isa.WarpMask) int {
	count := 0
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if mask.Active(id) {
			count++
		}
	}
	return count
}
