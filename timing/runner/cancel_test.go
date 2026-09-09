package runner_test

import (
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func TestMultiRunnerCancelYoungerKeepsOldLoadAndOtherWarps(t *testing.T) {
	owners, ram := multiSetup(t)
	refs, referenceRAM := multiSetup(t)
	for w := 1; w < 4; w++ {
		f, err := warp.NewWithMemory(refs[w], referenceRAM)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 8; n++ {
			s, _ := refs[w].Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			if result := f.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
				t.Fatal(result)
			}
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 60}})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(15, nil); err != nil {
		t.Fatal(err)
	}
	scope := model.Cancellation{Warp: 0, Epoch: 1, After: 1, Through: 20}
	before, _ := owners[0].Snapshot()
	if err = r.Cancel(scope); err != nil {
		t.Fatal(err)
	}
	after, _ := owners[0].Snapshot()
	if before != after {
		t.Fatal("cancel changed canonical owner")
	}
	seenCancel, oldWB := false, false
	if err = r.Run(350, func(rec runner.MultiRecord) {
		for _, tok := range rec.Cancelled {
			if tok.Warp == 0 && tok.ID > 1 {
				seenCancel = true
			}
		}
		for _, resource := range rec.ResourcesAfter {
			for _, entry := range resource.Residents {
				if scope.Matches(entry.Token) {
					t.Fatal("cancelled resident survived", resource.ID, entry)
				}
			}
		}
		wb := rec.Report.Writeback
		if wb.Valid && scope.Matches(wb.Token) {
			t.Fatal("cancelled writeback")
		}
		if wb.Valid && wb.Token.Warp == 0 && wb.Token.ID == 1 {
			oldWB = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seenCancel || !oldWB || r.InFlight() != 0 || r.Completed() {
		t.Fatal("cancel/drain/blocked completion", seenCancel, oldWB, r.InFlight(), r.Completed())
	}
	s, _ := owners[0].Snapshot()
	x4, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	x3, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if x4 != (isa.LaneValues{}) || x3 != (isa.LaneValues{0x04030201, 0x04030201, 0x04030201, 0x04030201}) || s.PC() != 0x104 {
		t.Fatal("older load or cancelled add result", x3, x4, s.PC())
	}
	for w := 1; w < 4; w++ {
		got, _ := owners[w].Snapshot()
		want, _ := refs[w].Snapshot()
		if got != want {
			t.Fatal("other warp damaged", w)
		}
	}
}
