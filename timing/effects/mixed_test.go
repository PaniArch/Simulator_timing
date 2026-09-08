package effects_test

import (
	"encoding/binary"
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

// Reuse the same owners, core, adapter and Akita clock across instructions.
// The single-active policy drains each instruction, including its store tail.
func TestMixedInstructionsPreserveOwnerContinuity(t *testing.T) {
	init, ram := memoryInitial(t, 0x002081b3)
	_, refRAM := memoryInitial(t, 0x002081b3)
	owner, _ := state.NewWarp(init)
	reference, _ := state.NewWarp(init)
	executor, _ := warp.NewWithMemory(reference, refRAM)
	adapter, _ := effects.New(owner, 9, nil)
	if err := adapter.BindMemory(ram); err != nil {
		t.Fatal(err)
	}
	pipeline, err := model.NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	clock, err := model.NewClock(1)
	if err != nil {
		t.Fatal(err)
	}
	words := []uint32{0x002081b3, 0x022081b3, 0x0020a023, 0x0000a183, 0x182081d3, 0x001091f3, 0x00000463}
	for index, word := range words {
		pc := snap(t, owner).PC()
		code := make([]byte, 4)
		binary.LittleEndian.PutUint32(code, word)
		if err = ram.Memory.Write(pc, code); err != nil {
			t.Fatal(err)
		}
		if err = refRAM.Memory.Write(pc, code); err != nil {
			t.Fatal(err)
		}
		result := executor.Step(state.ReadContext{})
		if result.Outcome != warp.OutcomeRetired {
			t.Fatal("reference", result)
		}
		token := model.Token{ID: uint64(index + 1), Epoch: 9, PC: pc, Mask: 15, Word: word}
		if err = adapter.Begin(token); err != nil {
			t.Fatal(err)
		}
		input := model.Signal{Valid: true, Token: token}
		fetch, response := model.Response{}, model.Response{}
		var request model.Token
		due := uint64(0)
		waiting := false
		pending := false
		done := false
		err = clock.Run(180, func(cycle uint64) (bool, error) {
			if waiting && cycle == due {
				var serviceErr error
				response, serviceErr = adapter.Service(cycle, request, 15)
				if serviceErr != nil {
					return false, serviceErr
				}
				waiting = false
			}
			context := state.ReadContext{}
			p, e := pipeline.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, MemoryResponse: response, FetchReady: true, MemoryReady: cycle%4 != 0, Eligible: true, ControlAllowed: adapter.ControlAllowed(context)})
			if e != nil {
				return false, e
			}
			if e = model.CommitEdge(p.Transition); e != nil {
				return false, e
			}
			if e = adapter.Observe(cycle, p.Report, context); e != nil {
				return false, e
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
				due = cycle + 17
				waiting = true
			}
			if p.Report.PendingRelease.Valid {
				pending = true
			}
			if pending && pipeline.Idle(!waiting && !response.Valid) {
				if e = adapter.Finish(); e != nil {
					return false, e
				}
				done = true
				return true, nil
			}
			return false, nil
		})
		if err != nil || !done {
			t.Fatal("mixed instruction", index, err, done)
		}
		if !reflect.DeepEqual(snap(t, owner), snap(t, reference)) {
			t.Fatal("owner continuity", index, snap(t, owner), snap(t, reference))
		}
		got, _ := ram.ReadBytes(0, 512)
		want, _ := refRAM.ReadBytes(0, 512)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("byte owner continuity", index)
		}
	}
}
