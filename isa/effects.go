package isa

import "fmt"

// FrozenLaneCount is fixed by VX_CFG_NUM_THREADS in the read-only RTL input.
const FrozenLaneCount = 4

// LaneMask uses one bit per frozen lane.
type LaneMask uint8

const AllLanes LaneMask = (1 << FrozenLaneCount) - 1

func (m LaneMask) Valid() bool { return m&^AllLanes == 0 }
func (m LaneMask) Active(lane uint8) bool {
	return lane < FrozenLaneCount && m&(1<<lane) != 0
}

type LaneValues [FrozenLaneCount]uint32

// WarpMask uses one bit per frozen warp.
type WarpMask uint8

const AllWarps WarpMask = (1 << FrozenWarpCount) - 1

func (m WarpMask) Valid() bool { return m&^AllWarps == 0 }
func (m WarpMask) Active(warp uint8) bool {
	return warp < FrozenWarpCount && m&(1<<warp) != 0
}

// IntegerInput is an immutable per-instruction view. It is deliberately not a
// register file or machine State: callers provide only already-read operands,
// PC, active lanes, and an optional address-range view.
type IntegerInput struct {
	PC         uint32
	ActiveMask LaneMask
	RS1        LaneValues
	RS2        LaneValues
	Bounds     *AddressBounds
}

// FloatInput is the immutable operand/FCSR view for one RV32F instruction.
// RS fields contain raw 32-bit register bits from the namespace declared by
// Decoded.Sources. FRM is consulted only when Decoded.Rounding is Dynamic.
type FloatInput struct {
	PC         uint32
	ActiveMask LaneMask
	RS1        LaneValues
	RS2        LaneValues
	RS3        LaneValues
	FRM        RoundingMode
	Bounds     *AddressBounds
}

// DivergenceRecordView is the one IPDOM stack row explicitly supplied to a
// JOIN evaluation. ElseVisited mirrors VX_ipdom_stack's q_idx bit: false means
// the deferred path is selected and the row is retained; true means the
// reconverged mask is restored and the row is popped.
type DivergenceRecordView struct {
	Valid        bool
	Pointer      uint8
	OriginalMask LaneMask
	NextPC       uint32
	ElseVisited  bool
}

// DivergenceView is not a stack. It exposes only the current write pointer and
// the row addressed by JOIN's rs1 operand.
type DivergenceView struct {
	WritePointer uint8
	Record       DivergenceRecordView
}

// CustomInput is the immutable operand/context view for one frozen Vortex
// custom instruction. PendingPriorWork and PendingLSU are architectural
// predicates consumed only to describe WSYNC/BAR drain and release effects.
type CustomInput struct {
	PC               uint32
	ActiveMask       LaneMask
	RS1              LaneValues
	RS2              LaneValues
	RS3              LaneValues
	WarpID           uint8
	MScratch         uint32
	Divergence       DivergenceView
	BarrierPhase     bool
	PendingPriorWork bool
	PendingLSU       bool
	Bounds           *AddressBounds
}

// AddressBounds is an optional immutable memory-service view. When absent,
// bounds validation is deferred to the owner response; when present, the ISA
// layer can produce a deterministic access fault before issuing a request.
type AddressBounds struct {
	Base uint32
	Size uint64
}

func (b AddressBounds) valid() bool {
	return uint64(b.Base)+b.Size <= uint64(1)<<32
}

func (b AddressBounds) contains(address uint32, width uint8) bool {
	start := uint64(address)
	end := start + uint64(width)
	base := uint64(b.Base)
	return end <= uint64(1)<<32 && start >= base && end <= base+b.Size
}

// RegisterWriteEffect is a single masked architectural write. Values outside
// Mask are inert. Writes to integer x0 are suppressed by every evaluator and
// completion helper and therefore never appear as an effect.
type RegisterWriteEffect struct {
	// ByteMask selects bytes within each lane; zero preserves legacy full-word writes.
	ByteMask    uint8
	Destination Register
	Mask        LaneMask
	Values      LaneValues
}

type PCReason uint8

const (
	PCSequential PCReason = iota
	PCBranch
	PCJump
	PCTrap
	PCTrapReturn
	PCReconverge
)

