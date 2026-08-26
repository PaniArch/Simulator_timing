package isa

import "fmt"

const (
	TrapCauseBreakpoint           = uint32(3)
	TrapCauseEnvironmentCallMMode = uint32(11)
)

// CTAView is immutable context supplied by the CTA owner. ThreadCoordinates
// is indexed [x/y/z][lane]; Entry is already a full byte-addressed PC.
type CTAView struct {
	ID                 uint32
	Rank               uint32
	Size               uint32
	ThreadCoordinates  [3]LaneValues
	BlockID            [3]uint32
	BlockDimensions    [3]uint32
	GridDimensions     [3]uint32
	BlockSize          uint32
	WarpStep           [3]uint32
	ParameterAddress   uint32
	LocalMemoryAddress uint32
	ClusterDimensions  [3]uint32
	ClusterSize        uint32
	Entry              uint32
}

type CounterView struct {
	Cycle   uint64
	Instret uint64
}

// CSRView is a per-instruction snapshot, not canonical CSR storage. Fields
// mirror values physically distributed across CSR data, scheduler, and CTA
// tables in RTL; a future owner constructs this view for the selected warp.
type CSRView struct {
	WarpID          uint8
	CoreID          uint8
	ActiveWarps     uint8
	ThreadMask      LaneMask
	SavedThreadMask LaneMask

	FCSR     uint32
	MStatus  uint32
	MTVec    uint32
	MScratch uint32
	MEPC     uint32
	MCause   uint32
	MTVal    uint32

	CTA      CTAView
	Counters CounterView
}

// SystemInput supplies the current instruction operands and the minimum CSR
// context snapshot. CSR register-source operations intentionally use RS1 lane
// zero, matching VX_csr_unit rather than inventing a warp-wide election rule.
type SystemInput struct {
	PC         uint32
	ActiveMask LaneMask
	RS1        LaneValues
	CSR        CSRView
}

type CSRAccessProblem uint8

const (
	CSRUnknownAddress CSRAccessProblem = iota
	CSRReadOnlyWrite
)

// CSRAccessError is the deterministic, effect-free result for an unknown CSR
// or an attempted write to a strict read-only address.
type CSRAccessError struct {
	Instruction string
	Address     uint16
	Problem     CSRAccessProblem
}

func (e *CSRAccessError) Error() string {
	problem := "unknown CSR address"
	if e.Problem == CSRReadOnlyWrite {
		problem = "write to read-only CSR"
	}
	return fmt.Sprintf("cannot evaluate %s at CSR 0x%03x: %s", e.Instruction, e.Address, problem)
}

// EvaluateSystem evaluates one strict CSR or synchronous System instruction.
// It consumes only the supplied snapshot and returns owner-routed effects.
func EvaluateSystem(decoded Decoded, input SystemInput) (InstructionEffects, error) {
	if !input.ActiveMask.Valid() {
		return InstructionEffects{}, evalError(decoded, "active mask exceeds four frozen lanes")
	}
	if input.CSR.WarpID >= FrozenWarpCount || input.CSR.CoreID >= FrozenCoreCount ||
		input.CSR.ActiveWarps&^uint8((1<<FrozenWarpCount)-1) != 0 ||
		!input.CSR.ThreadMask.Valid() || !input.CSR.SavedThreadMask.Valid() {
		return InstructionEffects{}, evalError(decoded, "CSR context exceeds frozen topology")
	}
	if decoded.Category != CategorySystem {
		return InstructionEffects{}, evalError(decoded, "instruction is outside System/CSR")
	}
	if decoded.CSR != CSRNone {
		return evaluateCSR(decoded, input)
	}
	return evaluateTrapSystem(decoded, input)
}

func evaluateCSR(decoded Decoded, input SystemInput) (InstructionEffects, error) {
	entry, ok := findCSR(decoded.CSRAddress)
	if !ok {
		return InstructionEffects{}, &CSRAccessError{Instruction: decoded.Name, Address: decoded.CSRAddress, Problem: CSRUnknownAddress}
	}
	values := readCSR(entry.Value, input.CSR)
	source := input.RS1[0]
	if decoded.CSRImmediate {
		source = uint32(decoded.CSRImmediateValue)
	}
	writeRequested := decoded.CSR == CSRRW || source != 0
	if writeRequested && !entry.Writable && !entry.WriteIgnored {
		return InstructionEffects{}, &CSRAccessError{Instruction: decoded.Name, Address: decoded.CSRAddress, Problem: CSRReadOnlyWrite}
	}

	effects := InstructionEffects{
		Control:  sequentialControl(input.PC),
		CSRReads: []CSRReadEffect{{Address: entry.Address, Scope: entry.Scope, Values: values}},
	}
	appendRegisterWrite(&effects, decoded, input.ActiveMask, values)
	if !writeRequested {
		return effects, nil
	}

	oldValue := values[0]
	newValue := source
	switch decoded.CSR {
	case CSRRS:
		newValue = oldValue | source
	case CSRRC:
		newValue = oldValue &^ source
	}
	write := CSRWriteEffect{
		Address: entry.Address, Scope: entry.Scope, WarpID: input.CSR.WarpID,
		OldValue: oldValue, WriteMask: entry.WriteMask, Ignored: entry.WriteIgnored,
	}
	if entry.Writable {
		write.Value = newValue & entry.WriteMask
	}
	effects.CSRWrites = []CSRWriteEffect{write}
	return effects, nil
}

