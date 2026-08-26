package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

// ReadContext contains values whose canonical owners are outside T2. It is
// consumed and copied while constructing a view; WarpState and WarpSnapshot
// never retain this object or any pointer inside it.
type ReadContext struct {
	CoreID           uint8
	ActiveWarps      uint8
	CTA              isa.CTAView
	Counters         isa.CounterView
	Bounds           *isa.AddressBounds
	BarrierPhase     bool
	BarrierPhases    *BarrierPhaseView
	PendingPriorWork bool
	PendingLSU       bool
}

// BarrierPhaseView is a detached snapshot of the frozen physical barrier
// address space for one CTA. The first index is AddressWarp and the second is
// the three-bit barrier ID. A nil view preserves the direct Warp API's legacy
// scalar BarrierPhase input; Core always supplies this canonical table.
type BarrierPhaseView [isa.FrozenWarpCount][8]bool

// DivergenceRecordSnapshot is an immutable-by-copy observation of one row.
type DivergenceRecordSnapshot struct {
	Valid        bool
	Pointer      uint8
	OriginalMask isa.LaneMask
	NextPC       uint32
	ElseVisited  bool
}

// WarpSnapshot is a detached value copy of all T2-owned facts. Its fields are
// private and its accessors return values, so it cannot mutate canonical state.
type WarpSnapshot struct {
	id              uint8
	pc              uint32
	activeMask      isa.LaneMask
	lifecycle       WarpLifecycle
	savedThreadMask isa.LaneMask
	fcsr            uint8
	trapCSRs        TrapCSRState
	divergence      divergenceState
	lanes           [isa.FrozenLaneCount]laneState
}

func (w *WarpState) Snapshot() (WarpSnapshot, error) {
	if w == nil {
		return WarpSnapshot{}, fmt.Errorf("state: nil warp")
	}
	return WarpSnapshot{
		id: w.id, pc: w.pc, activeMask: w.activeMask, lifecycle: w.lifecycle,
		savedThreadMask: w.savedThreadMask, fcsr: w.fcsr,
		trapCSRs: w.trapCSRs, divergence: w.divergence, lanes: w.lanes,
	}, nil
}

func (s WarpSnapshot) WarpID() uint8                 { return s.id }
func (s WarpSnapshot) PC() uint32                    { return s.pc }
func (s WarpSnapshot) ActiveMask() isa.LaneMask      { return s.activeMask }
func (s WarpSnapshot) Lifecycle() WarpLifecycle      { return s.lifecycle }
func (s WarpSnapshot) SavedThreadMask() isa.LaneMask { return s.savedThreadMask }
func (s WarpSnapshot) FCSR() uint32                  { return uint32(s.fcsr) }
func (s WarpSnapshot) TrapCSRs() TrapCSRState        { return s.trapCSRs }
func (s WarpSnapshot) DivergenceWritePointer() uint8 { return s.divergence.writePointer }

func (s WarpSnapshot) ReadRegister(register isa.Register) (isa.LaneValues, error) {
	if err := validateRegister(register); err != nil {
		return isa.LaneValues{}, err
	}
	var values isa.LaneValues
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		if register.File == isa.Integer {
			if register.Index != 0 {
				values[lane] = s.lanes[lane].gpr[register.Index]
			}
		} else {
			values[lane] = s.lanes[lane].fpr[register.Index]
		}
	}
	return values, nil
}

func (s WarpSnapshot) DivergenceRecord(pointer uint8) (DivergenceRecordSnapshot, error) {
	if pointer >= FrozenDivergenceDepth {
		return DivergenceRecordSnapshot{}, fmt.Errorf("state: divergence record pointer %d exceeds record capacity", pointer)
	}
	record := s.divergence.records[pointer]
	return DivergenceRecordSnapshot{
		Valid: record.valid, Pointer: pointer, OriginalMask: record.originalMask,
		NextPC: record.nextPC, ElseVisited: record.elseVisited,
	}, nil
}

