package isa

import "fmt"

// Decode performs strict, stateless decoding against the authoritative
// catalog. Words accepted by a broad RTL case but excluded by the frozen
// configuration (for example D-format FP or RV64 opcodes) have no match.
func Decode(word uint32) (Decoded, error) {
	match := -1
	for i := range catalog {
		if catalog[i].accepts(word) {
			if match != -1 {
				return Decoded{}, fmt.Errorf("ambiguous catalog match for 0x%08x: %s and %s", word, catalog[match].Name, catalog[i].Name)
			}
			match = i
		}
	}
	if match == -1 {
		return Decoded{}, &IllegalInstructionError{Word: word}
	}
	return decodeEntry(word, &catalog[match]), nil
}

func decodeEntry(word uint32, entry *Entry) Decoded {
	d := Decoded{
		Word:               word,
		Name:               entry.Name,
		Category:           entry.Category,
		Rounding:           RoundingNone,
		Format:             entry.Format,
		Result:             entry.Result,
		Effects:            entry.Effects,
		RequiresPC:         entry.RequiresPC,
		RequiresActiveMask: entry.RequiresActiveMask,
		WriteMask:          entry.WriteMask,
		Memory:             entry.Memory,
		CSR:                entry.CSR,
		Control:            entry.Control,
		Barrier:            entry.Barrier,
	}
	for _, operand := range entry.Operands {
		reg := Register{File: operand.File, Index: registerIndex(word, operand.Field)}
		if operand.Access == Read {
			d.Sources = append(d.Sources, reg)
		} else {
			d.Destinations = append(d.Destinations, reg)
		}
	}
	if entry.Immediate != ImmediateNone {
		d.HasImmediate = true
		d.Immediate = decodeImmediate(word, entry.Immediate)
	}
	if entry.Rounding {
		d.Rounding = decodeRounding((word >> 12) & 7)
	}
	if entry.CSR != CSRNone {
		d.CSRAddress = uint16(word >> 20)
		d.CSRImmediate = entry.Name == "csrrwi" || entry.Name == "csrrsi" || entry.Name == "csrrci"
		if d.CSRImmediate {
			d.CSRImmediateValue = uint8((word >> 15) & 0x1f)
		}
	}
	switch entry.Modifier {
	case ModifierSplitNegateRS2:
		d.ConditionNegated = (word>>20)&1 != 0
	case ModifierPredicateNegateRD:
		d.ConditionNegated = (word>>7)&1 != 0
	case ModifierGatherSourceLane:
		d.GatherSourceLane = uint8((word >> 25) & 3)
	}
	return d
}

func registerIndex(word uint32, field RegisterField) uint8 {
	switch field {
	case FieldRD:
		return uint8((word >> 7) & 0x1f)
	case FieldRS1:
		return uint8((word >> 15) & 0x1f)
	case FieldRS2:
		return uint8((word >> 20) & 0x1f)
	case FieldRS3:
		return uint8((word >> 27) & 0x1f)
	default:
		panic("unknown register field")
	}
}

func signExtend(value uint32, bits uint8) int32 {
	shift := 32 - bits
	return int32(value<<shift) >> shift
}

func decodeImmediate(word uint32, kind ImmediateKind) int32 {
	switch kind {
	case ImmediateI:
		return signExtend(word>>20, 12)
	case ImmediateS:
		return signExtend(((word>>25)<<5)|((word>>7)&0x1f), 12)
	case ImmediateB:
		value := ((word >> 31) << 12) | (((word >> 7) & 1) << 11) | (((word >> 25) & 0x3f) << 5) | (((word >> 8) & 0xf) << 1)
		return signExtend(value, 13)
	case ImmediateU:
		return int32(word & 0xfffff000)
	case ImmediateJ:
		value := ((word >> 31) << 20) | (((word >> 12) & 0xff) << 12) | (((word >> 20) & 1) << 11) | (((word >> 21) & 0x3ff) << 1)
		return signExtend(value, 21)
	case ImmediateShift:
		return int32((word >> 20) & 0x1f)
	case ImmediateCSR:
		return int32((word >> 20) & 0xfff)
	default:
		return 0
	}
}

func decodeRounding(encoded uint32) RoundingMode {
	switch encoded {
	case 0:
		return RNE
	case 1:
		return RTZ
	case 2:
		return RDN
	case 3:
		return RUP
	case 4:
		return RMM
	case 7:
		return Dynamic
	default:
		panic("reserved rounding mode reached decode")
	}
}
