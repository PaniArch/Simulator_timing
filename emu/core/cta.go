package core

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

const frozenMemoryBlockSize = uint32(64)

var (
	ErrLocalMemoryRange        = errors.New("core: local-memory access outside CTA allocation")
	ErrMemoryCrossing          = errors.New("core: access crosses the frozen local-memory window")
	ErrCTAResourcesUnavailable = errors.New("core: CTA resources temporarily unavailable")
)

// CTAConfig is the complete immutable input admitted by CTAManager. Static
// callers supply WarpIDs in CTA-rank order; Core selects them in dynamic mode.
// Thread coordinates are derived rather than accepted as a second truth.
type CTAConfig struct {
	ID                uint32
	WarpIDs           []uint8
	StartupPC         uint32
	BlockID           [3]uint32
	BlockDimensions   [3]uint32
	GridDimensions    [3]uint32
	BlockSize         uint32
	WarpStep          [3]uint32
	Entry             uint32
	ParameterAddress  uint32
	LocalMemorySize   uint32
	ClusterDimensions [3]uint32
	ClusterSize       uint32
	IsFirstOfCluster  bool
}

type LocalMemoryAllocation struct {
	Address uint32
	Size    uint32
}

type WarpMembership struct {
	WarpID            uint8
	Rank              uint32
	ActiveMask        isa.LaneMask
	ThreadCoordinates [3]isa.LaneValues
	Finished          bool
}

// CTASnapshot is a detached observation of the canonical CTA owner.
type CTASnapshot struct {
	ID                uint32
	Members           []WarpMembership
	StartupPC         uint32
	BlockID           [3]uint32
	BlockDimensions   [3]uint32
	GridDimensions    [3]uint32
	BlockSize         uint32
	WarpStep          [3]uint32
	Entry             uint32
	ParameterAddress  uint32
	LocalMemory       LocalMemoryAllocation
	ClusterDimensions [3]uint32
	ClusterSize       uint32
	IsFirstOfCluster  bool
}

// CTACompletionMember is a detached aggregation input/result for one explicit
// CTA member. OwnerFinished is the CTA owner's monotonic observation;
// Lifecycle and BlockReason are the current Core scheduler facts.
type CTACompletionMember struct {
	WarpID        uint8
	OwnerFinished bool
	Lifecycle     WarpLifecycle
	BlockReason   BlockReason
}

// CTACompletionSnapshot is a detached, non-sticky observation. Complete is
// true only when every explicit member is normally finished and this CTA has
// neither blocked members nor pending canonical barrier state.
type CTACompletionSnapshot struct {
	CTAID          uint32
	Complete       bool
	PendingBarrier bool
	BlockedMembers isa.WarpMask
	Members        []CTACompletionMember
}

type ctaSchedulerView struct {
	lifecycle   WarpLifecycle
	blockReason BlockReason
}

type ctaState struct {
	config        CTAConfig
	members       []WarpMembership
	allocation    LocalMemoryAllocation
	backingOffset uint32
}

type warpCTAKey struct {
	ctaID uint32
	rank  uint32
}

// CTAManager is the one writable owner for resident CTA metadata, membership,
// completion observations, allocation descriptors, and all physical LMEM
// bytes. Core/Warp retain only this service reference or a stable warp ID.
type CTAManager struct {
	mu       sync.RWMutex
	ctas     map[uint32]*ctaState
	byWarp   [isa.FrozenWarpCount]*warpCTAKey
	localMem [isa.FrozenLocalMemSize]byte
	sealed   bool
	dynamic  bool
	epoch    uint64
	barriers *BarrierCoordinator
}

func NewCTAManager() *CTAManager {
	return &CTAManager{ctas: make(map[uint32]*ctaState)}
}

// NewDynamicCTAManager creates an initially empty owner which may be attached
// to a Core and subsequently changed only through Core.AdmitCTA/ReclaimCTA.
func NewDynamicCTAManager() *CTAManager {
	return &CTAManager{ctas: make(map[uint32]*ctaState), dynamic: true}
}

