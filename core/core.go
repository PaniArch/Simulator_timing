// Package core manages the frozen single-core, four-warp functional model.
// Architectural warp state remains owned exclusively by state.WarpState;
// Core stores only executor references and scheduling lifecycle metadata.
package core

import (
	"fmt"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/warp"
)

// WarpLifecycle is the Core scheduler's view of one frozen warp slot.
// Inactive and finished both correspond to an inactive canonical WarpState;
// participated distinguishes a never-launched slot from a completed one.
type WarpLifecycle uint8

const (
	WarpInactive WarpLifecycle = iota
	WarpRunnable
	WarpBlocked
	WarpFinished
)

func (l WarpLifecycle) String() string {
	switch l {
	case WarpInactive:
		return "inactive"
	case WarpRunnable:
		return "runnable"
	case WarpBlocked:
		return "blocked"
	case WarpFinished:
		return "finished"
	default:
		return fmt.Sprintf("warp-lifecycle(%d)", l)
	}
}

// BlockReason is a typed scheduler fact. It never replaces or mirrors any
// architectural WarpState field. The more specific values are routing points
// for later Core owners; this milestone directly uses explicit, deferred, and
// fault reasons.
type BlockReason uint8

const (
	BlockNone BlockReason = iota
	BlockExplicit
	BlockPendingWork
	BlockWarpSpawn
	BlockBarrier
	BlockExternalOwner
	BlockFault
)

func (r BlockReason) String() string {
	switch r {
	case BlockNone:
		return "none"
	case BlockExplicit:
		return "explicit"
	case BlockPendingWork:
		return "pending-work"
	case BlockWarpSpawn:
		return "warp-spawn"
	case BlockBarrier:
		return "barrier"
	case BlockExternalOwner:
		return "external-owner"
	case BlockFault:
		return "fault"
	default:
		return fmt.Sprintf("block-reason(%d)", r)
	}
}

func validBlockReason(reason BlockReason) bool {
	return reason >= BlockExplicit && reason <= BlockFault
}

type slot struct {
	owner        *state.WarpState
	executor     *warp.Warp
	lifecycle    WarpLifecycle
	blockReason  BlockReason
	participated bool
}

// SlotSnapshot is a detached scheduler observation. Architectural fields are
// observations read from the canonical owner, not a writable Core-side copy.
type SlotSnapshot struct {
	WarpID                uint8
	Lifecycle             WarpLifecycle
	BlockReason           BlockReason
	Participated          bool
	ArchitecturalState    state.WarpLifecycle
	ArchitecturalLaneMask isa.LaneMask
}

// Core contains exactly the four slots required by the frozen configuration.
// The array is indexed by architectural warp ID, independent of constructor
// ordering. next is only the functional round-robin cursor.
type Core struct {
	slots       [isa.FrozenWarpCount]slot
	next        uint8
	pendingWork PendingWorkProvider
}

// PendingWorkProvider supplies the functional WSYNC predicate owned outside
// Core. It reports architectural work issued before WSYNC, not pipeline cycles
// or an implied memory-visibility rule.
type PendingWorkProvider interface {
	PendingPriorWork(warpID uint8) (bool, error)
}

// PendingWorkFunc adapts a function to PendingWorkProvider.
type PendingWorkFunc func(warpID uint8) (bool, error)

func (f PendingWorkFunc) PendingPriorWork(warpID uint8) (bool, error) {
	if f == nil {
		return false, fmt.Errorf("core: nil pending-work function")
	}
	return f(warpID)
}

