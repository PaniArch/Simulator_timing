package effects

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// BindSpawn supplies the original target owners and pre-issue snapshots. The
// caller owns residency/CTA membership and must validate those before binding;
// this adapter does not create scheduling slots or select another active warp.
// Binding is consumed by Finish/Reset. StageWarpSpawn validates exact targets,
// inactive lifecycle, source context and both sides' stale images at feedback.
func (a *Adapter) BindSpawn(active isa.WarpMask, targets []state.WarpSpawnTarget) error {
	if a.failed || a.current != nil {
		return fmt.Errorf("bind spawn while idle")
	}
	snapshot, err := a.owner.Snapshot()
	if err != nil {
		return err
	}
	if active != isa.WarpMask(1<<snapshot.WarpID()) {
		return fmt.Errorf("spawn requires exactly its source active")
	}
	a.spawnTargets = append([]state.WarpSpawnTarget(nil), targets...)
	a.spawnBound = true
	return nil
}

// SpawnBinding explicitly supplies existing owners and their pre-issue images.
type SpawnBinding struct {
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
