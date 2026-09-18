package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func TestBarrierExternalReleaseAndDrain(t *testing.T) {
	owners, ram := multiSetup(t)
	wait := customWord(t, "bar.wait", 0, 0)
	for w := range owners {
		for n, word := range []uint32{0x0000a183, wait, 0x00700393, customWord(t, "tmc", 0, 0)} {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	forwarded := 0
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 40, External: func(e isa.InstructionEffects) error {
		for _, b := range e.Barriers {
			if !b.ReleaseByCoordinator || !b.Wait {
				t.Fatal(b)
			}
			forwarded++
		}
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var tokens [4]model.Token
	var pending [4]int
	var loadResponse, barExecute [4]uint64
	if err = r.Run(2000, func(rec runner.MultiRecord) {
		// VX_mem_scheduler ibuf_pop is final response acceptance, not WB.
		if p := rec.Report.MemoryResponse; p.Valid && rec.Report.MemoryResponseReady {
			loadResponse[p.Warp] = rec.Cycle
		}
		if p := rec.Report.Executed[2]; p.Valid && p.Token.Word == wait {
			barExecute[p.Token.Warp] = rec.Cycle
		}
		if p := rec.Report.Control; p.Valid && p.Token.Word == wait {
			tokens[p.Token.Warp] = p.Token
		}
		for w, s := range rec.Warps {
			pending[w] = s.Pending
		}
	}); err != nil {
		t.Fatal(err)
	}
	if r.Completed() || forwarded != 4 || r.InFlight() != 0 {
		t.Fatal("blocked is not completed", forwarded, r.InFlight())
	}
	for w, tok := range tokens {
		if tok.ID == 0 || pending[w] != 1 || loadResponse[w] == 0 || barExecute[w] <= loadResponse[w] {
			t.Fatal("missing wait/drain", w, pending, loadResponse, barExecute)
		}
		wrong := tok
		wrong.Epoch++
		if err = r.Release(wrong); err == nil {
			t.Fatal("stale coordinator wake accepted")
		}
		if err = r.Release(tok); err != nil {
			t.Fatal(err)
		}
		if err = r.Release(tok); err == nil {
			t.Fatal("duplicate coordinator wake queued")
		}
	}
	releaseCycle := r.Cycle()
	if err = r.Run(10000, func(rec runner.MultiRecord) {
		if rec.Cycle == releaseCycle {
			if len(rec.Report.Wakeups) != 4 || rec.Report.InstructionAccepted {
				t.Fatal("release bypassed old-edge scheduler")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("barrier release failed to resume")
	}
	for w, o := range owners {
		s, _ := o.Snapshot()
		v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
		if s.ActiveMask() != 0 || v != (isa.LaneValues{7, 7, 7, 7}) {
			t.Fatal(w, v)
		}
		if err = r.Release(tokens[w]); err == nil {
			t.Fatal("late duplicate wake accepted")
		}
	}
}

func TestBarrierReleaseDoesNotWaitForExternalStoreTail(t *testing.T) {
	owners, ram := multiSetup(t)
	wait := customWord(t, "bar.wait", 0, 0)
	for w, owner := range owners {
		if err := owner.WriteRegister(isa.Register{File: isa.Integer, Index: 3}, 15, isa.LaneValues{0x42, 0x42, 0x42, 0x42}); err != nil {
			t.Fatal(err)
		}
		for n, word := range []uint32{0x0030a023, wait, 0x00700393, customWord(t, "tmc", 0, 0)} {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+n*4), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls := 0
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 150, Ready: func(cycle uint64) bool { return cycle%5 != 0 }, External: func(e isa.InstructionEffects) error { calls += len(e.Barriers); return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	var accepted, barrier, finished [4]uint64
	stoppedWithTail := false
	if err := r.Run(400, func(rec runner.MultiRecord) {
		if p := rec.Report.MemoryRequest; p.Valid && rec.Report.MemoryAccepted {
			accepted[p.Token.Warp] = rec.Cycle
		}
		if p := rec.Report.Executed[2]; p.Valid && p.Token.Word == wait {
			barrier[p.Token.Warp] = rec.Cycle
		}
		if p := rec.Report.Control; p.Valid && p.Token.Word == wait {
			if err := r.Release(p.Token); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range rec.Finished {
			if p.PC%0x100 == 0 {
				finished[p.Warp] = rec.Cycle
			}
		}
		for _, f := range rec.Report.Wakeups {
			if f.Kind == model.FeedbackTMC && finished[f.Token.Warp] == 0 {
				stoppedWithTail = true
				if r.Completed() {
					t.Fatal("completed before external visibility")
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || calls != 4 || !stoppedWithTail {
		t.Fatal("barrier/store scenario incomplete", calls, stoppedWithTail)
	}
	if done, err := r.MakeVisible(10000); err != nil || !done {
		t.Fatal("store visibility", done, err)
	}
	for w, owner := range owners {
		if accepted[w] == 0 || barrier[w] <= accepted[w] || finished[w] <= barrier[w] {
			t.Fatal("BAR incorrectly waited for service tail", accepted, barrier, finished)
		}
		s, _ := owner.Snapshot()
		v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
		if v != (isa.LaneValues{7, 7, 7, 7}) {
			t.Fatal("barrier release failed to resume work", w, v)
		}
		var data [4]byte
		if err := ram.Read(uint32(0x10800+w*0x100), data[:]); err != nil || binary.LittleEndian.Uint32(data[:]) != 0x42 {
			t.Fatal("store service did not finish", data, err)
		}
	}
}
