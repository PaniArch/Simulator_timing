package runner_test

import (
	"encoding/binary"
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func setup(t *testing.T) (*state.WarpState, *memory.Memory) {
	t.Helper()
	init := state.WarpInitial{Topology: state.FrozenTopology(), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
	for lane := uint8(0); lane < 4; lane++ {
		v := state.LaneInitial{ID: lane}
		v.GPR[1] = 64 + 8*uint32(lane)
		init.Lanes = append(init.Lanes, v)
	}
	owner, err := state.NewWarp(init)
	if err != nil {
		t.Fatal(err)
	}
	ram, err := memory.New(512)
	if err != nil {
		t.Fatal(err)
	}
	tmc := uint32(0)
	for _, entry := range isa.Catalog() {
		if entry.Name == "tmc" {
			tmc = entry.Example &^ uint32(31<<15)
		}
	}
	if tmc == 0 {
		t.Fatal("missing tmc")
	}
	words := []uint32{0x00900113, 0x022101b3, 0x0221c233, 0x0040a023, 0x0000a283, 0x00228463, 0x06300313, tmc}
	for i, word := range words {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err = ram.Write(0x100+uint32(i)*4, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	return owner, ram
}
func TestProgramAndDeterminism(t *testing.T) {
	reference, refRAM := setup(t)
	functional, _ := warp.NewWithMemory(reference, refRAM)
	for n := 0; n < 20; n++ {
		s, _ := reference.Snapshot()
		if s.ActiveMask() == 0 {
			break
		}
		result := functional.Step(state.ReadContext{})
		if result.Outcome != warp.OutcomeRetired {
			t.Fatal(result)
		}
	}
	want, _ := reference.Snapshot()
	wantBytes, _ := refRAM.ReadBytes(0, 512)
	var previous []runner.Record
	for run := 0; run < 4; run++ {
		owner, ram := setup(t)
		options := runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 3, MemoryCycles: 19}
		if run == 3 {
			options.MemoryCycles = 39
		}
		if run == 2 {
			options.Ready = func(cycle uint64) bool { return cycle >= 40 && cycle%3 != 0 }
		}
		r, err := runner.New(owner, ram, options)
		if err != nil {
			t.Fatal(err)
		}
		trace := []runner.Record{}
		observe := func(record runner.Record) { trace = append(trace, record) }
		if err = r.Run(10, observe); err != nil {
			t.Fatal(err)
		}
		if r.Completed() {
			t.Fatal("premature completion")
		}
		if err = r.Run(600, observe); err != nil {
			t.Fatal(err)
		}
		got, _ := owner.Snapshot()
		bytes, _ := ram.ReadBytes(0, 512)
		if !r.Completed() || r.Pending() || r.Retired() != 7 || got != want || !reflect.DeepEqual(bytes, wantBytes) {
			t.Fatal("program differs", r.Retired(), got, want)
		}
		if run == 0 {
			previous = trace
		} else if run == 1 && !reflect.DeepEqual(previous, trace) {
			t.Fatal("nondeterministic trace")
		}
		if run >= 2 && len(trace) <= len(previous) {
			t.Fatal("backpressure did not delay program")
		}
	}
}

func TestFlushCancelsStoreTail(t *testing.T) {
	owner, ram := setup(t)
	r, err := runner.New(owner, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 100})
	if err != nil {
		t.Fatal(err)
	}
	cancelled := false
	for n := 0; n < 150; n++ {
		var last runner.Record
		if err = r.Run(1, func(record runner.Record) { last = record }); err != nil {
			t.Fatal(err)
		}
		if last.Report.MemoryAccepted {
			before, _ := ram.ReadBytes(64, 28)
			if err = r.Flush(); err != nil {
				t.Fatal(err)
			}
			if r.Pending() {
				t.Fatal("flush left pending objects")
			}
			// Redirect at the canonical owner to termination, rather than replaying store.
			if err = owner.SetPC(0x11c); err != nil {
				t.Fatal(err)
			}
			if err = r.Run(250, nil); err != nil {
				t.Fatal(err)
			}
			after, _ := ram.ReadBytes(64, 28)
			if !r.Completed() || !reflect.DeepEqual(before, after) {
				t.Fatal("late cancelled store mutated bytes")
			}
			cancelled = true
			break
		}
	}
	if !cancelled {
		t.Fatal("never reached store request")
	}
}

func TestFlushAfterExecutionFaultAdvancesPastFailedEdge(t *testing.T) {
	owner, ram := setup(t)
	// Replace first instruction with a misaligned LW, faulting at execution.
	var code [4]byte
	binary.LittleEndian.PutUint32(code[:], 0x0010a283)
	if err := ram.Write(0x100, code[:]); err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(owner, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(100, nil); err == nil {
		t.Fatal("expected execution fault")
	}
	failedCycle := r.Cycle()
	if err = r.Flush(); err != nil {
		t.Fatal(err)
	}
	if r.Cycle() <= failedCycle {
		t.Fatal("failed edge was reused")
	}
	if err = owner.SetPC(0x11c); err != nil {
		t.Fatal(err)
	}
	if err = r.Run(100, nil); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("reset did not recover")
	}
}
