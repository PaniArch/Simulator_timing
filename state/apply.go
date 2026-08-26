package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

// EffectValidationError identifies a deterministic pre-commit rejection. No
// canonical state has changed when this error is returned.
type EffectValidationError struct {
	Field   string
	Problem string
}

func (e *EffectValidationError) Error() string {
	return fmt.Sprintf("state: invalid %s effect: %s", e.Field, e.Problem)
}

func effectError(field, format string, args ...any) error {
	return &EffectValidationError{Field: field, Problem: fmt.Sprintf(format, args...)}
}

// EffectStage is a completely validated candidate transaction. The candidate
// is detached from canonical state. Future-owner effects are retained until an
// orchestrator reports their success and calls CommitAfterExternal.
type EffectStage struct {
	owner            *WarpState
	before           WarpState
	after            WarpState
	forwarded        isa.InstructionEffects
	requiresExternal bool
	committed        bool
}

// RequiresExternalSuccess reports whether local mutation is conditional on a
// future Memory/Core/CTA/Barrier/Fault/ordering owner accepting its effects.
func (s *EffectStage) RequiresExternalSuccess() bool {
	return s != nil && s.requiresExternal
}

// ForwardedEffects returns a detached copy of all effects not consumed by the
// Lane/Warp owner. Mutating the returned bundle cannot alter the stage.
func (s *EffectStage) ForwardedEffects() isa.InstructionEffects {
	if s == nil {
		return isa.InstructionEffects{}
	}
	return cloneEffects(s.forwarded)
}

// Commit applies a local-only stage. It refuses stages which still require a
// future owner's explicit success.
func (s *EffectStage) Commit() error {
	if s == nil {
		return fmt.Errorf("state: nil effect stage")
	}
	if s.requiresExternal {
		return fmt.Errorf("state: effect stage requires external owner success")
	}
	return s.commit()
}

// CommitWithExternal atomically coordinates one synchronous external owner
// operation with a local-only stage. Staleness is checked before apply, so a
// rejected stage never calls the external owner. Once apply succeeds, the
// already-validated local replacement cannot fail. If apply reports failure,
// the canonical owner is restored to the exact pre-apply image; the external
// operation's own contract must likewise be all-or-error.
func (s *EffectStage) CommitWithExternal(apply func() error) error {
	if s == nil {
		return fmt.Errorf("state: nil effect stage")
	}
	if apply == nil {
		return fmt.Errorf("state: nil external apply callback")
	}
	if s.requiresExternal {
		return fmt.Errorf("state: coordinated commit requires a local-only effect stage")
	}
	if s.committed {
		return fmt.Errorf("state: effect stage was already committed")
	}
	if s.owner == nil || *s.owner != s.before {
		return fmt.Errorf("state: effect stage is stale because canonical state changed")
	}
	if err := apply(); err != nil {
		*s.owner = s.before
		return err
	}
	*s.owner = s.after
	s.committed = true
	return nil
}

// CommitAfterExternal applies a stage after its caller has successfully
// committed every forwarded prerequisite. A stale stage is still rejected.
func (s *EffectStage) CommitAfterExternal() error {
	if s == nil {
		return fmt.Errorf("state: nil effect stage")
	}
	if !s.requiresExternal {
		return fmt.Errorf("state: effect stage has no external prerequisite")
	}
	return s.commit()
}

// CommitForwardedWithExternal atomically coordinates a forwarded-effect stage
// with its synchronous external owner. Staleness is checked before apply, so
// the callback is never invoked for an obsolete Warp candidate. The callback
// must provide an all-or-error contract; after it succeeds the prevalidated
// Warp replacement is infallible.
func (s *EffectStage) CommitForwardedWithExternal(apply func() error) error {
	if s == nil {
		return fmt.Errorf("state: nil effect stage")
	}
	if apply == nil {
		return fmt.Errorf("state: nil external apply callback")
	}
	if !s.requiresExternal {
		return fmt.Errorf("state: coordinated forwarded commit requires an external prerequisite")
	}
	if s.committed {
		return fmt.Errorf("state: effect stage was already committed")
	}
	if s.owner == nil || *s.owner != s.before {
		return fmt.Errorf("state: effect stage is stale because canonical state changed")
	}
	if err := apply(); err != nil {
		return err
	}
	*s.owner = s.after
	s.committed = true
	return nil
}

