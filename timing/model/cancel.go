package model

import "fmt"

// Cancellation is a bounded software invalidation range. Through is captured
// at recovery time, so later admissions in the same epoch are never tombstoned.
// It is not an RTL squash signal and cannot undo already visible effects.
type Cancellation struct {
	Warp                  uint8
	Epoch, After, Through uint64
}

func (c Cancellation) Validate() error {
	if c.Warp >= 4 || c.Through <= c.After {
		return fmt.Errorf("invalid cancellation range")
	}
	return nil
}
func (c Cancellation) Matches(t Token) bool {
	return t.Warp == c.Warp && t.Epoch == c.Epoch && t.ID > c.After && t.ID <= c.Through
}
func tokenIdentity(a, b Token) bool {
	return a.Warp == b.Warp && a.Epoch == b.Epoch && a.ID == b.ID && a.Uop == b.Uop
}
func filterTokens(tokens []Token, c Cancellation) []Token {
	var next []Token
	for _, t := range tokens {
		if !c.Matches(t) {
			next = append(next, t)
		}
	}
	return next
}
func cancelSignal(s Signal, c Cancellation) Signal {
	if s.Valid && c.Matches(s.Token) {
		return Signal{}
	}
	return s
}
func (b *Buffer) cancel(c Cancellation) Transition {
	next := filterTokens(b.queue, c)
	return Transition{edits: []mutation{b.rev.propose(func() { b.queue = next })}}
}
func (p *Pipeline) cancel(c Cancellation) Transition {
	next := p.Stages()
	for i, s := range next {
		next[i] = cancelSignal(s, c)
	}
	return Transition{edits: []mutation{p.rev.propose(func() { p.stages = next })}}
}
func (w *WaitPool) cancel(c Cancellation) Transition {
	var next []Pending
	for _, p := range w.entries {
		if !c.Matches(p.Token) {
			next = append(next, p)
		}
	}
	return Transition{edits: []mutation{w.rev.propose(func() { w.entries = next })}}
}
func (m *Merge) cancel(c Cancellation) Transition { return m.out.cancel(c) } // preserve unrelated arbitration history
func (d *Divider) cancel(c Cancellation) Transition {
	if d.loaded.Valid && c.Matches(d.loaded.Token) {
		return d.Flush()
	}
	return Transition{}
}
func (p *STDPath) cancel(c Cancellation) Transition {
	return combine(Transition{}, p.pipe.cancel(c), p.out.cancel(c))
}
func (f *Fetch) cancel(c Cancellation) Transition {
	next := filterTokens(f.issued, c)
	t := combine(Transition{}, f.request.cancel(c), f.iflush.cancel(c), f.tags.cancel(c))
	t.edits = append(t.edits, f.rev.propose(func() { f.issued = next }))
	return t
}
func (s *Scoreboard) cancel(c Cancellation) Transition {
	reservations := filterTokens(s.reservations, c)
	busy, special := [4]uint64{}, [4]uint8{}
	for _, t := range reservations {
		if t.Writeback {
			busy[t.Warp] |= uint64(1) << t.Destination
		}
		special[t.Warp] |= t.WriteSpecial
	}
	var credits [4]int
	var creditTokens [4][]Token
	locked, lockOwner, ready := s.locked, s.lockOwner, s.ready
	for class := range credits {
		creditTokens[class] = filterTokens(s.creditTokens[class], c)
		credits[class] = len(creditTokens[class])
		if c.Matches(lockOwner[class]) {
			locked[class] = false
			lockOwner[class] = Token{}
		}
	}
	ready[c.Warp] = false // re-register eligibility on the next Evaluate
	t := s.out.cancel(c)
	for _, b := range s.staging {
		t = combine(t, b.cancel(c))
	}
	t.edits = append(t.edits, s.rev.propose(func() {
		s.reservations = reservations
		s.busy = busy
		s.special = special
		s.creditTokens = creditTokens
		s.credits = credits
		s.locked = locked
		s.lockOwner = lockOwner
		s.ready = ready
	}))
	return t
}
func (f *Frontend) cancel(c Cancellation) Transition {
	t := combine(Transition{}, f.schedule.cancel(c), f.fetch.cancel(c), f.issue.cancel(c), f.opc.first.cancel(c), f.opc.second.cancel(c), f.opc.out.cancel(c))
	if c.Matches(f.opc.held) {
		t.edits = append(t.edits, f.opc.rev.propose(func() { f.opc.held = Token{}; f.opc.fetched = 0 }))
	}
	for w := range f.ibuf {
		t = combine(t, f.ibuf[w].cancel(c))
		if f.seq[w].active.Valid && c.Matches(f.seq[w].active.Token) {
			t = combine(t, f.seq[w].Flush())
		}
	}
	for _, q := range f.dispatch.queues {
		t = combine(t, q.cancel(c))
	}
	if s := f.scheduler; s != nil {
		next := s.state
		// Each not-yet-popped macro appears in exactly one of schedule, fetch
		// context or IBuffer (aliases deduplicated). Packed active aliases IBuffer.
		ids := map[uint64]bool{}
		count := func(tok Token) {
			if c.Matches(tok) {
				ids[tok.ID] = true
			}
		}
		for _, tok := range f.schedule.queue {
			count(tok)
		}
		for _, p := range f.fetch.tags.entries {
			count(p.Token)
		}
		for _, tok := range f.ibuf[c.Warp].queue {
			count(tok)
		}
		next.IBufferCount[c.Warp] = (next.IBufferCount[c.Warp] - uint8(len(ids))) & 7
		next.IBufferFull[c.Warp] = next.IBufferCount[c.Warp] == 4
		next.AllIBuffersFull = true
		for _, full := range next.IBufferFull {
			next.AllIBuffersFull = next.AllIBuffersFull && full
		}
		next.DecodeUnlock = cancelSignal(next.DecodeUnlock, c)
		// Recovery owner supplies a future restart context; cancellation itself
		// parks this warp without overwriting its canonical or frontend PC/mask.
		next.Warps[c.Warp].Stalled = true
		t.edits = append(t.edits, s.rev.propose(func() { s.state = next }))
	}
	return t
}
func (a *ALU) cancel(c Cancellation) Transition {
	t := combine(Transition{}, a.integer.cancel(c), a.mul.cancel(c), a.div.cancel(c), a.md.cancel(c), a.result.cancel(c))
	branch := cancelSignal(a.branch, c)
	t.edits = append(t.edits, a.rev.propose(func() { a.branch = branch }))
	return t
}
func (l *LSU) cancel(c Cancellation) Transition {
	t := combine(Transition{}, l.dispatch.cancel(c), l.request.cancel(c), l.load.cancel(c), l.store.cancel(c), l.gather.cancel(c), l.result.cancel(c), l.tags.cancel(c))
	sent := filterTokens(l.sent, c)
	fence := l.fence
	for _, p := range l.tags.entries {
		if p.Token.Path == FENCE && c.Matches(p.Token) {
			fence = false
		}
	}
	t.edits = append(t.edits, l.rev.propose(func() { l.sent = sent; l.fence = fence }))
	return t
}
func (s *SFU) cancel(c Cancellation) Transition {
	t := combine(Transition{}, s.dispatch.cancel(c), s.wctl.cancel(c), s.gather.cancel(c), s.csr.out.cancel(c), s.result.cancel(c))
	control := cancelSignal(s.control, c)
	t.edits = append(t.edits, s.rev.propose(func() { s.control = control }))
	if head := s.dispatch.Output(Signal{}); head.Valid && head.Token.Path == CSRPath && c.Matches(head.Token) {
		t.edits = append(t.edits, s.csr.rev.propose(func() { s.csr.wait = 0 }))
	}
	return t
}
func (f *STDFPU) cancel(c Cancellation) Transition {
	t := combine(Transition{}, f.result.cancel(c), f.tags.cancel(c))
	for _, p := range f.paths {
		t = combine(t, p.cancel(c))
	}
	flags := cancelSignal(f.flags, c)
	t.edits = append(t.edits, f.rev.propose(func() { f.flags = flags }))
	return t
}

// Cancel is an explicit recovery boundary, separate from a normal Evaluate.
// The caller cancels matching services and effects at the same boundary. Any
// previously prepared Evaluate proposal becomes stale after CommitEdge.
func (c *Core) Cancel(scope Cancellation) (Transition, error) {
	if err := scope.Validate(); err != nil {
		return Transition{}, err
	}
	if s := c.front.scheduler; s != nil && s.state.Warps[scope.Warp].Epoch != scope.Epoch {
		return Transition{}, fmt.Errorf("cancellation epoch does not own scheduled warp")
	}
	t := combine(Transition{}, c.front.cancel(scope), c.alu.cancel(scope), c.lsu.cancel(scope), c.sfu.cancel(scope), c.fpu.cancel(scope), c.commit.out.cancel(scope))
	feedback := cancelSignal(c.commit.feedback, scope)
	t.edits = append(t.edits, c.commit.rev.propose(func() { c.commit.feedback = feedback }))
	return t, nil
}
