package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func customWord(t *testing.T, name string, rd, rs uint32) uint32 {
	t.Helper()
	for _, e := range isa.Catalog() {
		if e.Name == name {
			return e.Example&^uint32(31<<7|31<<15|31<<20) | rd<<7 | rs<<15
		}
	}
	t.Fatal(name)
	return 0
}

func TestMultiRunnerSIMTAndBranchRecovery(t *testing.T) {
	owners, ram := multiSetup(t)
	refs, referenceRAM := multiSetup(t)
	words := []uint32{0x0000a183, 0x00f00113, customWord(t, "tmc", 0, 2), customWord(t, "split", 8, 6), 0x00148493, customWord(t, "join", 0, 8), 0x00000463, 0x06300513, customWord(t, "tmc", 0, 0)}
	for w := range owners {
		for _, owner := range []*state.WarpState{owners[w], refs[w]} {
			if err := owner.WriteRegister(isa.Register{File: isa.Integer, Index: 6}, 15, isa.LaneValues{1, 1, 0, 0}); err != nil {
				t.Fatal(err)
			}
		}
		for n, word := range words {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			addr := uint32(0x100*(w+1) + 4*n)
			if err := ram.Write(addr, data[:]); err != nil {
				t.Fatal(err)
			}
			if err := referenceRAM.Write(addr, data[:]); err != nil {
				t.Fatal(err)
			}
		}
		f, err := warp.NewWithMemory(refs[w], referenceRAM)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 20; n++ {
			s, _ := refs[w].Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			if result := f.Step(state.ReadContext{}); result.Outcome != warp.OutcomeRetired {
				t.Fatal(result)
			}
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 3, MemoryCycles: 70, Ready: func(c uint64) bool { return c%5 != 0 }}})
	if err != nil {
		t.Fatal(err)
	}
	var trace []runner.MultiRecord
	observe := func(record runner.MultiRecord) { trace = append(trace, record) }
	if err = r.Run(40, observe); err != nil {
		t.Fatal(err)
	}
	if r.Completed() {
		t.Fatal("budget exhaustion mistaken for completion")
	}
	if err = r.Run(800, observe); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("control stream failed to finish", r.Retired())
	}
	joinEdges := map[uint64]uint64{}
	counts := [4]map[model.FeedbackKind]int{}
	for w := range owners {
		got, _ := owners[w].Snapshot()
		want, _ := refs[w].Snapshot()
		if got != want {
			t.Fatalf("warp%d differs\ngot %+v\nwant %+v", w, got, want)
		}
		counts[w] = map[model.FeedbackKind]int{}
	}
	for n, rec := range trace {
		if sig := rec.Report.Control; sig.Valid {
			d, err := isa.Decode(sig.Token.Word)
			if err != nil {
				t.Fatal(err)
			}
			if d.Control == isa.ControlJoin {
				joinEdges[sig.Token.ID] = rec.Cycle
			}
		}
		for _, f := range rec.Report.Wakeups {
			counts[f.Token.Warp][f.Kind]++
			if f.Kind == model.FeedbackJoin {
				if edge, ok := joinEdges[f.Token.ID]; !ok || rec.Cycle != edge+1 {
					t.Fatal("JOIN register offset", rec.Cycle, f)
				}
			} else if f.Kind == model.FeedbackBranch {
				if !rec.Report.Branch.Valid || rec.Report.Branch.Token.ID != f.Token.ID {
					t.Fatal("branch feedback edge shifted")
				}
			} else if !rec.Report.Control.Valid || rec.Report.Control.Token.ID != f.Token.ID {
				t.Fatal("SIMT feedback edge shifted")
			}
			if rec.Report.Offered.Valid && rec.Report.Offered.Token.Warp == f.Token.Warp {
				t.Fatal("same-edge feedback bypass")
			}
			if n+1 < len(trace) {
				next := trace[n+1].Report.Scheduler.Warps[f.Token.Warp]
				if next.Stalled || f.UpdatePC && next.PC != f.PC || f.UpdateMask && next.Mask != f.Mask {
					t.Fatal("feedback not visible next edge", rec.Cycle, f, next)
				}
			}
		}
		if rec.Report.Decoded.Valid && rec.Report.Decoded.Token.PC%0x100 == 0x1c {
			t.Fatal("wrong path was fetched")
		}
	}
	for w, c := range counts {
		if c[model.FeedbackTMC] != 2 || c[model.FeedbackSplit] != 1 || c[model.FeedbackJoin] != 2 || c[model.FeedbackBranch] != 1 {
			t.Fatal(w, c)
		}
	}
}