func (s *EffectStage) commit() error {
	if s.committed {
		return fmt.Errorf("state: effect stage was already committed")
	}
	if s.owner == nil || *s.owner != s.before {
		return fmt.Errorf("state: effect stage is stale because canonical state changed")
	}
	*s.owner = s.after
	s.committed = true
	return nil
}

// ApplyResult describes the immediate outcome. Pending is non-nil exactly when
// local mutation was withheld for future-owner success.
type ApplyResult struct {
	Committed        bool
	AwaitingExternal bool
	Forwarded        isa.InstructionEffects
	Pending          *EffectStage
}

// ApplyEffects validates the entire instruction bundle before any mutation.
// Local-only bundles commit immediately. Bundles with future-owner
// prerequisites return a pending stage and leave canonical state untouched.
func (w *WarpState) ApplyEffects(effects isa.InstructionEffects) (ApplyResult, error) {
	stage, err := w.StageEffects(effects)
	if err != nil {
		return ApplyResult{}, err
	}
	forwarded := stage.ForwardedEffects()
	if stage.RequiresExternalSuccess() {
		return ApplyResult{AwaitingExternal: true, Forwarded: forwarded, Pending: stage}, nil
	}
	if err := stage.Commit(); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Committed: true, Forwarded: forwarded}, nil
}

// StageEffects performs complete pre-validation and builds a detached next
// state. It never mutates w.
func (w *WarpState) StageEffects(effects isa.InstructionEffects) (*EffectStage, error) {
	if w == nil {
		return nil, fmt.Errorf("state: nil warp")
	}
	owned := cloneEffects(effects)
	forwarded, requiresExternal, err := w.classifyAndValidateForwarded(owned)
	if err != nil {
		return nil, err
	}
	if len(owned.Faults) != 0 && hasLocalMutation(owned) {
		return nil, effectError("bundle", "fault routing cannot coexist with original local mutation")
	}

	candidate := *w
	if err := stageRegisterWrites(&candidate, owned.RegisterWrites); err != nil {
		return nil, err
	}
	if err := stageFFlags(&candidate, owned.FFlags); err != nil {
		return nil, err
	}
	if err := w.validateCSRReads(owned.CSRReads); err != nil {
		return nil, err
	}
	localCSRWrites, err := w.localCSRWrites(owned.CSRWrites)
	if err != nil {
		return nil, err
	}
	if err := stageCSRWrites(w, &candidate, localCSRWrites); err != nil {
		return nil, err
	}
	if err := stageControlMaskDivergenceTrap(w, &candidate, owned); err != nil {
		return nil, err
	}

	return &EffectStage{
		owner: w, before: *w, after: candidate, forwarded: forwarded,
		requiresExternal: requiresExternal,
	}, nil
}

func hasLocalMutation(effects isa.InstructionEffects) bool {
	if len(effects.RegisterWrites) != 0 || effects.Control != nil || effects.FFlags != nil ||
		effects.Trap != nil || len(effects.WarpMasks) != 0 || effects.Divergence != nil {
		return true
	}
	for _, write := range effects.CSRWrites {
		if write.Scope == isa.CSRScopeWarp || (write.Scope == isa.CSRScopeConstant && write.Ignored) {
			return true
		}
	}
	return false
}

