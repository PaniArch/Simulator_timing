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
	barrierKey   BarrierKey
	barrierWait  bool
	barrierDrain bool
}

// SlotSnapshot is a detached scheduler observation. Architectural fields are
// observations read from the canonical owner, not a writable Core-side copy.
type SlotSnapshot struct {
	WarpID                uint8
	Lifecycle             WarpLifecycle
	BlockReason           BlockReason
	Participated          bool
	InitializedForKernel  bool
	ArchitecturalState    state.WarpLifecycle
	ArchitecturalLaneMask isa.LaneMask
	BarrierKey            *BarrierKey
	BarrierDraining       bool
}

// Core contains exactly the four slots required by the frozen configuration.
// The array is indexed by architectural warp ID, independent of constructor
// ordering. next is only the functional round-robin cursor.
type Core struct {
	slots       [isa.FrozenWarpCount]slot
	next        uint8
	pendingWork PendingWorkProvider
	ctas        *CTAManager
	barriers    *BarrierCoordinator
	initialized [isa.FrozenWarpCount]bool
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

// NewWithCTAManager constructs the frozen Core and atomically attaches its
// canonical CTA owner before execution begins.
func NewWithCTAManager(executors []*warp.Warp, manager *CTAManager) (*Core, error) {
	result, err := New(executors)
	if err != nil {
		return nil, err
	}
	if err := result.SetCTAManager(manager); err != nil {
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

// SetCTAManager attaches the single canonical CTA owner used by this Core.
// It may be attached once; replacing it would replace membership truth while
// Warp executors and LMEM routes still refer to the old owner.
func (c *Core) SetCTAManager(manager *CTAManager) error {
	if err := c.validate(); err != nil {
		return err
	}
	if manager == nil {
		return fmt.Errorf("core: nil CTA manager")
	}
	if c.ctas != nil && c.ctas != manager {
		return fmt.Errorf("core: cannot replace the canonical CTA manager")
	}
	if c.ctas == manager {
		return c.validate()
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if c.slots[id].participated && !manager.HasWarp(id) {
			return fmt.Errorf("core: participated warp %d has no CTA membership", id)
		}
	}
	if err := c.validateCTAMemoryRoutes(manager); err != nil {
		return err
	}
	if err := manager.seal(); err != nil {
		return err
	}
	barriers, err := NewBarrierCoordinator(manager)
	if err != nil {
		return err
	}
	if !barriers.empty() {
		return fmt.Errorf("core: cannot attach CTA manager with preexisting barrier state")
	}
	c.ctas = manager
	c.barriers = barriers
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if c.slots[id].lifecycle == WarpFinished {
			if err := manager.observeWarpFinished(id); err != nil {
				c.ctas, c.barriers = nil, nil
				return err
			}
		}
	}
	return c.validate()
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
// running/mask state untouched. A typed nonzero, non-barrier reason is
// mandatory; barrier lifecycle is reserved for the coordinator.
func (c *Core) Block(warpID uint8, reason BlockReason) error {
	if !validBlockReason(reason) {
		return fmt.Errorf("core: invalid block reason %s", reason)
	}
	if reason == BlockBarrier {
		return fmt.Errorf("core: barrier blocks are owned by barrier coordination")
	}
	selected, err := c.slotFor(warpID)
	if err != nil {
		return err
	}
	if selected.lifecycle != WarpRunnable {
		return fmt.Errorf("core: cannot block warp %d in %s state", warpID, selected.lifecycle)
	}
	selected.lifecycle, selected.blockReason = WarpBlocked, reason
	selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, reason == BlockBarrier
	return c.validateSlot(warpID)
}

// Resume makes a non-barrier blocked, canonically running warp runnable again.
// Barrier waiters and LSU drainers resume only through their canonical paths.
func (c *Core) Resume(warpID uint8) error {
	selected, err := c.slotFor(warpID)
	if err != nil {
		return err
	}
	if selected.lifecycle != WarpBlocked {
		return fmt.Errorf("core: cannot resume warp %d in %s state", warpID, selected.lifecycle)
	}
	if selected.blockReason == BlockBarrier {
		return fmt.Errorf("core: barrier-blocked warp %d can only resume through barrier coordination", warpID)
	}
	selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
	selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
	return c.validateSlot(warpID)
}

// Activate admits an externally initialized inactive slot. The caller must
// first make the canonical WarpState running with a nonzero mask; Core only
// records the scheduling transition and never supplies launch/CTA defaults.
func (c *Core) Activate(warpID uint8) error {
	if c == nil || warpID >= isa.FrozenWarpCount {
		return fmt.Errorf("core: invalid warp id %d", warpID)
	}
	if err := c.validate(); err != nil {
		return err
	}
	selected := &c.slots[warpID]
	if c.ctas != nil && !c.ctas.HasWarp(warpID) {
		return fmt.Errorf("core: cannot activate warp %d without CTA membership", warpID)
	}
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
	selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
	return c.validateSlot(warpID)
}

// AdmitCTA atomically selects free frozen Warp/CTA/LMEM slots, installs the
// canonical CTA context, launches every member through the WarpState owner,
// and makes the resulting slots runnable. CTAConfig.ID and WarpIDs are output
// facts in dynamic mode; callers must not supply WarpIDs.
func (c *Core) AdmitCTA(config CTAConfig) (CTASnapshot, error) {
	admitted, err := c.AdmitCTACluster([]CTAConfig{config})
	if err != nil {
		return CTASnapshot{}, err
	}
	return admitted[0], nil
}

// AdmitCTACluster atomically admits one complete KMU cluster. No CTA in the
// group becomes resident unless every required Warp, CTA, LMEM, WarpState,
// and scheduler transition can commit.
func (c *Core) AdmitCTACluster(configs []CTAConfig) ([]CTASnapshot, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if c.ctas == nil || !c.ctas.dynamic {
		return nil, fmt.Errorf("core: dynamic CTA admission requires a dynamic CTA manager")
	}
	if len(configs) == 0 || len(configs) > int(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("core: dynamic CTA cluster size %d is invalid", len(configs))
	}
	clusterSize := configs[0].ClusterSize
	clusterDimensions := configs[0].ClusterDimensions
	if clusterSize != uint32(len(configs)) {
		return nil, fmt.Errorf("core: complete cluster requires %d CTA configs, got %d", clusterSize, len(configs))
	}
	clusterProduct := uint32(1)
	for axis, dimension := range clusterDimensions {
		if dimension == 0 || dimension > 7 {
			return nil, fmt.Errorf("core: cluster dimension on axis %d is invalid", axis)
		}
		clusterProduct *= dimension
	}
	if clusterProduct != clusterSize {
		return nil, fmt.Errorf("core: cluster dimensions produce %d CTAs, got %d", clusterProduct, clusterSize)
	}
	var clusterOrigin [3]uint32
	warpIDs := make([][]uint8, len(configs))
	var selected isa.WarpMask
	for index, config := range configs {
		if len(config.WarpIDs) != 0 {
			return nil, fmt.Errorf("core: dynamic CTA admission selects WarpIDs")
		}
		if config.ClusterSize != clusterSize || config.ClusterDimensions != clusterDimensions || config.IsFirstOfCluster != (index == 0) {
			return nil, fmt.Errorf("core: CTA %d does not preserve complete cluster order/context", index)
		}
		first := configs[0]
		if config.StartupPC != first.StartupPC || config.BlockDimensions != first.BlockDimensions ||
			config.GridDimensions != first.GridDimensions || config.BlockSize != first.BlockSize ||
			config.WarpStep != first.WarpStep || config.Entry != first.Entry ||
			config.ParameterAddress != first.ParameterAddress || config.LocalMemorySize != first.LocalMemorySize {
			return nil, fmt.Errorf("core: CTA %d differs from its cluster launch context", index)
		}
		rank := uint32(index)
		offset := [3]uint32{
			rank % clusterDimensions[0],
			(rank / clusterDimensions[0]) % clusterDimensions[1],
			rank / (clusterDimensions[0] * clusterDimensions[1]),
		}
		for axis := range 3 {
			if config.BlockID[axis] < offset[axis] {
				return nil, fmt.Errorf("core: CTA %d block id precedes its cluster offset on axis %d", index, axis)
			}
			origin := config.BlockID[axis] - offset[axis]
			if index == 0 {
				clusterOrigin[axis] = origin
			} else if origin != clusterOrigin[axis] {
				return nil, fmt.Errorf("core: CTA %d does not share cluster origin on axis %d", index, axis)
			}
		}
		if config.BlockSize == 0 || config.BlockSize > uint32(isa.FrozenWarpCount*isa.FrozenLaneCount) {
			return nil, fmt.Errorf("core: dynamic CTA block size %d is invalid", config.BlockSize)
		}
		required := (config.BlockSize + uint32(isa.FrozenLaneCount) - 1) / uint32(isa.FrozenLaneCount)
		warpIDs[index] = make([]uint8, 0, required)
		for id := uint8(0); id < isa.FrozenWarpCount && uint32(len(warpIDs[index])) < required; id++ {
			if !selected.Active(id) && c.slots[id].lifecycle == WarpInactive && !c.slots[id].participated {
				warpIDs[index] = append(warpIDs[index], id)
				selected |= 1 << id
			}
		}
		if uint32(len(warpIDs[index])) != required {
			return nil, fmt.Errorf("%w: cluster CTA %d needs %d free warp slots", ErrCTAResourcesUnavailable, index, required)
		}
	}

	admission, err := c.ctas.stageDynamicAdmissions(configs, warpIDs)
	if err != nil {
		return nil, err
	}
	targets := make([]state.WarpLaunchTarget, 0, isa.FrozenWarpCount)
	for _, candidate := range admission.candidates {
		for _, member := range candidate.members {
			targets = append(targets, state.WarpLaunchTarget{
				WarpID: member.WarpID, Owner: c.slots[member.WarpID].owner,
				StartupPC: candidate.config.StartupPC, ActiveMask: member.ActiveMask,
				ParameterAddress: candidate.config.ParameterAddress,
				FirstUse:         !c.initialized[member.WarpID],
			})
		}
	}
	launch, err := state.StageWarpLaunch(targets)
	if err != nil {
		return nil, err
	}
	if err := admission.commit(func() error {
		for _, candidate := range admission.candidates {
			for _, member := range candidate.members {
				slot := &c.slots[member.WarpID]
				if slot.lifecycle != WarpInactive || slot.participated || slot.blockReason != BlockNone ||
					slot.barrierWait || slot.barrierDrain || slot.barrierKey != (BarrierKey{}) {
					return fmt.Errorf("core: warp %d scheduler slot changed before CTA admission", member.WarpID)
				}
			}
		}
		return launch.Commit()
	}); err != nil {
		return nil, err
	}
	result := make([]CTASnapshot, len(admission.candidates))
	for index, candidate := range admission.candidates {
		for _, member := range candidate.members {
			slot := &c.slots[member.WarpID]
			slot.lifecycle, slot.blockReason, slot.participated = WarpRunnable, BlockNone, true
			slot.barrierKey, slot.barrierWait, slot.barrierDrain = BarrierKey{}, false, false
			c.initialized[member.WarpID] = true
		}
		result[index] = snapshotCTA(candidate)
	}
	return result, nil
}

// ReclaimCTA releases a normally complete dynamic CTA. Completion includes
// every explicit member, scheduler blocks, canonical barrier records, and the
// configured functional pending-work owner. Successful reclaim removes all
// reverse membership and empty phase history while retaining Warp registers
// and LMEM bytes whose reset values are not architecturally frozen.
func (c *Core) ReclaimCTA(ctaID uint32) (CTASnapshot, error) {
	if err := c.validate(); err != nil {
		return CTASnapshot{}, err
	}
	if c.ctas == nil || !c.ctas.dynamic {
		return CTASnapshot{}, fmt.Errorf("core: dynamic CTA reclaim requires a dynamic CTA manager")
	}
	snapshot, err := c.ctas.Snapshot(ctaID)
	if err != nil {
		return CTASnapshot{}, err
	}
	completion, err := c.CTACompletion(ctaID)
	if err != nil {
		return CTASnapshot{}, err
	}
	if !completion.Complete {
		return CTASnapshot{}, fmt.Errorf("core: CTA %d is not normally complete", ctaID)
	}
	for _, member := range snapshot.Members {
		if c.pendingWork != nil {
			pending, pendingErr := c.pendingWork.PendingPriorWork(member.WarpID)
			if pendingErr != nil {
				return CTASnapshot{}, fmt.Errorf("core: pending-work view for reclaim warp %d: %w", member.WarpID, pendingErr)
			}
			if pending {
				return CTASnapshot{}, fmt.Errorf("core: CTA %d warp %d retains pending functional work", ctaID, member.WarpID)
			}
		}
	}
	if err := c.ctas.commitDynamicReclaim(ctaID, func(members []WarpMembership) {
		for _, member := range members {
			slot := &c.slots[member.WarpID]
			slot.lifecycle, slot.blockReason, slot.participated = WarpInactive, BlockNone, false
			slot.barrierKey, slot.barrierWait, slot.barrierDrain = BarrierKey{}, false, false
		}
	}); err != nil {
		return CTASnapshot{}, err
	}
	return snapshot, nil
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
	CTAValid            bool
	CTAID               uint32
	Barrier             *BarrierTransition
	CTACompletion       *CTACompletionSnapshot
}

// BarrierTransition is a detached observation of one Core-owned barrier
// coordination boundary. Before/After are exact canonical record images.
type BarrierTransition struct {
	Draining bool
	Accepted bool
	Request  isa.BarrierEffect
	Key      BarrierKey
	Before   BarrierSnapshot
	After    BarrierSnapshot
	Blocked  bool
	Released isa.WarpMask
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
	var issuedBarriers barrierView
	if c.ctas != nil {
		context.CTA, err = c.ctas.ViewForWarp(selectedID)
		if err != nil {
			return StepResult{}, fmt.Errorf("core: CTA context for warp %d: %w", selectedID, err)
		}
		issuedBarriers, err = c.barriers.view(context.CTA.ID)
		if err != nil {
			return StepResult{}, fmt.Errorf("core: barrier phase view for warp %d: %w", selectedID, err)
		}
		context.BarrierPhases = &issuedBarriers.phases
	}
	if c.pendingWork != nil {
		context.PendingPriorWork, err = c.pendingWork.PendingPriorWork(selectedID)
		if err != nil {
			return StepResult{}, fmt.Errorf("core: pending-work view for warp %d: %w", selectedID, err)
		}
	}
	c.next = (selectedID + 1) % isa.FrozenWarpCount
	selected := &c.slots[selectedID]
	beforeLifecycle, beforeBlockReason := selected.lifecycle, selected.blockReason
	beforeBarrierKey, beforeBarrierWait, beforeBarrierDrain := selected.barrierKey, selected.barrierWait, selected.barrierDrain
	var barrierTransition *BarrierTransition
	warpResult := selected.executor.Step(context)

	if warpResult.Outcome == warp.OutcomeDeferred && warpResult.Decoded != nil {
		switch warpResult.Decoded.Name {
		case "wspawn":
			if err := c.coordinateWarpSpawn(selectedID, active, ownerBefore, &warpResult); err != nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult, barrierTransition), err
			}
		case "wsync":
			if err := c.coordinateWarpSync(selectedID, context.PendingPriorWork, &warpResult); err != nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult, barrierTransition), err
			}
		case "bar", "bar.arrive", "bar.wait":
			if c.barriers == nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockBarrier
			} else if err := c.coordinateBarrier(selectedID, ownerBefore[selectedID], issuedBarriers, context.PendingLSU, &warpResult, &barrierTransition); err != nil {
				selected.lifecycle, selected.blockReason = beforeLifecycle, beforeBlockReason
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = beforeBarrierKey, beforeBarrierWait, beforeBarrierDrain
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult, barrierTransition), err
			}
		}
	}

	if warpResult.NextLifecycle == state.WarpInactive {
		selected.lifecycle, selected.blockReason = WarpFinished, BlockNone
		selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
		if c.ctas != nil {
			if err := c.ctas.observeWarpFinished(selectedID); err != nil {
				selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
				return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult, barrierTransition), err
			}
		}
	} else {
		switch warpResult.Outcome {
		case warp.OutcomeDeferred:
			if selected.lifecycle != WarpBlocked {
				selected.lifecycle = WarpBlocked
				selected.blockReason = deferredReason(warpResult)
			}
		case warp.OutcomeFault:
			selected.lifecycle, selected.blockReason = WarpBlocked, BlockFault
			selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
		}
	}
	if err := c.validateSlot(selectedID); err != nil {
		return StepResult{}, err
	}
	return c.stepResult(selectedID, beforeLifecycle, beforeBlockReason, warpResult, barrierTransition), nil
}

