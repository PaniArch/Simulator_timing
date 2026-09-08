package effects_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

// The external callback commits through the existing canonical coordinator;
// timing owns neither barrier records nor a shadow CTA state.
func TestBarrierOwnerAtControlFeedback(t *testing.T) {
	ctas := core.NewCTAManager()
	_, err := ctas.Admit(core.CTAConfig{ID: 0, WarpIDs: []uint8{0}, StartupPC: 0x100, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{1, 1, 1}, BlockSize: 4, WarpStep: [3]uint32{4, 0, 0}, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1, IsFirstOfCluster: true})
	if err != nil {
		t.Fatal(err)
	}
	barriers, err := core.NewBarrierCoordinator(ctas)
	if err != nil {
		t.Fatal(err)
	}
	view, err := ctas.ViewForWarp(0)
	if err != nil {
		t.Fatal(err)
	}
	phases, err := barriers.PhaseView(0)
	if err != nil {
		t.Fatal(err)
	}
	context := state.ReadContext{CTA: view, BarrierPhases: &phases}
	word := uint32(0)
	for _, entry := range isa.Catalog() {
		if entry.Name == "bar.arrive" {
			word = entry.Example&^uint32(31<<15|31<<20) | 1<<15 | 2<<20
		}
	}
	if word == 0 {
		t.Fatal("missing barrier opcode")
	}
	init := initial()
	for lane := range init.Lanes {
		init.Lanes[lane].GPR[1] = 0
		init.Lanes[lane].GPR[2] = 1
	}
	owner, _ := state.NewWarp(init)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 0}
	original, err := barriers.Snapshot(key)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	adapter, err := effects.New(owner, 9, func(e isa.InstructionEffects) error {
		calls++
		if len(e.Barriers) != 1 || len(e.WarpDrains) != 1 || e.WarpDrains[0].Wait {
			t.Fatal("unexpected barrier group", e)
		}
		stage, err := barriers.Stage(e.Barriers[0])
		if err != nil {
			return err
		}
		if stage.Result().Block {
			t.Fatal("single participant unexpectedly blocked")
		}
		return stage.Commit()
	})
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := model.NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	token := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
	if err = adapter.Begin(token); err != nil {
		t.Fatal(err)
	}
	input := model.Signal{Valid: true, Token: token}
	fetch := model.Response{}
	done := false
	for cycle := uint64(0); cycle < 100; cycle++ {
		p, err := pipeline.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: fetch, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
		if err != nil {
			t.Fatal(err)
		}
		if err = model.CommitEdge(p.Transition); err != nil {
			t.Fatal(err)
		}
		if err = adapter.Observe(cycle, p.Report, context); err != nil {
			t.Fatal(cycle, err)
		}
		record, err := barriers.Snapshot(key)
		if err != nil {
			t.Fatal(err)
		}
		if p.Report.Control.Valid {
			if calls != 1 || reflect.DeepEqual(record, original) {
				t.Fatal("control failed to reach original owner")
			}
		}
		if calls == 0 && (!reflect.DeepEqual(record, original) || snap(t, owner).PC() != init.PC) {
			t.Fatal("control visible early")
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
	if !done || calls != 1 || snap(t, owner).PC() != init.PC+4 {
		t.Fatal("barrier delivery incomplete/repeated", done, calls)
	}
}
