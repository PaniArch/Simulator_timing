package runner

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/model"
)

func rtlProbeKernel(t *testing.T, words []uint32, block [3]uint32, local uint32) (*Kernel, device.LaunchState) {
	t.Helper()
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	for i, word := range words {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := ram.Write(0x100+uint32(i)*4, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, GridDimensions: [3]uint32{1, 1, 1}, BlockDimensions: block, BlockSize: block[0] * block[1] * block[2], ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: local}
	k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	return k, launch
}

func TestRTLCTATailPersistsAcrossLaunchAndCapacityChange(t *testing.T) {
	// VX_cta_dispatch: tail_r resets only on reset; base_tail wraps against
	// the current usable_slots_r. A context change resets only init ownership.
	k, launch := rtlProbeKernel(t, []uint32{0x13, 0x13, 0x13, 0x13, 0xb}, [3]uint32{1, 1, 1}, 0)
	for i, local := range []uint32{0, 0, 8192, 0, 0, 16384} {
		if i != 0 {
			launch.LocalMemorySize = local
			launch.AlignedLocalMemorySize = 0 // recompute for the next launch
			var err error
			k, err = k.NextLaunch(launch)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := k.Run(2000, nil); err != nil {
			t.Fatal(err)
		}
		if !k.Status().Complete {
			t.Fatal("launch did not complete")
		}
		want := []int{0, 1, 0, 1, 2, 0}[i]
		found := false
		for _, e := range k.TakeEvents() {
			if e.Kind == "admitted" {
				found = true
				if e.Slot != want {
					t.Fatalf("launch %d slot=%d want=%d", i, e.Slot, want)
				}
			}
		}
		if !found {
			t.Fatal("no admission")
		}
		if done, err := k.FlushCaches(2000); err != nil || !done {
			t.Fatal(done, err)
		}
	}
}

func TestRTLResponseHandshakeHasNoRunnerHoldingSlot(t *testing.T) {
	// Same-bank four-lane loads compete with integer commits. Derived from
	// the RTLSim local_hit_stride0 counterexample; the complete array is one
	// CTA so this test cannot hide a response behind residency transitions.
	words := []uint32{0x02000293, 0xcdf02e73, 0x02a00313, 0x006e2023,
		0x000e2303, 0x000e2383, 0x00030333, 0xfff28293, 0xfe0298e3, 0xb}
	k, _ := rtlProbeKernel(t, words, [3]uint32{4, 4, 1}, 256)
	var delivered, blocked int
	if err := k.Run(10000, func(r MultiRecord) {
		fire := r.Report.MemoryResponse.Valid && r.Report.MemoryResponseReady
		if r.Memory.MemoryDelivered != fire {
			t.Fatalf("cycle %d memory dequeued=%v LSU fire=%v", r.Cycle, r.Memory.MemoryDelivered, fire)
		}
		if fire {
			delivered++
		}
		if r.Report.MemoryResponse.Valid && !r.Report.MemoryResponseReady {
			blocked++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || delivered == 0 || blocked == 0 {
		t.Fatal("missing pressure coverage", k.Status(), delivered, blocked)
	}
}

func TestRTLFetchBackpressureRetainsCacheResponse(t *testing.T) {
	// Repeated WAW-constrained serial divides fill the frontend. VX_fetch's
	// direct response-ready wire must retain the original cache/tag ownership.
	words := []uint32{0x02000093, 0x00200113}
	for i := 0; i < 24; i++ {
		words = append(words, 0x0220c1b3)
	}
	words = append(words, 0xb)
	k, _ := rtlProbeKernel(t, words, [3]uint32{4, 4, 1}, 0)
	blocked := 0
	if err := k.Run(15000, func(r MultiRecord) {
		if r.Memory.Fetch.Valid && !r.Report.FetchResponseReady {
			blocked++
			if _, ok := k.runner.hierarchy.fetchTokens[r.Memory.Fetch.Response.Identity]; !ok {
				t.Fatalf("cycle %d: cache response popped before fetch handshake", r.Cycle)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || blocked == 0 {
		t.Fatal("missing fetch backpressure coverage", k.Status(), blocked)
	}
}

func TestRTLCTASlotReleaseDoesNotWaitForPendingReceipt(t *testing.T) {
	k, launch := rtlProbeKernel(t, []uint32{0x13, 0x13, 0x13, 0x13, 0xb}, [3]uint32{1, 1, 1}, 16384)
	launch.GridDimensions = [3]uint32{8, 1, 1}
	var err error
	k, err = NewKernel(launch, k.backing, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	tmc := make(map[uint32]uint64)
	admissions := 0
	if err := k.Run(4000, func(r MultiRecord) {
		for _, f := range r.Report.Wakeups {
			if f.Kind == model.FeedbackTMC && f.UpdateMask && f.Mask == 0 {
				b := r.TokenBindings[f.Token.ID]
				if !b.Valid {
					t.Fatal("lost retirement identity")
				}
				tmc[b.CTA] = r.Cycle
			}
		}
		for _, e := range k.TakeEvents() {
			if e.Kind == "reclaimed" && e.Cycle != tmc[e.CTA]+3 {
				t.Fatalf("reclaim %+v after TMC %d", e, tmc[e.CTA])
			}
			if e.Kind == "admitted" {
				admissions++
				if e.CTA != 0 && e.Cycle != tmc[e.CTA-1]+4 {
					t.Fatalf("admission waited past RTL table write %+v", e)
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || admissions != 8 || len(k.tokenBindings) != 0 {
		t.Fatal("lost tail/slot progress", k.Status(), admissions, len(k.tokenBindings))
	}
}

func TestRTLIOApertureReachesNoncachedPath(t *testing.T) {
	// Two loads to the same I/O word: no D-cache hit is legal, but the RTL
	// coalescer still groups each two-lane pair into one 8-byte request.
	k, _ := rtlProbeKernel(t, []uint32{0x40000093, 0x0000a183, 0x0000a203, 0xb}, [3]uint32{4, 1, 1}, 0)
	k.runner.options.TraceMemory = true
	if err := k.backing.Write(0x400, []byte{42, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a  uint32
		io bool
	}{{0, false}, {63, false}, {64, true}, {0xffff, true}, {0x10000, false}, {0xffff0000, false}} {
		if got := k.runner.hierarchy.system.IsIO(tc.a); got != tc.io {
			t.Fatalf("I/O boundary %#x: %v", tc.a, got)
		}
	}
	requests := 0
	if err := k.Run(4000, func(r MultiRecord) {
		for _, x := range r.Memory.Transfers {
			if x.Boundary == "data-cache" {
				requests++
				if !x.Request.NonCacheable || x.Request.Address != 0x400 {
					t.Fatal("lost RTL is_addr_io", x)
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || requests != 4 {
		t.Fatal("wrong I/O grouping/completion", k.Status(), requests)
	}
}
