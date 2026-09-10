package runner

import (
	"fmt"
	"vortex.local/simulator/timing/model"
)

// Cancel is a software recovery boundary between clock edges. It parks the
// selected Warp while preserving all other work and already-visible effects.
// In the hierarchy path, exposed SIMD offers are irrevocable: remaining lanes
// still transfer after cancellation, but their architectural results are dropped.
// Through must bound identities already issued; it is not an open-ended ban.
// A later explicit restart/owner coordination is separate from cancellation.
func (r *MultiRunner) Cancel(scope model.Cancellation) error {
	if r.cacheFlush != nil {
		return fmt.Errorf("finish FlushCaches before changing residency")
	}
	if r.failed {
		return fmt.Errorf("failed runner requires residency reset")
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	p, err := r.core.Cancel(scope)
	if err != nil {
		return err
	}
	events := r.cancellationEvents(scope.Matches, "selective-cancel")
	if err = model.CommitEdge(p); err != nil {
		return err
	}
	tokens, err := r.effects.Cancel(scope)
	if err != nil {
		return err
	}
	if r.hierarchy != nil {
		r.hierarchy.cancel(scope)
	}
	r.cancelled = append(r.cancelled, tokens...)
	r.recoveryEvents = append(r.recoveryEvents, events...)
	r.parked[scope.Warp] = true
	if scope.Through > r.cancelThrough[scope.Warp] {
		r.cancelThrough[scope.Warp] = scope.Through
	}
	filter := func(q []request) []request {
		var next []request
		for _, p := range q {
			if !scope.Matches(p.token) {
				next = append(next, p)
			}
		}
		return next
	}
	r.fetch, r.loads = filter(r.fetch), filter(r.loads)
	matches := func(response model.Response) bool {
		return response.Valid && scope.Matches(model.Token{Warp: response.Warp, Epoch: response.Epoch, ID: response.ID})
	}
	if matches(r.fetchResponse) {
		r.fetchResponse = model.Response{}
	}
	if matches(r.response) {
		r.response = model.Response{}
	}
	for w, t := range r.blocked {
		if scope.Matches(t) {
			delete(r.blocked, w)
		}
	}
	var retained []model.Token
	for _, t := range r.releases {
		if !scope.Matches(t) {
			retained = append(retained, t)
		}
	}
	r.releases = retained
	r.stopped = false
	return nil
}
