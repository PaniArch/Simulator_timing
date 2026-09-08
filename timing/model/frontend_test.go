package model

import "testing"

func TestIssueRegisteredEligibilityAndCredits(t *testing.T) {
	i, err := NewIssue()
	if err != nil {
		t.Fatal(err)
	}
	p, err := i.Evaluate(valid(1), true, false, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	for cycle := 0; cycle < 3; cycle++ {
		p, err = i.Evaluate(Signal{}, true, false, [4]bool{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Output.Valid || i.Credits()[0] != 0 {
			t.Fatal("ineligible issue")
		}
		commit(t, p)
	}
	p, err = i.Evaluate(Signal{}, true, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if i.Credits()[0] != 0 {
		t.Fatal("eligibility bypassed ready register")
	}
	p, err = i.Evaluate(Signal{}, true, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if i.Credits()[0] != 1 {
		t.Fatal("staging did not issue after registered ready")
	}
	for k := 0; k < 20; k++ {
		p, err = i.Evaluate(valid(uint64(k+2)), true, true, [4]bool{})
		if err != nil {
			t.Fatal(err)
		}
		commit(t, p)
	}
	if i.Credits()[0] != 4 || i.RegisteredReady() {
		t.Fatal("FU guard failed", i.Credits())
	}
	releases := [4]bool{true}
	// Return enough room to clear almost-full; neither return bypasses the
	// old counter or registered ready on the return edge.
	for k := 0; k < 2; k++ {
		p, err = i.Evaluate(Signal{}, true, true, releases)
		if err != nil {
			t.Fatal(err)
		}
		commit(t, p)
		if i.Credits()[0] != 3-k {
			t.Fatal("return bypassed guard")
		}
	}
	p, err = i.Evaluate(Signal{}, true, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if i.Credits()[0] != 2 || !i.RegisteredReady() {
		t.Fatal("registered guard recovery")
	}
	p, err = i.Evaluate(Signal{}, true, true, [4]bool{})
	if err != nil {
		t.Fatal(err)
	}
	commit(t, p)
	if i.Credits()[0] != 3 {
		t.Fatal("held staging did not resume")
	}
}

func TestPackedBurstHoldsIbufferUntilLastUop(t *testing.T) {
	for pack, count := range map[uint8]int{1: 4, 2: 2} {
		s, err := NewSequencer()
		if err != nil {
			t.Fatal(err)
		}
		in := valid(7)
		in.Token.Pack = pack
		p, err := s.Evaluate(in, true)
		if err != nil {
			t.Fatal(err)
		}
		if p.Accepted || p.Output.Valid {
			t.Fatal("packed start bypassed register")
		}
		commit(t, p)
		for uop := 0; uop < count; uop++ {
			p, err = s.Evaluate(in, false)
			if err != nil {
				t.Fatal(err)
			}
			if p.Accepted || !p.Output.Valid || int(p.Output.Token.Uop) != uop {
				t.Fatal("packed stall advanced")
			}
			commit(t, p)
			p, err = s.Evaluate(in, true)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Completed || p.Accepted != (uop == count-1) || int(p.Output.Token.Uops) != count {
				t.Fatal("wrong burst handshake", p)
			}
			commit(t, p)
		}
		if s.Occupancy() != 0 {
			t.Fatal("burst not released")
		}
		p, err = s.Evaluate(valid(8), true)
		if err != nil || !p.Accepted || !p.Completed {
			t.Fatal("ordinary instruction got extra cycle", err)
		}
	}
}

func TestCSRPresentationWaitAndUnqualifiedRequestWindow(t *testing.T) {
	c, err := NewCSR()
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 4; cycle++ {
		p := c.Evaluate(valid(uint64(cycle/2+1)), false)
		if p.Accepted != (cycle%2 == 1) {
			t.Fatal("CSR admission interval", cycle)
		}
		commit(t, p.Transition)
	}
	if c.Occupancy() != 2 {
		t.Fatal("CSR result capacity")
	}
	p := c.Evaluate(valid(3), false)
	commit(t, p.Transition)
	for k := 0; k < 3; k++ {
		p = c.Evaluate(valid(3), false)
		if !p.RequestWindow || p.Accepted || !p.Output.Valid || p.Output.Token.ID != 1 {
			t.Fatal("lost CSR stall observation")
		}
		commit(t, p.Transition)
	}
	p = c.Evaluate(valid(3), true)
	if p.Accepted || !p.Completed {
		t.Fatal("full CSR result took same-edge slot")
	}
	commit(t, p.Transition)
	p = c.Evaluate(valid(3), true)
	if !p.Accepted {
		t.Fatal("CSR context wait restarted under stall")
	}
	commit(t, p.Transition)
}

func TestCommitPriorityAckFreeWBAndRegisteredPending(t *testing.T) {
	c, err := NewCommit()
	if err != nil {
		t.Fatal(err)
	}
	inputs := [4]Signal{valid(1), valid(2), valid(3), valid(4)}
	for k := 0; k < 4; k++ {
		inputs[k].Token.End = true
	}
	for cycle := 0; cycle < 6; cycle++ {
		p := c.Evaluate(inputs)
		if cycle < 4 {
			for j := 0; j < 4; j++ {
				if p.Ready[j] != (j == cycle) {
					t.Fatal("wrong commit priority")
				}
			}
			inputs[cycle] = Signal{}
		}
		if p.Writeback.Valid != (cycle >= 1 && cycle <= 4) || p.PendingRelease.Valid != (cycle >= 2) {
			t.Fatal("WB/feedback boundary", cycle)
		}
		if p.Writeback.Valid && p.Writeback.Token.ID != uint64(cycle) {
			t.Fatal("WB identity")
		}
		if p.PendingRelease.Valid && p.PendingRelease.Token.ID != uint64(cycle-1) {
			t.Fatal("pending identity")
		}
		commit(t, p.Transition)
	}
	// WB without eop remains visible, but does not decrement pending.
	in := valid(9)
	in.Token.End = false
	commit(t, c.Evaluate([4]Signal{in}).Transition)
	p := c.Evaluate([4]Signal{})
	if !p.Writeback.Valid {
		t.Fatal("suppressed non-eop WB")
	}
	commit(t, p.Transition)
	p = c.Evaluate([4]Signal{})
	if p.PendingRelease.Valid {
		t.Fatal("non-eop pending release")
	}
}