// New validates a complete set of existing Warp executors. Every ID in the
// frozen range must occur exactly once. No architectural state is copied into
// Core: each slot retains references to the executor and its WarpState owner.
func New(executors []*warp.Warp) (*Core, error) {
	if len(executors) != int(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("core: got %d warp slots, want frozen count %d", len(executors), isa.FrozenWarpCount)
	}
	result := &Core{}
	var seen [isa.FrozenWarpCount]bool
	for position, executor := range executors {
		if executor == nil {
			return nil, fmt.Errorf("core: nil warp executor at position %d", position)
		}
		owner := executor.CanonicalState()
		if owner == nil {
			return nil, fmt.Errorf("core: warp executor at position %d has nil canonical state", position)
		}
		snapshot, err := owner.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("core: inspect warp at position %d: %w", position, err)
		}
		id := snapshot.WarpID()
		if id >= isa.FrozenWarpCount {
			return nil, fmt.Errorf("core: warp id %d exceeds frozen count", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("core: duplicate warp id %d", id)
		}
		seen[id] = true
		lifecycle := WarpInactive
		participated := false
		if snapshot.Lifecycle() == state.WarpRunning {
			lifecycle = WarpRunnable
			participated = true
		}
		result.slots[id] = slot{
			owner: owner, executor: executor, lifecycle: lifecycle,
			participated: participated,
		}
	}
	for id, present := range seen {
		if !present {
			return nil, fmt.Errorf("core: missing warp id %d", id)
		}
	}
	if err := result.validate(); err != nil {
		return nil, err
	}
	return result, nil
}

// SetPendingWorkProvider attaches the explicit WSYNC predicate owner. A nil
// provider restores use of ReadContext.PendingPriorWork supplied to Step.
func (c *Core) SetPendingWorkProvider(provider PendingWorkProvider) error {
	if err := c.validate(); err != nil {
		return err
	}
	c.pendingWork = provider
	return nil
}

// Slot returns one detached lifecycle observation.
func (c *Core) Slot(warpID uint8) (SlotSnapshot, error) {
	if err := c.validate(); err != nil {
		return SlotSnapshot{}, err
	}
	if warpID >= isa.FrozenWarpCount {
		return SlotSnapshot{}, fmt.Errorf("core: warp id %d exceeds frozen count", warpID)
	}
	return c.snapshotSlot(warpID)
}

// Slots returns all four slots in architectural warp-ID order.
func (c *Core) Slots() ([]SlotSnapshot, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	result := make([]SlotSnapshot, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		observation, err := c.snapshotSlot(id)
		if err != nil {
			return nil, err
		}
		result[id] = observation
	}
	return result, nil
}

// Block removes a runnable warp from scheduling while leaving its canonical
// running/mask state untouched. A typed nonzero reason is mandatory.
func (c *Core) Block(warpID uint8, reason BlockReason) error {
	if !validBlockReason(reason) {
		return fmt.Errorf("core: invalid block reason %s", reason)
	}
	selected, err := c.slotFor(warpID)
	if err != nil {
		return err
	}
	if selected.lifecycle != WarpRunnable {
		return fmt.Errorf("core: cannot block warp %d in %s state", warpID, selected.lifecycle)
	}
	selected.lifecycle, selected.blockReason = WarpBlocked, reason
	return c.validateSlot(warpID)
}

// Resume makes a blocked, canonically running warp runnable again.
func (c *Core) Resume(warpID uint8) error {
	selected, err := c.slotFor(warpID)
	if err != nil {
		return err
	}
	if selected.lifecycle != WarpBlocked {
		return fmt.Errorf("core: cannot resume warp %d in %s state", warpID, selected.lifecycle)
	}
	selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
	return c.validateSlot(warpID)
}

// Activate admits an externally initialized inactive slot. The caller must
// first make the canonical WarpState running with a nonzero mask; Core only
// records the scheduling transition and never supplies launch/CTA defaults.
func (c *Core) Activate(warpID uint8) error {
	if c == nil || warpID >= isa.FrozenWarpCount {
		return fmt.Errorf("core: invalid warp id %d", warpID)
	}
	selected := &c.slots[warpID]
	if selected.lifecycle != WarpInactive || selected.participated {
		return fmt.Errorf("core: cannot activate warp %d in %s state", warpID, selected.lifecycle)
	}
	snapshot, err := selected.owner.Snapshot()
	if err != nil {
		return err
	}
	if snapshot.Lifecycle() != state.WarpRunning || snapshot.ActiveMask() == 0 {
		return fmt.Errorf("core: warp %d canonical state is not active", warpID)
	}
	selected.lifecycle, selected.blockReason, selected.participated = WarpRunnable, BlockNone, true
	return c.validateSlot(warpID)
}

