package state

import (
	"fmt"
	"vortex.local/simulator/isa"
)

// EffectStream owns only the control visibility frontier for one residency of
// a canonical Warp owner. Timing supplies strictly ordered instruction IDs;
// register writes/flags remain independently visible at their own events.
// Construct a new stream only after cancelling the old residency's deliveries.
// This policy is a software interface, not an RTL retirement/ROB claim.
type EffectStream struct {
	owner        *WarpState
	controlOrder uint64
}

func NewEffectStream(owner *WarpState) (*EffectStream, error) {
	if owner == nil {
		return nil, fmt.Errorf("nil effect stream owner")
	}
	return &EffectStream{owner: owner}, nil
}
func (s *EffectStream) ControlOrder() uint64 { return s.controlOrder }

// NewDelivery validates against the token's explicit context and current owner
// data, then discards the candidate. No precomputed WarpState survives to WB.
// Multiple deliveries for the same instruction order support disjoint memory
// fragments; their masks/receipts and residency must be validated by the caller.
func (s *EffectStream) NewDelivery(order uint64, context InstructionContext, effects isa.InstructionEffects) (*EffectDelivery, error) {
	if s == nil || s.owner == nil || order == 0 {
		return nil, fmt.Errorf("invalid effect stream identity")
	}
	if _, err := s.owner.stageInstructionEffects(context, effects); err != nil {
		return nil, err
	}
	return &EffectDelivery{owner: s.owner, effects: cloneEffects(effects), stream: s, order: order, instruction: context}, nil
}

// stageInstructionEffects validates control relationships against the locked
// PC/mask, while building its result from the live owner. The returned stage is
// used synchronously: Commit still compares the complete live before-image.
// Context-only overrides are restored unless that effect actually writes them.
func (w *WarpState) stageInstructionEffects(context InstructionContext, effects isa.InstructionEffects) (*EffectStage, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return nil, err
	}
	if _, err = snapshot.WithInstructionContext(context); err != nil {
		return nil, err
	}
	logical := *w
	logical.pc, logical.activeMask, logical.lifecycle = context.PC, context.Mask, WarpRunning
	stage, err := logical.StageEffects(effects)
	if err != nil {
		return nil, err
	}
	if effects.Control == nil {
		stage.after.pc = w.pc
	}
	if len(effects.WarpMasks) == 0 && effects.Divergence == nil && effects.Trap == nil {
		stage.after.activeMask, stage.after.lifecycle = w.activeMask, w.lifecycle
	}
	stage.owner, stage.before = w, *w
	return stage, nil
}

func (d *EffectDelivery) stageStreamControl(part isa.InstructionEffects) (*EffectStage, error) {
	if d.order <= d.stream.controlOrder {
		// A late ordinary completion has no remaining architectural PC effect.
		// A second or reordered control transition is not silently discarded.
		if (part.Control != nil && part.Control.Reason != isa.PCSequential) || part.Trap != nil || len(part.WarpMasks) != 0 || part.Divergence != nil || part.WarpSpawn != nil || len(part.WarpDrains) != 0 || len(part.Barriers) != 0 {
			return nil, fmt.Errorf("stale nonsequential control delivery")
		}
		part.Control = nil
		return d.owner.StageEffects(part)
	}
	return d.owner.stageInstructionEffects(d.instruction, part)
}
