package runner

import (
	"encoding/binary"
	"fmt"
	"testing"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

func lifecycleKernel(t *testing.T) (*Kernel, device.LaunchState) {
	t.Helper()
	ram, err := memory.New(0x12000)
	if err != nil {
		t.Fatal(err)
	}
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], 0x0000000b)
	if err := ram.Write(0x100, word[:]); err != nil {
		t.Fatal(err)
	}
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, GridDimensions: [3]uint32{1, 1, 1}, BlockDimensions: [3]uint32{1, 1, 1}, BlockSize: 1, ClusterDimensions: [3]uint32{1, 1, 1}}
	k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	return k, launch
}
func finishLifecycle(t *testing.T, k *Kernel) {
	t.Helper()
	if err := k.Run(2000, nil); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || !k.Status().MemoryDrained {
		t.Fatal(k.Status())
	}
}

func TestDeviceMemorySuccessor(t *testing.T) {
	k, launch := lifecycleKernel(t)
	if _, err := k.NextLaunch(launch); err == nil {
		t.Fatal("accepted unfinished launch")
	}
	finishLifecycle(t, k)
	cold := k.Status().LaunchCycles
	system, clock := k.runner.hierarchy.system, k.runner.clock
	next, err := k.NextLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	if next.runner.hierarchy.system != system || next.runner.clock != clock || next.Status().StartCycle != cold || next.Status().LaunchID != 2 {
		t.Fatal("device state was reconstructed", next.Status())
	}
	if err := k.Run(1, nil); err == nil {
		t.Fatal("old owner advanced")
	}
	if _, err := k.FlushCaches(1); err == nil {
		t.Fatal("old owner flushed")
	}
	if _, err := k.MakeVisible(1); err == nil {
		t.Fatal("old owner wrote back")
	}
	if _, err := k.NextLaunch(launch); err == nil {
		t.Fatal("double transfer")
	}
	id := next.runner.hierarchy.identity(model.Token{Warp: 0, ID: 1}, 1)
	// No CTA is bound yet; after admission requests must carry the new launch.
	finishLifecycle(t, next)
	if next.Status().LaunchCycles >= cold {
		t.Fatal("warm cache did not avoid cold initialization/miss", cold, next.Status())
	}
	if id.Kernel != 0 {
		t.Fatal("unbound residency unexpectedly valid", id)
	}
	if done, err := next.FlushCaches(2000); err != nil || !done {
		t.Fatal(done, err)
	}
	before := next.Status().Cycle
	third, err := next.NextLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	if third.runner.hierarchy.system != system || third.Status().StartCycle != before {
		t.Fatal("flush reset device")
	}
	var saw bool
	if err := third.Run(2000, func(r MultiRecord) {
		for identity := range third.runner.hierarchy.fetchTokens {
			saw = true
			if identity.Kernel != 3 {
				t.Fatalf("stale launch identity: %+v", identity)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !saw || !third.Status().Complete {
		t.Fatal("missing new launch requests")
	}
	other, _ := lifecycleKernel(t)
	if other.runner.hierarchy.system == system || other.Status().Cycle != 0 {
		t.Fatal("devices share state")
	}
	finishLifecycle(t, other)
	if other.Status().LaunchCycles != cold {
		t.Fatal("independent cold start changed")
	}
}

func TestDeviceMemoryTransferGuards(t *testing.T) {
	k, launch := lifecycleKernel(t)
	finishLifecycle(t, k)
	bad := launch
	bad.BlockSize = 0
	if _, err := k.NextLaunch(bad); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	if k.transferred {
		t.Fatal("failed launch consumed ownership")
	}
	originalBackend := k.options.Backend
	k.options.Backend = "unsupported-backend"
	if _, err := k.NextLaunch(launch); err == nil {
		t.Fatal("invalid construction accepted")
	}
	k.options.Backend = originalBackend
	if k.transferred {
		t.Fatal("construction failure consumed ownership")
	}
	id := memsys.Identity{Kernel: 1, Warp: 0, Transaction: 99}
	checks := []struct {
		name       string
		add, clear func()
	}{
		{"completion", func() { k.runner.hierarchy.complete = []memsys.Identity{id} }, func() { k.runner.hierarchy.complete = nil }},
		{"cancel", func() { k.runner.hierarchy.cancelledData[id] = true }, func() { delete(k.runner.hierarchy.cancelledData, id) }},
		{"transport", func() { k.runner.hierarchy.dataTokens[id] = model.Token{} }, func() { delete(k.runner.hierarchy.dataTokens, id) }},
		{"fault", func() { k.failed = fmt.Errorf("execution fault") }, func() { k.failed = nil }},
		{"flush", func() { k.runner.cacheFlush = &cacheFlush{} }, func() { k.runner.cacheFlush = nil }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.add()
			if _, err := k.NextLaunch(launch); err == nil {
				t.Fatal("accepted unsafe transfer")
			}
			check.clear()
		})
	}
	// A rejected descriptor/tail does not destroy the existing flush path.
	if done, err := k.FlushCaches(2000); err != nil || !done {
		t.Fatal(done, err)
	}
	if _, err := k.NextLaunch(launch); err != nil {
		t.Fatal(err)
	}
}

// Internal device launches may retain dirty global bytes while each new CTA
// gets a fresh local owner. Native host launches additionally require D/I flush.
func TestDeviceMemoryDirtyAndLocal(t *testing.T) {
	k, launch := lifecycleKernel(t)
	launch.LocalMemorySize = 64
	ram := k.backing
	put := func(base uint32, words []uint32) {
		for i, w := range words {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], w)
			if err := ram.Write(base+uint32(i)*4, b[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The RTL I/O aperture is noncacheable; dirty-line data belongs above it.
	put(0x100, []uint32{0x02a00113, 0x000100b7, 0x0020a023, 0xcdf020f3, 0x0020a023, 0x0000000b})
	// CSR local base; local load; global load; stop.
	put(0x180, []uint32{0xcdf020f3, 0x0000a183, 0x000100b7, 0x0000a203, 0x0000000b})
	var err error
	k, err = NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	finishLifecycle(t, k)
	var b [4]byte
	if err := ram.Read(0x10000, b[:]); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(b[:]) != 0 {
		t.Fatal("dirty store prematurely visible")
	}
	launch.StartupPC = 0x180
	next, err := k.NextLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	finishLifecycle(t, next)
	snapshot, err := next.runner.owners[0].Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	local, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	global, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	if local[0] != 0 || global[0] != 42 {
		t.Fatal("local owner or dirty global state lost", local, global)
	}
	if done, err := next.FlushCaches(2000); err != nil || !done {
		t.Fatal(done, err)
	}
	if err := ram.Read(0x10000, b[:]); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(b[:]) != 42 {
		t.Fatal("flush lost previous launch store")
	}
}
