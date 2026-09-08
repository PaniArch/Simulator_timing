package isa

import (
	"errors"
	"testing"
)

func customDecoded(t *testing.T, name string, mutate func(uint32) uint32) Decoded {
	t.Helper()
	for _, entry := range catalog {
		if entry.Name != name {
			continue
		}
		word := entry.Example
		if mutate != nil {
			word = mutate(word)
		}
		decoded, err := Decode(word)
		if err != nil {
			t.Fatalf("decode %s word %#x: %v", name, word, err)
		}
		if decoded.Name != name {
			t.Fatalf("decode %#x=%s, want %s", word, decoded.Name, name)
		}
		return decoded
	}
	t.Fatalf("custom instruction %q not found", name)
	return Decoded{}
}

func basicCustomInput() CustomInput {
	return CustomInput{PC: 0x100, ActiveMask: AllLanes, WarpID: 1}
}

func onlyWrite(t *testing.T, effects CustomEffects) RegisterWriteEffect {
	t.Helper()
	if len(effects.RegisterWrites) != 1 {
		t.Fatalf("register writes=%+v", effects.RegisterWrites)
	}
	return effects.RegisterWrites[0]
}

func TestEveryCustomCatalogInstructionIsFunctional(t *testing.T) {
	covered := make(map[string]bool)
	for _, entry := range catalog {
		if entry.Category != CategoryCustom {
			continue
		}
		decoded, err := Decode(entry.Example)
		if err != nil {
			t.Fatalf("decode %s: %v", entry.Name, err)
		}
		input := basicCustomInput()
		input.Divergence.WritePointer = uint8(input.RS1[3] & 3)
		if _, err := EvaluateCustom(decoded, input); err != nil {
			t.Errorf("evaluate %s: %v", entry.Name, err)
		}
		covered[entry.Name] = true
	}
	for _, entry := range catalog {
		if entry.Category == CategoryCustom && !covered[entry.Name] {
			t.Errorf("custom catalog instruction %s has no functional case", entry.Name)
		}
	}
}

func TestEveryCustomInstructionAcceptsDeterministicEmptyMaskView(t *testing.T) {
	for _, entry := range catalog {
		if entry.Category != CategoryCustom {
			continue
		}
		decoded, err := Decode(entry.Example)
		if err != nil {
			t.Fatal(err)
		}
		input := basicCustomInput()
		input.ActiveMask = 0
		input.Divergence.WritePointer = uint8(input.RS1[0] & 3)
		effects, err := EvaluateCustom(decoded, input)
		if err != nil {
			t.Errorf("empty-mask %s: %v", entry.Name, err)
			continue
		}
		if entry.Memory.Packed != 0 && (len(effects.PackedLoads) != 0 || len(effects.Faults) != 0) {
			t.Errorf("empty-mask %s touched memory: %+v", entry.Name, effects)
		}
	}
}

