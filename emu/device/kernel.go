package device

import (
	"errors"
	"fmt"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

// KernelOutcome is the typed stop reason for one bounded Kernel execution
// interval. Blocked, deferred, and budget outcomes may be resumed through a
// KernelExecution; KernelExecutor.Run remains the fresh-run convenience API.
type KernelOutcome uint8

const (
	KernelComplete KernelOutcome = iota
	KernelBudgetExceeded
	KernelBlocked
	KernelDeferred
	KernelTrap
	KernelFault
)

func (o KernelOutcome) String() string {
	switch o {
	case KernelComplete:
		return "complete"
	case KernelBudgetExceeded:
		return "budget-exceeded"
	case KernelBlocked:
		return "blocked"
	case KernelDeferred:
		return "deferred"
	case KernelTrap:
		return "trap"
	case KernelFault:
		return "fault"
	default:
		return fmt.Sprintf("kernel-outcome(%d)", o)
	}
}

type KernelBudgetExceededError struct{ Budget uint64 }

func (e *KernelBudgetExceededError) Error() string {
	if e == nil {
		return "device: nil kernel budget error"
	}
	return fmt.Sprintf("device: kernel execution budget %d exhausted", e.Budget)
}

type KernelBlockedError struct {
	ResidentCTAs   uint32
	PendingCluster uint32
}

func (e *KernelBlockedError) Error() string {
	if e == nil {
		return "device: nil kernel blocked error"
	}
	return fmt.Sprintf("device: kernel has no runnable warp (resident CTAs=%d, pending cluster=%d)", e.ResidentCTAs, e.PendingCluster)
}

type KernelDeferredError struct {
	WarpID uint8
	Reason core.BlockReason
}

func (e *KernelDeferredError) Error() string {
	if e == nil {
		return "device: nil kernel deferred error"
	}
	return fmt.Sprintf("device: kernel warp %d deferred with %s", e.WarpID, e.Reason)
}

// CTAEventKind identifies the three deterministic grid/residency boundaries.
type CTAEventKind uint8

const (
	CTAGenerated CTAEventKind = iota
	CTAAdmitted
	CTACompleted
)

func (k CTAEventKind) String() string {
	switch k {
	case CTAGenerated:
		return "generated"
	case CTAAdmitted:
		return "admitted"
	case CTACompleted:
		return "completed"
	default:
		return fmt.Sprintf("cta-event(%d)", k)
	}
}

// CTAEvent relates an immutable KMU generation record to its temporary Core
// resident slot. Resident is valid for admission/completion and detached.
type CTAEvent struct {
	Kind          CTAEventKind
	CTA           CTA
	ResidentValid bool
	Resident      core.CTASnapshot
}

// KernelTraceKind separates CTA ownership events from existing Core records.
type KernelTraceKind uint8

const (
	KernelTraceCTA KernelTraceKind = iota
	KernelTraceCore
)

// KernelTraceRecord is one detached, monotonically sequenced observation.
type KernelTraceRecord struct {
	Sequence uint64
	Kind     KernelTraceKind
	CTA      *CTAEvent
	Core     *core.TraceRecord
}

type KernelTraceSink interface {
	TraceKernel(record KernelTraceRecord)
}

type KernelTraceFunc func(record KernelTraceRecord)

func (f KernelTraceFunc) TraceKernel(record KernelTraceRecord) {
	if f != nil {
		f(record)
	}
}

// KernelRunOptions supplies the explicit instruction-attempt budget. Context
// is sampled once per Core.Step; nil supplies the zero functional view.
type KernelRunOptions struct {
	StepBudget uint64
	Context    func(attempt uint64) state.ReadContext
	Trace      KernelTraceSink
}

// KernelRunResult never contains memory bytes: output remains observable only
// through the exact caller-provided backing owner.
type KernelRunResult struct {
	Outcome   KernelOutcome
	Complete  bool
	Attempts  uint64
	Retired   uint64
	Generated uint32
	Admitted  uint32
	Completed uint32
	CTAEvents []CTAEvent
	Last      *KernelTraceRecord
	LastCore  *core.StepResult
	Err       error
}

// BackingMemory is the caller-owned canonical byte service used for the image,
// fetch, arguments, global data, atomic SIMT stores, and final output.
type BackingMemory interface {
	Read(address uint32, destination []byte) error
	Write(address uint32, source []byte) error
	WriteBatch(addresses []uint32, sources [][]byte) error
}

// KernelExecutor retains only a normalized launch value and the caller's one
// canonical global memory service. KernelExecutor.Run creates fresh transient
// owners; NewExecution creates an explicitly resumable owner session.
type KernelExecutor struct {
	launch LaunchState
	memory BackingMemory
}

func NewKernelExecutor(input LaunchState, memory BackingMemory) (*KernelExecutor, error) {
	if memory == nil {
		return nil, fmt.Errorf("device: kernel executor requires canonical atomic backing memory")
	}
	launch, err := ValidateLaunch(input)
	if err != nil {
		return nil, err
	}
	return &KernelExecutor{launch: launch, memory: memory}, nil
}

func (e *KernelExecutor) Launch() LaunchState {
	if e == nil {
		return LaunchState{}
	}
	return e.launch
}

type kernelRuntime struct {
	core     *core.Core
	walker   *GridWalker
	pending  []CTA
	resident map[uint32]CTA
}

// KernelExecution owns one set of transient Kernel/Core/CTA/Warp scheduling
// state. Run may be called again after a budget, blocked, or deferred outcome;
// it never recreates those owners or replaces the executor's backing memory.
// A caller that wants a fresh launch uses KernelExecutor.Run or NewExecution.
type KernelExecution struct {
	runtime  *kernelRuntime
	launch   LaunchState
	sequence uint64
	complete bool
}

// NewExecution creates all transient execution owners before any instruction
// is attempted. The caller-owned backing memory is referenced directly and is
// neither cleared nor snapshotted.
func (e *KernelExecutor) NewExecution() (*KernelExecution, error) {
	if e == nil || e.memory == nil {
		return nil, fmt.Errorf("device: nil kernel executor")
	}
	runtime, err := e.newRuntime()
	if err != nil {
		return nil, err
	}
	return &KernelExecution{runtime: runtime, launch: e.launch}, nil
}

// CompleteBarrierEvent applies an external functional event to this exact
// execution's canonical Core barrier owner. A following Run resumes the same
// Warp PCs, CTA membership, barriers, registers, and LMEM allocations.
func (e *KernelExecution) CompleteBarrierEvent(key core.BarrierKey) error {
	if e == nil || e.runtime == nil || e.runtime.core == nil {
		return fmt.Errorf("device: nil kernel execution")
	}
	if e.complete {
		return fmt.Errorf("device: completed kernel execution has no pending barrier event")
	}
	return e.runtime.core.CompleteBarrierEvent(key)
}

// Run executes only through GridWalker -> Core -> Warp -> ISA/effect owners.
// Memory calls are synchronous: a Core.Step cannot return until every issued
// fetch/load/store/batch call has completed or failed.
func (e *KernelExecutor) Run(options KernelRunOptions) (result KernelRunResult) {
	execution, err := e.NewExecution()
	if err != nil {
		result.Outcome, result.Err = KernelFault, err
		return result
	}
	return execution.Run(options)
}

// Run advances one existing execution until completion or one typed stop.
// Counters and CTAEvents describe this call; trace Sequence is monotonically
// increasing across every call on the same KernelExecution.
func (e *KernelExecution) Run(options KernelRunOptions) (result KernelRunResult) {
	if e == nil || e.runtime == nil || e.runtime.core == nil || e.runtime.walker == nil {
		result.Outcome, result.Err = KernelFault, fmt.Errorf("device: nil kernel execution")
		return result
	}
	if e.complete {
		result.Outcome, result.Complete = KernelComplete, true
		return result
	}
	runtime := e.runtime
	emitEvent := func(event CTAEvent) {
		event = detachCTAEvent(event)
		result.CTAEvents = append(result.CTAEvents, detachCTAEvent(event))
		recordEvent := detachCTAEvent(event)
		record := KernelTraceRecord{Sequence: e.sequence, Kind: KernelTraceCTA, CTA: &recordEvent}
		result.Last = &record
		if options.Trace != nil {
			sinkEvent := detachCTAEvent(event)
			options.Trace.TraceKernel(KernelTraceRecord{Sequence: e.sequence, Kind: KernelTraceCTA, CTA: &sinkEvent})
		}
		e.sequence++
	}
	emitCore := func(attempt uint64, step core.StepResult) {
		resultStep := core.DetachStepResult(step)
		result.LastCore = &resultStep
		coreRecord := core.TraceRecordFromStep(attempt, step)
		record := KernelTraceRecord{Sequence: e.sequence, Kind: KernelTraceCore, Core: &coreRecord}
		result.Last = &record
		if options.Trace != nil {
			sinkRecord := core.TraceRecordFromStep(attempt, step)
			options.Trace.TraceKernel(KernelTraceRecord{Sequence: e.sequence, Kind: KernelTraceCore, Core: &sinkRecord})
		}
		e.sequence++
	}

	for {
		if err := runtime.reclaimCompleted(&result, emitEvent); err != nil {
			result.Outcome, result.Err = KernelFault, err
			return result
		}
		complete, err := runtime.complete()
		if err != nil {
			result.Outcome, result.Err = KernelFault, err
			return result
		}
		if complete {
			e.complete = true
			result.Outcome, result.Complete = KernelComplete, true
			return result
		}

		if len(runtime.pending) == 0 && runtime.walker.Remaining() != 0 {
			for index := uint32(0); index < e.launch.ClusterSize; index++ {
				cta, ok := runtime.walker.Next()
				if !ok {
					result.Outcome, result.Err = KernelFault, fmt.Errorf("device: grid walker ended inside a cluster")
					return result
				}
				runtime.pending = append(runtime.pending, cta)
				result.Generated++
				emitEvent(CTAEvent{Kind: CTAGenerated, CTA: cta})
			}
		}

		if len(runtime.pending) != 0 {
			configs := make([]core.CTAConfig, len(runtime.pending))
			for index, cta := range runtime.pending {
				configs[index] = cta.CoreConfig()
			}
			admitted, admitErr := runtime.core.AdmitCTACluster(configs)
			if admitErr == nil {
				for index, snapshot := range admitted {
					cta := runtime.pending[index]
					runtime.resident[snapshot.ID] = cta
					result.Admitted++
					emitEvent(CTAEvent{Kind: CTAAdmitted, CTA: cta, ResidentValid: true, Resident: snapshot})
				}
				runtime.pending = nil
				continue
			}
			if !errors.Is(admitErr, core.ErrCTAResourcesUnavailable) {
				result.Outcome, result.Err = KernelFault, admitErr
				return result
			}
			if len(runtime.resident) == 0 {
				result.Outcome, result.Err = KernelFault, fmt.Errorf("device: validated cluster cannot be admitted to an empty core: %w", admitErr)
				return result
			}
		}

		if result.Attempts >= options.StepBudget {
			result.Outcome = KernelBudgetExceeded
			result.Err = &KernelBudgetExceededError{Budget: options.StepBudget}
			return result
		}
		context := state.ReadContext{}
		if options.Context != nil {
			context = options.Context(result.Attempts)
		}
		step, stepErr := runtime.core.Step(context)
		if step.Outcome == core.StepIdle {
			if stepErr != nil {
				result.Outcome, result.Err = KernelFault, stepErr
				return result
			}
			result.Outcome = KernelBlocked
			result.Err = &KernelBlockedError{ResidentCTAs: uint32(len(runtime.resident)), PendingCluster: uint32(len(runtime.pending))}
			return result
		}
		attempt := result.Attempts
		result.Attempts++
		emitCore(attempt, step)
		if stepErr != nil {
			result.Outcome, result.Err = KernelFault, stepErr
			return result
		}
		switch step.WarpResult.Outcome {
		case warp.OutcomeRetired:
			result.Retired++
		case warp.OutcomeDeferred:
			result.Outcome = KernelDeferred
			result.Err = &KernelDeferredError{WarpID: step.WarpID, Reason: step.BlockReason}
			return result
		case warp.OutcomeTrap:
			result.Outcome, result.Err = KernelTrap, step.WarpResult.Err
			return result
		case warp.OutcomeFault:
			result.Outcome, result.Err = KernelFault, step.WarpResult.Err
			return result
		default:
			result.Outcome = KernelFault
			result.Err = fmt.Errorf("device: Core selected warp %d with unexpected outcome %s", step.WarpID, step.WarpResult.Outcome)
			return result
		}
	}
}

func (e *KernelExecutor) newRuntime() (*kernelRuntime, error) {
	manager := core.NewDynamicCTAManager()
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		lanes := make([]state.LaneInitial, isa.FrozenLaneCount)
		for lane := range lanes {
			lanes[lane].ID = uint8(lane)
		}
		owner, err := state.NewWarp(state.WarpInitial{
			Topology: state.FrozenTopology(), WarpID: id, PC: e.launch.StartupPC,
			Lifecycle: state.WarpInactive, Lanes: lanes,
		})
		if err != nil {
			return nil, fmt.Errorf("device: initialize warp %d: %w", id, err)
		}
		route, err := core.NewCTAMemory(manager, id, e.memory)
		if err != nil {
			return nil, fmt.Errorf("device: route warp %d memory: %w", id, err)
		}
		executors[id], err = warp.NewWithServices(owner, e.memory, route)
		if err != nil {
			return nil, fmt.Errorf("device: create warp %d executor: %w", id, err)
		}
	}
	coreOwner, err := core.NewWithCTAManager(executors, manager)
	if err != nil {
		return nil, fmt.Errorf("device: create dynamic core: %w", err)
	}
	walker, err := NewGridWalker(e.launch)
	if err != nil {
		return nil, err
	}
	return &kernelRuntime{core: coreOwner, walker: walker, resident: make(map[uint32]CTA)}, nil
}

