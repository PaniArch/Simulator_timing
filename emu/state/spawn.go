package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

// WarpSpawnTarget identifies one existing canonical owner selected by a
// WSPAWN effect. The Core supplies the complete target set; State validates it
// against the staged effect before preparing any mutation.
type WarpSpawnTarget struct {
	WarpID uint8
	Owner  *WarpState
	// Expected is the detached target image observed before the source Warp
	// instruction began. It prevents an intervening target mutation from being
	// silently adopted as WSPAWN initialization input.
	Expected WarpSnapshot
}

type warpSpawnCandidate struct {
	owner  *WarpState
	before WarpState
	after  WarpState
}

// WarpSpawnStage is an all-or-nothing transaction spanning the source
// instruction stage and every target WarpState. It contains detached state
// images and performs no mutation until Commit.
type WarpSpawnStage struct {
	source    *EffectStage
	targets   []warpSpawnCandidate
	committed bool
}

// StageWarpSpawn validates the exact frozen WSPAWN contract and prepares a
// coordinated source/target replacement. Only target PC, lane0 mask/running
// lifecycle, and mscratch differ in each target candidate.
func StageWarpSpawn(source *EffectStage, targets []WarpSpawnTarget) (*WarpSpawnStage, error) {
	if source == nil || source.owner == nil {
		return nil, fmt.Errorf("state: nil WSPAWN source stage")
	}
	if source.committed {
		return nil, fmt.Errorf("state: WSPAWN source stage was already committed")
	}
	if !source.requiresExternal {
		return nil, fmt.Errorf("state: WSPAWN source stage has no external prerequisite")
	}
	if *source.owner != source.before {
		return nil, fmt.Errorf("state: WSPAWN source stage is stale because canonical state changed")
	}
	forwarded := source.forwarded
	if forwarded.WarpSpawn == nil || hasForwardedExceptSpawn(forwarded) {
		return nil, fmt.Errorf("state: WSPAWN stage requires exactly one forwarded spawn effect")
	}
	spawn := *forwarded.WarpSpawn
	if err := validateFrozenSpawn(source.owner.id, spawn); err != nil {
		return nil, err
	}
	if source.before.lifecycle != WarpRunning || source.before.activeMask == 0 ||
		spawn.MScratch != source.before.trapCSRs.MScratch {
		return nil, fmt.Errorf("state: WSPAWN source lifecycle, mask, or mscratch view is stale")
	}
	expectedSource := source.before
	expectedSource.pc += 4
	if source.after != expectedSource {
		return nil, fmt.Errorf("state: WSPAWN source candidate contains non-sequential or extra local mutation")
	}

	expectedCount := 0
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if spawn.Targets&(1<<id) != 0 {
			expectedCount++
		}
	}
	if len(targets) != expectedCount {
		return nil, fmt.Errorf("state: WSPAWN got %d targets, effect requires %d", len(targets), expectedCount)
	}

	seenOwners := map[*WarpState]struct{}{source.owner: {}}
	seenIDs := make(map[uint8]struct{}, len(targets))
	candidates := make([]warpSpawnCandidate, 0, len(targets))
	for index, target := range targets {
		if target.WarpID >= isa.FrozenWarpCount || spawn.Targets&(1<<target.WarpID) == 0 {
			return nil, fmt.Errorf("state: WSPAWN target %d at entry %d is not selected", target.WarpID, index)
		}
		if target.Owner == nil {
			return nil, fmt.Errorf("state: nil WSPAWN target owner at entry %d", index)
		}
		if _, duplicate := seenIDs[target.WarpID]; duplicate {
			return nil, fmt.Errorf("state: duplicate WSPAWN target id %d", target.WarpID)
		}
		if _, duplicate := seenOwners[target.Owner]; duplicate {
			return nil, fmt.Errorf("state: duplicate WSPAWN target owner for id %d", target.WarpID)
		}
		if target.Owner.id != target.WarpID {
			return nil, fmt.Errorf("state: WSPAWN target id %d references warp %d", target.WarpID, target.Owner.id)
		}
		current, _ := target.Owner.Snapshot()
		if current != target.Expected || target.Expected.WarpID() != target.WarpID {
			return nil, fmt.Errorf("state: WSPAWN target warp %d changed since source issue", target.WarpID)
		}
		if target.Owner.lifecycle != WarpInactive || target.Owner.activeMask != 0 {
			return nil, fmt.Errorf("state: WSPAWN target warp %d is not inactive", target.WarpID)
		}
		seenIDs[target.WarpID] = struct{}{}
		seenOwners[target.Owner] = struct{}{}
		before := *target.Owner
		after := before
		after.pc = spawn.TargetPC
		after.activeMask = spawn.InitialLaneMask
		after.lifecycle = WarpRunning
		after.trapCSRs.MScratch = spawn.MScratch
		candidates = append(candidates, warpSpawnCandidate{owner: target.Owner, before: before, after: after})
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if spawn.Targets&(1<<id) != 0 {
			if _, found := seenIDs[id]; !found {
				return nil, fmt.Errorf("state: WSPAWN target set is missing warp %d", id)
			}
		}
	}
	return &WarpSpawnStage{source: source, targets: candidates}, nil
}

