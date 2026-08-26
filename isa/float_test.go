package isa

import (
	"errors"
	"testing"

	memsupport "vortex.local/simulator/support/memory"
	"vortex.local/simulator/support/softfloat"
)

func evaluateFloatOne(t *testing.T, decoded Decoded, a, b, c uint32, frm RoundingMode) FloatEffects {
	t.Helper()
	input := FloatInput{PC: 0x100, ActiveMask: 1, FRM: frm}
	input.RS1[0], input.RS2[0], input.RS3[0] = a, b, c
	effects, err := EvaluateFloat(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func floatWriteValue(t *testing.T, effects FloatEffects) uint32 {
	t.Helper()
	if len(effects.RegisterWrites) != 1 || effects.RegisterWrites[0].Mask != 1 {
		t.Fatalf("register effects=%+v", effects.RegisterWrites)
	}
	if effects.Control == nil || effects.Control.NextPC != 0x104 {
		t.Fatalf("control effect=%+v", effects.Control)
	}
	return effects.RegisterWrites[0].Values[0]
}

func TestRV32FFunctionalCatalogCoverage(t *testing.T) {
	tests := []struct {
		name       string
		a, b, c    uint32
		want       uint32
		flagsTouch bool
	}{
		{"fmadd.s", 0x3fc00000, 0x40000000, 0x3f000000, 0x40600000, true},
		{"fmsub.s", 0x3fc00000, 0x40000000, 0x3f000000, 0x40200000, true},
		{"fnmsub.s", 0x3fc00000, 0x40000000, 0x3f000000, 0xc0200000, true},
		{"fnmadd.s", 0x3fc00000, 0x40000000, 0x3f000000, 0xc0600000, true},
		{"fadd.s", 0x3fc00000, 0x40100000, 0, 0x40700000, true},
		{"fsub.s", 0x40700000, 0x40100000, 0, 0x3fc00000, true},
		{"fmul.s", 0x3fc00000, 0x40000000, 0, 0x40400000, true},
		{"fdiv.s", 0x40400000, 0x40000000, 0, 0x3fc00000, true},
		{"fsqrt.s", 0x40800000, 0, 0, 0x40000000, true},
		{"fsgnj.s", 0x3fc00000, 0xc0000000, 0, 0xbfc00000, false},
		{"fsgnjn.s", 0x3fc00000, 0xc0000000, 0, 0x3fc00000, false},
		{"fsgnjx.s", 0x3fc00000, 0xc0000000, 0, 0xbfc00000, false},
		{"fmin.s", 0x3fc00000, 0x40000000, 0, 0x3fc00000, true},
		{"fmax.s", 0x3fc00000, 0x40000000, 0, 0x40000000, true},
		{"fle.s", 0x3f800000, 0x40000000, 0, 1, true},
		{"flt.s", 0x3f800000, 0x40000000, 0, 1, true},
		{"feq.s", 0x3f800000, 0x3f800000, 0, 1, true},
		{"fcvt.w.s", 0xc0000000, 0, 0, 0xfffffffe, true},
		{"fcvt.wu.s", 0x40400000, 0, 0, 3, true},
		{"fcvt.s.w", 0xfffffffe, 0, 0, 0xc0000000, true},
		{"fcvt.s.wu", 3, 0, 0, 0x40400000, true},
		{"fmv.x.w", 0x81234567, 0, 0, 0x81234567, false},
		{"fclass.s", 0x7fc00000, 0, 0, 1 << 9, false},
		{"fmv.w.x", 0x81234567, 0, 0, 0x81234567, false},
	}
	covered := make(map[string]bool, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			effects := evaluateFloatOne(t, decoded, test.a, test.b, test.c, RNE)
			if got := floatWriteValue(t, effects); got != test.want {
				t.Fatalf("result=%#08x, want %#08x", got, test.want)
			}
			if test.flagsTouch {
				if effects.FFlags == nil || effects.FFlags.Accumulate != 0 {
					t.Fatalf("FFLAGS effect=%+v", effects.FFlags)
				}
			} else if effects.FFlags != nil {
				t.Fatalf("flag-free instruction touched FFLAGS: %+v", effects.FFlags)
			}
			covered[test.name] = true
		})
	}
	assertCatalogFunctionallyCovered(t, covered, CategoryRV32F)
}