func stageRegisterWrites(candidate *WarpState, writes []isa.RegisterWriteEffect) error {
	seen := make(map[isa.Register]struct{}, len(writes))
	for index, write := range writes {
		if err := validateRegister(write.Destination); err != nil {
			return effectError("register-write", "entry %d: %v", index, err)
		}
		if !write.Mask.Valid() {
			return effectError("register-write", "entry %d mask %#x exceeds frozen lanes", index, write.Mask)
		}
		if _, duplicate := seen[write.Destination]; duplicate {
			return effectError("register-write", "duplicate destination namespace=%d index=%d", write.Destination.File, write.Destination.Index)
		}
		seen[write.Destination] = struct{}{}
		if write.Destination.File == isa.Integer && write.Destination.Index == 0 {
			continue
		}
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			if !write.Mask.Active(lane) {
				continue
			}
			if write.Destination.File == isa.Integer {
				candidate.lanes[lane].gpr[write.Destination.Index] = write.Values[lane]
			} else {
				candidate.lanes[lane].fpr[write.Destination.Index] = write.Values[lane]
			}
		}
	}
	return nil
}

func stageFFlags(candidate *WarpState, effect *isa.FFlagsEffect) error {
	if effect == nil {
		return nil
	}
	if uint8(effect.Accumulate)&^uint8(0x1f) != 0 {
		return effectError("fflags", "accumulation %#x exceeds five implemented bits", effect.Accumulate)
	}
	candidate.fcsr |= uint8(effect.Accumulate)
	return nil
}

func (w *WarpState) validateCSRReads(reads []isa.CSRReadEffect) error {
	for index, read := range reads {
		entry, ok := csrEntry(read.Address)
		if !ok {
			return effectError("csr-read", "entry %d has unknown address %#x", index, read.Address)
		}
		if read.Scope != entry.Scope {
			return effectError("csr-read", "entry %d address %#x scope=%d, want %d", index, read.Address, read.Scope, entry.Scope)
		}
		if value, owned := w.csrReadValue(read.Address); owned {
			for lane, got := range read.Values {
				if got != value {
					return effectError("csr-read", "entry %d lane %d stale value %#x, canonical value is %#x", index, lane, got, value)
				}
			}
		}
	}
	return nil
}

func (w *WarpState) localCSRWrites(writes []isa.CSRWriteEffect) ([]isa.CSRWriteEffect, error) {
	local := make([]isa.CSRWriteEffect, 0, len(writes))
	seen := make(map[uint16]struct{}, len(writes))
	fcsrWrites := 0
	for index, write := range writes {
		entry, ok := csrEntry(write.Address)
		if !ok {
			return nil, effectError("csr-write", "entry %d has unknown address %#x", index, write.Address)
		}
		if write.WarpID != w.id {
			return nil, effectError("csr-write", "entry %d targets warp %d, owner is warp %d", index, write.WarpID, w.id)
		}
		if write.Scope != entry.Scope || write.WriteMask != entry.WriteMask || write.Ignored != entry.WriteIgnored {
			return nil, effectError("csr-write", "entry %d metadata does not match catalog for address %#x", index, write.Address)
		}
		if !entry.Writable && !entry.WriteIgnored {
			return nil, effectError("csr-write", "entry %d writes read-only address %#x", index, write.Address)
		}
		if write.Value&^write.WriteMask != 0 {
			return nil, effectError("csr-write", "entry %d value %#x exceeds write mask %#x", index, write.Value, write.WriteMask)
		}
		if _, duplicate := seen[write.Address]; duplicate {
			return nil, effectError("csr-write", "duplicate address %#x", write.Address)
		}
		seen[write.Address] = struct{}{}
		if write.Address >= 0x001 && write.Address <= 0x003 {
			fcsrWrites++
			if fcsrWrites > 1 {
				return nil, effectError("csr-write", "multiple software writes alias the same FCSR storage")
			}
		}
		if write.Scope == isa.CSRScopeWarp || (write.Scope == isa.CSRScopeConstant && write.Ignored) {
			local = append(local, write)
		}
	}
	return local, nil
}

