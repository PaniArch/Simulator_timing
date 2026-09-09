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
	var loadRelease, barExecute [4]uint64
	if err = r.Run(200, func(rec runner.MultiRecord) {
		if p := rec.Report.PendingRelease; p.Valid && p.Token.PC%0x100 == 0 {
			loadRelease[p.Token.Warp] = rec.Cycle
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
		if tok.ID == 0 || pending[w] != 1 || barExecute[w] <= loadRelease[w] {
			t.Fatal("missing wait/drain", w, pending, loadRelease, barExecute)
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
	if err = r.Run(300, func(rec runner.MultiRecord) {
		if rec.Cycle == 200 {
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
