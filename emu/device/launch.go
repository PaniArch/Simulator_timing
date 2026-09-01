// Package device owns runtime-independent kernel launch orchestration for the
// frozen Vortex functional model.
package device

import (
	"errors"
	"fmt"
	"math"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/isa"
)

const (
	frozenMemoryBlockSize = uint32(64)
	frozenBlockFieldMax   = uint32(31) // CTA_TID_WIDTH+1 (5 bits).
	frozenWarpStepMax     = uint32(15) // CTA_TID_WIDTH (4 bits).
	frozenClusterDimMax   = uint32(7)  // NW_WIDTH+1 (3 bits).
)

// ErrInvalidLaunch identifies a launch rejected before any execution state is
// created. Callers may use errors.Is and retain the detailed wrapped reason.
var ErrInvalidLaunch = errors.New("device: invalid kernel launch")

// LaunchState is the stable, runtime-independent public kernel launch
// contract. The first group of fields mirrors values consumed independently by
// VX_kmu. The second group is normalized by ValidateLaunch and must not be
// supplied as a conflicting second truth.
//
// Kernel image loading, host argument structures, symbols, and DCR transport
// are intentionally outside this type. ParameterAddress already names bytes in
// the caller-owned device memory.
type LaunchState struct {
	StartupPC         uint32
	KernelEntryPC     uint32
	ParameterAddress  uint32
	GridDimensions    [3]uint32
	BlockDimensions   [3]uint32
	BlockSize         uint32
	WarpStep          [3]uint32
	LocalMemorySize   uint32
	ClusterDimensions [3]uint32

	BlockVolume            uint32
	WarpsPerCTA            uint32
	AlignedLocalMemorySize uint32
	ClusterSize            uint32
	ClusterWarpDemand      uint32
	ClusterLocalMemorySize uint32
	ResidentCTACapacity    uint32
	TotalCTAs              uint32
}

