package model

import "fmt"

// Fetch separates request-buffer acceptance (tag_store write) from external
// request acceptance and response consumption. I-cache service is external;
// the physical I-flush request buffer remains even with VM disabled.
type Fetch struct {
	request, iflush *Buffer
	tags            *WaitPool
	issued          []Token
	rev             revision
}
type FetchTransition struct {
	Transition
	Request         Signal
	RequestAccepted bool
	ResponseReady   bool
}

func NewFetch() (*Fetch, error) {
	req, err := NewBuffer("b-fetch-request")
	if err != nil {
		return nil, err
	}
	flush, err := NewBuffer("b-iflush")
	if err != nil {
		return nil, err
	}
	tags, err := NewWaitPool("res-fetch-tags")
	if err != nil {
		return nil, err
	}
	return &Fetch{request: req, iflush: flush, tags: tags}, nil
}
func (f *Fetch) ID() string { return "n-fetch" }

// Capacity counts contexts; request buffers carry the same instruction identity
// and must not be added to the context count as additional fetch capacity.
func (f *Fetch) Capacity() int      { return f.tags.Capacity() }
func (f *Fetch) Occupancy() int     { return f.tags.Occupancy() }
func (f *Fetch) Pending() []Pending { return f.tags.Pending() }
func (f *Fetch) Request() Signal    { return f.iflush.Output(Signal{}) }
func (f *Fetch) Evaluate(input Signal, response Response, requestReady, decodeReady bool) (FetchTransition, error) {
	index := -1
	if response.Valid {
		for i, t := range f.issued {
			if sameIdentity(t, response) {
				index = i
				break
			}
		}
		if index < 0 {
			return FetchTransition{}, fmt.Errorf("fetch response before external request acceptance or stale identity")
		}
	}
	flush := f.iflush.Evaluate(f.request.Output(Signal{}), requestReady)
	probe, err := f.tags.Evaluate(input, response, decodeReady)
	if err != nil {
		return FetchTransition{}, err
	}
	reqInput := input
	reqInput.Valid = reqInput.Valid && probe.InputReady
	req := f.request.Evaluate(reqInput, flush.InputReady)
	tagInput := input
	tagInput.Valid = tagInput.Valid && req.InputReady
	tags, err := f.tags.Evaluate(tagInput, response, decodeReady)
	if err != nil {
		return FetchTransition{}, err
	}
	issued := append([]Token(nil), f.issued...)
	if tags.Completed {
		issued = append(issued[:index], issued[index+1:]...)
	}
	if flush.Completed {
		issued = append(issued, flush.Output.Token)
	}
	t := transfer(input, tags.Output, probe.InputReady && req.InputReady, decodeReady)
	t.edits = append(req.edits, flush.edits...)
	t.edits = append(t.edits, tags.edits...)
	t.edits = append(t.edits, f.rev.propose(func() { f.issued = issued }))
	return FetchTransition{Transition: t, Request: flush.Output, RequestAccepted: flush.Completed, ResponseReady: tags.ResponseReady}, nil
}
func (f *Fetch) Flush() Transition {
	t := Transition{}
	for _, p := range []Transition{f.request.Flush(), f.iflush.Flush(), f.tags.Flush()} {
		t.edits = append(t.edits, p.edits...)
	}
	t.edits = append(t.edits, f.rev.propose(func() { f.issued = nil }))
	return t
}
