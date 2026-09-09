package runner

import (
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/model"
)

// Fault-injected interface fixture: normal wstall disallows these younger
// requests. Exercise defensive cleanup before invoking a visible byte service.
func TestRedirectCleanupCancelsYoungServiceIdentity(t *testing.T) {
	var owners [4]*state.WarpState
	for w := range owners {
		init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: uint8(w), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
		for l := uint8(0); l < 4; l++ {
			init.Lanes = append(init.Lanes, state.LaneInitial{ID: l})
		}
		var err error
		owners[w], err = state.NewWarp(init)
		if err != nil {
			t.Fatal(err)
		}
	}
	ram, _ := memory.New(1024)
	r, err := NewMulti(owners, ram, MultiOptions{Options: Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 10}})
	if err != nil {
		t.Fatal(err)
	}
	old := model.Token{Warp: 0, Epoch: 1, ID: 1, PC: 0x100, Mask: 15, Word: 0x00002083}
	young := old
	young.ID = 2
	young.PC = 0x104
	young.Word = 0x00102023
	other := young
	other.Warp = 1
	other.ID = 3
	for _, token := range []model.Token{old, young, other} {
		if err = r.effects.Begin(token); err != nil {
			t.Fatal(err)
		}
	}
	r.loads = []request{{old, 20}, {young, 0}, {other, 10}}
	r.fetch = []request{{model.Token{Warp: 0, Epoch: 1, ID: 4, PC: 0x108, Mask: 15}, 0}}
	before, _ := ram.ReadBytes(0, 1024)
	if err = r.cleanupFeedback([]model.SchedulerFeedback{{Token: old, Kind: model.FeedbackBranch, UpdatePC: true, PC: 0x180}}); err != nil {
		t.Fatal(err)
	}
	if len(r.loads) != 2 || r.loads[0].token.ID != 1 || r.loads[1].token.Warp != 1 || len(r.fetch) != 0 || r.InFlight() != 2 {
		t.Fatal("redirect isolation", r.loads, r.fetch, r.InFlight())
	}
	after, _ := ram.ReadBytes(0, 1024)
	for n, b := range after {
		if b != before[n] {
			t.Fatal("cancel reached byte owner")
		}
	}
	if err = r.effects.Begin(model.Token{Warp: 0, Epoch: 1, ID: 4, PC: 0x108, Mask: 15, Word: 0x00100093}); err == nil {
		t.Fatal("cancelled fetch revived")
	}
	found := false
	for _, e := range r.recoveryEvents {
		if e.Resource == "memory-service" && e.Token.ID == 2 && e.Kind == "cancel" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing service cancel event")
	}
}
