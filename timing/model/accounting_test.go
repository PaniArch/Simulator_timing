package model

import "testing"

func TestHardwarePendingRegisteredUopCommit(t *testing.T) {
	c := &Core{}
	step := func(issue, commit Signal) {
		t.Helper()
		p, err := c.account.evaluate(issue, commit, 0)
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
	if _, err := c.account.evaluate(Signal{}, b, 0); err == nil {
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
		p, err := c.account.evaluate(Signal{}, wb.PendingRelease, 0)
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
		p, err := c.account.evaluate(Signal{}, Signal{}, active)
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