func stageCSRWrites(before *WarpState, candidate *WarpState, writes []isa.CSRWriteEffect) error {
	for index, write := range writes {
		old, ok := before.csrValue(write.Address)
		if !ok {
			// Catalog-validated constant ignored writes have no storage.
			if !write.Ignored || write.OldValue != 0 || write.Value != 0 {
				return effectError("csr-write", "entry %d ignored write is not a zero no-op", index)
			}
			continue
		}
		if write.OldValue != old {
			return effectError("csr-write", "entry %d stale old value %#x, canonical value is %#x", index, write.OldValue, old)
		}
		candidate.setCSRValue(write.Address, write.Value)
	}
	return nil
}

func (w *WarpState) csrValue(address uint16) (uint32, bool) {
	switch address {
	case 0x001:
		return uint32(w.fcsr & 0x1f), true
	case 0x002:
		return uint32(w.fcsr>>5) & 7, true
	case 0x003:
		return uint32(w.fcsr), true
	case 0x300:
		return w.trapCSRs.MStatus, true
	case 0x305:
		return w.trapCSRs.MTVec, true
	case 0x340:
		return w.trapCSRs.MScratch, true
	case 0x341:
		return w.trapCSRs.MEPC, true
	case 0x342:
		return w.trapCSRs.MCause, true
	case 0x343:
		return w.trapCSRs.MTVal, true
	default:
		return 0, false
	}
}

func (w *WarpState) csrReadValue(address uint16) (uint32, bool) {
	if value, ok := w.csrValue(address); ok {
		return value, true
	}
	switch address {
	case 0xcc1:
		return uint32(w.id), true
	case 0xcc4:
		return uint32(w.activeMask), true
	default:
		return 0, false
	}
}

func (w *WarpState) setCSRValue(address uint16, value uint32) {
	switch address {
	case 0x001:
		w.fcsr = w.fcsr&^0x1f | uint8(value)
	case 0x002:
		w.fcsr = w.fcsr&0x1f | uint8(value<<5)
	case 0x003:
		w.fcsr = uint8(value)
	case 0x300:
		w.trapCSRs.MStatus = value
	case 0x305:
		w.trapCSRs.MTVec = value
	case 0x340:
		w.trapCSRs.MScratch = value
	case 0x341:
		w.trapCSRs.MEPC = value
	case 0x342:
		w.trapCSRs.MCause = value
	case 0x343:
		w.trapCSRs.MTVal = value
	}
}

func stageControlMaskDivergenceTrap(before, candidate *WarpState, effects isa.InstructionEffects) error {
	if len(effects.WarpMasks) > 1 {
		return effectError("warp-mask", "bundle contains %d replacements", len(effects.WarpMasks))
	}
	var mask *isa.WarpMaskEffect
	if len(effects.WarpMasks) == 1 {
		mask = &effects.WarpMasks[0]
		if mask.WarpID != before.id {
			return effectError("warp-mask", "targets warp %d, owner is warp %d", mask.WarpID, before.id)
		}
		if !mask.Mask.Valid() || mask.Active != (mask.Mask != 0) {
			return effectError("warp-mask", "mask %#x and active=%t are inconsistent", mask.Mask, mask.Active)
		}
		if mask.Reason > isa.WarpMaskJoin {
			return effectError("warp-mask", "unknown reason %d", mask.Reason)
		}
	}

	if effects.Trap != nil && (effects.Divergence != nil || mask != nil || len(effects.RegisterWrites) != 0 || effects.FFlags != nil) {
		return effectError("bundle", "trap cannot coexist with register, FFLAGS, warp-mask, or divergence mutation")
	}
	if err := stageDivergence(before, candidate, effects.Divergence, mask, effects.Control); err != nil {
		return err
	}
	if effects.Divergence == nil && mask != nil {
		if mask.Reason != isa.WarpMaskTMC && mask.Reason != isa.WarpMaskPredicate {
			return effectError("warp-mask", "split/join reason requires a divergence effect")
		}
		if effects.Control == nil || effects.Control.Reason != isa.PCSequential {
			return effectError("warp-mask", "TMC/PRED replacement requires sequential control")
		}
		candidate.activeMask = mask.Mask
		candidate.lifecycle = lifecycleFor(mask.Active)
	}
	if effects.Divergence == nil && effects.Control != nil && effects.Control.Reason == isa.PCReconverge {
		return effectError("control", "reconvergence requires a divergence transition")
	}
	if err := stageTrap(before, candidate, effects.Trap, effects.Control, effects.CSRWrites); err != nil {
		return err
	}
	if err := validateControl(before, effects.Control); err != nil {
		return err
	}
	if effects.Control != nil {
		candidate.pc = effects.Control.NextPC
	}
	return nil
}

