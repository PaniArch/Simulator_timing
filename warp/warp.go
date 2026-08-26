// Package warp orchestrates functional execution of one canonical WarpState.
// It owns no architectural state and intentionally does not schedule warps.
package warp

import (
	"encoding/binary"
	"errors"
	"fmt"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
)

// InstructionSource reads program bytes from their canonical owner. Read must
// either fill dst completely or return an error; Step always supplies exactly
// four bytes because the frozen configuration has no compressed instructions.
type InstructionSource interface {
	Read(address uint32, dst []byte) error
}

// MemoryService is the synchronous functional byte owner used for both fetch
// and data access. Read and Write must be all-or-error for the supplied slice;
// a failed Write must leave the addressed bytes unchanged. support/memory.Memory
// implements this boundary directly.
type MemoryService interface {
	InstructionSource
	Write(address uint32, src []byte) error
}

// Outcome is the architectural disposition of one Step.
type Outcome uint8

const (
	OutcomeRetired Outcome = iota
	OutcomeTrap
	OutcomeFault
	OutcomeDeferred
	OutcomeFinished
)

func (o Outcome) String() string {
	switch o {
	case OutcomeRetired:
		return "retired"
	case OutcomeTrap:
		return "trap"
	case OutcomeFault:
		return "fault"
	case OutcomeDeferred:
		return "deferred"
	case OutcomeFinished:
		return "finished"
	default:
		return fmt.Sprintf("outcome(%d)", o)
	}
}

// FaultKind identifies the boundary which rejected a Step. Architectural
// load/store faults remain in Fault.Architectural rather than being guessed
// into trap causes while U-FAULT-01 is unresolved.
type FaultKind uint8

const (
	FaultState FaultKind = iota
	FaultExecutionMode
	FaultInstructionAlignment
	FaultInstructionAccess
	FaultIllegalInstruction
	FaultView
	FaultEvaluation
	FaultEffectValidation
	FaultCommit
	FaultArchitectural
)

// Fault is a typed, deterministic stop reason. Cause retains the underlying
// source/decode/view/evaluate/apply error when one exists.
type Fault struct {
	Kind          FaultKind
	PC            uint32
	Cause         error
	Architectural []isa.FaultEffect
}

func (f *Fault) Error() string {
	if f == nil {
		return "warp: nil fault"
	}
	if f.Cause != nil {
		return fmt.Sprintf("warp: step fault kind=%d pc=%#x: %v", f.Kind, f.PC, f.Cause)
	}
	return fmt.Sprintf("warp: step fault kind=%d pc=%#x", f.Kind, f.PC)
}

func (f *Fault) Unwrap() error {
	if f == nil {
		return nil
	}
	return f.Cause
}

// Result contains only observations of this Step. Decoded and Effects are
// present after their respective boundaries; RawValid distinguishes a fetched
// zero word from a fetch failure. NextPC always reports the canonical owner.
type Result struct {
	Outcome  Outcome
	WarpID   uint8
	PC       uint32
	Raw      uint32
	RawValid bool
	Decoded  *isa.Decoded
	// IssuedEffects retains the T1 evaluator output. Effects is the final
	// completion bundle for memory operations and otherwise the same bundle.
	IssuedEffects *isa.InstructionEffects
	Effects       *isa.InstructionEffects
	NextPC        uint32
	Fault         *Fault
	Err           error
}

// Warp references the real canonical owner and instruction source. It does
// not cache PC, registers, CSR state, lane masks, lifecycle, or program bytes.
type Warp struct {
	state        *state.WarpState
	instructions InstructionSource
	memory       MemoryService
}

func New(owner *state.WarpState, instructions InstructionSource) (*Warp, error) {
	if owner == nil {
		return nil, fmt.Errorf("warp: nil canonical state")
	}
	if instructions == nil {
		return nil, fmt.Errorf("warp: nil instruction source")
	}
	w := &Warp{state: owner, instructions: instructions}
	if memory, ok := instructions.(MemoryService); ok {
		w.memory = memory
	}
	return w, nil
}

