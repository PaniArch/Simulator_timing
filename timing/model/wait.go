package model

import (
	"fmt"

	"vortex.local/simulator/timing"
)

// Response is presented by a timing service or backend until ResponseReady.
// The pool does not invent a service latency or own backing memory bytes.
type Response struct {
	Valid           bool
	ID, Epoch       uint64
	Warp, Uop, Mask uint8
	Word            uint32
}
type Pending struct {
	Token     Token
	Remaining uint8
}
type WaitTransition struct {
	Transition
	ResponseReady bool
}

// WaitPool retains request context until complete response coverage. A response
// flows directly into the downstream result buffer; there is no hidden queue.
type WaitPool struct {
	id       string
	capacity int
	laneMask uint8
	perWarp  bool
	warps    int
	entries  []Pending
	rev      revision
}

func NewWaitPool(resource string) (*WaitPool, error) {
	if resource != "res-fetch-tags" && resource != "res-lsu-load-tags" && resource != "res-fpu-tags" {
		return nil, fmt.Errorf("unsupported wait resource %q", resource)
	}
	capacity, err := timing.Number("resources", resource, "capacity")
	if err != nil {
		return nil, err
	}
	lanes, err := timing.Number("config", "cfg-baseline", "values", "threads_per_warp")
	if err != nil {
		return nil, err
	}
	if capacity <= 0 || lanes <= 0 || lanes > 8 {
		return nil, fmt.Errorf("unsupported wait pool configuration")
	}
	warps, err := timing.Number("config", "cfg-baseline", "values", "warps_per_core")
	if err != nil {
		return nil, err
	}
	return &WaitPool{id: resource, capacity: capacity, laneMask: uint8((1 << lanes) - 1), perWarp: resource == "res-fetch-tags", warps: warps}, nil
}
func (w *WaitPool) ID() string         { return w.id }
func (w *WaitPool) Capacity() int      { return w.capacity }
func (w *WaitPool) Occupancy() int     { return len(w.entries) }
func (w *WaitPool) Pending() []Pending { return append([]Pending(nil), w.entries...) }

func sameIdentity(t Token, r Response) bool {
	return t.ID == r.ID && t.Epoch == r.Epoch && t.Warp == r.Warp && t.Uop == r.Uop
}

func (w *WaitPool) Evaluate(input Signal, response Response, downstream bool) (WaitTransition, error) {
	ready := len(w.entries) < w.capacity
	for _, p := range w.entries {
		if input.Valid && (p.Token.ID == input.Token.ID && p.Token.Epoch == input.Token.Epoch && p.Token.Warp == input.Token.Warp && p.Token.Uop == input.Token.Uop || w.perWarp && p.Token.Warp == input.Token.Warp) {
			ready = false
		}
	}
	if input.Valid && (input.Token.Mask == 0 || input.Token.Mask & ^w.laneMask != 0 || int(input.Token.Warp) >= w.warps) {
		return WaitTransition{}, fmt.Errorf("invalid request lane mask")
	}
	output := Signal{}
	index := -1
	if response.Valid {
		for i, p := range w.entries {
			if sameIdentity(p.Token, response) {
				index = i
				break
			}
		}
		if index < 0 {
			return WaitTransition{}, fmt.Errorf("unknown or stale response identity")
		}
		p := w.entries[index]
		if response.Mask == 0 || response.Mask & ^p.Remaining != 0 || (w.perWarp || w.id == "res-fpu-tags") && response.Mask != p.Remaining {
			return WaitTransition{}, fmt.Errorf("invalid or duplicate response coverage")
		}
		output = Signal{Valid: true, Token: p.Token}
		output.Token.Mask = response.Mask
		output.Token.End = response.Mask == p.Remaining
		if w.id == "res-fpu-tags" {
			output.Token.End = p.Token.End // header eop is independent of tag release
		}
		if w.perWarp {
			output.Token.Word = response.Word
		}
	}
	t := transfer(input, output, ready, downstream)
	next := w.Pending()
	if t.Completed {
		next[index].Remaining &^= response.Mask
		if next[index].Remaining == 0 {
			next = append(next[:index], next[index+1:]...)
		}
	}
	if t.Accepted {
		next = append(next, Pending{Token: input.Token, Remaining: input.Token.Mask})
	}
	t.edits = []mutation{w.rev.propose(func() { w.entries = next })}
	return WaitTransition{Transition: t, ResponseReady: downstream}, nil
}
func (w *WaitPool) Flush() Transition {
	return Transition{edits: []mutation{w.rev.propose(func() { w.entries = nil })}}
}
