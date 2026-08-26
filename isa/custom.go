package isa

// EvaluateCustom evaluates one frozen Vortex custom/SIMT instruction against
// an immutable four-lane view. It returns owner-facing effects and never
// stores warp, divergence, barrier, register, or memory state.
func EvaluateCustom(decoded Decoded, input CustomInput) (CustomEffects, error) {
	if decoded.Category != CategoryCustom {
		return CustomEffects{}, evalError(decoded, "instruction is outside the Vortex custom milestone")
	}
	if !input.ActiveMask.Valid() {
		return CustomEffects{}, evalError(decoded, "active mask exceeds four frozen lanes")
	}
	if input.WarpID >= FrozenWarpCount {
		return CustomEffects{}, evalError(decoded, "warp id exceeds frozen warp count")
	}
	if input.Bounds != nil && !input.Bounds.valid() {
		return CustomEffects{}, evalError(decoded, "invalid memory bounds view")
	}

	switch decoded.Name {
	case "vote.all", "vote.any", "vote.uni", "vote.ballot",
		"shfl.up", "shfl.down", "shfl.bfly", "shfl.idx", "wgather":
		return evaluateCrossLane(decoded, input)
	case "vx_packlb_f", "vx_packlh_f":
		return evaluatePackedLoad(decoded, input)
	default:
		return evaluateWarpControl(decoded, input)
	}
}

func evaluateCrossLane(decoded Decoded, input CustomInput) (CustomEffects, error) {
	effects := CustomEffects{Control: sequentialControl(input.PC)}
	var values LaneValues
	writeMask := input.ActiveMask

	switch decoded.Name {
	case "vote.all", "vote.any", "vote.uni", "vote.ballot":
		var trueMask, falseMask LaneMask
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			if !input.ActiveMask.Active(lane) {
				continue
			}
			if input.RS1[lane]&1 != 0 {
				trueMask |= 1 << lane
			} else {
				falseMask |= 1 << lane
			}
		}
		var value uint32
		switch decoded.Name {
		case "vote.all":
			value = boolWord(falseMask == 0)
		case "vote.any":
			value = boolWord(trueMask != 0)
		case "vote.uni":
			value = boolWord(trueMask == 0 || falseMask == 0)
		case "vote.ballot":
			value = uint32(trueMask)
		}
		values = filledValues(value)

	case "shfl.up", "shfl.down", "shfl.bfly", "shfl.idx":
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			if !input.ActiveMask.Active(lane) {
				continue
			}
			bval := uint8(input.RS2[lane] & 3)
			cval := uint8((input.RS2[lane] >> 6) & 3)
			mask := uint8((input.RS2[lane] >> 12) & 3)
			minLane := lane & mask
			maxLane := minLane | (cval & (^mask & 3))
			target := lane
			switch decoded.Name {
			case "shfl.up":
				if lane >= bval && lane-bval >= minLane {
					target = lane - bval
				}
			case "shfl.down":
				candidate := uint16(lane) + uint16(bval)
				if candidate <= uint16(maxLane) {
					target = uint8(candidate)
				}
			case "shfl.bfly":
				candidate := lane ^ bval
				if candidate <= maxLane {
					target = candidate
				}
			case "shfl.idx":
				candidate := minLane | (bval & (^mask & 3))
				if candidate <= maxLane {
					target = candidate
				}
			}
			if input.ActiveMask.Active(target) {
				values[lane] = input.RS1[target]
			} else {
				values[lane] = input.RS1[lane]
			}
		}

	case "wgather":
		sourceOffset := decoded.GatherSourceLane & 3
		fallback, ok := highestActiveLane(input.ActiveMask)
		if !ok {
			// VX_find_first returns its zero padding when valid_out is ignored.
			fallback = 0
		}
		source := sourceOffset
		if !input.ActiveMask.Active(source) {
			source = fallback
		}
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			switch (lane - sourceOffset) & 3 {
			case 1:
				values[lane] = input.RS1[source]
			case 2:
				values[lane] = input.RS2[source]
			case 3:
				values[lane] = input.RS3[source]
			}
		}
		writeMask = AllLanes &^ (1 << sourceOffset)

	default:
		return CustomEffects{}, evalError(decoded, "missing cross-lane functional semantics")
	}

	appendRegisterWrite(&effects, decoded, writeMask, values)
	return effects, nil
}

