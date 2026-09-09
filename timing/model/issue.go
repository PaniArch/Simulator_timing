package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// Issue preserves the T9 isolated credit/eligibility diagnostic fixture.
// Frontend and Core use Scoreboard; this legacy component is not a scheduler
// and must not be used as the dependency authority for a connected pipeline.
type Issue struct {
	staging, out *Buffer
	ready        bool
	credits      [4]int
	limit        int
	rev          revision
}

func NewIssue() (*Issue, error) {
	limit, err := issueCreditLimit()
	if err != nil {
		return nil, err
	}
	staging, err := NewBuffer("b-scoreboard-staging")
	if err != nil {
		return nil, err
	}
	out, err := NewBuffer("b-scoreboard-out")
	if err != nil {
		return nil, err
	}
	return &Issue{staging: staging, out: out, limit: limit}, nil
}
func (i *Issue) ID() string             { return "n-scoreboard" }
func (i *Issue) Capacity() int          { return i.staging.Capacity() + i.out.Capacity() }
func (i *Issue) Occupancy() int         { return i.staging.Occupancy() + i.out.Occupancy() }
func (i *Issue) Credits() [4]int        { return i.credits }
func (i *Issue) RegisteredReady() bool  { return i.ready }
func (i *Issue) Output(s Signal) Signal { return i.out.Output(s) }
func (i *Issue) Evaluate(input Signal, downstream, eligible bool, releases [4]bool) (Transition, error) {
	if input.Valid && input.Token.Class >= 4 {
		return Transition{}, fmt.Errorf("invalid execution class")
	}
	staged := i.staging.Output(Signal{})
	selected := staged
	selected.Valid = selected.Valid && i.ready
	out := i.out.Evaluate(selected, downstream)
	staging := i.staging.Evaluate(input, out.InputReady && i.ready)
	credits := i.credits
	if out.Accepted {
		credits[staged.Token.Class]++
	}
	for class, release := range releases {
		if release {
			if i.credits[class] == 0 {
				return Transition{}, fmt.Errorf("FU release without old in-flight credit")
			}
			credits[class]--
		}
		if credits[class] < 0 || credits[class] > i.limit {
			return Transition{}, fmt.Errorf("unbalanced FU %d credits", class)
		}
	}
	candidate := staged
	if staging.Accepted {
		candidate = input
	}
	// RTL fu_goingfull uses OLD registered count. The one-entry guard covers
	// this registered suppress lag; same-edge return does not bypass it.
	ready := eligible && i.credits[candidate.Token.Class] < i.limit-1
	t := transfer(input, out.Output, staging.InputReady, downstream)
	t.edits = append(staging.edits, out.edits...)
	t.edits = append(t.edits, i.rev.propose(func() { i.ready, i.credits = ready, credits }))
	return t, nil
}
func (i *Issue) Flush() Transition {
	a, b := i.staging.Flush(), i.out.Flush()
	t := Transition{edits: append(a.edits, b.edits...)}
	t.edits = append(t.edits, i.rev.propose(func() { i.ready = false; i.credits = [4]int{} }))
	return t
}

// issueCreditLimit validates the shared frozen execution class/queue profile.
func issueCreditLimit() (int, error) {
	for i, name := range []string{"ALU", "LSU", "SFU", "FPU"} {
		index, err := timing.Number("config", "cfg-baseline", "values", "execution_class_indices", name)
		if err != nil {
			return 0, err
		}
		if index != i {
			return 0, fmt.Errorf("execution class mapping drift")
		}
	}
	limit, err := timing.Number("resources", "res-credits", "capacity")
	if err != nil {
		return 0, err
	}
	queue, err := timing.Number("resources", "res-dispatch", "capacity")
	if err != nil {
		return 0, err
	}
	if limit < 2 || limit != queue {
		return 0, fmt.Errorf("FU credit/dispatch capacity drift")
	}
	return limit, nil
}
