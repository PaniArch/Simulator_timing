package model

import (
	"errors"
	"math"
	"reflect"
	"testing"

	akita "github.com/sarchlab/akita/v5/timing"
)

// Running the private engine again must do nothing: no queued edge may survive
// a public Run return (the callback has already been released).
func assertClockQuiescent(t *testing.T, c *Clock) {
	t.Helper()
	if c.driver.step != nil || c.driver.err != nil || c.driver.remaining != 0 {
		t.Fatal("clock retained per-run state")
	}
	cycle, now := c.Cycle(), c.driver.engine.CurrentTime()
	if err := c.driver.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if c.Cycle() != cycle || c.driver.engine.CurrentTime() != now {
		t.Fatal("engine retained an event")
	}
}

func TestClockPersistentEngineBoundaries(t *testing.T) {
	c, err := NewClock(7)
	if err != nil {
		t.Fatal(err)
	}
	engine := c.driver.engine
	sentinel := errors.New("failed edge")
	var seen []uint64
	for _, tc := range []struct {
		name       string
		budget     uint64
		stopAt     uint64
		failAt     uint64
		wantCycle  uint64
		wantEvents []uint64
		wantError  bool
	}{
		{"zero", 0, 99, 99, 0, nil, false},
		{"budget", 3, 99, 99, 3, []uint64{0, 1, 2}, false},
		{"stop", 8, 4, 99, 5, []uint64{3, 4}, false},
		{"error", 8, 99, 6, 6, []uint64{5, 6}, true},
		{"zero-after-error", 0, 99, 99, 6, nil, false},
		{"stop-and-error", 8, 6, 6, 6, []uint64{6}, true},
		{"resume-at-failed-time", 2, 99, 99, 8, []uint64{6, 7}, false},
		{"stop-last-budget-edge", 1, 8, 99, 9, []uint64{8}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen = nil
			err := c.Run(tc.budget, func(edge uint64) (bool, error) {
				seen = append(seen, edge)
				if now := c.driver.engine.CurrentTime(); now != akita.VTimeInPicoSec(edge*7) {
					t.Fatalf("edge %d at time %d", edge, now)
				}
				if edge == tc.failAt {
					return edge == tc.stopAt, sentinel
				}
				return edge == tc.stopAt, nil
			})
			if (tc.wantError && err != sentinel) || (!tc.wantError && err != nil) {
				t.Fatalf("callback error lost or stale: %v", err)
			}
			if c.Cycle() != tc.wantCycle || !reflect.DeepEqual(seen, tc.wantEvents) {
				t.Fatalf("cycle=%d events=%v", c.Cycle(), seen)
			}
			if c.driver.engine != engine || c.driver.clock != c {
				t.Fatal("engine/driver ownership changed")
			}
			assertClockQuiescent(t, c)
		})
	}
	for _, budget := range []uint64{0, 1} {
		if err := c.Run(budget, nil); err == nil || c.Cycle() != 9 {
			t.Fatal("nil callback must fail even with zero budget", err)
		}
		assertClockQuiescent(t, c)
	}
}

func TestClockPersistentEngineIsolation(t *testing.T) {
	a, _ := NewClock(3)
	b, _ := NewClock(11)
	if a.driver.engine == b.driver.engine {
		t.Fatal("independent clocks share engine")
	}
	var aEvents, bEvents []uint64
	// Another clock can run even from a callback without replacing the active
	// clock's handler or time. The common handler name is engine-local.
	if err := a.Run(3, func(edge uint64) (bool, error) {
		aEvents = append(aEvents, edge)
		err := b.Run(2, func(other uint64) (bool, error) {
			bEvents = append(bEvents, other)
			if b.driver.engine.CurrentTime() != akita.VTimeInPicoSec(other*11) {
				t.Fatal("other clock time")
			}
			return false, nil
		})
		if a.driver.engine.CurrentTime() != akita.VTimeInPicoSec(edge*3) {
			t.Fatal("active clock time changed")
		}
		return false, err
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aEvents, []uint64{0, 1, 2}) || !reflect.DeepEqual(bEvents, []uint64{0, 1, 2, 3, 4, 5}) {
		t.Fatal(aEvents, bEvents)
	}
	assertClockQuiescent(t, a)
	assertClockQuiescent(t, b)
	// A new execution starts at zero even after another clock has advanced.
	fresh, _ := NewClock(3)
	if fresh.driver.engine == a.driver.engine || fresh.Cycle() != 0 || fresh.driver.engine.CurrentTime() != 0 {
		t.Fatal("new clock inherited event state")
	}
}

func TestClockPersistentEngineOverflow(t *testing.T) {
	if c, err := NewClock(0); err == nil || c != nil {
		t.Fatal("zero period accepted")
	}
	for _, tc := range []struct {
		name   string
		period uint64
		start  uint64
	}{
		{"cycle", 1, math.MaxUint64 - 1},
		{"time", 2, math.MaxUint64 / 2},
		{"maximum-period", math.MaxUint64, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := NewClock(akita.VTimeInPicoSec(tc.period))
			c.cycle = tc.start // Reach the boundary without billions of edges.
			engine := c.driver.engine
			calls := 0
			step := func(edge uint64) (bool, error) {
				calls++
				if edge != tc.start || engine.CurrentTime() != akita.VTimeInPicoSec(tc.start*tc.period) {
					t.Fatal("last representable edge/time changed")
				}
				return true, nil
			}
			// Preflight validates the entire requested budget even if step would
			// stop early. Rejection neither calls step nor advances engine time.
			if err := c.Run(2, step); err == nil || calls != 0 || c.Cycle() != tc.start || engine.CurrentTime() != 0 {
				t.Fatal("overflowing budget not rejected before execution", err)
			}
			assertClockQuiescent(t, c)
			if err := c.Run(1, step); err != nil || calls != 1 || c.Cycle() != tc.start+1 {
				t.Fatal("valid boundary edge rejected", err)
			}
			assertClockQuiescent(t, c)
			if err := c.Run(1, step); err == nil || calls != 1 || c.Cycle() != tc.start+1 {
				t.Fatal("overflow after boundary edge", err)
			}
			if err := c.Run(0, step); err != nil || calls != 1 {
				t.Fatal("zero budget at exhausted clock", err)
			}
			if c.driver.engine != engine {
				t.Fatal("overflow replaced engine")
			}
			assertClockQuiescent(t, c)
		})
	}
}
