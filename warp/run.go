package warp

import (
	"fmt"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
)

// RunOutcome is the deterministic stop reason for a bounded Run.
type RunOutcome uint8

const (
	RunBudgetExceeded RunOutcome = iota
	RunFault
	RunTrap
	RunDeferred
	RunFinished
)

func (o RunOutcome) String() string {
	switch o {
	case RunBudgetExceeded:
		return "budget-exceeded"
	case RunFault:
		return "fault"
	case RunTrap:
		return "trap"
	case RunDeferred:
		return "deferred"
	case RunFinished:
		return "finished"
	default:
		return fmt.Sprintf("run-outcome(%d)", o)
	}
}

// BudgetExceededError records the explicit finite limit which stopped Run.
type BudgetExceededError struct {
	Budget uint64
}

func (e *BudgetExceededError) Error() string {
	if e == nil {
		return "warp: nil budget error"
	}
	return fmt.Sprintf("warp: execution budget %d exhausted", e.Budget)
}

// TraceRecord is a detached observation of one attempted Step. Step is a
// zero-based attempt index. The scalar start/end SIMT observations are copied
// from Result, and Decoded/Effects are deep copies, so a sink cannot alter
// execution, the last Result, or canonical state.
type TraceRecord struct {
	Step              uint64
	WarpID            uint8
	PC                uint32
	ActiveMask        isa.LaneMask
	Lifecycle         state.WarpLifecycle
	DivergencePointer uint8
	Raw               uint32
	RawValid          bool
	Decoded           *isa.Decoded
	// IssuedEffects observes requests before owner completion; Effects observes
	// the final completion/apply bundle.
	IssuedEffects         *isa.InstructionEffects
	Effects               *isa.InstructionEffects
	NextPC                uint32
	NextActiveMask        isa.LaneMask
	NextLifecycle         state.WarpLifecycle
	NextDivergencePointer uint8
	Outcome               Outcome
}

// TraceSink receives optional correctness/debug records. A nil sink is the
// default and produces no trace or logging side effects.
type TraceSink interface {
	Trace(record TraceRecord)
}

// TraceFunc adapts a function to TraceSink and is convenient for slog or the
// repository's support/logging package at an application boundary.
type TraceFunc func(record TraceRecord)

func (f TraceFunc) Trace(record TraceRecord) {
	if f != nil {
		f(record)
	}
}

// RunOptions supplies an explicit finite budget and optional per-attempt
// external context/trace views. A nil Context supplies the zero ReadContext.
type RunOptions struct {
	StepBudget uint64
	Context    func(step uint64) state.ReadContext
	Trace      TraceSink
}

// RunResult summarizes a bounded execution. Attempts counts every Step call;
// Retired counts only OutcomeRetired progress. Last is nil when budget is zero.
type RunResult struct {
	Outcome  RunOutcome
	Attempts uint64
	Retired  uint64
	Last     *Result
	Err      error
}

// Run repeatedly calls Step and never maintains a PC. Each attempt therefore
// starts from the current canonical WarpState owner. Retired progress continues
// until the explicit budget; every other Step outcome stops immediately.
func (w *Warp) Run(options RunOptions) RunResult {
	result := RunResult{}
	for result.Attempts < options.StepBudget {
		index := result.Attempts
		context := state.ReadContext{}
		if options.Context != nil {
			context = options.Context(index)
		}
		step := w.Step(context)
		result.Attempts++
		result.Last = &step
		if options.Trace != nil {
			options.Trace.Trace(detachedTrace(index, step))
		}
		if step.Outcome == OutcomeRetired {
			result.Retired++
			continue
		}
		switch step.Outcome {
		case OutcomeTrap:
			result.Outcome = RunTrap
		case OutcomeDeferred:
			result.Outcome = RunDeferred
		case OutcomeFinished:
			result.Outcome = RunFinished
		default:
			result.Outcome = RunFault
		}
		result.Err = step.Err
		return result
	}
	result.Outcome = RunBudgetExceeded
	result.Err = &BudgetExceededError{Budget: options.StepBudget}
	return result
}

func detachedTrace(step uint64, result Result) TraceRecord {
	detached := DetachResult(result)
	return TraceRecord{
		Step: step, WarpID: detached.WarpID, PC: detached.PC,
		ActiveMask: detached.ActiveMask, Lifecycle: detached.Lifecycle, DivergencePointer: detached.DivergencePointer,
		Raw: detached.Raw, RawValid: detached.RawValid,
		Decoded: detached.Decoded, IssuedEffects: detached.IssuedEffects,
		Effects: detached.Effects, NextPC: detached.NextPC,
		NextActiveMask: detached.NextActiveMask, NextLifecycle: detached.NextLifecycle,
		NextDivergencePointer: detached.NextDivergencePointer, Outcome: detached.Outcome,
	}
}

// DetachResult deep-copies every mutable observation reachable from Result.
// It is shared by Warp and Core trace boundaries so a sink cannot mutate the
// caller's result, an effect bundle, or canonical owners through aliases.
func DetachResult(result Result) Result {
	detached := result
	detached.Decoded = cloneDecoded(result.Decoded)
	detached.IssuedEffects = cloneInstructionEffects(result.IssuedEffects)
	detached.Effects = cloneInstructionEffects(result.Effects)
	if result.Fault != nil {
		fault := *result.Fault
		fault.Architectural = append([]isa.FaultEffect(nil), result.Fault.Architectural...)
		detached.Fault = &fault
		if result.Err == result.Fault {
			detached.Err = detached.Fault
		}
	}
	return detached
}

func cloneDecoded(decoded *isa.Decoded) *isa.Decoded {
	if decoded == nil {
		return nil
	}
	copy := *decoded
	copy.Sources = append([]isa.Register(nil), decoded.Sources...)
	copy.Destinations = append([]isa.Register(nil), decoded.Destinations...)
	return &copy
}

func cloneInstructionEffects(effects *isa.InstructionEffects) *isa.InstructionEffects {
	if effects == nil {
		return nil
	}
	copy := &isa.InstructionEffects{
		RegisterWrites: append([]isa.RegisterWriteEffect(nil), effects.RegisterWrites...),
		MemoryRequests: append([]isa.MemoryRequest(nil), effects.MemoryRequests...),
		CSRReads:       append([]isa.CSRReadEffect(nil), effects.CSRReads...),
		CSRWrites:      append([]isa.CSRWriteEffect(nil), effects.CSRWrites...),
		WarpMasks:      append([]isa.WarpMaskEffect(nil), effects.WarpMasks...),
		WarpDrains:     append([]isa.WarpDrainEffect(nil), effects.WarpDrains...),
		Barriers:       append([]isa.BarrierEffect(nil), effects.Barriers...),
		PackedLoads:    append([]isa.PackedLoadRequest(nil), effects.PackedLoads...),
		Faults:         append([]isa.FaultEffect(nil), effects.Faults...),
	}
	copy.Control = cloneEffectPointer(effects.Control)
	copy.Ordering = cloneEffectPointer(effects.Ordering)
	copy.FFlags = cloneEffectPointer(effects.FFlags)
	copy.Trap = cloneEffectPointer(effects.Trap)
	copy.WarpSpawn = cloneEffectPointer(effects.WarpSpawn)
	copy.Divergence = cloneEffectPointer(effects.Divergence)
	return copy
}

func cloneEffectPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
