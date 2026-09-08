package effects_test

import (
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func initial() state.WarpInitial {
	v := state.WarpInitial{Topology: state.FrozenTopology(), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning, TrapCSRs: state.TrapCSRState{MTVec: 0x200}}
	for lane := uint8(0); lane < 4; lane++ {
		l := state.LaneInitial{ID: lane}
		l.GPR[1] = 6
		l.GPR[2] = 3
		l.GPR[3] = 0x7777
		l.FPR[1] = 0x3f800000
		l.FPR[2] = 0x40400000
		l.FPR[3] = 0x7777
		v.Lanes = append(v.Lanes, l)
	}
	return v
}
func snap(t *testing.T, w *state.WarpState) state.WarpSnapshot {
	t.Helper()
	s, e := w.Snapshot()
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func reg(t *testing.T, s state.WarpSnapshot, file isa.RegisterFile, index uint8) isa.LaneValues {
	t.Helper()
	v, e := s.ReadRegister(isa.Register{File: file, Index: index})
	if e != nil {
		t.Fatal(e)
	}
	return v
}

type frame struct {
	report        model.CoreReport
	before, after state.WarpSnapshot
}

func run(t *testing.T, word uint32, init state.WarpInitial) []frame {
	t.Helper()
	w, e := state.NewWarp(init)
	if e != nil {
		t.Fatal(e)
	}
	expected, e := state.NewWarp(init)
	if e != nil {
		t.Fatal(e)
	}
	result, e := expected.ExecuteSingle(word, state.ReadContext{})
	if e != nil || !result.Apply.Committed {
		t.Fatal("reference", result, e)
	}
	core, e := model.NewCore("std")
	if e != nil {
		t.Fatal(e)
	}
	adapter, e := effects.New(w, 7, nil)
	if e != nil {
		t.Fatal(e)
	}
	token := model.Token{ID: 1, Epoch: 7, PC: init.PC, Mask: uint8(init.ActiveMask), Warp: init.WarpID, Word: word}
	if e = adapter.Begin(token); e != nil {
		t.Fatal(e)
	}
	input := model.Signal{Valid: true, Token: token}
	response := model.Response{}
	frames := []frame{}
	done := false
	for cycle := uint64(0); cycle < 150; cycle++ {
		p, e := core.Evaluate(model.CoreInputs{Instruction: input, FetchResponse: response, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
		if e != nil {
			t.Fatal(e)
		}
		if e = model.CommitEdge(p.Transition); e != nil {
			t.Fatal(e)
		}
		before := snap(t, w)
		if e = adapter.Observe(cycle, p.Report, state.ReadContext{}); e != nil {
			t.Fatal("effect edge", cycle, e)
		}
		frames = append(frames, frame{p.Report, before, snap(t, w)})
		if p.Report.InstructionAccepted {
			input = model.Signal{}
		}
		if response.Valid && p.Report.FetchResponseReady {
			response = model.Response{}
		}
		if p.Report.FetchAccepted {
			r := p.Report.FetchRequest.Token
			response = model.Response{Valid: true, ID: r.ID, Epoch: r.Epoch, Warp: r.Warp, Mask: r.Mask, Word: word}
		}
		if p.Report.PendingRelease.Valid {
			done = true
		}
		if done && core.Idle(true) {
			if e = adapter.Finish(); e != nil {
				t.Fatal(e)
			}
			break
		}
	}
	if !done {
		t.Fatal("instruction did not complete")
	}
	if !reflect.DeepEqual(snap(t, w), snap(t, expected)) {
		t.Fatal("functional/timing final state mismatch", word, snap(t, w), snap(t, expected))
	}
	return frames
}
func TestIntegerVisibilityUsesWritebackAndPending(t *testing.T) {
	fs := run(t, 0x002081b3, initial()) // add x3,x1,x2
	for _, f := range fs {
		if reg(t, f.before, isa.Integer, 3) != reg(t, f.after, isa.Integer, 3) && !f.report.Writeback.Valid {
			t.Fatal("early register effect")
		}
		if f.before.PC() != f.after.PC() && !f.report.PendingRelease.Valid {
			t.Fatal("sequential PC moved before pending")
		}
	}
}
func TestBranchAndWCTLVisibility(t *testing.T) {
	for _, word := range []uint32{0x00108463, 0x0000800b, 0x00000073} { // beq, tmc, ecall
		fs := run(t, word, initial())
		seen := false
		for _, f := range fs {
			if f.before.PC() != f.after.PC() || f.before.ActiveMask() != f.after.ActiveMask() {
				if !f.report.Branch.Valid && !f.report.Control.Valid {
					t.Fatal("control applied at wrong boundary")
				}
				seen = true
			}
		}
		if !seen {
			t.Fatal("missing control")
		}
	}
}
func TestCSRAndFFlagsAreNotBundledAtWB(t *testing.T) {
	for _, word := range []uint32{0x001091f3, 0x182081d3} { // csrrw x3,fflags,x1; fdiv.s f3,f1,f2
		fs := run(t, word, initial())
		seen := false
		for _, f := range fs {
			if f.before.FCSR() != f.after.FCSR() {
				if !f.report.Executed[2].Valid && !f.report.Flags.Valid {
					t.Fatal("wrong CSR/FFLAGS visibility")
				}
				seen = true
			}
		}
		if !seen {
			t.Fatal("missing CSR/FFLAGS update")
		}
	}
}
func TestWGatherPreservesFunctionalWriteMask(t *testing.T) {
	var word uint32
	for _, e := range isa.Catalog() {
		if e.Name == "wgather" {
			word = e.Match | (3 << 7) | (1 << 15) | (2 << 20)
		}
	}
	if word == 0 {
		t.Fatal("missing WGATHER")
	}
	v := initial()
	v.ActiveMask = 5
	run(t, word, v)
}
func TestEpochAndReportReplayRejectedBeforeMutation(t *testing.T) {
	w, _ := state.NewWarp(initial())
	a, _ := effects.New(w, 7, nil)
	token := model.Token{ID: 1, Epoch: 7, PC: 0x100, Mask: 15, Word: 0x002081b3}
	if err := a.Begin(token); err != nil {
		t.Fatal(err)
	}
	routed, _ := model.DecodeToken(model.Signal{Valid: true, Token: token})
	before := snap(t, w)
	stale := routed
	stale.Token.Epoch = 6
	if err := a.Observe(0, model.CoreReport{Read: stale}, state.ReadContext{}); err == nil {
		t.Fatal("stale event accepted")
	}
	if !reflect.DeepEqual(before, snap(t, w)) {
		t.Fatal("stale event mutated owner")
	}
	if err := a.Reset(8); err != nil {
		t.Fatal(err)
	}
	token.Epoch = 8
	token.ID = 1
	if err := a.Begin(token); err != nil {
		t.Fatal(err)
	}
	if err := a.Observe(1, model.CoreReport{}, state.ReadContext{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Observe(1, model.CoreReport{}, state.ReadContext{}); err == nil {
		t.Fatal("edge replay accepted")
	}
}

func TestJoinVisibilityIncludesSIMTFeedbackRegister(t *testing.T) {
	var word uint32
	for _, e := range isa.Catalog() {
		if e.Name == "join" {
			word = e.Match | (1 << 15)
		}
	}
	v := initial()
	for i := range v.Lanes {
		v.Lanes[i].GPR[1] = 0
	}
	fs := run(t, word, v)
	request, visible := -1, -1
	for edge, f := range fs {
		if f.report.Control.Valid {
			request = edge
		}
		if f.before.PC() != f.after.PC() {
			visible = edge
		}
	}
	if request < 0 || visible != request+1 {
		t.Fatal("JOIN did not cross registered SIMT feedback", request, visible)
	}
}

func TestLongIntegerPathsKeepResultsInvisibleUntilWB(t *testing.T) {
	for _, word := range []uint32{0x022081b3, 0x0220c1b3} {
		fs := run(t, word, initial())
		execute, writeback := -1, -1
		for edge, f := range fs {
			if f.report.Executed[0].Valid {
				execute = edge
			}
			if f.report.Writeback.Valid {
				writeback = edge
			}
			if reg(t, f.before, isa.Integer, 3) != reg(t, f.after, isa.Integer, 3) && !f.report.Writeback.Valid {
				t.Fatal("long result visible before WB")
			}
		}
		if execute < 0 || writeback-execute < 5 {
			t.Fatal("long execution bypassed pipeline", execute, writeback)
		}
	}
}
func TestForeignFeedbackRejectsWholeReportBeforeWB(t *testing.T) {
	fs := run(t, 0x002081b3, initial())
	w, _ := state.NewWarp(initial())
	a, _ := effects.New(w, 7, nil)
	if err := a.Begin(model.Token{ID: 1, Epoch: 7, PC: 0x100, Mask: 15, Word: 0x002081b3}); err != nil {
		t.Fatal(err)
	}
	for cycle, f := range fs {
		r := f.report
		if r.Writeback.Valid {
			before := snap(t, w)
			r.Branch = r.Writeback
			r.Branch.Token.ID++
			if err := a.Observe(uint64(cycle), r, state.ReadContext{}); err == nil {
				t.Fatal("foreign feedback accepted")
			}
			if !reflect.DeepEqual(before, snap(t, w)) {
				t.Fatal("WB applied before identity preflight")
			}
			return
		}
		if err := a.Observe(uint64(cycle), r, state.ReadContext{}); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("no writeback")
}
