package effects_test

import (
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func TestTrapCSRFeedbackSamplingAndPriority(t *testing.T) {
	for _, test := range []struct {
		name           string
		csr, trap      uint32
		oldRead, oldPC uint32
		returns        bool
	}{
		{"mtvec-entry", 0x305091f3, 0x00000073, 0x200, 0x200, false},
		{"mepc-entry", 0x341091f3, 0x00000073, 0x240, 0x200, false},
		{"mepc-return", 0x341091f3, 0x30200073, 0x240, 0x240, true},
	} {
		for _, simultaneous := range []bool{false, true} {
			t.Run(test.name+map[bool]string{false: "/prior-edge", true: "/same-edge"}[simultaneous], func(t *testing.T) {
				c, owners, _ := concurrentSetup(t)
				old := snap(t, owners[0]).TrapCSRs()
				old.MEPC = 0x240
				owners[0].SetTrapCSRs(old)
				if err := owners[0].SetSavedThreadMask(5); err != nil {
					t.Fatal(err)
				}
				if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 15, isa.LaneValues{0x300, 0x300, 0x300, 0x300}); err != nil {
					t.Fatal(err)
				}
				csr := concurrentToken(t, 1, test.csr, 0x100)
				trap := concurrentToken(t, 2, test.trap, 0x104)
				for _, signal := range []model.Signal{csr, trap} {
					if err := c.Begin(signal.Token); err != nil {
						t.Fatal(err)
					}
				}
				read := csr
				read.Token.ReadMask, read.Token.LastRead = 1, true
				concurrentEdge(t, c, 0, model.CoreReport{Read: read})
				read = trap
				read.Token.ReadMask, read.Token.LastRead = 0, true
				concurrentEdge(t, c, 1, model.CoreReport{Read: read})
				exec := model.CoreReport{Executed: [4]model.Signal{trap}}
				if !simultaneous {
					exec.Executed[2], exec.CSRRequest, exec.CSRRequestWindow = csr, csr, true
				}
				concurrentEdge(t, c, 2, exec)
				before := snap(t, owners[0])
				wantPC := test.oldPC
				if !simultaneous && (test.returns || test.name == "mtvec-entry") {
					wantPC = 0x300
				}
				for n := 0; n < 2; n++ {
					feedback, err := c.Feedback(3, trap, model.Signal{})
					if err != nil || len(feedback) != 1 || feedback[0].PC != wantPC {
						t.Fatal("wrong feedback-edge target", feedback, wantPC, err)
					}
					if test.returns && (!feedback[0].UpdateMask || feedback[0].Mask != 5) {
						t.Fatal("return lost saved thread mask", feedback)
					}
					if snap(t, owners[0]) != before {
						t.Fatal("feedback preview mutated owner")
					}
				}
				report := model.CoreReport{Branch: trap}
				if simultaneous {
					report.Executed[2], report.CSRRequest, report.CSRRequestWindow = csr, csr, true
				}
				concurrentEdge(t, c, 3, report)
				after := snap(t, owners[0])
				if after.PC() != wantPC {
					t.Fatal("owner disagrees with scheduler feedback", after.PC(), wantPC)
				}
				if test.returns {
					if after.TrapCSRs().MEPC != 0x300 || after.ActiveMask() != 5 {
						t.Fatal("return overwrote software CSR or omitted mask", after.TrapCSRs(), after.ActiveMask())
					}
				} else if after.TrapCSRs().MEPC != 0x104 || after.TrapCSRs().MCause != 11 || after.TrapCSRs().MTVal != 0 || after.SavedThreadMask() != 15 {
					t.Fatal("hardware trap write priority lost", after.TrapCSRs(), after.SavedThreadMask())
				}
				if test.name == "mtvec-entry" && after.TrapCSRs().MTVec != 0x300 {
					t.Fatal("trap discarded software MTVEC write")
				}
				concurrentEdge(t, c, 4, model.CoreReport{Writeback: csr})
				if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{test.oldRead, test.oldRead, test.oldRead, test.oldRead}) {
					t.Fatal("CSR result did not sample old edge", got, test.oldRead)
				}
				concurrentEdge(t, c, 5, model.CoreReport{Writeback: trap, PendingRelease: csr})
				concurrentEdge(t, c, 6, model.CoreReport{PendingRelease: trap})
				if done, err := c.Reap(); err != nil || len(done) != 2 {
					t.Fatal("joint receipts failed", done, err)
				}
			})
		}
	}
}

