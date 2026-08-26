package device_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"vortex.local/simulator/device"
	"vortex.local/simulator/isa"
)

func validLaunch() device.LaunchState {
	return device.LaunchState{
		StartupPC: 0x100, KernelEntryPC: 0x240, ParameterAddress: 0x12345,
		GridDimensions: [3]uint32{4, 2, 2}, BlockDimensions: [3]uint32{8, 2, 1},
		BlockSize: 3, WarpStep: [3]uint32{4, 15, 0}, LocalMemorySize: 65,
		ClusterDimensions: [3]uint32{2, 2, 1},
	}
}

func TestLaunchStatePreservesIndependentKMUFieldsAndDerivesResources(t *testing.T) {
	input := validLaunch()
	launch, err := device.NewLaunchState(input)
	if err != nil {
		t.Fatal(err)
	}
	if launch.StartupPC != input.StartupPC || launch.KernelEntryPC != input.KernelEntryPC ||
		launch.StartupPC == launch.KernelEntryPC || launch.ParameterAddress != input.ParameterAddress {
		t.Fatalf("startup/entry/parameter fields were mixed: %+v", launch)
	}
	if launch.BlockDimensions != input.BlockDimensions || launch.BlockSize != 3 || launch.WarpStep != input.WarpStep {
		t.Fatalf("independently consumed block fields were mixed: %+v", launch)
	}
	if launch.BlockVolume != 16 || launch.WarpsPerCTA != 1 || launch.AlignedLocalMemorySize != 128 ||
		launch.ClusterSize != 4 || launch.ClusterWarpDemand != 4 || launch.ClusterLocalMemorySize != 512 ||
		launch.ResidentCTACapacity != 4 || launch.TotalCTAs != 16 {
		t.Fatalf("wrong derived launch facts: %+v", launch)
	}

	walker, err := device.NewGridWalker(launch)
	if err != nil {
		t.Fatal(err)
	}
	cta, ok := walker.Next()
	if !ok || cta.StartupPC != input.StartupPC || cta.KernelEntryPC != input.KernelEntryPC ||
		cta.ParameterAddress != input.ParameterAddress || cta.BlockSize != input.BlockSize || cta.WarpStep != input.WarpStep {
		t.Fatalf("CTA generation replaced an independent field: %+v, ok=%v", cta, ok)
	}
	config := cta.CoreConfig()
	if config.StartupPC != cta.StartupPC || config.Entry != cta.KernelEntryPC ||
		config.ParameterAddress != cta.ParameterAddress || config.BlockSize != cta.BlockSize ||
		config.BlockID != cta.BlockID || config.BlockDimensions != cta.BlockDimensions ||
		config.GridDimensions != cta.GridDimensions || config.WarpStep != cta.WarpStep ||
		config.LocalMemorySize != cta.AlignedLocalMemorySize ||
		config.ClusterDimensions != cta.ClusterDimensions || config.ClusterSize != cta.ClusterSize ||
		config.IsFirstOfCluster != cta.IsFirstOfCluster || config.WarpIDs != nil {
		t.Fatalf("device CTA did not preserve the dynamic Core context: %+v", config)
	}
}

func TestGridWalkerMatchesKMUClusterAndXYZOrder(t *testing.T) {
	walker, err := device.NewGridWalker(validLaunch())
	if err != nil {
		t.Fatal(err)
	}
	want := [][3]uint32{
		{0, 0, 0}, {1, 0, 0}, {0, 1, 0}, {1, 1, 0},
		{2, 0, 0}, {3, 0, 0}, {2, 1, 0}, {3, 1, 0},
		{0, 0, 1}, {1, 0, 1}, {0, 1, 1}, {1, 1, 1},
		{2, 0, 1}, {3, 0, 1}, {2, 1, 1}, {3, 1, 1},
	}
	var got [][3]uint32
	seen := make(map[[3]uint32]bool)
	for id := uint32(0); ; id++ {
		cta, ok := walker.Next()
		if !ok {
			break
		}
		if cta.ID != id || cta.ClusterRank >= cta.ClusterSize || cta.IsFirstOfCluster != (cta.ClusterRank == 0) {
			t.Fatalf("bad CTA metadata at %d: %+v", id, cta)
		}
		if seen[cta.BlockID] {
			t.Fatalf("duplicate BlockID %v", cta.BlockID)
		}
		seen[cta.BlockID] = true
		got = append(got, cta.BlockID)
	}
	if !reflect.DeepEqual(got, want) || walker.Remaining() != 0 {
		t.Fatalf("KMU walk mismatch\n got: %v\nwant: %v", got, want)
	}
	if _, ok := walker.Next(); ok {
		t.Fatal("exhausted walker resumed")
	}
}

