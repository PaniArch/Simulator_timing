package effects

import (
	"fmt"
	"vortex.local/simulator/timing/model"
)

// Cancel discards un-delivered receipts only. It cannot roll back a register,
// CSR, control or store effect that was already made visible. Identities remain
// monotonic, so removing an entry never permits its replay via Begin.
func (c *Concurrent) Cancel(scope model.Cancellation) ([]model.Token, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if c.failed {
		return nil, fmt.Errorf("failed effects require residency reset")
	}
	c.invalidations = append(c.invalidations, scope)
	var cancelled []model.Token
	for _, key := range c.keys() {
		a := c.entries[key]
		if scope.Matches(a.current.token) {
			cancelled = append(cancelled, a.current.token)
			cancelEngine(a)
			delete(c.entries, key)
		}
	}
	return cancelled, nil
}
