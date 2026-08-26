package isa

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func decodedCSR(t *testing.T, name string, address uint16, rd, rs1 uint8) Decoded {
	t.Helper()
	for _, entry := range catalog {
		if entry.Name == name {
			word := entry.Match | uint32(address)<<20 | uint32(rs1)<<15 | uint32(rd)<<7
			decoded, err := Decode(word)
			if err != nil {
				t.Fatalf("decode %s CSR %#x: %v", name, address, err)
			}
			return decoded
		}
	}
	t.Fatalf("instruction %q not found", name)
	return Decoded{}
}

func basicSystemInput() SystemInput {
	return SystemInput{
		PC: 0x100, ActiveMask: AllLanes,
		CSR: CSRView{WarpID: 2, ActiveWarps: 0b1111, ThreadMask: AllLanes},
	}
}

func instructionEffectsEmpty(effects InstructionEffects) bool {
	return len(effects.RegisterWrites) == 0 && effects.Control == nil &&
		len(effects.MemoryRequests) == 0 && effects.Ordering == nil && effects.FFlags == nil &&
		len(effects.CSRReads) == 0 && len(effects.CSRWrites) == 0 && effects.Trap == nil &&
		len(effects.WarpMasks) == 0 && effects.WarpSpawn == nil && effects.Divergence == nil &&
		len(effects.WarpDrains) == 0 && len(effects.Barriers) == 0 && len(effects.PackedLoads) == 0 &&
		len(effects.Faults) == 0
}

func requireCSRResult(t *testing.T, effects InstructionEffects, oldValue uint32) {
	t.Helper()
	if len(effects.RegisterWrites) != 1 {
		t.Fatalf("register writes=%+v", effects.RegisterWrites)
	}
	write := effects.RegisterWrites[0]
	if write.Mask != AllLanes || write.Values != filledValues(oldValue) {
		t.Fatalf("old-value write=%+v, want %#x", write, oldValue)
	}
	if len(effects.CSRReads) != 1 || effects.CSRReads[0].Values != filledValues(oldValue) {
		t.Fatalf("CSR read=%+v", effects.CSRReads)
	}
	if effects.Control == nil || effects.Control.Reason != PCSequential || effects.Control.NextPC != 0x104 {
		t.Fatalf("control=%+v", effects.Control)
	}
}

func TestCSRCatalogIsUniqueAuditableAndReachable(t *testing.T) {
	if len(csrCatalog) < 100 {
		t.Fatalf("CSR catalog unexpectedly small: %d", len(csrCatalog))
	}
	seen := make(map[uint16]string, len(csrCatalog))
	for _, entry := range csrCatalog {
		if prior, ok := seen[entry.Address]; ok {
			t.Errorf("CSR address %#x duplicated by %s and %s", entry.Address, prior, entry.Name)
		}
		seen[entry.Address] = entry.Name
		if entry.Name == "" || len(entry.RTLEvidence) == 0 {
			t.Errorf("incomplete CSR entry: %+v", entry)
		}
		for _, evidence := range entry.RTLEvidence {
			if !strings.HasPrefix(evidence, "hw/") || (!strings.Contains(evidence, ".sv") && !strings.Contains(evidence, ".vh")) {
				t.Errorf("bad RTL evidence %q", evidence)
			}
		}
		if entry.Writable && (entry.WriteIgnored || entry.WriteMask == 0) {
			t.Errorf("inconsistent writable entry: %+v", entry)
		}
		if entry.WriteIgnored && entry.WriteMask != 0 {
			t.Errorf("ignored entry has storage mask: %+v", entry)
		}

		decoded := decodedCSR(t, "csrrs", entry.Address, 3, 0)
		effects, err := EvaluateSystem(decoded, basicSystemInput())
		if err != nil {
			t.Errorf("catalog CSR %s is unreachable: %v", entry.Name, err)
		} else if len(effects.CSRReads) != 1 || len(effects.CSRWrites) != 0 {
			t.Errorf("zero-source read effects for %s: %+v", entry.Name, effects)
		}
	}
	for _, address := range []uint16{0xb01, 0xb81, 0xc00, 0xfff} {
		if _, ok := seen[address]; ok {
			t.Errorf("reserved/unknown CSR %#x was enabled", address)
		}
	}

	copyCatalog := CSRCatalog()
	copyCatalog[0].Name = "changed"
	copyCatalog[0].RTLEvidence[0] = "changed"
	if csrCatalog[0].Name == "changed" || csrCatalog[0].RTLEvidence[0] == "changed" {
		t.Fatal("CSRCatalog did not return a defensive copy")
	}
}