// Complete reports Core-level completion for slots which participated in this
// Core lifecycle. Never-activated empty slots are not part of the aggregation.
func (c *Core) Complete() (bool, error) {
	if err := c.validate(); err != nil {
		return false, err
	}
	if c.ctas != nil {
		completions, err := c.CTACompletions()
		if err != nil {
			return false, err
		}
		if len(completions) == 0 {
			return false, nil
		}
		for _, completion := range completions {
			if !completion.Complete {
				return false, nil
			}
		}
		return true, nil
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

// CTACompletion returns the CTA owner's detached aggregation over explicit
// members, current scheduler lifecycle, and canonical barrier state.
func (c *Core) CTACompletion(ctaID uint32) (CTACompletionSnapshot, error) {
	if err := c.validate(); err != nil {
		return CTACompletionSnapshot{}, err
	}
	if c.ctas == nil {
		return CTACompletionSnapshot{}, fmt.Errorf("core: no CTA manager is attached")
	}
	var scheduler [isa.FrozenWarpCount]ctaSchedulerView
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		scheduler[id] = ctaSchedulerView{lifecycle: c.slots[id].lifecycle, blockReason: c.slots[id].blockReason}
	}
	return c.ctas.completion(ctaID, scheduler)
}

// CTACompletions returns all resident CTA observations in deterministic ID
// order. Member slices are independently allocated by the CTA owner.
func (c *Core) CTACompletions() ([]CTACompletionSnapshot, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if c.ctas == nil {
		return nil, nil
	}
	ids, err := c.ctas.residentIDs()
	if err != nil {
		return nil, err
	}
	result := make([]CTACompletionSnapshot, len(ids))
	for index, id := range ids {
		result[index], err = c.CTACompletion(id)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
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
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
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
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
			}
		case BlockBarrier:
			if selected.barrierDrain && !context.PendingLSU {
				selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
				selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
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
	if c.ctas != nil {
		if err := c.ctas.ValidateSpawn(sourceID, spawn.Targets); err != nil {
			return fmt.Errorf("core: WSPAWN CTA membership: %w", err)
		}
	}
	if active != isa.WarpMask(1<<sourceID) {
		c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpBlocked, BlockWarpSpawn
		c.slots[sourceID].barrierKey, c.slots[sourceID].barrierWait, c.slots[sourceID].barrierDrain = BarrierKey{}, false, false
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
		slot.barrierKey, slot.barrierWait, slot.barrierDrain = BarrierKey{}, false, false
	}
	c.slots[sourceID].lifecycle, c.slots[sourceID].blockReason = WarpRunnable, BlockNone
	c.slots[sourceID].barrierKey, c.slots[sourceID].barrierWait, c.slots[sourceID].barrierDrain = BarrierKey{}, false, false
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
		c.slots[sourceID].barrierKey, c.slots[sourceID].barrierWait, c.slots[sourceID].barrierDrain = BarrierKey{}, false, false
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
	c.slots[sourceID].barrierKey, c.slots[sourceID].barrierWait, c.slots[sourceID].barrierDrain = BarrierKey{}, false, false
	completeDeferredResult(c.slots[sourceID].owner, result)
	return nil
}

func (c *Core) coordinateBarrier(sourceID uint8, issued state.WarpSnapshot, issuedBarriers barrierView, pendingLSU bool, result *warp.Result, transition **BarrierTransition) error {
	if result == nil || result.Decoded == nil || result.Effects == nil {
		return fmt.Errorf("core: barrier deferred without decoded effects")
	}
	effects := result.Effects
	if pendingLSU {
		if len(effects.WarpDrains) != 1 || effects.WarpDrains[0] != (isa.WarpDrainEffect{WarpID: sourceID, Kind: isa.DrainLSU, Wait: true}) ||
			effects.Control != nil || len(effects.Barriers) != 0 || len(effects.RegisterWrites) != 0 ||
			effects.WarpSpawn != nil || len(effects.MemoryRequests) != 0 || effects.Ordering != nil ||
			effects.FFlags != nil || len(effects.CSRReads) != 0 || len(effects.CSRWrites) != 0 ||
			effects.Trap != nil || len(effects.WarpMasks) != 0 || effects.Divergence != nil ||
			len(effects.PackedLoads) != 0 || len(effects.Faults) != 0 {
			return fmt.Errorf("core: pending-LSU barrier effect violates the drain contract")
		}
		selected := &c.slots[sourceID]
		selected.lifecycle, selected.blockReason = WarpBlocked, BlockBarrier
		selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, true
		if transition != nil {
			*transition = &BarrierTransition{Draining: true}
		}
		return nil
	}
	if len(effects.WarpDrains) != 1 || effects.WarpDrains[0] != (isa.WarpDrainEffect{WarpID: sourceID, Kind: isa.DrainLSU}) ||
		len(effects.Barriers) != 1 || effects.Control == nil || effects.WarpSpawn != nil ||
		len(effects.MemoryRequests) != 0 || effects.Ordering != nil || effects.FFlags != nil ||
		len(effects.CSRReads) != 0 || len(effects.CSRWrites) != 0 || effects.Trap != nil ||
		len(effects.WarpMasks) != 0 || effects.Divergence != nil || len(effects.PackedLoads) != 0 ||
		len(effects.Faults) != 0 {
		return fmt.Errorf("core: accepted barrier effect violates the frozen contract")
	}
	effect := effects.Barriers[0]
	wantKind := map[string]isa.BarrierKind{"bar": isa.BarrierSync, "bar.arrive": isa.BarrierArrive, "bar.wait": isa.BarrierWait}[result.Decoded.Name]
	if effect.WarpID != sourceID || effect.Kind != wantKind {
		return fmt.Errorf("core: barrier effect source/kind does not match issued instruction")
	}
	current, err := c.slots[sourceID].owner.Snapshot()
	if err != nil {
		return err
	}
	if current != issued {
		return fmt.Errorf("core: barrier source warp %d changed since instruction issue", sourceID)
	}
	localStage, err := c.slots[sourceID].owner.StageEffects(*effects)
	if err != nil {
		return fmt.Errorf("core: restage barrier source: %w", err)
	}
	forwarded := localStage.ForwardedEffects()
	if len(forwarded.WarpDrains) != 1 || len(forwarded.Barriers) != 1 || forwarded.WarpSpawn != nil ||
		len(forwarded.MemoryRequests) != 0 || len(forwarded.PackedLoads) != 0 || len(forwarded.CSRWrites) != 0 ||
		len(forwarded.CSRReads) != 0 || forwarded.Ordering != nil || len(forwarded.Faults) != 0 {
		return fmt.Errorf("core: barrier has unexpected forwarded effects")
	}
	barrierStage, err := c.barriers.stageFromView(effect, issuedBarriers)
	if err != nil {
		return fmt.Errorf("core: stage barrier: %w", err)
	}
	barrierResult := barrierStage.Result()
	if err := c.validateBarrierReleases(sourceID, barrierResult); err != nil {
		return err
	}
	if err := localStage.CommitForwardedWithExternal(barrierStage.Commit); err != nil {
		return fmt.Errorf("core: commit barrier transaction: %w", err)
	}
	if transition != nil {
		before := issuedBarriers.records[effect.AddressWarp][effect.ID]
		*transition = &BarrierTransition{Accepted: true, Request: effect, Key: barrierResult.Key,
			Before: before, After: snapshotBarrier(barrierResult.Key, barrierStage.after),
			Blocked: barrierResult.Block, Released: barrierResult.Releases}
	}
	c.applyBarrierReleases(sourceID, barrierResult)
	selected := &c.slots[sourceID]
	if barrierResult.Block {
		selected.lifecycle, selected.blockReason = WarpBlocked, BlockBarrier
		selected.barrierKey, selected.barrierWait, selected.barrierDrain = barrierResult.Key, true, false
	} else {
		selected.lifecycle, selected.blockReason = WarpRunnable, BlockNone
		selected.barrierKey, selected.barrierWait, selected.barrierDrain = BarrierKey{}, false, false
	}
	completeDeferredResult(selected.owner, result)
	// Every potentially failing check is complete before the coordinated
	// owner commit. The remaining scheduler assignments are prevalidated and
	// infallible, so an accepted request cannot surface a post-commit error.
	return nil
}

func (c *Core) validateBarrierReleases(sourceID uint8, result BarrierResult) error {
	if !result.Releases.Valid() {
		return fmt.Errorf("core: barrier release mask exceeds frozen warps")
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if !result.Releases.Active(id) || id == sourceID {
			continue
		}
		slot := &c.slots[id]
		if slot.lifecycle != WarpBlocked || slot.blockReason != BlockBarrier || !slot.barrierWait || slot.barrierKey != result.Key {
			return fmt.Errorf("core: barrier release warp %d is not waiting on the matching key", id)
		}
	}
	return nil
}

func (c *Core) applyBarrierReleases(sourceID uint8, result BarrierResult) {
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if !result.Releases.Active(id) || id == sourceID {
			continue
		}
		slot := &c.slots[id]
		slot.lifecycle, slot.blockReason = WarpRunnable, BlockNone
		slot.barrierKey, slot.barrierWait, slot.barrierDrain = BarrierKey{}, false, false
	}
}

// CompleteBarrierEvent accepts one canonical txbar completion and releases
// only scheduler waiters recorded under the matching CTA/address/id key.
func (c *Core) CompleteBarrierEvent(key BarrierKey) error {
	if err := c.validate(); err != nil {
		return err
	}
	if c.barriers == nil {
		return fmt.Errorf("core: no barrier coordinator is attached")
	}
	stage, err := c.barriers.StageEventCompletion(key)
	if err != nil {
		return err
	}
	result := stage.Result()
	if err := c.validateBarrierReleases(isa.FrozenWarpCount, result); err != nil {
		return err
	}
	if err := stage.Commit(); err != nil {
		return err
	}
	c.applyBarrierReleases(isa.FrozenWarpCount, result)
	// Release slots were validated before the all-or-error record commit; the
	// remaining lifecycle assignments cannot fail.
	return nil
}

// Barrier returns a detached canonical barrier observation.
func (c *Core) Barrier(key BarrierKey) (BarrierSnapshot, error) {
	if err := c.validate(); err != nil {
		return BarrierSnapshot{}, err
	}
	if c.barriers == nil {
		return BarrierSnapshot{}, fmt.Errorf("core: no barrier coordinator is attached")
	}
	return c.barriers.Snapshot(key)
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

func (c *Core) stepResult(selectedID uint8, lifecycle WarpLifecycle, blockReason BlockReason, result warp.Result, barrier *BarrierTransition) StepResult {
	selected := &c.slots[selectedID]
	step := StepResult{
		Outcome: StepExecuted, WarpID: selectedID,
		Lifecycle: lifecycle, PreviousBlockReason: blockReason, WarpResult: result,
		NextLifecycle: selected.lifecycle, BlockReason: selected.blockReason,
		Barrier: barrier,
	}
	if c.ctas != nil {
		if view, err := c.ctas.ViewForWarp(selectedID); err == nil {
			step.CTAValid, step.CTAID = true, view.ID
			if completion, completionErr := c.CTACompletion(view.ID); completionErr == nil {
				step.CTACompletion = &completion
			}
		}
	}
	return step
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
	result := SlotSnapshot{
		WarpID: warpID, Lifecycle: selected.lifecycle,
		BlockReason: selected.blockReason, Participated: selected.participated,
		InitializedForKernel:  c.initialized[warpID],
		ArchitecturalState:    architectural.Lifecycle(),
		ArchitecturalLaneMask: architectural.ActiveMask(),
		BarrierDraining:       selected.barrierDrain,
	}
	if selected.barrierWait {
		key := selected.barrierKey
		result.BarrierKey = &key
	}
	return result, nil
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
	if c.ctas != nil {
		if err := c.validateCTAMemoryRoutes(c.ctas); err != nil {
			return err
		}
		if c.barriers == nil || c.barriers.ctas != c.ctas {
			return fmt.Errorf("core: CTA and barrier canonical owners are not bound")
		}
	} else if c.barriers != nil {
		return fmt.Errorf("core: barrier coordinator has no CTA owner")
	}
	return nil
}

func (c *Core) validateCTAMemoryRoutes(manager *CTAManager) error {
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		if !manager.dynamic && !manager.HasWarp(id) {
			continue
		}
		route, ok := c.slots[id].executor.DataMemoryService().(*CTAMemory)
		if !ok || route == nil {
			return fmt.Errorf("core: CTA member warp %d has no CTA memory route", id)
		}
		if route.warpID != id || route.manager != manager {
			return fmt.Errorf("core: CTA member warp %d memory route references a different warp or CTA manager", id)
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
	if c.ctas != nil && selected.participated && !c.ctas.HasWarp(warpID) {
		return fmt.Errorf("core: participated warp %d has no CTA membership", warpID)
	}
	if selected.blockReason != BlockBarrier && (selected.barrierWait || selected.barrierDrain || selected.barrierKey != (BarrierKey{})) {
		return fmt.Errorf("core: warp %d retains barrier metadata outside a barrier block", warpID)
	}
	if selected.barrierWait && selected.barrierDrain {
		return fmt.Errorf("core: warp %d cannot wait on a record while draining LSU", warpID)
	}
	if selected.barrierWait {
		if c.barriers == nil {
			return fmt.Errorf("core: warp %d waits without a barrier coordinator", warpID)
		}
		record, err := c.barriers.Snapshot(selected.barrierKey)
		if err != nil || !record.Waiters.Active(warpID) {
			return fmt.Errorf("core: warp %d scheduler wait is absent from canonical barrier record", warpID)
		}
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
