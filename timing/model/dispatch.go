package model

import "fmt"

// Dispatch owns one frozen queue per execution class. Credits are released by
// Completed, when a FU accepts the queue head, rather than queue admission.
type Dispatch struct{ queues [4]*Buffer }
type DispatchTransition struct {
	Transition
	Outputs  [4]Signal
	Releases [4]bool
}

func NewDispatch() (*Dispatch, error) {
	d := &Dispatch{}
	for i := range d.queues {
		b, err := NewBuffer("b-dispatch")
		if err != nil {
			return nil, err
		}
		d.queues[i] = b
	}
	return d, nil
}
func (d *Dispatch) ID() string { return "n-dispatch" }
func (d *Dispatch) Capacity() int {
	n := 0
	for _, q := range d.queues {
		n += q.Capacity()
	}
	return n
}
func (d *Dispatch) Occupancy() int {
	n := 0
	for _, q := range d.queues {
		n += q.Occupancy()
	}
	return n
}
func (d *Dispatch) Occupancies() (n [4]int) {
	for i, q := range d.queues {
		n[i] = q.Occupancy()
	}
	return
}
func (d *Dispatch) Outputs() (s [4]Signal) {
	for i, q := range d.queues {
		s[i] = q.Output(Signal{})
	}
	return
}
func (d *Dispatch) Evaluate(input Signal, downstream [4]bool) (DispatchTransition, error) {
	if input.Valid && input.Token.Class >= 4 {
		return DispatchTransition{}, fmt.Errorf("invalid dispatch class")
	}
	t := DispatchTransition{Outputs: d.Outputs()}
	for i, q := range d.queues {
		in := input
		in.Valid = in.Valid && int(in.Token.Class) == i
		p := q.Evaluate(in, downstream[i])
		t.Transition = combine(t.Transition, p)
		t.Releases[i] = p.Completed
		if int(input.Token.Class) == i {
			t.InputReady = p.InputReady
			t.Accepted = p.Accepted
		}
	}
	return t, nil
}
func (d *Dispatch) Flush() Transition {
	t := Transition{}
	for _, q := range d.queues {
		t = combine(t, q.Flush())
	}
	return t
}
