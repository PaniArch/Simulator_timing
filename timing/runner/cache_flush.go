package runner

import (
	"fmt"
	"math"
	"vortex.local/simulator/timing/memsys"
)

// cacheFlush retains D completion while I is running, like the level-held done
// signals in VX_core.sv. Component request acceptance suppresses resubmission.
type cacheFlush struct {
	dataSent, dataDone, instructionSent bool
	transaction                         uint64
}

// FlushCaches performs the external cache-control operation (D then I), not an
// ISA FENCE or an epoch reset. The caller must finish execution first; this API
// does not drain or cancel a running Core. A false result preserves the operation
// for another budget. No instructions execute on these memory-only edges.
func (r *MultiRunner) FlushCaches(budget uint64) (bool, error) {
	if !r.Completed() {
		return false, fmt.Errorf("cache flush requires completed healthy execution")
	}
	if r.visibilityID != 0 {
		return false, fmt.Errorf("finish MakeVisible before cache flush")
	}
	return r.flushCaches(budget)
}
func (r *Runner) FlushCaches(budget uint64) (bool, error) { return r.multi.FlushCaches(budget) }
func (k *Kernel) FlushCaches(budget uint64) (bool, error) {
	if k.transferred {
		return false, fmt.Errorf("kernel memory ownership transferred")
	}
	if k.failed != nil {
		return false, k.failed
	}
	if !k.Status().Complete {
		return false, fmt.Errorf("cache flush requires completed kernel execution")
	}
	if k.visibilityID != 0 && !k.visible {
		return false, fmt.Errorf("finish MakeVisible before cache flush")
	}
	done, err := k.runner.flushCaches(budget)
	if err != nil {
		k.failed = err
	}
	if done {
		k.visible = true
	}
	return done, err
}
func (r *MultiRunner) flushCaches(budget uint64) (bool, error) {
	if r.cacheFlush == nil {
		id, err := r.nextControlTransaction()
		if err != nil {
			return false, err
		}
		r.cacheFlush = &cacheFlush{transaction: id}
	}
	done := false
	err := r.clock.Run(budget, func(cycle uint64) (bool, error) {
		f := r.cacheFlush
		id := memsys.Identity{Kernel: r.launchIdentity, Transaction: f.transaction}
		// Use the pre-edge D done latch: I cannot launch on the D completion edge.
		e, err := r.hierarchy.system.Step(cycle, memsys.SystemInput{
			FetchReady: true, MemoryReady: true, DataFlushReady: true, InstructionFlushReady: true,
			DataFlush:        memsys.FlushOffer{Valid: !f.dataSent, Identity: id, Tag: f.transaction},
			InstructionFlush: memsys.FlushOffer{Valid: f.dataDone && !f.instructionSent, Identity: id, Tag: f.transaction},
		})
		if err != nil {
			return false, err
		}
		if err := r.core.ClockIdleCounters(); err != nil {
			return false, err
		}
		f.dataSent = f.dataSent || e.DataFlushAccepted
		f.instructionSent = f.instructionSent || e.InstructionFlushAccepted
		for _, q := range e.WritebackErrors {
			if q.Err != nil {
				return false, q.Err
			}
		}
		if e.DataFlush.Delivered {
			if e.DataFlush.Err != nil {
				return false, e.DataFlush.Err
			}
			f.dataDone = true
		}
		if e.InstructionFlush.Delivered {
			if e.InstructionFlush.Err != nil {
				return false, e.InstructionFlush.Err
			}
			if !f.dataDone || !f.instructionSent {
				return false, fmt.Errorf("cache flush completion out of order")
			}
			done = true
		}
		return done, nil
	})
	if err != nil {
		// Recovery retains System and drains old control replies, just as abandoned
		// MakeVisible does. A terminal component protocol error still rejects reset.
		r.failed = true
		r.cacheFlush = nil
	}
	if done {
		r.cacheFlush = nil
		r.memoryVisible = true
	}
	return done, err
}

func (r *MultiRunner) nextControlTransaction() (uint64, error) {
	if r.controlSequence == math.MaxUint64 {
		return 0, fmt.Errorf("cache control transaction overflow")
	}
	r.controlSequence++
	return r.controlSequence, nil
}