// ControlEffect is the requested warp-PC update. Branch Taken is decided from
// the highest-numbered active lane, mirroring VX_alu_int's last_tid path.
type ControlEffect struct {
	Reason       PCReason
	CurrentPC    uint32
	NextPC       uint32
	Target       uint32
	Taken        bool
	DecisionLane uint8
}

type MemoryRequest struct {
	Lane           uint8
	Kind           MemoryKind
	Address        uint32
	AlignedAddress uint32
	Width          uint8
	Signed         bool
	ByteMask       uint8
	StoreData      uint32
}

// MemoryResponse is supplied by the memory owner. Data is the little-endian
// value of exactly Request.Width bytes in its low bits. A service/bounds error
// is represented as FaultLoadAccess or FaultStoreAccess.
type MemoryResponse struct {
	Request MemoryRequest
	Data    uint32
	Fault   FaultKind
	Reason  FaultReason
}

type OrderingEffect struct {
	// Bits use the RISC-V fence order I/O/R/W in instruction bit order.
	Predecessor uint8
	Successor   uint8
}

// FFlags has the architectural RISC-V FFLAGS bit layout. It intentionally
// matches Berkeley SoftFloat's exception bits.
type FFlags uint8

const (
	FFlagInexact FFlags = 1 << iota
	FFlagUnderflow
	FFlagOverflow
	FFlagDivideByZero
	FFlagInvalid
)

// FFlagsEffect requests sticky accumulation: the FCSR owner ORs Accumulate
// into its existing FFLAGS. A non-nil zero-valued effect distinguishes a
// flag-producing operation that raised no exception from an instruction such
// as FSGNJ or FMV that must not touch FFLAGS at all.
type FFlagsEffect struct {
	Accumulate FFlags
}

// CSRScope identifies which future owner key supplies or receives a CSR
// value. It does not prescribe how that owner stores canonical state.
type CSRScope uint8

const (
	CSRScopeConstant CSRScope = iota
	CSRScopeLane
	CSRScopeWarp
	CSRScopeCTA
	CSRScopeCore
	CSRScopeCounter
)

// CSRReadEffect records the typed value view consumed by one CSR instruction.
// Values may differ by lane for THREAD_ID, MHARTID, and CTA thread coordinates.
type CSRReadEffect struct {
	Address uint16
	Scope   CSRScope
	Values  LaneValues
}

// CSRWriteEffect is a request to the canonical CSR owner. WriteMask is the
// implemented field mask (for example FFLAGS=0x1f); Ignored marks an RTL-legal
// write to a frozen zero CSR that has no storage in this configuration.
type CSRWriteEffect struct {
	Address   uint16
	Scope     CSRScope
	WarpID    uint8
	OldValue  uint32
	Value     uint32
	WriteMask uint32
	Ignored   bool
}

type TrapKind uint8

const (
	TrapEnter TrapKind = iota
	TrapReturn
)

// TrapEffect contains the scheduler-visible single-instruction boundary. Trap
// CSR writes remain separate CSRWriteEffects so there is still only one CSR
// owner. All three frozen xRET encodings use TrapReturn; no privilege engine is
// inferred beyond the RTL path.
type TrapEffect struct {
	Kind               TrapKind
	Cause              uint32
	EPC                uint32
	Vector             uint32
	SaveThreadMask     LaneMask
	RestoreThreadMask  LaneMask
	RestoresThreadMask bool
}

type WarpMaskReason uint8

const (
	WarpMaskTMC WarpMaskReason = iota
	WarpMaskPredicate
	WarpMaskSplit
	WarpMaskJoin
)

// WarpMaskEffect requests replacement of one warp's active lane mask. Active
// is the resulting lifecycle state; a zero TMC/PRED mask terminates the warp.
type WarpMaskEffect struct {
	WarpID uint8
	Reason WarpMaskReason
	Mask   LaneMask
	Active bool
}

// WarpSpawnEffect is a request to future Warp/CSR owners, never a scheduler
// action performed by ISA. RTL gates application until only the spawning warp
// is active and copies exactly mscratch to each target.
type WarpSpawnEffect struct {
	SourceWarp               uint8
	RequestedCount           uint8
	Targets                  WarpMask
	TargetPC                 uint32
	InitialLaneMask          LaneMask
	CopyMScratch             bool
	MScratch                 uint32
	RequiresSingleActiveWarp bool
	ReleaseSourceAfterApply  bool
}

