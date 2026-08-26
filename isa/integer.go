package isa

// EvaluateInteger evaluates one already-decoded RV32I/M/Zicond or integer
// memory/fence instruction. It owns no state and never applies its effects.
func EvaluateInteger(decoded Decoded, input IntegerInput) (IntegerEffects, error) {
	if !input.ActiveMask.Valid() {
		return IntegerEffects{}, evalError(decoded, "active mask exceeds four frozen lanes")
	}
	if input.Bounds != nil && !input.Bounds.valid() {
		return IntegerEffects{}, evalError(decoded, "invalid memory bounds view")
	}
	if !integerMilestoneInstruction(decoded) {
		return IntegerEffects{}, evalError(decoded, "instruction is outside the integer/memory milestone")
	}

	effects := IntegerEffects{}
	if decoded.Memory.Kind == MemoryFence {
		effects.Ordering = &OrderingEffect{
			Predecessor: uint8((decoded.Word >> 24) & 0xf),
			Successor:   uint8((decoded.Word >> 20) & 0xf),
		}
		effects.Control = sequentialControl(input.PC)
		return effects, nil
	}
	if decoded.Memory.Kind == MemoryLoad || decoded.Memory.Kind == MemoryStore {
		return evaluateMemory(decoded, input)
	}
	if decoded.Control == ControlBranch || decoded.Control == ControlJump {
		return evaluateControl(decoded, input)
	}

	values, ok := evaluateALULanes(decoded, input)
	if !ok {
		return IntegerEffects{}, evalError(decoded, "missing integer functional semantics")
	}
	effects.Control = sequentialControl(input.PC)
	appendRegisterWrite(&effects, decoded, input.ActiveMask, values)
	return effects, nil
}

func integerMilestoneInstruction(decoded Decoded) bool {
	return decoded.Category == CategoryRV32I || decoded.Category == CategoryRV32M ||
		decoded.Category == CategoryZicond || decoded.Category == CategoryFence
}

func evalError(decoded Decoded, problem string) error {
	return &EvaluationError{Instruction: decoded.Name, Problem: problem}
}

func sequentialControl(pc uint32) *ControlEffect {
	return &ControlEffect{Reason: PCSequential, CurrentPC: pc, NextPC: pc + 4}
}

func evaluateALULanes(decoded Decoded, input IntegerInput) (LaneValues, bool) {
	var result LaneValues
	known := true
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		if !input.ActiveMask.Active(lane) {
			continue
		}
		a := input.RS1[lane]
		b := input.RS2[lane]
		imm := uint32(decoded.Immediate)
		switch decoded.Name {
		case "lui":
			result[lane] = imm
		case "auipc":
			result[lane] = input.PC + imm
		case "addi":
			result[lane] = a + imm
		case "slti":
			result[lane] = boolWord(int32(a) < int32(imm))
		case "sltiu":
			result[lane] = boolWord(a < imm)
		case "xori":
			result[lane] = a ^ imm
		case "ori":
			result[lane] = a | imm
		case "andi":
			result[lane] = a & imm
		case "slli":
			result[lane] = a << (imm & 31)
		case "srli":
			result[lane] = a >> (imm & 31)
		case "srai":
			result[lane] = uint32(int32(a) >> (imm & 31))
		case "add":
			result[lane] = a + b
		case "sub":
			result[lane] = a - b
		case "sll":
			result[lane] = a << (b & 31)
		case "slt":
			result[lane] = boolWord(int32(a) < int32(b))
		case "sltu":
			result[lane] = boolWord(a < b)
		case "xor":
			result[lane] = a ^ b
		case "srl":
			result[lane] = a >> (b & 31)
		case "sra":
			result[lane] = uint32(int32(a) >> (b & 31))
		case "or":
			result[lane] = a | b
		case "and":
			result[lane] = a & b
		case "mul":
			result[lane] = uint32(uint64(a) * uint64(b))
		case "mulh":
			result[lane] = highSignedSigned(a, b)
		case "mulhsu":
			result[lane] = highSignedUnsigned(a, b)
		case "mulhu":
			result[lane] = uint32((uint64(a) * uint64(b)) >> 32)
		case "div":
			result[lane] = divideSigned(a, b)
		case "divu":
			result[lane] = divideUnsigned(a, b)
		case "rem":
			result[lane] = remainderSigned(a, b)
		case "remu":
			result[lane] = remainderUnsigned(a, b)
		case "czero.eqz":
			if b != 0 {
				result[lane] = a
			}
		case "czero.nez":
			if b == 0 {
				result[lane] = a
			}
		default:
			known = false
		}
	}
	return result, known
}