func decodeWithRM(t *testing.T, name string, encoded uint32) Decoded {
	t.Helper()
	for _, entry := range catalog {
		if entry.Name == name {
			decoded, err := Decode(entry.Example&^maskRM | encoded<<12)
			if err != nil {
				t.Fatal(err)
			}
			return decoded
		}
	}
	t.Fatalf("catalog instruction %q not found", name)
	return Decoded{}
}

func TestRV32FStaticAndDynamicRoundingModes(t *testing.T) {
	tests := []struct {
		encoded uint32
		mode    RoundingMode
		want    uint32
	}{
		{0, RNE, 0x3f800000},
		{1, RTZ, 0x3f800000},
		{2, RDN, 0x3f800000},
		{3, RUP, 0x3f800001},
		{4, RMM, 0x3f800001},
	}
	for _, test := range tests {
		static := decodeWithRM(t, "fadd.s", test.encoded)
		effects := evaluateFloatOne(t, static, 0x3f800000, 0x33800000, 0, RoundingNone)
		if got := floatWriteValue(t, effects); got != test.want || effects.FFlags.Accumulate != FFlagInexact {
			t.Errorf("static %v: bits=%#x flags=%#x", test.mode, got, effects.FFlags.Accumulate)
		}

		dynamic := decodeWithRM(t, "fadd.s", 7)
		effects = evaluateFloatOne(t, dynamic, 0x3f800000, 0x33800000, 0, test.mode)
		if got := floatWriteValue(t, effects); got != test.want || effects.FFlags.Accumulate != FFlagInexact {
			t.Errorf("dynamic %v: bits=%#x flags=%#x", test.mode, got, effects.FFlags.Accumulate)
		}
	}

	dynamic := decodeWithRM(t, "fadd.s", 7)
	for _, invalid := range []RoundingMode{RoundingNone, Dynamic, RoundingMode(99)} {
		_, err := EvaluateFloat(dynamic, FloatInput{ActiveMask: 1, FRM: invalid})
		var evaluation *EvaluationError
		if !errors.As(err, &evaluation) {
			t.Errorf("FRM %d error=%T %v", invalid, err, err)
		}
	}
}

func TestRV32FExceptionalValuesAndStickyLaneMerge(t *testing.T) {
	tests := []struct {
		name    string
		a, b    uint32
		want    uint32
		flags   FFlags
		bitOnly bool
	}{
		{"fadd.s", 0x00000000, 0x80000000, 0x00000000, 0, false},
		{"fadd.s", 0x00000001, 0x00000001, 0x00000002, 0, false},
		{"fadd.s", 0x7f800000, 0xff800000, 0x7fc00000, FFlagInvalid, false},
		{"fadd.s", 0x7fc00000, 0x3f800000, 0x7fc00000, 0, false},
		{"fadd.s", 0x7f800001, 0x3f800000, 0, FFlagInvalid, true},
		{"fmul.s", 0x7f7fffff, 0x40000000, 0x7f800000, FFlagOverflow | FFlagInexact, false},
		{"fmul.s", 0x00800000, 0x00800000, 0, FFlagUnderflow | FFlagInexact, false},
		{"fdiv.s", 0x3f800000, 0, 0x7f800000, FFlagDivideByZero, false},
		{"fsqrt.s", 0xbf800000, 0, 0x7fc00000, FFlagInvalid, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effects := evaluateFloatOne(t, decodedByName(t, test.name), test.a, test.b, 0, RNE)
			got := floatWriteValue(t, effects)
			if (!test.bitOnly && got != test.want) || effects.FFlags == nil || effects.FFlags.Accumulate != test.flags {
				t.Fatalf("bits=%#08x flags=%+v, want bits=%#08x flags=%#x", got, effects.FFlags, test.want, test.flags)
			}
		})
	}

	decoded := decodedByName(t, "fdiv.s")
	input := FloatInput{ActiveMask: 0b0111}
	input.RS1 = LaneValues{0x3f800000, 0, 0x3f800000, 0x7f7fffff}
	input.RS2 = LaneValues{0, 0, 0x40400000, 0x00000001}
	effects, err := EvaluateFloat(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	wantFlags := FFlagInvalid | FFlagDivideByZero | FFlagInexact
	if effects.FFlags == nil || effects.FFlags.Accumulate != wantFlags {
		t.Fatalf("merged flags=%+v, want %#x", effects.FFlags, wantFlags)
	}
	if write := effects.RegisterWrites[0]; write.Mask != 0b0111 || write.Values[3] != 0 {
		t.Fatalf("inactive lane leaked: %+v", write)
	}
}

