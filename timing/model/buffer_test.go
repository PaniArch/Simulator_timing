package model

import (
	"errors"
	"reflect"
	"testing"
)

func mustBuffer(t *testing.T, id string) *Buffer {
	t.Helper()
	b, err := NewBuffer(id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func edge(t *testing.T, b *Buffer, id uint64, valid, ready bool) Transition {
	t.Helper()
	p := b.Evaluate(Signal{Valid: valid, Token: Token{ID: id}}, ready)
	if err := CommitEdge(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBufferCapacityAndFullRelease(t *testing.T) {
	for _, id := range []string{"b-alu-int-result", "b-schedule", "b-sfu-wctl-result", "b-ibuffer", "b-dispatch"} {
		t.Run(id, func(t *testing.T) {
			b := mustBuffer(t, id)
			for i := 0; i < b.Capacity(); i++ {
				p := edge(t, b, uint64(i+1), true, false)
				if !p.Accepted || p.Completed {
					t.Fatal("failed fill", p)
				}
			}
			before := b.Contents()
			for i := 0; i < 3; i++ {
				p := edge(t, b, 99, true, false)
				if p.Accepted || p.Output.Token.ID != 1 || !reflect.DeepEqual(before, b.Contents()) {
					t.Fatal("unstable full output")
				}
			}
			p := edge(t, b, 99, true, true)
			if !p.Completed || p.Accepted != (b.Capacity() == 1) {
				t.Fatal("incorrect full ready", p)
			}
			if b.Capacity() > 1 {
				p = edge(t, b, 100, true, true)
				if !p.Accepted || !p.Completed || b.Occupancy() != b.Capacity()-1 {
					t.Fatal("simultaneous push/pop")
				}
			}
			copy := b.Contents()
			if len(copy) > 0 {
				copy[0].ID = 999
				if b.Contents()[0].ID == 999 {
					t.Fatal("snapshot alias")
				}
			}
		})
	}
}

// This oracle uses the actual two-register valid equations, independently of
// the implementation's occupancy abstraction. Exhaust all 8-edge input/ready
// histories for both OUT_REG encodings, including invalid input while stalled.
func TestStreamBufferRegisteredReadyRTLOracle(t *testing.T) {
	for _, id := range []string{"b-schedule", "b-sfu-wctl-result"} {
		b := mustBuffer(t, id)
		for history := 0; history < 1<<16; history++ {
			if err := CommitEdge(b.Flush()); err != nil {
				t.Fatal(err)
			}
			vin, vout := true, false
			var data, saved uint64
			for k := 0; k < 8; k++ {
				valid, ready := history&(1<<(2*k)) != 0, history&(1<<(2*k+1)) != 0
				p := b.Evaluate(Signal{Valid: valid, Token: Token{ID: uint64(k + 1)}}, ready)
				if p.InputReady != vin || p.Output.Valid != vout || (vout && p.Output.Token.ID != data) {
					t.Fatalf("%s history=%x edge=%d got=%+v", id, history, k, p)
				}
				flow := ready || !vout
				nextReady, nextValid := vin, vout
				if valid || flow {
					nextReady = flow
				}
				if flow {
					nextValid = valid || !vin
					if vin {
						data = uint64(k + 1)
					} else {
						data = saved
					}
				}
				if valid && vin {
					saved = uint64(k + 1)
				}
				vin, vout = nextReady, nextValid
				if err := CommitEdge(p); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestCommonEdgeAndPassthrough(t *testing.T) {
	run := func(reverse bool) []uint64 {
		chain := []*Buffer{mustBuffer(t, "b-fetch-request"), mustBuffer(t, "b-decode"), mustBuffer(t, "b-ibuffer"), mustBuffer(t, "b-scoreboard-staging")}
		var completed []uint64
		for cycle := uint64(0); cycle < 6; cycle++ {
			ready := make([]bool, len(chain)+1)
			ready[len(chain)] = true
			for i := len(chain) - 1; i >= 0; i-- {
				ready[i] = chain[i].Ready(ready[i+1])
			}
			s := Signal{Valid: cycle == 0, Token: Token{ID: 1}}
			var plans []Transition
			for i, b := range chain {
				p := b.Evaluate(s, ready[i+1])
				plans = append(plans, p)
				s = p.Output
			}
			if s.Valid {
				completed = append(completed, cycle)
			}
			if reverse {
				for i, j := 0, len(plans)-1; i < j; i, j = i+1, j-1 {
					plans[i], plans[j] = plans[j], plans[i]
				}
			}
			if err := CommitEdge(plans...); err != nil {
				t.Fatal(err)
			}
		}
		return completed
	}
	if a, b := run(false), run(true); !reflect.DeepEqual(a, []uint64{3}) || !reflect.DeepEqual(a, b) {
		t.Fatalf("wrong registered crossing/order: %v %v", a, b)
	}
	b := mustBuffer(t, "b-decode")
	if p := edge(t, b, 1, true, false); p.Accepted || p.Completed || !p.Output.Valid || b.Occupancy() != 0 {
		t.Fatal("passthrough stored input")
	}
}

func TestEdgeRejectsStaleAndDuplicateAtomically(t *testing.T) {
	a, b := mustBuffer(t, "b-schedule"), mustBuffer(t, "b-dispatch")
	pa, pb := a.Evaluate(Signal{Valid: true}, false), b.Evaluate(Signal{Valid: true}, false)
	if err := CommitEdge(pa, pa); err == nil || a.Occupancy() != 0 {
		t.Fatal("duplicate mutated state")
	}
	if err := CommitEdge(pb); err != nil {
		t.Fatal(err)
	}
	if err := CommitEdge(pa, pb); err == nil || a.Occupancy() != 0 {
		t.Fatal("stale partially committed")
	}
	if err := CommitEdge(b.Flush()); err != nil || b.Occupancy() != 0 {
		t.Fatal("flush failed", err)
	}
}

func TestAkitaClockBudgetContinuationAndErrors(t *testing.T) {
	c, err := NewClock(1000)
	if err != nil {
		t.Fatal(err)
	}
	var cycles []uint64
	step := func(k uint64) (bool, error) { cycles = append(cycles, k); return false, nil }
	if err = c.Run(0, step); err != nil {
		t.Fatal(err)
	}
	if err = c.Run(2, step); err != nil {
		t.Fatal(err)
	}
	if err = c.Run(3, step); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cycles, []uint64{0, 1, 2, 3, 4}) {
		t.Fatal(cycles)
	}
	want := errors.New("edge failed")
	if err = c.Run(10, func(uint64) (bool, error) { return false, want }); !errors.Is(err, want) || c.Cycle() != 5 {
		t.Fatal("lost error", err)
	}
	if err = c.Run(10, func(uint64) (bool, error) { return true, nil }); err != nil || c.Cycle() != 6 {
		t.Fatal("ignored stop", err)
	}
}
