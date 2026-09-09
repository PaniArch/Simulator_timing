package effects

import (
	"fmt"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// latchFeedback keeps only the resolved functional result, captured at execute.
// Later feedback must not infer a redirect from the then-current WarpState.
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
	return &f
}

// Feedback previews already-resolved, old-edge producer values. It is read-only
// so the runner can include them in the SAME Core proposal as their visible
// functional delivery. JOIN uses its registered split_join deadline. Observe
// owns receipt validation and advancement; Scheduler owns duplicate rejection.
func (c *Concurrent) Feedback(cycle uint64, branch, control model.Signal) ([]model.SchedulerFeedback, error) {
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
		if i.feedback != nil && i.decoded.Control != isa.ControlJoin && i.decoded.Control != isa.ControlWarpSpawn {
			result = append(result, *i.feedback)
		}
	}
	for _, key := range c.keys() {
		i := c.entries[key].current
		if i.spawnAt != nil && cycle >= *i.spawnAt {
			if cycle != *i.spawnAt || i.feedback == nil {
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
