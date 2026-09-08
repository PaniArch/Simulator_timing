package model

import (
	"reflect"
	"testing"

	"vortex.local/simulator/timing"
)

func commit(t *testing.T, p Transition) {
	t.Helper()
	if err := CommitEdge(p); err != nil {
		t.Fatal(err)
	}
}
func valid(id uint64) Signal { return Signal{Valid: true, Token: Token{ID: id, Mask: 15}} }

func TestMultiplyGlobalStallAndThroughput(t *testing.T) {
	p, err := NewMultiply()
	if err != nil {
		t.Fatal(err)
	}
	depth, _ := timing.Number("timing_measurements", "tm-imul-result", "value")
	for k := 0; k < depth; k++ {
		e := p.Evaluate(valid(uint64(k+1)), false)
		if !e.Accepted || e.Completed {
			t.Fatal("early result")
		}
		commit(t, e)
	}
	before := p.Stages()
	for k := 0; k < 5; k++ {
		e := p.Evaluate(valid(99), false)
		if e.Accepted || e.Output.Token.ID != 1 {
			t.Fatal("blocked multiply accepted")
		}
		commit(t, e)
	}
	if !reflect.DeepEqual(p.Stages(), before) || p.Occupancy() != p.Capacity() {
		t.Fatal("pipeline advanced under stall")
	}
	for k := 0; k < depth; k++ {
		e := p.Evaluate(valid(uint64(k+10)), true)
		if !e.Accepted || !e.Completed || e.Output.Token.ID != uint64(k+1) {
			t.Fatal("lost II=1/order", e)
		}
		commit(t, e)
	}
	commit(t, p.Flush())
	commit(t, p.Evaluate(valid(7), true))
	for k := 1; k < depth; k++ {
		commit(t, p.Evaluate(Signal{}, true))
	}
	// A tail blocks the entire shift chain despite unused slots in front.
	if p.Occupancy() != 1 || p.Evaluate(valid(8), false).InputReady {
		t.Fatal("treated globally stalled shifts as elastic registers")
	}
}

func TestSerialDividerTimingAndHeldCompletion(t *testing.T) {
	d, err := NewDivider()
	if err != nil {
		t.Fatal(err)
	}
	latency, _ := timing.Number("timing_measurements", "tm-idiv-result", "value")
	commit(t, d.Evaluate(valid(1), true)) // E0
	for k := 1; k < latency; k++ {
		p := d.Evaluate(valid(2), true)
		if p.Accepted || p.Output.Valid {
			t.Fatal("early divide", k)
		}
		commit(t, p)
	}
	for k := 0; k < 4; k++ {
		p := d.Evaluate(valid(2), false)
		if !p.Output.Valid || p.Accepted || d.Occupancy() != 1 {
			t.Fatal("lost loaded context")
		}
		commit(t, p)
	}
	p := d.Evaluate(valid(2), true)
	if p.Accepted || !p.Completed || p.Output.Token.ID != 1 {
		t.Fatal("serial pop/push accepted")
	}
	commit(t, p)
	p = d.Evaluate(valid(2), true)
	if !p.Accepted || p.Output.Valid {
		t.Fatal("divider did not reopen next edge")
	}
	commit(t, p)
}

func TestSTDIncludesSerializerSkidExactlyOnce(t *testing.T) {
	for _, kind := range []string{"fma", "divsqrt", "cvt", "ncp"} {
		t.Run(kind, func(t *testing.T) {
			p, err := NewSTDPath(kind)
			if err != nil {
				t.Fatal(err)
			}
			latency, _ := timing.Number("timing_measurements", p.ID(), "value")
			for k := 0; k <= latency; k++ {
				input := Signal{}
				if k == 0 {
					input = valid(1)
				}
				e := p.Evaluate(input, true)
				if e.Completed != (k == latency) {
					t.Fatalf("wrong latency at E%d, want %d", k, latency)
				}
				commit(t, e)
			}
			for k := 0; k < p.Capacity()+5; k++ {
				commit(t, p.Evaluate(valid(uint64(k+10)), false))
			}
			if p.Occupancy() != p.Capacity() || p.Ready(false) {
				t.Fatal("unbounded STD occupancy")
			}
			old := p.Output(Signal{})
			for k := 0; k < 3; k++ {
				commit(t, p.Evaluate(valid(99), false))
				if p.Output(Signal{}) != old {
					t.Fatal("unstable output")
				}
			}
		})
	}
	if _, err := NewSTDPath("implicit"); err == nil {
		t.Fatal("silently selected backend")
	}
}

func TestCollectorConflictBeforeAcceptanceAndReadEdges(t *testing.T) {
	for _, test := range []struct {
		name    string
		sources [3]uint8
		used    uint8
		passes  int
	}{
		{"distinct", [3]uint8{1, 2, 3}, 7, 1},
		{"same bank", [3]uint8{1, 5, 9}, 7, 3},
		{"same register still arbitrates", [3]uint8{1, 1, 1}, 7, 3},
		{"x0 suppressed f0 read", [3]uint8{0, 32, 4}, 7, 2},
		{"unused", [3]uint8{1, 5, 9}, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := NewCollector()
			if err != nil {
				t.Fatal(err)
			}
			input := valid(1)
			input.Token.Sources = test.sources
			input.Token.Used = test.used
			var masks uint8
			accepted := -1
			for cycle := 0; cycle < test.passes+4; cycle++ {
				s := input
				if accepted >= 0 {
					s = Signal{}
				}
				p, err := c.Evaluate(s, true)
				if err != nil {
					t.Fatal(err)
				}
				if p.Accepted {
					accepted = cycle
				}
				if p.Read.Valid {
					masks |= p.Read.Token.ReadMask
					if cycle == 0 {
						t.Fatal("RAM read before first register")
					}
				}
				if p.Completed && cycle != test.passes-1+3 {
					t.Fatal("wrong OPC transfer edge", cycle)
				}
				if cycle == test.passes-1+3 && !p.Completed {
					t.Fatal("missing OPC result")
				}
				commit(t, p.Transition)
			}
			if accepted != test.passes-1 {
				t.Fatal("collision moved after handshake", accepted)
			}
			want := test.used
			for i, reg := range test.sources {
				if reg == 0 {
					want &^= 1 << i
				}
			}
			if masks != want {
				t.Fatalf("missing bank reads %b want %b", masks, want)
			}
		})
	}
}

