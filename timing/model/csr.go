package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// CSR models context wait and result storage only. RequestWindow exposes the
// unqualified csr_req_valid observation; effects applies every valid window,
// independently of result readiness (audit-csr-stall).
type CSR struct {
	latency, wait int
	out           *Buffer
	rev           revision
}
type CSRTransition struct {
	Transition
	RequestWindow bool
}

func NewCSR() (*CSR, error) {
	latency, err := timing.Number("timing_measurements", "tm-csr-context", "value")
	if err != nil {
		return nil, err
	}
	interval, err := timing.Number("timing_measurements", "tm-csr-admission", "value")
	if err != nil {
		return nil, err
	}
	if latency < 1 || interval != latency+1 {
		return nil, fmt.Errorf("CSR context/interval drift")
	}
	out, err := NewBuffer("b-sfu-csr-result")
	if err != nil {
		return nil, err
	}
	return &CSR{latency: latency, out: out}, nil
}
func (c *CSR) ID() string                 { return "tm-csr-context" }
func (c *CSR) Capacity() int              { return c.out.Capacity() }
func (c *CSR) Occupancy() int             { return c.out.Occupancy() }
func (c *CSR) Output(input Signal) Signal { return c.out.Output(input) }
func (c *CSR) Evaluate(input Signal, downstream bool) CSRTransition {
	done := c.wait == c.latency
	req := input
	req.Valid = req.Valid && done
	out := c.out.Evaluate(req, downstream)
	wait := c.wait
	if out.Accepted || !input.Valid {
		wait = 0
	} else if wait < c.latency {
		wait++
	}
	t := transfer(input, out.Output, out.InputReady && done, downstream)
	t.edits = append(out.edits, c.rev.propose(func() { c.wait = wait }))
	return CSRTransition{Transition: t, RequestWindow: req.Valid}
}
func (c *CSR) Flush() Transition {
	t := c.out.Flush()
	t.edits = append(t.edits, c.rev.propose(func() { c.wait = 0 }))
	return t
}
