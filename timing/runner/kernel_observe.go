package runner

import (
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

// KernelEvent records residency boundaries separately from pipeline edges.
// Slot is -1 for generation; CTA is always the immutable GridWalker ID.
type KernelEvent struct {
	Kind           string
	Cycle          uint64
	DeviceID       uint64
	CTA            uint32
	Slot           int
	Warps          isa.WarpMask
	LaunchID       uint64
	Generation     uint64 // CTA slot generation
	Warp           uint8
	Rank           uint32
	WarpGeneration uint64
}

// TakeEvents returns and clears detached events. Call after each Run, including
// a completed Run, to obtain its final reclamation/completion boundary events.
func (k *Kernel) TakeEvents() []KernelEvent {
	events := append([]KernelEvent(nil), k.events...)
	k.events = nil
	return events
}
func (k *Kernel) event(kind string, cta uint32, slot int, warps isa.WarpMask) {
	e := KernelEvent{DeviceID: k.deviceID, Kind: kind, Cycle: k.runner.Cycle(), CTA: cta, Slot: slot, Warps: warps, LaunchID: k.launchID}
	if slot >= 0 {
		e.Generation = k.generations[slot]
	}
	k.events = append(k.events, e)
}

// observeCTA exposes conservative full-generation quiescence. Hardware slot
// admission is separate: slot_valid clears at the delayed final warp_done,
// while in-flight token bindings and physical LMEM routes retain old ownership.
func (k *Kernel) observeCTA(slot int, c *KernelCTA) KernelCTA {
	out := *c
	out.Generation = k.generations[slot]
	out.StoppedWarps = 0
	out.MemoryPending = k.runner.hierarchy.system.HasResidency(k.launchID, uint64(slot))
	out.Reclaimable = c.Dispatched == k.launch.WarpsPerCTA && c.RetiredRanks == isa.WarpMask((1<<k.launch.WarpsPerCTA)-1) && !k.memory.BarrierPending(uint32(slot))
	for _, member := range c.Resident.Members {
		if member.Rank >= c.Dispatched {
			continue
		}
		w := member.WarpID
		snapshot, err := k.runner.owners[w].Snapshot()
		if err == nil && (snapshot.ActiveMask() == 0 || snapshot.Lifecycle() != state.WarpRunning) {
			out.StoppedWarps |= 1 << w
		}
		out.MemoryPending = out.MemoryPending || k.runner.hierarchy.warpPending(w)
		out.Reclaimable = out.Reclaimable && !k.runner.parked[w] && k.runner.WarpQuiescent(w)
	}
	out.Reclaimable = out.Reclaimable && !out.MemoryPending
	return out
}
