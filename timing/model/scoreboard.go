package model

import "fmt"

// Scoreboard owns the four staging registers and the shared issue skid. It
// implements VX_scoreboard; functional register values stay with their owners.
// See cc-reserve-release, cc-fu-credit-lock and cc-issue-arbitration.
type Scoreboard struct {
	reservations []Token // software identity ledger, not additional RTL capacity
	creditTokens [4][]Token
	lockOwner    [4]Token
	staging      [4]*Buffer
	out          *Merge
	busy         [4]uint64
	special      [4]uint8
	ready        [4]bool
	credits      [4]int
	locked       [4]bool
	limit        int
	rev          revision
}

type ScoreboardTransition struct {
	Transition
	Ready, AcceptedWarp [4]bool
	// Issued is staging OUTPUT acceptance (reserve), not skid output into OPC.
	Issued   Signal
	Selected int
}

type ScoreboardState struct {
	Busy     [4]uint64
	Special  [4]uint8
	Eligible [4]bool
	Credits  [4]int
	Locked   [4]bool
}

func NewScoreboard() (*Scoreboard, error) {
	limit, err := issueCreditLimit()
	if err != nil {
		return nil, err
	}
	s := &Scoreboard{limit: limit}
	s.out, err = NewMerge("b-scoreboard-out")
	if err != nil {
		return nil, err
	}
	if s.out.spec.Inputs != 4 || s.out.spec.Policy != "R" || !s.out.spec.Sticky {
		return nil, fmt.Errorf("unsupported four-warp issue arbitration")
	}
	for w := range s.staging {
		s.staging[w], err = NewBuffer("b-scoreboard-staging")
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}
func (s *Scoreboard) Output() Signal { return s.out.Output() }
func (s *Scoreboard) State() ScoreboardState {
	return ScoreboardState{s.busy, s.special, s.ready, s.credits, s.locked}
}
func (s *Scoreboard) Resources() []ResourceState {
	r := make([]ResourceState, 0, 5)
	for w, b := range s.staging {
		o := observed(b, b.Output(Signal{}))
		o.ID += fmt.Sprintf("/warp%d", w)
		r = append(r, o)
	}
	return append(r, observed(s.out, s.out.Output()))
}

func validateDependencies(t Token) error {
	if t.Warp >= 4 || t.Class >= 4 || t.Destination >= 64 || t.Used & ^uint8(7) != 0 || (t.ReadSpecial|t.WriteSpecial) & ^uint8(3) != 0 {
		return fmt.Errorf("invalid scoreboard token metadata")
	}
	if t.Writeback && t.Destination == 0 {
		return fmt.Errorf("integer zero writeback must be suppressed by decode")
	}
	for _, r := range t.Sources {
		if r >= 64 {
			return fmt.Errorf("invalid scoreboard source")
		}
	}
	return nil
}
func dependencyMask(t Token) uint64 {
	var mask uint64
	if t.Writeback {
		mask |= uint64(1) << t.Destination
	}
	for n, r := range t.Sources {
		if t.Used&(1<<n) != 0 && r != 0 {
			mask |= uint64(1) << r
		}
	}
	return mask
}

// Evaluate consumes old staging eligibility. Downstream is Collector input
// ready; releases are Dispatch OUTPUT handshakes. Only End writeback releases
// reservations, including for individual packed uops sharing a destination.
func (s *Scoreboard) Evaluate(inputs [4]Signal, writeback Signal, downstream bool, releases [4]bool) (ScoreboardTransition, error) {
	return s.evaluate(inputs, writeback, downstream, releases, true)
}

// enabled preserves the explicit-token diagnostic admission gate. Scheduled
// cores always enable this equation; actual eligibility comes from RTL state.
func (s *Scoreboard) evaluate(inputs [4]Signal, writeback Signal, downstream bool, releases [4]bool, enabled bool) (ScoreboardTransition, error) {
	for w, in := range inputs {
		if in.Valid {
			if err := validateDependencies(in.Token); err != nil {
				return ScoreboardTransition{}, err
			}
			if int(in.Token.Warp) != w {
				return ScoreboardTransition{}, fmt.Errorf("scoreboard input warp/port mismatch")
			}
		}
	}
	if writeback.Valid {
		if err := validateDependencies(writeback.Token); err != nil {
			return ScoreboardTransition{}, err
		}
	}
	requests := make([]Signal, 4)
	for w, b := range s.staging {
		requests[w] = b.Output(Signal{})
		requests[w].Valid = requests[w].Valid && s.ready[w]
	}
	out, err := s.out.Evaluate(requests, downstream)
	if err != nil {
		return ScoreboardTransition{}, err
	}
	reservations := append([]Token(nil), s.reservations...)
	var creditTokens [4][]Token
	for c := range creditTokens {
		creditTokens[c] = append([]Token(nil), s.creditTokens[c]...)
	}
	lockOwner := s.lockOwner
	busy, special, credits, locked := s.busy, s.special, s.credits, s.locked
	if writeback.Valid && writeback.Token.End {
		t := writeback.Token
		for n, old := range reservations {
			if tokenIdentity(old, t) {
				reservations = append(reservations[:n], reservations[n+1:]...)
				break
			}
		}
		if t.Writeback {
			bit := uint64(1) << t.Destination
			if busy[t.Warp]&bit == 0 {
				return ScoreboardTransition{}, fmt.Errorf("writeback without reserved destination")
			}
			busy[t.Warp] &^= bit
		}
		if special[t.Warp]&t.WriteSpecial != t.WriteSpecial {
			return ScoreboardTransition{}, fmt.Errorf("writeback without reserved special state")
		}
		special[t.Warp] &^= t.WriteSpecial
	}
	issued := Signal{}
	if out.Accepted {
		issued = requests[out.Selected]
		t := issued.Token
		reservations = append(reservations, t)
		creditTokens[t.Class] = append(creditTokens[t.Class], t)
		if t.Writeback {
			busy[t.Warp] |= uint64(1) << t.Destination
		}
		special[t.Warp] |= t.WriteSpecial
		credits[t.Class]++
		if t.FULock != t.FUUnlock {
			locked[t.Class] = t.FULock
			lockOwner[t.Class] = t
			if !t.FULock {
				lockOwner[t.Class] = Token{}
			}
		}
	}
	for class, release := range releases {
		if release {
			if s.credits[class] == 0 {
				return ScoreboardTransition{}, fmt.Errorf("FU release without old in-flight credit")
			}
			credits[class]--
			if len(creditTokens[class]) != 0 {
				creditTokens[class] = creditTokens[class][1:]
			}
		}
		if credits[class] < 0 || credits[class] > s.limit {
			return ScoreboardTransition{}, fmt.Errorf("unbalanced FU %d credits", class)
		}
	}
	result := ScoreboardTransition{Transition: out.Transition, Issued: issued, Selected: out.Selected}
	var ready [4]bool
	for w, b := range s.staging {
		staged := b.Output(Signal{})
		p := b.Evaluate(inputs[w], out.Ready[w] && s.ready[w])
		result.Ready[w], result.AcceptedWarp[w] = p.InputReady, p.Accepted
		result.edits = append(result.edits, p.edits...)
		candidate := staged.Token
		if p.Accepted {
			candidate = inputs[w].Token
		}
		// NEW reservations/locks but OLD credits: exactly the RTL next-state
		// equation. Never borrow a same-edge credit return for eligibility.
		ready[w] = enabled && busy[w]&dependencyMask(candidate) == 0 && special[w]&(candidate.ReadSpecial|candidate.WriteSpecial) == 0 && s.credits[candidate.Class] < s.limit-1 && !(locked[candidate.Class] && candidate.FULock)
	}
	result.edits = append(result.edits, s.rev.propose(func() {
		s.reservations, s.creditTokens, s.lockOwner = reservations, creditTokens, lockOwner
		s.busy, s.special, s.credits, s.locked, s.ready = busy, special, credits, locked, ready
	}))
	return result, nil
}
func (s *Scoreboard) Flush() Transition {
	t := s.out.Flush()
	for _, b := range s.staging {
		t = combine(t, b.Flush())
	}
	t.edits = append(t.edits, s.rev.propose(func() {
		s.reservations, s.creditTokens, s.lockOwner = nil, [4][]Token{}, [4]Token{}
		s.busy, s.special, s.ready, s.credits, s.locked = [4]uint64{}, [4]uint8{}, [4]bool{}, [4]int{}, [4]bool{}
	}))
	return t
}
