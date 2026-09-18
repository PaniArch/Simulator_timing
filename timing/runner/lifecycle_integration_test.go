package runner

import (
	"encoding/binary"
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

// Exercise the identity chain while an early Warp is reused, its old CTA still
// executes, and other members have store tails. Repeat after real D/I flush
// with different output addresses on the very same memory hierarchy.
func TestLifecycleReuseTraceAcrossFlush(t *testing.T) {
	seed, launch := incrementalKernel(t)
	k, err := NewKernel(launch, seed.backing, Options{Backend: "std", PeriodPS: 1, TraceMemory: true,
		Ready: func(c uint64) bool { return c%3 == 0 }})
	if err != nil {
		t.Fatal(err)
	}
	system, clock, deviceID := k.runner.hierarchy.system, k.runner.clock, k.deviceID
	var oldLocal memsys.Identity
	for run := uint64(1); run <= 2; run++ {
		dispatch := map[[2]uint64]KernelEvent{}
		type wire struct {
			id   memsys.Identity
			port int
		}
		accepted := map[wire]memsys.Transfer{}
		var commits uint64
		var overlap, tail, buffered, local bool
		before := k.Counters()
		err = k.Run(6000, func(r MultiRecord) {
			if r.DeviceID != deviceID || r.LaunchID != run {
				t.Fatal("wrong trace namespace", r.DeviceID, r.LaunchID)
			}
			for _, e := range k.TakeEvents() {
				if e.Kind == "warp-dispatched" {
					dispatch[[2]uint64{uint64(e.Warp), e.WarpGeneration}] = e
					if e.CTA == 1 && e.Rank == 0 {
						for _, c := range k.resident {
							if c != nil && c.Launch.ID == 0 {
								overlap = !k.runner.WarpQuiescent(0) && e.Warp != 0 && e.WarpGeneration == 2
							}
						}
					}
				}
			}
			for _, c := range k.Status().Resident {
				tail = tail || c.StoppedWarps != 0 && c.MemoryPending
			}
			check := func(id memsys.Identity) {
				if id.Warp >= 4 {
					t.Fatal("invalid physical Warp", id)
				}
				b := r.TokenBindings[id.Token]
				e, ok := dispatch[[2]uint64{uint64(id.Warp), id.WarpGeneration}]
				if id.Kernel != run || !b.Valid || !ok || b.CTA != e.CTA || b.Rank != e.Rank ||
					b.CTAGeneration != e.Generation || b.WarpGeneration != id.WarpGeneration || uint64(b.Slot) != id.CTA {
					t.Fatal("old or unbound memory identity", id, b, e)
				}
			}
			for _, f := range r.Memory.Transfers {
				check(f.Request.Identity)
				key := wire{f.Request.Identity, f.Port}
				switch f.Boundary {
				case "global-adapter":
					check(f.Parent)
					// Stores allocate no coalescer response slot; their wire
					// transaction still uniquely links adapter and Cache accepts.
					if _, exists := accepted[key]; exists || (!f.Request.Write && f.Batch.Generation == 0) {
						t.Fatal("duplicate/missing batch", f)
					}
					accepted[key] = f
				case "data-cache":
					a, ok := accepted[key]
					if !ok || a.Request != f.Request {
						t.Fatal("lost buffered fragment", a, f)
					}
					delete(accepted, key)
					buffered = buffered || f.Port == 0
				case "local-memory":
					check(f.Parent)
					local = true
					if run == 1 {
						oldLocal = f.Parent
					}
				}
			}
			for _, id := range r.Memory.Complete {
				check(id)
			}
			for _, s := range r.Memory.Stores {
				check(s.Identity)
			}
			if r.Report.PendingRelease.Valid {
				commits++
			}
			if run == 2 && local {
				if _, err := k.runner.hierarchy.localOwner(oldLocal); err == nil {
					t.Fatal("old launch entered new LMEM")
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if !k.Status().Complete || !k.Status().MemoryDrained || !overlap || !tail || !buffered || !local || len(accepted) != 0 || len(dispatch) != 8 {
			t.Fatal("missing combined lifecycle coverage", k.Status(), overlap, tail, buffered, local, len(accepted), len(dispatch))
		}
		if k.Counters().Instret != before.Instret+commits {
			t.Fatal("lost EOP accounting across reuse")
		}
		if done, err := k.FlushCaches(4000); err != nil || !done {
			t.Fatal(done, err)
		}
		// Earlier launch output must remain intact while the successor uses a
		// different destination. TLS is initialized once per physical Warp/launch.
		for prior := uint64(1); prior <= run; prior++ {
			base := uint32(0x800 + (prior-1)*0x400)
			for i := uint32(0); i < 32; i++ {
				for address, want := range map[uint32]uint32{base + i*4: i, base + 512 + i*4: 1} {
					var b [4]byte
					if err := k.backing.Read(address, b[:]); err != nil {
						t.Fatal(err)
					}
					if binary.LittleEndian.Uint32(b[:]) != want {
						t.Fatal("cross-launch output/TLS pollution", address, b, want)
					}
				}
			}
		}
		if run == 1 {
			previous, counters, cycle := k, k.Counters(), k.Status().Cycle
			launch.ParameterAddress += 0x400
			k, err = k.NextLaunch(launch)
			if err != nil {
				t.Fatal(err)
			}
			if k.runner.hierarchy.system != system || k.runner.clock != clock || k.Status().StartCycle != cycle || k.Counters() != counters {
				t.Fatal("device state reset on successor")
			}
			if err := previous.Run(1, nil); err == nil {
				t.Fatal("old owner advanced successor")
			}
		}
	}
}

// Cancellation is a lower-level recovery operation, not a native Device reset.
// An accepted child must drain before its Kernel can transfer the hierarchy.
func TestLifecycleCancelledPortTailThenNextLaunch(t *testing.T) {
	k, launch := lifecycleKernel(t)
	put := func(address, word uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := k.backing.Write(address, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	for i, word := range []uint32{0x40000093, 0x0000a183, 0xb} {
		put(0x100+uint32(i)*4, word)
	}
	put(0x400, 42)
	var tok model.Token
	for n := 0; n < 2000 && tok.ID == 0; n++ {
		if err := k.Run(1, nil); err != nil {
			t.Fatal(err)
		}
		if k.runner.hierarchy.system.DCachePort0Occupancy() != 0 {
			for _, token := range k.runner.hierarchy.dataTokens {
				tok = token
			}
		}
	}
	if tok.ID == 0 {
		t.Fatal("missing real queued load")
	}
	scope := model.Cancellation{Warp: tok.Warp, Epoch: tok.Epoch, Through: tok.ID}
	// Cancel the already-issued younger TMC as well; Restart must continue to
	// reject a retained control receipt rather than bypass its ownership gate.
	for _, resource := range k.runner.core.Resources() {
		for _, item := range resource.Residents {
			if item.Token.Warp == tok.Warp {
				scope.Through = max(scope.Through, item.Token.ID)
			}
		}
	}
	for _, item := range k.runner.effects.Pending() {
		if item.Token.Warp == tok.Warp {
			scope.Through = max(scope.Through, item.Token.ID)
		}
	}
	if err := k.runner.Cancel(scope); err != nil {
		t.Fatal(err)
	}
	if k.runner.WarpQuiescent(tok.Warp) || k.runner.hierarchy.drained() {
		t.Fatal("cancelled tail released")
	}
	if _, err := k.NextLaunch(launch); err == nil {
		t.Fatal("live cancellation transferred")
	}
	if err := k.runner.Restart(tok.Warp, model.WarpContext{Active: true, PC: 0x108, Mask: 1, Epoch: tok.Epoch}); err != nil {
		t.Fatal(err)
	}
	if err := k.Run(2000, func(r MultiRecord) {
		if r.Report.Writeback.Valid && scope.Matches(r.Report.Writeback.Token) {
			t.Fatal("cancelled load wrote back")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || !k.Status().MemoryDrained {
		t.Fatal(k.Status())
	}
	snapshot, err := k.runner.owners[0].Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if v[0] != 0 {
		t.Fatal("late response polluted recovery", v)
	}
	if done, err := k.FlushCaches(2000); err != nil || !done {
		t.Fatal(done, err)
	}
	put(0x400, 99)
	system := k.runner.hierarchy.system
	k, err = k.NextLaunch(launch)
	if err != nil {
		t.Fatal(err)
	}
	finishLifecycle(t, k)
	snapshot, err = k.runner.owners[0].Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	v, _ = snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if v[0] != 99 || k.runner.hierarchy.system != system || len(k.runner.hierarchy.cancelledData) != 0 {
		t.Fatal("successor inherited old response", v)
	}
}
