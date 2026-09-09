package runner

import "vortex.local/simulator/isa"

// KernelEvent records residency boundaries separately from pipeline edges.
// Slot is -1 for generation; CTA is always the immutable GridWalker ID.
type KernelEvent struct {
	Kind  string
	Cycle uint64
	CTA   uint32
	Slot  int
	Warps isa.WarpMask
}

// TakeEvents returns and clears detached events. Call after each Run, including
// a completed Run, to obtain its final reclamation/completion boundary events.
func (k *Kernel) TakeEvents() []KernelEvent {
	events := append([]KernelEvent(nil), k.events...)
	k.events = nil
	return events
}
func (k *Kernel) event(kind string, cta uint32, slot int, warps isa.WarpMask) {
	k.events = append(k.events, KernelEvent{Kind: kind, Cycle: k.runner.Cycle(), CTA: cta, Slot: slot, Warps: warps})
}
