package model

import (
	"errors"
	"reflect"
	"testing"
)

var baselineClockSink *Clock

var clockBaselineChunks = []struct {
	name   string
	chunks []uint64
}{
	{"single", []uint64{1}}, {"fixed", []uint64{32}}, {"irregular", []uint64{1, 7, 3, 29}},
}

// Each execute operation is exactly 1024 committed edges; each init operation
// only constructs a Clock. Execute reuses the clock across operations while
// preserving the original Run path.
func BenchmarkClockBaseline(b *testing.B) {
	for _, mode := range clockBaselineChunks {
		for _, phase := range []string{"init", "execute"} {
			b.Run(mode.name+"/"+phase, func(b *testing.B) {
				c, err := NewClock(1)
				if err != nil {
					b.Fatal(err)
				}
				step := func(uint64) (bool, error) { return false, nil }
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if phase == "init" {
						c, err = NewClock(1)
						if err != nil {
							b.Fatal(err)
						}
						continue
					}
					for n, j := uint64(0), 0; n < 1024; j++ {
						budget := min(mode.chunks[j%len(mode.chunks)], 1024-n)
						if err := c.Run(budget, step); err != nil {
							b.Fatal(err)
						}
						n += budget
					}
				}
				baselineClockSink = c
				if phase == "execute" {
					b.ReportMetric(1024, "edges/op")
				}
			})
		}
	}
}

func TestClockBaselineChunkEquivalence(t *testing.T) {
	for _, ending := range []string{"budget", "stop", "error"} {
		var reference []uint64
		for _, mode := range clockBaselineChunks {
			c, _ := NewClock(3)
			var events []uint64
			sentinel := errors.New("edge failure")
			terminal := false
			for j := 0; c.Cycle() < 97 && !terminal; j++ {
				budget := mode.chunks[j%len(mode.chunks)]
				if ending == "budget" {
					budget = min(budget, 97-c.Cycle())
				}
				err := c.Run(budget, func(cycle uint64) (bool, error) {
					events = append(events, cycle)
					if cycle == 96 && ending == "error" {
						return false, sentinel
					}
					return cycle == 96 && ending == "stop", nil
				})
				if err != nil {
					if ending != "error" || !errors.Is(err, sentinel) {
						t.Fatal(err)
					}
					terminal = true
				}
			}
			want := uint64(97)
			if ending == "error" {
				want = 96
			}
			if c.Cycle() != want || len(events) != 97 {
				t.Fatalf("%s/%s cycle=%d events=%d", ending, mode.name, c.Cycle(), len(events))
			}
			if reference == nil {
				reference = events
			} else if !reflect.DeepEqual(reference, events) {
				t.Fatal("event order changed")
			}
			// A new callback must replace the previous callback, including after errors.
			if err := c.Run(1, func(edge uint64) (bool, error) {
				if edge != want {
					t.Fatal(edge, want)
				}
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			if c.Cycle() != want+1 {
				t.Fatal("resume cycle")
			}
		}
	}
}
