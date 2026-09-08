package effects_test

import (
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func TestWarpSyncWaitsBeforeExecution(t *testing.T) {
	word := uint32(0)
	for _, entry := range isa.Catalog() {
		if entry.Name == "wsync" {
			word = entry.Example
		}
	}
	if word == 0 {
		t.Fatal("missing wsync")
	}
	init := initial()
	owner, _ := state.NewWarp(init)
	calls := 0
	adapter, _ := effects.New(owner, 9, func(e isa.InstructionEffects) error {
		if len(e.WarpDrains) != 1 || e.WarpDrains[0].Wait || !e.WarpDrains[0].ReleaseAfterDrain {
			t.Fatal("bad drain group", e)
		}
		calls++
		return nil
	})
	token := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
	if err := adapter.Begin(token); err != nil {
		t.Fatal(err)
	}
	pipeline, err := model.NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	input := model.Signal{Valid: true, Token: token}
	fetch := model.Response{}
	held, done := false, false
	for cycle := uint64(0); cycle < 120; cycle++ {
		context := state.ReadContext{PendingPriorWork: cycle < 45}
		p, err := pipeline.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: adapter.ControlAllowed(context)})
		if err != nil {
			t.Fatal(err)
		}
		if err = model.CommitEdge(p.Transition); err != nil {
			t.Fatal(err)
		}
		if err = adapter.Observe(cycle, p.Report, context); err != nil {
			t.Fatal(cycle, err)
		}
		if cycle < 45 {
			if p.Report.Executed[2].Valid || p.Report.Control.Valid || calls != 0 || snap(t, owner).PC() != init.PC {
				t.Fatal("drain control executed early")
			}
			if cycle == 44 {
				for _, r := range p.Report.Resources {
					if r.Occupancy != 0 {
						held = true
					}
				}
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
		if p.Report.PendingRelease.Valid && pipeline.Idle(true) {
			if err = adapter.Finish(); err != nil {
				t.Fatal(err)
			}
			done = true
			break
		}
	}
	if !done || !held || calls != 1 || snap(t, owner).PC() != init.PC+4 {
		t.Fatal("drain incomplete", done, held, calls)
	}
}