func TestSixCSRRMWInstructions(t *testing.T) {
	tests := []struct {
		name      string
		address   uint16
		rd, rs1   uint8
		register  uint32
		fcsr      uint32
		mstatus   uint32
		wantOld   uint32
		wantValue uint32
		wantMask  uint32
	}{
		{"csrrw", 0x003, 3, 5, 0x123, 0xab, 0, 0xab, 0x23, 0xff},
		{"csrrs", 0x300, 3, 5, 0xf0, 0, 0x0f, 0x0f, 0xff, 0xffffffff},
		{"csrrc", 0x300, 3, 5, 0x0f, 0, 0xff, 0xff, 0xf0, 0xffffffff},
		{"csrrwi", 0x001, 3, 0x1b, 0, 0x9f, 0, 0x1f, 0x1b, 0x1f},
		{"csrrsi", 0x002, 3, 2, 0, 5 << 5, 0, 5, 7, 0x07},
		{"csrrci", 0x003, 3, 0x0f, 0, 0xff, 0, 0xff, 0xf0, 0xff},
	}
	covered := make(map[string]bool)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedCSR(t, test.name, test.address, test.rd, test.rs1)
			input := basicSystemInput()
			input.RS1[0] = test.register
			input.CSR.FCSR, input.CSR.MStatus = test.fcsr, test.mstatus
			effects, err := EvaluateSystem(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			requireCSRResult(t, effects, test.wantOld)
			if len(effects.CSRWrites) != 1 {
				t.Fatalf("CSR writes=%+v", effects.CSRWrites)
			}
			write := effects.CSRWrites[0]
			if write.Value != test.wantValue || write.WriteMask != test.wantMask || write.OldValue != test.wantOld || write.Ignored || write.WarpID != 2 {
				t.Fatalf("CSR write=%+v", write)
			}
			covered[test.name] = true
		})
	}
	assertCatalogFunctionallyCovered(t, covered, CategorySystem)
}

