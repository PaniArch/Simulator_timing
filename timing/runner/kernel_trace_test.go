package runner

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

// Same PC repeats across CTA reuse and launches. Packed load has four hardware
// commits, one macro receipt, and four uop memory transactions.
func TestKernelTracePackedReuseAndCounters(t *testing.T) {
	ram, _ := memory.New(8192)
	put := func(a, w uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], w)
		if err := ram.Write(a, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	// x1 = input address; packed byte conversion; TMC. Reentry ends at TMC.
	for i, w := range []uint32{0x40000093, 0x0820918b, 0x13, 0x13, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	put(0x400, 0x01020304)
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, GridDimensions: [3]uint32{6, 1, 1}, BlockDimensions: [3]uint32{3, 1, 1}, BlockSize: 3, ClusterDimensions: [3]uint32{1, 1, 1}}
	k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1, TraceMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	deviceID := k.deviceID
	var previousCycles, previousCommits uint64
	for run := 0; run < 2; run++ {
		dispatch := map[[2]uint64]KernelEvent{}
		uops := map[[3]uint64]bool{}
		adapter := map[[3]uint64]memsys.Transfer{}
		var macros, commits, cacheFragments uint64
		if err := k.Run(4000, func(r MultiRecord) {
			if r.DeviceID != deviceID || r.LaunchID != uint64(run+1) {
				t.Fatal("namespace", r.DeviceID, r.LaunchID)
			}
			for _, e := range k.TakeEvents() {
				if e.Kind == "warp-dispatched" {
					dispatch[[2]uint64{uint64(e.Warp), e.WarpGeneration}] = e
				}
			}
			check := func(w uint8) WarpBinding {
				b := r.Bindings[w]
				e, ok := dispatch[[2]uint64{uint64(w), b.WarpGeneration}]
				if !b.Valid || !ok || b.CTA != e.CTA || b.Rank != e.Rank || b.CTAGeneration != e.Generation || uint32(e.Slot) != b.Slot {
					t.Fatalf("unbound token: %+v %+v", b, e)
				}
				return b
			}
			for _, e := range r.Events {
				check(e.Token.Warp)
			}
			for _, tok := range r.Finished {
				check(tok.Warp)
				macros++
			}
			if r.Report.PendingRelease.Valid {
				commits++
				check(r.Report.PendingRelease.Token.Warp)
			}
			for _, f := range r.Memory.Transfers {
				b := check(uint8(f.Request.Identity.Warp))
				if f.Request.Identity.Kernel != r.LaunchID || f.Request.Identity.WarpGeneration != b.WarpGeneration || f.Request.Identity.CTA != uint64(b.Slot) {
					t.Fatal("fragment residency", f, b)
				}
				key := [3]uint64{f.Request.Identity.Transaction, uint64(f.Port), uint64(f.Request.Identity.Warp)}
				switch f.Boundary {
				case "global-adapter":
					if f.Batch.Generation == 0 || f.Parent.Token != f.Request.Identity.Token {
						t.Fatal("lost batch", f)
					}
					adapter[key] = f
					uops[[3]uint64{uint64(b.CTA), f.Parent.Token, f.Parent.Subrequest}] = true
				case "data-cache":
					a, ok := adapter[key]
					if !ok || a.Request != f.Request {
						t.Fatal("buffer broke identity", f, a)
					}
					cacheFragments++
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !k.Status().Complete || macros != 30 || commits != 48 || len(uops) != 24 || cacheFragments == 0 || len(dispatch) != 6 {
			t.Fatal("trace/count coverage", k.Status(), macros, commits, len(uops), cacheFragments, len(dispatch))
		}
		c := k.Counters()
		if c.Instret != previousCommits+commits || c.Cycle <= previousCycles {
			t.Fatal("counter continuation", c)
		}
		previousCycles, previousCommits = c.Cycle, c.Instret
		if done, err := k.FlushCaches(2000); err != nil || !done {
			t.Fatal(done, err)
		}
		if f := k.Counters(); f.Instret != c.Instret || f.Cycle < c.Cycle || f.Cycle > c.Cycle+1 {
			t.Fatal("flush counter tail", c, f)
		}
		c = k.Counters()
		previousCycles = c.Cycle
		if run == 0 {
			k, err = k.NextLaunch(launch)
			if err != nil {
				t.Fatal(err)
			}
			if k.Counters() != c {
				t.Fatal("launch reset counters")
			}
		}
	}
	independent, _ := lifecycleKernel(t)
	if independent.deviceID == deviceID || independent.Counters().Instret != 0 || independent.Counters().Cycle != 0 {
		t.Fatal("device isolation")
	}
}