// StepOutcome describes whether one Core step selected an executor.
type StepOutcome uint8

const (
	StepIdle StepOutcome = iota
	StepExecuted
)

// StepResult reports one scheduling decision and the existing Warp.Step
// observation. WarpResult is zero when Outcome is StepIdle.
type StepResult struct {
	Outcome             StepOutcome
	WarpID              uint8
	Lifecycle           WarpLifecycle
	PreviousBlockReason BlockReason
	WarpResult          warp.Result
	NextLifecycle       WarpLifecycle
	BlockReason         BlockReason
}

// Step selects exactly one runnable warp in deterministic round-robin order
// and directly invokes its existing Warp.Step. The Core-derived active-warps
// CSR view includes both runnable and blocked canonically active warps.
func (c *Core) Step(context state.ReadContext) (StepResult, error) {
	if err := c.validate(); err != nil {
		return StepResult{}, err
	}
	if err := c.refreshFunctionalWaits(context); err != nil {
		return StepResult{}, err
	}
	selectedID, ok := c.selectRunnable()
	if !ok {
		return StepResult{Outcome: StepIdle}, nil
	}
	active, err := c.activeWarpMask()
	if err != nil {
		return StepResult{}, err
	}
	ownerBefore, err := c.ownerSnapshots()
	if err != nil {
		return StepResult{}, err
	}
	context.CoreID = 0
	context.ActiveWarps = uint8(active)
	if c.pendingWork != nil {
		context.PendingPriorWork, err = c.pendingWork.PendingPriorWork(selectedID)
		if err != nil {
			return StepResult{}, fmt.Errorf("core: pending-work view for warp %d: %w", selectedID, err)
		}
	}
	c.next = (selectedID + 1) % isa.FrozenWarpCount
	selected := &c.slots[selectedID]
	beforeLifecycle, beforeBlockReason := selected.lifecycle, selected.blockReason
	warpResult := selected.executor.Step(context)

	if warpResult.Outcome == warp.OutcomeDeferred && warpResult.Decoded != nil {
		switch warpResult.Decoded.Name {
		case "wspawn":
			if err := c.coordinateWarpSpawn(selectedID, active, ownerBefore, &warpResult); err != nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult), err
			}
		case "wsync":
			if err := c.coordinateWarpSync(selectedID, context.PendingPriorWork, &warpResult); err != nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult), err
			}
		case "bar", "bar.arrive", "bar.wait":
			selected.lifecycle, selected.blockReason = WarpBlocked, BlockBarrier
		}
	}

	if warpResult.NextLifecycle == state.WarpInactive {
		selected.lifecycle, selected.blockReason = WarpFinished, BlockNone
	} else {
		switch warpResult.Outcome {
		case warp.OutcomeDeferred:
			if selected.lifecycle != WarpBlocked {
				selected.lifecycle = WarpBlocked
				selected.blockReason = deferredReason(warpResult)
			}
		case warp.OutcomeFault:
			selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
		}
	}
	if err := c.validateSlot(selectedID); err != nil {
		return StepResult{}, err
	}
	return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult), nil
}

// Complete reports Core-level completion for slots which participated in this
// Core lifecycle. Never-activated empty slots are not part of the aggregation.
func (c *Core) Complete() (bool, error) {
	if err := c.validate(); err != nil {
		return false, err
	}
	participated := false
	for id := range c.slots {
		slot := &c.slots[id]
		if !slot.participated {
			continue
		}
		participated = true
		if slot.lifecycle != WarpFinished {
			return false, nil
		}
	}
	return participated, nil
}