func evaluateWarpControl(decoded Decoded, input CustomInput) (CustomEffects, error) {
	effects := CustomEffects{Control: sequentialControl(input.PC)}
	decisionLane, active := highestActiveLane(input.ActiveMask)
	if !active {
		decisionLane = 0
	}

	switch decoded.Name {
	case "tmc":
		mask := LaneMask(input.RS1[decisionLane]) & AllLanes
		effects.WarpMasks = []WarpMaskEffect{{WarpID: input.WarpID, Reason: WarpMaskTMC, Mask: mask, Active: mask != 0}}

	case "pred":
		var selected LaneMask
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			if input.ActiveMask.Active(lane) && ((input.RS1[lane]&1 != 0) != decoded.ConditionNegated) {
				selected |= 1 << lane
			}
		}
		if selected == 0 {
			selected = LaneMask(input.RS2[decisionLane]) & AllLanes
		}
		effects.WarpMasks = []WarpMaskEffect{{WarpID: input.WarpID, Reason: WarpMaskPredicate, Mask: selected, Active: selected != 0}}

	case "wspawn":
		count := uint8(input.RS1[decisionLane] & 7)
		var targets WarpMask
		for warp := uint8(0); warp < FrozenWarpCount; warp++ {
			if warp < count && warp != input.WarpID {
				targets |= 1 << warp
			}
		}
		effects.WarpSpawn = &WarpSpawnEffect{
			SourceWarp: input.WarpID, RequestedCount: count, Targets: targets,
			TargetPC: input.RS2[decisionLane], InitialLaneMask: 1,
			CopyMScratch: true, MScratch: input.MScratch, RequiresSingleActiveWarp: true,
			ReleaseSourceAfterApply: true,
		}

	case "split":
		if input.Divergence.WritePointer >= 4 {
			return CustomEffects{}, evalError(decoded, "divergence write pointer exceeds frozen encoding")
		}
		var thenMask LaneMask
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			if input.ActiveMask.Active(lane) && ((input.RS1[lane]&1 != 0) != decoded.ConditionNegated) {
				thenMask |= 1 << lane
			}
		}
		elseMask := input.ActiveMask &^ thenMask
		divergent := thenMask != 0 && elseMask != 0
		executeMask, deferredMask := thenMask, elseMask
		if popCount(thenMask) > popCount(elseMask) {
			executeMask, deferredMask = elseMask, thenMask
		}
		effects.Divergence = &DivergenceEffect{
			Action: DivergenceSplit, WarpID: input.WarpID,
			StackPointer: input.Divergence.WritePointer, Divergent: divergent,
			OriginalMask: input.ActiveMask, ExecuteMask: executeMask,
			DeferredMask: deferredMask, ReconvergencePC: input.PC + 4, Push: divergent,
		}
		if divergent {
			effects.WarpMasks = []WarpMaskEffect{{WarpID: input.WarpID, Reason: WarpMaskSplit, Mask: executeMask, Active: true}}
		}
		appendRegisterWrite(&effects, decoded, input.ActiveMask, filledValues(uint32(input.Divergence.WritePointer)))

	case "join":
		pointer := uint8(input.RS1[decisionLane] & 3)
		divergent := pointer != input.Divergence.WritePointer
		effect := &DivergenceEffect{Action: DivergenceJoin, WarpID: input.WarpID, StackPointer: pointer, Divergent: divergent, OriginalMask: input.ActiveMask}
		if divergent {
			record := input.Divergence.Record
			if !record.Valid || record.Pointer != pointer || !record.OriginalMask.Valid() {
				return CustomEffects{}, evalError(decoded, "JOIN requires the addressed divergence record view")
			}
			effect.OriginalMask = record.OriginalMask
			effect.ReconvergencePC = record.NextPC
			if record.ElseVisited {
				effect.ExecuteMask = record.OriginalMask
				effect.Pop = true
			} else {
				effect.ExecuteMask = record.OriginalMask &^ input.ActiveMask
				effect.MarkElseVisited = true
				effects.Control = &ControlEffect{Reason: PCReconverge, CurrentPC: input.PC, NextPC: record.NextPC, Target: record.NextPC, Taken: true, DecisionLane: decisionLane}
			}
			effects.WarpMasks = []WarpMaskEffect{{WarpID: input.WarpID, Reason: WarpMaskJoin, Mask: effect.ExecuteMask, Active: effect.ExecuteMask != 0}}
		}
		effects.Divergence = effect

	case "bar", "bar.arrive", "bar.wait":
		if input.PendingLSU {
			// VX_wctl_unit holds execute.ready low: the barrier request, phase
			// result, and PC effect do not exist until the LSU is drained.
			return CustomEffects{WarpDrains: []WarpDrainEffect{{WarpID: input.WarpID, Kind: DrainLSU, Wait: true}}}, nil
		}
		rs1 := input.RS1[decisionLane]
		rs2 := input.RS2[decisionLane]
		barrier := BarrierEffect{
			WarpID: input.WarpID, AddressWarp: uint8(rs1 & 3), ID: uint8((rs1 >> 8) & 7),
			Kind: decoded.Barrier, Global: rs1>>31 != 0, Phase: rs2&1 != 0,
			DrainLSU: true,
		}
		barrier.Sync = decoded.Barrier == BarrierSync
		barrier.Arrive = decoded.Barrier == BarrierSync || decoded.Barrier == BarrierArrive
		barrier.Wait = decoded.Barrier == BarrierSync || decoded.Barrier == BarrierWait
		barrier.ReleaseByCoordinator = barrier.Wait
		barrier.ParticipantCount = uint8(rs2 & 0x1f)
		barrier.SizeMinusOne = uint8((rs2 - 1) & 0x1f)
		if decoded.Barrier == BarrierArrive && rs2>>31 != 0 {
			barrier.Event = true
			barrier.Arrive = false
			// expect_tx is an event attachment, not an event completion.
			// VX_wctl_unit forces phase=1 on this path regardless of the
			// encoded count's low bit so VX_bar_unit increments events.
			barrier.Phase = true
			barrier.ExpectCount = uint8(rs2 & 0x1f)
			if barrier.ExpectCount == 0 {
				barrier.ExpectCount = 32
			}
		}
		effects.WarpDrains = []WarpDrainEffect{{WarpID: input.WarpID, Kind: DrainLSU}}
		effects.Barriers = []BarrierEffect{barrier}
		if decoded.Barrier == BarrierArrive {
			appendRegisterWrite(&effects, decoded, input.ActiveMask, filledValues(boolWord(input.BarrierPhase)))
		}

	case "wsync":
		effects.WarpDrains = []WarpDrainEffect{{WarpID: input.WarpID, Kind: DrainPriorInstructions, Wait: input.PendingPriorWork, ReleaseAfterDrain: true}}
		if input.PendingPriorWork {
			effects.Control = nil
		}

	default:
		return CustomEffects{}, evalError(decoded, "missing custom control functional semantics")
	}
	return effects, nil
}

