package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/runner"
)

// A real outstanding load holds WCTL at the LSU gate while a younger ALU
// writeback and branch reach the canonical PC on earlier clock edges.
func TestMultiRunnerDelayedAsyncBarrier(t *testing.T) {
	owners, ram := multiSetup(t)
	words := []uint32{0x0000a183, customWord(t, "bar.arrive", 8, 0), 0x00700393, 0x00000463, 0x06300393, customWord(t, "tmc", 0, 0)}
	for w := range owners {
		for n, word := range words {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), b[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls := [4]int{}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 100, External: func(e isa.InstructionEffects) error {
		if len(e.Barriers) != 1 || len(e.WarpDrains) != 1 || e.WarpDrains[0].Wait {
			t.Fatal("unexpected arrival", e)
		}
		calls[e.Barriers[0].WarpID]++
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var alu, branch, arrival [4]uint64
	if err = r.Run(1000, func(rec runner.MultiRecord) {
		if s := rec.Report.Writeback; s.Valid && s.Token.PC%0x100 == 8 {
			alu[s.Token.Warp] = rec.Cycle
		}
		if s := rec.Report.Branch; s.Valid && s.Token.PC%0x100 == 12 {
			branch[s.Token.Warp] = rec.Cycle
		}
		if s := rec.Report.Control; s.Valid && s.Token.PC%0x100 == 4 {
			arrival[s.Token.Warp] = rec.Cycle
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || calls != [4]int{1, 1, 1, 1} {
		t.Fatal("arrival completion", calls, r.Completed())
	}
	for w, o := range owners {
		if alu[w] == 0 || branch[w] <= alu[w] || arrival[w] <= branch[w] {
			t.Fatal("missing cross-edge overtaking", alu, branch, arrival)
		}
		s, err := o.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
		if err != nil {
			t.Fatal(err)
		}
		if v != (isa.LaneValues{7, 7, 7, 7}) || s.PC() != uint32(0x100*(w+1)+24) {
			t.Fatal("redirect rolled back", s.PC(), v)
		}
	}
}
