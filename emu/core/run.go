package core

import (
	"fmt"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
)

// RunOutcome is the deterministic stop reason for a bounded Core.Run.
type RunOutcome uint8

const (
	RunComplete RunOutcome = iota
	RunBudgetExceeded
	RunBlocked
	RunDeferred
	RunTrap
	RunFault
)

func (o RunOutcome) String() string {
	switch o {
	case RunComplete:
		return "complete"
	case RunBudgetExceeded:
		return "budget-exceeded"
	case RunBlocked:
		return "blocked"
	case RunDeferred:
		return "deferred"
	case RunTrap:
		return "trap"
	case RunFault:
		return "fault"
	default:
		return fmt.Sprintf("run-outcome(%d)", o)
	}
}

// BudgetExceededError records the explicit functional step limit.
type BudgetExceededError struct {
	Budget uint64
}

func (e *BudgetExceededError) Error() string {
	if e == nil {
		return "core: nil budget error"
	}
	return fmt.Sprintf("core: execution budget %d exhausted", e.Budget)
}

// NoRunnableError means relevant work remains but every such slot is blocked.
type NoRunnableError struct{}

func (*NoRunnableError) Error() string {
	return "core: no runnable warp while relevant work remains"
}

// DeferredError retains the selected warp and its typed Core block reason.
type DeferredError struct {
	WarpID uint8
	Reason BlockReason
}

func (e *DeferredError) Error() string {
	if e == nil {
		return "core: nil deferred error"
	}
	return fmt.Sprintf("core: warp %d deferred with %s", e.WarpID, e.Reason)
}

// TraceRecord is one detached scheduler selection and Warp.Step observation.
// CoreStep is zero-based within this Run call. Lifecycle fields are scheduler
// metadata immediately before and after the selected instruction attempt.
type TraceRecord struct {
	CoreStep            uint64
	WarpID              uint8
	Lifecycle           WarpLifecycle
	PreviousBlockReason BlockReason
	NextLifecycle       WarpLifecycle
	BlockReason         BlockReason
	WarpResult          warp.Result
	CTAValid            bool
	CTAID               uint32
	Barrier             *BarrierTransition
	CTACompletion       *CTACompletionSnapshot
}

// TraceSink receives optional detached Core records.
type TraceSink interface {
	Trace(record TraceRecord)
}

// TraceFunc adapts a function to TraceSink.
type TraceFunc func(record TraceRecord)

func (f TraceFunc) Trace(record TraceRecord) {
	if f != nil {
		f(record)
	}
}

// RunOptions supplies an explicit finite functional-step budget. Context is
// evaluated once per attempted Core step; nil supplies the zero ReadContext.
type RunOptions struct {
	StepBudget uint64
	Context    func(coreStep uint64) state.ReadContext
	Trace      TraceSink
}

// RunResult summarizes bounded Core execution. Attempts counts selected
// Warp.Step calls; Retired counts only fully completed instructions. Last is
// detached and nil if no Warp was selected.
type RunResult struct {
	Outcome        RunOutcome
	Attempts       uint64
	Retired        uint64
	Last           *StepResult
	Err            error
	CTACompletions []CTACompletionSnapshot
}

