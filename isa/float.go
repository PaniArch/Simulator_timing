package isa

import "vortex.local/simulator/support/softfloat"

const (
	f32Sign       = uint32(1 << 31)
	f32Exponent   = uint32(0x7f800000)
	f32Fraction   = uint32(0x007fffff)
	f32Quiet      = uint32(0x00400000)
	f32CanonicalQ = uint32(0x7fc00000)
)

// EvaluateFloat evaluates one already-decoded frozen RV32F instruction. All
// operands and results are raw binary32/GPR bits. It neither owns FPR/GPR/FCSR
// state nor applies memory requests or sticky flags.
func EvaluateFloat(decoded Decoded, input FloatInput) (FloatEffects, error) {
	if !input.ActiveMask.Valid() {
		return FloatEffects{}, evalError(decoded, "active mask exceeds four frozen lanes")
	}
	if input.Bounds != nil && !input.Bounds.valid() {
		return FloatEffects{}, evalError(decoded, "invalid memory bounds view")
	}
	if decoded.Category != CategoryRV32F || decoded.Format != FormatSingle {
		return FloatEffects{}, evalError(decoded, "instruction is outside frozen RV32F")
	}
	if decoded.Memory.Kind != MemoryNone {
		if !decoded.Memory.Float || (decoded.Name != "flw" && decoded.Name != "fsw") {
			return FloatEffects{}, evalError(decoded, "unsupported floating memory operation")
		}
		return evaluateMemory(decoded, IntegerInput{
			PC: input.PC, ActiveMask: input.ActiveMask, RS1: input.RS1,
			RS2: input.RS2, Bounds: input.Bounds,
		})
	}

	rounding, err := resolveFloatRounding(decoded, input.FRM)
	if err != nil {
		return FloatEffects{}, err
	}

	var values LaneValues
	var flags FFlags
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		if !input.ActiveMask.Active(lane) {
			continue
		}
		result, laneFlags, ok, laneErr := evaluateFloatLane(
			decoded.Name, input.RS1[lane], input.RS2[lane], input.RS3[lane], rounding,
		)
		if laneErr != nil {
			return FloatEffects{}, evalError(decoded, laneErr.Error())
		}
		if !ok {
			return FloatEffects{}, evalError(decoded, "missing RV32F functional semantics")
		}
		values[lane] = result
		flags |= FFlags(laneFlags)
	}

	effects := FloatEffects{Control: sequentialControl(input.PC)}
	appendRegisterWrite(&effects, decoded, input.ActiveMask, values)
	if decoded.Effects&EffectFFlags != 0 {
		effects.FFlags = &FFlagsEffect{Accumulate: flags}
	}
	return effects, nil
}

// CompleteFloatMemory converts FLW/FSW owner responses into FPR writes,
// faults, and PC+4 without reading or mutating memory inside the ISA package.
func CompleteFloatMemory(decoded Decoded, pc uint32, expected LaneMask, responses []MemoryResponse) (FloatEffects, error) {
	if decoded.Category != CategoryRV32F || !decoded.Memory.Float {
		return FloatEffects{}, evalError(decoded, "instruction is not floating memory")
	}
	return CompleteMemory(decoded, pc, expected, responses)
}

func resolveFloatRounding(decoded Decoded, frm RoundingMode) (softfloat.RoundingMode, error) {
	rounding := decoded.Rounding
	if rounding == Dynamic {
		rounding = frm
	}
	if decoded.Rounding == RoundingNone {
		return softfloat.RoundNearEven, nil
	}
	switch rounding {
	case RNE:
		return softfloat.RoundNearEven, nil
	case RTZ:
		return softfloat.RoundTowardZero, nil
	case RDN:
		return softfloat.RoundDown, nil
	case RUP:
		return softfloat.RoundUp, nil
	case RMM:
		return softfloat.RoundNearAway, nil
	default:
		return 0, evalError(decoded, "dynamic rounding requires a legal FRM value")
	}
}