func TestTMCAndPredicateMasksIncludingEmpty(t *testing.T) {
	tmc := customDecoded(t, "tmc", nil)
	input := basicCustomInput()
	input.ActiveMask = 0b0101
	input.RS1[2] = 0b1010
	effects, err := EvaluateCustom(tmc, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.WarpMasks) != 1 || effects.WarpMasks[0] != (WarpMaskEffect{WarpID: 1, Reason: WarpMaskTMC, Mask: 0b1010, Active: true}) {
		t.Fatalf("TMC mask=%+v", effects.WarpMasks)
	}

	input.ActiveMask = 0
	input.RS1[0] = 0
	effects, err = EvaluateCustom(tmc, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.WarpMasks[0].Mask != 0 || effects.WarpMasks[0].Active {
		t.Fatalf("empty TMC lifecycle=%+v", effects.WarpMasks[0])
	}

	pred := customDecoded(t, "pred", func(word uint32) uint32 { return word &^ (1 << 7) })
	input = basicCustomInput()
	input.ActiveMask = 0b1101
	input.RS1 = LaneValues{1, 1, 0, 1}
	effects, err = EvaluateCustom(pred, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := effects.WarpMasks[0]; got.Mask != 0b1001 || got.Reason != WarpMaskPredicate {
		t.Fatalf("PRED selected=%+v", got)
	}

	negated := customDecoded(t, "pred", func(word uint32) uint32 { return word | 1<<7 })
	effects, err = EvaluateCustom(negated, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.WarpMasks[0].Mask != 0b0100 {
		t.Fatalf("negated PRED mask=%04b", effects.WarpMasks[0].Mask)
	}

	input.RS1 = LaneValues{}
	input.RS2[3] = 0b0011
	effects, err = EvaluateCustom(pred, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.WarpMasks[0].Mask != 0b0011 {
		t.Fatalf("PRED fallback=%04b", effects.WarpMasks[0].Mask)
	}
}

func TestWspawnTargetsCountBoundariesAndMScratch(t *testing.T) {
	decoded := customDecoded(t, "wspawn", nil)
	for _, test := range []struct {
		count uint32
		want  WarpMask
	}{
		{0, 0}, {1, 0b0001}, {4, 0b1101}, {7, 0b1101},
	} {
		input := basicCustomInput()
		input.WarpID = 1
		input.ActiveMask = 0b0101 // highest active lane supplies warp-control operands
		input.RS1[2] = test.count
		input.RS2[2] = 0x12345678
		input.MScratch = 0xfeedbeef
		effects, err := EvaluateCustom(decoded, input)
		if err != nil {
			t.Fatal(err)
		}
		spawn := effects.WarpSpawn
		if spawn == nil || spawn.Targets != test.want || spawn.RequestedCount != uint8(test.count) ||
			spawn.TargetPC != 0x12345678 || spawn.InitialLaneMask != 1 || !spawn.CopyMScratch ||
			spawn.MScratch != 0xfeedbeef || !spawn.RequiresSingleActiveWarp || !spawn.ReleaseSourceAfterApply {
			t.Errorf("count %d spawn=%+v", test.count, spawn)
		}
	}
}

func TestSplitUniformDivergentNegatedAndMinorityFirst(t *testing.T) {
	decoded := customDecoded(t, "split", nil)
	input := basicCustomInput()
	input.Divergence.WritePointer = 2
	input.RS1 = LaneValues{1, 1, 1, 1}
	effects, err := EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Divergence == nil || effects.Divergence.Divergent || effects.Divergence.Push || len(effects.WarpMasks) != 0 {
		t.Fatalf("uniform split=%+v", effects)
	}
	if write := onlyWrite(t, effects); write.Mask != AllLanes || write.Values != filledValues(2) {
		t.Fatalf("split pointer result=%+v", write)
	}

	input.RS1 = LaneValues{1, 0, 0, 0}
	effects, err = EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	dvg := effects.Divergence
	if !dvg.Divergent || !dvg.Push || dvg.ExecuteMask != 0b0001 || dvg.DeferredMask != 0b1110 ||
		dvg.OriginalMask != AllLanes || dvg.ReconvergencePC != 0x104 || effects.WarpMasks[0].Mask != 1 {
		t.Fatalf("minority-first split=%+v masks=%+v", dvg, effects.WarpMasks)
	}

	input.RS1 = LaneValues{1, 1, 0, 0}
	effects, err = EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Divergence.ExecuteMask != 0b0011 { // ties choose the then side
		t.Fatalf("tie split execute=%04b", effects.Divergence.ExecuteMask)
	}

	negated := customDecoded(t, "split", func(word uint32) uint32 { return word | 1<<20 })
	effects, err = EvaluateCustom(negated, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Divergence.ExecuteMask != 0b1100 {
		t.Fatalf("negated split execute=%04b", effects.Divergence.ExecuteMask)
	}
}

func TestJoinNoopDeferredPathAndPop(t *testing.T) {
	decoded := customDecoded(t, "join", nil)
	input := basicCustomInput()
	input.ActiveMask = 0b0011
	input.RS1[1] = 2
	input.Divergence.WritePointer = 2
	effects, err := EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Divergence == nil || effects.Divergence.Divergent || len(effects.WarpMasks) != 0 || effects.Control.Reason != PCSequential {
		t.Fatalf("non-divergent JOIN=%+v", effects)
	}

	input.Divergence.WritePointer = 3
	input.Divergence.Record = DivergenceRecordView{Valid: true, Pointer: 2, OriginalMask: AllLanes, NextPC: 0x240}
	effects, err = EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if !effects.Divergence.MarkElseVisited || effects.Divergence.Pop || effects.Divergence.ExecuteMask != 0b1100 ||
		effects.WarpMasks[0].Mask != 0b1100 || effects.Control.Reason != PCReconverge || effects.Control.NextPC != 0x240 {
		t.Fatalf("first JOIN=%+v", effects)
	}

	input.ActiveMask = 0b1100
	input.RS1[3] = 2
	input.Divergence.Record.ElseVisited = true
	effects, err = EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Divergence.MarkElseVisited || !effects.Divergence.Pop || effects.Divergence.ExecuteMask != AllLanes ||
		effects.WarpMasks[0].Mask != AllLanes || effects.Control.Reason != PCSequential {
		t.Fatalf("second JOIN=%+v", effects)
	}

	input.Divergence.Record.Valid = false
	effects, err = EvaluateCustom(decoded, input)
	var eval *EvaluationError
	if !errors.As(err, &eval) || !instructionEffectsEmpty(effects) {
		t.Fatalf("invalid JOIN error=%T %v effects=%+v", err, err, effects)
	}
}

func TestBarrierVariantsAndWarpSyncEffects(t *testing.T) {
	input := basicCustomInput()
	input.ActiveMask = 0b0101
	input.RS1[2] = uint32(3<<8 | 2)
	input.RS2[2] = 4
	input.PendingLSU = true

	syncEffects, err := EvaluateCustom(customDecoded(t, "bar", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(syncEffects.WarpDrains) != 1 || !syncEffects.WarpDrains[0].Wait || len(syncEffects.Barriers) != 0 || syncEffects.Control != nil {
		t.Fatalf("pending LSU barrier leaked effects=%+v", syncEffects)
	}
	input.PendingLSU = false
	syncEffects, err = EvaluateCustom(customDecoded(t, "bar", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar := syncEffects.Barriers[0]
	if bar.Kind != BarrierSync || bar.ID != 3 || bar.AddressWarp != 2 || !bar.Sync || !bar.Arrive || !bar.Wait ||
		!bar.ReleaseByCoordinator || bar.ParticipantCount != 4 || bar.SizeMinusOne != 3 || !bar.DrainLSU || syncEffects.WarpDrains[0].Wait {
		t.Fatalf("sync barrier=%+v drains=%+v", bar, syncEffects.WarpDrains)
	}

	input.RS2[2] = 1
	waitEffects, err := EvaluateCustom(customDecoded(t, "bar.wait", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar = waitEffects.Barriers[0]
	if bar.Kind != BarrierWait || bar.Arrive || !bar.Wait || !bar.Phase || len(waitEffects.RegisterWrites) != 0 {
		t.Fatalf("wait barrier=%+v", bar)
	}

	input.BarrierPhase = true
	input.RS2[2] = 4
	arriveEffects, err := EvaluateCustom(customDecoded(t, "bar.arrive", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar = arriveEffects.Barriers[0]
	if bar.Kind != BarrierArrive || !bar.Arrive || bar.Wait || bar.Event || onlyWrite(t, arriveEffects).Values[2] != 1 {
		t.Fatalf("arrive barrier=%+v effects=%+v", bar, arriveEffects)
	}

	input.RS2[2] = 0x80000005
	expectEffects, err := EvaluateCustom(customDecoded(t, "bar.arrive", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar = expectEffects.Barriers[0]
	if !bar.Event || bar.Arrive || bar.ExpectCount != 5 || !bar.Phase {
		t.Fatalf("expect_tx barrier=%+v", bar)
	}
	input.RS2[2] = 0x80000000
	expectEffects, err = EvaluateCustom(customDecoded(t, "bar.arrive", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar = expectEffects.Barriers[0]
	if !bar.Event || bar.Arrive || !bar.Phase || bar.ExpectCount != 32 || bar.SizeMinusOne != 31 {
		t.Fatalf("expect_tx count-zero encoding=%+v", expectEffects.Barriers)
	}
	input.RS2[2] = 0x80000006
	expectEffects, err = EvaluateCustom(customDecoded(t, "bar.arrive", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	bar = expectEffects.Barriers[0]
	if !bar.Event || bar.Arrive || !bar.Phase || bar.ExpectCount != 6 || bar.SizeMinusOne != 5 {
		t.Fatalf("expect_tx even-count encoding=%+v", expectEffects.Barriers)
	}

	input.PendingPriorWork = true
	wsync, err := EvaluateCustom(customDecoded(t, "wsync", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(wsync.WarpDrains) != 1 || wsync.WarpDrains[0].Kind != DrainPriorInstructions || !wsync.WarpDrains[0].Wait || !wsync.WarpDrains[0].ReleaseAfterDrain {
		t.Fatalf("WSYNC drain=%+v", wsync.WarpDrains)
	}
	if wsync.Control != nil {
		t.Fatalf("pending WSYNC advanced PC: %+v", wsync.Control)
	}
	input.PendingPriorWork = false
	wsync, err = EvaluateCustom(customDecoded(t, "wsync", nil), input)
	if err != nil || wsync.WarpDrains[0].Wait || wsync.Control == nil || wsync.Control.NextPC != input.PC+4 {
		t.Fatalf("drained WSYNC=%+v err=%v", wsync, err)
	}
}

func TestVoteFourLaneAndEmptyMaskRules(t *testing.T) {
	input := basicCustomInput()
	input.ActiveMask = 0b1101
	input.RS1 = LaneValues{1, 1, 0, 1}
	for _, test := range []struct {
		name string
		want uint32
	}{
		{"vote.all", 0}, {"vote.any", 1}, {"vote.uni", 0}, {"vote.ballot", 0b1001},
	} {
		effects, err := EvaluateCustom(customDecoded(t, test.name, nil), input)
		if err != nil {
			t.Fatal(err)
		}
		write := onlyWrite(t, effects)
		if write.Mask != 0b1101 || write.Values != filledValues(test.want) {
			t.Errorf("%s write=%+v", test.name, write)
		}
	}

	input.ActiveMask = 0
	for _, test := range []struct {
		name string
		want uint32
	}{
		{"vote.all", 1}, {"vote.any", 0}, {"vote.uni", 1}, {"vote.ballot", 0},
	} {
		effects, err := EvaluateCustom(customDecoded(t, test.name, nil), input)
		if err != nil || len(effects.RegisterWrites) != 0 || effects.Control == nil {
			t.Errorf("empty %s effects=%+v err=%v (RTL scalar=%d)", test.name, effects, err, test.want)
		}
	}
}

func shuffleControl(b, c, mask uint32) uint32 { return b | c<<6 | mask<<12 }

func TestShuffleModesBoundariesAndInactiveFallback(t *testing.T) {
	input := basicCustomInput()
	input.RS1 = LaneValues{10, 20, 30, 40}
	for lane := range input.RS2 {
		input.RS2[lane] = shuffleControl(1, 3, 0)
	}
	for _, test := range []struct {
		name string
		want LaneValues
	}{
		{"shfl.up", LaneValues{10, 10, 20, 30}},
		{"shfl.down", LaneValues{20, 30, 40, 40}},
		{"shfl.bfly", LaneValues{20, 10, 40, 30}},
	} {
		effects, err := EvaluateCustom(customDecoded(t, test.name, nil), input)
		if err != nil {
			t.Fatal(err)
		}
		if write := onlyWrite(t, effects); write.Mask != AllLanes || write.Values != test.want {
			t.Errorf("%s write=%+v want=%v", test.name, write, test.want)
		}
	}

	for lane := range input.RS2 {
		input.RS2[lane] = shuffleControl(2, 3, 0)
	}
	idx, err := EvaluateCustom(customDecoded(t, "shfl.idx", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	if write := onlyWrite(t, idx); write.Values != (LaneValues{30, 30, 30, 30}) {
		t.Fatalf("SHFL.IDX=%+v", write)
	}

	input.ActiveMask = 0b0101
	for lane := range input.RS2 {
		input.RS2[lane] = shuffleControl(1, 3, 0)
	}
	down, err := EvaluateCustom(customDecoded(t, "shfl.down", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	if write := onlyWrite(t, down); write.Mask != 0b0101 || write.Values != (LaneValues{10, 0, 30, 0}) {
		t.Fatalf("inactive target fallback=%+v", write)
	}

	input.ActiveMask = AllLanes
	for lane := range input.RS2 {
		input.RS2[lane] = shuffleControl(1, 1, 2) // two independent 2-lane groups
	}
	down, err = EvaluateCustom(customDecoded(t, "shfl.down", nil), input)
	if err != nil {
		t.Fatal(err)
	}
	if write := onlyWrite(t, down); write.Values != (LaneValues{20, 20, 40, 40}) {
		t.Fatalf("group boundary=%+v", write)
	}
}

func TestWGatherEverySourceFallbackRotationAndWriteMask(t *testing.T) {
	for source := uint8(0); source < FrozenLaneCount; source++ {
		decoded := customDecoded(t, "wgather", func(word uint32) uint32 {
			return (word &^ (3 << 25)) | uint32(source)<<25
		})
		input := basicCustomInput()
		input.RS1[source] = 0x11
		input.RS2[source] = 0x22
		input.RS3[source] = 0x33
		effects, err := EvaluateCustom(decoded, input)
		if err != nil {
			t.Fatal(err)
		}
		write := onlyWrite(t, effects)
		if write.Mask != AllLanes&^(1<<source) {
			t.Errorf("source %d mask=%04b", source, write.Mask)
		}
		for lane := uint8(0); lane < FrozenLaneCount; lane++ {
			want := uint32(0)
			switch (lane - source) & 3 {
			case 1:
				want = 0x11
			case 2:
				want = 0x22
			case 3:
				want = 0x33
			}
			if write.Values[lane] != want {
				t.Errorf("source %d lane %d=%#x want %#x", source, lane, write.Values[lane], want)
			}
		}
	}

	decoded := customDecoded(t, "wgather", func(word uint32) uint32 { return (word &^ (3 << 25)) | 1<<25 })
	input := basicCustomInput()
	input.ActiveMask = 0b0101 // nominal source lane 1 is inactive; highest active lane is 2
	input.RS1[2], input.RS2[2], input.RS3[2] = 0xaa, 0xbb, 0xcc
	effects, err := EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	write := onlyWrite(t, effects)
	if write.Mask != 0b1101 || write.Values != (LaneValues{0xcc, 0, 0xaa, 0xbb}) {
		t.Fatalf("fallback gather=%+v", write)
	}

	input.ActiveMask = 0
	input.RS1[0], input.RS2[0], input.RS3[0] = 1, 2, 3
	effects, err = EvaluateCustom(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if write = onlyWrite(t, effects); write.Mask != 0b1101 || write.Values != (LaneValues{3, 0, 1, 2}) {
		t.Fatalf("empty-mask RTL zero fallback=%+v", write)
	}
}

func TestPackedLoadIssueAddressesWidthsBoundsAndAlignment(t *testing.T) {
	packb := customDecoded(t, "vx_packlb_f", nil)
	input := basicCustomInput()
	input.ActiveMask = 0b0101
	input.RS1[0], input.RS2[0] = 0x10, 3
	input.RS1[2], input.RS2[2] = 0x30, 4
	effects, err := EvaluateCustom(packb, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.PackedLoads) != 8 || effects.Control != nil {
		t.Fatalf("PACKLB issue=%+v", effects)
	}
	wantAddresses := []uint32{0x10, 0x13, 0x16, 0x19, 0x30, 0x34, 0x38, 0x3c}
	for i, request := range effects.PackedLoads {
		if request.Address != wantAddresses[i] || request.Width != 1 || request.Element != uint8(i%4) {
			t.Errorf("PACKLB request[%d]=%+v", i, request)
		}
	}

	packh := customDecoded(t, "vx_packlh_f", nil)
	input.ActiveMask = 1
	input.RS1[0], input.RS2[0] = 0xfffffffc, 2
	effects, err = EvaluateCustom(packh, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.PackedLoads) != 2 || effects.PackedLoads[0].Address != 0xfffffffc || effects.PackedLoads[1].Address != 0xfffffffe || effects.PackedLoads[1].Width != 2 {
		t.Fatalf("PACKLH requests=%+v", effects.PackedLoads)
	}

	input.RS1[0] = 1
	effects, err = EvaluateCustom(packh, input)
	if err != nil || len(effects.Faults) != 2 || len(effects.PackedLoads) != 0 || effects.Faults[0].Kind != FaultLoadAddressMisaligned {
		t.Fatalf("misaligned PACKLH=%+v err=%v", effects, err)
	}

	input.RS1[0], input.RS2[0] = 6, 2
	input.Bounds = &AddressBounds{Base: 0, Size: 8}
	effects, err = EvaluateCustom(packh, input)
	if err != nil || len(effects.Faults) != 1 || effects.Faults[0].Reason != FaultReasonBounds || len(effects.PackedLoads) != 0 {
		t.Fatalf("bounds PACKLH=%+v err=%v", effects, err)
	}
}

func TestPackedLoadCompletionAssemblyOrderingAndAtomicFault(t *testing.T) {
	packb := customDecoded(t, "vx_packlb_f", nil)
	responses := []PackedLoadResponse{
		{Request: PackedLoadRequest{Lane: 0, Element: 3, Address: 0x13, Width: 1}, Data: 0x44},
		{Request: PackedLoadRequest{Lane: 0, Element: 1, Address: 0x11, Width: 1}, Data: 0x22},
		{Request: PackedLoadRequest{Lane: 0, Element: 0, Address: 0x10, Width: 1}, Data: 0x11},
		{Request: PackedLoadRequest{Lane: 0, Element: 2, Address: 0x12, Width: 1}, Data: 0x33},
	}
	effects, err := CompletePackedLoad(packb, 0xfffffffc, 1, responses)
	if err != nil {
		t.Fatal(err)
	}
	if write := onlyWrite(t, effects); write.Destination.File != Float || write.Values[0] != 0x44332211 || effects.Control.NextPC != 0 {
		t.Fatalf("PACKLB completion=%+v", effects)
	}

	packh := customDecoded(t, "vx_packlh_f", nil)
	halfResponses := []PackedLoadResponse{
		{Request: PackedLoadRequest{Lane: 0, Element: 1, Address: 2, Width: 2}, Data: 0xbbbb},
		{Request: PackedLoadRequest{Lane: 0, Element: 0, Address: 0, Width: 2}, Data: 0xaaaa},
	}
	effects, err = CompletePackedLoad(packh, 0x100, 1, halfResponses)
	if err != nil || onlyWrite(t, effects).Values[0] != 0xbbbbaaaa {
		t.Fatalf("PACKLH completion=%+v err=%v", effects, err)
	}

	halfResponses[1].Fault = FaultLoadAccess
	effects, err = CompletePackedLoad(packh, 0x100, 1, halfResponses)
	if err != nil || len(effects.Faults) != 1 || len(effects.RegisterWrites) != 0 || effects.Control != nil {
		t.Fatalf("atomic packed fault=%+v err=%v", effects, err)
	}

	_, err = CompletePackedLoad(packh, 0x100, 1, halfResponses[:1])
	var eval *EvaluationError
	if !errors.As(err, &eval) {
		t.Fatalf("incomplete response error=%T %v", err, err)
	}
}

func TestCustomInputValidationAndNoStateOwnership(t *testing.T) {
	decoded := customDecoded(t, "vote.any", nil)
	input := basicCustomInput()
	input.ActiveMask = 0x80
	effects, err := EvaluateCustom(decoded, input)
	var eval *EvaluationError
	if !errors.As(err, &eval) || !instructionEffectsEmpty(effects) {
		t.Fatalf("invalid mask error=%T %v effects=%+v", err, err, effects)
	}
	input = basicCustomInput()
	input.WarpID = FrozenWarpCount
	effects, err = EvaluateCustom(decoded, input)
	if !errors.As(err, &eval) || !instructionEffectsEmpty(effects) {
		t.Fatalf("invalid warp error=%T %v effects=%+v", err, err, effects)
	}
}

func TestPackedPartCompletionCoverage(t *testing.T) {
	for _, name := range []string{"vx_packlb_f", "vx_packlh_f"} {
		d := customDecoded(t, name, nil)
		for element := uint8(0); element < d.Memory.Packed; element++ {
			response := PackedLoadResponse{Request: PackedLoadRequest{Lane: 0, Element: element, Width: d.Memory.Bytes}, Data: 0xabcd}
			e, err := CompletePackedLoadPart(d, 0x100, 1, element, []PackedLoadResponse{response})
			if err != nil {
				t.Fatal(err)
			}
			w := onlyWrite(t, e)
			mask := uint32(0xff)
			if d.Memory.Bytes == 2 {
				mask = 0xffff
			}
			if e.Control != nil || w.ByteMask != uint8((1<<d.Memory.Bytes)-1)<<(element*d.Memory.Bytes) || w.Values[0] != (response.Data&mask)<<(8*element*d.Memory.Bytes) {
				t.Fatal(e)
			}
			if _, err := CompletePackedLoadPart(d, 0x100, 3, element, []PackedLoadResponse{response}); err == nil {
				t.Fatal("missing lane accepted")
			}
			if _, err := CompletePackedLoadPart(d, 0x100, 1, (element+1)%d.Memory.Packed, []PackedLoadResponse{response}); err == nil {
				t.Fatal("foreign element accepted")
			}
		}
	}
}