func evaluatePackedLoad(decoded Decoded, input CustomInput) (CustomEffects, error) {
	if decoded.Memory.Kind != MemoryLoad || !decoded.Memory.Float ||
		!((decoded.Memory.Bytes == 1 && decoded.Memory.Packed == 4) || (decoded.Memory.Bytes == 2 && decoded.Memory.Packed == 2)) {
		return CustomEffects{}, evalError(decoded, "invalid packed-load metadata")
	}
	effects := CustomEffects{}
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		if !input.ActiveMask.Active(lane) {
			continue
		}
		for element := uint8(0); element < decoded.Memory.Packed; element++ {
			address := input.RS1[lane] + uint32(element)*input.RS2[lane]
			if address%uint32(decoded.Memory.Bytes) != 0 {
				effects.Faults = append(effects.Faults, FaultEffect{Kind: FaultLoadAddressMisaligned, Reason: FaultReasonAlignment, Lane: lane, Address: address, Width: decoded.Memory.Bytes})
				continue
			}
			if input.Bounds != nil && !input.Bounds.contains(address, decoded.Memory.Bytes) {
				effects.Faults = append(effects.Faults, FaultEffect{Kind: FaultLoadAccess, Reason: FaultReasonBounds, Lane: lane, Address: address, Width: decoded.Memory.Bytes})
				continue
			}
			effects.PackedLoads = append(effects.PackedLoads, PackedLoadRequest{Lane: lane, Element: element, Address: address, AlignedAddress: address &^ 3, Width: decoded.Memory.Bytes})
		}
	}
	if len(effects.Faults) != 0 {
		return CustomEffects{Faults: effects.Faults}, nil
	}
	return effects, nil
}

