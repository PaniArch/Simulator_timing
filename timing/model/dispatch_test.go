package model

import "testing"

func TestDispatchIndependentCapacityAndReleases(t *testing.T) {
	d, err := NewDispatch()
	if err != nil {
		t.Fatal(err)
	}
	for class := 0; class < 4; class++ {
		for n := 0; n < 4; n++ {
			in := valid(uint64(class*4 + n + 1))
			in.Token.Class = uint8(class)
			p, err := d.Evaluate(in, [4]bool{})
			if err != nil || !p.Accepted {
				t.Fatal("early full", class, n, err)
			}
			commit(t, p.Transition)
		}
	}
	if d.Occupancy() != d.Capacity() {
		t.Fatal("capacity")
	}
	heads := d.Outputs()
	for class := 0; class < 4; class++ {
		in := valid(99)
		in.Token.Class = uint8(class)
		ready := [4]bool{}
		ready[class] = true
		p, err := d.Evaluate(in, ready)
		if err != nil {
			t.Fatal(err)
		}
		if p.Accepted || !p.Releases[class] || p.Outputs != heads {
			t.Fatal("borrowed slot or changed held output")
		}
		// Probe other classes without installing; proposal must not mutate queues.
	}
	if d.Occupancy() != 16 {
		t.Fatal("evaluation mutated queues")
	}
	p, _ := d.Evaluate(Signal{}, [4]bool{true, true, true, true})
	commit(t, p.Transition)
	in := valid(99)
	in.Token.Class = 2
	p, _ = d.Evaluate(in, [4]bool{true, true, true, true})
	if !p.Accepted || p.Releases != [4]bool{true, true, true, true} {
		t.Fatal("simultaneous release/admit")
	}
	commit(t, p.Transition)
	if d.Occupancies() != [4]int{2, 2, 3, 2} {
		t.Fatal(d.Occupancies())
	}
}
