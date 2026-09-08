package effects_test

import (
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func TestSpawnAtRegisteredControl(t *testing.T) {
	for _, stale := range []bool{false, true} {
		word := uint32(0)
		for _, entry := range isa.Catalog() {
			if entry.Name == "wspawn" {
				word = entry.Example&^uint32(31<<15|31<<20) | 1<<15 | 2<<20
			}
		}
		init := initial()
		init.TrapCSRs.MScratch = 0x900
		for lane := range init.Lanes {
			init.Lanes[lane].GPR[1] = 2
			init.Lanes[lane].GPR[2] = 0x300
		}
		source, _ := state.NewWarp(init)
		targetInit := initial()
		targetInit.WarpID = 1
		targetInit.Lifecycle = state.WarpInactive
		targetInit.ActiveMask = 0
		target, _ := state.NewWarp(targetInit)
		expected := snap(t, target)
		adapter, _ := effects.New(source, 9, nil)
		if err := adapter.BindSpawn(3, nil); err == nil {
			t.Fatal("multiple active warps accepted")
		}
		if err := adapter.BindSpawn(1, []state.WarpSpawnTarget{{WarpID: 1, Owner: target, Expected: expected}}); err != nil {
			t.Fatal(err)
		}
		token := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
		if err := adapter.Begin(token); err != nil {
			t.Fatal(err)
		}
		if stale {
			if err := target.SetPC(0x400); err != nil {
				t.Fatal(err)
			}
			expected = snap(t, target)
		}
		pipeline, err := model.NewCore("std")
		if err != nil {
			t.Fatal(err)
		}
		input := model.Signal{Valid: true, Token: token}
		fetch := model.Response{}
		delivered, done := false, false
		for cycle := uint64(0); cycle < 100; cycle++ {
			p, err := pipeline.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = model.CommitEdge(p.Transition); err != nil {
				t.Fatal(err)
			}
			err = adapter.Observe(cycle, p.Report, state.ReadContext{})
			if p.Report.Control.Valid && stale {
				if err == nil || snap(t, target) != expected || snap(t, source).PC() != init.PC {
					t.Fatal("stale spawn partially committed", err)
				}
				if adapter.Finish() == nil {
					t.Fatal("failed spawn retired")
				}
				done = true
				break
			}
			if err != nil {
				t.Fatal(cycle, err)
			}
			if p.Report.Control.Valid {
				if delivered {
					t.Fatal("repeated control")
				}
				delivered = true
				got := snap(t, target)
				if got.PC() != 0x300 || got.ActiveMask() != 1 || got.TrapCSRs().MScratch != 0x900 || snap(t, source).PC() != init.PC+4 {
					t.Fatal("atomic spawn missing", got)
				}
			}
			if !delivered && (snap(t, target) != expected || snap(t, source).PC() != init.PC) {
				t.Fatal("spawn visible before feedback")
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
		if !done || !stale && !delivered {
			t.Fatal("spawn incomplete")
		}
	}
}
