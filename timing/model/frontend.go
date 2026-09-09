package model

import "fmt"

// Frontend composes four per-warp IBuffer/sequencer/staging paths with one
// shared Fetch, Collector and Dispatch. Every proposal samples old registers.
// The optional Scheduler owns autonomous fetch selection; explicit-token
// diagnostics use the same downstream resources and scoreboard.
type Frontend struct {
	schedule  *Buffer
	scheduler *Scheduler
	fetch     *Fetch
	ibuf      [4]*Buffer
	seq       [4]*Sequencer
	issue     *Scoreboard
	opc       *Collector
	dispatch  *Dispatch
}
type FrontendTransition struct {
	IssueSelected int
	Transition
	Offered, Request               Signal
	RequestAccepted, ResponseReady bool
	Outputs                        [4]Signal
	Releases                       [4]bool
	Read, Issued, Decoded          Signal
	IBufferPop                     [4]bool
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
	for w := range f.ibuf {
		if f.ibuf[w], err = NewBuffer("b-ibuffer"); err != nil {
			return nil, err
		}
		if f.seq[w], err = NewSequencer(); err != nil {
			return nil, err
		}
	}
	if f.issue, err = NewScoreboard(); err != nil {
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
func NewScheduledFrontend(warps [4]WarpContext) (*Frontend, error) {
	f, err := NewFrontend()
	if err != nil {
		return nil, err
	}
	f.scheduler, err = NewScheduler(warps)
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (f *Frontend) Outputs() [4]Signal               { return f.dispatch.Outputs() }
func (f *Frontend) Credits() [4]int                  { return f.issue.State().Credits }
func (f *Frontend) ScoreboardState() ScoreboardState { return f.issue.State() }
func (f *Frontend) SchedulerState() (SchedulerState, bool) {
	if f.scheduler == nil {
		return SchedulerState{}, false
	}
	return f.scheduler.State(), true
}
func (f *Frontend) Evaluate(input Signal, response Response, requestReady, eligible bool, downstream [4]bool, writeback Signal, feedback ...SchedulerFeedback) (FrontendTransition, error) {
	if f.scheduler == nil && len(feedback) != 0 {
		return FrontendTransition{}, fmt.Errorf("scheduler feedback requires scheduled frontend")
	}
	if f.scheduler != nil {
		if input.Valid {
			return FrontendTransition{}, fmt.Errorf("scheduled frontend does not accept externally selected instructions")
		}
		input = f.scheduler.Output()
		eligible = true
	}
	if input.Valid && input.Token.Warp >= 4 || response.Valid && response.Warp >= 4 {
		return FrontendTransition{}, fmt.Errorf("invalid frontend warp")
	}
	d, err := f.dispatch.Evaluate(f.opc.Output(Signal{}), downstream)
	if err != nil {
		return FrontendTransition{}, err
	}
	opc, err := f.opc.Evaluate(f.issue.Output(), d.InputReady)
	if err != nil {
		return FrontendTransition{}, err
	}
	var ibufOut, uops [4]Signal
	for w := range f.ibuf {
		ibufOut[w] = f.ibuf[w].Output(Signal{})
		uops[w] = f.seq[w].Output(ibufOut[w])
	}
	issue, err := f.issue.evaluate(uops, writeback, opc.InputReady, d.Releases, eligible)
	if err != nil {
		return FrontendTransition{}, err
	}
	var seq [4]Transition
	for w := range seq {
		seq[w], err = f.seq[w].Evaluate(ibufOut[w], issue.Ready[w])
		if err != nil {
			return FrontendTransition{}, err
		}
	}
	// Fetch validates response identity. Its response port is routed to that
	// warp's physical FIFO, whose full flag cannot borrow a same-edge pop.
	decodeReady := true
	if response.Valid {
		decodeReady = f.ibuf[response.Warp].Ready(seq[response.Warp].InputReady)
	}
	fetch, err := f.fetch.Evaluate(f.schedule.Output(Signal{}), response, requestReady, decodeReady)
	if err != nil {
		return FrontendTransition{}, err
	}
	decoded, err := DecodeToken(fetch.Output)
	if err != nil {
		return FrontendTransition{}, err
	}
	schedule := f.schedule.Evaluate(input, fetch.InputReady)
	t := combine(transfer(input, Signal{}, schedule.InputReady, false), schedule, fetch.Transition, issue.Transition, opc.Transition, d.Transition)
	var pops [4]bool
	decodeEvent := Signal{}
	for w := range f.ibuf {
		in := decoded
		in.Valid = in.Valid && int(in.Token.Warp) == w
		ibuf := f.ibuf[w].Evaluate(in, seq[w].InputReady)
		pops[w] = ibuf.Completed
		if ibuf.Accepted {
			decodeEvent = in
		}
		t = combine(t, ibuf, seq[w])
	}
	if f.scheduler != nil {
		accepted := f.schedule.Output(Signal{})
		accepted.Valid = fetch.Accepted
		scheduler, err := f.scheduler.Evaluate(schedule.InputReady, accepted, pops, decodeEvent, feedback...)
		if err != nil {
			return FrontendTransition{}, err
		}
		t = combine(t, scheduler)
	}
	return FrontendTransition{IssueSelected: issue.Selected, Transition: t, Offered: input, Request: fetch.Request, RequestAccepted: fetch.RequestAccepted, ResponseReady: fetch.ResponseReady, Outputs: d.Outputs, Releases: d.Releases, Read: opc.Read, Issued: issue.Issued, Decoded: decodeEvent, IBufferPop: pops}, nil
}
func (f *Frontend) Flush() Transition {
	t := combine(Transition{}, f.schedule.Flush(), f.fetch.Flush(), f.issue.Flush(), f.opc.Flush(), f.dispatch.Flush())
	for w := range f.ibuf {
		t = combine(t, f.ibuf[w].Flush(), f.seq[w].Flush())
	}
	if f.scheduler != nil {
		t = combine(t, f.scheduler.Flush())
	}
	return t
}
