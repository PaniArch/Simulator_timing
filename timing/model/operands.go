package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// Collector models bank passes while the input is presented, then the two
// registered metadata/RAM stages and separate output skid. It owns no RF data.
// Read events identify the physical read edge for a future functional adapter.
type Collector struct {
	banks              int
	first, second, out *Buffer
	fetched            uint8
	held               Token
	rev                revision
}

type OperandTransition struct {
	Transition
	// Read.Valid indicates pipe_reg1 fire/RAM read; Token.ReadMask lists
	// granted sources for this pass, including nonfinal collision passes.
	Read    Signal
	Granted uint8
}

func NewCollector() (*Collector, error) {
	banks, err := timing.Number("resources", "res-register-banks", "banks")
	if err != nil {
		return nil, err
	}
	sources, err := timing.Number("resources", "res-register-banks", "source_requests")
	if err != nil {
		return nil, err
	}
	if banks <= 0 || banks&(banks-1) != 0 || sources != len(Token{}.Sources) {
		return nil, fmt.Errorf("unsupported operand bank/source configuration")
	}
	a, err := NewBuffer("b-opc-read-first")
	if err != nil {
		return nil, err
	}
	b, err := NewBuffer("b-opc-read-second")
	if err != nil {
		return nil, err
	}
	out, err := NewBuffer("b-opc-out")
	if err != nil {
		return nil, err
	}
	return &Collector{banks: banks, first: a, second: b, out: out}, nil
}
func (c *Collector) ID() string { return "b-opc-read" }
func (c *Collector) Capacity() int {
	return c.first.Capacity() + c.second.Capacity() + c.out.Capacity()
}
func (c *Collector) Occupancy() int {
	return c.first.Occupancy() + c.second.Occupancy() + c.out.Occupancy()
}
func (c *Collector) Fetched() uint8             { return c.fetched }
func (c *Collector) Output(input Signal) Signal { return c.out.Output(input) }

func (c *Collector) Evaluate(input Signal, downstream bool) (OperandTransition, error) {
	if input.Valid && input.Token.Used & ^uint8(7) != 0 {
		return OperandTransition{}, fmt.Errorf("invalid source mask")
	}
	if c.fetched != 0 && (!input.Valid || input.Token != c.held) {
		return OperandTransition{}, fmt.Errorf("operand input changed before final acceptance")
	}
	var pending, granted uint8
	usedBanks := map[int]bool{}
	if input.Valid {
		for i, reg := range input.Token.Sources {
			if reg >= 64 {
				return OperandTransition{}, fmt.Errorf("invalid register ID")
			}
			bit := uint8(1 << i)
			if input.Token.Used&bit == 0 || c.fetched&bit != 0 || reg == 0 {
				continue
			}
			pending |= bit
			bank := int(reg) & (c.banks - 1)
			if !usedBanks[bank] {
				usedBanks[bank] = true
				granted |= bit
			}
		}
	}
	final := pending == granted
	out := c.out.Evaluate(c.second.Output(Signal{}), downstream)
	secondInput := c.first.Output(Signal{})
	secondInput.Valid = secondInput.Valid && secondInput.Token.LastRead
	second := c.second.Evaluate(secondInput, out.InputReady)
	firstInput := input
	firstInput.Token.ReadMask = granted
	firstInput.Token.LastRead = final
	first := c.first.Evaluate(firstInput, second.InputReady)
	fetched, held := c.fetched, c.held
	if first.InputReady && final {
		fetched = 0
		held = Token{}
	} else if first.Accepted {
		fetched |= granted
		held = input.Token
	}
	t := transfer(input, out.Output, first.InputReady && final, downstream)
	t.edits = append(t.edits, first.edits...)
	t.edits = append(t.edits, second.edits...)
	t.edits = append(t.edits, out.edits...)
	t.edits = append(t.edits, c.rev.propose(func() { c.fetched, c.held = fetched, held }))
	read := first.Output
	read.Valid = first.Completed
	if !first.Accepted {
		granted = 0
	}
	return OperandTransition{Transition: t, Read: read, Granted: granted}, nil
}
func (c *Collector) Flush() Transition {
	t := Transition{}
	for _, b := range []*Buffer{c.first, c.second, c.out} {
		t.edits = append(t.edits, b.Flush().edits...)
	}
	t.edits = append(t.edits, c.rev.propose(func() { c.fetched = 0; c.held = Token{} }))
	return t
}