func (c *Core) selectRunnable() (uint8, bool) {
	for offset := uint8(0); offset < isa.FrozenWarpCount; offset++ {
		id := (c.next + offset) % isa.FrozenWarpCount
		if c.slots[id].lifecycle == WarpRunnable {
			return id, true
		}
	}
	return 0, false
}

func (c *Core) activeWarpMask() (isa.WarpMask, error) {
	var result isa.WarpMask
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		snapshot, err := c.slots[id].owner.Snapshot()
		if err != nil {
			return 0, err
		}
		if snapshot.Lifecycle() == state.WarpRunning {
			result |= 1 << id
		}
	}
	return result, nil
}

func (c *Core) ownerSnapshots() ([isa.FrozenWarpCount]state.WarpSnapshot, error) {
	var result [isa.FrozenWarpCount]state.WarpSnapshot
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		snapshot, err := c.slots[id].owner.Snapshot()
		if err != nil {
			return result, fmt.Errorf("core: snapshot warp %d: %w", id, err)
		}
		result[id] = snapshot
	}
	return result, nil
}

func (c *Core) refreshFunctionalWaits(context state.ReadContext) error {
	active, err := c.activeWarpMask()
	if err != nil {
		return err
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		selected := &c.slots[id]
		if selected.lifecycle != WarpBlocked {
			continue
		}
		switch selected.blockReason {
		case BlockWarpSpawn:
			if active == isa.WarpMask(1<<id) {
				selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
			}
		case BlockPendingWork:
			pending := context.PendingPriorWork
			if c.pendingWork != nil {
				pending, err = c.pendingWork.PendingPriorWork(id)
				if err != nil {
					return fmt.Errorf("core: pending-work view for warp %d: %w", id, err)
				}
			}
			if !pending {
				selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
			}
		}
	}
	return c.validate()
}

