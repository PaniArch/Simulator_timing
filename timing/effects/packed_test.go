package effects_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func TestPackedServiceByteVisibility(t *testing.T)  { testPackedService(t, false) }
func TestPackedReturnedByteVisibility(t *testing.T) { testPackedService(t, true) }
func testPackedService(t *testing.T, returned bool) {
	for _, entry := range isa.Catalog() {
		if entry.Memory.Packed == 0 {
			continue
		}
		word := entry.Example&^uint32(31<<7|31<<15|31<<20) | 3<<7 | 1<<15 | 2<<20
		init, ram := memoryInitial(t, word)
		for lane := range init.Lanes {
			init.Lanes[lane].GPR[2] = uint32(entry.Memory.Bytes)
		}
		owner, _ := state.NewWarp(init)
		reference, _ := state.NewWarp(init)
		executor, _ := warp.NewWithMemory(reference, ram)
		if result := executor.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
			t.Fatal(result)
		}
		core, err := model.NewCore("std")
		if err != nil {
			t.Fatal(err)
		}
		adapter, _ := effects.New(owner, 9, nil)
		if err = adapter.BindMemory(ram); err != nil {
			t.Fatal(err)
		}
		token := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
		if err = adapter.Begin(token); err != nil {
			t.Fatal(err)
		}
		input := model.Signal{Valid: true, Token: token}
		fetch, response := model.Response{}, model.Response{}
		requests := map[uint8]model.Token{}
		pending, fragments := 0, 0
		finalWB := map[uint8]bool{}
		done := false
		for cycle := uint64(0); cycle < 220; cycle++ {
			// Same-rd packed uops are WAW serialized by Scoreboard. Return
			// disjoint lane fragments as each uop becomes service-visible.
			uop := uint8(fragments / 2)
			req, available := requests[uop]
			if cycle >= 70 && !response.Valid && fragments < int(entry.Memory.Packed)*2 && available {
				mask := uint8(5)
				if fragments%2 == 1 {
					mask = 10
				}
				before := snap(t, owner)
				if returned {
					result := effects.MemoryResult{Mask: mask}
					_, requests, e := adapter.MemoryRequests(req)
					if e != nil {
						t.Fatal(e)
					}
					for _, r := range requests {
						if mask&(1<<r.Lane) != 0 {
							if e := ram.Memory.Read(r.AlignedAddress, result.Data[r.Lane][:]); e != nil {
								t.Fatal(e)
							}
						}
					}
					ram.failRead = true // The adapter must never re-read this owner.
					response, err = adapter.AcceptMemoryResult(cycle, req, result)
				} else {
					response, err = adapter.Service(cycle, req, mask)
				}
				if err != nil {
					t.Fatal("service", cycle, err)
				}
				if !reflect.DeepEqual(before, snap(t, owner)) {
					t.Fatal("service changed owner before WB")
				}
				fragments++
			}
			p, err := core.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, MemoryResponse: response, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = model.CommitEdge(p.Transition); err != nil {
				t.Fatal(err)
			}
			before := snap(t, owner)
			if err = adapter.Observe(cycle, p.Report, state.ReadContext{}); err != nil {
				t.Fatal("observe", cycle, err)
			}
			after := snap(t, owner)
			old, next := reg(t, before, isa.Float, 3), reg(t, after, isa.Float, 3)
			for lane := 0; lane < 4; lane++ {
				allowed := uint32(0)
				if p.Report.Writeback.Valid && p.Report.Writeback.Token.Mask&(1<<lane) != 0 {
					allowed = 0xff
					if entry.Memory.Bytes == 2 {
						allowed = 0xffff
					}
					allowed <<= 8 * entry.Memory.Bytes * p.Report.Writeback.Token.Uop
				}
				if (old[lane]^next[lane])&^allowed != 0 {
					t.Fatal("WB overwrote another byte or lane")
				}
			}
			if p.Report.InstructionAccepted {
				input = model.Signal{}
			}
			if fetch.Valid && p.Report.FetchResponseReady {
				fetch = model.Response{}
			}
			if p.Report.FetchAccepted {
				f := p.Report.FetchRequest.Token
				fetch = model.Response{Valid: true, ID: f.ID, Epoch: f.Epoch, Warp: f.Warp, Mask: f.Mask, Word: word}
			}
			if response.Valid && p.Report.MemoryResponseReady {
				response = model.Response{}
			}
			if p.Report.Writeback.Valid && p.Report.Writeback.Token.End {
				finalWB[p.Report.Writeback.Token.Uop] = true
			}
			if p.Report.MemoryAccepted {
				r := p.Report.MemoryRequest.Token
				if r.Uop > 0 && !finalWB[r.Uop-1] {
					t.Fatal("packed request bypassed preceding final WB", r.Uop)
				}
				requests[r.Uop] = r
			}
			if p.Report.PendingRelease.Valid {
				pending++
			}
			if pending == int(entry.Memory.Packed) && core.Idle(true) {
				if err = adapter.Finish(); err != nil {
					t.Fatal(err)
				}
				done = true
				break
			}
		}
		if !done || !reflect.DeepEqual(snap(t, owner), snap(t, reference)) {
			t.Fatal("packed final mismatch", entry.Name, done, snap(t, owner), snap(t, reference))
		}
	}
}