func TestGridWalkerEmptyGridIsDeterministicallyExhausted(t *testing.T) {
	for axis := range 3 {
		input := validLaunch()
		input.GridDimensions[axis] = 0
		// Empty grids do not execute the equality-wrap walk, so unrelated axes
		// need not be divisible by the cluster shape.
		input.GridDimensions[(axis+1)%3] = 3
		walker, err := device.NewGridWalker(input)
		if err != nil {
			t.Fatalf("axis %d: %v", axis, err)
		}
		if walker.Launch().TotalCTAs != 0 || walker.Remaining() != 0 {
			t.Fatalf("axis %d: empty launch has work", axis)
		}
		for attempt := 0; attempt < 3; attempt++ {
			if _, ok := walker.Next(); ok {
				t.Fatalf("axis %d attempt %d: empty grid emitted CTA", axis, attempt)
			}
		}
	}
}

func TestLaunchValidationRejectsInvalidFieldsAndResourceCombinations(t *testing.T) {
	tests := []struct {
		name string
		edit func(*device.LaunchState)
		want string
	}{
		{"startup alignment", func(v *device.LaunchState) { v.StartupPC++ }, "startup PC"},
		{"entry alignment", func(v *device.LaunchState) { v.KernelEntryPC += 2 }, "kernel entry PC"},
		{"zero block dimension", func(v *device.LaunchState) { v.BlockDimensions[1] = 0 }, "block dimension Y"},
		{"wide block dimension", func(v *device.LaunchState) { v.BlockDimensions[0] = 32 }, "5-bit"},
		{"zero block size", func(v *device.LaunchState) { v.BlockSize = 0 }, "block size"},
		{"large block size", func(v *device.LaunchState) { v.BlockSize = 17 }, "block size"},
		{"wide warp step", func(v *device.LaunchState) { v.WarpStep[2] = 16 }, "warp step Z"},
		{"large LMEM", func(v *device.LaunchState) { v.LocalMemorySize = isa.FrozenLocalMemSize + 1 }, "local-memory"},
		{"zero cluster dimension", func(v *device.LaunchState) { v.ClusterDimensions[0] = 0 }, "cluster dimension X"},
		{"wide cluster dimension", func(v *device.LaunchState) { v.ClusterDimensions = [3]uint32{8, 1, 1} }, "3-bit"},
		{"cluster slot product", func(v *device.LaunchState) { v.ClusterDimensions = [3]uint32{2, 2, 2} }, "cluster product"},
		{"cluster warp capacity", func(v *device.LaunchState) { v.BlockSize = 5 }, "co-resident capacity"},
		{"cluster LMEM capacity", func(v *device.LaunchState) { v.LocalMemorySize = 8192 }, "co-resident capacity"},
		{"nontraversable grid", func(v *device.LaunchState) { v.GridDimensions[0] = 3 }, "not divisible"},
		{"CTA count overflow", func(v *device.LaunchState) {
			v.GridDimensions = [3]uint32{math.MaxUint32, 2, 1}
			v.ClusterDimensions = [3]uint32{1, 1, 1}
		}, "CTA count"},
		{"conflicting derived fact", func(v *device.LaunchState) { v.WarpsPerCTA = 4 }, "derived WarpsPerCTA"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validLaunch()
			test.edit(&input)
			if _, err := device.NewGridWalker(input); err == nil || !errors.Is(err, device.ErrInvalidLaunch) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want invalid launch containing %q", err, test.want)
			}
		})
	}
}

func TestLaunchValidationBoundaryValues(t *testing.T) {
	input := validLaunch()
	input.GridDimensions = [3]uint32{math.MaxUint32, 1, 1}
	input.BlockDimensions = [3]uint32{31, 31, 31}
	input.BlockSize = 16
	input.WarpStep = [3]uint32{15, 15, 15}
	input.LocalMemorySize = isa.FrozenLocalMemSize
	input.ClusterDimensions = [3]uint32{1, 1, 1}
	launch, err := device.ValidateLaunch(input)
	if err != nil {
		t.Fatal(err)
	}
	if launch.TotalCTAs != math.MaxUint32 || launch.BlockVolume != 31*31*31 ||
		launch.WarpsPerCTA != 4 || launch.AlignedLocalMemorySize != isa.FrozenLocalMemSize ||
		launch.ClusterWarpDemand != 4 || launch.ClusterLocalMemorySize != isa.FrozenLocalMemSize || launch.ResidentCTACapacity != 1 {
		t.Fatalf("wrong boundary derivation: %+v", launch)
	}
	validatedAgain, err := device.ValidateLaunch(launch)
	if err != nil || validatedAgain != launch {
		t.Fatalf("validation is not idempotent: %+v, %v", validatedAgain, err)
	}
}
