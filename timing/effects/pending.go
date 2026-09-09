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

// DrainBefore answers the old-edge predicates for a control instruction in
// this Warp. Decode/issue are ordered per Warp, so when that control reaches SFU
// every older instruction has an entry here or has already been fully reaped.
// Frontend-only younger tokens do not constitute prior work. Caller-owned
// external predicates must be ORed with these, not replaced by them.
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
