// Package isa describes and decodes the instruction set enabled by the
// repository's frozen Vortex configuration.  It deliberately owns no machine
// state: decoded operations only name their inputs, results, and effect
// boundaries for a future state/effect layer.
package isa

import "fmt"

// Category is the frozen extension or architectural group which owns an
// instruction.
type Category string

const (
	CategoryRV32I  Category = "RV32I"
	CategoryRV32M  Category = "RV32M"
	CategoryRV32F  Category = "RV32F"
	CategoryZicond Category = "Zicond"
	CategorySystem Category = "System/CSR"
	CategoryFence  Category = "Fence"
	CategoryCustom Category = "Vortex custom/SIMT"
)

// RegisterFile identifies an architectural register namespace without saying
// how (or where) a future State layer stores it.
type RegisterFile uint8

const (
	Integer RegisterFile = iota
	Float
)

// RegisterField identifies the instruction field containing a register index.
type RegisterField uint8

const (
	FieldRD RegisterField = iota
	FieldRS1
	FieldRS2
	FieldRS3
)

// OperandAccess distinguishes values read by evaluation from results returned
// to the effect layer. Some Vortex custom encodings intentionally use rd as an
// input and a destination.
type OperandAccess uint8

const (
	Read OperandAccess = iota
	Write
)

// OperandSpec is catalog metadata; Register is the corresponding decoded
// operation value.
type OperandSpec struct {
	Field  RegisterField
	File   RegisterFile
	Access OperandAccess
}

type Register struct {
	File  RegisterFile
	Index uint8
}

// ImmediateKind describes how Decode extracts and sign-extends an immediate.
type ImmediateKind uint8

const (
	ImmediateNone ImmediateKind = iota
	ImmediateI
	ImmediateS
	ImmediateB
	ImmediateU
	ImmediateJ
	ImmediateShift
	ImmediateCSR
)

// RoundingMode is either absent, a static IEEE rounding mode, or the dynamic
// FRM selection. Encodings 5 and 6 are never produced by the strict decoder.
type RoundingMode uint8

const (
	RoundingNone RoundingMode = iota
	RNE
	RTZ
	RDN
	RUP
	RMM
	Dynamic
)

type FloatFormat uint8

const (
	FormatNone FloatFormat = iota
	FormatSingle
)

// ResultKind says what evaluation returns before an owner applies effects.
type ResultKind uint8

const (
	ResultNone ResultKind = iota
	ResultInteger
	ResultFloat
	ResultMemoryValue
	ResultCSRValue
	ResultControlValue
)

// EffectKind is a bit set of owner/service boundaries potentially crossed by
// one instruction. Register results are included so catalog coverage can be
// audited without inspecting Go implementation details.
type EffectKind uint32

const (
	EffectRegister EffectKind = 1 << iota
	EffectMemory
	EffectOrdering
	EffectCSR
	EffectControl
	EffectFFlags
	EffectLane
	EffectWarp
	EffectBarrier
)

// WriteMask states how a future effect router selects destination lanes.
type WriteMask uint8

const (
	WriteMaskNone WriteMask = iota
	WriteMaskActive
	// WGATHER suppresses the selected source lane and writes every other lane in
	// each four-lane group, including lanes absent from the input active mask
	// (VX_alu_int.sv:160-177 and E-LANE-01 in docs/architecture.md).
	WriteMaskGatherNonSource
)

type MemoryKind uint8

const (
	MemoryNone MemoryKind = iota
	MemoryLoad
	MemoryStore
	MemoryFence
)

type MemorySpec struct {
	Kind   MemoryKind
	Bytes  uint8
	Signed bool
	Float  bool
	Packed uint8
}

type CSRKind uint8

const (
	CSRNone CSRKind = iota
	CSRRW
	CSRRS
	CSRRC
)

type ControlKind uint8

const (
	ControlNone ControlKind = iota
	ControlBranch
	ControlJump
	ControlTrap
	ControlTrapReturn
	ControlThreadMask
	ControlWarpSpawn
	ControlSplit
	ControlJoin
	ControlPredicate
	ControlWarpSync
)

type BarrierKind uint8

const (
	BarrierNone BarrierKind = iota
	BarrierSync
	BarrierArrive
	BarrierWait
)

type ModifierKind uint8

const (
	ModifierNone ModifierKind = iota
	ModifierSplitNegateRS2
	ModifierPredicateNegateRD
	ModifierGatherSourceLane
)

// ValueConstraint closes legality holes which cannot be represented by a
// single match/mask pair (currently legal FP rounding modes and non-zero rd).
// Mask selects a contiguous instruction field and Values are unshifted.
type ValueConstraint struct {
	Name    string
	Mask    uint32
	Shift   uint8
	Values  []uint32
	NonZero bool
}

func (c ValueConstraint) accepts(word uint32) bool {
	v := (word & c.Mask) >> c.Shift
	if c.NonZero && v == 0 {
		return false
	}
	if len(c.Values) == 0 {
		return true
	}
	for _, allowed := range c.Values {
		if v == allowed {
			return true
		}
	}
	return false
}

// Entry is one authoritative catalog row. Example is an auditable legal word
// used by reachability tests; RTLEvidence contains repository-relative source
// locations for both decode and, where relevant, the consuming execute path.
type Entry struct {
	Name               string
	Category           Category
	Match              uint32
	Mask               uint32
	Constraints        []ValueConstraint
	Operands           []OperandSpec
	Immediate          ImmediateKind
	Rounding           bool
	Format             FloatFormat
	Result             ResultKind
	Effects            EffectKind
	RequiresPC         bool
	RequiresActiveMask bool
	WriteMask          WriteMask
	Memory             MemorySpec
	CSR                CSRKind
	Control            ControlKind
	Barrier            BarrierKind
	Modifier           ModifierKind
	RTLEvidence        []string
	Example            uint32
}

func (e Entry) accepts(word uint32) bool {
	if word&e.Mask != e.Match {
		return false
	}
	for _, constraint := range e.Constraints {
		if !constraint.accepts(word) {
			return false
		}
	}
	return true
}

// Decoded is independent of any register arrays or canonical State layout.
// PC is intentionally not an input to Decode; branch/jump immediates and the
// Control boundary tell the future execution layer how PC participates.
type Decoded struct {
	Word               uint32
	Name               string
	Category           Category
	Sources            []Register
	Destinations       []Register
	HasImmediate       bool
	Immediate          int32
	Rounding           RoundingMode
	Format             FloatFormat
	Result             ResultKind
	Effects            EffectKind
	RequiresPC         bool
	RequiresActiveMask bool
	WriteMask          WriteMask
	Memory             MemorySpec
	CSR                CSRKind
	CSRAddress         uint16
	CSRImmediate       bool
	CSRImmediateValue  uint8
	Control            ControlKind
	Barrier            BarrierKind
	ConditionNegated   bool
	GatherSourceLane   uint8
}

// IllegalInstructionError is the deterministic result for disabled, reserved,
// malformed, or otherwise non-catalog words.
type IllegalInstructionError struct {
	Word uint32
}

func (e *IllegalInstructionError) Error() string {
	return fmt.Sprintf("illegal or disabled instruction 0x%08x", e.Word)
}
