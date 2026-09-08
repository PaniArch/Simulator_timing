package state

import (
	"fmt"
	"vortex.local/simulator/isa"
)

// VisibilityEvent selects an explicitly scheduled effect group. It is not a
// pipeline clock or an automatic retirement policy; the timing adapter decides
// which hardware/model observation authorizes each group.
type VisibilityEvent uint8

const (
	WritebackEvent VisibilityEvent = iota
	ControlEvent
	CSREvent
	FFlagsEvent
	MemoryEvent
	FaultEvent
	visibilityEventCount
)

// EffectDelivery retains immutable effects and delivery receipts, not a delayed
// WarpState candidate. Every event creates and commits a fresh owner transaction
// synchronously. Consequently an earlier control/CSR event cannot be overwritten
// later by a stale whole-instruction replacement. Cancellation never rolls back
// already-visible effects. The timing adapter owns residency/epoch validation.
type EffectDelivery struct {
	owner     *WarpState
	effects   isa.InstructionEffects
	delivered [visibilityEventCount]bool
	writes    isa.LaneMask
	cancelled bool
}

func (w *WarpState) NewEffectDelivery(effects isa.InstructionEffects) (*EffectDelivery, error) {
	// Preserve complete legacy validation at creation, but discard the candidate.
	if _, err := w.StageEffects(effects); err != nil {
		return nil, err
	}
	return &EffectDelivery{owner: w, effects: cloneEffects(effects)}, nil
}
func (d *EffectDelivery) Cancel() { d.cancelled = true }
func (d *EffectDelivery) Delivered(event VisibilityEvent) bool {
	return event < visibilityEventCount && d.delivered[event]
}
func (d *EffectDelivery) WrittenLanes() isa.LaneMask { return d.writes }

// Deliver applies one event exactly once (or one disjoint WB lane subset).
// The callback is required only for a forwarded group and must honor the
// existing all-or-error external-owner contract. Failure records no receipt.
func (d *EffectDelivery) Deliver(event VisibilityEvent, lanes isa.LaneMask, external func(isa.InstructionEffects) error) error {
	if d == nil || d.owner == nil || d.cancelled {
		return fmt.Errorf("inactive effect delivery")
	}
	if event >= visibilityEventCount || d.delivered[event] {
		return fmt.Errorf("unknown or repeated visibility event")
	}
	if event != WritebackEvent && lanes != 0 {
		return fmt.Errorf("lane subset only valid for writeback")
	}
	e := cloneEffects(d.effects)
	part := isa.InstructionEffects{}
	expected := isa.LaneMask(0)
	switch event {
	case WritebackEvent:
		for _, w := range e.RegisterWrites {
			expected |= w.Mask
		}
		if !lanes.Valid() || lanes & ^expected != 0 || lanes&d.writes != 0 || expected != 0 && lanes == 0 {
			return fmt.Errorf("invalid or repeated writeback coverage")
		}
		for _, w := range e.RegisterWrites {
			w.Mask &= lanes
			if w.Mask != 0 {
				part.RegisterWrites = append(part.RegisterWrites, w)
			}
		}
	case ControlEvent:
		part.Control = e.Control
		part.Trap = e.Trap
		part.WarpMasks = e.WarpMasks
		part.Divergence = e.Divergence
		part.WarpSpawn = e.WarpSpawn
		part.WarpDrains = e.WarpDrains
		part.Barriers = e.Barriers
		if e.Trap != nil {
			part.CSRWrites = e.CSRWrites
		}
	case CSREvent:
		if e.Trap != nil {
			return fmt.Errorf("trap CSR writes belong to control event")
		}
		part.CSRReads = e.CSRReads
		part.CSRWrites = e.CSRWrites
	case FFlagsEvent:
		part.FFlags = e.FFlags
	case MemoryEvent:
		part.MemoryRequests = e.MemoryRequests
		part.PackedLoads = e.PackedLoads
		part.Ordering = e.Ordering
	case FaultEvent:
		part.Faults = e.Faults
	}
	stage, err := d.owner.StageEffects(part)
	if err != nil {
		return err
	}
	if stage.RequiresExternalSuccess() {
		if external == nil {
			return fmt.Errorf("visibility event requires external owner")
		}
		err = stage.CommitForwardedWithExternal(func() error { return external(stage.ForwardedEffects()) })
	} else {
		if external != nil {
			return fmt.Errorf("local event cannot invoke an external owner")
		}
		err = stage.Commit()
	}
	if err != nil {
		return err
	}
	if event == WritebackEvent {
		d.writes |= lanes
		d.delivered[event] = d.writes == expected
	} else {
		d.delivered[event] = true
	}
	return nil
}

// DeliverWarpSpawn commits the control event and all target initializations in
// the existing atomic owner transaction. Target Expected images must come from
// before instruction issue. No source candidate survives across timing edges.
func (d *EffectDelivery) DeliverWarpSpawn(targets []WarpSpawnTarget) error {
	if d == nil || d.owner == nil || d.cancelled || d.delivered[ControlEvent] || d.effects.WarpSpawn == nil {
		return fmt.Errorf("inactive, repeated or non-spawn control delivery")
	}
	e := cloneEffects(d.effects)
	part := isa.InstructionEffects{Control: e.Control, WarpSpawn: e.WarpSpawn, Trap: e.Trap,
		WarpMasks: e.WarpMasks, Divergence: e.Divergence, WarpDrains: e.WarpDrains, Barriers: e.Barriers}
	if e.Trap != nil {
		part.CSRWrites = e.CSRWrites
	}
	source, err := d.owner.StageEffects(part)
	if err != nil {
		return err
	}
	transaction, err := StageWarpSpawn(source, targets)
	if err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	d.delivered[ControlEvent] = true
	return nil
}