func lifecycleFor(active bool) WarpLifecycle {
	if active {
		return WarpRunning
	}
	return WarpInactive
}

func stageDivergence(before, candidate *WarpState, effect *isa.DivergenceEffect, mask *isa.WarpMaskEffect, control *isa.ControlEffect) error {
	if effect == nil {
		return nil
	}
	if effect.WarpID != before.id {
		return effectError("divergence", "targets warp %d, owner is warp %d", effect.WarpID, before.id)
	}
	if effect.StackPointer > FrozenDivergenceDepth || !effect.OriginalMask.Valid() || !effect.ExecuteMask.Valid() || !effect.DeferredMask.Valid() {
		return effectError("divergence", "pointer or lane mask exceeds frozen capacity")
	}
	if effect.Action > isa.DivergenceJoin {
		return effectError("divergence", "unknown action %d", effect.Action)
	}
	if control == nil {
		return effectError("divergence", "missing control effect")
	}

	switch effect.Action {
	case isa.DivergenceSplit:
		if effect.StackPointer != before.divergence.writePointer || effect.OriginalMask != before.activeMask {
			return effectError("divergence", "SPLIT pointer/mask is stale")
		}
		if effect.MarkElseVisited || effect.Pop || effect.Push != effect.Divergent {
			return effectError("divergence", "SPLIT action flags are contradictory")
		}
		if control.Reason != isa.PCSequential || effect.ReconvergencePC != before.pc+4 ||
			effect.ExecuteMask&effect.DeferredMask != 0 || effect.ExecuteMask|effect.DeferredMask != effect.OriginalMask {
			return effectError("divergence", "SPLIT control, PC, or mask partition is inconsistent")
		}
		if !effect.Divergent {
			if mask != nil || effect.ExecuteMask != 0 && effect.DeferredMask != 0 {
				return effectError("divergence", "uniform SPLIT cannot replace the active mask")
			}
			return nil
		}
		if before.divergence.writePointer >= FrozenDivergenceDepth || effect.ReconvergencePC&3 != 0 ||
			effect.ExecuteMask == 0 || effect.DeferredMask == 0 ||
			effect.ExecuteMask&effect.DeferredMask != 0 {
			return effectError("divergence", "SPLIT push has invalid capacity, PC, or mask partition")
		}
		if mask == nil || mask.Reason != isa.WarpMaskSplit || mask.Mask != effect.ExecuteMask || !mask.Active {
			return effectError("divergence", "SPLIT mask effect does not match execute mask")
		}
		pointer := before.divergence.writePointer
		candidate.divergence.records[pointer] = divergenceRecord{
			valid: true, originalMask: effect.OriginalMask, nextPC: effect.ReconvergencePC,
		}
		candidate.divergence.writePointer++
		candidate.activeMask, candidate.lifecycle = mask.Mask, WarpRunning

	case isa.DivergenceJoin:
		if effect.Push || effect.MarkElseVisited == effect.Pop && effect.Divergent {
			return effectError("divergence", "JOIN action flags are contradictory")
		}
		if !effect.Divergent {
			if effect.StackPointer != before.divergence.writePointer || effect.OriginalMask != before.activeMask ||
				effect.ExecuteMask != 0 || effect.DeferredMask != 0 || effect.ReconvergencePC != 0 ||
				effect.MarkElseVisited || effect.Pop || mask != nil || control.Reason != isa.PCSequential {
				return effectError("divergence", "non-divergent JOIN metadata is inconsistent")
			}
			return nil
		}
		if effect.StackPointer >= FrozenDivergenceDepth {
			return effectError("divergence", "JOIN pointer %d has no record row", effect.StackPointer)
		}
		record := before.divergence.records[effect.StackPointer]
		if !record.valid || effect.OriginalMask != record.originalMask || effect.ReconvergencePC != record.nextPC {
			return effectError("divergence", "JOIN record view is stale")
		}
		if mask == nil || mask.Reason != isa.WarpMaskJoin || mask.Mask != effect.ExecuteMask || mask.Active != (effect.ExecuteMask != 0) {
			return effectError("divergence", "JOIN mask effect does not match execute mask")
		}
		if effect.MarkElseVisited {
			want := record.originalMask &^ before.activeMask
			if record.elseVisited || effect.ExecuteMask != want || effect.ExecuteMask == 0 || control.Reason != isa.PCReconverge || control.NextPC != record.nextPC {
				return effectError("divergence", "JOIN mark-else transition is invalid")
			}
			candidate.divergence.records[effect.StackPointer].elseVisited = true
		} else {
			if !record.elseVisited || effect.StackPointer+1 != before.divergence.writePointer || effect.ExecuteMask != record.originalMask || control.Reason != isa.PCSequential {
				return effectError("divergence", "JOIN pop transition is invalid")
			}
			candidate.divergence.records[effect.StackPointer] = divergenceRecord{}
			candidate.divergence.writePointer--
		}
		candidate.activeMask = mask.Mask
		candidate.lifecycle = lifecycleFor(mask.Active)
	}
	return nil
}

