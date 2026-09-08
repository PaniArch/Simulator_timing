package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// Commit merges the four frozen execution classes with P priority into the
// registered commit output. Architectural writeback has no ready input.
type Commit struct {
	out      *Buffer
	feedback Signal
	rev      revision
}
type CommitTransition struct {
	Transition
	Ready          [4]bool
	Writeback      Signal
	PendingRelease Signal
}

func NewCommit() (*Commit, error) {
	latency, err := timing.Number("timing_measurements", "tm-wb-pending", "value")
	if err != nil {
		return nil, err
	}
	if latency != 1 {
		return nil, fmt.Errorf("commit feedback register drift")
	}
	out, err := NewBuffer("b-commit")
	if err != nil {
		return nil, err
	}
	return &Commit{out: out}, nil
}
func (c *Commit) ID() string     { return "n-commit" }
func (c *Commit) Capacity() int  { return c.out.Capacity() }
func (c *Commit) Occupancy() int { return c.out.Occupancy() }
func (c *Commit) Output() Signal { return c.out.Output(Signal{}) }
func (c *Commit) Evaluate(inputs [4]Signal) CommitTransition {
	selected := -1
	for i, s := range inputs {
		if s.Valid {
			selected = i
			break
		}
	}
	input := Signal{}
	if selected >= 0 {
		input = inputs[selected]
	}
	t := c.out.Evaluate(input, true)
	var ready [4]bool
	if selected >= 0 {
		ready[selected] = t.InputReady
	}
	feedback := t.Output
	feedback.Valid = feedback.Valid && feedback.Token.End
	t.edits = append(t.edits, c.rev.propose(func() { c.feedback = feedback }))
	return CommitTransition{Transition: t, Ready: ready, Writeback: t.Output, PendingRelease: c.feedback}
}
func (c *Commit) Flush() Transition {
	t := c.out.Flush()
	t.edits = append(t.edits, c.rev.propose(func() { c.feedback = Signal{} }))
	return t
}