// IntegerInput builds the T1 integer view from decoded source namespace/index.
func (s WarpSnapshot) IntegerInput(decoded isa.Decoded, context ReadContext) (isa.IntegerInput, error) {
	sources, err := s.sourceValues(decoded)
	if err != nil {
		return isa.IntegerInput{}, err
	}
	bounds, err := validateContext(context)
	if err != nil {
		return isa.IntegerInput{}, err
	}
	return isa.IntegerInput{PC: s.pc, ActiveMask: s.activeMask, RS1: sources[0], RS2: sources[1], Bounds: bounds}, nil
}

// FloatInput builds the T1 floating view and translates the raw FRM encoding
// in canonical FCSR to the enum used by the stateless evaluator.
func (s WarpSnapshot) FloatInput(decoded isa.Decoded, context ReadContext) (isa.FloatInput, error) {
	sources, err := s.sourceValues(decoded)
	if err != nil {
		return isa.FloatInput{}, err
	}
	bounds, err := validateContext(context)
	if err != nil {
		return isa.FloatInput{}, err
	}
	return isa.FloatInput{
		PC: s.pc, ActiveMask: s.activeMask, RS1: sources[0], RS2: sources[1], RS3: sources[2],
		FRM: roundingMode((s.fcsr >> 5) & 7), Bounds: bounds,
	}, nil
}

// SystemInput combines warp-owned CSR storage with copied identity, CTA, and
// counter context supplied by their future owners.
func (s WarpSnapshot) SystemInput(decoded isa.Decoded, context ReadContext) (isa.SystemInput, error) {
	sources, err := s.sourceValues(decoded)
	if err != nil {
		return isa.SystemInput{}, err
	}
	if _, err := validateContext(context); err != nil {
		return isa.SystemInput{}, err
	}
	return isa.SystemInput{
		PC: s.pc, ActiveMask: s.activeMask, RS1: sources[0],
		CSR: isa.CSRView{
			WarpID: s.id, CoreID: context.CoreID, ActiveWarps: context.ActiveWarps,
			ThreadMask: s.activeMask, SavedThreadMask: s.savedThreadMask,
			FCSR: uint32(s.fcsr), MStatus: s.trapCSRs.MStatus, MTVec: s.trapCSRs.MTVec,
			MScratch: s.trapCSRs.MScratch, MEPC: s.trapCSRs.MEPC,
			MCause: s.trapCSRs.MCause, MTVal: s.trapCSRs.MTVal,
			CTA: context.CTA, Counters: context.Counters,
		},
	}, nil
}

// CustomInput supplies the addressed JOIN row, not a mutable stack reference.
func (s WarpSnapshot) CustomInput(decoded isa.Decoded, context ReadContext) (isa.CustomInput, error) {
	sources, err := s.sourceValues(decoded)
	if err != nil {
		return isa.CustomInput{}, err
	}
	bounds, err := validateContext(context)
	if err != nil {
		return isa.CustomInput{}, err
	}
	view := isa.DivergenceView{WritePointer: s.divergence.writePointer}
	if decoded.Control == isa.ControlJoin {
		lane := highestActive(s.activeMask)
		pointer := uint8(sources[0][lane] & 3)
		if pointer < FrozenDivergenceDepth {
			record := s.divergence.records[pointer]
			view.Record = isa.DivergenceRecordView{
				Valid: record.valid, Pointer: pointer, OriginalMask: record.originalMask,
				NextPC: record.nextPC, ElseVisited: record.elseVisited,
			}
		}
	}
	barrierPhase := context.BarrierPhase
	if context.BarrierPhases != nil && decoded.Barrier != isa.BarrierNone {
		phases := *context.BarrierPhases
		lane := highestActive(s.activeMask)
		addressWarp := uint8(sources[0][lane]) & (isa.FrozenWarpCount - 1)
		barrierID := uint8(sources[0][lane]>>8) & 7
		barrierPhase = phases[addressWarp][barrierID]
	}
	return isa.CustomInput{
		PC: s.pc, ActiveMask: s.activeMask, RS1: sources[0], RS2: sources[1], RS3: sources[2],
		WarpID: s.id, MScratch: s.trapCSRs.MScratch, Divergence: view,
		BarrierPhase: barrierPhase, PendingPriorWork: context.PendingPriorWork,
		PendingLSU: context.PendingLSU, Bounds: bounds,
	}, nil
}

