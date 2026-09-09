package model

import "testing"

func TestSchedulerFeedbackRegisteredRecovery(t *testing.T) {
	for _, kind := range []FeedbackKind{FeedbackBranch, FeedbackTMC, FeedbackSplit, FeedbackJoin, FeedbackWake} {
		t.Run(string(kind), func(t *testing.T) {
			contexts := [4]WarpContext{
				{Active: true, Stalled: true, PC: 0x104, Mask: 15, Epoch: 9},
				{Active: true, PC: 0x200, Mask: 3, Epoch: 9},
				{Active: true, Stalled: true, PC: 0x300, Mask: 12, Epoch: 9},
			}
			s, err := NewScheduler(contexts)
			if err != nil {
				t.Fatal(err)
			}
			event := SchedulerFeedback{Token: Token{Warp: 0, Epoch: 9, ID: 1}, Kind: kind}
			switch kind {
			case FeedbackBranch:
				event.UpdatePC = true
				event.PC = 0x180
			case FeedbackTMC:
				event.UpdateMask = true
				event.Mask = 0
			case FeedbackSplit:
				event.UpdateMask = true
				event.Mask = 5
			case FeedbackJoin:
				event.UpdateMask = true
				event.Mask = 10
				event.UpdatePC = true
				event.PC = 0x140
			}
			before := s.State()
			p, err := s.Evaluate(true, Signal{}, [4]bool{}, Signal{}, event)
			if err != nil {
				t.Fatal(err)
			}
			if s.State() != before || s.Output().Token.Warp != 1 {
				t.Fatal("feedback bypassed edge")
			}
			if err = CommitEdge(p); err != nil {
				t.Fatal(err)
			}
			after := s.State()
			if after.Warps[0].Stalled || after.LastControl[0] != 1 || after.Warps[2] != contexts[2] {
				t.Fatal(after)
			}
			if event.UpdatePC && after.Warps[0].PC != event.PC || event.UpdateMask && after.Warps[0].Mask != event.Mask {
				t.Fatal("result context lost")
			}
			if !after.Warps[1].Stalled || after.IBufferCount[1] != 1 {
				t.Fatal("other warp scheduling lost")
			}
			if kind == FeedbackTMC {
				if after.Warps[0].Active || s.Output().Valid {
					t.Fatal("terminated warp remains runnable")
				}
			} else if !s.Output().Valid || s.Output().Token.Warp != 0 {
				t.Fatal("recovery missing at next cycle")
			}
			if _, err = s.Evaluate(false, Signal{}, [4]bool{}, Signal{}, event); err == nil {
				t.Fatal("duplicate feedback accepted")
			}
		})
	}
}

func TestSchedulerFeedbackRejectsBeforeMutation(t *testing.T) {
	contexts := [4]WarpContext{{Active: true, Stalled: true, PC: 0x104, Mask: 15, Epoch: 9}}
	good := SchedulerFeedback{Token: Token{Warp: 0, Epoch: 9, ID: 1}, Kind: FeedbackBranch, UpdatePC: true, PC: 0x180}
	stale := good
	stale.Token.Epoch = 8
	badPC := good
	badPC.PC = 3
	zeroMask := good
	zeroMask.UpdateMask = true
	for _, events := range [][]SchedulerFeedback{{stale}, {badPC}, {zeroMask}, {good, good}} {
		s, _ := NewScheduler(contexts)
		before := s.State()
		if _, err := s.Evaluate(false, Signal{}, [4]bool{}, Signal{}, events...); err == nil {
			t.Fatal("invalid feedback accepted", events)
		}
		if s.State() != before {
			t.Fatal("rejected feedback changed state")
		}
	}
}

func TestSchedulerFeedbackPriorityIndependentOfProducerOrder(t *testing.T) {
	branch := SchedulerFeedback{Token: Token{Warp: 0, Epoch: 9, ID: 2}, Kind: FeedbackBranch, UpdatePC: true, PC: 0x400}
	join := SchedulerFeedback{Token: Token{Warp: 0, Epoch: 9, ID: 3}, Kind: FeedbackJoin, UpdatePC: true, PC: 0x300, UpdateMask: true, Mask: 3}
	for _, events := range [][]SchedulerFeedback{{branch, join}, {join, branch}} {
		s, _ := NewScheduler([4]WarpContext{{Active: true, Stalled: true, Mask: 15, Epoch: 9}})
		before := append([]SchedulerFeedback(nil), events...)
		p, err := s.Evaluate(false, Signal{}, [4]bool{}, Signal{}, events...)
		if err != nil {
			t.Fatal(err)
		}
		if err := CommitEdge(p); err != nil {
			t.Fatal(err)
		}
		got := s.State()
		if got.Warps[0].PC != branch.PC || got.Warps[0].Mask != join.Mask || got.Warps[0].Stalled || got.LastControl[0] != 3 {
			t.Fatal("lost per-field priority", got)
		}
		for i := range events {
			if events[i] != before[i] {
				t.Fatal("proposal reordered caller slice")
			}
		}
	}
}

func TestSchedulerFrontendAndFeedbackAssignmentPriority(t *testing.T) {
	// These are interface-priority tests, not injected speculative execution.
	// VX_scheduler gives schedule stall and fetch PC advance the final ordinary
	// assignments; decode unlock and branch feedback must still be consumed.
	s, _ := NewScheduler([4]WarpContext{{Active: true, Mask: 15, Epoch: 9, PC: 0x100}})
	s.state.DecodeUnlock = Signal{Valid: true, Token: Token{Warp: 0}}
	branch := SchedulerFeedback{Token: Token{Warp: 0, Epoch: 9, ID: 1}, Kind: FeedbackBranch, UpdatePC: true, PC: 0x400}
	fetch := Signal{Valid: true, Token: Token{Warp: 0, PC: 0x200}}
	decode := Signal{Valid: true, Token: Token{Warp: 0, WarpStall: true}}
	p, err := s.Evaluate(true, fetch, [4]bool{}, decode, branch)
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitEdge(p); err != nil {
		t.Fatal(err)
	}
	got := s.State()
	if !got.Warps[0].Stalled || got.Warps[0].PC != 0x204 || got.LastControl[0] != 1 || got.DecodeUnlock != decode || got.IBufferCount[0] != 1 {
		t.Fatal("frontend/feedback update lost", got)
	}
}
