package model

import "fmt"

// Restart is a software recovery boundary, not a functional PC/mask write.
// Retained older execution may continue; no old frontend or control operation
// may survive to overwrite the caller's restart context. after skips tombstones.
func (c *Core) Restart(warp uint8, context WarpContext, after uint64) (Transition, error) {
	s := c.front.scheduler
	if s == nil || warp >= 4 || after == ^uint64(0) {
		return Transition{}, fmt.Errorf("invalid restart target")
	}
	if context.Epoch != s.state.Warps[warp].Epoch || !s.state.Parked[warp] || context.Stalled || context.PC&3 != 0 || context.Mask&^uint8(15) != 0 || context.Active && context.Mask == 0 {
		return Transition{}, fmt.Errorf("invalid restart context")
	}
	if s.state.IBufferCount[warp] != 0 || s.state.DecodeUnlock.Valid && s.state.DecodeUnlock.Token.Warp == warp {
		return Transition{}, fmt.Errorf("restart retains frontend work")
	}
	for _, resource := range c.Resources() {
		for _, entry := range resource.Residents {
			if entry.Token.Warp == warp && entry.Token.WarpStall {
				return Transition{}, fmt.Errorf("restart retains control work")
			}
		}
	}
	next := s.state
	next.Warps[warp] = context
	next.Parked[warp] = false
	nextID := s.nextID
	if nextID <= after {
		nextID = after + 1
	}
	return Transition{edits: []mutation{s.rev.propose(func() { s.state = next; s.nextID = nextID })}}, nil
}
