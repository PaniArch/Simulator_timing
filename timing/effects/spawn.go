package effects

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
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
