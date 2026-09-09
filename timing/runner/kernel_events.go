package runner

import (
	"fmt"
	"sort"
	"vortex.local/simulator/emu/core"
)

// BarrierEvent identifies one expected external completion. Tickets come from
// PendingBarrierEvents; they are bound to this launch, CTA generation and one
// expectation, not just the reusable hardware barrier address.
type BarrierEvent struct {
	CTA, Slot              uint32
	AddressWarp, BarrierID uint8
	Sequence               uint64
	owner                  *Kernel
}

func (k *Kernel) PendingBarrierEvents() []BarrierEvent {
	events := make([]BarrierEvent, 0, len(k.barrierEvents))
	for event := range k.barrierEvents {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence < events[j].Sequence })
	return events
}

// CompleteBarrierEvent accepts a ticket once, between edges or in the observer.
// Released waiters are queued for the following pipeline edge. Invalid tickets
// do not modify coordinator state or poison the resumable execution.
func (k *Kernel) CompleteBarrierEvent(event BarrierEvent) error {
	if k.failed != nil {
		return k.failed
	}
	if event.owner != k || !k.barrierEvents[event] || event.Slot >= 4 {
		return fmt.Errorf("unknown or consumed barrier event")
	}
	resident := k.resident[event.Slot]
	if resident == nil || resident.Launch.ID != event.CTA {
		return fmt.Errorf("stale CTA generation in barrier event")
	}
	stage, err := k.memory.Barriers().StageEventCompletion(core.BarrierKey{CTAID: event.Slot, AddressWarp: event.AddressWarp, ID: event.BarrierID})
	if err != nil {
		return err
	}
	result := stage.Result()
	if err := stage.Commit(); err != nil {
		return err
	}
	delete(k.barrierEvents, event)
	k.barrierReleases |= result.Releases
	if err := k.releaseBarriers(); err != nil {
		return err
	}
	k.event("barrier-complete", event.CTA, int(event.Slot), result.Releases)
	return nil
}
