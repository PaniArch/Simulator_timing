package isa

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCatalogEntriesAreCompleteAndReachable(t *testing.T) {
	if len(catalog) < 100 {
		t.Fatalf("catalog unexpectedly small: %d entries", len(catalog))
	}
	names := make(map[string]bool, len(catalog))
	categories := make(map[Category]int)
	for i, entry := range catalog {
		t.Run(fmt.Sprintf("%03d_%s", i, entry.Name), func(t *testing.T) {
			if entry.Name == "" || entry.Category == "" {
				t.Fatal("missing instruction name or category")
			}
			if names[entry.Name] {
				t.Fatalf("duplicate catalog instruction name %q", entry.Name)
			}
			names[entry.Name] = true
			categories[entry.Category]++
			if entry.Match&^entry.Mask != 0 {
				t.Fatalf("match has bits outside mask: match=%08x mask=%08x", entry.Match, entry.Mask)
			}
			if len(entry.RTLEvidence) == 0 {
				t.Fatal("missing RTL evidence")
			}
			for _, evidence := range entry.RTLEvidence {
				if !strings.HasPrefix(evidence, "hw/rtl/") || !strings.Contains(evidence, ".sv") {
					t.Fatalf("evidence is not a concrete RTL path: %q", evidence)
				}
			}
			if !entry.accepts(entry.Example) {
				t.Fatalf("catalog example 0x%08x does not satisfy its own legality rules", entry.Example)
			}
			decoded, err := Decode(entry.Example)
			if err != nil {
				t.Fatalf("example 0x%08x is unreachable: %v", entry.Example, err)
			}
			if decoded.Name != entry.Name {
				t.Fatalf("example decoded as %q, want %q", decoded.Name, entry.Name)
			}
			if decoded.Effects != entry.Effects || decoded.Result != entry.Result {
				t.Fatalf("decoded result/effects differ from catalog")
			}
		})
	}
	for _, category := range []Category{CategoryRV32I, CategoryRV32M, CategoryRV32F, CategoryZicond, CategorySystem, CategoryFence, CategoryCustom} {
		if categories[category] == 0 {
			t.Errorf("frozen category %s has no entries", category)
		}
	}
}

func TestCatalogEntriesDoNotOverlap(t *testing.T) {
	for i := range catalog {
		for j := i + 1; j < len(catalog); j++ {
			if entriesOverlap(catalog[i], catalog[j]) {
				t.Errorf("catalog entries %q and %q accept a common word", catalog[i].Name, catalog[j].Name)
			}
		}
	}
}

// entriesOverlap is a small SAT check. Match/mask supplies fixed bits and the
// only non-mask predicates occupy at most rm(3)+rd(5) bits, so exhaustive
// enumeration of those predicate bits proves pairwise disjointness.
func entriesOverlap(a, b Entry) bool {
	fixedBoth := a.Mask & b.Mask
	if ((a.Match ^ b.Match) & fixedBoth) != 0 {
		return false
	}
	fixed := a.Mask | b.Mask
	variable := uint32(0)
	for _, c := range a.Constraints {
		variable |= c.Mask &^ fixed
	}
	for _, c := range b.Constraints {
		variable |= c.Mask &^ fixed
	}
	positions := make([]uint8, 0, 8)
	for bit := uint8(0); bit < 32; bit++ {
		if variable&(uint32(1)<<bit) != 0 {
			positions = append(positions, bit)
		}
	}
	if len(positions) > 16 {
		panic("catalog constraint SAT domain unexpectedly large")
	}
	baseWord := a.Match | b.Match
	for assignment := 0; assignment < 1<<len(positions); assignment++ {
		word := baseWord
		for i, position := range positions {
			if assignment&(1<<i) != 0 {
				word |= uint32(1) << position
			}
		}
		if a.accepts(word) && b.accepts(word) {
			return true
		}
	}
	return false
}

