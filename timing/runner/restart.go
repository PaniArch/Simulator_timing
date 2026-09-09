package runner

import (
	"fmt"
	"math"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/timing/model"
)

// Restart resumes a previously cancelled Warp with explicit frontend context.
// It changes no canonical state and preserves older work and other Warps.
func (r *MultiRunner) Restart(warp uint8, context model.WarpContext) error {
	if r.failed || warp >= 4 || !r.parked[warp] {
		return fmt.Errorf("restart requires cancelled healthy warp")
	}
	for _, p := range r.effects.Pending() {
		if p.Token.Warp == warp && p.Token.WarpStall {
			return fmt.Errorf("restart retains control receipt")
		}
	}
	p, err := r.core.Restart(warp, context, r.cancelThrough[warp])
	if err != nil {
		return err
	}
	if err = model.CommitEdge(p); err != nil {
		return err
	}
	r.parked[warp] = false
	r.stopped = false
	r.recoveryEvents = append(r.recoveryEvents, StageEvent{Resource: "scheduler", Kind: "restart", Token: model.Token{Warp: warp, Epoch: context.Epoch, PC: context.PC, Mask: context.Mask}, Reason: "explicit-context"})
	return nil
}

// Flush abandons all un-delivered work, advances the residency epoch, and
// restarts from each live canonical owner. Visible effects are not rolled back.
// This explicit software reset does not promise replay of abandoned instructions.
func (r *MultiRunner) Flush() error {
	if r.epoch == math.MaxUint64 {
		return fmt.Errorf("epoch overflow")
	}
	var contexts [4]model.WarpContext
	for w, owner := range r.owners {
		s, err := owner.Snapshot()
		if err != nil {
			return err
		}
		contexts[w] = model.WarpContext{Active: s.Lifecycle() == state.WarpRunning && s.ActiveMask() != 0, PC: s.PC(), Mask: uint8(s.ActiveMask()), Epoch: r.epoch + 1}
	}
	core, err := model.NewScheduledCore(r.options.Backend, contexts)
	if err != nil {
		return err
	}
	if r.failed {
		if err = r.clock.Run(1, func(uint64) (bool, error) { return true, nil }); err != nil {
			return err
		}
	}
	events := r.cancellationEvents(func(model.Token) bool { return true }, "epoch-flush")
	pending := r.effects.Pending()
	if err = r.effects.Reset(r.epoch + 1); err != nil {
		return err
	}
	for _, entry := range pending {
		r.cancelled = append(r.cancelled, entry.Token)
	}
	r.recoveryEvents = append(r.recoveryEvents, events...)
	r.core = core
	r.epoch++
	r.fetch = nil
	r.loads = nil
	r.fetchResponse = model.Response{}
	r.response = model.Response{}
	r.blocked = map[uint8]model.Token{}
	r.releases = nil
	r.parked = [4]bool{}
	r.cancelThrough = [4]uint64{}
	r.stopped = false
	r.failed = false
	return nil
}

func (r *MultiRunner) cancellationEvents(matches func(model.Token) bool, reason string) []StageEvent {
	var events []StageEvent
	add := func(resource string, t model.Token) {
		if matches(t) {
			events = append(events, StageEvent{Resource: resource, Kind: "cancel", Token: t, Reason: reason})
		}
	}
	for _, resource := range r.core.Resources() {
		for _, entry := range resource.Residents {
			add(resource.ID, entry.Token)
		}
	}
	for w := uint8(0); w < 4; w++ {
		if t, ok := r.blocked[w]; ok {
			add("external-wait", t)
		}
	}
	for _, p := range r.fetch {
		add("fetch-service", p.token)
	}
	for _, p := range r.loads {
		add("memory-service", p.token)
	}
	// Held service responses still alias a Core tag/context. Recover its full
	// token rather than inventing Word/PC metadata from the response identity.
	for _, item := range []struct {
		name     string
		response model.Response
	}{{"fetch-response", r.fetchResponse}, {"memory-response", r.response}} {
		if !item.response.Valid {
			continue
		}
		found := false
		for _, resource := range r.core.Resources() {
			for _, entry := range resource.Residents {
				t := entry.Token
				v := item.response
				if !found && t.ID == v.ID && t.Epoch == v.Epoch && t.Warp == v.Warp && t.Uop == v.Uop {
					t.Mask = v.Mask
					add(item.name, t)
					found = true
				}
			}
		}
	}
	return events
}
