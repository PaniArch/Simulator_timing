package runner

import (
	"fmt"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// Release queues an explicit coordinator wake for the next core edge. The
// barrier owner decides when to release; this runner does not count CTA members
// or implement barrier phases. The complete blocked token must match exactly.
func (r *MultiRunner) Release(token model.Token) error {
	if r.failed {
		return fmt.Errorf("failed runner requires reset")
	}
	saved, ok := r.blocked[token.Warp]
	if !ok || saved != token {
		return fmt.Errorf("unknown, stale or inconsistent external release")
	}
	for _, pending := range r.releases {
		if pending == token {
			return fmt.Errorf("duplicate queued release")
		}
	}
	r.releases = append(r.releases, token)
	return nil
}
func (r *MultiRunner) externalFeedback() []model.SchedulerFeedback {
	var result []model.SchedulerFeedback
	for _, token := range r.releases {
		result = append(result, model.SchedulerFeedback{Token: token, Kind: model.FeedbackWake})
	}
	return result
}
func (r *MultiRunner) trackExternalWait(report model.CoreReport) error {
	if !report.Control.Valid {
		return nil
	}
	t := report.Control.Token
	d, err := isa.Decode(t.Word)
	if err != nil {
		return err
	}
	if d.Barrier == isa.BarrierSync || d.Barrier == isa.BarrierWait {
		if _, exists := r.blocked[t.Warp]; exists {
			return fmt.Errorf("duplicate external wait")
		}
		r.blocked[t.Warp] = t
	}
	return nil
}

// Normal RTL wstall produces no younger work. This defensive software cleanup
// covers an explicitly injected speculative/recovery context, and runs BEFORE
// services so a cancelled younger store cannot reach its byte owner this edge.
func (r *MultiRunner) cleanupRedirects(cycle uint64) error {
	branch, control := r.core.FeedbackSignals()
	feedback, err := r.effects.Feedback(cycle, branch, control)
	if err != nil {
		return err
	}
	return r.cleanupFeedback(feedback)
}
func (r *MultiRunner) cleanupFeedback(feedback []model.SchedulerFeedback) error {
	for _, f := range feedback {
		if f.Kind == model.FeedbackWake {
			continue
		}
		through := f.Token.ID
		consider := func(t model.Token) {
			if t.Warp == f.Token.Warp && t.Epoch == f.Token.Epoch && t.ID > through {
				through = t.ID
			}
		}
		for _, resource := range r.core.Resources() {
			for _, p := range resource.Residents {
				consider(p.Token)
			}
		}
		for _, p := range r.effects.Pending() {
			consider(p.Token)
		}
		for _, queue := range [][]request{r.fetch, r.loads} {
			for _, p := range queue {
				consider(p.token)
			}
		}
		if through > f.Token.ID {
			if err := r.Cancel(model.Cancellation{Warp: f.Token.Warp, Epoch: f.Token.Epoch, After: f.Token.ID, Through: through}); err != nil {
				return err
			}
		}
	}
	return nil
}
