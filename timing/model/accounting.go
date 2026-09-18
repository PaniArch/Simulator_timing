package model

import "fmt"

// instructionAccounting tracks issue_sched_if and commit_sched_if, independently
// of functional receipts and memory service tails. Each packed uop is an issue.
// The token ledger supports software cancellation; RTL uses per-warp counters.
type instructionAccounting struct {
	issued  Signal
	pending []Token
	instret uint64
	cycles  uint64
	busy    bool
	rev     revision
}

func (a *instructionAccounting) evaluate(issue, commit Signal, activeNext uint8, dispatchBusy bool) (Transition, error) {
	next := append([]Token(nil), a.pending...)
	if a.issued.Valid {
		next = append(next, a.issued.Token)
	}
	retired := a.instret
	cycles := a.cycles
	// VX_scheduler.busy ORs the registered busy_buf with the CTA dispatcher's
	// current DISPATCH/kmu_fire signal. Do not register that second input again.
	if a.busy || dispatchBusy {
		cycles = (cycles + 1) & ((1 << 44) - 1)
	}
	// VX_scheduler busy_buf samples next active mask and OLD pending empty.
	busy := activeNext != 0 || len(a.pending) != 0
	if commit.Valid {
		index := -1
		for i, token := range next {
			if tokenIdentity(token, commit.Token) {
				index = i
				break
			}
		}
		if index < 0 {
			return Transition{}, fmt.Errorf("commit notification without hardware pending issue")
		}
		next = append(next[:index], next[index+1:]...)
		retired = (retired + 1) & ((1 << 44) - 1)
	}
	return Transition{edits: []mutation{a.rev.propose(func() {
		a.issued, a.pending, a.instret = issue, next, retired
		a.cycles, a.busy = cycles, busy
	})}}, nil
}

func (a *instructionAccounting) cancel(scope Cancellation) Transition {
	next, issue := filterTokens(a.pending, scope), cancelSignal(a.issued, scope)
	return Transition{edits: []mutation{a.rev.propose(func() { a.pending, a.issued = next, issue })}}
}

func (a *instructionAccounting) flush() Transition {
	return Transition{edits: []mutation{a.rev.propose(func() { a.pending, a.issued = nil, Signal{} })}}
}

// HardwarePending is the old-edge registered scheduler count. It excludes
// frontend work and effect delivery. External service tails do not extend a
// committed store's pending lifetime.
func (c *Core) HardwarePending() (counts [4]int) {
	for _, token := range c.account.pending {
		counts[token.Warp]++
	}
	return
}

// Instret counts registered EOP commit notifications, including packed uops.
func (c *Core) Instret() uint64 { return c.account.instret }

// LSUSchedulerDrained represents VX_mem_scheduler.req_queue_empty at the
// abstract vector request boundary. It includes load response tags, but
// excludes result queues and external store tails.
func (c *Core) LSUSchedulerDrained() bool {
	return c.lsu.request.Occupancy() == 0 && c.lsu.tags.Occupancy() == 0
}

// Cycles is the scheduler busy-qualified counter, not the runner wall clock.
func (c *Core) Cycles() uint64 { return c.account.cycles }

// SchedulerBusy exposes busy_buf after the edge; the caller adds the current
// CTA-dispatch busy signal and the other VX_core.busy contributors separately.
func (c *Core) SchedulerBusy() bool { return c.account.busy }

// ActiveWarps reads the registered scheduler mask. Explicit-token mode has no
// autonomous warp residency and returns zero.
func (c *Core) ActiveWarps() (mask uint8) {
	if c.front.scheduler != nil {
		for w, context := range c.front.scheduler.state.Warps {
			if context.Active {
				mask |= 1 << w
			}
		}
	}
	return
}

// ContinueCounters transfers device counters into a fresh launch pipeline.
// It does not copy pending instructions or reset the memory hierarchy.
func (c *Core) ContinueCounters(previous *Core) error {
	if c.account.cycles != 0 || c.account.instret != 0 || c.account.busy || c.account.issued.Valid || len(c.account.pending) != 0 {
		return fmt.Errorf("counter continuation requires fresh core")
	}
	if previous == nil || previous.account.issued.Valid || len(previous.account.pending) != 0 {
		return fmt.Errorf("counter continuation requires drained previous core")
	}
	c.account.cycles, c.account.instret = previous.account.cycles, previous.account.instret
	c.account.busy = previous.account.busy
	return nil
}

// ClockIdleCounters clocks the scheduler counter registers on an explicit
// memory-only device edge. The final registered busy bit may count this edge;
// subsequent idle flush edges do not count. No instruction can be discarded.
func (c *Core) ClockIdleCounters() error {
	if c.account.issued.Valid || len(c.account.pending) != 0 || c.ActiveWarps() != 0 {
		return fmt.Errorf("idle counter edge requires drained inactive core")
	}
	p, err := c.account.evaluate(Signal{}, Signal{}, 0, false)
	if err != nil {
		return err
	}
	return CommitEdge(p)
}
