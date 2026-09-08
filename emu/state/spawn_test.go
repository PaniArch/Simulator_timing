package state_test

import (
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func spawnCatalogWord(t *testing.T) uint32 {
	t.Helper()
	for _, entry := range isa.Catalog() {
		if entry.Name == "wspawn" {
			return entry.Example
		}
	}
	t.Fatal("wspawn catalog entry not found")
	return 0
}

func spawnInitial(id uint8, running bool) state.WarpInitial {
	lanes := make([]state.LaneInitial, isa.FrozenLaneCount)
	for lane := range lanes {
		lanes[lane].ID = uint8(lane)
	}
	initial := state.WarpInitial{
		Topology: state.FrozenTopology(), WarpID: id,
		PC: 0x100 + uint32(id)*0x20, Lifecycle: state.WarpInactive,
		TrapCSRs: state.TrapCSRState{MScratch: 0x80 + uint32(id)},
		Lanes:    lanes,
	}
	if running {
		initial.Lifecycle, initial.ActiveMask = state.WarpRunning, isa.AllLanes
	}
	return initial
}

func spawnOwner(t *testing.T, initial state.WarpInitial) *state.WarpState {
	t.Helper()
	owner, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func makeSpawnStage(t *testing.T) (*state.EffectStage, isa.WarpSpawnEffect, *state.WarpState) {
	t.Helper()
	initial := spawnInitial(3, true)
	decoded, err := isa.Decode(spawnCatalogWord(t))
	if err != nil {
		t.Fatal(err)
	}
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[decoded.Sources[0].Index] = 3
		initial.Lanes[lane].GPR[decoded.Sources[1].Index] = 0x300
	}
	source := spawnOwner(t, initial)
	snapshot, _ := source.Snapshot()
	effects, err := snapshot.Evaluate(decoded, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := source.StageEffects(effects)
	if err != nil {
		t.Fatal(err)
	}
	return stage, *effects.WarpSpawn, source
}

func targetDescriptors(owners ...*state.WarpState) []state.WarpSpawnTarget {
	result := make([]state.WarpSpawnTarget, len(owners))
	for index, owner := range owners {
		snapshot, _ := owner.Snapshot()
		result[index] = state.WarpSpawnTarget{WarpID: snapshot.WarpID(), Owner: owner, Expected: snapshot}
	}
	return result
}

func TestWarpSpawnStageRejectsTargetActivationFailureWithoutMutation(t *testing.T) {
	sourceStage, _, source := makeSpawnStage(t)
	target0 := spawnOwner(t, spawnInitial(0, false))
	target1 := spawnOwner(t, spawnInitial(1, true))
	target2 := spawnOwner(t, spawnInitial(2, false))
	sourceBefore, _ := source.Snapshot()
	target0Before, _ := target0.Snapshot()
	target1Before, _ := target1.Snapshot()
	target2Before, _ := target2.Snapshot()

	if _, err := state.StageWarpSpawn(sourceStage, targetDescriptors(target0, target1, target2)); err == nil {
		t.Fatal("running target accepted")
	}
	sourceAfter, _ := source.Snapshot()
	target0After, _ := target0.Snapshot()
	target1After, _ := target1.Snapshot()
	target2After, _ := target2.Snapshot()
	if sourceAfter != sourceBefore || target0After != target0Before || target1After != target1Before || target2After != target2Before {
		t.Fatal("failed WSPAWN staging changed source or a target")
	}
}

func TestWarpSpawnCommitStalenessNeverPartiallyActivates(t *testing.T) {
	t.Run("target-stale", func(t *testing.T) {
		sourceStage, _, source := makeSpawnStage(t)
		target0 := spawnOwner(t, spawnInitial(0, false))
		target1 := spawnOwner(t, spawnInitial(1, false))
		target2 := spawnOwner(t, spawnInitial(2, false))
		sourceBefore, _ := source.Snapshot()
		target0Before, _ := target0.Snapshot()
		target2Before, _ := target2.Snapshot()
		transaction, err := state.StageWarpSpawn(sourceStage, targetDescriptors(target0, target1, target2))
		if err != nil {
			t.Fatal(err)
		}
		if err := target1.SetPC(0x280); err != nil {
			t.Fatal(err)
		}
		injected, _ := target1.Snapshot()
		if err := transaction.Commit(); err == nil {
			t.Fatal("stale target transaction committed")
		}
		sourceAfter, _ := source.Snapshot()
		target0After, _ := target0.Snapshot()
		target1After, _ := target1.Snapshot()
		target2After, _ := target2.Snapshot()
		if sourceAfter != sourceBefore || target0After != target0Before || target1After != injected || target2After != target2Before {
			t.Fatal("stale target failure partially committed WSPAWN")
		}
	})

	t.Run("source-stale", func(t *testing.T) {
		sourceStage, _, source := makeSpawnStage(t)
		target0 := spawnOwner(t, spawnInitial(0, false))
		target1 := spawnOwner(t, spawnInitial(1, false))
		target2 := spawnOwner(t, spawnInitial(2, false))
		target0Before, _ := target0.Snapshot()
		target1Before, _ := target1.Snapshot()
		target2Before, _ := target2.Snapshot()
		transaction, err := state.StageWarpSpawn(sourceStage, targetDescriptors(target0, target1, target2))
		if err != nil {
			t.Fatal(err)
		}
		if err := source.SetPC(0x240); err != nil {
			t.Fatal(err)
		}
		injected, _ := source.Snapshot()
		if err := transaction.Commit(); err == nil {
			t.Fatal("stale source transaction committed")
		}
		sourceAfter, _ := source.Snapshot()
		target0After, _ := target0.Snapshot()
		target1After, _ := target1.Snapshot()
		target2After, _ := target2.Snapshot()
		if sourceAfter != injected || target0After != target0Before || target1After != target1Before || target2After != target2Before {
			t.Fatal("stale source failure partially activated targets")
		}
	})
}

func TestSpawnDeliveryUsesLiveSourceAndOriginalTargets(t *testing.T) {
	_, _, source := makeSpawnStage(t)
	decoded, _ := isa.Decode(spawnCatalogWord(t))
	before, _ := source.Snapshot()
	effects, err := before.Evaluate(decoded, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := source.NewEffectDelivery(effects)
	if err != nil {
		t.Fatal(err)
	}
	targets := []*state.WarpState{spawnOwner(t, spawnInitial(0, false)), spawnOwner(t, spawnInitial(1, false)), spawnOwner(t, spawnInitial(2, false))}
	descriptors := targetDescriptors(targets...)
	// A stale target must reject the entire control event, without consuming it.
	if err = targets[1].SetPC(0x500); err != nil {
		t.Fatal(err)
	}
	if err = delivery.DeliverWarpSpawn(descriptors); err == nil {
		t.Fatal("stale target accepted")
	}
	if delivery.Delivered(state.ControlEvent) {
		t.Fatal("failed spawn consumed receipt")
	}
	sourceAfter, _ := source.Snapshot()
	target0, _ := targets[0].Snapshot()
	if sourceAfter != before || target0 != descriptors[0].Expected {
		t.Fatal("partial spawn mutation")
	}
	// The caller explicitly refreshes pre-issue context for this owner-level retry.
	descriptors = targetDescriptors(targets...)
	marker := isa.Register{File: isa.Integer, Index: 25}
	if err = source.WriteRegister(marker, 1, isa.LaneValues{0xabcdef}); err != nil {
		t.Fatal(err)
	}
	if err = delivery.DeliverWarpSpawn(descriptors); err != nil {
		t.Fatal(err)
	}
	sourceAfter, _ = source.Snapshot()
	values, _ := sourceAfter.ReadRegister(marker)
	if sourceAfter.PC() != before.PC()+4 || values[0] != 0xabcdef {
		t.Fatal("spawn restored old source")
	}
	for _, target := range targets {
		s, _ := target.Snapshot()
		if s.PC() != 0x300 || s.ActiveMask() != 1 || s.Lifecycle() != state.WarpRunning {
			t.Fatal("target not initialized", s)
		}
	}
	if err = delivery.DeliverWarpSpawn(descriptors); err == nil {
		t.Fatal("spawn replay accepted")
	}
}
