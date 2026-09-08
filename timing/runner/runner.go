// Package runner supplies conservative single-active-warp program execution.
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

type Options struct {
	Backend                             string
	PeriodPS, FetchCycles, MemoryCycles uint64
	// Ready is an optional deterministic external request-port backpressure input.
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
type Runner struct {
	cancelled                      []StageEvent
	identity                       model.Token
	previous                       []model.ResourceState
	owner                          *state.WarpState
	memory                         warp.MemoryService
	core                           *model.Core
	adapter                        *effects.Adapter
	clock                          *model.Clock
	options                        Options
	epoch, nextID                  uint64
	active, begun, stopped, failed bool
	offer                          model.Signal
	fetch                          *request
	loads                          []request
	response                       model.Response
	pending                        map[uint8]bool
	uops                           uint8
	retired                        uint64
}

func New(owner *state.WarpState, memory warp.MemoryService, options Options) (*Runner, error) {
	if owner == nil || memory == nil || options.FetchCycles == 0 || options.MemoryCycles == 0 {
		return nil, fmt.Errorf("owner, memory and explicit positive service delays required")
	}
	core, err := model.NewCore(options.Backend)
	if err != nil {
		return nil, err
	}
	clock, err := model.NewClock(akita.VTimeInPicoSec(options.PeriodPS))
	if err != nil {
		return nil, err
	}
	adapter, err := effects.New(owner, 1, options.External)
	if err != nil {
		return nil, err
	}
	if err = adapter.BindMemory(memory); err != nil {
		return nil, err
	}
	return &Runner{owner: owner, memory: memory, core: core, adapter: adapter, clock: clock, options: options, epoch: 1, nextID: 1}, nil
}
func (r *Runner) Retired() uint64 { return r.retired }
func (r *Runner) Cycle() uint64   { return r.clock.Cycle() }
func (r *Runner) Completed() bool { return r.stopped && !r.failed }
func (r *Runner) Pending() bool {
	return r.active || r.fetch != nil || len(r.loads) != 0 || r.response.Valid || !r.core.Idle(true)
}

// Flush cancels transient work and future service calls; visible effects are not
// rolled back. Canonical PC/mask remain owned by the caller. No old request is
// retained to execute after the new epoch begins.
func (r *Runner) Flush() error {
	if r.epoch == math.MaxUint64 {
		return fmt.Errorf("epoch overflow")
	}
	// Akita's driver leaves a failed edge number unchanged. Consume that edge
	// before resetting residency so the next Observe remains strictly later.
	if r.failed {
		if err := r.clock.Run(1, func(uint64) (bool, error) { return true, nil }); err != nil {
			return err
		}
	}
	if err := model.CommitEdge(r.core.Flush()); err != nil {
		return err
	}
	if err := r.adapter.Reset(r.epoch + 1); err != nil {
		return err
	}
	for _, resource := range r.previous {
		for _, entry := range resource.Residents {
			r.cancelled = append(r.cancelled, StageEvent{Resource: resource.ID, Kind: "cancel", Token: entry.Token, Reason: "epoch-flush"})
		}
	}
	if r.fetch != nil {
		r.cancelled = append(r.cancelled, StageEvent{Resource: "fetch-service", Kind: "cancel", Token: r.fetch.token, Reason: "epoch-flush"})
	}
	for _, request := range r.loads {
		r.cancelled = append(r.cancelled, StageEvent{Resource: "memory-service", Kind: "cancel", Token: request.token, Reason: "epoch-flush"})
	}
	r.epoch++
	r.active = false
	r.begun = false
	r.stopped = false
	r.failed = false
	r.offer = model.Signal{}
	r.fetch = nil
	r.loads = nil
	r.response = model.Response{}
	r.pending = nil
	r.previous = nil
	r.identity = model.Token{}
	return nil
}

// Run stops at completion or budget exhaustion. Budget exhaustion preserves all
// queues and clock state for continuation. observe receives detached reports.
func (r *Runner) Run(budget uint64, observe func(Record)) error {
	if r.failed {
		return fmt.Errorf("runner requires flush after fault")
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
func (r *Runner) step(cycle uint64) (Record, error) {
	record := Record{Cycle: cycle, Epoch: r.epoch, Phase: "pipeline", Events: r.cancelled}
	r.cancelled = nil
	if !r.active && r.core.Idle(true) {
		s, err := r.owner.Snapshot()
		if err != nil {
			return record, err
		}
		if s.ActiveMask() == 0 || s.Lifecycle() != state.WarpRunning {
			r.stopped = true
			record.Completed = true
			record.Phase = "finished"
			return record, nil
		}
		if s.PC()&3 != 0 {
			return record, &warp.Fault{Kind: warp.FaultInstructionAlignment, PC: s.PC()}
		}
		if r.nextID == math.MaxUint64 {
			return record, fmt.Errorf("instruction ID overflow")
		}
		r.offer = model.Signal{Valid: true, Token: model.Token{ID: r.nextID, Epoch: r.epoch, Warp: s.WarpID(), PC: s.PC(), Mask: uint8(s.ActiveMask())}}
		r.identity = r.offer.Token
		r.nextID++
		r.active = true
		r.pending = map[uint8]bool{}
		r.uops = 1
	}
	record.Subject = r.identity
	fr := model.Response{}
	if r.fetch != nil && r.fetch.due <= cycle {
		token := r.fetch.token
		if !r.begun {
			var bytes [4]byte
			if err := r.memory.Read(token.PC, bytes[:]); err != nil {
				return record, &warp.Fault{Kind: warp.FaultInstructionAccess, PC: token.PC, Cause: err}
			}
			token.Word = binary.LittleEndian.Uint32(bytes[:])
			r.fetch.token = token
			r.identity = token
			record.Subject = token
			decoded, decodeErr := isa.Decode(token.Word)
			if decodeErr != nil {
				return record, &warp.Fault{Kind: warp.FaultIllegalInstruction, PC: token.PC, Cause: decodeErr}
			}
			if decoded.Control == isa.ControlWarpSpawn {
				return record, fmt.Errorf("program runner requires caller-managed spawn residency; use effects.BindSpawn")
			}
			if err := r.adapter.Begin(token); err != nil {
				return record, err
			}
			if decoded.Memory.Packed != 0 {
				r.uops = decoded.Memory.Packed
			}
			r.begun = true
		}
		token = r.fetch.token
		fr = model.Response{Valid: true, ID: token.ID, Epoch: token.Epoch, Warp: token.Warp, Mask: token.Mask, Word: token.Word}
	}
	if !r.response.Valid && len(r.loads) > 0 && r.loads[0].due <= cycle {
		token := r.loads[0].token
		response, err := r.adapter.Service(cycle, token, token.Mask)
		if err != nil {
			return record, err
		}
		record.Events = append(record.Events, StageEvent{Resource: "memory-service", Kind: "complete", Token: token})
		r.response = response
		r.loads = r.loads[1:]
	}
	context := state.ReadContext{}
	if r.options.Context != nil {
		context = r.options.Context()
	}
	ready := true
	if r.options.Ready != nil {
		ready = r.options.Ready(cycle)
	}
	p, err := r.core.Evaluate(model.CoreInputs{Instruction: r.offer, FetchResponse: fr, MemoryResponse: r.response, FetchReady: ready, MemoryReady: ready, Eligible: true, ControlAllowed: r.adapter.ControlAllowed(context)})
	if err != nil {
		return record, err
	}
	if err = model.CommitEdge(p.Transition); err != nil {
		return record, err
	}
	record.Report = p.Report
	after := r.core.Resources()
	record.ResourcesAfter = after
	record.Events = append(record.Events, stageEvents(r.previous, after)...)
	record.Events = append(record.Events, boundaryEvents(p.Report)...)
	for index := range record.Events {
		event := &record.Events[index]
		if event.Kind == "stay" && event.Reason == "awaiting-transfer" {
			if !ready && (event.Resource == "b-lsu-req" || event.Resource == "b-fetch-request") {
				event.Reason = "external-backpressure"
			}
			if !r.adapter.ControlAllowed(context) && event.Resource == "b-sfu-dispatch" {
				event.Reason = "control-drain"
			}
		}
	}
	if fr.Valid && p.Report.FetchResponseReady {
		record.Events = append(record.Events, StageEvent{Resource: "decode", Kind: "complete", Token: r.identity})
	}
	r.previous = r.core.Resources()
	if err = r.adapter.Observe(cycle, p.Report, context); err != nil {
		return record, err
	}
	if p.Report.InstructionAccepted {
		r.offer = model.Signal{}
	}
	if fr.Valid && p.Report.FetchResponseReady {
		r.fetch = nil
	}
	if r.response.Valid && p.Report.MemoryResponseReady {
		r.response = model.Response{}
	}
	if p.Report.FetchAccepted {
		if cycle > math.MaxUint64-r.options.FetchCycles {
			return record, fmt.Errorf("fetch due overflow")
		}
		r.fetch = &request{p.Report.FetchRequest.Token, cycle + r.options.FetchCycles}
	}
	if p.Report.MemoryAccepted {
		if cycle > math.MaxUint64-r.options.MemoryCycles {
			return record, fmt.Errorf("memory due overflow")
		}
		r.loads = append(r.loads, request{p.Report.MemoryRequest.Token, cycle + r.options.MemoryCycles})
	}
	if p.Report.PendingRelease.Valid {
		r.pending[p.Report.PendingRelease.Token.Uop] = true
	}
	if r.active && len(r.pending) == int(r.uops) && r.core.Idle(r.fetch == nil && len(r.loads) == 0 && !r.response.Valid) {
		if err = r.adapter.Finish(); err != nil {
			return record, err
		}
		r.retired++
		r.active = false
		r.begun = false
		record.Phase = "instruction-complete"
	} else if !ready {
		record.Phase = "external-backpressure"
	} else if r.fetch != nil {
		record.Phase = "fetch-service-wait"
	} else if len(r.loads) != 0 {
		record.Phase = "memory-service-wait"
	}
	if r.fetch != nil {
		record.Services = append(record.Services, ServiceState{"fetch-service", r.fetch.token, r.fetch.due})
	}
	for _, request := range r.loads {
		record.Services = append(record.Services, ServiceState{"memory-service", request.token, request.due})
	}
	return record, nil
}
