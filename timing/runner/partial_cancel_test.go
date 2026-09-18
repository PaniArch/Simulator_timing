package runner

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

type countedLocalWrites struct {
	warp.AtomicMemoryService
	writes *int
}

func (m countedLocalWrites) WriteBatch(addresses []uint32, sources [][]byte) error {
	*m.writes++
	return m.AtomicMemoryService.WriteBatch(addresses, sources)
}

// A cold global path fills while the local half of a younger store applies.
// Cancel at that split boundary and immediately restart: the old global offer
// must still finish, but its acceptance must not acknowledge the new LSU token.
func TestRealMemoryCancelPartiallyAppliedMixedStore(t *testing.T) {
	for _, flush := range []bool{false, true} {
		var want baselineResult
		var wantWrites map[memsys.Identity]int
		var wantLocal [4]byte
		for modeIndex, mode := range baselineChunks {
			for _, diag := range []bool{true, false} {
				t.Run(fmt.Sprintf("flush=%t/%s/diag=%t", flush, mode.name, diag), func(t *testing.T) {
					ram, _ := memory.New(0x12000)
					for i := 0; i < 32; i++ {
						var b [4]byte
						binary.LittleEndian.PutUint32(b[:], 0x0020a023) // sw x2,0(x1)
						if err := ram.Write(0x100+uint32(i*4), b[:]); err != nil {
							t.Fatal(err)
						}
					}
					var stop [4]byte
					binary.LittleEndian.PutUint32(stop[:], 0x0000000b)
					if err := ram.Write(0x180, stop[:]); err != nil {
						t.Fatal(err)
					}
					manager := core.NewCTAManager()
					_, err := manager.Admit(core.CTAConfig{ID: 0, WarpIDs: []uint8{0}, StartupPC: 0x100, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{1, 1, 1}, BlockSize: 4, WarpStep: [3]uint32{4, 0, 0}, LocalMemorySize: 64, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1})
					if err != nil {
						t.Fatal(err)
					}
					local, err := core.NewCTAMemory(manager, 0, ram)
					if err != nil {
						t.Fatal(err)
					}
					var owners [4]*state.WarpState
					for w := uint8(0); w < 4; w++ {
						init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, PC: 0x100, Lifecycle: state.WarpInactive}
						if w == 0 {
							init.Lifecycle = state.WarpRunning
							init.ActiveMask = 15
						}
						for l := uint8(0); l < 4; l++ {
							lane := state.LaneInitial{ID: l}
							// This test deliberately fills cached global requests,
							// not the RTL non-cacheable low-address I/O aperture.
							lane.GPR[1] = 0x10800 + uint32(l)*128
							if l == 0 {
								lane.GPR[1] = isa.FrozenLocalMemBase
							}
							lane.GPR[2] = uint32(100 + l)
							init.Lanes = append(init.Lanes, lane)
						}
						owners[w], err = state.NewWarp(init)
						if err != nil {
							t.Fatal(err)
						}
					}
					localWrites := make(map[memsys.Identity]*int)
					r, err := NewMulti(owners, ram, MultiOptions{Options: Options{Backend: "std", PeriodPS: 1}, DataMemory: [4]warp.MemoryService{local}, MemorySystem: &MemorySystemOptions{
						Config: memsys.Config{Latency: 500, AcceptsPerCycle: 1, MaxInflight: 1, ReturnsPerCycle: 1},
						Bind:   func(model.Token) memsys.Identity { return memsys.Identity{Kernel: 1, CTA: 0, WarpGeneration: 1} },
						LocalOwner: func(id memsys.Identity) (warp.AtomicMemoryService, error) {
							if localWrites[id] == nil {
								localWrites[id] = new(int)
							}
							return countedLocalWrites{local, localWrites[id]}, nil
						},
					}})
					if err != nil {
						t.Fatal(err)
					}
					var old memsys.Identity
					found := false
					for i := 0; i < 10000 && !found; i++ {
						if err = r.Run(1, nil); err != nil {
							t.Fatal(err)
						}
						m := r.hierarchy
						if m.data.Valid {
							for _, s := range m.stores {
								if s.identity == m.data.Request.Identity && s.result.Mask&1 != 0 {
									old = s.identity
									found = true
								}
							}
						}
					}
					if !found {
						t.Fatal("did not reach partially applied mixed store")
					}
					before, _ := owners[0].Snapshot()
					scope := model.Cancellation{Warp: 0, Epoch: 1, Through: 10000}
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
						t.Fatal("cancel changed canonical state")
					}
					if !r.hierarchy.data.Valid || r.hierarchy.data.Request.Identity != old {
						t.Fatal("cancel dropped held transport")
					}
					if !flush {
						if err = r.Restart(0, model.WarpContext{Active: true, PC: before.PC(), Mask: uint8(before.ActiveMask()), Epoch: 1}); err != nil {
							t.Fatal(err)
						}
					}
					observe := func(rec MultiRecord) {
						if rec.Report.Writeback.Valid && scope.Matches(rec.Report.Writeback.Token) {
							t.Fatal("cancelled store wrote architectural result")
						}
						if rec.Report.MemoryAccepted && scope.Matches(rec.Report.MemoryRequest.Token) {
							t.Fatal("cancelled input accepted by Core")
						}
					}
					if !diag {
						observe = nil
					}
					for call := 0; call < 30000 && !r.Completed(); call++ {
						if err = r.Run(mode.chunks[call%len(mode.chunks)], observe); err != nil {
							t.Fatal(err)
						}
					}
					if !r.Completed() || len(r.hierarchy.cancelledData) != 0 || len(r.hierarchy.dataTokens) != 0 || r.hierarchy.data.Valid {
						t.Fatal("cancel/restart leaked transport")
					}
					if localWrites[old] == nil || *localWrites[old] != 1 {
						t.Fatal("cancelled local subset was replayed or lost")
					}
					for id, count := range localWrites {
						if *count != 1 {
							t.Fatal("duplicate local application", id, *count)
						}
					}
					var data [4]byte
					if err = local.Read(isa.FrozenLocalMemBase, data[:]); err != nil {
						t.Fatal(err)
					}
					if binary.LittleEndian.Uint32(data[:]) != 100 {
						t.Fatal("local side effect lost")
					}
					// Compare after real writeback as well as execution: cancellation must
					// neither replay the applied local subset nor drop the held global tail.
					result := baselineResult{Cycle: r.Cycle(), Retired: r.Retired(), Counters: isa.CounterView{Cycle: r.core.Cycles(), Instret: r.core.Instret()}}
					result.Before, err = ram.ReadBytes(0, 0x12000)
					if err != nil {
						t.Fatal(err)
					}
					for w, owner := range owners {
						result.Owners[w], err = owner.Snapshot()
						if err != nil {
							t.Fatal(err)
						}
					}
					done := false
					for call := 0; call < 30000 && !done; call++ {
						done, err = r.FlushCaches(mode.chunks[call%len(mode.chunks)])
						if err != nil {
							t.Fatal(err)
						}
					}
					if !done {
						t.Fatal("cancelled store flush exceeded bound")
					}
					result.FlushCycle = r.Cycle()
					result.FlushCounters = isa.CounterView{Cycle: r.core.Cycles(), Instret: r.core.Instret()}
					result.After, err = ram.ReadBytes(0, 0x12000)
					if err != nil {
						t.Fatal(err)
					}
					writes := make(map[memsys.Identity]int)
					for id, n := range localWrites {
						writes[id] = *n
					}
					if modeIndex == 0 && diag {
						want, wantWrites, wantLocal = result, writes, data
					} else if !reflect.DeepEqual(result, want) || !reflect.DeepEqual(writes, wantWrites) || data != wantLocal {
						t.Fatal("cancelled mixed-store state, transport applications or flush counters differ")
					}
				})
			}
		}
	}
}