// ValidateLaunch validates every launch and resource relationship before a
// GridWalker or later execution owner can be created. A successful result is a
// detached normalized value; the input is never modified.
func ValidateLaunch(input LaunchState) (LaunchState, error) {
	if input.StartupPC&3 != 0 {
		return LaunchState{}, invalid("startup PC %#x is not RV32 instruction aligned", input.StartupPC)
	}
	if input.KernelEntryPC&3 != 0 {
		return LaunchState{}, invalid("kernel entry PC %#x is not RV32 instruction aligned", input.KernelEntryPC)
	}

	blockVolume := uint64(1)
	for axis := range 3 {
		dimension := input.BlockDimensions[axis]
		if dimension == 0 || dimension > frozenBlockFieldMax {
			return LaunchState{}, invalid("block dimension %s=%d is outside the frozen 5-bit nonzero range", axisName(axis), dimension)
		}
		var err error
		blockVolume, err = checkedMultiply(blockVolume, uint64(dimension), "block-dimension product")
		if err != nil {
			return LaunchState{}, err
		}
		if input.WarpStep[axis] > frozenWarpStepMax {
			return LaunchState{}, invalid("warp step %s=%d exceeds the frozen 4-bit field", axisName(axis), input.WarpStep[axis])
		}
	}
	if blockVolume > math.MaxUint32 {
		return LaunchState{}, invalid("block-dimension product %d exceeds uint32", blockVolume)
	}
	if input.BlockSize == 0 || input.BlockSize > uint32(isa.FrozenWarpCount*isa.FrozenLaneCount) {
		return LaunchState{}, invalid("block size %d is outside frozen core capacity 1..%d", input.BlockSize, isa.FrozenWarpCount*isa.FrozenLaneCount)
	}
	warpsPerCTA := (input.BlockSize + uint32(isa.FrozenLaneCount) - 1) / uint32(isa.FrozenLaneCount)

	if input.LocalMemorySize > isa.FrozenLocalMemSize {
		return LaunchState{}, invalid("local-memory size %d exceeds frozen capacity %d", input.LocalMemorySize, isa.FrozenLocalMemSize)
	}
	alignedLocalMemory := alignUp(input.LocalMemorySize, frozenMemoryBlockSize)
	if alignedLocalMemory > isa.FrozenLocalMemSize {
		return LaunchState{}, invalid("64-byte-aligned local-memory size %d exceeds frozen capacity %d", alignedLocalMemory, isa.FrozenLocalMemSize)
	}

	clusterSize := uint64(1)
	for axis := range 3 {
		dimension := input.ClusterDimensions[axis]
		if dimension == 0 || dimension > frozenClusterDimMax {
			return LaunchState{}, invalid("cluster dimension %s=%d is outside the frozen 3-bit nonzero range", axisName(axis), dimension)
		}
		var err error
		clusterSize, err = checkedMultiply(clusterSize, uint64(dimension), "cluster-dimension product")
		if err != nil {
			return LaunchState{}, err
		}
		if clusterSize > uint64(isa.FrozenWarpCount) {
			return LaunchState{}, invalid("cluster product %d exceeds %d co-resident CTA slots", clusterSize, isa.FrozenWarpCount)
		}
	}
	clusterWarpDemand, err := checkedMultiply(clusterSize, uint64(warpsPerCTA), "cluster warp demand")
	if err != nil {
		return LaunchState{}, err
	}
	if clusterWarpDemand > uint64(isa.FrozenWarpCount) {
		return LaunchState{}, invalid("cluster warp demand %d exceeds co-resident capacity %d", clusterWarpDemand, isa.FrozenWarpCount)
	}
	clusterLocalMemory, err := checkedMultiply(clusterSize, uint64(alignedLocalMemory), "cluster local-memory demand")
	if err != nil {
		return LaunchState{}, err
	}
	if clusterLocalMemory > uint64(isa.FrozenLocalMemSize) {
		return LaunchState{}, invalid("cluster local-memory demand %d exceeds co-resident capacity %d", clusterLocalMemory, isa.FrozenLocalMemSize)
	}

	residentByWarps := uint32(isa.FrozenWarpCount) / warpsPerCTA
	residentByLocalMemory := uint32(isa.FrozenWarpCount)
	if alignedLocalMemory != 0 {
		residentByLocalMemory = isa.FrozenLocalMemSize / alignedLocalMemory
		if residentByLocalMemory > uint32(isa.FrozenWarpCount) {
			residentByLocalMemory = uint32(isa.FrozenWarpCount)
		}
	}
	residentCapacity := min(residentByWarps, residentByLocalMemory)
	if uint32(clusterSize) > residentCapacity {
		return LaunchState{}, invalid("cluster size %d exceeds co-resident capacity %d (warps/CTA=%d, aligned LMEM/CTA=%d)", clusterSize, residentCapacity, warpsPerCTA, alignedLocalMemory)
	}

	gridEmpty := false
	for _, dimension := range input.GridDimensions {
		if dimension == 0 {
			gridEmpty = true
		}
	}
	var totalCTAs uint64
	if !gridEmpty {
		totalCTAs = 1
		for axis := range 3 {
			grid := input.GridDimensions[axis]
			cluster := input.ClusterDimensions[axis]
			if grid%cluster != 0 {
				return LaunchState{}, invalid("grid dimension %s=%d is not divisible by cluster dimension %d", axisName(axis), grid, cluster)
			}
			var err error
			totalCTAs, err = checkedMultiply(totalCTAs, uint64(grid), "CTA count")
			if err != nil {
				return LaunchState{}, err
			}
			if totalCTAs > math.MaxUint32 {
				return LaunchState{}, invalid("CTA count %d exceeds the frozen 32-bit counter", totalCTAs)
			}
		}
	}

	normalized := input
	derived := []struct {
		name     string
		provided uint32
		computed uint32
	}{
		{"BlockVolume", input.BlockVolume, uint32(blockVolume)},
		{"WarpsPerCTA", input.WarpsPerCTA, warpsPerCTA},
		{"AlignedLocalMemorySize", input.AlignedLocalMemorySize, alignedLocalMemory},
		{"ClusterSize", input.ClusterSize, uint32(clusterSize)},
		{"ClusterWarpDemand", input.ClusterWarpDemand, uint32(clusterWarpDemand)},
		{"ClusterLocalMemorySize", input.ClusterLocalMemorySize, uint32(clusterLocalMemory)},
		{"ResidentCTACapacity", input.ResidentCTACapacity, residentCapacity},
		{"TotalCTAs", input.TotalCTAs, uint32(totalCTAs)},
	}
	for _, field := range derived {
		if field.provided != 0 && field.provided != field.computed {
			return LaunchState{}, invalid("derived %s=%d conflicts with computed value %d", field.name, field.provided, field.computed)
		}
	}
	normalized.BlockVolume = uint32(blockVolume)
	normalized.WarpsPerCTA = warpsPerCTA
	normalized.AlignedLocalMemorySize = alignedLocalMemory
	normalized.ClusterSize = uint32(clusterSize)
	normalized.ClusterWarpDemand = uint32(clusterWarpDemand)
	normalized.ClusterLocalMemorySize = uint32(clusterLocalMemory)
	normalized.ResidentCTACapacity = residentCapacity
	normalized.TotalCTAs = uint32(totalCTAs)
	return normalized, nil
}

// NewLaunchState is the constructor spelling for ValidateLaunch.
func NewLaunchState(input LaunchState) (LaunchState, error) { return ValidateLaunch(input) }