func stageTrap(before, candidate *WarpState, trap *isa.TrapEffect, control *isa.ControlEffect, writes []isa.CSRWriteEffect) error {
	if trap == nil {
		if control != nil && (control.Reason == isa.PCTrap || control.Reason == isa.PCTrapReturn) {
			return effectError("trap", "trap control is missing its trap effect")
		}
		return nil
	}
	if !trap.SaveThreadMask.Valid() || !trap.RestoreThreadMask.Valid() || control == nil {
		return effectError("trap", "mask exceeds frozen lanes or control is missing")
	}
	switch trap.Kind {
	case isa.TrapEnter:
		expectedVector := before.trapCSRs.MTVec &^ 3
		if control.Reason != isa.PCTrap || trap.EPC != before.pc ||
			trap.Vector != expectedVector || control.NextPC != expectedVector || control.Target != expectedVector ||
			trap.SaveThreadMask != before.activeMask || trap.SaveThreadMask == 0 ||
			trap.RestoresThreadMask || trap.RestoreThreadMask != 0 {
			return effectError("trap", "entry PC/vector/mask metadata is inconsistent")
		}
		if !trapEntryWritesMatch(writes, before.id, trap) {
			return effectError("trap", "entry requires exact MEPC/MCAUSE/MTVAL writes")
		}
		candidate.savedThreadMask = trap.SaveThreadMask
	case isa.TrapReturn:
		if control.Reason != isa.PCTrapReturn || trap.EPC != before.trapCSRs.MEPC || control.NextPC != trap.EPC&^3 || trap.SaveThreadMask != 0 || len(writes) != 0 {
			return effectError("trap", "return PC/CSR metadata is inconsistent")
		}
		if trap.RestoresThreadMask {
			if trap.RestoreThreadMask == 0 || trap.RestoreThreadMask != before.savedThreadMask {
				return effectError("trap", "return restore mask is stale or empty")
			}
			candidate.activeMask, candidate.lifecycle = trap.RestoreThreadMask, WarpRunning
		} else if trap.RestoreThreadMask != 0 || before.savedThreadMask != 0 {
			return effectError("trap", "return omitted a nonzero saved mask")
		}
	default:
		return effectError("trap", "unknown kind %d", trap.Kind)
	}
	return nil
}

