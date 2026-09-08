package model

import (
	"fmt"
	akita "github.com/sarchlab/akita/v5/timing"
	"math"
	"vortex.local/simulator/timing"
)

// TokenOptions describes an explicit diagnostic service, not RTL memory
// latency. Token streams do not execute branch targets or functional effects.
type TokenOptions struct {
	Backend                                     string
	PeriodPS, FetchCycles, MemoryCycles, Budget uint64
}
type CycleReport struct {
	Cycle uint64
	CoreReport
}
type TokenRun struct {
	Cycles    []CycleReport
	Completed int
}
type serviceToken struct {
	signal Signal
	due    uint64
}

// RunTokens exercises the whole connected core using Akita. Only one decoded
// instruction (possibly multiple packed uops) is active. A new instruction waits
// for all its eop notifications, internal drain and the external store tail.
func RunTokens(words []uint32, o TokenOptions) (TokenRun, error) {
	run := TokenRun{}
	if o.FetchCycles == 0 || o.MemoryCycles == 0 || o.Budget == 0 || o.FetchCycles > math.MaxUint64-o.Budget || o.MemoryCycles > math.MaxUint64-o.Budget {
		return run, fmt.Errorf("positive bounded service delays and cycle budget required")
	}
	c, err := NewCore(o.Backend)
	if err != nil {
		return run, err
	}
	clock, err := NewClock(akita.VTimeInPicoSec(o.PeriodPS))
	if err != nil {
		return run, err
	}
	lanes, err := timing.Number("config", "cfg-baseline", "values", "threads_per_warp")
	if err != nil {
		return run, err
	}
	if lanes < 1 || lanes > 8 {
		return run, fmt.Errorf("unsupported lanes")
	}
	if len(words) == 0 {
		return run, nil
	}
	var fetch, memory []serviceToken
	offered := Signal{}
	active := false
	finished := false
	eops := map[uint8]bool{}
	err = clock.Run(o.Budget, func(edge uint64) (bool, error) {
		// Stores have no core response but keep the external service non-idle.
		tail := memory[:0]
		for _, r := range memory {
			if r.signal.Token.Path != STORE || r.due > edge {
				tail = append(tail, r)
			}
		}
		memory = tail
		if !active && run.Completed < len(words) && c.Idle(len(fetch) == 0 && len(memory) == 0) {
			offered = Signal{Valid: true, Token: Token{ID: uint64(run.Completed + 1), PC: uint32(run.Completed * 4), Mask: uint8((1 << lanes) - 1)}}
			active = true
			finished = false
			eops = map[uint8]bool{}
		}
		fr, mr := Response{}, Response{}
		if len(fetch) > 0 && fetch[0].due <= edge {
			fr = responseFor(fetch[0].signal)
			fr.Word = words[fr.ID-1]
		}
		mi := -1
		for i, r := range memory {
			if r.signal.Token.Path != STORE && r.due <= edge {
				mi = i
				mr = responseFor(r.signal)
				break
			}
		}
		p, err := c.Evaluate(CoreInputs{Instruction: offered, FetchResponse: fr, MemoryResponse: mr, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: len(memory) == 0})
		if err != nil {
			return false, err
		}
		if err = CommitEdge(p.Transition); err != nil {
			return false, err
		}
		r := p.Report
		run.Cycles = append(run.Cycles, CycleReport{edge, r})
		if r.InstructionAccepted {
			offered = Signal{}
		}
		if fr.Valid && r.FetchResponseReady {
			fetch = fetch[1:]
		}
		if mr.Valid && r.MemoryResponseReady {
			memory = append(memory[:mi], memory[mi+1:]...)
		}
		if r.FetchAccepted {
			fetch = append(fetch, serviceToken{r.FetchRequest, edge + o.FetchCycles})
		}
		if r.MemoryAccepted {
			memory = append(memory, serviceToken{r.MemoryRequest, edge + o.MemoryCycles})
		}
		if r.PendingRelease.Valid {
			tok := r.PendingRelease.Token
			if !active || tok.ID != uint64(run.Completed+1) || eops[tok.Uop] {
				return false, fmt.Errorf("unexpected or duplicate pending notification")
			}
			eops[tok.Uop] = true
			finished = len(eops) == int(tok.Uops)
		}
		if active && finished && c.Idle(len(fetch) == 0 && len(memory) == 0) {
			run.Completed++
			active = false
		}
		return run.Completed == len(words), nil
	})
	if err != nil {
		return run, err
	}
	if run.Completed != len(words) {
		return run, fmt.Errorf("cycle budget exhausted after %d/%d tokens", run.Completed, len(words))
	}
	return run, nil
}
func responseFor(s Signal) Response {
	return Response{Valid: s.Valid, ID: s.Token.ID, Epoch: s.Token.Epoch, Warp: s.Token.Warp, Uop: s.Token.Uop, Mask: s.Token.Mask}
}