// CompletePackedLoad assembles little-endian element uops into one FPR value
// per active lane. A fault suppresses every partial write and PC effect.
func CompletePackedLoad(decoded Decoded, pc uint32, expected LaneMask, responses []PackedLoadResponse) (CustomEffects, error) {
	if decoded.Category != CategoryCustom || decoded.Memory.Kind != MemoryLoad || !decoded.Memory.Float ||
		!((decoded.Memory.Bytes == 1 && decoded.Memory.Packed == 4) || (decoded.Memory.Bytes == 2 && decoded.Memory.Packed == 2)) {
		return CustomEffects{}, evalError(decoded, "instruction has no completable packed load")
	}
	if !expected.Valid() {
		return CustomEffects{}, evalError(decoded, "expected packed-load mask exceeds four frozen lanes")
	}
	var seen [FrozenLaneCount][4]bool
	var values LaneValues
	var faults []FaultEffect
	for _, response := range responses {
		request := response.Request
		if request.Lane >= FrozenLaneCount || request.Element >= decoded.Memory.Packed || seen[request.Lane][request.Element] ||
			request.Width != decoded.Memory.Bytes {
			return CustomEffects{}, evalError(decoded, "duplicate or invalid packed-load response")
		}
		seen[request.Lane][request.Element] = true
		if response.Fault != FaultNone {
			if response.Fault != FaultLoadAccess {
				return CustomEffects{}, evalError(decoded, "packed-load response has incompatible fault kind")
			}
			reason := response.Reason
			if reason == FaultReasonNone {
				reason = FaultReasonMemoryService
			}
			faults = append(faults, FaultEffect{Kind: FaultLoadAccess, Reason: reason, Lane: request.Lane, Address: request.Address, Width: request.Width})
			continue
		}
		mask := uint32(0xff)
		if request.Width == 2 {
			mask = 0xffff
		}
		values[request.Lane] |= (response.Data & mask) << (8 * request.Width * request.Element)
	}
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		for element := uint8(0); element < decoded.Memory.Packed; element++ {
			if seen[lane][element] != expected.Active(lane) {
				return CustomEffects{}, evalError(decoded, "packed-load responses do not cover the expected lane/element set")
			}
		}
	}
	if len(faults) != 0 {
		return CustomEffects{Faults: faults}, nil
	}
	effects := CustomEffects{Control: sequentialControl(pc)}
	appendRegisterWrite(&effects, decoded, expected, values)
	return effects, nil
}

func popCount(mask LaneMask) uint8 {
	var count uint8
	for lane := uint8(0); lane < FrozenLaneCount; lane++ {
		if mask.Active(lane) {
			count++
		}
	}
	return count
}
