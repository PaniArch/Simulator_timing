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

func (a *instructionAccounting) evaluate(issue, commit Signal, activeNext uint8) (Transition, error) {
	next := append([]Token(nil), a.pending...)
	if a.issued.Valid {
		next = append(next, a.issued.Token)
	}
	retired := a.instret
	cycles := a.cycles
	if a.busy {
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
