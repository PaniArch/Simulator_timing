package runner

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/model"
)

// Stop with a real request in the registered port-0 queue, before its Cache
// acceptance. Cancellation must preserve both held SIMD offers and the queued
// child even if the parent handshake has not yet returned to the Core.
func TestDFlushQueuedRunnerCancellation(t *testing.T) {
	for name, instruction := range map[string]uint32{"load": 0x0000a183, "store": 0x0020a023, "inline-flush": 0x0000000f} {
		for _, flush := range []bool{false, true} {
			mode := "selective"
			if flush {
				mode = "epoch-flush"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				ram, _ := memory.New(4096)
				put := func(a, w uint32) {
					var b [4]byte
					binary.LittleEndian.PutUint32(b[:], w)
					if err := ram.Write(a, b[:]); err != nil {
						t.Fatal(err)
					}
				}
				put(0x100, instruction)
				put(0x104, 0x0000000b)
				put(0x800, 42)
				var owners [4]*state.WarpState
				for w := uint8(0); w < 4; w++ {
					init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, PC: 0x100, Lifecycle: state.WarpInactive}
					if w == 0 {
						init.ActiveMask = 1
						init.Lifecycle = state.WarpRunning
					}
					for l := uint8(0); l < 4; l++ {
						lane := state.LaneInitial{ID: l}
						lane.GPR[1] = 0x800
						lane.GPR[2] = 99
						init.Lanes = append(init.Lanes, lane)
					}
					var err error
					owners[w], err = state.NewWarp(init)
					if err != nil {
						t.Fatal(err)
					}
				}
				r, err := NewMulti(owners, ram, MultiOptions{Options: Options{Backend: "std", PeriodPS: 1}})
				if err != nil {
					t.Fatal(err)
				}
				var tok model.Token
				for i := 0; i < 2000 && tok.ID == 0; i++ {
					if err = r.Run(1, nil); err != nil {
						t.Fatal(err)
					}
					if r.hierarchy.system.DCachePort0Occupancy() != 0 {
						for _, token := range r.hierarchy.dataTokens {
							tok = token
						}
					}
				}
				if tok.ID == 0 {
					t.Fatal("never accepted port-0 input")
				}
				var oldIDFound bool
				for id, token := range r.hierarchy.dataTokens {
					if token == tok {
						oldIDFound = true
						if !r.hierarchy.system.HasResidency(id.Kernel, id.CTA) {
							t.Fatal("queued identity not resident")
						}
					}
				}
				if !oldIDFound || r.hierarchy.system.Drained() || r.Completed() {
					t.Fatal("released queued transport")
				}
				before, _ := owners[0].Snapshot()
				scope := model.Cancellation{Warp: 0, Epoch: tok.Epoch, Through: 10000}
				if flush {
					err = r.Flush()
				} else {
					err = r.Cancel(scope)
				}
				if err != nil {
					t.Fatal(err)
				}
				after, _ := owners[0].Snapshot()
				if before != after {
					t.Fatal("cancellation mutated canonical state")
				}
				if len(r.hierarchy.cancelledData) == 0 || r.hierarchy.system.Drained() {
					t.Fatal("cancel dropped buffered tail")
				}
				if !flush {
					if err = r.Restart(0, model.WarpContext{Active: true, PC: 0x104, Mask: 1, Epoch: tok.Epoch}); err != nil {
						t.Fatal(err)
					}
				}
				if err = r.Run(4000, func(rec MultiRecord) {
					if rec.Report.Writeback.Valid && scope.Matches(rec.Report.Writeback.Token) {
						t.Fatal("cancelled buffered word wrote back")
					}
				}); err != nil {
					t.Fatal(err)
				}
				if !r.Completed() || !r.hierarchy.system.Drained() || len(r.hierarchy.cancelledData) != 0 || len(r.hierarchy.dataTokens) != 0 {
					t.Fatalf("tail leaked or completed early: completed=%v drained=%v cancelled=%v tokens=%v data=%+v complete=%v", r.Completed(), r.hierarchy.system.Drained(), r.hierarchy.cancelledData, r.hierarchy.dataTokens, r.hierarchy.data, r.hierarchy.complete)
				}
				if done, err := r.MakeVisible(4000); err != nil || !done {
					t.Fatal("visibility", done, err)
				}
				var b [4]byte
				if err = ram.Read(0x800, b[:]); err != nil {
					t.Fatal(err)
				}
				want := uint32(42)
				if name == "store" {
					want = 99
				}
				if binary.LittleEndian.Uint32(b[:]) != want {
					t.Fatal("accepted store lost", b)
				}
				final, _ := owners[0].Snapshot()
				v, _ := final.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
				expected := uint32(0)
				if flush && name == "load" {
					expected = 42
				}
				if v[0] != expected {
					t.Fatal("late response changed restarted state", v)
				}
			})
		}
	}
}
