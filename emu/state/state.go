// Package state owns the lane- and warp-scoped architectural state for the
// frozen Vortex configuration. ISA evaluation remains stateless: callers take
// a snapshot, evaluate one instruction, and submit its effects through the
// staged atomic boundary implemented by this owner.
package state

import (
	"fmt"

	"vortex.local/simulator/isa"
)

const (
	// FrozenRegisterCount is the architectural size of both the GPR and FPR
	// namespaces.
	FrozenRegisterCount = 32
	// FrozenDivergenceDepth is DV_STACK_SIZE = NUM_THREADS - 1. The RTL write
	// pointer has an additional full value, so valid pointers are 0..3 while
	// record indices are 0..2.
	FrozenDivergenceDepth = isa.FrozenLaneCount - 1
)

// Topology makes all capacity assumptions supplied at initialization
// auditable. NewWarp rejects anything except the frozen configuration.
type Topology struct {
	LaneCount       uint8
	WarpCount       uint8
	CoreCount       uint8
	RegisterCount   uint8
	DivergenceDepth uint8
}

// FrozenTopology returns the only topology accepted by this package.
func FrozenTopology() Topology {
	return Topology{
		LaneCount: isa.FrozenLaneCount, WarpCount: isa.FrozenWarpCount,
		CoreCount: isa.FrozenCoreCount, RegisterCount: FrozenRegisterCount,
		DivergenceDepth: FrozenDivergenceDepth,
	}
}

// WarpLifecycle is the canonical active-warp fact. Runnable/blocked state is
// deliberately absent: those facts belong to future scheduling owners.
type WarpLifecycle uint8

const (
	WarpInactive WarpLifecycle = iota
	WarpRunning
)

// LaneInitial supplies explicit, ABI-independent register startup contents.
// IDs are required so missing, duplicated, and out-of-range lanes are rejected.
type LaneInitial struct {
	ID  uint8
	GPR [FrozenRegisterCount]uint32
	FPR [FrozenRegisterCount]uint32
}

// TrapCSRState is the warp-scoped writable System/CSR storage confirmed by T1.
type TrapCSRState struct {
	MStatus  uint32
	MTVec    uint32
	MScratch uint32
	MEPC     uint32
	MCause   uint32
	MTVal    uint32
}

// DivergenceRecordInitial is one occupied IPDOM row.
type DivergenceRecordInitial struct {
	Pointer      uint8
	OriginalMask isa.LaneMask
	NextPC       uint32
	ElseVisited  bool
}

// DivergenceInitial explicitly describes the live IPDOM prefix. Records must
// contain each pointer in [0, WritePointer) exactly once.
type DivergenceInitial struct {
	WritePointer uint8
	Records      []DivergenceRecordInitial
}

// WarpInitial contains every T2-owned startup fact. Values controlled by the
// unresolved Kernel/CTA ABI have no defaults here.
type WarpInitial struct {
	Topology        Topology
	WarpID          uint8
	PC              uint32
	ActiveMask      isa.LaneMask
	Lifecycle       WarpLifecycle
	SavedThreadMask isa.LaneMask
	FCSR            uint32
	TrapCSRs        TrapCSRState
	Divergence      DivergenceInitial
	Lanes           []LaneInitial
}

type laneState struct {
	gpr [FrozenRegisterCount]uint32
	fpr [FrozenRegisterCount]uint32
}

type divergenceRecord struct {
	valid        bool
	originalMask isa.LaneMask
	nextPC       uint32
	elseVisited  bool
}

type divergenceState struct {
	writePointer uint8
	records      [FrozenDivergenceDepth]divergenceRecord
}