// CTA is one immutable KMU-equivalent launch record. StartupPC remains the
// initial Warp PC; KernelEntryPC is CTA context and is never substituted for it.
type CTA struct {
	ID                     uint32
	StartupPC              uint32
	KernelEntryPC          uint32
	ParameterAddress       uint32
	BlockID                [3]uint32
	BlockDimensions        [3]uint32
	GridDimensions         [3]uint32
	BlockSize              uint32
	WarpStep               [3]uint32
	AlignedLocalMemorySize uint32
	ClusterDimensions      [3]uint32
	ClusterSize            uint32
	ClusterOrigin          [3]uint32
	IntraClusterOffset     [3]uint32
	ClusterRank            uint32
	IsFirstOfCluster       bool
}

// CoreConfig converts one generated KMU record into the dynamic CTA context
// consumed by Core. Resident CTA and Warp slot IDs remain Core-owned outputs.
func (c CTA) CoreConfig() core.CTAConfig {
	return core.CTAConfig{
		StartupPC: c.StartupPC, BlockID: c.BlockID,
		BlockDimensions: c.BlockDimensions, GridDimensions: c.GridDimensions,
		BlockSize: c.BlockSize, WarpStep: c.WarpStep,
		Entry: c.KernelEntryPC, ParameterAddress: c.ParameterAddress,
		LocalMemorySize:   c.AlignedLocalMemorySize,
		ClusterDimensions: c.ClusterDimensions, ClusterSize: c.ClusterSize,
		IsFirstOfCluster: c.IsFirstOfCluster,
	}
}

// GridWalker produces the exact frozen VX_kmu nested order. It owns only CTA
// generation progress, not CTA residency, Warp state, or completion.
type GridWalker struct {
	launch  LaunchState
	origin  [3]uint32
	intra   [3]uint32
	emitted uint32
}

// NewGridWalker validates before allocating generation state. Invalid launch
// input therefore cannot leave a partially initialized executor or walker.
func NewGridWalker(input LaunchState) (*GridWalker, error) {
	launch, err := ValidateLaunch(input)
	if err != nil {
		return nil, err
	}
	return &GridWalker{launch: launch}, nil
}

// Launch returns a detached copy of the normalized launch contract.
func (w *GridWalker) Launch() LaunchState {
	if w == nil {
		return LaunchState{}
	}
	return w.launch
}

// Remaining returns the number of CTAs not yet emitted.
func (w *GridWalker) Remaining() uint32 {
	if w == nil || w.emitted >= w.launch.TotalCTAs {
		return 0
	}
	return w.launch.TotalCTAs - w.emitted
}

// Next returns each BlockID exactly once, with X the fastest-changing axis at
// both the intra-cluster and cluster-origin levels. An empty grid returns false
// immediately and remains exhausted.
func (w *GridWalker) Next() (CTA, bool) {
	if w == nil || w.emitted >= w.launch.TotalCTAs {
		return CTA{}, false
	}
	blockID := [3]uint32{
		w.origin[0] + w.intra[0],
		w.origin[1] + w.intra[1],
		w.origin[2] + w.intra[2],
	}
	rank := w.intra[0] + w.launch.ClusterDimensions[0]*(w.intra[1]+w.launch.ClusterDimensions[1]*w.intra[2])
	result := CTA{
		ID: w.emitted, StartupPC: w.launch.StartupPC, KernelEntryPC: w.launch.KernelEntryPC,
		ParameterAddress: w.launch.ParameterAddress, BlockID: blockID,
		BlockDimensions: w.launch.BlockDimensions, GridDimensions: w.launch.GridDimensions,
		BlockSize: w.launch.BlockSize, WarpStep: w.launch.WarpStep,
		AlignedLocalMemorySize: w.launch.AlignedLocalMemorySize,
		ClusterDimensions:      w.launch.ClusterDimensions, ClusterSize: w.launch.ClusterSize,
		ClusterOrigin: w.origin, IntraClusterOffset: w.intra, ClusterRank: rank,
		IsFirstOfCluster: rank == 0,
	}
	w.emitted++
	w.advance()
	return result, true
}

func (w *GridWalker) advance() {
	for axis := range 3 {
		w.intra[axis]++
		if w.intra[axis] < w.launch.ClusterDimensions[axis] {
			return
		}
		w.intra[axis] = 0
	}
	for axis := range 3 {
		w.origin[axis] += w.launch.ClusterDimensions[axis]
		if w.origin[axis] < w.launch.GridDimensions[axis] {
			return
		}
		w.origin[axis] = 0
	}
}

func alignUp(value, alignment uint32) uint32 {
	if value == 0 {
		return 0
	}
	return (value + alignment - 1) &^ (alignment - 1)
}

func checkedMultiply(left, right uint64, label string) (uint64, error) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, invalid("%s overflows uint64", label)
	}
	return left * right, nil
}

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidLaunch, fmt.Sprintf(format, arguments...))
}

func axisName(axis int) string {
	return [...]string{"X", "Y", "Z"}[axis]
}
