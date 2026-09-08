package model

import "testing"

func TestSFURegisteredPathsAndDrain(t *testing.T) {
	for _, path := range []Path{WCTL, CSRPath} {
		s, err := NewSFU()
		if err != nil {
			t.Fatal(err)
		}
		in := valid(1)
		in.Token.Class = 2
		in.Token.Path = path
		in.Token.End = true
		want := 4
		if path == CSRPath {
			want++
		}
		for edge := 0; edge <= want+1; edge++ {
			p, err := s.Evaluate(in, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if p.Completed != (edge == want) {
				t.Fatal("SFU latency", path, edge, p.Completed)
			}
			if p.Control.Valid != (path == WCTL && edge == 2) {
				t.Fatal("control edge", path, edge)
			}
			if p.Accepted {
				in = Signal{}
			}
			commit(t, p.Transition)
		}
	}
	s, _ := NewSFU()
	in := valid(1)
	in.Token.Class = 2
	in.Token.Path = WCTL
	in.Token.End = true
	p, _ := s.Evaluate(in, true, false)
	commit(t, p.Transition)
	for edge := 0; edge < 6; edge++ {
		p, _ := s.Evaluate(Signal{}, true, false)
		if p.Control.Valid || p.Completed {
			t.Fatal("drain bypass")
		}
		commit(t, p.Transition)
	}
	for edge := 0; edge < 5; edge++ {
		p, _ := s.Evaluate(Signal{}, true, true)
		if p.Control.Valid != (edge == 1) {
			t.Fatal("drain release edge")
		}
		commit(t, p.Transition)
	}
}

func TestSTDHeaderLimitReorderAndFlags(t *testing.T) {
	f, err := NewSTDFPU()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id uint64, path Path) Signal {
		s := valid(id)
		s.Token.Path = path
		s.Token.Class = 3
		s.Token.End = true
		return s
	}
	a, b, c := mk(1, DIVSQRT), mk(2, NCP), mk(3, FMA)
	p, err := f.Evaluate(a, true)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	p, err = f.Evaluate(b, true)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	if f.Occupancy() != 2 {
		t.Fatal("tags not allocated")
	}
	completed := []uint64{}
	flags := []uint64{}
	offered := c
	lastResult := uint64(0)
	for edge := 2; edge < 60; edge++ {
		full := f.Occupancy() == f.Capacity()
		// Hold all results long enough for both subcores to finish.
		p, err := f.Evaluate(offered, edge >= 25)
		if err != nil {
			t.Fatal(err)
		}
		if full && p.Accepted {
			t.Fatal("borrowed tag on same-edge release")
		}
		if p.Flags.Valid {
			flags = append(flags, p.Flags.Token.ID)
			if lastResult != p.Flags.Token.ID {
				t.Fatal("flags did not follow response one edge")
			}
		}
		lastResult = 0
		if p.Completed {
			completed = append(completed, p.Output.Token.ID)
			lastResult = p.Output.Token.ID
		}
		if p.Accepted {
			offered = Signal{}
		}
		commit(t, p.Transition)
	}
	if len(completed) != 3 || completed[0] != 2 || completed[1] != 1 || completed[2] != 3 || len(flags) != 3 || f.Occupancy() != 0 {
		t.Fatal("reordered results/tags", completed, flags, f.Pending())
	}
}

func TestSTDComposedLatency(t *testing.T) {
	for _, tc := range []struct {
		path    Path
		latency int
	}{{FMA, 10}, {DIVSQRT, 19}, {CVT, 7}, {NCP, 4}} {
		f, err := NewSTDFPU()
		if err != nil {
			t.Fatal(err)
		}
		in := valid(1)
		in.Token.Path = tc.path
		in.Token.Class = 3
		in.Token.End = true
		for edge := 0; edge <= tc.latency+1; edge++ {
			p, err := f.Evaluate(in, true)
			if err != nil {
				t.Fatal(err)
			}
			if p.Completed != (edge == tc.latency) || p.Flags.Valid != (edge == tc.latency+1) {
				t.Fatal("STD composed latency", tc, edge, p.Completed, p.Flags)
			}
			if p.Accepted {
				in = Signal{}
			}
			commit(t, p.Transition)
		}
	}
}

func TestFPUHeaderCoveragePreservesEOP(t *testing.T) {
	pool, err := NewWaitPool("res-fpu-tags")
	if err != nil {
		t.Fatal(err)
	}
	in := valid(1)
	in.Token.End = false
	p, err := pool.Evaluate(in, Response{}, true)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	r := Response{Valid: true, ID: 1, Mask: 1}
	if _, err := pool.Evaluate(Signal{}, r, true); err == nil {
		t.Fatal("FPU accepted partial header response")
	}
	r.Mask = in.Token.Mask
	p, err = pool.Evaluate(Signal{}, r, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Completed || p.Output.Token.End {
		t.Fatal("tag release changed packet eop")
	}
	commit(t, p.Transition)
	if pool.Occupancy() != 0 {
		t.Fatal("non-eop tag was not released")
	}
}
