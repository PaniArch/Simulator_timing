package state_test

import (
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
)

func inactiveLaunchWarp(t *testing.T, id uint8, pc uint32) *state.WarpState {
	t.Helper()
	initial := baseInitial()
	initial.WarpID = id
	initial.PC = pc
	initial.ActiveMask = 0
	initial.Lifecycle = state.WarpInactive
	initial.SavedThreadMask = 0
	initial.Divergence = state.DivergenceInitial{}
	for lane := range initial.Lanes {
		initial.Lanes[lane].ID = uint8(lane)
	}
	owner, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestWarpLaunchStageFirstUseReuseMScratchAndAtomicity(t *testing.T) {
	first := inactiveLaunchWarp(t, 0, 0x880)
	reused := inactiveLaunchWarp(t, 1, 0x420)
	beforeFirst, _ := first.Snapshot()
	beforeReused, _ := reused.Snapshot()
	stage, err := state.StageWarpLaunch([]state.WarpLaunchTarget{
		{WarpID: 0, Owner: first, StartupPC: 0x100, ActiveMask: isa.AllLanes, ParameterAddress: 0xfeed0000, FirstUse: true},
		{WarpID: 1, Owner: reused, StartupPC: 0x100, ActiveMask: 0b0011, ParameterAddress: 0xfeed0000, FirstUse: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := first.Snapshot(); after != beforeFirst {
		t.Fatal("staging changed first-use owner")
	}
	if after, _ := reused.Snapshot(); after != beforeReused {
		t.Fatal("staging changed reused owner")
	}
	results := stage.Results()
	if len(results) != 2 || results[0].PC != 0x100 || !results[0].FirstUse ||
		results[1].PC != 0x420-state.FrozenCTAReentryBytes || results[1].FirstUse {
		t.Fatalf("wrong launch results: %+v", results)
	}
	results[0].PC = 0
	if stage.Results()[0].PC != 0x100 {
		t.Fatal("launch result aliases the stage")
	}
	if err := stage.Commit(); err != nil {
		t.Fatal(err)
	}
	for id, owner := range []*state.WarpState{first, reused} {
		snapshot, snapshotErr := owner.Snapshot()
		if snapshotErr != nil || snapshot.PC() != []uint32{0x100, 0x420 - state.FrozenCTAReentryBytes}[id] ||
			snapshot.ActiveMask() != []isa.LaneMask{isa.AllLanes, 0b0011}[id] ||
			snapshot.Lifecycle() != state.WarpRunning || snapshot.TrapCSRs().MScratch != 0xfeed0000 {
			t.Fatalf("warp %d launch snapshot=%+v err=%v", id, snapshot, snapshotErr)
		}
	}
	if err := stage.Commit(); err == nil {
		t.Fatal("launch stage committed twice")
	}
}

func TestWarpLaunchStageRejectsInvalidAndStaleTargetsWithoutPartialMutation(t *testing.T) {
	left := inactiveLaunchWarp(t, 0, 0x200)
	right := inactiveLaunchWarp(t, 1, 0x300)
	beforeLeft, _ := left.Snapshot()
	beforeRight, _ := right.Snapshot()
	if _, err := state.StageWarpLaunch([]state.WarpLaunchTarget{
		{WarpID: 0, Owner: left, StartupPC: 0x102, ActiveMask: 1, FirstUse: true},
	}); err == nil {
		t.Fatal("misaligned startup was accepted")
	}
	if after, _ := left.Snapshot(); after != beforeLeft {
		t.Fatal("invalid staging changed owner")
	}

	stage, err := state.StageWarpLaunch([]state.WarpLaunchTarget{
		{WarpID: 0, Owner: left, StartupPC: 0x100, ActiveMask: 1, FirstUse: true},
		{WarpID: 1, Owner: right, StartupPC: 0x100, ActiveMask: 1, FirstUse: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := right.SetPC(0x304); err != nil {
		t.Fatal(err)
	}
	if err := stage.Commit(); err == nil {
		t.Fatal("stale launch committed")
	}
	if after, _ := left.Snapshot(); after != beforeLeft {
		t.Fatal("stale multi-warp commit partially changed left owner")
	}
	if after, _ := right.Snapshot(); after.PC() != 0x304 || after.ActiveMask() != beforeRight.ActiveMask() {
		t.Fatal("stale multi-warp commit overwrote intervening right owner change")
	}
}
