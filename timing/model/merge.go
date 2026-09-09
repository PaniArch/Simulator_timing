package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// Merge owns its result storage and arbitration cursor. The cursor advances on
// input acceptance, not output consumption. R is VX_rr_arbiter MODEL=1,
// Lowest masked request, then lowest unmasked request on wrap. STICKY=1
// retains the previous accepted winner while it requests, without moving the mask.
type Merge struct {
	spec     timing.ArbiterSpec
	out      *Buffer
	next     int
	previous int
	rev      revision
}
type MergeTransition struct {
	Transition
	Ready    []bool
	Selected int
}

func NewMerge(id string) (*Merge, error) {
	spec, err := timing.Arbiter(id)
	if err != nil {
		return nil, err
	}
	out, err := NewBuffer(id)
	if err != nil {
		return nil, err
	}
	if out.Capacity() == 0 {
		return nil, fmt.Errorf("%s: merge requires registered output", id)
	}
	return &Merge{spec: spec, out: out, previous: -1}, nil
}
func (m *Merge) ID() string     { return m.out.ID() }
func (m *Merge) Capacity() int  { return m.out.Capacity() }
func (m *Merge) Occupancy() int { return m.out.Occupancy() }
func (m *Merge) Output() Signal { return m.out.Output(Signal{}) }
func (m *Merge) Evaluate(inputs []Signal, downstream bool) (MergeTransition, error) {
	if len(inputs) != m.spec.Inputs {
		return MergeTransition{}, fmt.Errorf("%s: input count mismatch", m.ID())
	}
	selected := -1
	for offset := 0; offset < len(inputs); offset++ {
		i := offset
		if m.spec.Policy == "R" {
			i = (m.next + offset) % len(inputs)
		}
		if inputs[i].Valid {
			selected = i
			break
		}
	}
	retained := m.spec.Sticky && m.previous >= 0 && inputs[m.previous].Valid
	if retained {
		selected = m.previous
	}
	input := Signal{}
	if selected >= 0 {
		input = inputs[selected]
	}
	t := m.out.Evaluate(input, downstream)
	ready := make([]bool, len(inputs))
	next, previous := m.next, m.previous
	if selected >= 0 {
		ready[selected] = t.InputReady
	}
	if t.Accepted && m.spec.Policy == "R" {
		if !retained {
			next = (selected + 1) % len(inputs)
		}
		previous = selected
	}
	t.edits = append(t.edits, m.rev.propose(func() { m.next, m.previous = next, previous }))
	return MergeTransition{Transition: t, Ready: ready, Selected: selected}, nil
}
func (m *Merge) Flush() Transition {
	t := m.out.Flush()
	t.edits = append(t.edits, m.rev.propose(func() { m.next, m.previous = 0, -1 }))
	return t
}

// combine preserves the private proposals; all owners are still validated by
// CommitEdge together before any state is installed.
func combine(port Transition, parts ...Transition) Transition {
	for _, p := range parts {
		port.edits = append(port.edits, p.edits...)
	}
	return port
}

func routed(s Signal, path Path) Signal { s.Valid = s.Valid && s.Token.Path == path; return s }

// requireInputs checks the port topology before a composite indexes Ready.
func (m *Merge) requireInputs(n int) error {
	if m.spec.Inputs != n {
		return fmt.Errorf("%s: expected %d response ports, IR has %d", m.ID(), n, m.spec.Inputs)
	}
	return nil
}
