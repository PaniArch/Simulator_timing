package effects_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func TestReturnedMemoryPartialLoadsAndAppliedStores(t *testing.T) {
	for _, word := range []uint32{0x0000a183, 0x00008183, 0x0020a023} {
		init, ram := memoryInitial(t, word)
		_, referenceRAM := memoryInitial(t, word)
		owner, _ := state.NewWarp(init)
		reference, _ := state.NewWarp(init)
		executor, _ := warp.NewWithMemory(reference, referenceRAM)
		if executor.Step(state.ReadContext{}).Outcome != warp.OutcomeRetired {
			t.Fatal("functional reference")
		}
		pipeline, err := model.NewCore("std")
		if err != nil {
			t.Fatal(err)
		}
		a, _ := effects.New(owner, 9, nil)
		if err = a.BindMemory(ram); err != nil {
			t.Fatal(err)
		}
		token := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
		if err = a.Begin(token); err != nil {
			t.Fatal(err)
		}
		input := model.Signal{Valid: true, Token: token}
		fetch, response := model.Response{}, model.Response{}
		due := uint64(1000)
		var request model.Token
		parts := 0
		pending, done := false, false
		ram.failRead = true
		beforeBytes, _ := ram.Memory.ReadBytes(0, 512)
		for cycle := uint64(0); cycle < 150; cycle++ {
			if cycle == due || cycle == due+8 {
				mask := uint8(5)
				if parts == 1 {
					mask = 10
				}
				result := effects.MemoryResult{Mask: mask}
				requests, packed, err := a.MemoryRequests(request)
				if err != nil || len(packed) != 0 || len(requests) != 4 {
					t.Fatal("detached functional requests", err)
				}
				for _, r := range requests {
					if mask&(1<<r.Lane) != 0 {
						if err = ram.Memory.Read(r.AlignedAddress, result.Data[r.Lane][:]); err != nil {
							t.Fatal(err)
						}
					}
				}
				// Stores were applied by the memory system; this receipt intentionally
				// leaves this test's bound backing untouched to detect accidental replay.
				before := snap(t, owner)
				response, err = a.AcceptMemoryResult(cycle, request, result)
				if err != nil {
					t.Fatal(err)
				}
				parts++
				if word != 0x0020a023 && !reflect.DeepEqual(before, snap(t, owner)) {
					t.Fatal("returned bytes wrote registers before WB")
				}
				if ram.batches != 0 || ram.writes != 0 {
					t.Fatal("returned store repeated backing mutation")
				}
			}
			p, err := pipeline.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, MemoryResponse: response, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = model.CommitEdge(p.Transition); err != nil {
				t.Fatal(err)
			}
			if err = a.Observe(cycle, p.Report, state.ReadContext{}); err != nil {
				t.Fatal(err)
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
			if p.Report.MemoryAccepted {
				request = p.Report.MemoryRequest.Token
				due = cycle + 15
			}
			if p.Report.PendingRelease.Valid {
				pending = true
			}
			if pending && parts == 2 && pipeline.Idle(true) {
				if err = a.Finish(); err != nil {
					t.Fatal(err)
				}
				done = true
				break
			}
		}
		if !done || !reflect.DeepEqual(snap(t, owner), snap(t, reference)) {
			t.Fatal("returned completion architectural mismatch")
		}
		afterBytes, _ := ram.Memory.ReadBytes(0, 512)
		if !reflect.DeepEqual(beforeBytes, afterBytes) {
			t.Fatal("returned path mutated backing")
		}
	}
}