func TestMultiRunnerWSYNCDrainsOwnWarp(t *testing.T) {
	owners, ram := multiSetup(t)
	words := []uint32{0x0000a183, customWord(t, "wsync", 0, 0), 0x00700393, customWord(t, "tmc", 0, 0)}
	for w := range owners {
		for n, word := range words {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	callbacks := [4]int{}
	options := runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 10, External: func(e isa.InstructionEffects) error {
		for _, d := range e.WarpDrains {
			if d.Wait || !d.ReleaseAfterDrain {
				t.Fatal("unready WSYNC forwarded", d)
			}
			callbacks[d.WarpID]++
		}
		return nil
	}}}
	r, err := runner.NewMulti(owners, ram, options)
	if err != nil {
		t.Fatal(err)
	}
	var release, execute, finish [4]uint64
	if err = r.Run(500, func(rec runner.MultiRecord) {
		p := rec.Report.PendingRelease
		if p.Valid && p.Token.PC%0x100 == 0 {
			release[p.Token.Warp] = rec.Cycle
		}
		e := rec.Report.Executed[2]
		if e.Valid && e.Token.PC%0x100 == 4 {
			execute[e.Token.Warp] = rec.Cycle
		}
		for _, f := range rec.Report.Wakeups {
			if f.Kind == model.FeedbackTMC {
				finish[f.Token.Warp] = rec.Cycle
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || callbacks != [4]int{1, 1, 1, 1} {
		t.Fatal("WSYNC did not finish exactly once", callbacks)
	}
	for w := range owners {
		if release[w] == 0 || execute[w] != release[w]+1 {
			t.Fatal("drain boundary", w, release, execute)
		}
		s, _ := owners[w].Snapshot()
		v, err := s.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
		if err != nil {
			t.Fatal(err)
		}
		if v != (isa.LaneValues{7, 7, 7, 7}) {
			t.Fatal(v)
		}
		// The real bank/backend order leaves Warp 3 pending last. Earlier
		// Warps must execute WSYNC while that unrelated load still waits.
		if w != 3 && (release[w] >= release[3] || execute[w] >= release[3]) {
			t.Fatal("other Warp LSU failed to progress", release)
		}
		if finish[w] <= execute[w] {
			t.Fatal("control completed before execution", finish, execute)
		}
	}
}

// VX_wctl_unit WSYNC waits for pending almost-empty, not external store
// visibility. Kernel completion must nevertheless retain the service tail.
func TestMultiRunnerWSYNCDoesNotWaitForStoreTail(t *testing.T) {
	owners, ram := multiSetup(t)
	for w := range owners {
		for n, word := range []uint32{0x0030a023, customWord(t, "wsync", 0, 0), customWord(t, "tmc", 0, 0)} {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 150, External: func(isa.InstructionEffects) error { return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	var committed, sync, finished [4]uint64
	if err := r.Run(400, func(rec runner.MultiRecord) {
		if p := rec.Report.PendingRelease; p.Valid && p.Token.PC%0x100 == 0 {
			committed[p.Token.Warp] = rec.Cycle
		}
		if p := rec.Report.Executed[2]; p.Valid && p.Token.PC%0x100 == 4 {
			sync[p.Token.Warp] = rec.Cycle
		}
		for _, p := range rec.Finished {
			if p.PC%0x100 == 0 {
				finished[p.Warp] = rec.Cycle
			}
		}
		for w := range owners {
			if sync[w] != 0 && finished[w] == 0 && r.Completed() {
				t.Fatal("completed with store tail")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	for w := range owners {
		if committed[w] == 0 || sync[w] <= committed[w] || finished[w] <= sync[w] {
			t.Fatal("pending and service tail conflated", committed, sync, finished)
		}
	}
	if !r.Completed() {
		t.Fatal("service tails never completed")
	}
}
