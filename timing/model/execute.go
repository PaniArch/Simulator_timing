package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// Pipeline is a globally enabled shift pipeline, as in RV32 multiply and the
// full-width STD serializers. A blocked valid tail freezes every stage, even
// when there are bubbles inside. This differs from a chain of elastic queues.
type Pipeline struct {
	id     string
	stages []Signal
	rev    revision
}

func newPipeline(id string, depth int) (*Pipeline, error) {
	if depth <= 0 {
		return nil, fmt.Errorf("%s: invalid pipeline depth %d", id, depth)
	}
	return &Pipeline{id: id, stages: make([]Signal, depth)}, nil
}

func NewMultiply() (*Pipeline, error) {
	interval, err := timing.Number("timing_measurements", "tm-imul-interval", "value")
	if err != nil {
		return nil, err
	}
	if interval != 1 {
		return nil, fmt.Errorf("multiply interval drift")
	}
	depth, err := timing.Number("timing_measurements", "tm-imul-result", "value")
	if err != nil {
		return nil, err
	}
	return newPipeline("tm-imul-result", depth)
}

func (p *Pipeline) ID() string    { return p.id }
func (p *Pipeline) Capacity() int { return len(p.stages) }
func (p *Pipeline) Occupancy() int {
	n := 0
	for _, s := range p.stages {
		if s.Valid {
			n++
		}
	}
	return n
}
func (p *Pipeline) Stages() []Signal           { return append([]Signal(nil), p.stages...) }
func (p *Pipeline) Output(_ Signal) Signal     { return p.stages[len(p.stages)-1] }
func (p *Pipeline) Ready(downstream bool) bool { return downstream || !p.Output(Signal{}).Valid }
func (p *Pipeline) Evaluate(input Signal, downstream bool) Transition {
	t := transfer(input, p.Output(input), p.Ready(downstream), downstream)
	next := p.Stages()
	if t.InputReady {
		copy(next[1:], p.stages[:len(p.stages)-1])
		next[0] = input
	}
	t.edits = []mutation{p.rev.propose(func() { p.stages = next })}
	return t
}
func (p *Pipeline) Flush() Transition {
	next := make([]Signal, len(p.stages))
	return Transition{edits: []mutation{p.rev.propose(func() { p.stages = next })}}
}

// STDPath explicitly selects a source-conditional STD subcore. Its measured
// latency INCLUDES the serializer output buffer. Backend merge/header tags
// remain separate resources. No external build selection is inferred.
type STDPath struct {
	pipe *Pipeline
	out  *Buffer
}

func NewSTDPath(kind string) (*STDPath, error) {
	var measurement, boundary string
	switch kind {
	case "fma":
		measurement, boundary = "tm-fp-fma", "b-std-fma-serializer"
	case "divsqrt":
		measurement, boundary = "tm-fp-divsqrt", "b-std-fdivsqrt-serializer"
	case "cvt":
		measurement, boundary = "tm-fp-cvt", "b-std-cvt-serializer"
	case "ncp":
		measurement, boundary = "tm-fp-ncp", "b-std-ncp-serializer"
	default:
		return nil, fmt.Errorf("unsupported STD subcore %q", kind)
	}
	latency, err := timing.Number("timing_measurements", measurement, "value")
	if err != nil {
		return nil, err
	}
	out, err := NewBuffer(boundary)
	if err != nil {
		return nil, err
	}
	peReg, err := timing.Number("boundaries", boundary, "rtl_parameters", "PE_REG")
	if err != nil {
		return nil, err
	}
	interval, err := timing.Number("timing_measurements", "tm-fp-pe-interval", "value")
	if err != nil {
		return nil, err
	}
	if peReg != 0 || out.Capacity() != 2 || interval != 1 {
		return nil, fmt.Errorf("unsupported STD serializer profile")
	}
	bufferLatency, err := timing.Number("timing_measurements", "tm-buffer-2", "value")
	if err != nil {
		return nil, err
	}
	pipe, err := newPipeline(measurement, latency-bufferLatency)
	if err != nil {
		return nil, err
	}
	return &STDPath{pipe: pipe, out: out}, nil
}
func (p *STDPath) ID() string                 { return p.pipe.ID() }
func (p *STDPath) Capacity() int              { return p.pipe.Capacity() + p.out.Capacity() }
func (p *STDPath) Occupancy() int             { return p.pipe.Occupancy() + p.out.Occupancy() }
func (p *STDPath) Output(input Signal) Signal { return p.out.Output(p.pipe.Output(input)) }
func (p *STDPath) Ready(downstream bool) bool { return p.pipe.Ready(p.out.Ready(downstream)) }
func (p *STDPath) Evaluate(input Signal, downstream bool) Transition {
	a := p.pipe.Evaluate(input, p.out.Ready(downstream))
	b := p.out.Evaluate(a.Output, downstream)
	t := transfer(input, b.Output, a.InputReady, downstream)
	t.edits = append(a.edits, b.edits...)
	return t
}
func (p *STDPath) Flush() Transition {
	a, b := p.pipe.Flush(), p.out.Flush()
	return Transition{edits: append(a.edits, b.edits...)}
}

// Divider retains loaded ownership through arithmetic AND output stalls.
// Unlike a pipe register it never accepts on the edge releasing its result.
type Divider struct {
	latency   int
	loaded    Signal
	remaining int
	rev       revision
}

func NewDivider() (*Divider, error) {
	get := func(id string) (int, error) { return timing.Number("timing_measurements", id, "value") }
	latency, err := get("tm-idiv-result")
	if err != nil {
		return nil, err
	}
	iterations, err := get("tm-idiv-iterations")
	if err != nil {
		return nil, err
	}
	interval, err := get("tm-idiv-accept")
	if err != nil {
		return nil, err
	}
	capacity, err := get("tm-idiv-capacity")
	if err != nil {
		return nil, err
	}
	if latency != iterations+1 || interval != latency+1 || capacity != 1 {
		return nil, fmt.Errorf("serial divider IR relationship drift")
	}
	return &Divider{latency: latency}, nil
}
func (d *Divider) ID() string    { return "tm-idiv-result" }
func (d *Divider) Capacity() int { return 1 }
func (d *Divider) Occupancy() int {
	if d.loaded.Valid {
		return 1
	}
	return 0
}
func (d *Divider) Remaining() int    { return d.remaining }
func (d *Divider) Ready(_ bool) bool { return !d.loaded.Valid }
func (d *Divider) Output(_ Signal) Signal {
	if d.remaining != 0 {
		return Signal{}
	}
	return d.loaded
}
func (d *Divider) Evaluate(input Signal, downstream bool) Transition {
	t := transfer(input, d.Output(input), d.Ready(downstream), downstream)
	loaded, remaining := d.loaded, d.remaining
	if remaining > 0 {
		remaining--
	}
	if t.Completed {
		loaded = Signal{}
	}
	if t.Accepted {
		loaded = input
		remaining = d.latency - 1
	}
	t.edits = []mutation{d.rev.propose(func() { d.loaded, d.remaining = loaded, remaining })}
	return t
}
func (d *Divider) Flush() Transition {
	return Transition{edits: []mutation{d.rev.propose(func() { d.loaded = Signal{}; d.remaining = 0 })}}
}
