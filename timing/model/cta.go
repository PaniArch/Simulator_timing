package model

import "fmt"

// WarpDispatchable is the VX_cta_dispatch active_warps availability predicate.
// Old commit and pending tokens are NOT a hardware dispatch gate.
func (c *Core) WarpDispatchable(warp uint8) bool {
	if c == nil || warp >= 4 || c.front.scheduler == nil {
		return false
	}
	s := c.front.scheduler
	return !s.state.Warps[warp].Active && !(s.launch.Valid && s.launch.Token.Warp == warp)
}

// WarpQuiescent checks one slot's pipeline ownership, independently of other
// active Warps. External requests and software receipts are checked by runner.
func (c *Core) WarpQuiescent(warp uint8) bool {
	if c == nil || warp >= 4 || c.front.scheduler == nil {
		return false
	}
	if c.front.scheduler.launch.Valid && c.front.scheduler.launch.Token.Warp == warp {
		return false
	}
	s := c.front.scheduler.state
	if s.Warps[warp].Active || s.IBufferCount[warp] != 0 || s.DecodeUnlock.Valid && s.DecodeUnlock.Token.Warp == warp {
		return false
	}
	score := c.front.ScoreboardState()
	if score.Busy[warp] != 0 || score.Special[warp] != 0 {
		return false
	}
	// FU credits and locks are indexed by class, not Warp. Inspect their
	// identity ledgers so unrelated operations never hold this slot hostage.
	for class, tokens := range c.front.issue.creditTokens {
		for _, token := range tokens {
			if token.Warp == warp {
				return false
			}
		}
		if c.front.issue.locked[class] && c.front.issue.lockOwner[class].Warp == warp {
			return false
		}
	}
	for _, signal := range []Signal{c.commit.Pending(), c.alu.Branch(), c.sfu.Control(), c.fpu.Flags()} {
		if signal.Valid && signal.Token.Warp == warp {
			return false
		}
	}
	if c.account.issued.Valid && c.account.issued.Token.Warp == warp {
		return false
	}
	for _, t := range c.account.pending {
		if t.Warp == warp {
			return false
		}
	}
	for _, resource := range c.Resources() {
		for _, entry := range resource.Residents {
			if entry.Token.Warp == warp {
				return false
			}
		}
	}
	return true
}

// DispatchWarp stages the scheduler part of normal CTA activation. It does not
// reset the Core or allocate instruction identities again. The residency owner
// computes startup/reentry PC and must also initialize the canonical WarpState.
// Dispatch presents cta_fire for the next pipeline edge. It cannot make the
// Warp selectable before that edge's active/PC/mask registers are committed.
func (c *Core) DispatchWarp(warp uint8, pc uint32, mask uint8) (Transition, error) {
	if !c.WarpDispatchable(warp) || pc&3 != 0 || mask == 0 || mask&^uint8(15) != 0 {
		return Transition{}, fmt.Errorf("CTA dispatch requires inactive slot and valid PC/mask")
	}
	s := c.front.scheduler
	if s.launch.Valid {
		return Transition{}, fmt.Errorf("only one CTA Warp fire is supported per edge")
	}
	launch := Signal{Valid: true, Token: Token{Warp: warp, PC: pc, Mask: mask}}
	return Transition{edits: []mutation{s.rev.propose(func() { s.launch = launch })}}, nil
}