func (c *Core) coordinateWarpSpawn(sourceID uint8, active isa.WarpMask, ownerBefore [isa.FrozenWarpCount]state.WarpSnapshot, result *warp.Result) error {
	if result == nil || result.Effects == nil || result.Effects.WarpSpawn == nil {
		return fmt.Errorf("core: WSPAWN deferred without a spawn effect")
	}
	spawn := *result.Effects.WarpSpawn
	if err := validateWarpSpawnEffect(sourceID, spawn); err != nil {
		return err
	}
	if active != isa.WarpMask(1<<sourceID) {
		c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpBlocked, BlockWarpSpawn
		return nil
	}

	targets := make([]state.WarpSpawnTarget, 0, isa.FrozenWarpCount-1)
	for targetID := uint8(0); targetID < isa.FrozenWarpCount; targetID++ {
		if spawn.Targets&(1<<targetID) == 0 {
			continue
		}
		target := &c.slots[targetID]
		if target.lifecycle != WarpInactive || target.participated {
			return fmt.Errorf("core: WSPAWN target warp %d is not an unused inactive slot", targetID)
		}
		targets = append(targets, state.WarpSpawnTarget{WarpID: targetID, Owner: target.owner, Expected: ownerBefore[targetID]})
	}
	sourceSnapshot, err := c.slots[sourceID].owner.Snapshot()
	if err != nil {
		return err
	}
	if sourceSnapshot != ownerBefore[sourceID] {
		return fmt.Errorf("core: WSPAWN source warp %d changed since instruction issue", sourceID)
	}
	if sourceSnapshot.TrapCSRs().MScratch != spawn.MScratch {
		return fmt.Errorf("core: WSPAWN mscratch view is stale")
	}
	sourceStage, err := c.slots[sourceID].owner.StageEffects(*result.Effects)
	if err != nil {
		return fmt.Errorf("core: restage WSPAWN source: %w", err)
	}
	transaction, err := state.StageWarpSpawn(sourceStage, targets)
	if err != nil {
		return fmt.Errorf("core: stage WSPAWN transaction: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("core: commit WSPAWN transaction: %w", err)
	}
	for _, target := range targets {
		slot := &c.slots[target.WarpID]
		slot.lifecycle, slot.blockReason, slot.participated = WarpRunnable, BlockNone, true
	}
	c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpRunnable, BlockNone
	completeDeferredResult(c.slots[sourceID].owner, result)
	return c.validate()
}

func (c *Core) coordinateWarpSync(sourceID uint8, pending bool, result *warp.Result) error {
	if result == nil || result.Effects == nil || len(result.Effects.WarpDrains) != 1 {
		return fmt.Errorf("core: WSYNC deferred without exactly one drain effect")
	}
	effects := result.Effects
	drain := result.Effects.WarpDrains[0]
	if drain.WarpID != sourceID || drain.Kind != isa.DrainPriorInstructions ||
		drain.Wait != pending || !drain.ReleaseAfterDrain || effects.WarpSpawn != nil ||
		len(effects.Barriers) != 0 || len(effects.RegisterWrites) != 0 || len(effects.MemoryRequests) != 0 ||
		effects.Ordering != nil || effects.FFlags != nil || len(effects.CSRReads) != 0 || len(effects.CSRWrites) != 0 ||
		effects.Trap != nil || len(effects.WarpMasks) != 0 || effects.Divergence != nil ||
		len(effects.PackedLoads) != 0 || len(effects.Faults) != 0 || (pending && effects.Control != nil) ||
		(!pending && effects.Control == nil) {
		return fmt.Errorf("core: WSYNC drain effect violates the functional contract")
	}
	if pending {
		c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpBlocked, BlockPendingWork
		return nil
	}
	stage, err := c.slots[sourceID].owner.StageEffects(*result.Effects)
	if err != nil {
		return fmt.Errorf("core: restage WSYNC: %w", err)
	}
	forwarded := stage.ForwardedEffects()
	if forwarded.WarpSpawn != nil || len(forwarded.WarpDrains) != 1 || len(forwarded.Barriers) != 0 ||
		len(forwarded.MemoryRequests) != 0 || len(forwarded.PackedLoads) != 0 || len(forwarded.CSRWrites) != 0 ||
		len(forwarded.CSRReads) != 0 || forwarded.Ordering != nil || len(forwarded.Faults) != 0 {
		return fmt.Errorf("core: WSYNC has unexpected forwarded effects")
	}
	if err := stage.CommitAfterExternal(); err != nil {
		return fmt.Errorf("core: commit WSYNC: %w", err)
	}
	c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpRunnable, BlockNone
	completeDeferredResult(c.slots[sourceID].owner, result)
	return nil
}

func validateWarpSpawnEffect(sourceID uint8, spawn isa.WarpSpawnEffect) error {
	if spawn.SourceWarp != sourceID || sourceID >= isa.FrozenWarpCount || !spawn.Targets.Valid() ||
		spawn.Targets&(1<<sourceID) != 0 || spawn.TargetPC&3 != 0 || spawn.InitialLaneMask != 1 ||
		!spawn.CopyMScratch || !spawn.RequiresSingleActiveWarp || !spawn.ReleaseSourceAfterApply {
		return fmt.Errorf("core: invalid frozen WSPAWN source/target/PC/mask/copy/gate fields")
	}
	var expected isa.WarpMask
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if id < spawn.RequestedCount && id != sourceID {
			expected |= 1 << id
		}
	}
	if spawn.Targets != expected {
		return fmt.Errorf("core: WSPAWN targets %#x do not match count %d/source %d", spawn.Targets, spawn.RequestedCount, sourceID)
	}
	return nil
}

func completeDeferredResult(owner *state.WarpState, result *warp.Result) {
	snapshot, err := owner.Snapshot()
	if err != nil {
		return
	}
	result.Outcome = warp.OutcomeRetired
	result.Err, result.Fault = nil, nil
	result.NextPC = snapshot.PC()
	result.NextActiveMask = snapshot.ActiveMask()
	result.NextLifecycle = snapshot.Lifecycle()
	result.NextDivergencePointer = snapshot.DivergenceWritePointer()
}

