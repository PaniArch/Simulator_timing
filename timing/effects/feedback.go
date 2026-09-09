package effects

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// latchFeedback captures resolved producer values. Branch operands resolve at
// execute; scheduler-owned trap CSR values resolve at their feedback edge.
func latchFeedback(token model.Token, decoded isa.Decoded, e isa.InstructionEffects) *model.SchedulerFeedback {
	f := model.SchedulerFeedback{Token: token}
	switch {
	case decoded.Control == isa.ControlWarpSpawn:
		f.Kind = model.FeedbackSpawn
		if e.WarpSpawn != nil {
			f.Targets = uint8(e.WarpSpawn.Targets)
			f.TargetPC = e.WarpSpawn.TargetPC
		}
	case token.Branch:
		f.Kind = model.FeedbackBranch
	case decoded.Control == isa.ControlSplit:
		f.Kind = model.FeedbackSplit
	case decoded.Control == isa.ControlJoin:
		f.Kind = model.FeedbackJoin
	case len(e.WarpMasks) != 0:
		f.Kind = model.FeedbackTMC
	case decoded.Control == isa.ControlWarpSync:
		f.Kind = model.FeedbackWake
	default:
		return nil
	}
	if e.Control != nil && e.Control.Reason != isa.PCSequential {
		f.UpdatePC, f.PC = true, e.Control.NextPC
	}
	for _, mask := range e.WarpMasks {
		if mask.WarpID == token.Warp {
			f.UpdateMask, f.Mask = true, uint8(mask.Mask)
		}
	}
	if e.Trap != nil && e.Trap.Kind == isa.TrapReturn && e.Trap.RestoresThreadMask {
		f.UpdateMask, f.Mask = true, uint8(e.Trap.RestoreThreadMask)
	}
	return &f
}

// Feedback previews already-resolved, old-edge producer values. It is read-only
// so the runner can include them in the SAME Core proposal as their visible
// functional delivery. JOIN uses its registered split_join deadline. Observe
// owns receipt validation and advancement; Scheduler owns duplicate rejection.
func (c *Concurrent) Feedback(cycle uint64, branch, control model.Signal, singleActive ...bool) ([]model.SchedulerFeedback, error) {
	if c.failed {
		return nil, fmt.Errorf("concurrent effects require reset")
	}
	var result []model.SchedulerFeedback
	for _, signal := range []model.Signal{branch, control} {
		if !signal.Valid {
			continue
		}
		a, err := c.lookup(signal.Token)
		if err != nil {
			return nil, err
		}
		i := a.current
		if a.isTrap() {
			snapshot, err := a.owner.Snapshot()
			if err != nil {
				return nil, err
			}
			e, err := a.trapEffects(snapshot)
			if err != nil {
				return nil, err
			}
			result = append(result, *latchFeedback(i.token, i.decoded, e))
			continue
		}
		if i.feedback != nil && i.decoded.Control != isa.ControlJoin && i.decoded.Control != isa.ControlWarpSpawn {
			result = append(result, *i.feedback)
		}
	}
	for _, key := range c.keys() {
		i := c.entries[key].current
		if i.spawnAt != nil && cycle >= *i.spawnAt && (len(singleActive) == 0 || singleActive[0]) {
			if _, err := c.entries[key].selectedSpawnTargets(); err != nil {
				return nil, err
			}
			if i.feedback == nil {
				return nil, fmt.Errorf("invalid registered spawn deadline")
			}
			result = append(result, *i.feedback)
		}
		if i.joinAt != nil && cycle >= *i.joinAt {
			if cycle != *i.joinAt || i.feedback == nil {
				return nil, fmt.Errorf("invalid registered JOIN deadline")
			}
			result = append(result, *i.feedback)
		}
	}
	return result, nil
}

func (a *Adapter) isTrap() bool {
	return a.current != nil && (a.current.decoded.Control == isa.ControlTrap || a.current.decoded.Control == isa.ControlTrapReturn)
}

// Trap operands are token context; MTVEC/MEPC and saved mask are scheduler
// registers sampled at branch feedback, not when the ALU recognizes the opcode.
func (a *Adapter) trapEffects(snapshot state.WarpSnapshot) (isa.InstructionEffects, error) {
	i := a.current
	if !a.isTrap() || i.capture == nil || !i.evaluated {
		return isa.InstructionEffects{}, fmt.Errorf("trap feedback before execution")
	}
	if a.stream != nil {
		var err error
		snapshot, err = snapshot.WithInstructionContext(a.instructionContext())
		if err != nil {
			return isa.InstructionEffects{}, err
		}
	}
	return i.capture.EvaluateAt(snapshot, state.ReadContext{})
}

func (a *Adapter) refreshTrap(cycle uint64, snapshot state.WarpSnapshot) error {
	i := a.current
	if i.trapRefreshed && i.trapCycle == cycle {
		return nil
	}
	if i.delivery != nil && (i.delivery.Delivered(state.ControlEvent) || i.delivery.Delivered(state.WritebackEvent)) {
		return fmt.Errorf("late or repeated trap refresh")
	}
	e, err := a.trapEffects(snapshot)
	if err != nil {
		return err
	}
	delivery, err := a.newDelivery(e)
	if err != nil {
		return err
	}
	i.delivery, i.feedback = delivery, latchFeedback(i.token, i.decoded, e)
	i.trapCycle, i.trapRefreshed = cycle, true
	return nil
}
