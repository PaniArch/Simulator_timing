package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

// SingleInstructionResult records one Decode/View/Evaluate/Apply boundary.
// It is not an instruction stream, fetch loop, scheduler, or Warp.Step API.
type SingleInstructionResult struct {
	Decoded isa.Decoded
	Effects isa.InstructionEffects
	Apply   ApplyResult
}

// ExecuteSingle connects one caller-supplied instruction word to the existing
// stateless T1 evaluator through a detached canonical State view, then submits
// its effects to the atomic Warp owner. Future-owner requests are returned in
// Apply.Forwarded and follow the same pending-stage rules as ApplyEffects.
func (w *WarpState) ExecuteSingle(word uint32, context ReadContext) (SingleInstructionResult, error) {
	decoded, err := isa.Decode(word)
	if err != nil {
		return SingleInstructionResult{}, err
	}
	snapshot, err := w.Snapshot()
	if err != nil {
		return SingleInstructionResult{Decoded: decoded}, err
	}
	effects, err := snapshot.Evaluate(decoded, context)
	result := SingleInstructionResult{Decoded: decoded, Effects: cloneEffects(effects)}
	if err != nil {
		return result, err
	}
	result.Apply, err = w.ApplyEffects(effects)
	return result, err
}

// Evaluate routes one decoded instruction through the T1 evaluator using this
// detached snapshot. It is shared by ExecuteSingle and higher orchestration
// layers so evaluator dispatch is not copied into Warp executors.
func (s WarpSnapshot) Evaluate(decoded isa.Decoded, context ReadContext) (isa.InstructionEffects, error) {
	var effects isa.InstructionEffects
	var err error
	switch decoded.Category {
	case isa.CategoryRV32I, isa.CategoryRV32M, isa.CategoryZicond, isa.CategoryFence:
		input, inputErr := s.IntegerInput(decoded, context)
		if inputErr != nil {
			return isa.InstructionEffects{}, inputErr
		}
		effects, err = isa.EvaluateInteger(decoded, input)
	case isa.CategoryRV32F:
		input, inputErr := s.FloatInput(decoded, context)
		if inputErr != nil {
			return isa.InstructionEffects{}, inputErr
		}
		effects, err = isa.EvaluateFloat(decoded, input)
	case isa.CategorySystem:
		input, inputErr := s.SystemInput(decoded, context)
		if inputErr != nil {
			return isa.InstructionEffects{}, inputErr
		}
		effects, err = isa.EvaluateSystem(decoded, input)
	case isa.CategoryCustom:
		input, inputErr := s.CustomInput(decoded, context)
		if inputErr != nil {
			return isa.InstructionEffects{}, inputErr
		}
		effects, err = isa.EvaluateCustom(decoded, input)
	default:
		err = fmt.Errorf("state: decoded instruction %q has unsupported category %q", decoded.Name, decoded.Category)
	}
	return effects, err
}

// CompleteMemoryAndApply connects responses from a future Memory owner to the
// T1 completion helper and the same atomic State boundary. The instruction was
// already decoded and issued by the caller; this method does not fetch, loop,
// retain pending memory state, or serve memory itself.
func (w *WarpState) CompleteMemoryAndApply(decoded isa.Decoded, expected isa.LaneMask, responses []isa.MemoryResponse) (SingleInstructionResult, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return SingleInstructionResult{Decoded: decoded}, err
	}
	effects, err := snapshot.CompleteMemory(decoded, expected, responses)
	result := SingleInstructionResult{Decoded: decoded, Effects: cloneEffects(effects)}
	if err != nil {
		return result, err
	}
	result.Apply, err = w.ApplyEffects(effects)
	return result, err
}

// CompleteMemory routes ordinary integer and floating memory responses through
// the matching T1 completion helper using this detached snapshot's PC.
func (s WarpSnapshot) CompleteMemory(decoded isa.Decoded, expected isa.LaneMask, responses []isa.MemoryResponse) (isa.InstructionEffects, error) {
	if decoded.Category == isa.CategoryRV32F {
		return isa.CompleteFloatMemory(decoded, s.PC(), expected, responses)
	}
	return isa.CompleteMemory(decoded, s.PC(), expected, responses)
}

// CompletePackedLoad routes lane-local packed-load responses through the T1
// assembly semantics without exposing canonical state to the ISA package.
func (s WarpSnapshot) CompletePackedLoad(decoded isa.Decoded, expected isa.LaneMask, responses []isa.PackedLoadResponse) (isa.InstructionEffects, error) {
	return isa.CompletePackedLoad(decoded, s.PC(), expected, responses)
}