// Run repeatedly invokes the existing Core.Step scheduler. Deferred, trap,
// and fault stop after preserving that exact lower-level result; a subsequent
// call may resume a functional wait after its owner view changes. An idle step
// is complete only when every participated slot is finished, otherwise it is
// explicitly blocked. The budget counts instructions, never cycles.
func (c *Core) Run(options RunOptions) (result RunResult) {
	defer func() {
		if completions, err := c.CTACompletions(); err == nil {
			result.CTACompletions = detachCTACompletions(completions)
		}
	}()
	complete, err := c.Complete()
	if err != nil {
		result.Outcome, result.Err = RunFault, err
		return result
	}
	if complete {
		result.Outcome = RunComplete
		return result
	}

	for result.Attempts < options.StepBudget {
		context := state.ReadContext{}
		if options.Context != nil {
			context = options.Context(result.Attempts)
		}
		step, stepErr := c.Step(context)
		if step.Outcome == StepIdle {
			if stepErr != nil {
				result.Outcome, result.Err = RunFault, stepErr
				return result
			}
			complete, completeErr := c.Complete()
			if completeErr != nil {
				result.Outcome, result.Err = RunFault, completeErr
			} else if complete {
				result.Outcome = RunComplete
			} else {
				result.Outcome, result.Err = RunBlocked, &NoRunnableError{}
			}
			return result
		}

		index := result.Attempts
		result.Attempts++
		detached := detachStepResult(step)
		result.Last = &detached
		if options.Trace != nil {
			options.Trace.Trace(detachedCoreTrace(index, step))
		}
		if stepErr != nil {
			result.Outcome, result.Err = RunFault, stepErr
			return result
		}

		switch step.WarpResult.Outcome {
		case warp.OutcomeRetired:
			result.Retired++
			complete, err = c.Complete()
			if err != nil {
				result.Outcome, result.Err = RunFault, err
				return result
			}
			if complete {
				result.Outcome = RunComplete
				return result
			}
		case warp.OutcomeDeferred:
			result.Outcome = RunDeferred
			result.Err = &DeferredError{WarpID: step.WarpID, Reason: step.BlockReason}
			return result
		case warp.OutcomeTrap:
			result.Outcome = RunTrap
			result.Err = step.WarpResult.Err
			return result
		case warp.OutcomeFault:
			result.Outcome = RunFault
			result.Err = step.WarpResult.Err
			return result
		default:
			result.Outcome = RunFault
			result.Err = fmt.Errorf("core: selected warp %d returned unexpected outcome %s", step.WarpID, step.WarpResult.Outcome)
			return result
		}
	}
	result.Outcome = RunBudgetExceeded
	result.Err = &BudgetExceededError{Budget: options.StepBudget}
	return result
}

func detachStepResult(result StepResult) StepResult {
	detached := result
	detached.WarpResult = warp.DetachResult(result.WarpResult)
	if result.Barrier != nil {
		barrier := *result.Barrier
		detached.Barrier = &barrier
	}
	if result.CTACompletion != nil {
		completion := detachCTACompletion(*result.CTACompletion)
		detached.CTACompletion = &completion
	}
	return detached
}

// DetachStepResult deep-copies every mutable observation reachable from one
// Core step so upper-level Device trace/result boundaries cannot alias it.
func DetachStepResult(result StepResult) StepResult { return detachStepResult(result) }

func detachedCoreTrace(step uint64, result StepResult) TraceRecord {
	detached := detachStepResult(result)
	return TraceRecord{
		CoreStep: step, WarpID: detached.WarpID,
		Lifecycle: detached.Lifecycle, PreviousBlockReason: detached.PreviousBlockReason,
		NextLifecycle: detached.NextLifecycle, BlockReason: detached.BlockReason,
		WarpResult: detached.WarpResult, CTAValid: detached.CTAValid, CTAID: detached.CTAID,
		Barrier: detached.Barrier, CTACompletion: detached.CTACompletion,
	}
}

// TraceRecordFromStep constructs the same detached Core trace record used by
// Core.Run for an upper-level scheduler which directly calls Core.Step.
func TraceRecordFromStep(step uint64, result StepResult) TraceRecord {
	return detachedCoreTrace(step, result)
}

func detachCTACompletion(completion CTACompletionSnapshot) CTACompletionSnapshot {
	completion.Members = append([]CTACompletionMember(nil), completion.Members...)
	return completion
}

func detachCTACompletions(completions []CTACompletionSnapshot) []CTACompletionSnapshot {
	result := make([]CTACompletionSnapshot, len(completions))
	for index, completion := range completions {
		result[index] = detachCTACompletion(completion)
	}
	return result
}