func evaluateFloatLane(name string, a, b, c uint32, rounding softfloat.RoundingMode) (uint32, softfloat.ExceptionFlags, bool, error) {
	var result softfloat.Result32
	var err error
	switch name {
	case "fadd.s":
		result, err = softfloat.F32Add(a, b, rounding)
	case "fsub.s":
		result, err = softfloat.F32Sub(a, b, rounding)
	case "fmul.s":
		result, err = softfloat.F32Mul(a, b, rounding)
	case "fdiv.s":
		result, err = softfloat.F32Div(a, b, rounding)
	case "fsqrt.s":
		result, err = softfloat.F32Sqrt(a, rounding)
	case "fmadd.s":
		result, err = softfloat.F32FMA(a, b, c, rounding)
	case "fmsub.s":
		result, err = softfloat.F32FMA(a, b, c^f32Sign, rounding)
	case "fnmsub.s":
		result, err = softfloat.F32FMA(a^f32Sign, b, c, rounding)
	case "fnmadd.s":
		result, err = softfloat.F32FMA(a^f32Sign, b, c^f32Sign, rounding)
	case "fcvt.w.s":
		result, err = softfloat.F32ToI32(a, rounding)
	case "fcvt.wu.s":
		result, err = softfloat.F32ToUI32(a, rounding)
	case "fcvt.s.w":
		result, err = softfloat.I32ToF32(a, rounding)
	case "fcvt.s.wu":
		result, err = softfloat.UI32ToF32(a, rounding)
	case "fsgnj.s":
		return a&^f32Sign | b&f32Sign, 0, true, nil
	case "fsgnjn.s":
		return a&^f32Sign | (^b)&f32Sign, 0, true, nil
	case "fsgnjx.s":
		return a ^ (b & f32Sign), 0, true, nil
	case "fmin.s", "fmax.s":
		bits, cmpFlags := floatMinMax(name == "fmax.s", a, b)
		return bits, cmpFlags, true, nil
	case "feq.s":
		comparison := softfloat.F32Equal(a, b)
		return boolWord(comparison.Value), comparison.Flags, true, nil
	case "flt.s":
		comparison := softfloat.F32LessThan(a, b)
		return boolWord(comparison.Value), comparison.Flags, true, nil
	case "fle.s":
		comparison := softfloat.F32LessOrEqual(a, b)
		return boolWord(comparison.Value), comparison.Flags, true, nil
	case "fclass.s":
		return floatClass(a), 0, true, nil
	case "fmv.x.w", "fmv.w.x":
		return a, 0, true, nil
	default:
		return 0, 0, false, nil
	}
	return result.Bits, result.Flags, true, err
}

func floatMinMax(maximum bool, a, b uint32) (uint32, softfloat.ExceptionFlags) {
	aNaN, bNaN := floatNaN(a), floatNaN(b)
	flags := softfloat.ExceptionFlags(0)
	if floatSignalingNaN(a) || floatSignalingNaN(b) {
		flags = softfloat.FlagInvalid
	}
	if aNaN && bNaN {
		return f32CanonicalQ, flags
	}
	if aNaN {
		return b, flags
	}
	if bNaN {
		return a, flags
	}
	if a<<1 == 0 && b<<1 == 0 {
		if maximum {
			return a & b, flags // +0 wins; -0 only if both are -0.
		}
		return a | b, flags // -0 wins if either operand is -0.
	}
	less := softfloat.F32LessThan(a, b)
	if maximum {
		if less.Value {
			return b, flags
		}
		return a, flags
	}
	if less.Value {
		return a, flags
	}
	return b, flags
}

func floatNaN(bits uint32) bool {
	return bits&f32Exponent == f32Exponent && bits&f32Fraction != 0
}

func floatSignalingNaN(bits uint32) bool {
	return floatNaN(bits) && bits&f32Quiet == 0
}

func floatClass(bits uint32) uint32 {
	sign := bits&f32Sign != 0
	exponent := bits & f32Exponent
	fraction := bits & f32Fraction
	switch {
	case exponent == f32Exponent && fraction == 0:
		if sign {
			return 1 << 0
		}
		return 1 << 7
	case exponent == f32Exponent:
		if bits&f32Quiet == 0 {
			return 1 << 8
		}
		return 1 << 9
	case exponent == 0 && fraction == 0:
		if sign {
			return 1 << 3
		}
		return 1 << 4
	case exponent == 0:
		if sign {
			return 1 << 2
		}
		return 1 << 5
	default:
		if sign {
			return 1 << 1
		}
		return 1 << 6
	}
}
