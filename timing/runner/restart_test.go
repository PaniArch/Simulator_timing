package runner_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func TestMultiRunnerRecoveryAndFlush(t *testing.T) {
	refs, referenceRAM := multiSetup(t)
	for _, owner := range refs {
		f, err := warp.NewWithMemory(owner, referenceRAM)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 8; n++ {
			s, _ := owner.Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			if result := f.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
				t.Fatal(result)
			}
		}
	}
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "selective-restart", true: "epoch-flush"}[flush], func(t *testing.T) {
			owners, ram := multiSetup(t)
			r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 60}})
			if err != nil {
				t.Fatal(err)
			}
			budget := uint64(15)
			if flush {
				budget = 5
			}
			if err = r.Run(budget, nil); err != nil {
				t.Fatal(err)
			}
			before := [4]state.WarpSnapshot{}
			for w, o := range owners {
				before[w], _ = o.Snapshot()
			}
			if flush {
				err = r.Flush()
			} else {
				err = r.Cancel(model.Cancellation{Warp: 0, Epoch: 1, After: 1, Through: 20})
				if err == nil {
					err = r.Restart(0, model.WarpContext{Active: true, PC: 0x104, Mask: 15, Epoch: 1})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Cycle() != budget {
				t.Fatal("recovery secretly advanced clock")
			}
			for w, o := range owners {
				after, _ := o.Snapshot()
				if after != before[w] {
					t.Fatal("recovery mutated canonical owner")
				}
			}
			sawCancel, sawRestart, oldWB := false, false, false
			if err = r.Run(800, func(rec runner.MultiRecord) {
				for _, e := range rec.Events {
					if e.Kind == "cancel" {
						sawCancel = true
						if e.Token.Epoch != 1 {
							t.Fatal("cancel lost old epoch")
						}
					}
					if e.Kind == "restart" {
						sawRestart = true
					}
				}
				if flush {
					for _, resource := range rec.ResourcesAfter {
						for _, resident := range resource.Residents {
							if resident.Token.Epoch != 2 {
								t.Fatal("old epoch resident after flush")
							}
						}
					}
				} else {
					wb := rec.Report.Writeback
					if wb.Valid && wb.Token.Warp == 0 && wb.Token.ID == 1 {
						oldWB = true
					}
					if rec.Report.Decoded.Valid && rec.Report.Decoded.Token.Warp == 0 && rec.Report.Decoded.Token.ID <= 20 {
						t.Fatal("restart reused tombstone identity")
					}
				}
				// Mutate detached event data: later results must be unaffected.
				if len(rec.Events) > 0 {
					rec.Events[0].Token.Mask = 255
				}
			}); err != nil {
				t.Fatal(err)
			}
			if !r.Completed() || !sawCancel || !flush && (!sawRestart || !oldWB) {
				t.Fatal("missing recovery completion/events", r.Completed(), sawCancel, sawRestart, oldWB)
			}
			for w, o := range owners {
				got, _ := o.Snapshot()
				want, _ := refs[w].Snapshot()
				if got != want {
					t.Fatalf("warp%d final state differs", w)
				}
			}
			got, _ := ram.ReadBytes(0, 4096)
			want, _ := referenceRAM.ReadBytes(0, 4096)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("recovery memory mismatch")
			}
		})
	}
}
