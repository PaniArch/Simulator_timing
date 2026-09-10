package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func TestKernelSpawnRetainsCTAMembership(t *testing.T) {
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	put := func(pc, w uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], w)
		if err := ram.Write(pc, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	// Warp 1 initially stops. Warp 0 waits in WSPAWN until the registered
	// single-active condition, then restarts Warp 1 at 0x300 with lane 0 only.
	spawn := customWord(t, "wspawn", 0, 2) | 3<<20
	for i, w := range []uint32{0xcc1020f3, 0x00009e63, 0x00200113, 0x30000193, spawn, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	put(0x120, 0xb)
	for i, w := range []uint32{0x34002273, 0x04d00293, 0x00522023, 0xb} {
		put(0x300+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x300, ParameterAddress: 0x800, GridDimensions: [3]uint32{1, 1, 1}, BlockDimensions: [3]uint32{4, 4, 1}, BlockSize: 16, WarpStep: [3]uint32{0, 1, 0}, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: kernelMemoryConfig(70)})
	if err != nil {
		t.Fatal(err)
	}
	activated := false
	if err := k.Run(1000, func(r runner.MultiRecord) {
		for _, f := range r.Report.Wakeups {
			if f.Kind == "spawn" {
				activated = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !activated || !k.Status().Complete || k.Status().Completed != 1 {
		t.Fatal(k.Status(), activated)
	}
	var b [4]byte
	kernelVisible(t, k)
	if err := ram.Read(0x800, b[:]); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(b[:]) != 77 {
		t.Fatal(b)
	}
	// A count covering unowned Warp slots must fail after operands resolve.
	put(0x108, 0x00400113)
	launch.BlockDimensions = [3]uint32{8, 1, 1}
	launch.BlockSize = 8
	launch.WarpStep = [3]uint32{4, 0, 0}
	invalid, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: kernelMemoryConfig(20)})
	if err != nil {
		t.Fatal(err)
	}
	if err := invalid.Run(1000, nil); err == nil {
		t.Fatal("spawn into unbound CTA slots accepted")
	}

}
