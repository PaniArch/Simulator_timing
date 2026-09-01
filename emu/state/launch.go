package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

// FrozenCTAReentryBytes is the five-instruction per-CTA dispatch window used
// by VX_scheduler when a warp is reused within one kernel.
const FrozenCTAReentryBytes = uint32(20)

// WarpLaunchTarget describes one existing canonical owner selected by a CTA
// dispatch. FirstUse selects the kernel startup PC; reuse rewinds the owner's
// completed PC by FrozenCTAReentryBytes. No register namespace is replaced.
type WarpLaunchTarget struct {
	WarpID           uint8
	Owner            *WarpState
	StartupPC        uint32
	ActiveMask       isa.LaneMask
	ParameterAddress uint32
	FirstUse         bool
}

// WarpLaunchResult is a detached observation of one staged launch transition.
type WarpLaunchResult struct {
	WarpID     uint8
	PC         uint32
	ActiveMask isa.LaneMask
	FirstUse   bool
}

type warpLaunchCandidate struct {
	owner  *WarpState
	before WarpState
	after  WarpState
	result WarpLaunchResult
}

// WarpLaunchStage is an all-or-nothing transition over all WarpState owners in
// one CTA. It changes only PC, active mask/lifecycle, and mscratch, matching the
// scheduler's CTA dispatch path.
type WarpLaunchStage struct {
	targets   []warpLaunchCandidate
	committed bool
}

// StageWarpLaunch validates every target and prepares detached candidates
// without mutating any canonical owner.
func StageWarpLaunch(targets []WarpLaunchTarget) (*WarpLaunchStage, error) {
	if len(targets) == 0 || len(targets) > int(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("state: CTA launch has invalid target count %d", len(targets))
	}
	seenIDs := make(map[uint8]struct{}, len(targets))
	seenOwners := make(map[*WarpState]struct{}, len(targets))
	candidates := make([]warpLaunchCandidate, len(targets))
	for index, target := range targets {
		if target.WarpID >= isa.FrozenWarpCount || target.Owner == nil {
			return nil, fmt.Errorf("state: CTA launch target %d has invalid warp or owner", index)
		}
		if target.Owner.id != target.WarpID {
			return nil, fmt.Errorf("state: CTA launch target id %d references warp %d", target.WarpID, target.Owner.id)
		}
		if _, duplicate := seenIDs[target.WarpID]; duplicate {
			return nil, fmt.Errorf("state: CTA launch repeats warp id %d", target.WarpID)
		}
		if _, duplicate := seenOwners[target.Owner]; duplicate {
			return nil, fmt.Errorf("state: CTA launch repeats a canonical owner")
		}
		if target.StartupPC&3 != 0 {
			return nil, fmt.Errorf("state: CTA startup PC %#x is not four-byte aligned", target.StartupPC)
		}
		if !target.ActiveMask.Valid() || target.ActiveMask == 0 {
			return nil, fmt.Errorf("state: CTA launch warp %d has invalid active mask %#x", target.WarpID, target.ActiveMask)
		}
		if target.Owner.lifecycle != WarpInactive || target.Owner.activeMask != 0 {
			return nil, fmt.Errorf("state: CTA launch warp %d is not canonically inactive", target.WarpID)
		}

		before := *target.Owner
		after := before
		pc := before.pc - FrozenCTAReentryBytes
		if target.FirstUse {
			pc = target.StartupPC
		}
		after.pc = pc
		after.activeMask = target.ActiveMask
		after.lifecycle = WarpRunning
		after.trapCSRs.MScratch = target.ParameterAddress
		candidates[index] = warpLaunchCandidate{
			owner: target.Owner, before: before, after: after,
			result: WarpLaunchResult{WarpID: target.WarpID, PC: pc, ActiveMask: target.ActiveMask, FirstUse: target.FirstUse},
		}
		seenIDs[target.WarpID] = struct{}{}
		seenOwners[target.Owner] = struct{}{}
	}
	return &WarpLaunchStage{targets: candidates}, nil
}

// Results returns detached staged launch observations in target order.
func (s *WarpLaunchStage) Results() []WarpLaunchResult {
	if s == nil {
		return nil
	}
	result := make([]WarpLaunchResult, len(s.targets))
	for index, target := range s.targets {
		result[index] = target.result
	}
	return result
}

// Commit rejects any stale owner before installing all prepared candidates.
// Once assignment begins, no fallible work remains.
func (s *WarpLaunchStage) Commit() error {
	if s == nil || len(s.targets) == 0 {
		return fmt.Errorf("state: nil CTA launch stage")
	}
	if s.committed {
		return fmt.Errorf("state: CTA launch stage was already committed")
	}
	for _, target := range s.targets {
		if target.owner == nil || *target.owner != target.before {
			return fmt.Errorf("state: CTA launch stage is stale because warp state changed")
		}
	}
	for _, target := range s.targets {
		*target.owner = target.after
	}
	s.committed = true
	return nil
}