func TestCollectorBackpressureAndStablePartialInput(t *testing.T) {
	c, err := NewCollector()
	if err != nil {
		t.Fatal(err)
	}
	in := valid(1)
	in.Token.Sources = [3]uint8{1, 5, 9}
	in.Token.Used = 7
	p, err := c.Evaluate(in, false)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	if p.Accepted || c.Fetched() != 1 {
		t.Fatal("first collision pass")
	}
	bad := in
	bad.Token.ID++
	if _, err := c.Evaluate(bad, false); err == nil {
		t.Fatal("replaced partial instruction")
	}
	commit(t, c.Flush())
	for k := 0; k < 10; k++ {
		p, err := c.Evaluate(valid(uint64(k+1)), false)
		if err != nil {
			t.Fatal(err)
		}
		commit(t, p.Transition)
	}
	if c.Occupancy() != c.Capacity() {
		t.Fatal("OPC failed to fill")
	}
	before := c.Output(Signal{})
	p, err = c.Evaluate(valid(11), false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Accepted || p.Granted != 0 || p.Read.Valid {
		t.Fatal("OPC advanced while full")
	}
	commit(t, p.Transition)
	if c.Output(Signal{}) != before {
		t.Fatal("OPC output not held")
	}
}

func response(t Token, mask uint8) Response {
	return Response{Valid: true, ID: t.ID, Epoch: t.Epoch, Warp: t.Warp, Uop: t.Uop, Mask: mask}
}

func TestWaitPoolPartialOutOfOrderBackpressureAndEpoch(t *testing.T) {
	w, err := NewWaitPool("res-lsu-load-tags")
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k < w.Capacity(); k++ {
		p, err := w.Evaluate(valid(uint64(k+1)), Response{}, true)
		if err != nil || !p.Accepted {
			t.Fatal(err)
		}
		commit(t, p.Transition)
	}
	p, err := w.Evaluate(valid(99), response(valid(2).Token, 3), false)
	if err != nil || p.Accepted || p.Completed || !p.Output.Valid {
		t.Fatal("full/backpressure", err)
	}
	commit(t, p.Transition)
	p, err = w.Evaluate(valid(99), response(valid(2).Token, 3), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Accepted || !p.Completed || p.Output.Token.End {
		t.Fatal("partial response released context")
	}
	commit(t, p.Transition)
	if w.Occupancy() != w.Capacity() {
		t.Fatal("partial freed tag")
	}
	if _, err = w.Evaluate(Signal{}, response(valid(2).Token, 1), true); err == nil {
		t.Fatal("duplicate lanes accepted")
	}
	p, err = w.Evaluate(valid(99), response(valid(2).Token, 12), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Accepted || !p.Output.Token.End {
		t.Fatal("full pool reused released tag same edge")
	}
	commit(t, p.Transition)
	p, err = w.Evaluate(valid(99), response(valid(1).Token, 15), true)
	if err != nil || !p.Accepted || !p.Completed {
		t.Fatal("simultaneous release/admit", err)
	}
	commit(t, p.Transition)
	stale := response(valid(99).Token, 15)
	stale.Epoch++
	if _, err = w.Evaluate(Signal{}, stale, true); err == nil {
		t.Fatal("wrong epoch matched")
	}
	copy := w.Pending()
	copy[0].Remaining = 0
	if w.Pending()[0].Remaining == 0 {
		t.Fatal("pending alias")
	}
	commit(t, w.Flush())
	if _, err = w.Evaluate(Signal{}, response(valid(99).Token, 15), true); err == nil {
		t.Fatal("late cancelled response accepted")
	}
}

func TestFetchContextCannotBeOverwritten(t *testing.T) {
	w, err := NewWaitPool("res-fetch-tags")
	if err != nil {
		t.Fatal(err)
	}
	in := valid(1)
	p, err := w.Evaluate(in, Response{}, true)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	r := response(in.Token, 15)
	r.Word = 0x00100093
	p, err = w.Evaluate(valid(2), r, true)
	if err != nil || p.Accepted || !p.Completed || p.Output.Token.Word != r.Word {
		t.Fatal("fetch context overwrite", err)
	}
	commit(t, p.Transition)
	p, err = w.Evaluate(valid(2), Response{}, true)
	if err != nil || !p.Accepted {
		t.Fatal("fetch tag not released", err)
	}
}

func TestMixedComponentEdgeIsAtomic(t *testing.T) {
	p, _ := NewMultiply()
	d, _ := NewDivider()
	b := mustBuffer(t, "b-commit")
	pm, pd, pb := p.Evaluate(valid(1), false), d.Evaluate(valid(2), false), b.Evaluate(valid(3), false)
	commit(t, pd)
	if err := CommitEdge(pm, pd, pb); err == nil || p.Occupancy() != 0 || b.Occupancy() != 0 {
		t.Fatal("partial mixed edge")
	}
	if err := CommitEdge(pb, pm); err != nil {
		t.Fatal(err)
	}
}
