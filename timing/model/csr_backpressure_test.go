package model

import "testing"

func TestCSRRequestWindowHeldUntilResultAcceptance(t *testing.T) {
	csr, err := NewCSR()
	if err != nil {
		t.Fatal(err)
	}
	input := Signal{Valid: true, Token: Token{ID: 1, Path: CSRPath, Class: 2, End: true}}
	accepted, held := 0, 0
	var oldOutput Signal
	for n := 0; n < 40; n++ {
		p := csr.Evaluate(input, false)
		if p.Completed {
			t.Fatal("output escaped backpressure")
		}
		if accepted == csr.Capacity() && p.RequestWindow && !p.Accepted {
			held++
			if oldOutput.Valid && oldOutput != p.Output {
				t.Fatal("held result changed")
			}
			oldOutput = p.Output
		}
		if err := CommitEdge(p.Transition); err != nil {
			t.Fatal(err)
		}
		if p.Accepted {
			accepted++
			input.Token.ID++
		}
	}
	if accepted != csr.Capacity() || held < 3 {
		t.Fatal("did not exercise full result buffer", accepted, held)
	}
	var outputs []uint64
	for n := 0; n < 20; n++ {
		p := csr.Evaluate(input, true)
		if p.Completed {
			outputs = append(outputs, p.Output.Token.ID)
		}
		if err := CommitEdge(p.Transition); err != nil {
			t.Fatal(err)
		}
		if p.Accepted {
			accepted++
			input.Valid = false
		}
	}
	if accepted != csr.Capacity()+1 || len(outputs) != accepted {
		t.Fatal("held request dropped or duplicated", accepted, outputs)
	}
	for i, id := range outputs {
		if id != uint64(i+1) {
			t.Fatal("result identity reordered", outputs)
		}
	}
}
