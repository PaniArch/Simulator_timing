package model

import "testing"

func TestHardwarePendingRegisteredUopCommit(t *testing.T) {
	c := &Core{}
	step := func(issue, commit Signal) {
		t.Helper()
		p, err := c.account.evaluate(issue, commit, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if err = CommitEdge(p); err != nil {
			t.Fatal(err)
		}
	}
	a := Signal{Valid: true, Token: Token{ID: 1, Epoch: 1, Warp: 2, Uops: 2, End: true}}
	b := a
	b.Token.Uop = 1
	step(a, Signal{})
	if c.HardwarePending()[2] != 0 {
		t.Fatal("issue notification bypassed register")
	}
	step(b, Signal{})
	if c.HardwarePending()[2] != 1 {
		t.Fatal("missing registered issue")
	}
	step(Signal{}, a)
	if c.HardwarePending()[2] != 1 || c.Instret() != 1 {
		t.Fatal("simultaneous issue/commit did not balance")
	}
	step(Signal{}, b)
	if c.HardwarePending()[2] != 0 || c.Instret() != 2 {
		t.Fatal("packed uops were counted as one macro")
	}
	if _, err := c.account.evaluate(Signal{}, b, 0, false); err == nil {
		t.Fatal("duplicate commit accepted")
	}
}

func TestHardwarePendingPartialWritebackAndCancellation(t *testing.T) {
	c := &Core{}
	commit, err := NewCommit()
	if err != nil {
		t.Fatal(err)
	}
	token := Token{ID: 7, Epoch: 1, Warp: 1, Mask: 1}
	c.account.pending = []Token{token}
	step := func(input Signal) {
		t.Helper()
		wb := commit.Evaluate([4]Signal{input})
		p, err := c.account.evaluate(Signal{}, wb.PendingRelease, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if err = CommitEdge(wb.Transition, p); err != nil {
			t.Fatal(err)
		}
	}
	step(Signal{Valid: true, Token: token})
	for n := 0; n < 4; n++ {
		step(Signal{})
	}
	if c.Instret() != 0 || c.HardwarePending()[1] != 1 {
		t.Fatal("partial WB released pending")
	}
	token.End = true
	step(Signal{Valid: true, Token: token})
	for n := 0; n < 4; n++ {
		step(Signal{})
	}
	if c.Instret() != 1 || c.HardwarePending()[1] != 0 {
		t.Fatal("EOP did not release pending")
	}
	c.account.pending = []Token{token}
	c.account.issued = Signal{Valid: true, Token: token}
	if err := CommitEdge(c.account.cancel(Cancellation{Warp: 1, Epoch: 1, Through: 7})); err != nil {
		t.Fatal(err)
	}
	step(Signal{})
	if c.HardwarePending()[1] != 0 || c.Instret() != 1 {
		t.Fatal("cancellation retired or resurrected work")
	}
}

func TestLSUSchedulerDrainIncludesTagsButExcludesResults(t *testing.T) {
	lsu, err := NewLSU()
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{lsu: lsu}
	token := Token{ID: 1, Epoch: 1, Mask: 15, Path: LOAD}
	lsu.tags.entries = []Pending{{Token: token, Remaining: 15}}
	lsu.load.queue = []Token{token}
	if c.LSUSchedulerDrained() {
		t.Fatal("outstanding load did not block BAR")
	}
	lsu.tags.entries = nil
	if !c.LSUSchedulerDrained() {
		t.Fatal("result queue blocked BAR after tag release")
	}
	lsu.request.queue = []Token{token}
	if c.LSUSchedulerDrained() {
		t.Fatal("queued request did not block BAR")
	}
}

func TestSchedulerCycleCounterBusyRegisterAndWrap(t *testing.T) {
	c := &Core{}
	step := func(active uint8) {
		t.Helper()
		p, err := c.account.evaluate(Signal{}, Signal{}, active, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := CommitEdge(p); err != nil {
			t.Fatal(err)
		}
	}
	step(0)
	step(1)
	if c.Cycles() != 0 {
		t.Fatal("busy qualification bypassed register")
	}
	step(0)
	if c.Cycles() != 1 {
		t.Fatal("old busy not counted")
	}
	step(0)
	if c.Cycles() != 1 {
		t.Fatal("idle counted as busy")
	}
	c.account.pending = []Token{{ID: 1}}
	step(0)
	step(0)
	if c.Cycles() != 2 {
		t.Fatal("inactive pending work did not keep scheduler busy")
	}
	c.account.cycles = (1 << 44) - 1
	step(0)
	if c.Cycles() != 0 {
		t.Fatal("counter did not wrap at RTL width")
	}
}

func TestCounterContinuationAndIdleTail(t *testing.T) {
	previous, err := NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	previous.account = instructionAccounting{cycles: (1 << 44) - 1, instret: 27, busy: true}
	next, err := NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	if err := next.ContinueCounters(previous); err != nil {
		t.Fatal(err)
	}
	if next.Cycles() != previous.Cycles() || next.Instret() != 27 || !next.account.busy {
		t.Fatal("counter state lost")
	}
	if err := next.ClockIdleCounters(); err != nil {
		t.Fatal(err)
	}
	if next.Cycles() != 0 || next.Instret() != 27 || next.account.busy {
		t.Fatal("registered tail/width")
	}
	if err := next.ClockIdleCounters(); err != nil {
		t.Fatal(err)
	}
	if next.Cycles() != 0 {
		t.Fatal("idle flush counted as busy")
	}
	if err := next.ContinueCounters(previous); err == nil {
		t.Fatal("overwrote used counters")
	}
	previous.account.pending = []Token{{ID: 1}}
	if err := (&Core{}).ContinueCounters(previous); err == nil {
		t.Fatal("continued live instructions")
	}
	if err := previous.ClockIdleCounters(); err == nil {
		t.Fatal("idle clock discarded live instruction")
	}
}

func TestCounterDispatchBusyIsCombinationalOR(t *testing.T) {
	var a instructionAccounting
	for _, step := range []struct {
		dispatch bool
		active   uint8
		want     uint64
	}{
		{true, 0, 1},  // admission, before any active Warp
		{true, 1, 2},  // selection/fire clocks busy_buf
		{true, 0, 3},  // both busy inputs: count once
		{false, 0, 3}, // dispatcher itself adds no registered tail
	} {
		p, err := a.evaluate(Signal{}, Signal{}, step.active, step.dispatch)
		if err != nil {
			t.Fatal(err)
		}
		if err := CommitEdge(p); err != nil {
			t.Fatal(err)
		}
		if a.cycles != step.want {
			t.Fatal(a.cycles, step)
		}
	}
}