func evaluateControl(decoded Decoded, input IntegerInput) (IntegerEffects, error) {
	decisionLane, ok := highestActiveLane(input.ActiveMask)
	if !ok {
		return IntegerEffects{}, evalError(decoded, "control instruction requires an active lane")
	}
	effects := IntegerEffects{}
	sequential := input.PC + 4
	control := &ControlEffect{CurrentPC: input.PC, NextPC: sequential, DecisionLane: decisionLane}

	if decoded.Control == ControlJump {
		control.Reason = PCJump
		control.Taken = true
		if decoded.Name == "jal" {
			control.Target = input.PC + uint32(decoded.Immediate)
		} else {
			control.Target = (input.RS1[decisionLane] + uint32(decoded.Immediate)) &^ 1
		}
		if control.Target&3 != 0 {
			effects.Faults = []FaultEffect{{Kind: FaultInstructionAddressMisaligned, Reason: FaultReasonAlignment, Lane: decisionLane, Address: control.Target, Width: 4}}
			return effects, nil
		}
		control.NextPC = control.Target
		appendRegisterWrite(&effects, decoded, input.ActiveMask, filledValues(sequential))
		effects.Control = control
		return effects, nil
	}

	control.Reason = PCBranch
	control.Target = input.PC + uint32(decoded.Immediate)
	a := input.RS1[decisionLane]
	b := input.RS2[decisionLane]
	switch decoded.Name {
	case "beq":
		control.Taken = a == b
	case "bne":
		control.Taken = a != b
	case "blt":
		control.Taken = int32(a) < int32(b)
	case "bge":
		control.Taken = int32(a) >= int32(b)
	case "bltu":
		control.Taken = a < b
	case "bgeu":
		control.Taken = a >= b
	default:
		return IntegerEffects{}, evalError(decoded, "unknown branch operation")
	}
	if control.Taken {
		if control.Target&3 != 0 {
			effects.Faults = []FaultEffect{{Kind: FaultInstructionAddressMisaligned, Reason: FaultReasonAlignment, Lane: decisionLane, Address: control.Target, Width: 4}}
			return effects, nil
		}
		control.NextPC = control.Target
	}
	effects.Control = control
	return effects, nil
}

func evaluateMemory(decoded Decoded, input IntegerInput) (IntegerEffects, error) {
	if decoded.Memory.Bytes != 1 && decoded.Memory.Bytes != 2 && decoded.Memory.Bytes != 4 {
		return IntegerEffects{}, evalError(decoded, "unsupported integer memory width")
	}
	effects := IntegerEffects{}
	requests := make([]MemoryRequest, 0, FrozenLaneCount)
	faults := make([]FaultEffect, 0, FrozenLaneCount)
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		if !input.ActiveMask.Active(lane) {
			continue
		}
		address := input.RS1[lane] + uint32(decoded.Immediate)
		if address%uint32(decoded.Memory.Bytes) != 0 {
			kind := FaultLoadAddressMisaligned
			if decoded.Memory.Kind == MemoryStore {
				kind = FaultStoreAddressMisaligned
			}
			faults = append(faults, FaultEffect{Kind: kind, Reason: FaultReasonAlignment, Lane: lane, Address: address, Width: decoded.Memory.Bytes})
			continue
		}
		if input.Bounds != nil && !input.Bounds.contains(address, decoded.Memory.Bytes) {
			kind := FaultLoadAccess
			if decoded.Memory.Kind == MemoryStore {
				kind = FaultStoreAccess
			}
			faults = append(faults, FaultEffect{Kind: kind, Reason: FaultReasonBounds, Lane: lane, Address: address, Width: decoded.Memory.Bytes})
			continue
		}
		laneOffset := uint8(address & 3)
		byteMask := uint8((uint16(1)<<decoded.Memory.Bytes)-1) << laneOffset
		request := MemoryRequest{
			Lane:           lane,
			Kind:           decoded.Memory.Kind,
			Address:        address,
			AlignedAddress: address &^ 3,
			Width:          decoded.Memory.Bytes,
			Signed:         decoded.Memory.Signed,
			ByteMask:       byteMask,
		}
		if decoded.Memory.Kind == MemoryStore {
			request.StoreData = input.RS2[lane] << (8 * laneOffset)
		}
		requests = append(requests, request)
	}
	if len(faults) != 0 {
		effects.Faults = faults
		return effects, nil
	}
	effects.MemoryRequests = requests
	return effects, nil
}