func evaluateTrapSystem(decoded Decoded, input SystemInput) (InstructionEffects, error) {
	switch decoded.Name {
	case "ecall", "ebreak":
		if input.ActiveMask == 0 || input.CSR.ThreadMask == 0 {
			return InstructionEffects{}, evalError(decoded, "trap entry requires a running thread mask")
		}
		cause := TrapCauseEnvironmentCallMMode
		if decoded.Name == "ebreak" {
			cause = TrapCauseBreakpoint
		}
		vector := input.CSR.MTVec &^ 3
		return InstructionEffects{
			Control: &ControlEffect{Reason: PCTrap, CurrentPC: input.PC, NextPC: vector, Target: vector, Taken: true},
			CSRWrites: []CSRWriteEffect{
				trapCSRWrite(0x341, input.CSR.WarpID, input.CSR.MEPC, input.PC),
				trapCSRWrite(0x342, input.CSR.WarpID, input.CSR.MCause, cause),
				trapCSRWrite(0x343, input.CSR.WarpID, input.CSR.MTVal, 0),
			},
			Trap: &TrapEffect{
				Kind: TrapEnter, Cause: cause, EPC: input.PC, Vector: vector,
				SaveThreadMask: input.CSR.ThreadMask,
			},
		}, nil
	case "uret", "sret", "mret":
		if input.ActiveMask == 0 {
			return InstructionEffects{}, evalError(decoded, "trap return requires an active lane")
		}
		target := input.CSR.MEPC &^ 3
		restore := input.CSR.SavedThreadMask != 0
		return InstructionEffects{
			Control: &ControlEffect{Reason: PCTrapReturn, CurrentPC: input.PC, NextPC: target, Target: target, Taken: true},
			Trap: &TrapEffect{
				Kind: TrapReturn, EPC: input.CSR.MEPC,
				RestoreThreadMask: input.CSR.SavedThreadMask, RestoresThreadMask: restore,
			},
		}, nil
	default:
		return InstructionEffects{}, evalError(decoded, "missing System functional semantics")
	}
}

func trapCSRWrite(address uint16, warpID uint8, oldValue, value uint32) CSRWriteEffect {
	return CSRWriteEffect{
		Address: address, Scope: CSRScopeWarp, WarpID: warpID,
		OldValue: oldValue, Value: value, WriteMask: 0xffffffff,
	}
}

func readCSR(kind CSRValueKind, view CSRView) LaneValues {
	var values LaneValues
	value := uint32(0)
	switch kind {
	case csrValueFFlags:
		value = view.FCSR & 0x1f
	case csrValueFRM:
		value = view.FCSR >> 5 & 0x07
	case csrValueFCSR:
		value = view.FCSR & 0xff
	case csrValueMStatus:
		value = view.MStatus
	case csrValueMTVec:
		value = view.MTVec
	case csrValueMScratch:
		value = view.MScratch
	case csrValueMEPC:
		value = view.MEPC
	case csrValueMCause:
		value = view.MCause
	case csrValueMTVal:
		value = view.MTVal
	case csrValueMISA:
		value = FrozenMISA
	case csrValueThreadID:
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			values[lane] = uint32(lane)
		}
		return values
	case csrValueHartID:
		base := uint32(view.CoreID)*FrozenWarpCount*FrozenLaneCount + uint32(view.WarpID)*FrozenLaneCount
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			values[lane] = base + uint32(lane)
		}
		return values
	case csrValueWarpID:
		value = uint32(view.WarpID)
	case csrValueCoreID:
		value = uint32(view.CoreID)
	case csrValueActiveWarps:
		value = uint32(view.ActiveWarps)
	case csrValueActiveThreads:
		value = uint32(view.ThreadMask)
	case csrValueNumThreads:
		value = FrozenLaneCount
	case csrValueNumWarps:
		value = FrozenWarpCount
	case csrValueNumCores:
		value = FrozenCoreCount
	case csrValueLocalMemBase:
		value = FrozenLocalMemBase
	case csrValueNumBarriers:
		value = FrozenBarrierCount
	case csrValueCTAID:
		value = view.CTA.ID
	case csrValueCTARank:
		value = view.CTA.Rank
	case csrValueCTASize:
		value = view.CTA.Size
	case csrValueCTAThreadX:
		return view.CTA.ThreadCoordinates[0]
	case csrValueCTAThreadY:
		return view.CTA.ThreadCoordinates[1]
	case csrValueCTAThreadZ:
		return view.CTA.ThreadCoordinates[2]
	case csrValueCTABlockX:
		value = view.CTA.BlockID[0]
	case csrValueCTABlockY:
		value = view.CTA.BlockID[1]
	case csrValueCTABlockZ:
		value = view.CTA.BlockID[2]
	case csrValueCTABlockDimX:
		value = view.CTA.BlockDimensions[0]
	case csrValueCTABlockDimY:
		value = view.CTA.BlockDimensions[1]
	case csrValueCTABlockDimZ:
		value = view.CTA.BlockDimensions[2]
	case csrValueCTAGridDimX:
		value = view.CTA.GridDimensions[0]
	case csrValueCTAGridDimY:
		value = view.CTA.GridDimensions[1]
	case csrValueCTAGridDimZ:
		value = view.CTA.GridDimensions[2]
	case csrValueCTALMemAddress:
		value = view.CTA.LocalMemoryAddress
	case csrValueCTAClusterSize:
		value = view.CTA.ClusterSize
	case csrValueCTAEntry:
		value = view.CTA.Entry
	case csrValueCycleLow:
		value = uint32(view.Counters.Cycle)
	case csrValueCycleHigh:
		value = uint32(view.Counters.Cycle>>32) & ((1 << (FrozenCounterBits - 32)) - 1)
	case csrValueInstretLow:
		value = uint32(view.Counters.Instret)
	case csrValueInstretHigh:
		value = uint32(view.Counters.Instret>>32) & ((1 << (FrozenCounterBits - 32)) - 1)
	case csrValueZero:
		value = 0
	}
	return filledValues(value)
}
