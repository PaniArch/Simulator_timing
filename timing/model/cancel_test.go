package model

import (
	"reflect"
	"testing"
)

func TestCancelRangeAndResources(t *testing.T) {
	scope := Cancellation{Warp: 0, Epoch: 9, After: 1, Through: 3}
	old := Token{ID: 1, Epoch: 9, Warp: 0, Mask: 15}
	young := old
	young.ID = 2
	other := young
	other.Warp = 1
	future := young
	future.ID = 4
	if scope.Matches(old) || !scope.Matches(young) || scope.Matches(other) || scope.Matches(future) {
		t.Fatal("range identity")
	}
	c, err := NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	c.alu.mul.stages[0] = Signal{Valid: true, Token: young}
	c.alu.mul.stages[1] = Signal{Valid: true, Token: old}
	c.alu.div.loaded = Signal{Valid: true, Token: other}
	c.alu.div.remaining = 12
	c.lsu.tags.entries = []Pending{{Token: young, Remaining: 10}, {Token: other, Remaining: 5}}
	c.lsu.sent = []Token{young, other}
	c.commit.feedback = Signal{Valid: true, Token: young}
	before := c.Resources()
	p, err := c.Cancel(scope)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c.Resources()) {
		t.Fatal("cancel mutated before edge")
	}
	commit(t, p)
	if c.alu.mul.stages[0].Valid || c.alu.mul.stages[1].Token != old || c.alu.div.remaining != 12 || c.alu.div.loaded.Token != other || c.commit.feedback.Valid {
		t.Fatal("pipeline age isolation")
	}
	if len(c.lsu.tags.entries) != 1 || c.lsu.tags.entries[0].Remaining != 5 || c.lsu.sent[0] != other {
		t.Fatal("partial coverage not retained")
	}
	if _, err = c.lsu.Evaluate(Signal{}, Response{Valid: true, ID: 2, Epoch: 9, Warp: 0, Mask: 10}, true, true); err == nil {
		t.Fatal("cancelled response accepted")
	}
	if err = CommitEdge(p); err == nil {
		t.Fatal("stale cancel proposal replayed")
	}
}

func TestCancelRejectsForeignSchedulerEpoch(t *testing.T) {
	c, err := NewScheduledCore("std", [4]WarpContext{{Active: true, Epoch: 10, PC: 0x100, Mask: 15}})
	if err != nil {
		t.Fatal(err)
	}
	before := c.front.scheduler.State()
	if _, err = c.Cancel(Cancellation{Warp: 0, Epoch: 9, After: 0, Through: 10}); err == nil {
		t.Fatal("old epoch parked current residency")
	}
	if c.front.scheduler.State() != before {
		t.Fatal("foreign cancellation mutated scheduler")
	}
}
