package effects

import "vortex.local/simulator/timing/model"

// PendingInstruction is a detached macro-instruction receipt summary. A final
// pending notification alone is insufficient: stores can still have service
// tails and packed operations retain per-uop/fragment work until fully drained.
type PendingInstruction struct {
	Token             model.Token
	PendingReleased   bool
	MemoryOutstanding bool
}

func (c *Concurrent) Pending() []PendingInstruction {
	var result []PendingInstruction
	for _, key := range c.keys() {
		a := c.entries[key]
		i := a.current
		memory := i.token.Class == 1 && !i.pending || i.memory != nil && !a.memoryFinished() || i.packed != nil && !a.packedFinished()
		result = append(result, PendingInstruction{Token: i.token, PendingReleased: i.pending, MemoryOutstanding: memory})
	}
	return result
}

// DrainBefore summarizes older software receipts for diagnostics only. It is
// not a hardware pending or LSU-drained predicate and must not gate control
// execution. MultiRunner uses Core hardware accounting and LSU state instead.
func (c *Concurrent) DrainBefore(token model.Token) (prior, lsu bool) {
	for _, entry := range c.Pending() {
		if entry.Token.Warp != token.Warp || entry.Token.Epoch != token.Epoch || entry.Token.ID >= token.ID {
			continue
		}
		prior = true
		lsu = lsu || entry.MemoryOutstanding
	}
	return
}
