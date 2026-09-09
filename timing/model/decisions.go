package model

// IssueCandidate is a detached old-edge staging/eligibility explanation.
// Eligibility is the registered bit; the other fields explain current inputs
// to its next-state equation, not an unregistered bypass into arbitration.
type IssueCandidate struct {
	Staged                                                       Signal
	Eligible                                                     bool
	RegisterHazard, SpecialHazard, CreditBlocked, SequenceLocked bool
}

func (c *Core) IssueCandidates() (result [4]IssueCandidate) {
	s := c.front.issue
	for w, b := range s.staging {
		staged := b.Output(Signal{})
		t := staged.Token
		result[w] = IssueCandidate{Staged: staged, Eligible: s.ready[w], RegisterHazard: staged.Valid && s.busy[w]&dependencyMask(t) != 0, SpecialHazard: staged.Valid && s.special[w]&(t.ReadSpecial|t.WriteSpecial) != 0, CreditBlocked: staged.Valid && s.credits[t.Class] >= s.limit-1, SequenceLocked: staged.Valid && s.locked[t.Class] && t.FULock}
	}
	return
}
