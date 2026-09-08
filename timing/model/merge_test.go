package model

import "testing"

func TestMergePriorityRotationAndFullHold(t *testing.T) {
	for _, id := range []string{"b-alu-merge", "b-alu-muldiv-merge"} {
		t.Run(id, func(t *testing.T) {
			m, err := NewMerge(id)
			if err != nil {
				t.Fatal(err)
			}
			inputs := []Signal{valid(1), valid(2)}
			for edge := 0; edge < 2; edge++ {
				p, err := m.Evaluate(inputs, false)
				if err != nil {
					t.Fatal(err)
				}
				want := 0
				if id == "b-alu-merge" {
					want = edge
				}
				if p.Selected != want || !p.Accepted || !p.Ready[want] || p.Ready[1-want] {
					t.Fatal("wrong grant", edge, p)
				}
				commit(t, p.Transition)
			}
			head := m.Output()
			for edge := 0; edge < 4; edge++ {
				p, _ := m.Evaluate(inputs, false)
				if p.Accepted || p.Output != head {
					t.Fatal("full merge changed")
				}
				commit(t, p.Transition)
			}
			p, _ := m.Evaluate(inputs, true)
			if p.Accepted || !p.Completed {
				t.Fatal("borrowed full slot")
			}
			commit(t, p.Transition)
			p, _ = m.Evaluate(inputs, true)
			if p.Selected != 0 || !p.Accepted || !p.Completed {
				t.Fatal("rotation advanced on stall/output pop")
			}
			commit(t, p.Transition)
			commit(t, m.Flush())
			p, _ = m.Evaluate(inputs, true)
			if p.Selected != 0 || p.Output.Valid {
				t.Fatal("reset")
			}
		})
	}
}

func TestMergeSparseRoundRobin(t *testing.T) {
	m, err := NewMerge("b-alu-merge")
	if err != nil {
		t.Fatal(err)
	}
	// A sole higher-index request wins after reset, then wraps to the low port.
	p, _ := m.Evaluate([]Signal{{}, valid(2)}, true)
	commit(t, p.Transition)
	p, _ = m.Evaluate([]Signal{valid(1), valid(2)}, true)
	if p.Selected != 0 {
		t.Fatal("round robin did not wrap")
	}
	commit(t, p.Transition)
	p, _ = m.Evaluate([]Signal{valid(1), {}}, true)
	// Full buffers cannot accept, but combinational grant remains the requester.
	if p.Selected != 0 {
		t.Fatal("masked request fallback")
	}
}
