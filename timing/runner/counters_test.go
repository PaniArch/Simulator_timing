package runner_test

import (
	"encoding/binary"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/runner"
)

func TestMultiRunnerArchitecturalCountersUseOldHardwareEvents(t *testing.T) {
	owners, ram := multiSetup(t)
	// Packed conversion produces four EOP commits but one effect receipt.
	words := []uint32{0x0820918b, 0xcc302473, 0xb00024f3, 0xb0202573, customWord(t, "tmc", 0, 0)}
	for w := range owners {
		for n, word := range words {
			var data [4]byte
			binary.LittleEndian.PutUint32(data[:], word)
			if err := ram.Write(uint32(0x100*(w+1)+4*n), data[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	options := runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 20}, Contexts: func() (contexts [4]state.ReadContext) {
		for w := range contexts {
			contexts[w].Counters = isa.CounterView{Cycle: 9999, Instret: 9999}
		}
		return
	}}
	r, err := runner.NewMulti(owners, ram, options)
	if err != nil {
		t.Fatal(err)
	}
	var expected [4][3]uint32
	var committed uint64
	if err := r.Run(10000, func(rec runner.MultiRecord) {
		if rec.Counters.Instret != committed {
			t.Fatal("instret not driven by registered EOP", rec.Counters, committed)
		}
		if rec.Report.PendingRelease.Valid {
			committed++
		}
		if p := rec.Report.Executed[2]; p.Valid {
			index := int(p.Token.PC%0x100)/4 - 1
			switch index {
			case 0:
				for w, context := range rec.Report.Scheduler.Warps {
					if context.Active {
						expected[p.Token.Warp][0] |= 1 << w
					}
				}
			case 1:
				expected[p.Token.Warp][1] = uint32(rec.Counters.Cycle)
			case 2:
				expected[p.Token.Warp][2] = uint32(rec.Counters.Instret)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("counter program incomplete")
	}
	if committed != 32 || r.Retired() != [4]uint64{5, 5, 5, 5} {
		t.Fatal("uop commits and macro receipts conflated", committed, r.Retired())
	}
	for w, owner := range owners {
		s, _ := owner.Snapshot()
		for n, want := range expected[w] {
			got, err := s.ReadRegister(isa.Register{File: isa.Integer, Index: uint8(8 + n)})
			if err != nil || want == 0 || got != (isa.LaneValues{want, want, want, want}) {
				t.Fatal("CSR differs from old-edge hardware state", w, n, got, want, err)
			}
		}
	}
}
