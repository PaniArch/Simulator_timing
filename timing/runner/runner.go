// Package runner supplies scheduled execution backed by the real memory hierarchy.
package runner

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

type Options struct {
	TraceMemory bool // capture detached accepted fragment/transport events
	// DataMemory is the explicit lifetime-stable local route for a single Warp.
	DataMemory    warp.MemoryService
	MemoryConfig  *memsys.Config        // nil selects the IR external-backend default for every runner
	MemoryBackend memsys.BackendFactory // nil retains the fixed-latency backend

	Backend  string
	PeriodPS uint64
	// Deprecated: ignored. Cache resources and MemoryConfig determine service timing.
	FetchCycles, MemoryCycles uint64
	// Ready is a deterministic request throttle. In the hierarchy path it
	// gates new offers; already exposed offers remain valid until accepted.
	Ready    func(cycle uint64) bool
	Context  func() state.ReadContext
	External effects.ExternalOwner
}
type ServiceState struct {
	Resource string
	Token    model.Token
	Due      uint64
}
type Record struct {
	Services       []ServiceState
	Subject        model.Token
	ResourcesAfter []model.ResourceState
	Events         []StageEvent
	Cycle          uint64
	Epoch          uint64
	Phase          string
	Completed      bool
	Report         model.CoreReport
}
type request struct {
	token model.Token
	due   uint64
}

// Runner uses the same scheduled Core and memory system as MultiRunner, with
// only the caller's Warp initially active. No per-instruction drain is inserted.
type Runner struct {
	multi    *MultiRunner
	selected uint8
}

func New(owner *state.WarpState, memory warp.MemoryService, options Options) (*Runner, error) {
	if owner == nil {
		return nil, fmt.Errorf("warp owner required")
	}
	snapshot, err := owner.Snapshot()
	if err != nil {
		return nil, err
	}
	selected := snapshot.WarpID()
	if selected >= 4 {
		return nil, fmt.Errorf("warp outside frozen topology")
	}
	var owners [4]*state.WarpState
	for w := uint8(0); w < 4; w++ {
		if w == selected {
			owners[w] = owner
			continue
		}
		initial := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, Lifecycle: state.WarpInactive}
		for lane := uint8(0); lane < 4; lane++ {
			initial.Lanes = append(initial.Lanes, state.LaneInitial{ID: lane})
		}
		owners[w], err = state.NewWarp(initial)
		if err != nil {
			return nil, err
		}
	}
	opts := MultiOptions{Options: options}
	opts.DataMemory[selected] = options.DataMemory
	multi, err := NewMulti(owners, memory, opts)
	if err != nil {
		return nil, err
	}
	return &Runner{multi: multi, selected: selected}, nil
}
func (r *Runner) Retired() uint64 { return r.multi.Retired()[r.selected] }
func (r *Runner) Cycle() uint64   { return r.multi.Cycle() }
func (r *Runner) Completed() bool { return r.multi.Completed() }
func (r *Runner) Pending() bool {
	m := r.multi
	return m.effects.InFlight() != 0 || !m.core.Idle(!m.fetchResponse.Valid && !m.response.Valid) || !m.hierarchy.drained()
}

// Flush snapshots the caller's current PC/mask into a new execution epoch.
// Set any redirect before calling it. Already exposed memory transfers survive;
// their old architectural results are discarded and their effects are retained.
func (r *Runner) Flush() error                            { return r.multi.Flush() }
func (r *Runner) MakeVisible(budget uint64) (bool, error) { return r.multi.MakeVisible(budget) }

// Release forwards the exact blocked BAR token supplied to the external coordinator.
func (r *Runner) Release(token model.Token) error { return r.multi.Release(token) }

// Run preserves queues and clock state when the budget expires.
func (r *Runner) Run(budget uint64, observe func(Record)) error {
	return r.multi.Run(budget, func(m MultiRecord) {
		if observe == nil {
			return
		}
		record := Record{Cycle: m.Cycle, Epoch: r.multi.epoch, Phase: "pipeline", Report: m.Report,
			ResourcesAfter: m.ResourcesAfter, Events: m.Events, Services: m.Services, Completed: r.Completed()}
		if len(m.Finished) != 0 {
			record.Phase = "instruction-complete"
			record.Subject = m.Finished[len(m.Finished)-1]
		}
		if record.Completed {
			record.Phase = "finished"
		}
		observe(record)
	})
}