// Admit validates the complete CTA before changing membership, allocation, or
// bytes. LMEM footprints are rounded to the frozen 64-byte memory block and
// packed into the 16 KiB backing store behind one CTA-scoped virtual window.
func (m *CTAManager) Admit(config CTAConfig) (CTASnapshot, error) {
	if m == nil {
		return CTASnapshot{}, fmt.Errorf("core: nil CTA manager")
	}
	candidate, err := validateCTAConfig(config)
	if err != nil {
		return CTASnapshot{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dynamic {
		return CTASnapshot{}, fmt.Errorf("core: dynamic CTA admission must be coordinated by Core")
	}
	if m.sealed {
		return CTASnapshot{}, fmt.Errorf("core: CTA manager is already attached and sealed")
	}
	if _, exists := m.ctas[config.ID]; exists {
		return CTASnapshot{}, fmt.Errorf("core: CTA id %d is already resident", config.ID)
	}
	for _, member := range candidate.members {
		if key := m.byWarp[member.WarpID]; key != nil {
			return CTASnapshot{}, fmt.Errorf("core: warp %d already belongs to CTA %d", member.WarpID, key.ctaID)
		}
	}
	allocation, backingOffset, err := m.allocateLocked(candidate.config.LocalMemorySize)
	if err != nil {
		return CTASnapshot{}, err
	}
	candidate.allocation = allocation
	candidate.backingOffset = backingOffset
	m.ctas[config.ID] = candidate
	for _, member := range candidate.members {
		m.byWarp[member.WarpID] = &warpCTAKey{ctaID: config.ID, rank: member.Rank}
	}
	m.epoch++
	return snapshotCTA(candidate), nil
}

// seal freezes direct static admission once Core validates every member route.
// A dynamic manager may subsequently change residency only through Core's
// coordinated admission/reclaim protocol.
func (m *CTAManager) seal() error {
	if m == nil {
		return fmt.Errorf("core: nil CTA manager")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.ctas) == 0 && !m.dynamic {
		return fmt.Errorf("core: cannot attach an empty CTA manager")
	}
	m.sealed = true
	return nil
}

// Create is a naming alias for Admit.
func (m *CTAManager) Create(config CTAConfig) (CTASnapshot, error) { return m.Admit(config) }

type ctaAdmissionStage struct {
	owner      *CTAManager
	epoch      uint64
	candidates []*ctaState
}

func (m *CTAManager) stageDynamicAdmissions(configs []CTAConfig, warpIDs [][]uint8) (*ctaAdmissionStage, error) {
	if m == nil {
		return nil, fmt.Errorf("core: nil CTA manager")
	}
	if len(configs) == 0 || len(configs) != len(warpIDs) {
		return nil, fmt.Errorf("core: dynamic CTA cluster has %d configs and %d warp sets", len(configs), len(warpIDs))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dynamic || !m.sealed || m.barriers == nil {
		return nil, fmt.Errorf("core: CTA manager is not attached for dynamic admission")
	}
	freeCTAIDs := make([]uint32, 0, isa.FrozenWarpCount)
	for id := uint32(0); id < uint32(isa.FrozenWarpCount); id++ {
		if m.ctas[id] == nil {
			freeCTAIDs = append(freeCTAIDs, id)
		}
	}
	if len(freeCTAIDs) < len(configs) {
		return nil, fmt.Errorf("%w: need %d CTA slots, have %d", ErrCTAResourcesUnavailable, len(configs), len(freeCTAIDs))
	}
	intervals := m.allocationIntervalsLocked()
	candidates := make([]*ctaState, len(configs))
	var stagedWarps isa.WarpMask
	for index, config := range configs {
		config.ID = freeCTAIDs[index]
		config.WarpIDs = append([]uint8(nil), warpIDs[index]...)
		candidate, err := validateCTAConfig(config)
		if err != nil {
			return nil, err
		}
		for _, member := range candidate.members {
			if key := m.byWarp[member.WarpID]; key != nil {
				return nil, fmt.Errorf("%w: warp %d belongs to CTA %d", ErrCTAResourcesUnavailable, member.WarpID, key.ctaID)
			}
			if stagedWarps.Active(member.WarpID) {
				return nil, fmt.Errorf("core: dynamic CTA cluster repeats warp %d", member.WarpID)
			}
			stagedWarps |= 1 << member.WarpID
		}
		allocation, backingOffset, err := allocateFromIntervals(candidate.config.LocalMemorySize, intervals)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCTAResourcesUnavailable, err)
		}
		candidate.allocation = allocation
		candidate.backingOffset = backingOffset
		intervals = append(intervals, allocationInterval{backingOffset, backingOffset + allocation.Size})
		candidates[index] = candidate
	}
	return &ctaAdmissionStage{owner: m, epoch: m.epoch, candidates: candidates}, nil
}

// commit installs no manager state unless the external canonical WarpState
// transition succeeds. The epoch and all resources are rechecked while the
// manager lock excludes another admission/reclaim.
func (s *ctaAdmissionStage) commit(external func() error) error {
	if s == nil || s.owner == nil || len(s.candidates) == 0 || external == nil {
		return fmt.Errorf("core: nil dynamic CTA admission stage")
	}
	m := s.owner
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dynamic || !m.sealed || m.epoch != s.epoch {
		return fmt.Errorf("core: dynamic CTA admission stage is stale")
	}
	for _, candidate := range s.candidates {
		ctaID := candidate.config.ID
		if m.ctas[ctaID] != nil {
			return fmt.Errorf("core: CTA slot %d became occupied", ctaID)
		}
		for _, member := range candidate.members {
			if m.byWarp[member.WarpID] != nil {
				return fmt.Errorf("core: warp %d became occupied", member.WarpID)
			}
		}
	}
	if err := external(); err != nil {
		return err
	}
	for _, candidate := range s.candidates {
		ctaID := candidate.config.ID
		m.ctas[ctaID] = candidate
		for _, member := range candidate.members {
			m.byWarp[member.WarpID] = &warpCTAKey{ctaID: ctaID, rank: member.Rank}
		}
	}
	m.epoch++
	return nil
}

func (m *CTAManager) commitDynamicReclaim(ctaID uint32, external func([]WarpMembership)) error {
	if m == nil || external == nil {
		return fmt.Errorf("core: nil dynamic CTA reclaim")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dynamic || !m.sealed || m.barriers == nil {
		return fmt.Errorf("core: CTA manager is not attached for dynamic reclaim")
	}
	cta := m.ctas[ctaID]
	if cta == nil {
		return fmt.Errorf("core: CTA %d is not resident", ctaID)
	}
	m.barriers.mu.Lock()
	defer m.barriers.mu.Unlock()
	for key, record := range m.barriers.records {
		if key.CTAID == ctaID && (record.arrivals != 0 || record.waiters != 0 || record.participantCount != 0 ||
			record.events != 0 || record.arrivalsComplete) {
			return fmt.Errorf("core: CTA %d retains pending barrier state", ctaID)
		}
	}
	for _, member := range cta.members {
		key := m.byWarp[member.WarpID]
		if key == nil || key.ctaID != ctaID || key.rank != member.Rank {
			return fmt.Errorf("core: CTA %d membership changed before reclaim", ctaID)
		}
	}
	external(append([]WarpMembership(nil), cta.members...))
	for _, member := range cta.members {
		m.byWarp[member.WarpID] = nil
	}
	delete(m.ctas, ctaID)
	for key := range m.barriers.records {
		if key.CTAID == ctaID {
			delete(m.barriers.records, key)
		}
	}
	m.epoch++
	return nil
}

func validateCTAConfig(config CTAConfig) (*ctaState, error) {
	if config.ID >= uint32(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("core: CTA id %d exceeds frozen resident slots", config.ID)
	}
	if len(config.WarpIDs) == 0 || len(config.WarpIDs) > int(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("core: CTA %d has invalid member count %d", config.ID, len(config.WarpIDs))
	}
	if config.Entry&3 != 0 {
		return nil, fmt.Errorf("core: CTA %d entry %#x is not four-byte aligned", config.ID, config.Entry)
	}
	if config.StartupPC&3 != 0 {
		return nil, fmt.Errorf("core: CTA %d startup PC %#x is not four-byte aligned", config.ID, config.StartupPC)
	}
	if config.ClusterSize == 0 || config.ClusterSize > uint32(isa.FrozenWarpCount) {
		return nil, fmt.Errorf("core: CTA %d cluster size %d is invalid", config.ID, config.ClusterSize)
	}
	legacyCoordinates := config.BlockSize == 0
	threads := uint64(1)
	for axis := range 3 {
		if config.BlockDimensions[axis] == 0 || config.GridDimensions[axis] == 0 {
			return nil, fmt.Errorf("core: CTA %d has zero block/grid dimension on axis %d", config.ID, axis)
		}
		if config.BlockID[axis] >= config.GridDimensions[axis] {
			return nil, fmt.Errorf("core: CTA %d block id exceeds grid on axis %d", config.ID, axis)
		}
		if !legacyCoordinates && config.BlockDimensions[axis] > 31 {
			return nil, fmt.Errorf("core: CTA %d block dimension on axis %d exceeds frozen field", config.ID, axis)
		}
		if !legacyCoordinates && config.WarpStep[axis] > 15 {
			return nil, fmt.Errorf("core: CTA %d warp step on axis %d exceeds frozen field", config.ID, axis)
		}
		threads *= uint64(config.BlockDimensions[axis])
		if legacyCoordinates && threads > uint64(isa.FrozenWarpCount*isa.FrozenLaneCount) {
			return nil, fmt.Errorf("core: CTA %d block has %d threads, exceeds frozen core capacity", config.ID, threads)
		}
	}
	if legacyCoordinates {
		config.BlockSize = uint32(threads)
		config.ClusterDimensions = [3]uint32{1, 1, 1}
	} else {
		if config.BlockSize > uint32(isa.FrozenWarpCount*isa.FrozenLaneCount) {
			return nil, fmt.Errorf("core: CTA %d block size %d exceeds frozen core capacity", config.ID, config.BlockSize)
		}
		clusterProduct := uint64(1)
		for axis, dimension := range config.ClusterDimensions {
			if dimension == 0 || dimension > 7 {
				return nil, fmt.Errorf("core: CTA %d cluster dimension on axis %d is invalid", config.ID, axis)
			}
			clusterProduct *= uint64(dimension)
		}
		if clusterProduct != uint64(config.ClusterSize) {
			return nil, fmt.Errorf("core: CTA %d cluster dimensions produce %d members, got size %d", config.ID, clusterProduct, config.ClusterSize)
		}
	}
	requiredWarps := (uint64(config.BlockSize) + uint64(isa.FrozenLaneCount) - 1) / uint64(isa.FrozenLaneCount)
	if requiredWarps != uint64(len(config.WarpIDs)) {
		return nil, fmt.Errorf("core: CTA %d dimensions require %d warps, got %d members", config.ID, requiredWarps, len(config.WarpIDs))
	}
	if config.LocalMemorySize > isa.FrozenLocalMemSize {
		return nil, fmt.Errorf("core: CTA %d LMEM size %d exceeds frozen size %d", config.ID, config.LocalMemorySize, isa.FrozenLocalMemSize)
	}
	aligned := alignUp(config.LocalMemorySize, frozenMemoryBlockSize)
	if aligned > isa.FrozenLocalMemSize {
		return nil, fmt.Errorf("core: CTA %d aligned LMEM size %d exceeds frozen size", config.ID, aligned)
	}
	config.WarpIDs = append([]uint8(nil), config.WarpIDs...)
	config.LocalMemorySize = aligned
	seen := make(map[uint8]struct{}, len(config.WarpIDs))
	members := make([]WarpMembership, len(config.WarpIDs))
	var warpBase [3]uint32
	for rank, warpID := range config.WarpIDs {
		if warpID >= isa.FrozenWarpCount {
			return nil, fmt.Errorf("core: CTA %d member warp %d exceeds frozen topology", config.ID, warpID)
		}
		if _, duplicate := seen[warpID]; duplicate {
			return nil, fmt.Errorf("core: CTA %d repeats member warp %d", config.ID, warpID)
		}
		seen[warpID] = struct{}{}
		coordinates := threadCoordinates(uint32(rank), config.BlockDimensions)
		if !legacyCoordinates {
			coordinates = warpStepCoordinates(warpBase, config.BlockDimensions)
		}
		remaining := config.BlockSize - uint32(rank)*uint32(isa.FrozenLaneCount)
		mask := isa.AllLanes
		if remaining < uint32(isa.FrozenLaneCount) {
			mask = isa.LaneMask((uint32(1) << remaining) - 1)
		}
		members[rank] = WarpMembership{WarpID: warpID, Rank: uint32(rank), ActiveMask: mask, ThreadCoordinates: coordinates}
		if !legacyCoordinates {
			warpBase = advanceWarpBase(warpBase, config.BlockDimensions, config.WarpStep)
		}
	}
	return &ctaState{config: config, members: members}, nil
}

func threadCoordinates(rank uint32, dimensions [3]uint32) [3]isa.LaneValues {
	var result [3]isa.LaneValues
	for lane := uint32(0); lane < isa.FrozenLaneCount; lane++ {
		linear := rank*isa.FrozenLaneCount + lane
		result[0][lane] = linear % dimensions[0]
		linear /= dimensions[0]
		result[1][lane] = linear % dimensions[1]
		result[2][lane] = linear / dimensions[1]
	}
	return result
}

func warpStepCoordinates(base [3]uint32, dimensions [3]uint32) [3]isa.LaneValues {
	var result [3]isa.LaneValues
	current := base
	for lane := uint32(0); lane < isa.FrozenLaneCount; lane++ {
		for axis := range 3 {
			result[axis][lane] = current[axis]
		}
		current = advanceLaneCoordinate(current, dimensions)
	}
	return result
}

func advanceLaneCoordinate(previous [3]uint32, dimensions [3]uint32) [3]uint32 {
	bdx, bdy := dimensions[0]&15, dimensions[1]&15
	nextX := previous[0] + 1
	wrapX := nextX >= bdx
	if wrapX {
		nextX -= bdx
	}
	nextY := previous[1]
	if wrapX {
		nextY++
	}
	wrapY := wrapX && nextY >= bdy
	if wrapY {
		nextY -= bdy
	}
	nextZ := previous[2]
	if wrapY {
		nextZ++
	}
	return [3]uint32{nextX & 15, nextY & 15, nextZ & 15}
}

func advanceWarpBase(previous [3]uint32, dimensions, step [3]uint32) [3]uint32 {
	bdx, bdy := dimensions[0]&15, dimensions[1]&15
	nextX := previous[0] + step[0]
	wrapX := nextX >= bdx
	if wrapX {
		nextX -= bdx
	}
	nextY := previous[1] + step[1]
	if wrapX {
		nextY++
	}
	// VX_cta_dispatch applies WarpStep.Y independently of the X carry, so Y
	// may wrap even when X did not. This differs from the per-lane +1 ripple.
	wrapY := nextY >= bdy
	if wrapY {
		nextY -= bdy
	}
	nextZ := previous[2] + step[2]
	if wrapY {
		nextZ++
	}
	return [3]uint32{nextX & 15, nextY & 15, nextZ & 15}
}

func alignUp(value, alignment uint32) uint32 {
	if value == 0 {
		return 0
	}
	return (value + alignment - 1) &^ (alignment - 1)
}

type allocationInterval struct{ start, end uint32 }

func (m *CTAManager) allocationIntervalsLocked() []allocationInterval {
	intervals := make([]allocationInterval, 0, len(m.ctas))
	for _, resident := range m.ctas {
		intervals = append(intervals, allocationInterval{resident.backingOffset, resident.backingOffset + resident.allocation.Size})
	}
	return intervals
}

func allocateFromIntervals(size uint32, intervals []allocationInterval) (LocalMemoryAllocation, uint32, error) {
	// Every CTA observes the same virtual LMEM window. The manager separately
	// packs each resident allocation into its sole physical byte array, so the
	// same numeric address is translated through CTA membership and cannot
	// alias another CTA's bytes.
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	var backingOffset uint32
	for _, occupied := range intervals {
		if uint64(backingOffset)+uint64(size) <= uint64(occupied.start) {
			break
		}
		if occupied.end > backingOffset {
			backingOffset = occupied.end
		}
	}
	if uint64(backingOffset)+uint64(size) > uint64(isa.FrozenLocalMemSize) {
		return LocalMemoryAllocation{}, 0, fmt.Errorf("core: no LMEM allocation of %d bytes remains", size)
	}
	return LocalMemoryAllocation{Address: isa.FrozenLocalMemBase, Size: size}, backingOffset, nil
}

func (m *CTAManager) allocateLocked(size uint32) (LocalMemoryAllocation, uint32, error) {
	return allocateFromIntervals(size, m.allocationIntervalsLocked())
}

func (m *CTAManager) HasWarp(warpID uint8) bool {
	if m == nil || warpID >= isa.FrozenWarpCount {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.byWarp[warpID] != nil
}

func (m *CTAManager) Snapshot(ctaID uint32) (CTASnapshot, error) {
	if m == nil {
		return CTASnapshot{}, fmt.Errorf("core: nil CTA manager")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cta := m.ctas[ctaID]
	if cta == nil {
		return CTASnapshot{}, fmt.Errorf("core: CTA %d is not resident", ctaID)
	}
	return snapshotCTA(cta), nil
}

func (m *CTAManager) residentIDs() ([]uint32, error) {
	if m == nil {
		return nil, fmt.Errorf("core: nil CTA manager")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]uint32, 0, len(m.ctas))
	for id := range m.ctas {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (m *CTAManager) completion(ctaID uint32, scheduler [isa.FrozenWarpCount]ctaSchedulerView) (CTACompletionSnapshot, error) {
	if m == nil {
		return CTACompletionSnapshot{}, fmt.Errorf("core: nil CTA manager")
	}
	m.mu.RLock()
	cta := m.ctas[ctaID]
	if cta == nil {
		m.mu.RUnlock()
		return CTACompletionSnapshot{}, fmt.Errorf("core: CTA %d is not resident", ctaID)
	}
	members := append([]WarpMembership(nil), cta.members...)
	barriers := m.barriers
	m.mu.RUnlock()

	result := CTACompletionSnapshot{CTAID: ctaID, Members: make([]CTACompletionMember, len(members))}
	allFinished := len(members) != 0
	for index, member := range members {
		view := scheduler[member.WarpID]
		result.Members[index] = CTACompletionMember{WarpID: member.WarpID, OwnerFinished: member.Finished,
			Lifecycle: view.lifecycle, BlockReason: view.blockReason}
		if view.lifecycle == WarpBlocked {
			result.BlockedMembers |= 1 << member.WarpID
		}
		if !member.Finished || view.lifecycle != WarpFinished {
			allFinished = false
		}
	}
	result.PendingBarrier = barriers != nil && barriers.pendingCTA(ctaID)
	result.Complete = allFinished && result.BlockedMembers == 0 && !result.PendingBarrier
	return result, nil
}

func snapshotCTA(cta *ctaState) CTASnapshot {
	result := CTASnapshot{
		ID: cta.config.ID, StartupPC: cta.config.StartupPC, BlockID: cta.config.BlockID,
		BlockDimensions: cta.config.BlockDimensions, GridDimensions: cta.config.GridDimensions,
		BlockSize: cta.config.BlockSize, WarpStep: cta.config.WarpStep,
		Entry: cta.config.Entry, ParameterAddress: cta.config.ParameterAddress,
		LocalMemory: cta.allocation, ClusterDimensions: cta.config.ClusterDimensions,
		ClusterSize: cta.config.ClusterSize, IsFirstOfCluster: cta.config.IsFirstOfCluster,
		Members: append([]WarpMembership(nil), cta.members...),
	}
	return result
}

func (m *CTAManager) ViewForWarp(warpID uint8) (isa.CTAView, error) {
	if m == nil || warpID >= isa.FrozenWarpCount {
		return isa.CTAView{}, fmt.Errorf("core: invalid CTA lookup warp %d", warpID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := m.byWarp[warpID]
	if key == nil {
		return isa.CTAView{}, fmt.Errorf("core: warp %d has no CTA membership", warpID)
	}
	cta := m.ctas[key.ctaID]
	if cta == nil || key.rank >= uint32(len(cta.members)) || cta.members[key.rank].WarpID != warpID {
		return isa.CTAView{}, fmt.Errorf("core: warp %d CTA membership is inconsistent", warpID)
	}
	member := cta.members[key.rank]
	return isa.CTAView{
		ID: cta.config.ID, Rank: member.Rank, Size: uint32(len(cta.members)),
		ThreadCoordinates: member.ThreadCoordinates, BlockID: cta.config.BlockID,
		BlockDimensions: cta.config.BlockDimensions, GridDimensions: cta.config.GridDimensions,
		BlockSize: cta.config.BlockSize, WarpStep: cta.config.WarpStep,
		ParameterAddress:   cta.config.ParameterAddress,
		LocalMemoryAddress: cta.allocation.Address, ClusterDimensions: cta.config.ClusterDimensions,
		ClusterSize: cta.config.ClusterSize, Entry: cta.config.Entry,
	}, nil
}

func (m *CTAManager) ValidateSpawn(sourceID uint8, targets isa.WarpMask) error {
	if m == nil || sourceID >= isa.FrozenWarpCount || !targets.Valid() {
		return fmt.Errorf("invalid source or target mask")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	source := m.byWarp[sourceID]
	if source == nil {
		return fmt.Errorf("source warp %d has no CTA", sourceID)
	}
	cta := m.ctas[source.ctaID]
	var members isa.WarpMask
	for _, member := range cta.members {
		members |= 1 << member.WarpID
	}
	activated := targets | isa.WarpMask(1<<sourceID)
	if activated != members {
		return fmt.Errorf("source/target mask %#x does not equal CTA %d membership %#x", activated, source.ctaID, members)
	}
	for targetID := uint8(0); targetID < isa.FrozenWarpCount; targetID++ {
		if targets&(1<<targetID) == 0 {
			continue
		}
		target := m.byWarp[targetID]
		if target == nil || target.ctaID != source.ctaID {
			return fmt.Errorf("target warp %d does not belong to source CTA %d", targetID, source.ctaID)
		}
	}
	return nil
}

func (m *CTAManager) observeWarpFinished(warpID uint8) error {
	if m == nil || warpID >= isa.FrozenWarpCount {
		return fmt.Errorf("core: invalid finished warp %d", warpID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.byWarp[warpID]
	if key == nil {
		return fmt.Errorf("core: finished warp %d has no CTA membership", warpID)
	}
	cta := m.ctas[key.ctaID]
	if cta == nil || key.rank >= uint32(len(cta.members)) || cta.members[key.rank].WarpID != warpID {
		return fmt.Errorf("core: finished warp %d has inconsistent CTA membership", warpID)
	}
	cta.members[key.rank].Finished = true
	return nil
}

type memoryRoute uint8

const (
	routeGlobal memoryRoute = iota
	routeLocal
)

// CTAMemory routes the frozen LMEM virtual window through canonical CTA
// membership. It retains no bytes and uses the global owner for every wholly
// non-LMEM range.
type CTAMemory struct {
	manager *CTAManager
	warpID  uint8
	global  warp.MemoryService
}

func NewCTAMemory(manager *CTAManager, warpID uint8, global warp.MemoryService) (*CTAMemory, error) {
	if manager == nil || global == nil {
		return nil, fmt.Errorf("core: CTA memory requires manager and global owner")
	}
	if warpID >= isa.FrozenWarpCount {
		return nil, fmt.Errorf("core: CTA memory warp %d exceeds frozen topology", warpID)
	}
	return &CTAMemory{manager: manager, warpID: warpID, global: global}, nil
}

// BoundWarpID lets Warp constructors reject a router attached to the wrong
// architectural owner while keeping the service itself byte-only.
func (m *CTAMemory) BoundWarpID() uint8 { return m.warpID }

func classifyMemoryRange(address uint32, length uint64) (memoryRoute, error) {
	start, end := uint64(address), uint64(address)+length
	if end > uint64(1)<<32 {
		return 0, ErrMemoryCrossing
	}
	localStart := uint64(isa.FrozenLocalMemBase)
	localEnd := localStart + uint64(isa.FrozenLocalMemSize)
	if length == 0 {
		if start >= localStart && start < localEnd {
			return routeLocal, nil
		}
		return routeGlobal, nil
	}
	overlaps := start < localEnd && localStart < end
	inside := start >= localStart && end <= localEnd
	if overlaps && !inside {
		return 0, ErrMemoryCrossing
	}
	if inside {
		return routeLocal, nil
	}
	return routeGlobal, nil
}

func (m *CTAMemory) Read(address uint32, destination []byte) error {
	route, err := classifyMemoryRange(address, uint64(len(destination)))
	if err != nil {
		return err
	}
	if route == routeGlobal {
		return m.global.Read(address, destination)
	}
	return m.manager.readLocal(m.warpID, address, destination)
}

func (m *CTAMemory) Write(address uint32, source []byte) error {
	route, err := classifyMemoryRange(address, uint64(len(source)))
	if err != nil {
		return err
	}
	if route == routeGlobal {
		return m.global.Write(address, source)
	}
	return m.manager.writeLocal(m.warpID, address, source)
}

func (m *CTAMemory) WriteBatch(addresses []uint32, sources [][]byte) error {
	if len(addresses) != len(sources) {
		return fmt.Errorf("core: atomic batch has %d addresses and %d sources", len(addresses), len(sources))
	}
	localAddresses := make([]uint32, 0, len(addresses))
	localSources := make([][]byte, 0, len(addresses))
	globalAddresses := make([]uint32, 0, len(addresses))
	globalSources := make([][]byte, 0, len(addresses))
	for index, address := range addresses {
		entryRoute, err := classifyMemoryRange(address, uint64(len(sources[index])))
		if err != nil {
			return err
		}
		if entryRoute == routeLocal {
			localAddresses = append(localAddresses, address)
			localSources = append(localSources, sources[index])
		} else {
			globalAddresses = append(globalAddresses, address)
			globalSources = append(globalSources, sources[index])
		}
	}
	if len(globalAddresses) == 0 {
		return m.manager.writeLocalBatch(m.warpID, addresses, sources)
	}
	atomic, ok := m.global.(warp.AtomicMemoryService)
	if !ok {
		return fmt.Errorf("core: global memory lacks atomic batch support")
	}
	if len(localAddresses) == 0 {
		return atomic.WriteBatch(globalAddresses, globalSources)
	}
	return m.manager.writeLocalWithExternal(m.warpID, localAddresses, localSources, func() error {
		return atomic.WriteBatch(globalAddresses, globalSources)
	})
}

func (m *CTAManager) localRangeLocked(warpID uint8, address uint32, length uint64) (uint32, error) {
	if warpID >= isa.FrozenWarpCount || m.byWarp[warpID] == nil {
		return 0, fmt.Errorf("%w: warp %d has no CTA", ErrLocalMemoryRange, warpID)
	}
	cta := m.ctas[m.byWarp[warpID].ctaID]
	start := uint64(address)
	end := start + length
	allocStart := uint64(cta.allocation.Address)
	allocEnd := allocStart + uint64(cta.allocation.Size)
	if start < allocStart || end > allocEnd || (length != 0 && start == allocEnd) {
		return 0, fmt.Errorf("%w: CTA %d address=%#x length=%d allocation=[%#x,%#x)", ErrLocalMemoryRange, cta.config.ID, address, length, allocStart, allocEnd)
	}
	return cta.backingOffset + address - cta.allocation.Address, nil
}

func (m *CTAManager) readLocal(warpID uint8, address uint32, destination []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	offset, err := m.localRangeLocked(warpID, address, uint64(len(destination)))
	if err != nil {
		return err
	}
	copy(destination, m.localMem[offset:offset+uint32(len(destination))])
	return nil
}

func (m *CTAManager) writeLocal(warpID uint8, address uint32, source []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offset, err := m.localRangeLocked(warpID, address, uint64(len(source)))
	if err != nil {
		return err
	}
	copy(m.localMem[offset:offset+uint32(len(source))], source)
	return nil
}

func (m *CTAManager) writeLocalBatch(warpID uint8, addresses []uint32, sources [][]byte) error {
	return m.writeLocalWithExternal(warpID, addresses, sources, nil)
}

// writeLocalWithExternal validates every local range while holding its owner
// lock, then commits the all-or-error external batch before applying local
// candidates with no remaining failure point. This is the same external-first,
// infallible-local pattern used by Warp/State coordinated stores.
func (m *CTAManager) writeLocalWithExternal(warpID uint8, addresses []uint32, sources [][]byte, external func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	offsets := make([]uint32, len(addresses))
	for index, address := range addresses {
		offset, err := m.localRangeLocked(warpID, address, uint64(len(sources[index])))
		if err != nil {
			return err
		}
		start, end := uint64(address), uint64(address)+uint64(len(sources[index]))
		for previous := 0; previous < index; previous++ {
			otherStart := uint64(addresses[previous])
			otherEnd := otherStart + uint64(len(sources[previous]))
			if start < otherEnd && otherStart < end {
				return fmt.Errorf("core: overlapping local-memory batch entries %d and %d", previous, index)
			}
		}
		offsets[index] = offset
	}
	if external != nil {
		if err := external(); err != nil {
			return err
		}
	}
	for index, offset := range offsets {
		copy(m.localMem[offset:offset+uint32(len(sources[index]))], sources[index])
	}
	return nil
}
