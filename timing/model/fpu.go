package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// STDFPU explicitly selects STD; it does not infer the external build backend.
// Headers alias pipeline tokens, limiting inflight instructions to two rather
// than adding two more data entries. Numeric tag addresses are abstracted by
// stable identity; capacity, registered full and release edges are preserved.
type STDFPU struct {
	paths  [4]*STDPath
	result *Merge
	tags   *WaitPool
	flags  Signal
	rev    revision
}
type FPUTransition struct {
	Transition
	Flags Signal
}

var stdPaths = [4]Path{FMA, DIVSQRT, CVT, NCP}

func NewSTDFPU() (*STDFPU, error) {
	for _, id := range []string{"b-fpu-dispatch", "b-fpu-gather", "b-fpu-response"} {
		b, err := timing.Buffer(id)
		if err != nil {
			return nil, err
		}
		if b.Size != 0 {
			return nil, fmt.Errorf("unsupported FPU boundary %s", id)
		}
	}
	n, err := timing.Number("boundaries", "b-fpu-csr-feedback", "latency_cycles")
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("FPU CSR register drift")
	}
	f := &STDFPU{}
	for i, kind := range []string{"fma", "divsqrt", "cvt", "ncp"} {
		if f.paths[i], err = NewSTDPath(kind); err != nil {
			return nil, err
		}
	}
	if f.result, err = NewMerge("b-fpu-backend-out"); err != nil {
		return nil, err
	}
	if f.tags, err = NewWaitPool("res-fpu-tags"); err != nil {
		return nil, err
	}
	if err = f.result.requireInputs(4); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *STDFPU) ID() string         { return "n-fpu" }
func (f *STDFPU) Capacity() int      { return f.tags.Capacity() }
func (f *STDFPU) Occupancy() int     { return f.tags.Occupancy() }
func (f *STDFPU) Pending() []Pending { return f.tags.Pending() }
func (f *STDFPU) Output() Signal     { return f.result.Output() }
func (f *STDFPU) Evaluate(input Signal, downstream bool) (FPUTransition, error) {
	selected := -1
	for i, p := range stdPaths {
		if input.Token.Path == p {
			selected = i
		}
	}
	if input.Valid && (input.Token.Class != 3 || selected < 0) {
		return FPUTransition{}, fmt.Errorf("invalid STD route")
	}
	outputs := make([]Signal, len(f.paths))
	for i, p := range f.paths {
		outputs[i] = p.Output(Signal{})
	}
	result, err := f.result.Evaluate(outputs, downstream)
	if err != nil {
		return FPUTransition{}, err
	}
	r := result.Output
	response := Response{Valid: r.Valid, ID: r.Token.ID, Epoch: r.Token.Epoch, Warp: r.Token.Warp, Uop: r.Token.Uop, Mask: r.Token.Mask}
	probe, err := f.tags.Evaluate(input, response, downstream)
	if err != nil {
		return FPUTransition{}, err
	}
	var parts []Transition
	ready := false
	for i, p := range f.paths {
		in := routed(input, stdPaths[i])
		in.Valid = in.Valid && probe.InputReady
		part := p.Evaluate(in, result.Ready[i])
		parts = append(parts, part)
		if i == selected {
			ready = part.InputReady
		}
	}
	tagIn := input
	tagIn.Valid = tagIn.Valid && ready
	tags, err := f.tags.Evaluate(tagIn, response, downstream)
	if err != nil {
		return FPUTransition{}, err
	}
	parts = append(parts, result.Transition, tags.Transition)
	t := combine(transfer(input, result.Output, ready && probe.InputReady, downstream), parts...)
	flags := result.Output
	flags.Valid = result.Completed && flags.Token.End
	t.edits = append(t.edits, f.rev.propose(func() { f.flags = flags }))
	return FPUTransition{Transition: t, Flags: f.flags}, nil
}
func (f *STDFPU) Flush() Transition {
	t := combine(Transition{}, f.result.Flush(), f.tags.Flush())
	for _, p := range f.paths {
		t = combine(t, p.Flush())
	}
	t.edits = append(t.edits, f.rev.propose(func() { f.flags = Signal{} }))
	return t
}
