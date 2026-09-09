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
	s, _ := NewScheduler(contexts)
	if _, err := s.Evaluate(false, Signal{}, [4]bool{}, Signal{Valid: true, Token: Token{Warp: 0}}, good); err == nil {
		t.Fatal("frontend collision accepted")
	}
}
