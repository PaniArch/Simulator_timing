package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// Sequencer holds one packed burst but does not pop the upstream ibuffer until
// its LAST uop is accepted. Ordinary instructions remain combinational.
type Sequencer struct {
	bytes, halves int
	active        Signal
	original      Token
	rev           revision
}

func NewSequencer() (*Sequencer, error) {
	capacity, err := timing.Number("resources", "res-uop", "capacity")
	if err != nil {
		return nil, err
	}
	if capacity != 1 {
		return nil, fmt.Errorf("unsupported sequencer capacity")
	}
	bytes, err := timing.Number("resources", "res-uop", "packed_byte_uops")
	if err != nil {
		return nil, err
	}
	halves, err := timing.Number("resources", "res-uop", "packed_half_uops")
	if err != nil {
		return nil, err
	}
	if bytes != 2*halves || bytes > 8 || halves < 1 {
		return nil, fmt.Errorf("unsupported packed configuration")
	}
	return &Sequencer{bytes: bytes, halves: halves}, nil
}
func (s *Sequencer) ID() string    { return "n-uop" }
func (s *Sequencer) Capacity() int { return 1 }
func (s *Sequencer) Occupancy() int {
	if s.active.Valid {
		return 1
	}
	return 0
}
func (s *Sequencer) Output(input Signal) Signal {
	if s.active.Valid {
		return s.active
	}
	if input.Token.Pack != 0 {
		return Signal{}
	}
	return input
}
func (s *Sequencer) Evaluate(input Signal, downstream bool) (Transition, error) {
	if input.Valid && input.Token.Pack > 2 {
		return Transition{}, fmt.Errorf("invalid packed kind")
	}
	if s.active.Valid && (!input.Valid || input.Token != s.original) {
		return Transition{}, fmt.Errorf("packed input changed before final handshake")
	}
	ready := downstream
	if s.active.Valid {
		ready = ready && s.active.Token.Uop+1 == s.active.Token.Uops
	} else if input.Token.Pack != 0 {
		ready = false
	}
	t := transfer(input, s.Output(input), ready, downstream)
	next, original := s.active, s.original
	if !s.active.Valid && input.Valid && input.Token.Pack != 0 {
		original = input.Token
		next = input
		next.Token.Uop = 0
		count := s.bytes
		if input.Token.Pack == 2 {
			count = s.halves
		}
		next.Token.Uops = uint8(count)
	} else if s.active.Valid && t.Completed {
		if t.Accepted {
			next = Signal{}
			original = Token{}
		} else {
			next.Token.Uop++
		}
	}
	t.edits = []mutation{s.rev.propose(func() { s.active, s.original = next, original })}
	return t, nil
}
func (s *Sequencer) Flush() Transition {
	return Transition{edits: []mutation{s.rev.propose(func() { s.active = Signal{}; s.original = Token{} })}}
}