func trapEntryWritesMatch(writes []isa.CSRWriteEffect, warpID uint8, trap *isa.TrapEffect) bool {
	if len(writes) != 3 {
		return false
	}
	want := map[uint16]uint32{0x341: trap.EPC, 0x342: trap.Cause, 0x343: 0}
	for _, write := range writes {
		value, ok := want[write.Address]
		if !ok || write.Scope != isa.CSRScopeWarp || write.WarpID != warpID || write.Value != value {
			return false
		}
		delete(want, write.Address)
	}
	return len(want) == 0
}

func validateControl(before *WarpState, control *isa.ControlEffect) error {
	if control == nil {
		return nil
	}
	if control.CurrentPC != before.pc || control.NextPC&3 != 0 {
		return effectError("control", "current PC is stale or next PC %#x is misaligned", control.NextPC)
	}
	decisionActive := control.DecisionLane < isa.FrozenLaneCount && before.activeMask.Active(control.DecisionLane)
	switch control.Reason {
	case isa.PCSequential:
		if control.Taken || control.NextPC != control.CurrentPC+4 {
			return effectError("control", "sequential relationship is inconsistent")
		}
	case isa.PCBranch:
		if !decisionActive || control.NextPC != choosePC(control.Taken, control.Target, control.CurrentPC+4) || control.Taken && control.Target&3 != 0 {
			return effectError("control", "branch decision lane or target relationship is inconsistent")
		}
	case isa.PCJump:
		if !decisionActive || !control.Taken || control.NextPC != control.Target || control.Target&3 != 0 {
			return effectError("control", "jump decision lane or target relationship is inconsistent")
		}
	case isa.PCTrap, isa.PCTrapReturn:
		if !control.Taken || control.NextPC != control.Target || control.Target&3 != 0 {
			return effectError("control", "trap target relationship is inconsistent")
		}
	case isa.PCReconverge:
		if !decisionActive || !control.Taken || control.NextPC != control.Target || control.Target&3 != 0 {
			return effectError("control", "reconvergence target relationship is inconsistent")
		}
	default:
		return effectError("control", "unknown reason %d", control.Reason)
	}
	return nil
}

func choosePC(taken bool, target, sequential uint32) uint32 {
	if taken {
		return target
	}
	return sequential
}

