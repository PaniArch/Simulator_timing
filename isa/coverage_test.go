package isa

import (
	"fmt"
	"testing"
)

type evaluatorID string

const (
	evaluatorInteger evaluatorID = "EvaluateInteger"
	evaluatorFloat   evaluatorID = "EvaluateFloat"
	evaluatorSystem  evaluatorID = "EvaluateSystem"
	evaluatorCustom  evaluatorID = "EvaluateCustom"
)

// registeredFunctionalVector is deliberately independent of catalog. Adding
// an enabled instruction without explicitly registering its evaluator/vector,
// or retaining a vector after removing an instruction, fails the final gate.
type registeredFunctionalVector struct {
	Name      string
	Evaluator evaluatorID
}

var registeredFunctionalVectors = func() []registeredFunctionalVector {
	vectors := make([]registeredFunctionalVector, 0, 105)
	add := func(evaluator evaluatorID, names ...string) {
		for _, name := range names {
			vectors = append(vectors, registeredFunctionalVector{Name: name, Evaluator: evaluator})
		}
	}
	add(evaluatorInteger,
		"lui", "auipc", "jal", "jalr", "beq", "bne", "blt", "bge", "bltu", "bgeu",
		"lb", "lh", "lw", "lbu", "lhu", "sb", "sh", "sw",
		"addi", "slli", "slti", "sltiu", "xori", "srli", "srai", "ori", "andi",
		"add", "sub", "sll", "slt", "sltu", "xor", "srl", "sra", "or", "and", "fence",
		"mul", "mulh", "mulhsu", "mulhu", "div", "divu", "rem", "remu", "czero.eqz", "czero.nez",
	)
	add(evaluatorSystem,
		"ecall", "ebreak", "uret", "sret", "mret",
		"csrrw", "csrrs", "csrrc", "csrrwi", "csrrsi", "csrrci",
	)
	add(evaluatorFloat,
		"flw", "fsw", "fmadd.s", "fmsub.s", "fnmsub.s", "fnmadd.s",
		"fadd.s", "fsub.s", "fmul.s", "fdiv.s", "fsqrt.s",
		"fsgnj.s", "fsgnjn.s", "fsgnjx.s", "fmin.s", "fmax.s",
		"fle.s", "flt.s", "feq.s", "fcvt.w.s", "fcvt.wu.s", "fcvt.s.w", "fcvt.s.wu",
		"fmv.x.w", "fclass.s", "fmv.w.x",
	)
	add(evaluatorCustom,
		"tmc", "wspawn", "split", "join", "bar", "pred", "bar.wait", "bar.arrive", "wsync",
		"vote.all", "vote.any", "vote.uni", "vote.ballot",
		"shfl.up", "shfl.down", "shfl.bfly", "shfl.idx", "wgather", "vx_packlb_f", "vx_packlh_f",
	)
	return vectors
}()

func TestCatalogDecodeEvaluatorVectorCoverageGate(t *testing.T) {
	entries := make(map[string]Entry, len(catalog))
	for _, entry := range catalog {
		if _, exists := entries[entry.Name]; exists {
			t.Fatalf("duplicate catalog name %q", entry.Name)
		}
		entries[entry.Name] = entry
	}

	registered := make(map[string]registeredFunctionalVector, len(registeredFunctionalVectors))
	for _, vector := range registeredFunctionalVectors {
		if prior, exists := registered[vector.Name]; exists {
			t.Errorf("duplicate functional vectors for %s: %s and %s", vector.Name, prior.Evaluator, vector.Evaluator)
			continue
		}
		registered[vector.Name] = vector
		entry, exists := entries[vector.Name]
		if !exists {
			t.Errorf("functional vector %q has no frozen catalog entry", vector.Name)
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			exampleDecoded, err := Decode(entry.Example)
			if err != nil {
				t.Fatalf("catalog example is not decode-reachable: %v", err)
			}
			if exampleDecoded.Name != vector.Name {
				t.Fatalf("catalog example decoded as %s", exampleDecoded.Name)
			}
			vectorWord := entry.Example
			if entry.CSR != CSRNone {
				// Catalog reachability is word-pattern coverage. The functional
				// vector independently chooses writable FCSR (0x003), since
				// ordinary rExample operand bits are not a CSR address manifest.
				vectorWord = (vectorWord &^ uint32(0xfff00000)) | uint32(0x003)<<20
			}
			decoded, err := Decode(vectorWord)
			if err != nil || decoded.Name != vector.Name {
				t.Fatalf("registered functional word %#08x is unreachable or decoded as %s: %v", vectorWord, decoded.Name, err)
			}
			effects, err := runRegisteredFunctionalVector(vector, decoded)
			if err != nil {
				t.Fatalf("%s rejected catalog example: %v", vector.Evaluator, err)
			}
			if instructionEffectsEmpty(effects) {
				t.Fatalf("%s produced no functional result/effect", vector.Evaluator)
			}
		})
	}

	for _, entry := range catalog {
		if _, exists := registered[entry.Name]; !exists {
			t.Errorf("catalog entry %q lacks a registered functional evaluator/vector", entry.Name)
		}
	}
	if len(registeredFunctionalVectors) != len(catalog) {
		t.Errorf("functional vector count=%d, catalog count=%d", len(registeredFunctionalVectors), len(catalog))
	}
}

func runRegisteredFunctionalVector(vector registeredFunctionalVector, decoded Decoded) (InstructionEffects, error) {
	switch vector.Evaluator {
	case evaluatorInteger:
		if decoded.Category != CategoryRV32I && decoded.Category != CategoryRV32M &&
			decoded.Category != CategoryZicond && decoded.Category != CategoryFence {
			return InstructionEffects{}, fmt.Errorf("integer evaluator registered for category %s", decoded.Category)
		}
		input := IntegerInput{PC: 0x100, ActiveMask: AllLanes, RS1: filledValues(0x100), RS2: filledValues(1)}
		return EvaluateInteger(decoded, input)

	case evaluatorFloat:
		if decoded.Category != CategoryRV32F {
			return InstructionEffects{}, fmt.Errorf("float evaluator registered for category %s", decoded.Category)
		}
		input := FloatInput{
			PC: 0x100, ActiveMask: AllLanes,
			RS1: filledValues(0x3f800000), RS2: filledValues(0x40000000), RS3: filledValues(0x40400000),
			FRM: RNE,
		}
		return EvaluateFloat(decoded, input)

	case evaluatorSystem:
		if decoded.Category != CategorySystem {
			return InstructionEffects{}, fmt.Errorf("system evaluator registered for category %s", decoded.Category)
		}
		input := SystemInput{
			PC: 0x100, ActiveMask: AllLanes, RS1: filledValues(1),
			CSR: CSRView{WarpID: 1, ActiveWarps: uint8(AllWarps), ThreadMask: AllLanes, MTVec: 0x200, MEPC: 0x300},
		}
		return EvaluateSystem(decoded, input)

	case evaluatorCustom:
		if decoded.Category != CategoryCustom {
			return InstructionEffects{}, fmt.Errorf("custom evaluator registered for category %s", decoded.Category)
		}
		input := CustomInput{PC: 0x100, ActiveMask: AllLanes, WarpID: 1}
		// A JOIN vector with no stored divergence is legal when its operand
		// equals the explicitly supplied current write pointer.
		input.Divergence.WritePointer = uint8(input.RS1[3] & 3)
		return EvaluateCustom(decoded, input)

	default:
		return InstructionEffects{}, fmt.Errorf("unknown registered evaluator %q", vector.Evaluator)
	}
}