// WarpState is the sole writable owner for one warp's T2 state. Its fields and
// lane arrays are private so views cannot expose mutable references.
type WarpState struct {
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

// NewWarp validates and copies a complete explicit initial state.
func NewWarp(initial WarpInitial) (*WarpState, error) {
	if initial.Topology != FrozenTopology() {
		return nil, fmt.Errorf("state: topology %+v does not match frozen topology %+v", initial.Topology, FrozenTopology())
	}
	if initial.WarpID >= isa.FrozenWarpCount {
		return nil, fmt.Errorf("state: warp id %d exceeds frozen warp count", initial.WarpID)
	}
	if initial.PC&3 != 0 {
		return nil, fmt.Errorf("state: PC %#x is not four-byte aligned", initial.PC)
	}
	if !initial.ActiveMask.Valid() || !initial.SavedThreadMask.Valid() {
		return nil, fmt.Errorf("state: lane mask exceeds frozen lane count")
	}
	if err := validateLifecycle(initial.Lifecycle, initial.ActiveMask); err != nil {
		return nil, err
	}
	if initial.FCSR&^uint32(0xff) != 0 {
		return nil, fmt.Errorf("state: FCSR %#x exceeds the frozen eight-bit storage", initial.FCSR)
	}
	if len(initial.Lanes) != isa.FrozenLaneCount {
		return nil, fmt.Errorf("state: got %d lane initializers, want %d", len(initial.Lanes), isa.FrozenLaneCount)
	}

	divergence, err := makeDivergence(initial.Divergence)
	if err != nil {
		return nil, err
	}
	w := &WarpState{
		id: initial.WarpID, pc: initial.PC, activeMask: initial.ActiveMask,
		lifecycle: initial.Lifecycle, savedThreadMask: initial.SavedThreadMask,
		fcsr: uint8(initial.FCSR), trapCSRs: initial.TrapCSRs, divergence: divergence,
	}
	var seen [isa.FrozenLaneCount]bool
	for _, lane := range initial.Lanes {
		if lane.ID >= isa.FrozenLaneCount || seen[lane.ID] {
			return nil, fmt.Errorf("state: lane id %d is out of range or duplicated", lane.ID)
		}
		if lane.GPR[0] != 0 {
			return nil, fmt.Errorf("state: lane %d initializes integer x0 to nonzero value %#x", lane.ID, lane.GPR[0])
		}
		seen[lane.ID] = true
		w.lanes[lane.ID] = laneState{gpr: lane.GPR, fpr: lane.FPR}
	}
	return w, nil
}

func validateLifecycle(lifecycle WarpLifecycle, mask isa.LaneMask) error {
	switch lifecycle {
	case WarpInactive:
		if mask != 0 {
			return fmt.Errorf("state: inactive warp has nonzero active mask %#x", mask)
		}
	case WarpRunning:
		if mask == 0 {
			return fmt.Errorf("state: running warp has an empty active mask")
		}
	default:
		return fmt.Errorf("state: unknown warp lifecycle %d", lifecycle)
	}
	return nil
}

func makeDivergence(initial DivergenceInitial) (divergenceState, error) {
	var result divergenceState
	if initial.WritePointer > FrozenDivergenceDepth {
		return result, fmt.Errorf("state: divergence write pointer %d exceeds full pointer %d", initial.WritePointer, FrozenDivergenceDepth)
	}
	if len(initial.Records) != int(initial.WritePointer) {
		return result, fmt.Errorf("state: divergence pointer %d requires %d live records, got %d", initial.WritePointer, initial.WritePointer, len(initial.Records))
	}
	result.writePointer = initial.WritePointer
	for _, record := range initial.Records {
		if record.Pointer >= initial.WritePointer || record.Pointer >= FrozenDivergenceDepth || result.records[record.Pointer].valid {
			return divergenceState{}, fmt.Errorf("state: divergence record pointer %d is out of the live prefix or duplicated", record.Pointer)
		}
		if !record.OriginalMask.Valid() || record.OriginalMask == 0 {
			return divergenceState{}, fmt.Errorf("state: divergence record %d has invalid original mask %#x", record.Pointer, record.OriginalMask)
		}
		if record.NextPC&3 != 0 {
			return divergenceState{}, fmt.Errorf("state: divergence record %d PC %#x is not four-byte aligned", record.Pointer, record.NextPC)
		}
		result.records[record.Pointer] = divergenceRecord{
			valid: true, originalMask: record.OriginalMask,
			nextPC: record.NextPC, elseVisited: record.ElseVisited,
		}
	}
	for pointer := uint8(0); pointer < initial.WritePointer; pointer++ {
		if !result.records[pointer].valid {
			return divergenceState{}, fmt.Errorf("state: divergence live prefix is missing record %d", pointer)
		}
	}
	return result, nil
}

// ReadRegister copies one complete lane vector, including inactive lanes.
// Reading an inactive lane is observational and never changes state.
func (w *WarpState) ReadRegister(register isa.Register) (isa.LaneValues, error) {
	if w == nil {
		return isa.LaneValues{}, fmt.Errorf("state: nil warp")
	}
	if err := validateRegister(register); err != nil {
		return isa.LaneValues{}, err
	}
	return w.readRegister(register), nil
}

// WriteRegister applies exactly the caller-supplied mask. It intentionally
// does not intersect the active mask: instruction effects (including WGATHER)
// own destination-mask selection. Integer x0 writes are harmless no-ops.
func (w *WarpState) WriteRegister(register isa.Register, mask isa.LaneMask, values isa.LaneValues) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	if err := validateRegister(register); err != nil {
		return err
	}
	if !mask.Valid() {
		return fmt.Errorf("state: register write mask %#x exceeds frozen lanes", mask)
	}
	if register.File == isa.Integer && register.Index == 0 {
		return nil
	}
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		if !mask.Active(lane) {
			continue
		}
		if register.File == isa.Integer {
			w.lanes[lane].gpr[register.Index] = values[lane]
		} else {
			w.lanes[lane].fpr[register.Index] = values[lane]
		}
	}
	return nil
}

