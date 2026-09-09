package model

import "testing"

func TestCTADispatchPreservesOtherWorkAndRejectsStale(t *testing.T) {
	var contexts [4]WarpContext
	for w := range contexts {
		contexts[w].Epoch = 1
	}
	contexts[1] = WarpContext{Active: true, Mask: 15, PC: 0x100, Epoch: 1}
	c, err := NewScheduledCore("std", contexts)
	if err != nil {
		t.Fatal(err)
	}
	if !c.WarpQuiescent(0) || c.WarpQuiescent(1) {
		t.Fatal("slot-specific availability")
	}
	p, err := c.DispatchWarp(0, 0x200, 3)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := c.DispatchWarp(2, 0x300, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = CommitEdge(p); err != nil {
		t.Fatal(err)
	}
	if err = CommitEdge(stale); err == nil {
		t.Fatal("stale activation accepted")
	}
	s := c.front.scheduler.State()
	if s.Warps[1] != contexts[1] || s.Warps[2].Active || !s.Warps[0].Active || s.Warps[0].Mask != 3 || s.Warps[0].PC != 0x200 {
		t.Fatal(s)
	}
	if _, err = c.DispatchWarp(0, 0x400, 15); err == nil {
		t.Fatal("active slot overwritten")
	}
	c.account.pending = []Token{{Warp: 2, Epoch: 1, ID: 100}}
	if c.WarpQuiescent(2) {
		t.Fatal("hardware pending ignored")
	}
}
