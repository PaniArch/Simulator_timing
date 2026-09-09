package runner

import (
	"encoding/binary"
	"fmt"
	akita "github.com/sarchlab/akita/v5/timing"
	"math"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

type MultiOptions struct {
	Spawn func(model.Token) (effects.SpawnBinding, error) // explicit pre-issue owner binding
	Options
	Contexts func() [4]state.ReadContext
	// MemoryDelay selects an explicit external service delay, never cache timing.
	// Nil uses MemoryCycles. A response remains stable until accepted.
	MemoryDelay func(model.Token) uint64
}
type MultiRecord struct {
	Warps          [4]WarpObservation
	Events         []StageEvent
	Cancelled      []model.Token // effect contexts removed since the previous edge
	Cycle          uint64
	Report         model.CoreReport
	InFlight       int
	Finished       []model.Token
	Retired        [4]uint64
	ResourcesAfter []model.ResourceState
	Services       []ServiceState
}

// MultiRunner drives scheduled Core admission from four explicit owners. Fetch
// and data services are queues keyed by token identity, not a single current
// instruction. Only final completion uses Core.Idle; ordinary admission never does.
type MultiRunner struct {
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
	if memory == nil || options.FetchCycles == 0 || options.MemoryCycles == 0 {
		return nil, fmt.Errorf("owners and explicit positive service delays required")
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
	adapter, err := effects.NewConcurrent(owners, 1, memory, external)
	if err != nil {
		return nil, err
	}
	return &MultiRunner{blocked: map[uint8]model.Token{}, epoch: 1, owners: owners, memory: memory, core: core, clock: clock, effects: adapter, options: options}, nil
}
func (r *MultiRunner) Cycle() uint64      { return r.clock.Cycle() }
func (r *MultiRunner) Completed() bool    { return r.stopped && !r.failed }
func (r *MultiRunner) Retired() [4]uint64 { return r.retired }
func (r *MultiRunner) InFlight() int      { return r.effects.InFlight() }
func (r *MultiRunner) Run(budget uint64, observe func(MultiRecord)) error {
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
func dueRequest(queue []request, cycle uint64) int {
	found := -1
	for n, p := range queue {
		if p.due <= cycle && (found < 0 || p.due < queue[found].due) {
			found = n
		}
	}
	return found
}
func (r *MultiRunner) step(cycle uint64) (MultiRecord, error) {
	if err := r.cleanupRedirects(cycle); err != nil {
		return MultiRecord{Cycle: cycle}, err
	}
	record := MultiRecord{Events: append([]StageEvent(nil), r.recoveryEvents...), Cycle: cycle, Cancelled: append([]model.Token(nil), r.cancelled...)}
	r.cancelled = nil
	r.recoveryEvents = nil
	if !r.fetchResponse.Valid {
		if n := dueRequest(r.fetch, cycle); n >= 0 {
			token := r.fetch[n].token
			var data [4]byte
			if err := r.memory.Read(token.PC, data[:]); err != nil {
				return record, &warp.Fault{Kind: warp.FaultInstructionAccess, PC: token.PC, Cause: err}
			}
			r.fetchResponse = model.Response{Valid: true, ID: token.ID, Epoch: token.Epoch, Warp: token.Warp, Mask: token.Mask, Word: binary.LittleEndian.Uint32(data[:])}
			r.fetch = append(r.fetch[:n], r.fetch[n+1:]...)
		}
	}
	if !r.response.Valid {
		if n := dueRequest(r.loads, cycle); n >= 0 {
			token := r.loads[n].token
			response, err := r.effects.Service(cycle, token, token.Mask)
			if err != nil {
				return record, err
			}
			r.response = response
			r.loads = append(r.loads[:n], r.loads[n+1:]...)
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
	ready := true
	if r.options.Ready != nil {
		ready = r.options.Ready(cycle)
	}
	control := r.core.ControlInput()
	context := state.ReadContext{}
	if control.Valid {
		context = contexts[control.Token.Warp]
		prior, lsu := r.effects.DrainBefore(control.Token)
		context.PendingPriorWork = context.PendingPriorWork || prior
		context.PendingLSU = context.PendingLSU || lsu
		contexts[control.Token.Warp] = context
	}
	branch, simt := r.core.FeedbackSignals()
	feedback, err := r.effects.Feedback(cycle, branch, simt)
	if err != nil {
		return record, err
	}
	feedback = append(feedback, r.externalFeedback()...)
	p, err := r.core.Evaluate(model.CoreInputs{Feedback: feedback, FetchResponse: r.fetchResponse, MemoryResponse: r.response, FetchReady: ready, MemoryReady: ready, ControlAllowed: r.effects.ControlAllowed(control, context)})
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
	if p.Report.FetchAccepted {
		if r.options.FetchCycles > math.MaxUint64-cycle {
			return record, fmt.Errorf("fetch due overflow")
		}
		r.fetch = append(r.fetch, request{p.Report.FetchRequest.Token, cycle + r.options.FetchCycles})
	}
	if p.Report.MemoryAccepted {
		token := p.Report.MemoryRequest.Token
		delay := r.options.MemoryCycles
		if r.options.MemoryDelay != nil {
			delay = r.options.MemoryDelay(token)
		}
		if delay == 0 || delay > math.MaxUint64-cycle {
			return record, fmt.Errorf("invalid memory service delay")
		}
		r.loads = append(r.loads, request{token, cycle + delay})
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
	allStopped := true
	for _, owner := range r.owners {
		s, err := owner.Snapshot()
		if err != nil {
			return record, err
		}
		allStopped = allStopped && (s.ActiveMask() == 0 || s.Lifecycle() != state.WarpRunning)
	}
	r.stopped = allStopped && r.effects.InFlight() == 0 && r.core.Idle(len(r.fetch) == 0 && len(r.loads) == 0 && !r.fetchResponse.Valid && !r.response.Valid)
	return record, nil
}
