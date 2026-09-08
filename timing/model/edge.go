package model

import "fmt"

// revision and mutation are private to components. Orchestration can combine
// their proposals but cannot access next-state images or install them directly.
type revision struct{ value uint64 }
type mutation struct {
	owner   *revision
	version uint64
	apply   func()
}

func (r *revision) propose(apply func()) mutation { return mutation{r, r.value, apply} }

// Transition contains detached handshake observations and private owner edits.
type Transition struct {
	InputReady bool
	Output     Signal
	Accepted   bool
	Completed  bool
	edits      []mutation
}

func transfer(input, output Signal, ready, downstream bool) Transition {
	return Transition{InputReady: ready, Output: output, Accepted: input.Valid && ready, Completed: output.Valid && downstream}
}

// CommitEdge rejects all stale/duplicate proposals before changing any owner.
// The caller computes every proposal against old state, then invokes this once
// per common edge. No component can observe another component's partial update.
func CommitEdge(transitions ...Transition) error {
	seen := map[*revision]bool{}
	for _, t := range transitions {
		if len(t.edits) == 0 {
			return fmt.Errorf("empty component transition")
		}
		for _, edit := range t.edits {
			if edit.owner == nil || edit.apply == nil || seen[edit.owner] {
				return fmt.Errorf("invalid or duplicate component transition")
			}
			seen[edit.owner] = true
			if edit.version != edit.owner.value {
				return fmt.Errorf("stale component transition")
			}
		}
	}
	for _, t := range transitions {
		for _, edit := range t.edits {
			edit.apply()
			edit.owner.value++
		}
	}
	return nil
}
