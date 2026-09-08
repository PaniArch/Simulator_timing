package effects

import (
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

// Preserve the original functional boundary: architectural faults are typed
// stop reasons, not guessed traps or writes to canonical state. The caller
// flushes the pipeline and cancels services; already visible fragments remain.
func architecturalFault(pc uint32, faults []isa.FaultEffect, cause error) error {
	return &warp.Fault{Kind: warp.FaultArchitectural, PC: pc, Cause: cause, Architectural: append([]isa.FaultEffect(nil), faults...)}
}