func validateRegister(register isa.Register) error {
	if register.Index >= FrozenRegisterCount {
		return fmt.Errorf("state: register index %d exceeds architectural register count", register.Index)
	}
	if register.File != isa.Integer && register.File != isa.Float {
		return fmt.Errorf("state: unknown register namespace %d", register.File)
	}
	return nil
}

func (w *WarpState) readRegister(register isa.Register) isa.LaneValues {
	var values isa.LaneValues
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		if register.File == isa.Integer {
			if register.Index != 0 {
				values[lane] = w.lanes[lane].gpr[register.Index]
			}
		} else {
			values[lane] = w.lanes[lane].fpr[register.Index]
		}
	}
	return values
}

// SetPC updates the canonical PC after validating the frozen non-RVC alignment.
func (w *WarpState) SetPC(pc uint32) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	if pc&3 != 0 {
		return fmt.Errorf("state: PC %#x is not four-byte aligned", pc)
	}
	w.pc = pc
	return nil
}

// SetActiveMask atomically updates the mask and its lifecycle fact.
func (w *WarpState) SetActiveMask(mask isa.LaneMask, lifecycle WarpLifecycle) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	if !mask.Valid() {
		return fmt.Errorf("state: active mask %#x exceeds frozen lanes", mask)
	}
	if err := validateLifecycle(lifecycle, mask); err != nil {
		return err
	}
	w.activeMask, w.lifecycle = mask, lifecycle
	return nil
}

func (w *WarpState) SetSavedThreadMask(mask isa.LaneMask) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	if !mask.Valid() {
		return fmt.Errorf("state: saved thread mask %#x exceeds frozen lanes", mask)
	}
	w.savedThreadMask = mask
	return nil
}

func (w *WarpState) SetFCSR(value uint32) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	if value&^uint32(0xff) != 0 {
		return fmt.Errorf("state: FCSR %#x exceeds the frozen eight-bit storage", value)
	}
	w.fcsr = uint8(value)
	return nil
}

func (w *WarpState) SetTrapCSRs(value TrapCSRState) {
	if w != nil {
		w.trapCSRs = value
	}
}

// SetDivergence replaces the canonical stack only after all metadata validates.
func (w *WarpState) SetDivergence(initial DivergenceInitial) error {
	if w == nil {
		return fmt.Errorf("state: nil warp")
	}
	value, err := makeDivergence(initial)
	if err != nil {
		return err
	}
	w.divergence = value
	return nil
}