func TestStrictDecoderRejectsReservedAndDisabledEncodings(t *testing.T) {
	tests := []struct {
		name string
		word uint32
	}{
		{"all-zero", 0},
		{"compressed", 0x00000001},
		{"rv64-addiw", 0x0000001b},
		{"rv64-addw", 0x0000003b},
		{"atomic", 0x0000202f},
		{"vector", 0x00000057},
		{"jalr-funct3", bitsF3(1, opJALR)},
		{"reserved-branch-funct3", bitsF3(2, opBranch)},
		{"rv64-ld", bitsF3(3, opLoad)},
		{"rv64-lwu", bitsF3(6, opLoad)},
		{"rv64-sd", bitsF3(3, opStore)},
		{"slli-high-shamt", bitsF7(1, 1, opImm)},
		{"register-reserved-funct7", bitsF7(2, 0, opReg)},
		{"zicond-reserved-funct3", bitsF7(7, 4, opReg)},
		{"fence-i-not-frozen", 0x0000100f},
		{"fence-reserved-rd", 0x0000008f},
		{"fence-reserved-rs1", 0x0000800f},
		{"fence-reserved-fm", 0x8000000f},
		{"system-reserved-funct3", bitsF3(4, opSystem)},
		{"ecall-nonzero-rd", 0x000000f3},
		{"unknown-system", 0x10500073},
		{"fld", bitsF3(3, opFLoad)},
		{"fsd", bitsF3(3, opFStore)},
		{"fadd-d-format", bitsF7(1, 0, opFloat)},
		{"fmadd-d-format", 1<<25 | opFMAdd},
		{"fmadd-reserved-format", 3<<25 | opFMAdd},
		{"fadd-reserved-rm-5", bitsF7(0, 5, opFloat)},
		{"fadd-reserved-rm-6", bitsF7(0, 6, opFloat)},
		{"fsqrt-nonzero-rs2", bitsRS2(1, 0x2c, 0, opFloat)},
		{"fcvt-l-s-rv64", bitsRS2(2, 0x60, 0, opFloat)},
		{"fsgnj-reserved-funct3", bitsF7(0x10, 3, opFloat)},
		{"fmin-reserved-funct3", bitsF7(0x14, 2, opFloat)},
		{"fcmp-reserved-funct3", bitsF7(0x50, 3, opFloat)},
		{"fmv-nonzero-rs2", bitsRS2(1, 0x70, 0, opFloat)},
		{"tcu-custom-disabled", bitsF7(2, 0, opCustom)},
		{"dxa-custom-disabled", bitsF7(3, 0, opCustom)},
		{"tmc-reserved-rd", bitsF7(0, 0, opCustom) | 1<<7},
		{"tmc-reserved-rs2", bitsF7(0, 0, opCustom) | 1<<20},
		{"split-reserved-rs2", bitsF7(0, 2, opCustom) | 2<<20},
		{"join-reserved-rd", bitsF7(0, 3, opCustom) | 1<<7},
		{"pred-reserved-rd", bitsF7(0, 5, opCustom) | 2<<7},
		{"wsync-reserved-rs1", bitsF7(0, 7, opCustom) | 1<<15},
		{"vote-reserved-rs2", bitsF7(1, 0, opCustom) | 1<<20},
		{"pack-reserved-funct3", bitsF7(4, 3, opCustom)},
		{"tex-custom-disabled", bitsF3(5, opGather)},
		{"om-custom-disabled", bitsF3(2, opGather)},
		{"gfx-window-disabled", bitsF3(4, opGather)},
		{"custom-3-disabled", 0x5b},
		{"custom-4-disabled", 0x7b},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := Decode(test.word)
			if err == nil {
				t.Fatalf("0x%08x unexpectedly decoded as %s", test.word, decoded.Name)
			}
			var illegal *IllegalInstructionError
			if !errors.As(err, &illegal) {
				t.Fatalf("got non-deterministic error type %T: %v", err, err)
			}
			if illegal.Word != test.word {
				t.Fatalf("error records word 0x%08x, want 0x%08x", illegal.Word, test.word)
			}
		})
	}
}

func TestFloatingRoundingModes(t *testing.T) {
	baseWord := bitsF7(0, 0, opFloat) | 1<<7 | 2<<15 | 3<<20
	modes := map[uint32]RoundingMode{0: RNE, 1: RTZ, 2: RDN, 3: RUP, 4: RMM, 7: Dynamic}
	for encoded, want := range modes {
		decoded, err := Decode(baseWord | encoded<<12)
		if err != nil {
			t.Fatalf("rm=%d rejected: %v", encoded, err)
		}
		if decoded.Name != "fadd.s" || decoded.Rounding != want || decoded.Format != FormatSingle {
			t.Fatalf("rm=%d decoded as %+v", encoded, decoded)
		}
	}
}

func TestDecodeFieldsAndEffectBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		word      uint32
		wantName  string
		immediate int32
		check     func(*testing.T, Decoded)
	}{
		{"addi-negative", 0xfff08193, "addi", -1, func(t *testing.T, d Decoded) {
			assertRegs(t, d.Sources, []Register{{Integer, 1}})
			assertRegs(t, d.Destinations, []Register{{Integer, 3}})
		}},
		{"store-negative", (0x7f << 25) | (2 << 20) | (1 << 15) | (2 << 12) | (0x18 << 7) | opStore, "sw", -8, func(t *testing.T, d Decoded) {
			if d.Memory.Kind != MemoryStore || d.Memory.Bytes != 4 || d.Result != ResultNone {
				t.Fatalf("bad store boundary: %+v", d)
			}
		}},
		{"branch-negative", (1 << 31) | (0x3f << 25) | (2 << 20) | (1 << 15) | (0xf << 8) | (1 << 7) | opBranch, "beq", -2, func(t *testing.T, d Decoded) {
			if d.Control != ControlBranch || d.Effects&EffectControl == 0 || !d.RequiresPC {
				t.Fatalf("bad branch boundary: %+v", d)
			}
		}},
		{"lui", 0xabcde1b7, "lui", -1412571136, nil},
		{"jal", 0x004001ef, "jal", 4, nil},
		{"csr-immediate", (0x003 << 20) | (7 << 15) | (5 << 12) | (4 << 7) | opSystem, "csrrwi", 3, func(t *testing.T, d Decoded) {
			if !d.CSRImmediate || d.CSRImmediateValue != 7 || d.CSR != CSRRW || d.CSRAddress != 3 || len(d.Sources) != 0 {
				t.Fatalf("bad CSR boundary: %+v", d)
			}
		}},
		{"barrier", bitsF7(0, 4, opCustom) | 1<<15 | 2<<20, "bar", 0, func(t *testing.T, d Decoded) {
			if d.Barrier != BarrierSync || d.Effects&EffectBarrier == 0 || d.WriteMask != WriteMaskNone {
				t.Fatalf("bad barrier boundary: %+v", d)
			}
		}},
		{"split-negated", bitsF7(0, 2, opCustom) | 3<<7 | 1<<15 | 1<<20, "split", 0, func(t *testing.T, d Decoded) {
			if !d.ConditionNegated || !d.RequiresPC || d.Control != ControlSplit {
				t.Fatalf("bad split modifier: %+v", d)
			}
		}},
		{"predicate-negated", bitsF7(0, 5, opCustom) | 1<<7 | 1<<15 | 2<<20, "pred", 0, func(t *testing.T, d Decoded) {
			if !d.ConditionNegated || d.Control != ControlPredicate {
				t.Fatalf("bad predicate modifier: %+v", d)
			}
		}},
		{"barrier-arrive", bitsF7(0, 6, opCustom) | 3<<7 | 1<<15 | 2<<20, "bar.arrive", 0, func(t *testing.T, d Decoded) {
			if d.Barrier != BarrierArrive || len(d.Sources) != 2 || len(d.Destinations) != 1 {
				t.Fatalf("bad barrier-arrive operands: %+v", d)
			}
		}},
		{"wgather", bitsF3(0, opGather) | 4<<7 | 1<<15 | 2<<20 | 2<<25 | 3<<27, "wgather", 0, func(t *testing.T, d Decoded) {
			if d.WriteMask != WriteMaskGatherNonSource || !d.RequiresActiveMask || d.GatherSourceLane != 2 {
				t.Fatalf("bad gather mask boundary: %+v", d)
			}
			assertRegs(t, d.Sources, []Register{{Integer, 1}, {Integer, 2}, {Integer, 3}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := Decode(test.word)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Name != test.wantName {
				t.Fatalf("decoded %s, want %s", decoded.Name, test.wantName)
			}
			if decoded.HasImmediate && decoded.Immediate != test.immediate {
				t.Fatalf("immediate=%d, want %d", decoded.Immediate, test.immediate)
			}
			if test.check != nil {
				test.check(t, decoded)
			}
		})
	}
}

func assertRegs(t *testing.T, got, want []Register) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("registers=%v, want %v", got, want)
	}
}

func TestCatalogReturnsIndependentSlice(t *testing.T) {
	copyOfCatalog := Catalog()
	copyOfCatalog[0].Name = "corrupted"
	copyOfCatalog[len(copyOfCatalog)-1].RTLEvidence[0] = "corrupted"
	constraintIndex := -1
	for i := range copyOfCatalog {
		if len(copyOfCatalog[i].Constraints) != 0 && len(copyOfCatalog[i].Constraints[0].Values) != 0 {
			constraintIndex = i
			break
		}
	}
	if constraintIndex == -1 {
		t.Fatal("catalog has no value constraint")
	}
	copyOfCatalog[constraintIndex].Constraints[0].Values[0] = 99
	if catalog[0].Name == "corrupted" {
		t.Fatal("Catalog exposed the manifest slice for mutation")
	}
	if catalog[len(catalog)-1].RTLEvidence[0] == "corrupted" || catalog[constraintIndex].Constraints[0].Values[0] == 99 {
		t.Fatal("Catalog exposed nested manifest storage for mutation")
	}
}
