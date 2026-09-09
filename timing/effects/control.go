package effects

import (
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

// ControlAllowed supplies the existing SFU admission gate from a detached
// old-edge context. A blocked WSYNC/BAR stays before execution, so no evaluated
// wait-only bundle can be mistaken for a retired instruction. The caller must
// use the same context for this gate and Observe on the common edge.
func (a *Adapter) ControlAllowed(context state.ReadContext) bool {
	if a.failed {
		return false
	}
	if a.current == nil {
		return true
	}
	decoded := a.current.decoded
	if decoded.Barrier != isa.BarrierNone {
		return !context.PendingLSU
	}
	if decoded.Control == isa.ControlWarpSync {
		return !context.PendingPriorWork
	}
	if decoded.Control == isa.ControlWarpSpawn {
		return a.spawnBound && (a.stream == nil || !context.PendingPriorWork)
	}
	return true
}
