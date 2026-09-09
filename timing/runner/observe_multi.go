package runner

import "vortex.local/simulator/timing/model"

type WarpObservation struct {
	Active, Stalled, Runnable bool
	PC                        uint32
	Mask                      uint8
	Epoch                     uint64
	Pending, PendingLSU       int
	StallReason               string
}

func (r *MultiRunner) observeWarps(s model.SchedulerState) (result [4]WarpObservation) {
	type identity struct {
		warp      uint8
		epoch, id uint64
	}
	seen := map[identity]bool{}
	add := func(t model.Token) {
		k := identity{t.Warp, t.Epoch, t.ID}
		if !seen[k] {
			seen[k] = true
			result[t.Warp].Pending++
		}
	}
	for w, c := range s.Warps {
		result[w] = WarpObservation{Active: c.Active, Stalled: c.Stalled, Runnable: c.Active && !c.Stalled && (!s.IBufferFull[w] || s.AllIBuffersFull), PC: c.PC, Mask: c.Mask, Epoch: c.Epoch}
		switch {
		case !c.Active:
			result[w].StallReason = "inactive"
		case r.parked[w]:
			result[w].StallReason = "software-cancel"
		case c.Stalled:
			result[w].StallReason = "fetch-or-control-feedback"
		case s.IBufferFull[w] && !s.AllIBuffersFull:
			result[w].StallReason = "ibuffer-full"
		}
	}
	for _, resource := range r.core.Resources() {
		for _, entry := range resource.Residents {
			add(entry.Token)
		}
	}
	for _, p := range r.effects.Pending() {
		add(p.Token)
		if p.MemoryOutstanding {
			result[p.Token.Warp].PendingLSU++
		}
		if p.Token.WarpStall && result[p.Token.Warp].Stalled && !r.parked[p.Token.Warp] {
			result[p.Token.Warp].StallReason = "control-feedback"
		}
	}
	for _, q := range [][]request{r.fetch, r.loads} {
		for _, p := range q {
			add(p.token)
		}
	}
	for w, t := range r.blocked {
		add(t)
		result[w].StallReason = "external-barrier"
	}
	return
}
