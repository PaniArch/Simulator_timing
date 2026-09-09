package effects

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// BindSpawn supplies explicit target owners. Standalone adapters retain the
// single-active/pre-issue snapshot contract; Concurrent binds owners while other
// warps run and initializes from live images at the scheduler activation edge.
func (a *Adapter) BindSpawn(active isa.WarpMask, targets []state.WarpSpawnTarget) error {
	if a.failed || a.current != nil {
		return fmt.Errorf("bind spawn while idle")
	}
	snapshot, err := a.owner.Snapshot()
	if err != nil {
		return err
	}
	if active&^isa.AllWarps != 0 || active&(1<<snapshot.WarpID()) == 0 || a.stream == nil && active != isa.WarpMask(1<<snapshot.WarpID()) {
		return fmt.Errorf("invalid spawn active mask")
	}
	var seen isa.WarpMask
	for _, target := range targets {
		if target.WarpID >= 4 || target.Owner == nil || seen.Active(target.WarpID) {
			return fmt.Errorf("invalid or duplicate spawn owner")
		}
		seen |= 1 << target.WarpID
	}
	a.spawnTargets = append([]state.WarpSpawnTarget(nil), targets...)
	a.spawnBound = true
	return nil
}

// SpawnBinding explicitly supplies existing owners and their pre-issue images.
type SpawnBinding struct {
	// Pool binds eligible owners; the issued operand mask selects the subset.
	Pool    bool
	Active  isa.WarpMask
	Targets []state.WarpSpawnTarget
}

func (c *Concurrent) BeginSpawn(token model.Token, binding SpawnBinding) error {
	decoded, err := isa.Decode(token.Word)
	if err != nil {
		return err
	}
	if decoded.Control != isa.ControlWarpSpawn {
		return fmt.Errorf("binding requires WSPAWN")
	}
	return c.begin(token, &binding)
}

func (a *Adapter) selectedSpawnTargets() ([]state.WarpSpawnTarget, error) {
	if !a.spawnPool {
		return a.spawnTargets, nil
	}
	if a.current == nil || a.current.feedback == nil {
		return nil, fmt.Errorf("spawn targets before operand evaluation")
	}
	mask := a.current.feedback.Targets
	var targets []state.WarpSpawnTarget
	for _, target := range a.spawnTargets {
		if mask&(1<<target.WarpID) != 0 {
			targets = append(targets, target)
			mask &^= 1 << target.WarpID
		}
	}
	if mask != 0 {
		return nil, fmt.Errorf("WSPAWN target outside bound CTA membership")
	}
	return targets, nil
}