func TestTrapCSRFlagsAndPriorBarrierShareOldEdge(t *testing.T) {
	_, owners, ram := concurrentSetup(t)
	for index, value := range map[uint8]uint32{1: 0, 2: 1} {
		if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: index}, 15, isa.LaneValues{value, value, value, value}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	c, err := effects.NewConcurrent(owners, 9, ram, [4]effects.ExternalOwner{func(e isa.InstructionEffects) error { calls += len(e.Barriers); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var barWord uint32
	for _, entry := range isa.Catalog() {
		if entry.Name == "bar.arrive" {
			barWord = entry.Example&^uint32(31<<7|31<<15|31<<20) | 4<<7 | 1<<15 | 2<<20
		}
	}
	fp := concurrentToken(t, 1, 0x182081d3, 0x100)
	bar := concurrentToken(t, 2, barWord, 0x104)
	csr := concurrentToken(t, 3, 0x002111f3, 0x108) // FRM=1, old FRM read into x3
	trap := concurrentToken(t, 4, 0x00000073, 0x10c)
	for _, s := range []model.Signal{fp, bar, csr, trap} {
		if err := c.Begin(s.Token); err != nil {
			t.Fatal(err)
		}
	}
	for n, s := range []model.Signal{fp, bar, csr, trap} {
		read := s
		read.Token.ReadMask, read.Token.LastRead = s.Token.Used, true
		for k, source := range s.Token.Sources {
			if source == 0 {
				read.Token.ReadMask &^= 1 << k
			}
		}
		report := model.CoreReport{Read: read}
		if n == 1 {
			report.Executed[3] = fp
		}
		concurrentEdge(t, c, uint64(n), report)
	}
	concurrentEdge(t, c, 4, model.CoreReport{Executed: [4]model.Signal{trap, {}, bar}})
	concurrentEdge(t, c, 5, model.CoreReport{Executed: [4]model.Signal{{}, {}, csr}, CSRRequest: csr, CSRRequestWindow: true, Flags: fp, Control: bar, Branch: trap})
	s := snap(t, owners[0])
	if calls != 1 || s.FCSR() != 0x21 || s.PC() != 0x200 || s.TrapCSRs().MEPC != 0x10c {
		t.Fatal("combined edge lost a producer", calls, s.FCSR(), s.PC(), s.TrapCSRs())
	}
	concurrentEdge(t, c, 6, model.CoreReport{Writeback: fp})
	concurrentEdge(t, c, 7, model.CoreReport{PendingRelease: fp, Writeback: bar})
	concurrentEdge(t, c, 8, model.CoreReport{PendingRelease: bar, Writeback: csr})
	if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{}) {
		t.Fatal("CSR read forwarded new FRM", got)
	}
	concurrentEdge(t, c, 9, model.CoreReport{PendingRelease: csr, Writeback: trap})
	concurrentEdge(t, c, 10, model.CoreReport{PendingRelease: trap})
	if done, err := c.Reap(); err != nil || len(done) != 4 {
		t.Fatal("combined edge lost receipts", done, err)
	}
}

func TestHeldCSRReReadsHardwareTrapWriteOnNextEdge(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	csr := concurrentToken(t, 1, 0x341091f3, 0x100) // csrrw mepc=6
	trap := concurrentToken(t, 2, 0x00000073, 0x104)
	for _, s := range []model.Signal{csr, trap} {
		if err := c.Begin(s.Token); err != nil {
			t.Fatal(err)
		}
	}
	read := csr
	read.Token.ReadMask, read.Token.LastRead = 1, true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	read = trap
	read.Token.ReadMask, read.Token.LastRead = 0, true
	concurrentEdge(t, c, 1, model.CoreReport{Read: read})
	concurrentEdge(t, c, 2, model.CoreReport{Executed: [4]model.Signal{trap}})
	concurrentEdge(t, c, 3, model.CoreReport{Branch: trap, CSRRequest: csr, CSRRequestWindow: true})
	if got := snap(t, owners[0]).TrapCSRs().MEPC; got != 0x104 {
		t.Fatal("trap lost same-edge write priority", got)
	}
	concurrentEdge(t, c, 4, model.CoreReport{CSRRequest: csr, CSRRequestWindow: true, Executed: [4]model.Signal{{}, {}, csr}})
	if got := snap(t, owners[0]).TrapCSRs().MEPC; got != 6 {
		t.Fatal("held CSR did not repeat next-edge write", got)
	}
	concurrentEdge(t, c, 5, model.CoreReport{Writeback: csr})
	if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{0x104, 0x104, 0x104, 0x104}) {
		t.Fatal("accepted CSR did not re-read hardware update", got)
	}
	concurrentEdge(t, c, 6, model.CoreReport{PendingRelease: csr, Writeback: trap})
	concurrentEdge(t, c, 7, model.CoreReport{PendingRelease: trap})
	if done, err := c.Reap(); err != nil || len(done) != 2 {
		t.Fatal("held joint receipts incomplete", done, err)
	}
}
