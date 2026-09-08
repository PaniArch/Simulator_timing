package model

import (
	"reflect"
	"testing"
)

func TestWholeCoreTokenStream(t *testing.T) {
	// Integer, multiply, divide, load, store, fence, WCTL, CSR, FPU and branch.
	words := []uint32{0x00100093, 0x022081b3, 0x0220c1b3, 0x00002083, 0x00102023, 0x0000000f, 0x0000000b, 0x001010f3, 0x00000053, 0x00000063}
	o := TokenOptions{Backend: "std", PeriodPS: 1000, FetchCycles: 2, MemoryCycles: 9, Budget: 1000}
	run, err := RunTokens(words, o)
	if err != nil {
		t.Fatal(err)
	}
	if run.Completed != len(words) {
		t.Fatal("incomplete stream")
	}
	counts := [4]int{}
	wb := 0
	for _, edge := range run.Cycles {
		for i, d := range edge.Dispatched {
			if d {
				counts[i]++
			}
		}
		if edge.Writeback.Valid {
			wb++
		}
		for _, r := range edge.Resources {
			if r.Occupancy > r.Capacity || r.Occupancy < 0 {
				t.Fatal("capacity", r)
			}
		}
	}
	if counts != [4]int{4, 3, 2, 1} || wb != len(words) {
		t.Fatal(counts, wb)
	}
	// Budget exhaustion is an error, never reported as successful completion.
	o.Budget = 3
	if _, err := RunTokens(words, o); err == nil {
		t.Fatal("budget silently completed")
	}
}
func TestCorePureEvaluationAndAtomicCommit(t *testing.T) {
	c, err := NewCore("std")
	if err != nil {
		t.Fatal(err)
	}
	in := CoreInputs{Instruction: valid(1), Eligible: true, ControlAllowed: true, FetchReady: true, MemoryReady: true}
	a, err := c.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	before := c.Resources()
	b, err := c.Evaluate(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Report, b.Report) || !reflect.DeepEqual(before, c.Resources()) {
		t.Fatal("pure evaluation changed state")
	}
	// Reverse every independent owner mutation, including nested components.
	for i, j := 0, len(b.edits)-1; i < j; i, j = i+1, j-1 {
		b.edits[i], b.edits[j] = b.edits[j], b.edits[i]
	}
	commit(t, b.Transition)
	after := c.Resources()
	if err := CommitEdge(a.Transition); err == nil {
		t.Fatal("stale core proposal accepted")
	}
	if !reflect.DeepEqual(after, c.Resources()) {
		t.Fatal("stale edge partially installed")
	}
	commit(t, c.Flush())
	if !c.Idle(true) {
		t.Fatal("flush did not clear transient core")
	}
}

func TestPackedTokenStreamWaitsForAllUops(t *testing.T) {
	run, err := RunTokens([]uint32{0x0800100b, 0x0800200b}, TokenOptions{Backend: "std", PeriodPS: 1000, FetchCycles: 1, MemoryCycles: 12, Budget: 300})
	if err != nil {
		t.Fatal(err)
	}
	count := map[uint64]int{}
	for _, r := range run.Cycles {
		if r.PendingRelease.Valid {
			count[r.PendingRelease.Token.ID]++
		}
	}
	if count[1] != 4 || count[2] != 2 || run.Completed != 2 {
		t.Fatal("packed burst completion", count)
	}
}
