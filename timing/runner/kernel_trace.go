package runner

import "vortex.local/simulator/isa"

// WarpBinding is an explicit namespace for tokens in a Kernel MultiRecord.
// Join Token.Warp to this array, then Token.ID/Epoch/Uop to memory Identity.
// Valid is false for unbound slots. It is never inferred from PC or issue order.
// Selected but not dispatched members are not valid execution bindings.
type WarpBinding struct {
	Valid                         bool
	CTA, Rank                     uint32
	Slot                          uint32
	CTAGeneration, WarpGeneration uint64
}

func (k *Kernel) traceBindings() (bindings [4]WarpBinding) {
	for slot, c := range k.resident {
		if c == nil {
			continue
		}
		for _, m := range c.Resident.Members {
			if m.Rank < c.Dispatched {
				bindings[m.WarpID] = WarpBinding{true, c.Launch.ID, m.Rank, uint32(slot), k.generations[slot], k.warpGenerations[m.WarpID]}
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
