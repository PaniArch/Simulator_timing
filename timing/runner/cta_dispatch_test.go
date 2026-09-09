package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/runner"
)

func TestCTADispatchWhileOtherWarpsRun(t *testing.T) {
	owners, ram := multiSetup(t)
	initial := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: 0, Lifecycle: state.WarpInactive}
	for l := uint8(0); l < 4; l++ {
		initial.Lanes = append(initial.Lanes, state.LaneInitial{ID: l})
	}
	var err error
	owners[0], err = state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	// csrr x5,mscratch; addi x6,x0,9; tmc zero
	for i, word := range []uint32{0x340022f3, 0x00900313, 0x0000000b} {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err = ram.Write(0x500+uint32(i)*4, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 40}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(12, nil); err != nil {
		t.Fatal(err)
	}
	if !r.WarpQuiescent(0) || r.Completed() {
		t.Fatal("initial slot not available alongside work")
	}
	cycle := r.Cycle()
	if err = r.DispatchWarp(0, 0x500, 0xabc, 3, true); err != nil {
		t.Fatal(err)
	}
	if r.Cycle() != cycle {
		t.Fatal("dispatch invented a pipeline edge")
	}
	if err = r.DispatchWarp(0, 0x500, 0, 3, true); err == nil {
		t.Fatal("active slot redispatched")
	}
	if err = r.Run(1000, nil); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || !r.WarpQuiescent(0) {
		t.Fatal("dispatch did not drain")
	}
	snapshot, err := owners[0].Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for reg, want := range map[uint8]uint32{5: 0xabc, 6: 9} {
		got, err := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: reg})
		if err != nil {
			t.Fatal(err)
		}
		if got != (isa.LaneValues{want, want, 0, 0}) {
			t.Fatalf("x%d=%v", reg, got)
		}
	}
}