func (c *Core) stepResult(selectedID uint8, lifecycle WarpLifecycle, blockReason BlockReason, result warp.Result) StepResult {
	selected := &c.slots[selectedID]
	return StepResult{
		Outcome: StepExecuted, WarpID: selectedID,
		Lifecycle: lifecycle, PreviousBlockReason: blockReason, WarpResult: result,
		NextLifecycle: selected.lifecycle, BlockReason: selected.blockReason,
	}
}

func (c *Core) slotFor(warpID uint8) (*slot, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if warpID >= isa.FrozenWarpCount {
		return nil, fmt.Errorf("core: warp id %d exceeds frozen count", warpID)
	}
	return &c.slots[warpID], nil
}

func (c *Core) snapshotSlot(warpID uint8) (SlotSnapshot, error) {
	selected := &c.slots[warpID]
	architectural, err := selected.owner.Snapshot()
	if err != nil {
		return SlotSnapshot{}, err
	}
	return SlotSnapshot{
		WarpID: warpID, Lifecycle: selected.lifecycle,
		BlockReason: selected.blockReason, Participated: selected.participated,
		ArchitecturalState:    architectural.Lifecycle(),
		ArchitecturalLaneMask: architectural.ActiveMask(),
	}, nil
}

func (c *Core) validate() error {
	if c == nil {
		return fmt.Errorf("core: nil core")
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if err := c.validateSlot(id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) validateSlot(warpID uint8) error {
	selected := &c.slots[warpID]
	if selected.owner == nil || selected.executor == nil || selected.executor.CanonicalState() != selected.owner {
		return fmt.Errorf("core: warp %d slot does not reference one canonical executor/owner pair", warpID)
	}
	snapshot, err := selected.owner.Snapshot()
	if err != nil {
		return fmt.Errorf("core: inspect warp %d: %w", warpID, err)
	}
	if snapshot.WarpID() != warpID {
		return fmt.Errorf("core: slot %d references canonical warp %d", warpID, snapshot.WarpID())
	}
	switch selected.lifecycle {
	case WarpInactive:
		if selected.participated || selected.blockReason != BlockNone || snapshot.Lifecycle() != state.WarpInactive || snapshot.ActiveMask() != 0 {
			return fmt.Errorf("core: inactive warp %d is inconsistent with canonical state", warpID)
		}
	case WarpRunnable:
		if !selected.participated || selected.blockReason != BlockNone || snapshot.Lifecycle() != state.WarpRunning || snapshot.ActiveMask() == 0 {
			return fmt.Errorf("core: runnable warp %d is inconsistent with canonical state", warpID)
		}
	case WarpBlocked:
		if !selected.participated || !validBlockReason(selected.blockReason) || snapshot.Lifecycle() != state.WarpRunning || snapshot.ActiveMask() == 0 {
			return fmt.Errorf("core: blocked warp %d is inconsistent with canonical state", warpID)
		}
	case WarpFinished:
		if !selected.participated || selected.blockReason != BlockNone || snapshot.Lifecycle() != state.WarpInactive || snapshot.ActiveMask() != 0 {
			return fmt.Errorf("core: finished warp %d is inconsistent with canonical state", warpID)
		}
	default:
		return fmt.Errorf("core: warp %d has unknown lifecycle %d", warpID, selected.lifecycle)
	}
	return nil
}

func deferredReason(result warp.Result) BlockReason {
	for _, effects := range []*isa.InstructionEffects{result.Effects, result.IssuedEffects} {
		if effects == nil {
			continue
		}
		if effects.WarpSpawn != nil {
			return BlockWarpSpawn
		}
		if len(effects.Barriers) != 0 {
			return BlockBarrier
		}
		if len(effects.WarpDrains) != 0 {
			return BlockPendingWork
		}
	}
	return BlockExternalOwner
}
