package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// LSU ends at the frozen scheduler's vector memory port. The external service
// owns coalescing/cache/local memory and store tails; it supplies response masks
// and readiness, never an invented fixed memory latency.
type LSU struct {
	dispatch, request, load, store, gather *Buffer
	result                                 *Merge
	tags                                   *WaitPool
	sent                                   []Token
	fence                                  bool
	rev                                    revision
}
type LSUTransition struct {
	Execute Signal
	Transition
	Request                        Signal
	RequestAccepted, ResponseReady bool
	SliceAccepted                  bool
}

func NewLSU() (*LSU, error) {
	l := &LSU{}
	var err error
	lanes, err := timing.Number("resources", "res-lsu", "lanes")
	if err != nil {
		return nil, err
	}
	clients, err := timing.Number("resources", "res-lsu-request", "clients")
	if err != nil {
		return nil, err
	}
	get := func(field string) (int, error) {
		return timing.Number("resources", "res-lsu-request", "rtl_parameters", field)
	}
	core, err := get("CORE_REQS")
	if err != nil {
		return nil, err
	}
	channels, err := get("MEM_CHANNELS")
	if err != nil {
		return nil, err
	}
	word, err := get("WORD_SIZE")
	if err != nil {
		return nil, err
	}
	line, err := get("LINE_SIZE")
	if err != nil {
		return nil, err
	}
	partial, err := get("RSP_PARTIAL")
	if err != nil {
		return nil, err
	}
	if clients != 1 || core != lanes || channels != lanes || word != line || partial != 1 {
		return nil, fmt.Errorf("unsupported LSU scheduler topology")
	}

	for _, field := range []string{"MEM_OUT_BUF", "CORE_OUT_BUF"} {
		n, e := timing.Number("resources", "res-lsu-request", "rtl_parameters", field)
		if e != nil {
			return nil, e
		}
		if n != 0 {
			return nil, fmt.Errorf("unsupported scheduler output register")
		}
	}
	for _, p := range []struct {
		dst **Buffer
		id  string
	}{{&l.dispatch, "b-lsu-dispatch"}, {&l.request, "b-lsu-req"}, {&l.load, "b-lsu-load-result"}, {&l.store, "b-lsu-store-result"}, {&l.gather, "b-lsu-gather"}} {
		if *p.dst, err = NewBuffer(p.id); err != nil {
			return nil, err
		}
	}
	if l.result, err = NewMerge("b-lsu-result-merge"); err != nil {
		return nil, err
	}
	if err = l.result.requireInputs(2); err != nil {
		return nil, err
	}
	if l.tags, err = NewWaitPool("res-lsu-load-tags"); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *LSU) ID() string { return "n-lsu" }

// Capacity/Occupancy count physical data storage only. Pending reports the
// separate context pool, whose entries alias queued/in-service load tokens.
func (l *LSU) Capacity() int {
	return l.dispatch.Capacity() + l.request.Capacity() + l.load.Capacity() + l.store.Capacity() + l.result.Capacity() + l.gather.Capacity()
}
func (l *LSU) Occupancy() int {
	return l.dispatch.Occupancy() + l.request.Occupancy() + l.load.Occupancy() + l.store.Occupancy() + l.result.Occupancy() + l.gather.Occupancy()
}
func (l *LSU) Pending() []Pending { return l.tags.Pending() }
func (l *LSU) FenceLocked() bool  { return l.fence }
func (l *LSU) Output() Signal     { return l.gather.Output(Signal{}) }
func (l *LSU) Request() Signal    { return l.request.Output(Signal{}) }
func (l *LSU) Drained(serviceIdle bool) bool {
	return serviceIdle && l.Occupancy() == 0 && l.tags.Occupancy() == 0 && !l.fence
}
func (l *LSU) Evaluate(input Signal, response Response, requestReady, downstream bool) (LSUTransition, error) {
	if input.Valid && (input.Token.Class != 1 || (input.Token.Path != LOAD && input.Token.Path != STORE && input.Token.Path != FENCE)) {
		return LSUTransition{}, fmt.Errorf("invalid LSU route")
	}
	index := -1
	if response.Valid {
		for i, t := range l.sent {
			if sameIdentity(t, response) {
				index = i
				break
			}
		}
		if index < 0 {
			return LSUTransition{}, fmt.Errorf("LSU response before request acceptance or stale identity")
		}
	}
	gather := l.gather.Evaluate(l.result.Output(), downstream)
	result, err := l.result.Evaluate([]Signal{l.load.Output(Signal{}), l.store.Output(Signal{})}, gather.InputReady)
	if err != nil {
		return LSUTransition{}, err
	}
	execute := l.dispatch.Output(Signal{})
	skip := execute.Token.Path == FENCE && !execute.Token.End
	noResponse := execute.Token.Path == STORE || skip
	tagIn := execute
	tagIn.Valid = tagIn.Valid && !noResponse && !l.fence
	probe, err := l.tags.Evaluate(tagIn, response, l.load.Ready(result.Ready[0]))
	if err != nil {
		return LSUTransition{}, err
	}
	schedulerReady := l.request.Ready(requestReady) && (execute.Token.Path == STORE || probe.InputReady)
	sliceReady := (schedulerReady || skip) && (!noResponse || l.store.Ready(result.Ready[1])) && !l.fence
	reqIn := execute
	reqIn.Valid = reqIn.Valid && !skip && !l.fence && (!noResponse || l.store.Ready(result.Ready[1])) && (execute.Token.Path == STORE || probe.InputReady)
	request := l.request.Evaluate(reqIn, requestReady)
	tagIn.Valid = tagIn.Valid && sliceReady
	tags, err := l.tags.Evaluate(tagIn, response, l.load.Ready(result.Ready[0]))
	if err != nil {
		return LSUTransition{}, err
	}
	load := l.load.Evaluate(tags.Output, result.Ready[0])
	storeIn := execute
	storeIn.Valid = storeIn.Valid && noResponse && (schedulerReady || skip) && !l.fence
	store := l.store.Evaluate(storeIn, result.Ready[1])
	dispatch := l.dispatch.Evaluate(input, sliceReady)
	sent := append([]Token(nil), l.sent...)
	fence := l.fence
	if request.Accepted && execute.Token.Path == FENCE && execute.Token.End {
		fence = true
	}
	if tags.Completed && tags.Output.Token.End {
		sent = append(sent[:index], sent[index+1:]...)
		if tags.Output.Token.Path == FENCE {
			fence = false
		}
	}
	if request.Completed && request.Output.Token.Path != STORE {
		sent = append(sent, request.Output.Token)
	}
	t := combine(transfer(input, gather.Output, dispatch.InputReady, downstream), dispatch, request, tags.Transition, load, store, result.Transition, gather)
	t.edits = append(t.edits, l.rev.propose(func() { l.sent = sent; l.fence = fence }))
	executed := execute
	executed.Valid = dispatch.Completed
	return LSUTransition{Execute: executed, Transition: t, Request: request.Output, RequestAccepted: request.Completed, ResponseReady: tags.ResponseReady, SliceAccepted: dispatch.Completed}, nil
}
func (l *LSU) Flush() Transition {
	t := combine(Transition{}, l.dispatch.Flush(), l.request.Flush(), l.tags.Flush(), l.load.Flush(), l.store.Flush(), l.result.Flush(), l.gather.Flush())
	t.edits = append(t.edits, l.rev.propose(func() { l.sent = nil; l.fence = false }))
	return t
}