func TestCSRZeroSourceAndLaneZeroWriteOperand(t *testing.T) {
	for _, test := range []struct {
		name string
		rs1  uint8
	}{
		{"csrrs", 7},
		{"csrrc", 7},
		{"csrrsi", 0},
		{"csrrci", 0},
	} {
		decoded := decodedCSR(t, test.name, 0x300, 3, test.rs1)
		input := basicSystemInput()
		input.CSR.MStatus = 0x12345678
		effects, err := EvaluateSystem(decoded, input)
		if err != nil {
			t.Fatal(err)
		}
		if len(effects.CSRWrites) != 0 {
			t.Errorf("%s zero source wrote CSR: %+v", test.name, effects.CSRWrites)
		}
	}

	decoded := decodedCSR(t, "csrrs", 0x300, 3, 7)
	input := basicSystemInput()
	input.ActiveMask = 0b0100
	input.CSR.MStatus = 0x10
	input.RS1 = LaneValues{1, 0, 0x80000000, 0}
	effects, err := EvaluateSystem(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.CSRWrites[0].Value != 0x11 {
		t.Fatalf("CSR source did not use RTL lane0: %+v", effects.CSRWrites[0])
	}
	if write := effects.RegisterWrites[0]; write.Mask != 0b0100 || write.Values[2] != 0x10 || write.Values[0] != 0x10 {
		t.Fatalf("old-value lane mask=%+v", write)
	}

	decoded = decodedCSR(t, "csrrw", 0x300, 0, 0)
	input = basicSystemInput()
	input.CSR.MStatus = 0xffffffff
	effects, err = EvaluateSystem(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.RegisterWrites) != 0 || len(effects.CSRWrites) != 1 || effects.CSRWrites[0].Value != 0 {
		t.Fatalf("CSRRW x0 effects=%+v", effects)
	}
}

func TestCSRReadOnlyUnknownAndIgnoredWritesAreAtomic(t *testing.T) {
	readonlyTests := []struct {
		name    string
		address uint16
		rs1     uint8
		value   uint32
	}{
		{"csrrw", 0x301, 5, 0},
		{"csrrs", 0x301, 5, 1},
		{"csrrsi", 0xcc0, 1, 0},
	}
	for _, test := range readonlyTests {
		decoded := decodedCSR(t, test.name, test.address, 3, test.rs1)
		input := basicSystemInput()
		input.RS1[0] = test.value
		effects, err := EvaluateSystem(decoded, input)
		var access *CSRAccessError
		if !errors.As(err, &access) || access.Problem != CSRReadOnlyWrite {
			t.Errorf("read-only error=%T %v", err, err)
		}
		if !instructionEffectsEmpty(effects) {
			t.Errorf("read-only failure leaked effects: %+v", effects)
		}
	}

	unknown := decodedCSR(t, "csrrs", 0x777, 3, 0)
	effects, err := EvaluateSystem(unknown, basicSystemInput())
	var access *CSRAccessError
	if !errors.As(err, &access) || access.Problem != CSRUnknownAddress || access.Address != 0x777 {
		t.Fatalf("unknown error=%T %v", err, err)
	}
	if !instructionEffectsEmpty(effects) {
		t.Fatalf("unknown failure leaked effects: %+v", effects)
	}

	ignored := decodedCSR(t, "csrrw", 0x180, 3, 5)
	input := basicSystemInput()
	input.RS1[0] = 0xdeadbeef
	effects, err = EvaluateSystem(ignored, input)
	if err != nil {
		t.Fatal(err)
	}
	requireCSRResult(t, effects, 0)
	if len(effects.CSRWrites) != 1 || !effects.CSRWrites[0].Ignored || effects.CSRWrites[0].WriteMask != 0 || effects.CSRWrites[0].Value != 0 {
		t.Fatalf("VM-disabled SATP write=%+v", effects.CSRWrites)
	}

	readonlyRead := decodedCSR(t, "csrrs", 0x301, 3, 0)
	effects, err = EvaluateSystem(readonlyRead, basicSystemInput())
	if err != nil {
		t.Fatal(err)
	}
	requireCSRResult(t, effects, FrozenMISA)
}

func readCSRForTest(t *testing.T, address uint16, view CSRView) InstructionEffects {
	t.Helper()
	decoded := decodedCSR(t, "csrrs", address, 3, 0)
	input := basicSystemInput()
	input.CSR = view
	input.ActiveMask = AllLanes
	effects, err := EvaluateSystem(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func TestCSRIdentityAndCTAContextViews(t *testing.T) {
	view := basicSystemInput().CSR
	view.WarpID = 2
	view.ActiveWarps = 0b1101
	view.ThreadMask = 0b1011
	view.CTA = CTAView{
		ID: 17, Rank: 2, Size: 64,
		ThreadCoordinates: [3]LaneValues{{10, 11, 12, 13}, {20, 21, 22, 23}, {30, 31, 32, 33}},
		BlockID:           [3]uint32{4, 5, 6}, BlockDimensions: [3]uint32{8, 9, 10},
		GridDimensions: [3]uint32{11, 12, 13}, LocalMemoryAddress: 0xffff2000,
		ClusterSize: 3, Entry: 0x80000100,
	}
	tests := []struct {
		address uint16
		want    LaneValues
	}{
		{0xcc0, LaneValues{0, 1, 2, 3}},
		{0xf14, LaneValues{8, 9, 10, 11}},
		{0xcc1, filledValues(2)},
		{0xcc2, filledValues(0)},
		{0xcc3, filledValues(0b1101)},
		{0xcc4, filledValues(0b1011)},
		{0xcd3, LaneValues{10, 11, 12, 13}},
		{0xcd4, LaneValues{20, 21, 22, 23}},
		{0xcd5, LaneValues{30, 31, 32, 33}},
		{0xcd0, filledValues(17)},
		{0xcd8, filledValues(6)},
		{0xcda, filledValues(9)},
		{0xcde, filledValues(13)},
		{0xcdf, filledValues(0xffff2000)},
		{0xce0, filledValues(3)},
		{0xce1, filledValues(0x80000100)},
	}
	for _, test := range tests {
		effects := readCSRForTest(t, test.address, view)
		if got := effects.RegisterWrites[0].Values; got != test.want || effects.CSRReads[0].Values != test.want {
			t.Errorf("CSR %#x values=%v read=%v, want %v", test.address, got, effects.CSRReads[0].Values, test.want)
		}
	}
}

func TestCSRConstantsZerosAndCounterView(t *testing.T) {
	view := basicSystemInput().CSR
	view.Counters = CounterView{Cycle: 0x1122334455667788, Instret: 0x99aabbccddeeff00}
	tests := []struct {
		address uint16
		want    uint32
	}{
		{0x301, FrozenMISA},
		{0xf11, 0}, {0xf12, 0}, {0xf13, 0},
		{0xfc0, 4}, {0xfc1, 4}, {0xfc2, 1}, {0xfc3, 0xffff0000}, {0xfc4, 8},
		{0x180, 0}, {0x302, 0}, {0x303, 0}, {0x304, 0}, {0x3a0, 0}, {0x3b0, 0}, {0x744, 0},
		{0xb00, 0x55667788}, {0xb80, 0x00000344},
		{0xb02, 0xddeeff00}, {0xb82, 0x00000bcc},
		{0xb03, 0}, {0xb22, 0}, {0xb83, 0}, {0xba2, 0},
	}
	for _, test := range tests {
		effects := readCSRForTest(t, test.address, view)
		if got := effects.RegisterWrites[0].Values[0]; got != test.want {
			t.Errorf("CSR %#x=%#x, want %#x", test.address, got, test.want)
		}
	}
}

func TestFCSRReadAndWriteMasks(t *testing.T) {
	view := basicSystemInput().CSR
	view.FCSR = 0x1ab
	for _, test := range []struct {
		address uint16
		want    uint32
	}{
		{0x001, 0x0b}, {0x002, 5}, {0x003, 0xab},
	} {
		effects := readCSRForTest(t, test.address, view)
		if got := effects.RegisterWrites[0].Values[0]; got != test.want {
			t.Errorf("FCSR view %#x=%#x, want %#x", test.address, got, test.want)
		}
	}

	for _, test := range []struct {
		address uint16
		source  uint32
		want    uint32
		mask    uint32
	}{
		{0x001, 0xffffffff, 0x1f, 0x1f},
		{0x002, 0xffffffff, 0x07, 0x07},
		{0x003, 0xffffffff, 0xff, 0xff},
	} {
		decoded := decodedCSR(t, "csrrw", test.address, 3, 5)
		input := basicSystemInput()
		input.CSR.FCSR, input.RS1[0] = view.FCSR, test.source
		effects, err := EvaluateSystem(decoded, input)
		if err != nil {
			t.Fatal(err)
		}
		write := effects.CSRWrites[0]
		if write.Value != test.want || write.WriteMask != test.mask {
			t.Errorf("FCSR write %#x=%+v", test.address, write)
		}
	}
}

func TestSystemTrapEntryEffects(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause uint32
	}{
		{"ecall", TrapCauseEnvironmentCallMMode},
		{"ebreak", TrapCauseBreakpoint},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			input := SystemInput{
				PC: 0x104, ActiveMask: 0b0101,
				CSR: CSRView{WarpID: 1, ThreadMask: 0b0101, MTVec: 0x803, MEPC: 1, MCause: 2, MTVal: 3},
			}
			effects, err := EvaluateSystem(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if effects.Control == nil || effects.Control.Reason != PCTrap || effects.Control.NextPC != 0x800 || !effects.Control.Taken {
				t.Fatalf("trap control=%+v", effects.Control)
			}
			if effects.Trap == nil || effects.Trap.Kind != TrapEnter || effects.Trap.Cause != test.cause || effects.Trap.EPC != 0x104 || effects.Trap.Vector != 0x800 || effects.Trap.SaveThreadMask != 0b0101 {
				t.Fatalf("trap effect=%+v", effects.Trap)
			}
			if len(effects.CSRWrites) != 3 {
				t.Fatalf("trap CSR writes=%+v", effects.CSRWrites)
			}
			want := []struct {
				address  uint16
				old, new uint32
			}{{0x341, 1, 0x104}, {0x342, 2, test.cause}, {0x343, 3, 0}}
			for i, expected := range want {
				got := effects.CSRWrites[i]
				if got.Address != expected.address || got.OldValue != expected.old || got.Value != expected.new || got.WarpID != 1 || got.WriteMask != 0xffffffff {
					t.Errorf("trap CSR write %d=%+v", i, got)
				}
			}
			if len(effects.RegisterWrites) != 0 || len(effects.CSRReads) != 0 {
				t.Fatalf("trap leaked ordinary results: %+v", effects)
			}
		})
	}
}

func TestAllTrapReturnsRedirectAndConditionallyRestoreMask(t *testing.T) {
	for _, name := range []string{"uret", "sret", "mret"} {
		t.Run(name, func(t *testing.T) {
			decoded := decodedByName(t, name)
			input := SystemInput{
				PC: 0x200, ActiveMask: 1,
				CSR: CSRView{WarpID: 3, ThreadMask: 1, SavedThreadMask: 0b1010, MEPC: 0x123},
			}
			effects, err := EvaluateSystem(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if effects.Control == nil || effects.Control.Reason != PCTrapReturn || effects.Control.NextPC != 0x120 || !effects.Control.Taken {
				t.Fatalf("return control=%+v", effects.Control)
			}
			if effects.Trap == nil || effects.Trap.Kind != TrapReturn || effects.Trap.EPC != 0x123 || !effects.Trap.RestoresThreadMask || effects.Trap.RestoreThreadMask != 0b1010 {
				t.Fatalf("return effect=%+v", effects.Trap)
			}
			if len(effects.CSRWrites) != 0 || len(effects.RegisterWrites) != 0 {
				t.Fatalf("return added unsupported effects: %+v", effects)
			}
		})
	}

	decoded := decodedByName(t, "mret")
	input := SystemInput{PC: 0x200, ActiveMask: 1, CSR: CSRView{ThreadMask: 1, MEPC: 0x400}}
	effects, err := EvaluateSystem(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Trap.RestoresThreadMask || effects.Trap.RestoreThreadMask != 0 {
		t.Fatalf("bare MRET cleared running mask: %+v", effects.Trap)
	}
}

func TestSystemInputAndIllegalPathsHaveNoPartialEffects(t *testing.T) {
	tests := []struct {
		name  string
		input SystemInput
	}{
		{"ecall", SystemInput{ActiveMask: 1, CSR: CSRView{ThreadMask: 0}}},
		{"mret", SystemInput{ActiveMask: 0, CSR: CSRView{ThreadMask: 1}}},
		{"csrrs", SystemInput{ActiveMask: 0x80}},
		{"csrrs", SystemInput{ActiveMask: 1, CSR: CSRView{WarpID: 4}}},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("%d_%s", i, test.name), func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			if decoded.CSR != CSRNone {
				decoded.CSRAddress = 0x300
			}
			effects, err := EvaluateSystem(decoded, test.input)
			if err == nil || !instructionEffectsEmpty(effects) {
				t.Fatalf("effects=%+v err=%v", effects, err)
			}
		})
	}

	integer := decodedByName(t, "add")
	effects, err := EvaluateSystem(integer, basicSystemInput())
	if err == nil || !instructionEffectsEmpty(effects) {
		t.Fatalf("integer accepted as System: %+v %v", effects, err)
	}
}
