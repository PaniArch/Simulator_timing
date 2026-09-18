package runner

import (
	"encoding/binary"
	"testing"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

func incrementalKernel(t *testing.T) (*Kernel, device.LaunchState) {
	t.Helper()
	ram, _ := memory.New(8192)
	put := func(pc, word uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := ram.Write(pc, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	// Physical register x20 is TLS: one-time initialization followed by the
	// five-instruction reentry window. Reuse must preserve it, not rerun prologue.
	put(0xfc, 0x001a0a13)
	for i, w := range []uint32{0xce1022f3, 0x13, 0x000283e7, 0x13, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	// Rank zero runs a long dependency chain; other ranks return early.
	words := []uint32{0xcd1020f3, 0x00100113, 0x04009263}
	for i := 0; i < 16; i++ {
		words = append(words, 0x02214133)
	} // div x2,x2,x2
	// Branch above skips 16 divides to 0x24c.
	words = append(words,
		0xcd602173,             // block index x2
		0x00411113,             // block * 16
		0x00209093,             // rank * 4
		0x00110133,             // x2=block*16+rank*4
		0xcc0021f3,             // lane
		0x00310133,             // logical thread value
		0x00219193,             // lane byte offset
		0xcdf02273,             // LMEM address
		0x00209293,             // rank byte offset (rank already *4)
		0x00520233, 0x00320233, // local per-thread address
		0x00222023, 0x00022303, // local store/load
		0x34002273, 0x00211293, 0x00520233, // global + value*4
		0x00622023, // output
		0x21422023, // TLS at output+512
		0x00038067)
	for i, w := range words {
		put(0x200+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: 0xfc, KernelEntryPC: 0x200, ParameterAddress: 0x800, GridDimensions: [3]uint32{2, 1, 1}, BlockDimensions: [3]uint32{4, 4, 1}, BlockSize: 16, WarpStep: [3]uint32{0, 1, 0}, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
	k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	return k, launch
}

func TestIncrementalDispatchEdges(t *testing.T) {
	k, _ := incrementalKernel(t)
	if err := k.Run(8, nil); err != nil {
		t.Fatal(err)
	}
	events := k.TakeEvents()
	var fired []KernelEvent
	for _, e := range events {
		if e.Kind == "warp-dispatched" {
			fired = append(fired, e)
		}
	}
	if len(fired) != 4 {
		t.Fatal(events)
	}
	for rank, e := range fired {
		if e.Cycle != uint64(3+rank) || e.Rank != uint32(rank) || e.Warp != uint8(rank) || e.LaunchID != 1 || e.WarpGeneration != 1 {
			t.Fatal(e)
		}
	}
	// Next CTA accepted with no free Warp: LMEM/context reservation is independent
	// of physical availability, and cannot be mistaken for completion.
	if len(k.Status().Resident) != 2 || k.resident[1].Dispatched != 0 || k.Status().Complete {
		t.Fatal(k.Status())
	}
	// After the KMU start register edge, seven edges have admission/DISPATCH or
	// registered Warp busy. The initial admission and selection count before
	// any Warp is active, directly from VX_cta_dispatch.busy.
	if k.Counters().Cycle != 7 || k.Counters().Instret != 0 {
		t.Fatal("CTA dispatch missing from hardware busy counter", k.Counters())
	}
}

func TestIncrementalEarlyReuseAndNextLaunch(t *testing.T) {
	k, launch := incrementalKernel(t)
	for run := 0; run < 2; run++ {
		overlap, tail := false, false
		var events []KernelEvent
		if err := k.Run(6000, func(r MultiRecord) {
			for _, c := range k.Status().Resident {
				tail = tail || c.StoppedWarps != 0 && c.MemoryPending
			}
			for _, e := range k.TakeEvents() {
				events = append(events, e)
				if e.Kind == "warp-dispatched" && e.CTA == 1 && e.Rank == 0 {
					for _, c := range k.resident {
						if c != nil && c.Launch.ID == 0 {
							overlap = !k.runner.WarpQuiescent(0)
						}
					}
					if e.Warp == 0 || e.WarpGeneration != 2 {
						t.Fatalf("did not reuse early slot: %+v", e)
					}
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !overlap || !tail || !k.Status().Complete {
			t.Fatal("overlap/tail/completion", overlap, tail, k.Status(), events)
		}
		if done, err := k.FlushCaches(4000); err != nil || !done {
			t.Fatal(done, err)
		}
		for i := uint32(0); i < 32; i++ {
			for address, want := range map[uint32]uint32{0x800 + i*4: i, 0xa00 + i*4: 1} {
				var b [4]byte
				if err := k.backing.Read(address, b[:]); err != nil {
					t.Fatal(err)
				}
				if got := binary.LittleEndian.Uint32(b[:]); got != want {
					t.Fatalf("launch %d addr %#x: %d want %d", run, address, got, want)
				}
			}
		}
		if run == 0 {
			var err error
			k, err = k.NextLaunch(launch)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestIncrementalReuseRetainsIdentityTails(t *testing.T) {
	// Inject software-only tails at a boundary with quiescent initial owners.
	// RTL availability is active_warps, not software receipt drain. Selection
	// must preserve each tail, without allowing it to change priority.
	for _, kind := range []string{"completion", "cancel", "cancel-fetch", "accepted", "held-fetch", "held-data", "transport", "parked", "fault"} {
		t.Run(kind, func(t *testing.T) {
			k, _ := incrementalKernel(t)
			if err := k.Run(2, nil); err != nil {
				t.Fatal(err)
			} // KMU start then CTA accepted, no selection yet
			id := memsys.Identity{Kernel: 1, CTA: 0, Warp: 0, WarpGeneration: 1, Transaction: 99, Token: 99}
			m := k.runner.hierarchy
			switch kind {
			case "completion":
				m.complete = append(m.complete, id)
			case "cancel":
				m.cancelledData[id] = true
			case "cancel-fetch":
				m.cancelledFetch[id] = true
			case "accepted":
				m.accepted[id] = true
			case "held-fetch":
				m.fetch = memsys.WordOffer{Valid: true, Request: memsys.WordRequest{Identity: id}}
			case "held-data":
				m.data = memsys.SIMDOffer{Valid: true, Request: memsys.SIMDRequest{Identity: id}}
			case "transport":
				m.dataTokens[id] = model.Token{ID: 99, Warp: 0}
			case "parked":
				k.runner.parked[0] = true
			case "fault":
				k.runner.failed = true
			}
			if err := k.residency(); err != nil {
				t.Fatal(err)
			}
			if kind == "fault" {
				if k.dispatch.selected != nil {
					t.Fatal("failed runner reused")
				}
				return
			}
			first := uint8(0)
			if kind == "parked" {
				first = 1
			}
			if k.dispatch.selected == nil || k.dispatch.selected.WarpID != first {
				t.Fatal("software tail changed RTL priority", k.dispatch)
			}
			if kind != "parked" && !m.warpPending(0) {
				t.Fatal("dispatch discarded old transport ownership")
			}
			m.complete = nil
			delete(m.cancelledData, id)
			delete(m.cancelledFetch, id)
			delete(m.accepted, id)
			m.fetch = memsys.WordOffer{}
			m.data = memsys.SIMDOffer{}
			delete(m.dataTokens, id)
			k.runner.parked[0] = false
			if !k.runner.WarpQuiescent(0) {
				t.Fatal("drained slot unavailable")
			}
			if err := k.residency(); err != nil {
				t.Fatal(err)
			}
			if k.dispatch.selected == nil || k.dispatch.selected.WarpID != 1-first {
				t.Fatal("drained slot not reused")
			}
			old := k.runner.hierarchy.identity(model.Token{Warp: 0, ID: 1}, 1)
			old.WarpGeneration--
			if _, err := k.runner.hierarchy.localOwner(old); err == nil {
				t.Fatal("old generation routed to new LMEM")
			}
			view, err := k.memory.ViewForWarp(0)
			rank := uint32(first)
			if err != nil || view.Rank != rank || view.Size != 4 || view.ThreadCoordinates[1] != (isa.LaneValues{rank, rank, rank, rank}) {
				t.Fatal(view, err)
			}
		})
	}
}

func TestIncrementalRetirementRegisteredEdges(t *testing.T) {
	k, _ := lifecycleKernel(t)
	var tmc, retired uint64
	var sawTMC, sawRetired bool
	if err := k.Run(2000, func(r MultiRecord) {
		for _, f := range r.Report.Wakeups {
			if f.Kind == model.FeedbackTMC && f.UpdateMask && f.Mask == 0 {
				tmc, sawTMC = r.Cycle, true
			}
		}
		for _, c := range k.Status().Resident {
			if !sawRetired && c.RetiredRanks != 0 {
				retired, sawRetired = r.Cycle, true
			}
			if c.Reclaimable && (!sawTMC || r.Cycle < tmc+2) {
				t.Fatal("CTA bypassed retirement RAM pipeline", r.Cycle, tmc)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !sawTMC || !sawRetired || retired != tmc+2 || !k.Status().Complete {
		t.Fatal(tmc, retired, k.Status())
	}
}

func TestIncrementalFaultRequiresNewDevice(t *testing.T) {
	seed, launch := lifecycleKernel(t)
	var word [4]byte
	binary.LittleEndian.PutUint32(word[:], 0xb)
	if err := seed.backing.Write(0x10800, word[:]); err != nil {
		t.Fatal(err)
	}
	launch.StartupPC, launch.KernelEntryPC = 0x10800, 0x10800
	owner := &recoveryReadFault{AtomicMemoryService: seed.backing.(*memory.Memory), fail: true}
	failed, err := NewKernel(launch, owner, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = failed.Run(2000, nil); err == nil {
		t.Fatal("missing delayed refill fault")
	}
	if _, err = failed.NextLaunch(launch); err == nil {
		t.Fatal("failed residency transferred")
	}
	failed.TakeEvents()
	cycle := failed.Status().Cycle
	if err = failed.Run(2000, nil); err == nil || failed.Status().Cycle != cycle || len(failed.TakeEvents()) != 0 {
		t.Fatal("failed Kernel dispatched more work")
	}
	// A new device is an explicit reset, not a transfer of faulted memory state.
	recovered, err := NewKernel(launch, owner, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	finishLifecycle(t, recovered)
	if recovered.runner.hierarchy.system == failed.runner.hierarchy.system {
		t.Fatal("faulted hierarchy reused")
	}
}