type DivergenceAction uint8

const (
	DivergenceSplit DivergenceAction = iota
	DivergenceJoin
)

// DivergenceEffect describes one IPDOM-stack transaction and the exact mask
// selected by VX_split_join. The owner applies Push, MarkElseVisited, or Pop;
// the evaluator retains no stack or pointer state.
type DivergenceEffect struct {
	Action          DivergenceAction
	WarpID          uint8
	StackPointer    uint8
	Divergent       bool
	OriginalMask    LaneMask
	ExecuteMask     LaneMask
	DeferredMask    LaneMask
	ReconvergencePC uint32
	Push            bool
	MarkElseVisited bool
	Pop             bool
}

type WarpDrainKind uint8

const (
	DrainPriorInstructions WarpDrainKind = iota
	DrainLSU
)

// WarpDrainEffect maps cycle-level ready gating to an owner-facing predicate.
// Wait is true when the supplied view says work remains; ReleaseAfterDrain is
// the scheduler-visible completion boundary.
type WarpDrainEffect struct {
	WarpID            uint8
	Kind              WarpDrainKind
	Wait              bool
	ReleaseAfterDrain bool
}

// BarrierEffect is the full single-instruction request decoded by
// VX_wctl_unit. Coordination state (mask/count/events/phase) remains owned by
// the future barrier service.
type BarrierEffect struct {
	WarpID               uint8
	AddressWarp          uint8
	ID                   uint8
	Kind                 BarrierKind
	Global               bool
	Sync                 bool
	Arrive               bool
	Wait                 bool
	ReleaseByCoordinator bool
	Event                bool
	ExpectCount          uint8
	ParticipantCount     uint8
	SizeMinusOne         uint8
	Phase                bool
	DrainLSU             bool
}

// PackedLoadRequest identifies one element uop of a strided packed load. The
// memory owner returns exactly one response per (lane, element).
type PackedLoadRequest struct {
	Lane           uint8
	Element        uint8
	Address        uint32
	AlignedAddress uint32
	Width          uint8
}

type PackedLoadResponse struct {
	Request PackedLoadRequest
	Data    uint32
	Fault   FaultKind
	Reason  FaultReason
}

type FaultKind uint8

const (
	FaultNone FaultKind = iota
	FaultInstructionAddressMisaligned
	FaultLoadAddressMisaligned
	FaultStoreAddressMisaligned
	FaultLoadAccess
	FaultStoreAccess
)

type FaultReason uint8

const (
	FaultReasonNone FaultReason = iota
	FaultReasonAlignment
	FaultReasonBounds
	FaultReasonMemoryService
)

type FaultEffect struct {
	Kind    FaultKind
	Reason  FaultReason
	Lane    uint8
	Address uint32
	Width   uint8
}

// InstructionEffects is a description, not an applied transaction. A future
// router validates all contained effects before owners update canonical state.
type InstructionEffects struct {
	RegisterWrites []RegisterWriteEffect
	Control        *ControlEffect
	MemoryRequests []MemoryRequest
	Ordering       *OrderingEffect
	FFlags         *FFlagsEffect
	CSRReads       []CSRReadEffect
	CSRWrites      []CSRWriteEffect
	Trap           *TrapEffect
	WarpMasks      []WarpMaskEffect
	WarpSpawn      *WarpSpawnEffect
	Divergence     *DivergenceEffect
	WarpDrains     []WarpDrainEffect
	Barriers       []BarrierEffect
	PackedLoads    []PackedLoadRequest
	Faults         []FaultEffect
}

// The aliases retain milestone-specific API names while sharing the same
// owner-facing transaction vocabulary.
type IntegerEffects = InstructionEffects
type FloatEffects = InstructionEffects
type CustomEffects = InstructionEffects

// EvaluationError reports API misuse or a decoded operation outside the
// selected evaluator. Architectural faults are returned in the typed effects.
type EvaluationError struct {
	Instruction string
	Problem     string
}

func (e *EvaluationError) Error() string {
	return fmt.Sprintf("cannot evaluate %s: %s", e.Instruction, e.Problem)
}