func TestRV32FFMAIsFused(t *testing.T) {
	const a, b, c = uint32(0x3f800001), uint32(0x3f7ffffe), uint32(0xbf800000)
	effects := evaluateFloatOne(t, decodedByName(t, "fmadd.s"), a, b, c, RNE)
	if got := floatWriteValue(t, effects); got != 0xa8800000 {
		t.Fatalf("fused result=%#x", got)
	}
	product, err := softfloat.F32Mul(a, b, softfloat.RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	nonfused, err := softfloat.F32Add(product.Bits, c, softfloat.RoundNearEven)
	if err != nil {
		t.Fatal(err)
	}
	if nonfused.Bits == effects.RegisterWrites[0].Values[0] {
		t.Fatalf("test vector did not distinguish fused and non-fused: %#x", nonfused.Bits)
	}
}

func TestRV32FMinMaxCompareNaNAndZeros(t *testing.T) {
	tests := []struct {
		name  string
		a, b  uint32
		want  uint32
		flags FFlags
	}{
		{"fmin.s", 0x00000000, 0x80000000, 0x80000000, 0},
		{"fmax.s", 0x00000000, 0x80000000, 0x00000000, 0},
		{"fmin.s", 0x7fc12345, 0x3f800000, 0x3f800000, 0},
		{"fmax.s", 0x7f812345, 0x40000000, 0x40000000, FFlagInvalid},
		{"fmin.s", 0x7fc12345, 0x7f812345, f32CanonicalQ, FFlagInvalid},
		{"feq.s", 0x7fc00000, 0x3f800000, 0, 0},
		{"feq.s", 0x7f800001, 0x3f800000, 0, FFlagInvalid},
		{"flt.s", 0x7fc00000, 0x3f800000, 0, FFlagInvalid},
		{"fle.s", 0x80000000, 0x00000000, 1, 0},
	}
	for _, test := range tests {
		effects := evaluateFloatOne(t, decodedByName(t, test.name), test.a, test.b, 0, RNE)
		if got := floatWriteValue(t, effects); got != test.want || effects.FFlags == nil || effects.FFlags.Accumulate != test.flags {
			t.Errorf("%s(%#x,%#x): bits=%#x flags=%+v, want %#x/%#x", test.name, test.a, test.b, got, effects.FFlags, test.want, test.flags)
		}
	}
}

func TestRV32FFClassAllTenCategories(t *testing.T) {
	tests := []struct {
		bits uint32
		bit  uint32
	}{
		{0xff800000, 0}, {0xbf800000, 1}, {0x80000001, 2}, {0x80000000, 3},
		{0x00000000, 4}, {0x00000001, 5}, {0x3f800000, 6}, {0x7f800000, 7},
		{0x7f800001, 8}, {0x7fc00000, 9},
	}
	decoded := decodedByName(t, "fclass.s")
	for _, test := range tests {
		effects := evaluateFloatOne(t, decoded, test.bits, 0, 0, RoundingNone)
		if got := floatWriteValue(t, effects); got != 1<<test.bit || effects.FFlags != nil {
			t.Errorf("class(%#x)=%#x flags=%+v, want bit %d", test.bits, got, effects.FFlags, test.bit)
		}
	}
}

func TestRV32FConversionSaturationAndInexact(t *testing.T) {
	tests := []struct {
		name  string
		bits  uint32
		want  uint32
		flags FFlags
	}{
		{"fcvt.w.s", 0x3fc00000, 2, FFlagInexact},
		{"fcvt.w.s", 0x4f000000, 0x7fffffff, FFlagInvalid},
		{"fcvt.w.s", 0xff800000, 0x80000000, FFlagInvalid},
		{"fcvt.w.s", 0x7fc00000, 0x7fffffff, FFlagInvalid},
		{"fcvt.wu.s", 0xbf800000, 0, FFlagInvalid},
		{"fcvt.wu.s", 0x4f800000, 0xffffffff, FFlagInvalid},
		{"fcvt.s.w", 0x7fffffff, 0x4f000000, FFlagInexact},
		{"fcvt.s.wu", 0xffffffff, 0x4f800000, FFlagInexact},
	}
	for _, test := range tests {
		effects := evaluateFloatOne(t, decodedByName(t, test.name), test.bits, 0, 0, RNE)
		if got := floatWriteValue(t, effects); got != test.want || effects.FFlags == nil || effects.FFlags.Accumulate != test.flags {
			t.Errorf("%s(%#x): bits=%#x flags=%+v, want %#x/%#x", test.name, test.bits, got, effects.FFlags, test.want, test.flags)
		}
	}
}

func TestRV32FRegisterNamespacesAndInactiveLanes(t *testing.T) {
	decoded := decodedByName(t, "fsgnj.s")
	decoded.Destinations[0].Index = 0 // f0 is writable, unlike x0.
	input := FloatInput{PC: 0xfffffffc, ActiveMask: 0b0101}
	input.RS1 = LaneValues{0x3f800000, 0xdeadbeef, 0x40000000, 0xdeadbeef}
	input.RS2 = LaneValues{0x80000000, 0, 0, 0}
	effects, err := EvaluateFloat(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	write := effects.RegisterWrites[0]
	if write.Destination != (Register{File: Float, Index: 0}) || write.Mask != 0b0101 || write.Values != (LaneValues{0xbf800000, 0, 0x40000000, 0}) {
		t.Fatalf("FPR write=%+v", write)
	}
	if effects.Control == nil || effects.Control.NextPC != 0 {
		t.Fatalf("PC wrap effect=%+v", effects.Control)
	}

	compare := decodedByName(t, "feq.s")
	compare.Destinations[0].Index = 0
	effects = evaluateFloatOne(t, compare, 0, 0, 0, RNE)
	if len(effects.RegisterWrites) != 0 {
		t.Fatalf("x0 comparison write was not suppressed: %+v", effects.RegisterWrites)
	}
}

func TestRV32FFloatingMemoryEffectsAndFaults(t *testing.T) {
	memory, err := memsupport.New(16)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Store32(4, 0x7fc12345); err != nil {
		t.Fatal(err)
	}

	flw := decodedByName(t, "flw")
	flw.Immediate = 0
	input := FloatInput{PC: 0x40, ActiveMask: 1}
	input.RS1[0] = 4
	before := memory.Snapshot()
	issued, err := EvaluateFloat(flw, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(issued.MemoryRequests) != 1 || issued.Control != nil || issued.FFlags != nil || string(memory.Snapshot()) != string(before) {
		t.Fatalf("FLW issue mutated owner or leaked effects: %+v", issued)
	}
	completed, err := CompleteFloatMemory(flw, input.PC, input.ActiveMask, []MemoryResponse{serveMemoryRequest(memory, issued.MemoryRequests[0])})
	if err != nil {
		t.Fatal(err)
	}
	if got := completed.RegisterWrites[0]; got.Destination.File != Float || got.Values[0] != 0x7fc12345 || completed.Control == nil || completed.Control.NextPC != 0x44 {
		t.Fatalf("FLW completion=%+v", completed)
	}

	fsw := decodedByName(t, "fsw")
	fsw.Immediate = 0
	input.RS1[0], input.RS2[0] = 8, 0x80000001
	before = memory.Snapshot()
	issued, err = EvaluateFloat(fsw, input)
	if err != nil {
		t.Fatal(err)
	}
	request := issued.MemoryRequests[0]
	if request.StoreData != 0x80000001 || request.ByteMask != 0xf || string(memory.Snapshot()) != string(before) {
		t.Fatalf("FSW request=%+v", request)
	}
	response := serveMemoryRequest(memory, request)
	completed, err = CompleteFloatMemory(fsw, input.PC, input.ActiveMask, []MemoryResponse{response})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := memory.Load32(8)
	if err != nil || stored != 0x80000001 || len(completed.RegisterWrites) != 0 || completed.Control == nil {
		t.Fatalf("FSW owner result=%#x err=%v effects=%+v", stored, err, completed)
	}

	input.Bounds = &AddressBounds{Base: 0, Size: 8}
	input.RS1[0] = 2
	issued, err = EvaluateFloat(flw, input)
	if err != nil || len(issued.Faults) != 1 || issued.Faults[0].Kind != FaultLoadAddressMisaligned || len(issued.MemoryRequests) != 0 {
		t.Fatalf("FLW alignment=%+v err=%v", issued, err)
	}
	input.RS1[0] = 8
	issued, err = EvaluateFloat(fsw, input)
	if err != nil || len(issued.Faults) != 1 || issued.Faults[0].Kind != FaultStoreAccess || issued.Faults[0].Reason != FaultReasonBounds {
		t.Fatalf("FSW bounds=%+v err=%v", issued, err)
	}

	input.Bounds = nil
	input.ActiveMask = 0b0101
	input.RS1 = LaneValues{0, 2, 4, 2} // inactive misaligned lanes are inert.
	issued, err = EvaluateFloat(flw, input)
	if err != nil || len(issued.Faults) != 0 || len(issued.MemoryRequests) != 2 {
		t.Fatalf("inactive lane issue=%+v err=%v", issued, err)
	}
	responses := []MemoryResponse{
		{Request: issued.MemoryRequests[0], Data: 0x3f800000},
		{Request: issued.MemoryRequests[1], Fault: FaultLoadAccess, Reason: FaultReasonMemoryService},
	}
	completed, err = CompleteFloatMemory(flw, input.PC, input.ActiveMask, responses)
	if err != nil || len(completed.Faults) != 1 || len(completed.RegisterWrites) != 0 || completed.Control != nil {
		t.Fatalf("FLW fault atomicity=%+v err=%v", completed, err)
	}
}

func TestRV32FEvaluationInputValidation(t *testing.T) {
	decoded := decodedByName(t, "fadd.s")
	for _, input := range []FloatInput{
		{ActiveMask: 0x80},
		{ActiveMask: 1, Bounds: &AddressBounds{Base: 0xffffffff, Size: 2}},
	} {
		_, err := EvaluateFloat(decoded, input)
		var evaluation *EvaluationError
		if !errors.As(err, &evaluation) {
			t.Errorf("input=%+v error=%T %v", input, err, err)
		}
	}
	integer := decodedByName(t, "add")
	if _, err := EvaluateFloat(integer, FloatInput{ActiveMask: 1}); err == nil {
		t.Fatal("integer instruction accepted by floating evaluator")
	}
	if _, err := CompleteFloatMemory(decoded, 0, 0, nil); err == nil {
		t.Fatal("non-memory FP instruction accepted by completion")
	}
}