func (r *kernelRuntime) reclaimCompleted(result *KernelRunResult, emit func(CTAEvent)) error {
	completions, err := r.core.CTACompletions()
	if err != nil {
		return err
	}
	for _, completion := range completions {
		if !completion.Complete {
			continue
		}
		cta, exists := r.resident[completion.CTAID]
		if !exists {
			return fmt.Errorf("device: resident CTA slot %d has no generation record", completion.CTAID)
		}
		reclaimed, err := r.core.ReclaimCTA(completion.CTAID)
		if err != nil {
			return fmt.Errorf("device: reclaim CTA slot %d: %w", completion.CTAID, err)
		}
		delete(r.resident, completion.CTAID)
		result.Completed++
		emit(CTAEvent{Kind: CTACompleted, CTA: cta, ResidentValid: true, Resident: reclaimed})
	}
	return nil
}

func (r *kernelRuntime) complete() (bool, error) {
	if r.walker.Remaining() != 0 || len(r.pending) != 0 || len(r.resident) != 0 {
		return false, nil
	}
	completions, err := r.core.CTACompletions()
	if err != nil {
		return false, err
	}
	if len(completions) != 0 {
		return false, fmt.Errorf("device: no resident mapping but Core retains %d CTAs", len(completions))
	}
	slots, err := r.core.Slots()
	if err != nil {
		return false, err
	}
	for _, slot := range slots {
		if slot.Lifecycle != core.WarpInactive || slot.Participated || slot.BlockReason != core.BlockNone ||
			slot.ArchitecturalState != state.WarpInactive || slot.ArchitecturalLaneMask != 0 || slot.BarrierKey != nil || slot.BarrierDraining {
			return false, fmt.Errorf("device: lower owner retains work in warp slot %d", slot.WarpID)
		}
	}
	// Barrier records are removed by successful reclaim. Memory/fetch/data
	// services are synchronous, so no functional request survives a Step return.
	return true, nil
}

func detachCTAEvent(event CTAEvent) CTAEvent {
	event.Resident.Members = append([]core.WarpMembership(nil), event.Resident.Members...)
	return event
}