// Commit first checks every source and target for staleness, then installs all
// already-validated candidates. There is no fallible operation after the
// first assignment, so a failure cannot partially advance the source or
// activate a subset of targets.
func (s *WarpSpawnStage) Commit() error {
	if s == nil || s.source == nil {
		return fmt.Errorf("state: nil WSPAWN stage")
	}
	if s.committed || s.source.committed {
		return fmt.Errorf("state: WSPAWN stage was already committed")
	}
	if s.source.owner == nil || *s.source.owner != s.source.before {
		return fmt.Errorf("state: WSPAWN source stage is stale because canonical state changed")
	}
	for _, target := range s.targets {
		if target.owner == nil || *target.owner != target.before {
			return fmt.Errorf("state: WSPAWN target stage is stale because canonical state changed")
		}
	}
	*s.source.owner = s.source.after
	for _, target := range s.targets {
		*target.owner = target.after
	}
	s.source.committed = true
	s.committed = true
	return nil
}

func validateFrozenSpawn(sourceID uint8, spawn isa.WarpSpawnEffect) error {
	if spawn.SourceWarp != sourceID || sourceID >= isa.FrozenWarpCount {
		return fmt.Errorf("state: WSPAWN source id is inconsistent")
	}
	if !spawn.Targets.Valid() || spawn.Targets&(1<<sourceID) != 0 {
		return fmt.Errorf("state: WSPAWN target mask is invalid or contains source")
	}
	if spawn.RequestedCount > 7 {
		return fmt.Errorf("state: WSPAWN requested count exceeds its three-bit encoding")
	}
	var expected isa.WarpMask
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if id < spawn.RequestedCount && id != sourceID {
			expected |= 1 << id
		}
	}
	if spawn.Targets != expected {
		return fmt.Errorf("state: WSPAWN target mask %#x does not match count %d/source %d", spawn.Targets, spawn.RequestedCount, sourceID)
	}
	if spawn.TargetPC&3 != 0 || spawn.InitialLaneMask != 1 || !spawn.CopyMScratch ||
		!spawn.RequiresSingleActiveWarp || !spawn.ReleaseSourceAfterApply {
		return fmt.Errorf("state: WSPAWN PC, lane0 mask, mscratch copy, or gate contract is invalid")
	}
	return nil
}

func hasForwardedExceptSpawn(effects isa.InstructionEffects) bool {
	return len(effects.MemoryRequests) != 0 || effects.Ordering != nil || len(effects.CSRReads) != 0 ||
		len(effects.CSRWrites) != 0 || len(effects.WarpDrains) != 0 || len(effects.Barriers) != 0 ||
		len(effects.PackedLoads) != 0 || len(effects.Faults) != 0
}