// Convenience builders snapshot the current state at the call boundary.
func (w *WarpState) IntegerInput(decoded isa.Decoded, context ReadContext) (isa.IntegerInput, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return isa.IntegerInput{}, err
	}
	return snapshot.IntegerInput(decoded, context)
}

func (w *WarpState) FloatInput(decoded isa.Decoded, context ReadContext) (isa.FloatInput, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return isa.FloatInput{}, err
	}
	return snapshot.FloatInput(decoded, context)
}

func (w *WarpState) SystemInput(decoded isa.Decoded, context ReadContext) (isa.SystemInput, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return isa.SystemInput{}, err
	}
	return snapshot.SystemInput(decoded, context)
}

func (w *WarpState) CustomInput(decoded isa.Decoded, context ReadContext) (isa.CustomInput, error) {
	snapshot, err := w.Snapshot()
	if err != nil {
		return isa.CustomInput{}, err
	}
	return snapshot.CustomInput(decoded, context)
}

func (s WarpSnapshot) sourceValues(decoded isa.Decoded) ([3]isa.LaneValues, error) {
	var result [3]isa.LaneValues
	if len(decoded.Sources) > len(result) {
		return result, fmt.Errorf("state: decoded instruction %q has %d sources, maximum is %d", decoded.Name, len(decoded.Sources), len(result))
	}
	for position, register := range decoded.Sources {
		values, err := s.ReadRegister(register)
		if err != nil {
			return result, fmt.Errorf("state: decoded instruction %q source %d: %w", decoded.Name, position, err)
		}
		result[position] = values
	}
	return result, nil
}

func validateContext(context ReadContext) (*isa.AddressBounds, error) {
	if context.CoreID >= isa.FrozenCoreCount {
		return nil, fmt.Errorf("state: core id %d exceeds frozen core count", context.CoreID)
	}
	if context.ActiveWarps&^uint8(isa.AllWarps) != 0 {
		return nil, fmt.Errorf("state: active warp mask %#x exceeds frozen warps", context.ActiveWarps)
	}
	if context.Counters.Cycle >= uint64(1)<<isa.FrozenCounterBits || context.Counters.Instret >= uint64(1)<<isa.FrozenCounterBits {
		return nil, fmt.Errorf("state: counter view exceeds frozen %d-bit width", isa.FrozenCounterBits)
	}
	if context.Bounds == nil {
		return nil, nil
	}
	if uint64(context.Bounds.Base)+context.Bounds.Size > uint64(1)<<32 {
		return nil, fmt.Errorf("state: address bounds exceed the 32-bit address space")
	}
	copy := *context.Bounds
	return &copy, nil
}

func roundingMode(raw uint8) isa.RoundingMode {
	switch raw {
	case 0:
		return isa.RNE
	case 1:
		return isa.RTZ
	case 2:
		return isa.RDN
	case 3:
		return isa.RUP
	case 4:
		return isa.RMM
	default:
		// Reserved FRM state remains observable via SystemInput.FCSR. Returning
		// RoundingNone makes a dynamic FP instruction reject it deterministically.
		return isa.RoundingNone
	}
}

func highestActive(mask isa.LaneMask) uint8 {
	for lane := uint8(isa.FrozenLaneCount); lane > 0; lane-- {
		if mask.Active(lane - 1) {
			return lane - 1
		}
	}
	return 0
}
