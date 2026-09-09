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
	stream      *EffectStream
	order       uint64
	instruction InstructionContext
	owner       *WarpState
	effects     isa.InstructionEffects
	delivered   [visibilityEventCount]bool
	writes      isa.LaneMask
	cancelled   bool
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
	var stage *EffectStage
	var err error
	if d.stream != nil && event == ControlEvent {
		stage, err = d.stageStreamControl(part)
	} else {
		stage, err = d.owner.StageEffects(part)
	}
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
	if d.stream != nil && event == ControlEvent && e.Control != nil && d.order > d.stream.controlOrder {
		d.stream.controlOrder = d.order
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
	return d.deliverWarpSpawn(targets, false)
}

// DeliverWarpSpawnAtActivation uses the live target image at the scheduler's
// single-active activation edge. Only PC/mask/mscratch are initialized; older
// writebacks and service tails can retain the same owner and instruction IDs.
func (d *EffectDelivery) DeliverWarpSpawnAtActivation(targets []WarpSpawnTarget) error {
	return d.deliverWarpSpawn(targets, true)
}

func (d *EffectDelivery) deliverWarpSpawn(targets []WarpSpawnTarget, activation bool) error {
	if d != nil && d.stream != nil {
		if d.order <= d.stream.controlOrder || !activation && d.owner.pc != d.instruction.PC || d.owner.activeMask != d.instruction.Mask {
			return fmt.Errorf("stale concurrent spawn control context")
		}
	}
	if d == nil || d.owner == nil || d.cancelled || d.delivered[ControlEvent] || d.effects.WarpSpawn == nil {
		return fmt.Errorf("inactive, repeated or non-spawn control delivery")
	}
	e := cloneEffects(d.effects)
	part := isa.InstructionEffects{Control: e.Control, WarpSpawn: e.WarpSpawn, Trap: e.Trap,
		WarpMasks: e.WarpMasks, Divergence: e.Divergence, WarpDrains: e.WarpDrains, Barriers: e.Barriers}
	if e.Trap != nil {
		part.CSRWrites = e.CSRWrites
	}
	var source *EffectStage
	var err error
	var instructionPC *uint32
	if activation && d.stream != nil {
		part.WarpSpawn.MScratch = d.owner.trapCSRs.MScratch
		source, err = d.owner.stageInstructionEffects(d.instruction, part)
		instructionPC = &d.instruction.PC
		targets = append([]WarpSpawnTarget(nil), targets...)
		for n := range targets {
			if targets[n].Owner == nil {
				return fmt.Errorf("nil activation target")
			}
			targets[n].Expected, err = targets[n].Owner.Snapshot()
			if err != nil {
				return err
			}
		}
	} else {
		source, err = d.owner.StageEffects(part)
	}
	if err != nil {
		return err
	}
	transaction, err := stageWarpSpawn(source, targets, instructionPC)
	if err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return err
	}
	d.delivered[ControlEvent] = true
	if d.stream != nil {
		d.stream.controlOrder = d.order
	}
	return nil
}

// DeliverCSRWithFlags merges the two hardware CSR producers against one old
// owner image. StageEffects validates reads/RMW old values before accumulating
// flags and applying the software write to its addressed field. Both receipts
// advance only after the common transaction commits.
func (d *EffectDelivery) DeliverCSRWithFlags(flags *EffectDelivery, external func(isa.InstructionEffects) error) error {
	if flags == nil {
		return fmt.Errorf("missing combined flags delivery")
	}
	return d.deliverCSRJoint(flags, nil, external)
}

// DeliverCSRWithTrap samples both producers against one old owner. Software
// CSR writes precede hardware trap writes; trap redirects use old CSR values.
// An optional FPU flags receipt joins the same transaction.
func (d *EffectDelivery) DeliverCSRWithTrap(trap, flags *EffectDelivery, external func(isa.InstructionEffects) error) error {
	if trap == nil {
		return fmt.Errorf("missing combined trap delivery")
	}
	return d.deliverCSRJoint(flags, trap, external)
}

func (d *EffectDelivery) deliverCSRJoint(flags, trap *EffectDelivery, external func(isa.InstructionEffects) error) error {
	if d == nil || d.owner == nil || d.cancelled || d.delivered[CSREvent] || d.effects.Trap != nil {
		return fmt.Errorf("invalid or repeated combined CSR delivery")
	}
	part := isa.InstructionEffects{CSRReads: d.effects.CSRReads, CSRWrites: d.effects.CSRWrites}
	if flags != nil {
		if flags == d || flags.owner != d.owner || flags.cancelled || flags.delivered[FFlagsEvent] {
			return fmt.Errorf("invalid combined flags receipt")
		}
		part.FFlags = flags.effects.FFlags
	}
	stage, err := d.owner.StageEffects(part)
	if err != nil {
		return err
	}
	if trap != nil {
		if trap == d || trap == flags || trap.owner != d.owner || trap.cancelled || trap.delivered[ControlEvent] || trap.effects.Trap == nil {
			return fmt.Errorf("invalid combined trap receipt")
		}
		e := trap.effects
		control := isa.InstructionEffects{Control: e.Control, Trap: e.Trap, CSRWrites: e.CSRWrites}
		var controlStage *EffectStage
		if trap.stream != nil {
			controlStage, err = trap.stageStreamControl(control)
		} else {
			controlStage, err = trap.owner.StageEffects(control)
		}
		if err != nil {
			return err
		}
		if controlStage.RequiresExternalSuccess() {
			return fmt.Errorf("trap control unexpectedly forwards external effects")
		}
		// Merge only fields written by scheduler trap logic, preserving software
		// writes to MTVEC/MSTATUS and FCSR updates from the other producers.
		stage.after.pc = controlStage.after.pc
		if e.Trap.Kind == isa.TrapEnter {
			stage.after.trapCSRs.MEPC = controlStage.after.trapCSRs.MEPC
			stage.after.trapCSRs.MCause = controlStage.after.trapCSRs.MCause
			stage.after.trapCSRs.MTVal = controlStage.after.trapCSRs.MTVal
			stage.after.savedThreadMask = controlStage.after.savedThreadMask
		} else if e.Trap.RestoresThreadMask {
			stage.after.activeMask, stage.after.lifecycle = controlStage.after.activeMask, controlStage.after.lifecycle
		}
	}
	if stage.RequiresExternalSuccess() {
		if external == nil {
			return fmt.Errorf("combined CSR event requires external owner")
		}
		err = stage.CommitForwardedWithExternal(func() error { return external(stage.ForwardedEffects()) })
	} else {
		if external != nil {
			return fmt.Errorf("local CSR event cannot invoke external owner")
		}
		err = stage.Commit()
	}
	if err != nil {
		return err
	}
	d.delivered[CSREvent] = true
	if flags != nil {
		flags.delivered[FFlagsEvent] = true
	}
	if trap != nil {
		trap.delivered[ControlEvent] = true
		if trap.stream != nil && trap.order > trap.stream.controlOrder {
			trap.stream.controlOrder = trap.order
		}
	}
	return nil
}
