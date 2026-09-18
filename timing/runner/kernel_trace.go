package runner

import (
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// Tokens retain the CTA selected when they were fetched, even after the
// physical wid is rebound while old commit or memory work is still in flight.
func (k *Kernel) bindToken(t model.Token) WarpBinding {
	if b, ok := k.tokenBindings[t.ID]; ok {
		return b
	}
	view, err := k.memory.ViewForWarp(t.Warp)
	if err != nil {
		return WarpBinding{}
	}
	c := k.resident[view.ID]
	if c == nil {
		return WarpBinding{}
	}
	b := WarpBinding{true, c.Launch.ID, view.Rank, view.ID, k.generations[view.ID], k.warpGenerations[t.Warp], t.Warp}
	k.tokenBindings[t.ID] = b
	k.bindingRefs[[2]uint64{uint64(t.Warp), b.WarpGeneration}]++
	return b
}

// WarpBinding is an explicit namespace for tokens in a Kernel MultiRecord.
// MultiRecord.Bindings describes current physical membership; TokenBindings
// retains each in-flight Token.ID's original generation across physical reuse.
// Valid is false for unbound slots. It is never inferred from PC or issue order.
// Selected but not dispatched members are not valid execution bindings.
type WarpBinding struct {
	Valid                         bool
	CTA, Rank                     uint32
	Slot                          uint32
	CTAGeneration, WarpGeneration uint64
	PhysicalWarp                  uint8
}

func (k *Kernel) traceBindings() (bindings [4]WarpBinding) {
	for slot, c := range k.resident {
		if c == nil {
			continue
		}
		for _, m := range c.Resident.Members {
			if m.Rank < c.Dispatched {
				bindings[m.WarpID] = WarpBinding{true, c.Launch.ID, m.Rank, uint32(slot), k.generations[slot], k.warpGenerations[m.WarpID], m.WarpID}
			}
		}
	}
	return
}

// Counters is the device-cumulative, 44-bit hardware CSR view at the current
// committed edge. Instret counts EOP Warp/uop notifications, not macro receipts
// or active lanes. Explicit memory-only flush clocks the busy register tail.
// Like Run/Status, callers must serialize access to this Kernel.
func (k *Kernel) Counters() isa.CounterView {
	return isa.CounterView{Cycle: k.runner.core.Cycles(), Instret: k.runner.core.Instret()}
}
