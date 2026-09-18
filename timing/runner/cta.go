package runner

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

func (r *MultiRunner) warpDispatchable(warp uint8) bool {
	if r == nil || r.failed || !r.core.WarpDispatchable(warp) || r.parked[warp] {
		return false
	}
	s, err := r.owners[warp].Snapshot()
	return err == nil && s.Lifecycle() == state.WarpInactive && s.ActiveMask() == 0
}

// WarpQuiescent includes byte-service tails and effect receipts as well as the
// hardware pipeline. It is a lifecycle condition, not physical dispatch readiness
// and not a WSYNC/BAR gate.
func (r *MultiRunner) WarpQuiescent(warp uint8) bool {
	if r == nil || r.failed || warp >= 4 || !r.core.WarpQuiescent(warp) {
		return false
	}
	if r.hierarchy != nil && r.hierarchy.warpPending(warp) {
		return false
	}
	if _, ok := r.blocked[warp]; ok {
		return false
	}
	for _, t := range r.releases {
		if t.Warp == warp {
			return false
		}
	}
	for _, p := range r.effects.Pending() {
		if p.Token.Warp == warp {
			return false
		}
	}
	for _, q := range [][]request{r.fetch, r.loads} {
		for _, p := range q {
			if p.token.Warp == warp {
				return false
			}
		}
	}
	for _, response := range []model.Response{r.fetchResponse, r.response} {
		if response.Valid && response.Warp == warp {
			return false
		}
	}
	s, err := r.owners[warp].Snapshot()
	return err == nil && s.Lifecycle() == state.WarpInactive && s.ActiveMask() == 0
}

// DispatchWarp initializes an available canonical owner and its scheduler slot.
// Call between Run intervals, or from the observer after an edge. This is a
// normal residency boundary and preserves every unrelated pipeline/service.
func (r *MultiRunner) DispatchWarp(warp uint8, startupPC, parameter uint32, mask isa.LaneMask, firstUse bool) error {
	if r.cacheFlush != nil {
		return fmt.Errorf("finish FlushCaches before changing residency")
	}
	if !r.warpDispatchable(warp) {
		return fmt.Errorf("CTA dispatch requires available Warp slot")
	}
	stage, err := state.StageWarpLaunch([]state.WarpLaunchTarget{{WarpID: warp, Owner: r.owners[warp], StartupPC: startupPC, ParameterAddress: parameter, ActiveMask: mask, FirstUse: firstUse}})
	if err != nil {
		return err
	}
	result := stage.Results()[0]
	proposal, err := r.core.DispatchWarp(warp, result.PC, uint8(result.ActiveMask))
	if err != nil {
		return err
	}
	// No callbacks or concurrent mutation are permitted inside this owner
	// operation: both proposals were validated from the same boundary state.
	if err = model.CommitEdge(proposal); err != nil {
		return err
	}
	if err = stage.Commit(); err != nil {
		r.failed = true
		return err
	}
	r.effects.ActivateWarp(warp)
	r.stopped = false
	r.recoveryEvents = append(r.recoveryEvents, StageEvent{Resource: "scheduler", Kind: "cta-dispatch", Token: model.Token{Warp: warp, Epoch: r.epoch, PC: result.PC, Mask: uint8(mask)}, Reason: "residency"})
	return nil
}