// NewWithMemory uses one canonical byte owner for instruction fetch and data
// access. Warp retains only the service reference, never a program/data copy.
func NewWithMemory(owner *state.WarpState, memory MemoryService) (*Warp, error) {
	if memory == nil {
		return nil, fmt.Errorf("warp: nil memory service")
	}
	w, err := New(owner, memory)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Step executes one single-lane instruction through the existing
// Decode -> State View -> T1 Evaluate -> T2 Stage/Commit chain. Effects owned
// by a configured synchronous MemoryService are completed here; Core, CTA,
// Barrier, or multi-lane SIMT effects are returned deferred.
func (w *Warp) Step(context state.ReadContext) (result Result) {
	if w == nil || w.state == nil {
		return faultResult(0, FaultState, fmt.Errorf("warp: nil executor or canonical state"))
	}
	// InstructionSource is an external owner boundary and may synchronously
	// expose a canonical-state change (for example, a stale-stage test double).
	// Preserve the starting PC in Result.PC, but refresh NextPC for every return
	// path so it always observes the canonical owner at the Step boundary.
	defer func() {
		canonical, snapshotErr := w.state.Snapshot()
		if snapshotErr == nil {
			result.NextPC = canonical.PC()
		}
	}()
	snapshot, err := w.state.Snapshot()
	if err != nil {
		return faultResult(0, FaultState, err)
	}
	pc := snapshot.PC()
	result = Result{WarpID: snapshot.WarpID(), PC: pc, NextPC: pc}
	if snapshot.Lifecycle() == state.WarpInactive {
		result.Outcome = OutcomeFinished
		return result
	}
	mask := snapshot.ActiveMask()
	if !exactlyOneLane(mask) {
		return stepError(result, FaultExecutionMode, fmt.Errorf("warp: running single-lane executor requires exactly one active lane, got mask %#x", mask))
	}
	if pc&3 != 0 {
		return stepError(result, FaultInstructionAlignment, fmt.Errorf("warp: instruction PC %#x is not four-byte aligned", pc))
	}
	if w.instructions == nil {
		return stepError(result, FaultInstructionAccess, fmt.Errorf("warp: nil instruction source"))
	}

	var bytes [4]byte
	if err := w.instructions.Read(pc, bytes[:]); err != nil {
		return stepError(result, FaultInstructionAccess, err)
	}
	result.Raw = binary.LittleEndian.Uint32(bytes[:])
	result.RawValid = true

	decoded, err := isa.Decode(result.Raw)
	if err != nil {
		result.Outcome = OutcomeFault
		result.Fault = &Fault{Kind: FaultIllegalInstruction, PC: pc, Cause: err}
		result.Err = result.Fault
		return result
	}
	result.Decoded = &decoded

	effects, err := snapshot.Evaluate(decoded, context)
	result.IssuedEffects = &effects
	result.Effects = &effects
	if err != nil {
		kind := FaultEvaluation
		var evaluation *isa.EvaluationError
		var csr *isa.CSRAccessError
		if !errors.As(err, &evaluation) && !errors.As(err, &csr) {
			kind = FaultView
		}
		result.Outcome = OutcomeFault
		result.Fault = &Fault{Kind: kind, PC: pc, Cause: err}
		result.Err = result.Fault
		return result
	}

	stage, err := w.state.StageEffects(effects)
	if err != nil {
		result.Outcome = OutcomeFault
		result.Fault = &Fault{Kind: FaultEffectValidation, PC: pc, Cause: err}
		result.Err = result.Fault
		return result
	}
	if len(effects.Faults) != 0 {
		return architecturalFault(result, effects.Faults, nil)
	}
	if len(effects.MemoryRequests) != 0 {
		if w.memory == nil {
			result.Outcome = OutcomeDeferred
			return result
		}
		return w.completeMemory(result, snapshot, decoded, effects.MemoryRequests, mask)
	}
	if len(effects.PackedLoads) != 0 {
		if w.memory == nil {
			result.Outcome = OutcomeDeferred
			return result
		}
		return w.completePackedLoad(result, snapshot, decoded, effects.PackedLoads, mask)
	}
	if effects.Ordering != nil && stage.RequiresExternalSuccess() {
		if w.memory == nil || hasUnresolvedExternal(effects) {
			result.Outcome = OutcomeDeferred
			return result
		}
		// The configured functional byte owner is synchronous: all preceding
		// Read/Write calls have completed before Step reaches this boundary.
		if err := stage.CommitAfterExternal(); err != nil {
			return stepError(result, FaultCommit, err)
		}
		result.Outcome = OutcomeRetired
		return result
	}
	if decoded.Category == isa.CategoryCustom || stage.RequiresExternalSuccess() {
		result.Outcome = OutcomeDeferred
		return result
	}
	if err := stage.Commit(); err != nil {
		result.Outcome = OutcomeFault
		result.Fault = &Fault{Kind: FaultCommit, PC: pc, Cause: err}
		result.Err = result.Fault
		return result
	}
	after, err := w.state.Snapshot()
	if err != nil {
		return stepError(result, FaultState, err)
	}
	result.NextPC = after.PC()
	if effects.Trap != nil {
		result.Outcome = OutcomeTrap
	} else {
		result.Outcome = OutcomeRetired
	}
	return result
}

func (w *Warp) completeMemory(result Result, snapshot state.WarpSnapshot, decoded isa.Decoded, requests []isa.MemoryRequest, expected isa.LaneMask) Result {
	responses := make([]isa.MemoryResponse, 0, len(requests))
	var serviceErr error
	var store *storeTransaction
	for _, request := range requests {
		if request.Kind == isa.MemoryStore {
			transaction, err := w.prepareStore(request)
			if err != nil {
				serviceErr = err
				responses = append(responses, memoryFaultResponse(request))
				continue
			}
			store = transaction
			responses = append(responses, isa.MemoryResponse{Request: request})
			continue
		}
		data, err := w.readMemory(request.Address, request.Width)
		if err != nil {
			serviceErr = err
			responses = append(responses, memoryFaultResponse(request))
			continue
		}
		responses = append(responses, isa.MemoryResponse{Request: request, Data: data})
	}

	completed, err := snapshot.CompleteMemory(decoded, expected, responses)
	result.Effects = &completed
	if err != nil {
		return stepError(result, FaultEvaluation, err)
	}
	stage, err := w.state.StageEffects(completed)
	if err != nil {
		return stepError(result, FaultEffectValidation, err)
	}
	if len(completed.Faults) != 0 {
		return architecturalFault(result, completed.Faults, serviceErr)
	}
	if store != nil {
		writeCalled := false
		err := stage.CommitWithExternal(func() error {
			writeCalled = true
			return w.memory.Write(store.address, store.after)
		})
		if err != nil && writeCalled {
			faults := []isa.FaultEffect{{Kind: isa.FaultStoreAccess, Reason: isa.FaultReasonMemoryService, Lane: store.request.Lane, Address: store.request.Address, Width: store.request.Width}}
			failed, completionErr := snapshot.CompleteMemory(decoded, expected, []isa.MemoryResponse{{Request: store.request, Fault: isa.FaultStoreAccess, Reason: isa.FaultReasonMemoryService}})
			if completionErr == nil {
				result.Effects = &failed
				faults = failed.Faults
				if _, validationErr := w.state.StageEffects(failed); validationErr != nil {
					return stepError(result, FaultEffectValidation, validationErr)
				}
			}
			return architecturalFault(result, faults, errors.Join(err, completionErr))
		}
		if err != nil {
			return stepError(result, FaultCommit, err)
		}
	} else if err := stage.Commit(); err != nil {
		return stepError(result, FaultCommit, err)
	}
	result.Outcome = OutcomeRetired
	return result
}

func (w *Warp) completePackedLoad(result Result, snapshot state.WarpSnapshot, decoded isa.Decoded, requests []isa.PackedLoadRequest, expected isa.LaneMask) Result {
	responses := make([]isa.PackedLoadResponse, 0, len(requests))
	var serviceErr error
	for _, request := range requests {
		data, err := w.readMemory(request.Address, request.Width)
		response := isa.PackedLoadResponse{Request: request, Data: data}
		if err != nil {
			serviceErr = err
			response.Fault, response.Reason = isa.FaultLoadAccess, isa.FaultReasonMemoryService
		}
		responses = append(responses, response)
	}
	completed, err := snapshot.CompletePackedLoad(decoded, expected, responses)
	result.Effects = &completed
	if err != nil {
		return stepError(result, FaultEvaluation, err)
	}
	stage, err := w.state.StageEffects(completed)
	if err != nil {
		return stepError(result, FaultEffectValidation, err)
	}
	if len(completed.Faults) != 0 {
		return architecturalFault(result, completed.Faults, serviceErr)
	}
	if err := stage.Commit(); err != nil {
		return stepError(result, FaultCommit, err)
	}
	result.Outcome = OutcomeRetired
	return result
}

type storeTransaction struct {
	request isa.MemoryRequest
	address uint32
	after   []byte
}

func (w *Warp) prepareStore(request isa.MemoryRequest) (*storeTransaction, error) {
	if request.Width != 1 && request.Width != 2 && request.Width != 4 {
		return nil, fmt.Errorf("warp: unsupported store width %d", request.Width)
	}
	offset := request.Address - request.AlignedAddress
	wantMask := uint8((uint16(1)<<request.Width)-1) << offset
	if offset+uint32(request.Width) > 4 || request.ByteMask != wantMask {
		return nil, fmt.Errorf("warp: store byte mask %#x does not match address %#x width %d", request.ByteMask, request.Address, request.Width)
	}
	preflight := make([]byte, request.Width)
	if err := w.memory.Read(request.Address, preflight); err != nil {
		return nil, err
	}
	after := make([]byte, request.Width)
	value := request.StoreData >> (8 * offset)
	for index := range after {
		after[index] = byte(value >> (8 * index))
	}
	return &storeTransaction{request: request, address: request.Address, after: after}, nil
}

func (w *Warp) readMemory(address uint32, width uint8) (uint32, error) {
	if width != 1 && width != 2 && width != 4 {
		return 0, fmt.Errorf("warp: unsupported memory width %d", width)
	}
	bytes := make([]byte, width)
	if err := w.memory.Read(address, bytes); err != nil {
		return 0, err
	}
	var value uint32
	for index, b := range bytes {
		value |= uint32(b) << (8 * index)
	}
	return value, nil
}

func memoryFaultResponse(request isa.MemoryRequest) isa.MemoryResponse {
	kind := isa.FaultLoadAccess
	if request.Kind == isa.MemoryStore {
		kind = isa.FaultStoreAccess
	}
	return isa.MemoryResponse{Request: request, Fault: kind, Reason: isa.FaultReasonMemoryService}
}

func architecturalFault(result Result, faults []isa.FaultEffect, cause error) Result {
	result.Outcome = OutcomeFault
	result.Fault = &Fault{Kind: FaultArchitectural, PC: result.PC, Cause: cause, Architectural: append([]isa.FaultEffect(nil), faults...)}
	result.Err = result.Fault
	return result
}

func stepError(result Result, kind FaultKind, cause error) Result {
	result.Outcome = OutcomeFault
	result.Fault = &Fault{Kind: kind, PC: result.PC, Cause: cause}
	result.Err = result.Fault
	return result
}

func hasUnresolvedExternal(effects isa.InstructionEffects) bool {
	return len(effects.MemoryRequests) != 0 || len(effects.CSRWrites) != 0 || effects.WarpSpawn != nil ||
		len(effects.WarpDrains) != 0 || len(effects.Barriers) != 0 || len(effects.PackedLoads) != 0 || len(effects.Faults) != 0
}

func exactlyOneLane(mask isa.LaneMask) bool {
	return mask.Valid() && mask != 0 && mask&(mask-1) == 0
}

func faultResult(pc uint32, kind FaultKind, cause error) Result {
	fault := &Fault{Kind: kind, PC: pc, Cause: cause}
	return Result{Outcome: OutcomeFault, PC: pc, NextPC: pc, Fault: fault, Err: fault}
}
