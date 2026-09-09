package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func TestMultiRunnerTrapAndReturnThroughScheduledCore(t *testing.T) {
	owners, ram := multiSetup(t)
	for w := range owners {
		for n, word := range []uint32{0x60000093, 0x30509073, 0x00000073, 0x00700393, customWord(t, "tmc", 0, 0)} {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	for n, word := range []uint32{0x341022f3, 0x00428293, 0x34129073, 0x30200073} {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], word)
		if err := ram.Write(0x600+uint32(4*n), data[:]); err != nil {
			t.Fatal(err)
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 10}})
	if err != nil {
		t.Fatal(err)
	}
	var enters, returns [4]int
	if err := r.Run(500, func(rec runner.MultiRecord) {
		for _, f := range rec.Report.Wakeups {
			if f.Kind != model.FeedbackBranch {
				continue
			}
			switch f.Token.Word {
			case 0x00000073:
				enters[f.Token.Warp]++
				if f.PC != 0x600 {
					t.Fatal("trap vector", f)
				}
			case 0x30200073:
				returns[f.Token.Warp]++
				if f.PC != uint32(0x100*(int(f.Token.Warp)+1)+12) || !f.UpdateMask || f.Mask != 15 {
					t.Fatal("trap return", f)
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || enters != [4]int{1, 1, 1, 1} || returns != [4]int{1, 1, 1, 1} {
		t.Fatal("trap round trip incomplete", enters, returns)
	}
	for w, owner := range owners {
		s, _ := owner.Snapshot()
		v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
		if s.PC() != uint32(0x100*(w+1)+20) || v != (isa.LaneValues{7, 7, 7, 7}) {
			t.Fatal("return lost main continuation", w, s.PC(), v)
		}
	}
}
