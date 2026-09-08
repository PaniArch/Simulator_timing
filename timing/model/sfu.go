package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// SFU connects the full-width WCTL/CSR PEs, their result queues, R merge and
// lane dispatch/gather. ControlAllowed is the caller's old-state WSYNC/BAR
// drain predicate. Control observations carry identity, not architectural data.
type SFU struct {
	dispatch, wctl, gather *Buffer
	csr                    *CSR
	result                 *Merge
	control                Signal
	rev                    revision
}
type SFUTransition struct {
	Transition
	Control             Signal
	Execute, CSRRequest Signal
	CSRRequestWindow    bool
}

func NewSFU() (*SFU, error) {
	s := &SFU{}
	var err error
	n, err := timing.Number("timing_measurements", "tm-wctl-owner", "value")
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("WCTL sideband drift")
	}
	if s.dispatch, err = NewBuffer("b-sfu-dispatch"); err != nil {
		return nil, err
	}
	if s.wctl, err = NewBuffer("b-sfu-wctl-result"); err != nil {
		return nil, err
	}
	if s.gather, err = NewBuffer("b-sfu-gather"); err != nil {
		return nil, err
	}
	if s.csr, err = NewCSR(); err != nil {
		return nil, err
	}
	if s.result, err = NewMerge("b-sfu-merge"); err != nil {
		return nil, err
	}
	if err = s.result.requireInputs(2); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *SFU) ID() string { return "n-sfu" }
func (s *SFU) Capacity() int {
	return s.dispatch.Capacity() + s.wctl.Capacity() + s.csr.Capacity() + s.result.Capacity() + s.gather.Capacity()
}
func (s *SFU) Occupancy() int {
	return s.dispatch.Occupancy() + s.wctl.Occupancy() + s.csr.Occupancy() + s.result.Occupancy() + s.gather.Occupancy()
}
func (s *SFU) Output() Signal       { return s.gather.Output(Signal{}) }
func (s *SFU) ExecuteInput() Signal { return s.dispatch.Output(Signal{}) }
func (s *SFU) Evaluate(input Signal, downstream, controlAllowed bool) (SFUTransition, error) {
	if input.Valid && (input.Token.Class != 2 || (input.Token.Path != WCTL && input.Token.Path != CSRPath)) {
		return SFUTransition{}, fmt.Errorf("invalid SFU route")
	}
	gather := s.gather.Evaluate(s.result.Output(), downstream)
	result, err := s.result.Evaluate([]Signal{s.wctl.Output(Signal{}), s.csr.Output(Signal{})}, gather.InputReady)
	if err != nil {
		return SFUTransition{}, err
	}
	execute := s.ExecuteInput()
	wIn := routed(execute, WCTL)
	wIn.Valid = wIn.Valid && controlAllowed
	wctl := s.wctl.Evaluate(wIn, result.Ready[0])
	csr := s.csr.Evaluate(routed(execute, CSRPath), result.Ready[1])
	ready := wctl.InputReady && controlAllowed
	if execute.Token.Path == CSRPath {
		ready = csr.InputReady
	}
	dispatch := s.dispatch.Evaluate(input, ready)
	control := wIn
	control.Valid = wctl.Accepted && wIn.Token.End
	t := combine(transfer(input, gather.Output, dispatch.InputReady, downstream), dispatch, wctl, csr.Transition, result.Transition, gather)
	t.edits = append(t.edits, s.rev.propose(func() { s.control = control }))
	execEvent := execute
	execEvent.Valid = dispatch.Completed
	csrEvent := routed(execute, CSRPath)
	csrEvent.Valid = csr.RequestWindow
	return SFUTransition{Transition: t, Control: s.control, Execute: execEvent, CSRRequest: csrEvent, CSRRequestWindow: csr.RequestWindow}, nil
}
func (s *SFU) Flush() Transition {
	t := combine(Transition{}, s.dispatch.Flush(), s.wctl.Flush(), s.csr.Flush(), s.result.Flush(), s.gather.Flush())
	t.edits = append(t.edits, s.rev.propose(func() { s.control = Signal{} }))
	return t
}
