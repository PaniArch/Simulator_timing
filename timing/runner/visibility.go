package runner

import (
	"fmt"
	"vortex.local/simulator/timing/memsys"
)

// MakeVisible explicitly flushes dirty D-cache bytes after execution completes.
// Budget exhaustion is resumable. This is a software output boundary, not FENCE.
func (r *MultiRunner) MakeVisible(budget uint64) (bool, error) {
	if r.cacheFlush != nil {
		return false, fmt.Errorf("finish FlushCaches before MakeVisible")
	}
	if !r.Completed() {
		return false, fmt.Errorf("visibility requires completed healthy execution")
	}
	if r.memoryVisible {
		return true, nil
	}
	if r.visibilityID == 0 {
		id, err := r.nextControlTransaction()
		if err != nil {
			return false, err
		}
		r.visibilityID = id
	}
	err := r.clock.Run(budget, func(cycle uint64) (bool, error) {
		e, err := r.hierarchy.system.Step(cycle, memsys.SystemInput{FetchReady: true, MemoryReady: true, DataFlush: memsys.FlushOffer{Valid: !r.visibilitySent, Identity: memsys.Identity{Kernel: 1, Transaction: r.visibilityID}, Tag: r.visibilityID}, DataFlushReady: true})
		if err != nil {
			return false, err
		}
		if err := r.core.ClockIdleCounters(); err != nil {
			return false, err
		}
		r.visibilitySent = r.visibilitySent || e.DataFlushAccepted
		for _, q := range e.WritebackErrors {
			if q.Err != nil {
				return false, q.Err
			}
		}
		if e.DataFlush.Delivered {
			if e.DataFlush.Err != nil {
				return false, e.DataFlush.Err
			}
			r.memoryVisible = true
			r.visibilitySent = false
			r.visibilityID = 0
		}
		return r.memoryVisible, nil
	})
	if err != nil {
		r.failed = true
	}
	return r.memoryVisible, err
}
