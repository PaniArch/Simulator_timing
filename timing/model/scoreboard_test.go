package model

import (
	"reflect"
	"testing"
)

func scoreboardToken(id uint64, warp, class, rd uint8) Signal {
	return Signal{Valid: true, Token: Token{ID: id, Warp: warp, Class: class, Mask: 15, Destination: rd, Writeback: rd != 0, End: true, FULock: true, FUUnlock: true}}
}
func newScoreboard(t *testing.T) *Scoreboard {
	t.Helper()
	s, err := NewScoreboard()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func scoreEdge(t *testing.T, s *Scoreboard, in [4]Signal, wb Signal, downstream bool, releases [4]bool) ScoreboardTransition {
	t.Helper()
	p, err := s.Evaluate(in, wb, downstream, releases)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	return p
}

func TestScoreboardFourWarpCompetitionAndFullSkid(t *testing.T) {
	s := newScoreboard(t)
	var in [4]Signal
	for w := range in {
		in[w] = scoreboardToken(uint64(w+1), uint8(w), uint8(w), 1)
	}
	for edge := 0; edge <= 6; edge++ {
		offered := [4]Signal{}
		if edge == 0 {
			offered = in
		}
		// E1/E2 fill the skid; E3 is blocked, E4 drains without borrowing a
		// slot, E5/E6 accept remaining warps in the RTL masked priority order.
		p := scoreEdge(t, s, offered, Signal{}, edge >= 4, [4]bool{})
		want := -1
		switch edge {
		case 1:
			want = 0
		case 2:
			want = 1
		case 5:
			want = 2
		case 6:
			want = 3
		}
		if p.Issued.Valid != (want >= 0) || want >= 0 && int(p.Issued.Token.Warp) != want {
			t.Fatalf("E%d issued %+v, want warp %d", edge, p.Issued, want)
		}
		if edge == 0 && p.AcceptedWarp != [4]bool{true, true, true, true} {
			t.Fatal("four independent staging admissions", p.AcceptedWarp)
		}
		if (edge == 3 || edge == 4) && p.Selected != 2 {
			t.Fatal("stall advanced arbiter", edge, p.Selected)
		}
	}
	if s.State().Credits != [4]int{1, 1, 1, 1} {
		t.Fatal(s.State())
	}
}

func TestScoreboardRAWFinalWBAndOtherWarpProgress(t *testing.T) {
	s := newScoreboard(t)
	writer := scoreboardToken(1, 0, 1, 32) // f0 is a real destination
	dependent := scoreboardToken(2, 0, 0, 2)
	dependent.Token.Sources[0] = 32
	dependent.Token.Used = 1
	other := scoreboardToken(3, 1, 0, 2)
	for edge := 0; edge <= 4; edge++ {
		in := [4]Signal{}
		wb := Signal{}
		switch edge {
		case 0:
			in[0] = writer
		case 1:
			in[0] = dependent
			in[1] = other
		case 2:
			wb = writer
			wb.Token.End = false
		case 3:
			wb = writer
		}
		p := scoreEdge(t, s, in, wb, true, [4]bool{})
		want := uint64(0)
		switch edge {
		case 1:
			want = 1
		case 2:
			want = 3
		case 4:
			want = 2
		}
		if p.Issued.Valid != (want != 0) || want != 0 && p.Issued.Token.ID != want {
			t.Fatalf("E%d issued %+v want ID%d", edge, p.Issued, want)
		}
		if edge == 2 && (s.State().Eligible[0] || s.State().Busy[0]&(uint64(1)<<32) == 0) {
			t.Fatal("partial WB released f0")
		}
		if edge == 3 && !s.State().Eligible[0] {
			t.Fatal("final WB did not register dependent eligibility")
		}
	}
}

func TestScoreboardReleaseReserveNextStateAndSpecialWAW(t *testing.T) {
	s := newScoreboard(t)
	writer := scoreboardToken(1, 0, 3, 1)
	writer.Token.WriteSpecial = 1
	next := scoreboardToken(2, 0, 3, 2)
	next.Token.WriteSpecial = 1
	scoreEdge(t, s, [4]Signal{writer}, Signal{}, true, [4]bool{}) // E0 staging
	scoreEdge(t, s, [4]Signal{next}, Signal{}, true, [4]bool{})   // E1 reserve flags
	if s.State().Eligible[0] {
		t.Fatal("special WAW was ignored")
	}
	p := scoreEdge(t, s, [4]Signal{}, writer, true, [4]bool{}) // E2 release -> ready register
	if p.Issued.Valid || !s.State().Eligible[0] {
		t.Fatal("special release bypassed or missed readiness")
	}
	p = scoreEdge(t, s, [4]Signal{}, Signal{}, true, [4]bool{}) // E3 next writer
	if !p.Issued.Valid || p.Issued.Token.ID != 2 || s.State().Special[0] != 1 {
		t.Fatal("special writer did not resume")
	}

	// Deliberately seed the combinational equation's overlap case. This does
	// not assert that same-rd release+reserve is reachable from legal decode.
	// It verifies reserve dominance independently of proposal/call ordering.
	overlap := newScoreboard(t)
	tok := scoreboardToken(7, 0, 0, 4)
	tok.Token.WriteSpecial = 3
	overlap.busy[0] = 1 << 4
	overlap.special[0] = 3
	overlap.ready[0] = true
	overlap.staging[0].queue = []Token{tok.Token}
	a, err := overlap.Evaluate([4]Signal{}, tok, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	before := overlap.State()
	b, err := overlap.Evaluate([4]Signal{}, tok, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if overlap.State() != before || !reflect.DeepEqual(a.Issued, b.Issued) {
		t.Fatal("Evaluate changed old state")
	}
	for i, j := 0, len(b.edits)-1; i < j; i, j = i+1, j-1 {
		b.edits[i], b.edits[j] = b.edits[j], b.edits[i]
	}
	commit(t, b.Transition)
	if overlap.State().Busy[0] != 1<<4 || overlap.State().Special[0] != 3 || overlap.State().Eligible[0] {
		t.Fatal("reserve must dominate release", overlap.State())
	}
	after := overlap.State()
	if CommitEdge(a.Transition) == nil || overlap.State() != after {
		t.Fatal("stale proposal changed scoreboard")
	}
}

func TestScoreboardCreditGuardAndSequenceLock(t *testing.T) {
	t.Run("old-credit-guard", func(t *testing.T) {
		s := newScoreboard(t)
		for edge := 0; edge <= 8; edge++ {
			in := [4]Signal{scoreboardToken(uint64(edge+1), 0, 0, 0)}
			release := [4]bool{}
			if edge == 5 || edge == 6 {
				release[0] = true
			}
			p := scoreEdge(t, s, in, Signal{}, true, release)
			want := edge >= 1 && edge <= 4 || edge == 8
			if p.Issued.Valid != want {
				t.Fatalf("E%d issue %t want %t", edge, p.Issued.Valid, want)
			}
			counts := []int{0, 1, 2, 3, 4, 3, 2, 2, 3}
			if s.State().Credits[0] != counts[edge] {
				t.Fatalf("E%d credits %+v", edge, s.State())
			}
		}
	})
	t.Run("sequence-lock", func(t *testing.T) {
		s := newScoreboard(t)
		first := scoreboardToken(1, 0, 1, 0)
		first.Token.FUUnlock = false
		last := scoreboardToken(2, 0, 1, 0)
		last.Token.FULock = false
		rival := scoreboardToken(3, 1, 1, 0)
		for edge := 0; edge <= 3; edge++ {
			in := [4]Signal{}
			if edge == 0 {
				in[0] = first
				in[1] = rival
			}
			if edge == 1 {
				in[0] = last
			}
			p := scoreEdge(t, s, in, Signal{}, true, [4]bool{})
			if edge > 0 && (!p.Issued.Valid || p.Issued.Token.ID != uint64(edge)) {
				t.Fatalf("E%d wrong lock sequence %+v", edge, p.Issued)
			}
			if edge == 1 && (!s.State().Locked[1] || s.State().Eligible[1]) {
				t.Fatal("acquire did not suppress rival")
			}
			if edge == 2 && (s.State().Locked[1] || !s.State().Eligible[1]) {
				t.Fatal("unlock did not register rival eligibility")
			}
		}
	})
}

func TestScoreboardPackedUopWAWBoundary(t *testing.T) {
	s := newScoreboard(t)
	seq, err := NewSequencer()
	if err != nil {
		t.Fatal(err)
	}
	in := scoreboardToken(1, 0, 1, 32)
	in.Token.Pack = 1
	var last Signal
	issued := 0
	for edge := 0; edge <= 12; edge++ {
		wb := Signal{}
		if edge == 4 || edge == 7 || edge == 10 {
			wb = last
		} // final WB per uop
		if edge == 3 {
			wb = last
			wb.Token.End = false
		}
		releases := [4]bool{}
		if edge == 3 || edge == 6 || edge == 9 {
			releases[1] = true
		}
		p, err := s.Evaluate([4]Signal{seq.Output(in)}, wb, true, releases)
		if err != nil {
			t.Fatal(err)
		}
		q, err := seq.Evaluate(in, p.Ready[0])
		if err != nil {
			t.Fatal(err)
		}
		if err = CommitEdge(q, p.Transition); err != nil {
			t.Fatal(err)
		}
		if q.Accepted {
			in = Signal{}
		}
		want := edge == 2 || edge == 5 || edge == 8 || edge == 11
		if p.Issued.Valid != want {
			t.Fatalf("E%d packed issue %+v", edge, p.Issued)
		}
		if want {
			if int(p.Issued.Token.Uop) != issued || !p.Issued.Token.FULock || !p.Issued.Token.FUUnlock || s.State().Locked[1] {
				t.Fatal("packed uop identity/11 lock semantics", p.Issued)
			}
			issued++
			last = p.Issued
		}
	}
	if issued != 4 || seq.Occupancy() != 0 {
		t.Fatal("packed macro did not finish")
	}
}

func TestScoreboardInvalidFeedbackIsAtomicAndFlushResets(t *testing.T) {
	s := newScoreboard(t)
	scoreEdge(t, s, [4]Signal{scoreboardToken(1, 0, 0, 1)}, Signal{}, true, [4]bool{})
	before := s.Resources()
	if _, err := s.Evaluate([4]Signal{}, Signal{}, true, [4]bool{false, true}); err == nil {
		t.Fatal("unowned credit release")
	}
	if _, err := s.Evaluate([4]Signal{}, scoreboardToken(2, 0, 0, 2), true, [4]bool{}); err == nil {
		t.Fatal("unowned register release")
	}
	if !reflect.DeepEqual(before, s.Resources()) {
		t.Fatal("failed evaluation mutated storage")
	}
	scoreEdge(t, s, [4]Signal{}, Signal{}, true, [4]bool{})
	commit(t, s.Flush())
	if s.State() != (ScoreboardState{}) {
		t.Fatal("flush left transient reservations", s.State())
	}
	for _, r := range s.Resources() {
		if r.Occupancy != 0 {
			t.Fatal(r)
		}
	}
}

func TestScoreboardRegisterNamespacesAndSpecialReads(t *testing.T) {
	for _, tc := range []struct {
		name           string
		writer, reader Token
		blocked        bool
	}{
		{"integer-RAW", Token{Destination: 1, Writeback: true}, Token{Sources: [3]uint8{1}, Used: 1}, true},
		{"float-is-separate", Token{Destination: 1, Writeback: true}, Token{Sources: [3]uint8{33}, Used: 1}, false},
		{"integer-WAW", Token{Destination: 1, Writeback: true}, Token{Destination: 1, Writeback: true}, true},
		{"zero-never-busy", Token{Destination: 1, Writeback: true}, Token{Sources: [3]uint8{0}, Used: 1}, false},
		{"fflags-read", Token{WriteSpecial: 1}, Token{ReadSpecial: 1}, true},
		{"frm-independent", Token{WriteSpecial: 1}, Token{ReadSpecial: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScoreboard(t)
			writer := Signal{Valid: true, Token: tc.writer}
			writer.Token.ID = 1
			writer.Token.End = true
			reader := Signal{Valid: true, Token: tc.reader}
			reader.Token.ID = 2
			reader.Token.End = true
			scoreEdge(t, s, [4]Signal{writer}, Signal{}, true, [4]bool{})
			scoreEdge(t, s, [4]Signal{reader}, Signal{}, true, [4]bool{})
			if s.State().Eligible[0] == tc.blocked {
				t.Fatal("incorrect registered dependency", s.State())
			}
			p := scoreEdge(t, s, [4]Signal{}, writer, true, [4]bool{})
			if p.Issued.Valid == tc.blocked {
				t.Fatal("release bypass or unrelated candidate blocked", p.Issued)
			}
			p = scoreEdge(t, s, [4]Signal{}, Signal{}, true, [4]bool{})
			if p.Issued.Valid != tc.blocked {
				t.Fatal("registered wakeup cycle", p.Issued)
			}
		})
	}
}
