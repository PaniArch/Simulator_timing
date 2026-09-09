package model

import "testing"

func TestSchedulerFourWarpSelectionAndRegisteredUnlock(t *testing.T) {
	var contexts [4]WarpContext
	for w := range contexts {
		contexts[w] = WarpContext{Active: true, PC: uint32(0x100 + w*0x100), Mask: uint8(w + 1), Epoch: 9}
	}
	s, err := NewScheduler(contexts)
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := NewBuffer("b-schedule")
	if err != nil {
		t.Fatal(err)
	}
	for edge := 0; edge <= 6; edge++ {
		// Request acceptance stops at E2/E3. The buffered payload must remain
		// unchanged. At E2 the skid can still accept its second entry; only
		// the full skid at E3 prevents the next internal schedule acceptance.
		fetchReady := edge != 2 && edge != 3
		q := schedule.Evaluate(s.Output(), fetchReady)
		accepted := q.Output
		accepted.Valid = q.Completed
		decode := Signal{}
		if edge == 4 {
			decode = Signal{Valid: true, Token: Token{Warp: 0}}
		}
		p, err := s.Evaluate(q.InputReady, accepted, [4]bool{}, decode)
		if err != nil {
			t.Fatal(err)
		}
		before := s.State()
		if err = CommitEdge(q, p); err != nil {
			t.Fatal(err)
		}
		if q.Accepted && (edge == 0 || edge == 1) && q.Output.Valid && q.Output.Token.Warp != 0 {
			t.Fatal("schedule order")
		}
		if edge == 2 || edge == 3 {
			if q.Output.Token.Warp != 1 || q.Output.Token.PC != 0x200 || q.Output.Token.Mask != 2 || q.Output.Token.ID != 2 || q.Output.Token.Epoch != 9 {
				t.Fatal("schedule payload changed under backpressure", q.Output)
			}
			if edge == 3 && (q.Accepted || s.State() != before) {
				t.Fatal("stalled schedule advanced state")
			}
		}
		if edge == 0 && s.State().Warps[0].PC != 0x100 {
			t.Fatal("PC advanced on internal enqueue")
		}
		if edge == 1 && s.State().Warps[0].PC != 0x104 {
			t.Fatal("PC did not advance on Fetch acceptance")
		}
		if edge == 4 && !s.State().Warps[0].Stalled {
			t.Fatal("decode unlock bypassed register")
		}
		if edge == 5 && s.State().Warps[0].Stalled {
			t.Fatal("registered decode unlock was not consumed")
		}
		if edge == 6 && (!q.Accepted || s.State().Warps[0].PC != 0x104 || !s.State().Warps[0].Stalled) {
			t.Fatal("newly eligible warp was not selected at E6")
		}
	}
}

func TestSchedulerFullAccountingAndControlStall(t *testing.T) {
	var contexts [4]WarpContext
	for w := range contexts {
		contexts[w] = WarpContext{Active: true, Mask: 15}
	}
	s, err := NewScheduler(contexts)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the full-boundary fixture directly to distinguish equality from >=,
	// and all-full fallback from a generic hard FIFO-capacity guard.
	s.state.IBufferCount = [4]uint8{4, 4, 4, 3}
	s.state.IBufferFull = [4]bool{true, true, true, false}
	if s.Output().Token.Warp != 3 {
		t.Fatal("full warp selected before free warp")
	}
	p, err := s.Evaluate(true, Signal{}, [4]bool{}, Signal{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if !s.State().AllIBuffersFull || s.Output().Token.Warp != 0 {
		t.Fatal("all-full fallback missing")
	}
	p, err = s.Evaluate(true, Signal{}, [4]bool{}, Signal{Valid: true, Token: Token{Warp: 3, WarpStall: true}})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if s.State().IBufferCount[0] != 5 || s.State().IBufferFull[0] || s.State().AllIBuffersFull {
		t.Fatal("counter incorrectly clamped", s.State())
	}
	p, err = s.Evaluate(false, Signal{}, [4]bool{true}, Signal{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if !s.State().Warps[3].Stalled {
		t.Fatal("control decode must not unlock warp")
	}
	if s.State().IBufferCount[0] != 4 || !s.State().IBufferFull[0] {
		t.Fatal("registered pop accounting")
	}
	// Simultaneous schedule and pop keep the count, without inventing a slot.
	s.state.Warps[0].Stalled = false
	p, err = s.Evaluate(true, Signal{}, [4]bool{true}, Signal{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if s.State().IBufferCount[0] != 4 {
		t.Fatal("simultaneous count update")
	}
}
