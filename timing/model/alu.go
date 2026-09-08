package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// ALU is the full-width RV32 non-DPI execution topology. Its lane boundaries
// are verified direct wires; INT and MULDIV have distinct physical queues.
// Branch carries timing identity only; the functional adapter owns decisions.
type ALU struct {
	integer    *Buffer
	mul        *Pipeline
	div        *Divider
	md, result *Merge
	branch     Signal
	rev        revision
}
type ALUTransition struct {
	Transition
	Branch        Signal
	IntegerResult Signal
}

func NewALU() (*ALU, error) {
	for _, id := range []string{"b-alu-dispatch", "b-alu-gather"} {
		b, err := timing.Buffer(id)
		if err != nil {
			return nil, err
		}
		if b.Size != 0 {
			return nil, fmt.Errorf("%s: unsupported non-fullwidth ALU", id)
		}
	}
	delay, err := timing.Number("timing_measurements", "tm-branch-owner", "value")
	if err != nil {
		return nil, err
	}
	if delay != 1 {
		return nil, fmt.Errorf("branch register drift")
	}
	a := &ALU{}
	if a.integer, err = NewBuffer("b-alu-int-result"); err != nil {
		return nil, err
	}
	if a.mul, err = NewMultiply(); err != nil {
		return nil, err
	}
	if a.div, err = NewDivider(); err != nil {
		return nil, err
	}
	if a.md, err = NewMerge("b-alu-muldiv-merge"); err != nil {
		return nil, err
	}
	if a.result, err = NewMerge("b-alu-merge"); err != nil {
		return nil, err
	}
	if err = a.md.requireInputs(2); err != nil {
		return nil, err
	}
	if err = a.result.requireInputs(2); err != nil {
		return nil, err
	}
	return a, nil
}
func (a *ALU) ID() string { return "n-alu" }
func (a *ALU) Capacity() int {
	return a.integer.Capacity() + a.mul.Capacity() + a.div.Capacity() + a.md.Capacity() + a.result.Capacity()
}
func (a *ALU) Occupancy() int {
	return a.integer.Occupancy() + a.mul.Occupancy() + a.div.Occupancy() + a.md.Occupancy() + a.result.Occupancy()
}
func (a *ALU) Output() Signal { return a.result.Output() }
func (a *ALU) Evaluate(input Signal, downstream bool) (ALUTransition, error) {
	if input.Valid && (input.Token.Class != 0 || (input.Token.Path != INT && input.Token.Path != MUL && input.Token.Path != DIV)) {
		return ALUTransition{}, fmt.Errorf("invalid ALU route")
	}
	result, err := a.result.Evaluate([]Signal{a.integer.Output(Signal{}), a.md.Output()}, downstream)
	if err != nil {
		return ALUTransition{}, err
	}
	md, err := a.md.Evaluate([]Signal{a.mul.Output(Signal{}), a.div.Output(Signal{})}, result.Ready[1])
	if err != nil {
		return ALUTransition{}, err
	}
	integer := a.integer.Evaluate(routed(input, INT), result.Ready[0])
	mul := a.mul.Evaluate(routed(input, MUL), md.Ready[0])
	div := a.div.Evaluate(routed(input, DIV), md.Ready[1])
	ready := false
	switch input.Token.Path {
	case INT:
		ready = integer.InputReady
	case MUL:
		ready = mul.InputReady
	case DIV:
		ready = div.InputReady
	}
	branch := integer.Output
	branch.Valid = integer.Completed && branch.Token.Branch && branch.Token.End
	t := combine(transfer(input, result.Output, ready, downstream), integer, mul, div, md.Transition, result.Transition)
	t.edits = append(t.edits, a.rev.propose(func() { a.branch = branch }))
	observed := integer.Output
	observed.Valid = integer.Completed
	return ALUTransition{Transition: t, Branch: a.branch, IntegerResult: observed}, nil
}
func (a *ALU) Flush() Transition {
	t := combine(Transition{}, a.integer.Flush(), a.mul.Flush(), a.div.Flush(), a.md.Flush(), a.result.Flush())
	t.edits = append(t.edits, a.rev.propose(func() { a.branch = Signal{} }))
	return t
}
