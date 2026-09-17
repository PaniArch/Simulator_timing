package runner

import (
	"fmt"
	akita "github.com/sarchlab/akita/v5/timing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

type MultiOptions struct {
	MemorySystem *MemorySystemOptions // nil selects IR defaults with lifetime-stable per-Warp routes

	// DataMemory supplies optional Warp-specific data routes. Nil entries use
	// the global memory argument. Fetch always uses that global argument.
	// Routes must retain their CTA binding while requests/effects remain live.
	DataMemory [4]warp.MemoryService
	Spawn      func(model.Token) (effects.SpawnBinding, error) // explicit pre-issue owner binding
	Options
	Contexts func() [4]state.ReadContext
	// MemoryDelay is a deprecated compatibility field; real memory ignores it.
	MemoryDelay func(model.Token) uint64
}
type MultiRecord struct {
	DeviceID, LaunchID uint64
	Bindings           [4]WarpBinding    // detached physical-to-logical binding for every token on this edge
	Memory             memsys.SystemEdge // detached memory handshakes; fragments require TraceMemory
	Counters           isa.CounterView   // old-edge hardware scheduler counters
	Warps              [4]WarpObservation
	Events             []StageEvent
	Cancelled          []model.Token // effect contexts removed since the previous edge
	Cycle              uint64
	Report             model.CoreReport
	InFlight           int
	Finished           []model.Token
	Retired            [4]uint64
	ResourcesAfter     []model.ResourceState
	Services           []ServiceState
}

// MultiRunner drives scheduled Core admission from four explicit owners. Fetch
// and data services are queues keyed by token identity, not a single current
// instruction. Only final completion uses Core.Idle; ordinary admission never does.
type MultiRunner struct {
	dispatchBusy                  bool // Kernel's actual pre-edge CTA DISPATCH/admission signal
	launchIdentity                uint64
	controlSequence, visibilityID uint64
	cacheFlush                    *cacheFlush
	hierarchy                     *runnerMemory
	visibilitySent, memoryVisible bool

	blocked                 map[uint8]model.Token
	releases                []model.Token
	epoch                   uint64
	parked                  [4]bool
	cancelThrough           [4]uint64
	recoveryEvents          []StageEvent
	cancelled               []model.Token
	owners                  [4]*state.WarpState
	memory                  warp.MemoryService
	core                    *model.Core
	effects                 *effects.Concurrent
	clock                   *model.Clock
	options                 MultiOptions
	fetch, loads            []request
	fetchResponse, response model.Response
	retired                 [4]uint64
	stopped, failed         bool
}

func NewMulti(owners [4]*state.WarpState, memory warp.MemoryService, options MultiOptions) (*MultiRunner, error) {
	if memory == nil {
		return nil, fmt.Errorf("memory owner required")
	}
	if options.MemorySystem == nil {
		config, err := memsys.DefaultConfig()
		if err != nil {
			return nil, err
		}
		if options.MemoryConfig != nil {
			config = *options.MemoryConfig
		}
		// Static standalone bindings retain the supplied routes for this runner's
		// lifetime. Dynamic CTA allocation must use explicit MemorySystem callbacks.
		routes := options.DataMemory
		options.MemorySystem = &MemorySystemOptions{
			Config: config,
			Bind: func(t model.Token) memsys.Identity {
				return memsys.Identity{Kernel: 1, CTA: uint64(t.Warp), WarpGeneration: 1}
			},
			LocalOwner: func(id memsys.Identity) (warp.AtomicMemoryService, error) {
				if id.Kernel != 1 || id.Warp >= 4 || id.CTA != uint64(id.Warp) || id.WarpGeneration != 1 {
					return nil, fmt.Errorf("invalid static local binding")
				}
				owner, ok := routes[id.Warp].(warp.AtomicMemoryService)
				if !ok {
					return nil, fmt.Errorf("local access requires an explicit lifetime-stable atomic data route")
				}
				return owner, nil
			},
		}
	}
	var warps [4]model.WarpContext
	for w, owner := range owners {
		if owner == nil {
			return nil, fmt.Errorf("four explicit warp owners required")
		}
		s, err := owner.Snapshot()
		if err != nil {
			return nil, err
		}
		if int(s.WarpID()) != w {
			return nil, fmt.Errorf("warp owner/slot mismatch")
		}
		warps[w] = model.WarpContext{Active: s.Lifecycle() == state.WarpRunning && s.ActiveMask() != 0, PC: s.PC(), Mask: uint8(s.ActiveMask()), Epoch: 1}
	}
	core, err := model.NewScheduledCore(options.Backend, warps)
	if err != nil {
		return nil, err
	}
	clock, err := model.NewClock(akita.VTimeInPicoSec(options.PeriodPS))
	if err != nil {
		return nil, err
	}
	external := [4]effects.ExternalOwner{options.External, options.External, options.External, options.External}
	routes := options.DataMemory
	for w := range routes {
		if routes[w] == nil {
			routes[w] = memory
		}
	}
	adapter, err := effects.NewConcurrentWithMemory(owners, 1, routes, external)
	if err != nil {
		return nil, err
	}
	r := &MultiRunner{launchIdentity: 1, blocked: map[uint8]model.Token{}, epoch: 1, owners: owners, memory: memory, core: core, clock: clock, effects: adapter, options: options}
	if options.MemorySystem != nil {
		r.hierarchy, err = newRunnerMemory(memory, options.MemorySystem)
		if err != nil {
			return nil, err
		}
	}
	return r, nil
}
func (r *MultiRunner) Cycle() uint64      { return r.clock.Cycle() }
func (r *MultiRunner) Completed() bool    { return r.stopped && !r.failed }
func (r *MultiRunner) Retired() [4]uint64 { return r.retired }
func (r *MultiRunner) InFlight() int      { return r.effects.InFlight() }
func (r *MultiRunner) Run(budget uint64, observe func(MultiRecord)) error {
	if r.cacheFlush != nil {
		return fmt.Errorf("finish FlushCaches before Run")
	}
	if r.failed {
		return fmt.Errorf("multi-warp runner stopped after failed edge")
	}
	if r.stopped {
		return nil
	}
	err := r.clock.Run(budget, func(cycle uint64) (bool, error) {
		record, err := r.step(cycle)
		if observe != nil {
			observe(record)
		}
		return r.stopped, err
	})
	if err != nil {
		r.failed = true
	}
	return err
}
func (r *MultiRunner) step(cycle uint64) (MultiRecord, error) {
	r.memoryVisible = false
	if err := r.cleanupRedirects(cycle); err != nil {
		return MultiRecord{Cycle: cycle}, err
	}
	record := MultiRecord{Counters: isa.CounterView{Cycle: r.core.Cycles(), Instret: r.core.Instret()}, Events: append([]StageEvent(nil), r.recoveryEvents...), Cycle: cycle, Cancelled: append([]model.Token(nil), r.cancelled...)}
	r.cancelled = nil
	r.recoveryEvents = nil
	if r.hierarchy != nil {
		if err := r.hierarchy.receive(r, cycle); err != nil {
			return record, err
		}
	}
	var contexts [4]state.ReadContext
	if r.options.Contexts != nil {
		contexts = r.options.Contexts()
	} else if r.options.Context != nil {
		c := r.options.Context()
		for w := range contexts {
			contexts[w] = c
		}
	}
	// CSR snapshots observe model state before this edge, independently of
	// software receipt completion and caller-provided CTA/barrier context.
	for w := range contexts {
		contexts[w].Counters = isa.CounterView{Cycle: r.core.Cycles(), Instret: r.core.Instret()}
		contexts[w].ActiveWarps = r.core.ActiveWarps()
	}
	ready := true
	if r.options.Ready != nil {
		ready = r.options.Ready(cycle)
	}
	control := r.core.ControlInput()
	context := state.ReadContext{}
	if control.Valid {
		context = contexts[control.Token.Warp]
		// WSYNC samples the registered pending count (including itself).
		// BAR samples the shared LSU scheduler request queue, not service tails.
		context.PendingPriorWork = r.core.HardwarePending()[control.Token.Warp] > 1
		context.PendingLSU = !r.core.LSUSchedulerDrained()

		contexts[control.Token.Warp] = context
	}
	branch, simt := r.core.FeedbackSignals()
	feedback, err := r.effects.Feedback(cycle, branch, simt, r.core.SingleActive())
	if err != nil {
		return record, err
	}
	feedback = append(feedback, r.externalFeedback()...)
	inputs := model.CoreInputs{DispatchBusy: r.dispatchBusy, Feedback: feedback, FetchResponse: r.fetchResponse, MemoryResponse: r.response, FetchReady: ready, MemoryReady: ready, ControlAllowed: r.effects.ControlAllowed(control, context)}
	if r.hierarchy != nil {
		inputs.FetchReady = false
		inputs.MemoryReady = false
	}
	p, err := r.core.Evaluate(inputs)
	if err == nil && r.hierarchy != nil {
		var edgeErr error
		edge, e := r.hierarchy.step(r, cycle, p.Report)
		record.Memory = edge
		edgeErr = e
		if edgeErr != nil {
			return record, edgeErr
		}
		inputs.FetchReady = edge.FetchAccepted
		inputs.MemoryReady = edge.MemoryAccepted
		p, err = r.core.Evaluate(inputs)
	}
	if err != nil {
		return record, err
	}
	record.Warps = r.observeWarps(p.Report.Scheduler)
	if control.Valid && !r.effects.ControlAllowed(control, context) {
		record.Warps[control.Token.Warp].StallReason = "control-drain"
	}
	if p.Report.Decoded.Valid {
		tok := p.Report.Decoded.Token
		decoded, e := isa.Decode(tok.Word)
		if e != nil {
			return record, e
		}
		if decoded.Control == isa.ControlWarpSpawn && r.options.Spawn != nil {
			binding, e := r.options.Spawn(tok)
			if e != nil {
				return record, e
			}
			err = r.effects.BeginSpawn(tok, binding)
		} else {
			err = r.effects.Begin(tok)
		}
		if err != nil {
			return record, err
		}
	}
	if err = model.CommitEdge(p.Transition); err != nil {
		return record, err
	}
	if err = r.effects.Observe(cycle, p.Report, contexts); err != nil {
		return record, err
	}
	for _, f := range p.Report.Wakeups {
		r.parked[f.Token.Warp] = false
		delete(r.blocked, f.Token.Warp)
	}
	r.releases = nil
	if err = r.trackExternalWait(p.Report); err != nil {
		return record, err
	}
	record.Report = p.Report
	record.InFlight = r.effects.InFlight()
	if r.fetchResponse.Valid && p.Report.FetchResponseReady {
		r.fetchResponse = model.Response{}
	}
	if r.response.Valid && p.Report.MemoryResponseReady {
		r.response = model.Response{}
	}
	record.Finished, err = r.effects.Reap()
	if err != nil {
		return record, err
	}
	for _, token := range record.Finished {
		r.retired[token.Warp]++
	}
	record.Retired = r.retired
	record.ResourcesAfter = r.core.Resources()
	record.Events = append(record.Events, stageEvents(p.Report.Resources, record.ResourcesAfter)...)
	record.Events = append(record.Events, boundaryEvents(p.Report)...)
	for _, f := range p.Report.Wakeups {
		record.Events = append(record.Events, StageEvent{Resource: "scheduler", Kind: "wakeup", Token: f.Token, Reason: string(f.Kind)})
	}
	for _, entry := range r.fetch {
		record.Services = append(record.Services, ServiceState{"fetch-service", entry.token, entry.due})
	}
	for _, entry := range r.loads {
		record.Services = append(record.Services, ServiceState{"memory-service", entry.token, entry.due})
	}
	if r.hierarchy != nil {
		record.Services = append(record.Services, r.hierarchy.services()...)
	}
	allStopped := true
	for _, owner := range r.owners {
		s, err := owner.Snapshot()
		if err != nil {
			return record, err
		}
		allStopped = allStopped && (s.ActiveMask() == 0 || s.Lifecycle() != state.WarpRunning)
	}
	memoryDrained := r.hierarchy == nil || r.hierarchy.drained()
	r.stopped = memoryDrained && allStopped && r.effects.InFlight() == 0 && r.core.Idle(len(r.fetch) == 0 && len(r.loads) == 0 && !r.fetchResponse.Valid && !r.response.Valid)
	return record, nil
}
