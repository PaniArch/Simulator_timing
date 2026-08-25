// Package softfloat exposes deterministic IEEE-754 binary32 mathematics.
package softfloat

/*
#include "bridge.h"
*/
import "C"

import (
	"fmt"
	"sync"
)

// RoundingMode selects a Berkeley SoftFloat rounding mode.
type RoundingMode uint8

const (
	RoundNearEven   RoundingMode = 0
	RoundTowardZero RoundingMode = 1
	RoundDown       RoundingMode = 2
	RoundUp         RoundingMode = 3
	RoundNearAway   RoundingMode = 4
	RoundOdd        RoundingMode = 6
)

// ExceptionFlags is the bit set reported by Berkeley SoftFloat.
type ExceptionFlags uint8

const (
	FlagInexact      ExceptionFlags = 1
	FlagUnderflow    ExceptionFlags = 2
	FlagOverflow     ExceptionFlags = 4
	FlagDivideByZero ExceptionFlags = 8
	FlagInvalid      ExceptionFlags = 16
)

// Result32 carries an F32 or 32-bit integer bit pattern and its exceptions.
type Result32 struct {
	Bits  uint32
	Flags ExceptionFlags
}

// CompareResult carries a comparison result and its exceptions.
type CompareResult struct {
	Value bool
	Flags ExceptionFlags
}

var stateMu sync.Mutex

func validateRoundingMode(mode RoundingMode) error {
	switch mode {
	case RoundNearEven, RoundTowardZero, RoundDown, RoundUp, RoundNearAway, RoundOdd:
		return nil
	default:
		return fmt.Errorf("softfloat: unsupported rounding mode %d", mode)
	}
}

func result32(result C.sf_result32) Result32 {
	return Result32{Bits: uint32(result.bits), Flags: ExceptionFlags(result.flags)}
}

func compareResult(result C.sf_compare_result) CompareResult {
	return CompareResult{Value: bool(result.value), Flags: ExceptionFlags(result.flags)}
}

// F32Add returns a+b using raw IEEE-754 binary32 operands.
func F32Add(a, b uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_add(C.uint32_t(a), C.uint32_t(b), C.uint8_t(mode))), nil
}

// F32Sub returns a-b using raw IEEE-754 binary32 operands.
func F32Sub(a, b uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_sub(C.uint32_t(a), C.uint32_t(b), C.uint8_t(mode))), nil
}

// F32Mul returns a*b using raw IEEE-754 binary32 operands.
func F32Mul(a, b uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_mul(C.uint32_t(a), C.uint32_t(b), C.uint8_t(mode))), nil
}

// F32Div returns a/b using raw IEEE-754 binary32 operands.
func F32Div(a, b uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_div(C.uint32_t(a), C.uint32_t(b), C.uint8_t(mode))), nil
}

// F32Sqrt returns sqrt(a) using a raw IEEE-754 binary32 operand.
func F32Sqrt(a uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_sqrt(C.uint32_t(a), C.uint8_t(mode))), nil
}

// F32FMA returns the fused result a*b+c using raw binary32 operands.
func F32FMA(a, b, c uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_fma(C.uint32_t(a), C.uint32_t(b), C.uint32_t(c), C.uint8_t(mode))), nil
}

// I32ToF32 interprets bits as a two's-complement int32 and returns F32 bits.
func I32ToF32(bits uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_i32_to_f32(C.uint32_t(bits), C.uint8_t(mode))), nil
}

// UI32ToF32 converts an unsigned 32-bit bit pattern to F32 bits.
func UI32ToF32(bits uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_ui32_to_f32(C.uint32_t(bits), C.uint8_t(mode))), nil
}

// F32ToI32 returns the rounded two's-complement int32 bit pattern.
func F32ToI32(bits uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_to_i32(C.uint32_t(bits), C.uint8_t(mode))), nil
}

// F32ToUI32 returns the rounded unsigned 32-bit bit pattern.
func F32ToUI32(bits uint32, mode RoundingMode) (Result32, error) {
	if err := validateRoundingMode(mode); err != nil {
		return Result32{}, err
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	return result32(C.sf_f32_to_ui32(C.uint32_t(bits), C.uint8_t(mode))), nil
}

// F32Equal performs SoftFloat's quiet equality comparison.
func F32Equal(a, b uint32) CompareResult {
	stateMu.Lock()
	defer stateMu.Unlock()
	return compareResult(C.sf_f32_eq(C.uint32_t(a), C.uint32_t(b)))
}

// F32LessThan performs SoftFloat's signaling less-than comparison.
func F32LessThan(a, b uint32) CompareResult {
	stateMu.Lock()
	defer stateMu.Unlock()
	return compareResult(C.sf_f32_lt(C.uint32_t(a), C.uint32_t(b)))
}

// F32LessOrEqual performs SoftFloat's signaling less-or-equal comparison.
func F32LessOrEqual(a, b uint32) CompareResult {
	stateMu.Lock()
	defer stateMu.Unlock()
	return compareResult(C.sf_f32_le(C.uint32_t(a), C.uint32_t(b)))
}
