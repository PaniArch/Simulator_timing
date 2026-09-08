package model

import "testing"

func memToken(id uint64, path Path) Signal {
	s := valid(id)
	s.Token.Class = 1
	s.Token.Path = path
	s.Token.End = true
	return s
}
func memResponse(s Signal, mask uint8) Response {
	return Response{Valid: s.Valid, ID: s.Token.ID, Epoch: s.Token.Epoch, Warp: s.Token.Warp, Uop: s.Token.Uop, Mask: mask}
}
func TestLSUStoreCompletionPrecedesExternalAcceptance(t *testing.T) {
	l, err := NewLSU()
	if err != nil {
		t.Fatal(err)
	}
	in := memToken(1, STORE)
	for edge := 0; edge < 8; edge++ {
		p, err := l.Evaluate(in, Response{}, false, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.Accepted {
			in = Signal{}
		}
		if p.Completed != (edge == 4) {
			t.Fatal("store result latency", edge, p.Completed)
		}
		if p.RequestAccepted {
			t.Fatal("store escaped stalled service")
		}
		commit(t, p.Transition)
	}
	if !l.Request().Valid || l.Drained(true) {
		t.Fatal("lost store request tail")
	}
	p, _ := l.Evaluate(Signal{}, Response{}, true, true)
	commit(t, p.Transition)
	if l.Drained(false) || !l.Drained(true) {
		t.Fatal("provider tail ignored")
	}
}
func TestLSUFenceLockAndPartialResponse(t *testing.T) {
	l, err := NewLSU()
	if err != nil {
		t.Fatal(err)
	}
	in := memToken(1, FENCE)
	var req Signal
	for edge := 0; edge < 3; edge++ {
		p, err := l.Evaluate(in, Response{}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.Accepted {
			in = Signal{}
		}
		if p.RequestAccepted {
			req = p.Request
		}
		commit(t, p.Transition)
	}
	if !req.Valid || !l.FenceLocked() {
		t.Fatal("fence not sent/locked")
	}
	p, err := l.Evaluate(memToken(2, LOAD), memResponse(req, 3), true, true)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p.Transition)
	if !l.FenceLocked() || len(l.Pending()) != 1 {
		t.Fatal("partial fence unlocked")
	}
	for n := 0; n < 4; n++ {
		p, err := l.Evaluate(Signal{}, Response{}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.SliceAccepted || p.RequestAccepted {
			t.Fatal("bypassed fence")
		}
		commit(t, p.Transition)
	}
	p, err = l.Evaluate(Signal{}, memResponse(req, 12), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.SliceAccepted {
		t.Fatal("same-edge unlock bypass")
	}
	commit(t, p.Transition)
	if l.FenceLocked() {
		t.Fatal("final response did not unlock")
	}
	p, err = l.Evaluate(Signal{}, Response{}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.SliceAccepted {
		t.Fatal("did not reopen next edge")
	}
	commit(t, p.Transition)
}
func TestLSUResponseIdentityAndBackpressure(t *testing.T) {
	l, _ := NewLSU()
	in := memToken(1, LOAD)
	p, _ := l.Evaluate(in, Response{}, false, false)
	commit(t, p.Transition)
	p, _ = l.Evaluate(Signal{}, Response{}, false, false)
	commit(t, p.Transition)
	if _, err := l.Evaluate(Signal{}, memResponse(in, 15), false, false); err == nil {
		t.Fatal("response before send accepted")
	}
	p, _ = l.Evaluate(Signal{}, Response{}, true, false)
	commit(t, p.Transition)
	for _, mask := range []uint8{1, 2, 4, 8} {
		p, err := l.Evaluate(Signal{}, memResponse(in, mask), true, false)
		if err != nil {
			t.Fatal(err)
		}
		if !p.ResponseReady {
			t.Fatal("premature response full")
		}
		commit(t, p.Transition)
	}
	if len(l.Pending()) != 0 {
		t.Fatal("tag not released at response acceptance")
	}
	if _, err := l.Evaluate(Signal{}, memResponse(in, 8), true, true); err == nil {
		t.Fatal("duplicate response accepted")
	}
	count := 0
	for n := 0; n < 12; n++ {
		p, err := l.Evaluate(Signal{}, Response{}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.Completed {
			count++
			if p.Output.Token.End != (count == 4) {
				t.Fatal("wrong partial eop")
			}
		}
		commit(t, p.Transition)
	}
	if count != 4 || !l.Drained(true) {
		t.Fatal("partial results lost", count)
	}
}

func TestLSUFullTagsStoreBypassAndRegisteredRelease(t *testing.T) {
	l, err := NewLSU()
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(1); id <= 8; id++ {
		in := memToken(id, LOAD)
		sent := false
		for k := 0; k < 8; k++ {
			p, e := l.Evaluate(in, Response{}, true, true)
			if e != nil {
				t.Fatal(e)
			}
			if p.Accepted {
				in = Signal{}
			}
			commit(t, p.Transition)
			if p.RequestAccepted {
				sent = true
				break
			}
		}
		if !sent {
			t.Fatal("premature full tags", id)
		}
	}
	if len(l.Pending()) != 8 {
		t.Fatal("tag capacity")
	}
	in := memToken(9, STORE)
	done := false
	for k := 0; k < 8; k++ {
		p, e := l.Evaluate(in, Response{}, true, true)
		if e != nil {
			t.Fatal(e)
		}
		if p.Accepted {
			in = Signal{}
		}
		if p.Completed {
			done = true
		}
		commit(t, p.Transition)
	}
	if !done || len(l.Pending()) != 8 {
		t.Fatal("store consumed/blocked by load tags")
	}
	p, _ := l.Evaluate(memToken(10, LOAD), Response{}, true, true)
	commit(t, p.Transition)
	p, err = l.Evaluate(Signal{}, memResponse(memToken(1, LOAD), 15), true, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.SliceAccepted {
		t.Fatal("full pool borrowed same-edge release")
	}
	commit(t, p.Transition)
	p, err = l.Evaluate(Signal{}, Response{}, true, true)
	if err != nil || !p.SliceAccepted {
		t.Fatal("load did not reopen", err)
	}
	commit(t, p.Transition)
}