func (w *WarpState) classifyAndValidateForwarded(effects isa.InstructionEffects) (isa.InstructionEffects, bool, error) {
	forwarded := isa.InstructionEffects{
		MemoryRequests: append([]isa.MemoryRequest(nil), effects.MemoryRequests...),
		Ordering:       clonePointer(effects.Ordering),
		CSRReads:       append([]isa.CSRReadEffect(nil), effects.CSRReads...),
		WarpSpawn:      clonePointer(effects.WarpSpawn),
		WarpDrains:     append([]isa.WarpDrainEffect(nil), effects.WarpDrains...),
		Barriers:       append([]isa.BarrierEffect(nil), effects.Barriers...),
		PackedLoads:    append([]isa.PackedLoadRequest(nil), effects.PackedLoads...),
		Faults:         append([]isa.FaultEffect(nil), effects.Faults...),
	}
	for _, write := range effects.CSRWrites {
		if write.Scope != isa.CSRScopeWarp && !(write.Scope == isa.CSRScopeConstant && write.Ignored) {
			forwarded.CSRWrites = append(forwarded.CSRWrites, write)
		}
	}

	for index, request := range effects.MemoryRequests {
		if request.Lane >= isa.FrozenLaneCount {
			return isa.InstructionEffects{}, false, effectError("memory-request", "entry %d lane %d exceeds frozen lanes", index, request.Lane)
		}
	}
	if effects.Ordering != nil && (effects.Ordering.Predecessor&^0xf != 0 || effects.Ordering.Successor&^0xf != 0) {
		return isa.InstructionEffects{}, false, effectError("ordering", "I/O/R/W mask exceeds four bits")
	}
	if effects.WarpSpawn != nil {
		spawn := effects.WarpSpawn
		if spawn.SourceWarp != w.id || !spawn.Targets.Valid() || !spawn.InitialLaneMask.Valid() || spawn.TargetPC&3 != 0 {
			return isa.InstructionEffects{}, false, effectError("warp-spawn", "source, target mask, lane mask, or PC exceeds frozen topology")
		}
	}
	for index, drain := range effects.WarpDrains {
		if drain.WarpID != w.id || drain.Kind > isa.DrainLSU {
			return isa.InstructionEffects{}, false, effectError("warp-drain", "entry %d has wrong warp or unknown kind", index)
		}
	}
	for index, barrier := range effects.Barriers {
		if barrier.WarpID != w.id || barrier.AddressWarp >= isa.FrozenWarpCount || barrier.ID >= isa.FrozenBarrierCount || barrier.Kind == isa.BarrierNone || barrier.Kind > isa.BarrierWait {
			return isa.InstructionEffects{}, false, effectError("barrier", "entry %d exceeds frozen identity or kind", index)
		}
	}
	for index, request := range effects.PackedLoads {
		if request.Lane >= isa.FrozenLaneCount {
			return isa.InstructionEffects{}, false, effectError("packed-load", "entry %d lane %d exceeds frozen lanes", index, request.Lane)
		}
	}
	for index, fault := range effects.Faults {
		if fault.Kind == isa.FaultNone || fault.Kind > isa.FaultStoreAccess || fault.Lane >= isa.FrozenLaneCount {
			return isa.InstructionEffects{}, false, effectError("fault", "entry %d has invalid kind or lane", index)
		}
	}

	requires := len(forwarded.MemoryRequests) != 0 || forwarded.Ordering != nil || len(forwarded.CSRWrites) != 0 ||
		forwarded.WarpSpawn != nil || len(forwarded.WarpDrains) != 0 || len(forwarded.Barriers) != 0 ||
		len(forwarded.PackedLoads) != 0 || len(forwarded.Faults) != 0
	return forwarded, requires, nil
}

func csrEntry(address uint16) (isa.CSREntry, bool) {
	for _, entry := range isa.CSRCatalog() {
		if entry.Address == address {
			return entry, true
		}
	}
	return isa.CSREntry{}, false
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneEffects(effects isa.InstructionEffects) isa.InstructionEffects {
	return isa.InstructionEffects{
		RegisterWrites: append([]isa.RegisterWriteEffect(nil), effects.RegisterWrites...),
		Control:        clonePointer(effects.Control),
		MemoryRequests: append([]isa.MemoryRequest(nil), effects.MemoryRequests...),
		Ordering:       clonePointer(effects.Ordering),
		FFlags:         clonePointer(effects.FFlags),
		CSRReads:       append([]isa.CSRReadEffect(nil), effects.CSRReads...),
		CSRWrites:      append([]isa.CSRWriteEffect(nil), effects.CSRWrites...),
		Trap:           clonePointer(effects.Trap),
		WarpMasks:      append([]isa.WarpMaskEffect(nil), effects.WarpMasks...),
		WarpSpawn:      clonePointer(effects.WarpSpawn),
		Divergence:     clonePointer(effects.Divergence),
		WarpDrains:     append([]isa.WarpDrainEffect(nil), effects.WarpDrains...),
		Barriers:       append([]isa.BarrierEffect(nil), effects.Barriers...),
		PackedLoads:    append([]isa.PackedLoadRequest(nil), effects.PackedLoads...),
		Faults:         append([]isa.FaultEffect(nil), effects.Faults...),
	}
}
