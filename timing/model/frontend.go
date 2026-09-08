package model

// Frontend composes schedule through the four Dispatch queues. Decode and
// ordinary sequencing are wires; every proposal reads old registered outputs.
// Eligibility must be supplied by a conservative driver or future scoreboard.
type Frontend struct {
	schedule, ibuf *Buffer
	fetch          *Fetch
	seq            *Sequencer
	issue          *Issue
	opc            *Collector
	dispatch       *Dispatch
}
type FrontendTransition struct {
	Transition
	Request                        Signal
	RequestAccepted, ResponseReady bool
	Outputs                        [4]Signal
	Releases                       [4]bool
	Read                           Signal
}

func NewFrontend() (*Frontend, error) {
	f := &Frontend{}
	var err error
	if f.schedule, err = NewBuffer("b-schedule"); err != nil {
		return nil, err
	}
	if f.fetch, err = NewFetch(); err != nil {
		return nil, err
	}
	if f.ibuf, err = NewBuffer("b-ibuffer"); err != nil {
		return nil, err
	}
	if f.seq, err = NewSequencer(); err != nil {
		return nil, err
	}
	if f.issue, err = NewIssue(); err != nil {
		return nil, err
	}
	if f.opc, err = NewCollector(); err != nil {
		return nil, err
	}
	if f.dispatch, err = NewDispatch(); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *Frontend) Outputs() [4]Signal { return f.dispatch.Outputs() }
func (f *Frontend) Credits() [4]int    { return f.issue.Credits() }
func (f *Frontend) Evaluate(input Signal, response Response, requestReady, eligible bool, downstream [4]bool) (FrontendTransition, error) {
	d, err := f.dispatch.Evaluate(f.opc.Output(Signal{}), downstream)
	if err != nil {
		return FrontendTransition{}, err
	}
	opc, err := f.opc.Evaluate(f.issue.Output(Signal{}), d.InputReady)
	if err != nil {
		return FrontendTransition{}, err
	}
	ibufOut := f.ibuf.Output(Signal{})
	issue, err := f.issue.Evaluate(f.seq.Output(ibufOut), opc.InputReady, eligible, d.Releases)
	if err != nil {
		return FrontendTransition{}, err
	}
	seq, err := f.seq.Evaluate(ibufOut, issue.InputReady)
	if err != nil {
		return FrontendTransition{}, err
	}
	fetch, err := f.fetch.Evaluate(f.schedule.Output(Signal{}), response, requestReady, f.ibuf.Ready(seq.InputReady))
	if err != nil {
		return FrontendTransition{}, err
	}
	decoded, err := DecodeToken(fetch.Output)
	if err != nil {
		return FrontendTransition{}, err
	}
	ibuf := f.ibuf.Evaluate(decoded, seq.InputReady)
	schedule := f.schedule.Evaluate(input, fetch.InputReady)
	t := combine(transfer(input, Signal{}, schedule.InputReady, false), schedule, fetch.Transition, ibuf, seq, issue, opc.Transition, d.Transition)
	return FrontendTransition{Transition: t, Request: fetch.Request, RequestAccepted: fetch.RequestAccepted, ResponseReady: fetch.ResponseReady, Outputs: d.Outputs, Releases: d.Releases, Read: opc.Read}, nil
}
func (f *Frontend) Flush() Transition {
	return combine(Transition{}, f.schedule.Flush(), f.fetch.Flush(), f.ibuf.Flush(), f.seq.Flush(), f.issue.Flush(), f.opc.Flush(), f.dispatch.Flush())
}