// CompleteMemory converts memory-owner responses into load register writes or
// architectural access faults. A successful completion produces PC+4; fault
// completion produces neither PC nor partial register effects. It does not
// read or mutate memory itself.
func CompleteMemory(decoded Decoded, pc uint32, expected LaneMask, responses []MemoryResponse) (IntegerEffects, error) {
	if decoded.Memory.Kind != MemoryLoad && decoded.Memory.Kind != MemoryStore {
		return IntegerEffects{}, evalError(decoded, "instruction has no completable memory effect")
	}
	if !expected.Valid() {
		return IntegerEffects{}, evalError(decoded, "expected memory lane mask exceeds four frozen lanes")
	}
	effects := IntegerEffects{}
	var values LaneValues
	var mask LaneMask
	seen := LaneMask(0)
	for _, response := range responses {
		request := response.Request
		if request.Lane >= FrozenLaneCount || seen.Active(request.Lane) {
			return IntegerEffects{}, evalError(decoded, "duplicate or invalid memory response lane")
		}
		seen |= 1 << request.Lane
		if request.Kind != decoded.Memory.Kind || request.Width != decoded.Memory.Bytes || request.Signed != decoded.Memory.Signed {
			return IntegerEffects{}, evalError(decoded, "memory response does not match decoded operation")
		}
		if response.Fault != FaultNone {
			if (decoded.Memory.Kind == MemoryLoad && response.Fault != FaultLoadAccess) ||
				(decoded.Memory.Kind == MemoryStore && response.Fault != FaultStoreAccess) {
				return IntegerEffects{}, evalError(decoded, "memory response has incompatible fault kind")
			}
			reason := response.Reason
			if reason == FaultReasonNone {
				reason = FaultReasonMemoryService
			}
			effects.Faults = append(effects.Faults, FaultEffect{Kind: response.Fault, Reason: reason, Lane: request.Lane, Address: request.Address, Width: request.Width})
			continue
		}
		if decoded.Memory.Kind == MemoryLoad {
			values[request.Lane] = extendLoad(response.Data, request.Width, request.Signed)
			mask |= 1 << request.Lane
		}
	}
	if seen != expected {
		return IntegerEffects{}, evalError(decoded, "memory responses do not cover the expected lane mask")
	}
	if len(effects.Faults) != 0 {
		// An instruction is atomic at the effect boundary: never return partial
		// lane writeback together with a fault.
		return IntegerEffects{Faults: effects.Faults}, nil
	}
	if decoded.Memory.Kind == MemoryLoad {
		appendRegisterWrite(&effects, decoded, mask, values)
	}
	effects.Control = sequentialControl(pc)
	return effects, nil
}

func appendRegisterWrite(effects *IntegerEffects, decoded Decoded, mask LaneMask, values LaneValues) {
	if mask == 0 || len(decoded.Destinations) == 0 {
		return
	}
	destination := decoded.Destinations[0]
	if destination.File == Integer && destination.Index == 0 {
		return
	}
	effects.RegisterWrites = append(effects.RegisterWrites, RegisterWriteEffect{Destination: destination, Mask: mask, Values: values})
}

func extendLoad(value uint32, width uint8, signed bool) uint32 {
	switch width {
	case 1:
		if signed {
			return uint32(int32(int8(value)))
		}
		return value & 0xff
	case 2:
		if signed {
			return uint32(int32(int16(value)))
		}
		return value & 0xffff
	default:
		return value
	}
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func highSignedSigned(a, b uint32) uint32 {
	product := int64(int32(a)) * int64(int32(b))
	return uint32(uint64(product) >> 32)
}

func highSignedUnsigned(a, b uint32) uint32 {
	product := int64(int32(a)) * int64(uint64(b))
	return uint32(uint64(product) >> 32)
}

func divideSigned(a, b uint32) uint32 {
	if b == 0 {
		return ^uint32(0)
	}
	if a == 0x80000000 && b == 0xffffffff {
		return a
	}
	return uint32(int32(a) / int32(b))
}

func divideUnsigned(a, b uint32) uint32 {
	if b == 0 {
		return ^uint32(0)
	}
	return a / b
}

func remainderSigned(a, b uint32) uint32 {
	if b == 0 {
		return a
	}
	if a == 0x80000000 && b == 0xffffffff {
		return 0
	}
	return uint32(int32(a) % int32(b))
}

func remainderUnsigned(a, b uint32) uint32 {
	if b == 0 {
		return a
	}
	return a % b
}

func highestActiveLane(mask LaneMask) (uint8, bool) {
	for lane := uint8(FrozenLaneCount); lane != 0; lane-- {
		candidate := lane - 1
		if mask.Active(candidate) {
			return candidate, true
		}
	}
	return 0, false
}

func filledValues(value uint32) LaneValues {
	return LaneValues{value, value, value, value}
}
