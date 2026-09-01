package core_test

import (
	"reflect"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
)

const testNOP = uint32(0x00108093)

func filled(value uint32) isa.LaneValues { return isa.LaneValues{value, value, value, value} }

func setBarrierInstruction(t *testing.T, owner *state.WarpState, source *wordSource, name string, addressWarp, id uint8, rs2 uint32, global bool) isa.Decoded {
	t.Helper()
	word, registers := customSources(t, name)
	decoded, err := isa.Decode(word)
	if err != nil {
		t.Fatal(err)
	}
	rs1 := uint32(addressWarp) | uint32(id)<<8
	if global {
		rs1 |= 1 << 31
	}
	if err := owner.WriteRegister(registers[0], isa.AllLanes, filled(rs1)); err != nil {
		t.Fatal(err)
	}
	if err := owner.WriteRegister(registers[1], isa.AllLanes, filled(rs2)); err != nil {
		t.Fatal(err)
	}
	source.word = word
	return decoded
}

func makeTwoWarpBarrierCore(t *testing.T) (*core.Core, *core.CTAManager, [isa.FrozenWarpCount]*state.WarpState, [isa.FrozenWarpCount]*wordSource) {
	t.Helper()
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(ctaConfig(0, []uint8{0, 1}, 8, 64)); err != nil {
		t.Fatal(err)
	}
	var initials [isa.FrozenWarpCount]state.WarpInitial
	var words [isa.FrozenWarpCount]uint32
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id < 2)
		words[id] = testNOP
	}
	manager, owners, sources := makeCTAReadyCoreFromInitials(t, initials, words, ctas)
	return manager, ctas, owners, sources
}

func TestCoreBarrierSyncWaitReleaseAndCanonicalPhase(t *testing.T) {
	manager, ctas, owners, sources := makeTwoWarpBarrierCore(t)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 3}
	manualBefore, _ := manager.Slot(0)
	if err := manager.Block(0, core.BlockBarrier); err == nil {
		t.Fatal("public Block created coordinator-owned barrier state")
	}
	manualAfter, _ := manager.Slot(0)
	if !reflect.DeepEqual(manualAfter, manualBefore) {
		t.Fatalf("rejected manual barrier block changed slot: before=%+v after=%+v", manualBefore, manualAfter)
	}
	setBarrierInstruction(t, owners[0], sources[0], "bar", 0, 3, 2, false)
	setBarrierInstruction(t, owners[1], sources[1], "bar", 0, 3, 2, false)

	before0, _ := owners[0].Snapshot()
	first, err := manager.Step(state.ReadContext{})
	after0, _ := owners[0].Snapshot()
	if err != nil || first.WarpID != 0 || first.WarpResult.Outcome != warp.OutcomeRetired ||
		first.NextLifecycle != core.WarpBlocked || first.BlockReason != core.BlockBarrier || after0.PC() != before0.PC()+4 {
		t.Fatalf("first sync=%+v before=%+v after=%+v err=%v", first, before0, after0, err)
	}
	record, err := manager.Barrier(key)
	if err != nil || record.Arrivals != 1 || record.Waiters != 1 || record.ParticipantCount != 2 || record.Phase {
		t.Fatalf("first record=%+v err=%v", record, err)
	}
	waitSlotBefore, _ := manager.Slot(0)
	waitOwnerBefore, _ := owners[0].Snapshot()
	if err := manager.Resume(0); err == nil {
		t.Fatal("public Resume released a canonical barrier waiter")
	}
	waitSlotAfter, _ := manager.Slot(0)
	waitOwnerAfter, _ := owners[0].Snapshot()
	recordAfterRejectedResume, _ := manager.Barrier(key)
	if !reflect.DeepEqual(waitSlotAfter, waitSlotBefore) || waitOwnerAfter != waitOwnerBefore || recordAfterRejectedResume != record {
		t.Fatalf("rejected waiter resume changed owners: slot=%+v/%+v warpChanged=%t barrier=%+v/%+v",
			waitSlotBefore, waitSlotAfter, waitOwnerBefore != waitOwnerAfter, record, recordAfterRejectedResume)
	}
	sources[0].word = testNOP

	before1, _ := owners[1].Snapshot()
	second, err := manager.Step(state.ReadContext{})
	after1, _ := owners[1].Snapshot()
	if err != nil || second.WarpID != 1 || second.WarpResult.Outcome != warp.OutcomeRetired ||
		second.NextLifecycle != core.WarpRunnable || after1.PC() != before1.PC()+4 {
		t.Fatalf("second sync=%+v before=%+v after=%+v err=%v", second, before1, after1, err)
	}
	record, _ = manager.Barrier(key)
	slot0, _ := manager.Slot(0)
	if !record.Phase || record.Arrivals != 0 || record.Waiters != 0 || slot0.Lifecycle != core.WarpRunnable {
		t.Fatalf("released record=%+v slot0=%+v", record, slot0)
	}

	// BAR.ARRIVE must return the coordinator's old phase even when the caller
	// supplies the opposite scalar view. A count-one arrival flips it again.
	arrive := setBarrierInstruction(t, owners[0], sources[0], "bar.arrive", 0, 3, 1, false)
	sources[1].word = testNOP
	step, err := manager.Step(state.ReadContext{BarrierPhase: false})
	if err != nil || step.WarpID != 0 || step.NextLifecycle != core.WarpRunnable {
		t.Fatalf("arrive=%+v err=%v", step, err)
	}
	values, err := owners[0].ReadRegister(arrive.Destinations[0])
	if err != nil || values != filled(1) {
		t.Fatalf("canonical phase write=%v err=%v", values, err)
	}
	record, _ = manager.Barrier(key)
	if record.Phase {
		t.Fatalf("count-one arrival did not reuse phase: %+v", record)
	}
	firstOwner, err := core.NewBarrierCoordinator(ctas)
	if err != nil {
		t.Fatal(err)
	}
	secondOwner, err := core.NewBarrierCoordinator(ctas)
	if err != nil || firstOwner != secondOwner {
		t.Fatalf("CTA manager created divergent barrier owners: first=%p second=%p err=%v", firstOwner, secondOwner, err)
	}
	if err := manager.SetCTAManager(ctas); err != nil {
		t.Fatal(err)
	}
	afterReattach, _ := manager.Barrier(key)
	if !reflect.DeepEqual(afterReattach, record) {
		t.Fatalf("idempotent CTA attachment reset barrier state: before=%+v after=%+v", record, afterReattach)
	}
}

func TestCoreBarrierWaitBeforeArriveAndImmediatePass(t *testing.T) {
	manager, _, owners, sources := makeTwoWarpBarrierCore(t)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 1}
	// Upper operand bits are not a participant count for WAIT; only bit zero
	// selects phase in the frozen bar unit.
	setBarrierInstruction(t, owners[0], sources[0], "bar.wait", 0, 1, 2, false)
	sources[1].word = testNOP
	wait, err := manager.Step(state.ReadContext{BarrierPhase: true})
	if err != nil || wait.WarpResult.Outcome != warp.OutcomeRetired || wait.NextLifecycle != core.WarpBlocked {
		t.Fatalf("wait=%+v err=%v", wait, err)
	}
	sources[0].word = testNOP
	setBarrierInstruction(t, owners[1], sources[1], "bar.arrive", 0, 1, 1, false)
	arrive, err := manager.Step(state.ReadContext{})
	if err != nil || arrive.WarpID != 1 || arrive.NextLifecycle != core.WarpRunnable {
		t.Fatalf("arrival=%+v err=%v", arrive, err)
	}
	slot0, _ := manager.Slot(0)
	record, _ := manager.Barrier(key)
	if slot0.Lifecycle != core.WarpRunnable || !record.Phase {
		t.Fatalf("waiter not released: slot=%+v record=%+v", slot0, record)
	}

	// Phase zero differs from canonical phase one and therefore passes now.
	setBarrierInstruction(t, owners[0], sources[0], "bar.wait", 0, 1, 0, false)
	sources[1].word = testNOP
	pass, err := manager.Step(state.ReadContext{BarrierPhase: false})
	if err != nil || pass.WarpID != 0 || pass.NextLifecycle != core.WarpRunnable || pass.WarpResult.Outcome != warp.OutcomeRetired {
		t.Fatalf("immediate wait=%+v err=%v", pass, err)
	}
}

func TestCoreBarrierEventsBlockUntilLastCompletion(t *testing.T) {
	manager, _, owners, sources := makeTwoWarpBarrierCore(t)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 7}
	setBarrierInstruction(t, owners[0], sources[0], "bar.arrive", 0, 7, 1<<31|2, false)
	sources[1].word = testNOP
	if step, err := manager.Step(state.ReadContext{}); err != nil || step.NextLifecycle != core.WarpRunnable {
		t.Fatalf("expect event=%+v err=%v", step, err)
	}
	record, _ := manager.Barrier(key)
	if record.Events != 2 || record.Phase {
		t.Fatalf("event attach record=%+v", record)
	}

	setBarrierInstruction(t, owners[1], sources[1], "bar", 0, 7, 1, false)
	sources[0].word = testNOP
	sync, err := manager.Step(state.ReadContext{})
	if err != nil || sync.WarpID != 1 || sync.NextLifecycle != core.WarpBlocked {
		t.Fatalf("event-gated sync=%+v err=%v", sync, err)
	}
	if err := manager.CompleteBarrierEvent(key); err != nil {
		t.Fatal(err)
	}
	slot, _ := manager.Slot(1)
	record, _ = manager.Barrier(key)
	if slot.Lifecycle != core.WarpBlocked || record.Events != 1 || !record.ArrivalsComplete {
		t.Fatalf("early event completion slot=%+v record=%+v", slot, record)
	}
	if err := manager.CompleteBarrierEvent(key); err != nil {
		t.Fatal(err)
	}
	slot, _ = manager.Slot(1)
	record, _ = manager.Barrier(key)
	if slot.Lifecycle != core.WarpRunnable || record.Events != 0 || !record.Phase {
		t.Fatalf("final event completion slot=%+v record=%+v", slot, record)
	}
	if err := manager.CompleteBarrierEvent(key); err == nil {
		t.Fatal("event underflow succeeded")
	}
}

func TestCoreBarrierLSUDrainRetriesOnceAndMultipleWaitersResume(t *testing.T) {
	t.Run("LSU-drain", func(t *testing.T) {
		ctas := core.NewCTAManager()
		if _, err := ctas.Admit(ctaConfig(0, []uint8{0}, 4, 64)); err != nil {
			t.Fatal(err)
		}
		var initials [isa.FrozenWarpCount]state.WarpInitial
		var words [isa.FrozenWarpCount]uint32
		for id := uint8(0); id < isa.FrozenWarpCount; id++ {
			initials[id] = initial(id, id == 0)
			words[id] = testNOP
		}
		manager, owners, sources := makeCTAReadyCoreFromInitials(t, initials, words, ctas)
		setBarrierInstruction(t, owners[0], sources[0], "bar", 0, 0, 1, false)
		before, _ := owners[0].Snapshot()
		blocked, err := manager.Step(state.ReadContext{PendingLSU: true})
		afterBlocked, _ := owners[0].Snapshot()
		if err != nil || blocked.WarpResult.Outcome != warp.OutcomeDeferred || blocked.NextLifecycle != core.WarpBlocked || afterBlocked != before {
			t.Fatalf("drain block=%+v err=%v changed=%t", blocked, err, afterBlocked != before)
		}
		slot, _ := manager.Slot(0)
		record, _ := manager.Barrier(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 0})
		if !slot.BarrierDraining || record != (core.BarrierSnapshot{Key: record.Key}) {
			t.Fatalf("drain exposed request: slot=%+v record=%+v", slot, record)
		}
		if err := manager.Resume(0); err == nil {
			t.Fatal("public Resume bypassed canonical LSU drain")
		}
		afterRejectedResume, _ := manager.Slot(0)
		afterRejectedOwner, _ := owners[0].Snapshot()
		afterRejectedRecord, _ := manager.Barrier(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 0})
		if !reflect.DeepEqual(afterRejectedResume, slot) || afterRejectedOwner != afterBlocked || afterRejectedRecord != record {
			t.Fatalf("rejected drain resume changed owners: slot=%+v/%+v warpChanged=%t barrier=%+v/%+v",
				slot, afterRejectedResume, afterRejectedOwner != afterBlocked, record, afterRejectedRecord)
		}
		retired, err := manager.Step(state.ReadContext{PendingLSU: false})
		after, _ := owners[0].Snapshot()
		if err != nil || retired.WarpResult.Outcome != warp.OutcomeRetired || after.PC() != before.PC()+4 || sources[0].reads != 2 {
			t.Fatalf("drain retry=%+v after=%+v reads=%d err=%v", retired, after, sources[0].reads, err)
		}
	})

	t.Run("multiple-waiters", func(t *testing.T) {
		ctas := core.NewCTAManager()
		if _, err := ctas.Admit(ctaConfig(0, []uint8{0, 1, 2}, 12, 64)); err != nil {
			t.Fatal(err)
		}
		var initials [isa.FrozenWarpCount]state.WarpInitial
		var words [isa.FrozenWarpCount]uint32
		for id := uint8(0); id < isa.FrozenWarpCount; id++ {
			initials[id] = initial(id, id < 3)
			words[id] = testNOP
		}
		manager, owners, sources := makeCTAReadyCoreFromInitials(t, initials, words, ctas)
		setBarrierInstruction(t, owners[0], sources[0], "bar", 0, 4, 3, false)
		setBarrierInstruction(t, owners[1], sources[1], "bar", 0, 4, 3, false)
		setBarrierInstruction(t, owners[2], sources[2], "bar.arrive", 0, 4, 3, false)
		for want := uint8(0); want < 2; want++ {
			step, err := manager.Step(state.ReadContext{})
			if err != nil || step.WarpID != want || step.NextLifecycle != core.WarpBlocked {
				t.Fatalf("waiter %d step=%+v err=%v", want, step, err)
			}
			sources[want].word = testNOP
		}
		last, err := manager.Step(state.ReadContext{})
		if err != nil || last.WarpID != 2 || last.NextLifecycle != core.WarpRunnable {
			t.Fatalf("last arrival=%+v err=%v", last, err)
		}
		for id := uint8(0); id < 3; id++ {
			slot, slotErr := manager.Slot(id)
			if slotErr != nil || slot.Lifecycle != core.WarpRunnable {
				t.Fatalf("released slot %d=%+v err=%v", id, slot, slotErr)
			}
		}
	})
}

func TestBarrierCoordinatorIsolationErrorsAndStaleAtomicity(t *testing.T) {
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(ctaConfig(0, []uint8{0, 1}, 8, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := ctas.Admit(ctaConfig(1, []uint8{2, 3}, 8, 64)); err != nil {
		t.Fatal(err)
	}
	coordinator, err := core.NewBarrierCoordinator(ctas)
	if err != nil {
		t.Fatal(err)
	}
	waitEffect := func(warp, address, id uint8, phase bool) isa.BarrierEffect {
		count, size := uint8(0), uint8(31)
		if phase {
			count, size = 1, 0
		}
		return isa.BarrierEffect{WarpID: warp, AddressWarp: address, ID: id, Kind: isa.BarrierWait,
			Wait: true, ReleaseByCoordinator: true, ParticipantCount: count, SizeMinusOne: size,
			Phase: phase, DrainLSU: true}
	}
	first, err := coordinator.Stage(waitEffect(0, 0, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := coordinator.Stage(waitEffect(1, 0, 0, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	before, _ := coordinator.Snapshot(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 0})
	if err := stale.Commit(); err == nil {
		t.Fatal("stale barrier stage committed")
	}
	after, _ := coordinator.Snapshot(before.Key)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("stale commit changed record: before=%+v after=%+v", before, after)
	}
	if _, err := coordinator.Stage(waitEffect(0, 2, 0, false)); err == nil {
		t.Fatal("cross-CTA AddressWarp succeeded")
	}
	arrival := func(warp, count uint8) isa.BarrierEffect {
		return isa.BarrierEffect{WarpID: warp, AddressWarp: 0, ID: 5, Kind: isa.BarrierArrive,
			Arrive: true, ParticipantCount: count, SizeMinusOne: count - 1,
			Phase: count&1 != 0, DrainLSU: true}
	}
	arrivalStage, err := coordinator.Stage(arrival(0, 2))
	if err != nil || arrivalStage.Commit() != nil {
		t.Fatalf("initial arrival stage=%v err=%v", arrivalStage, err)
	}
	arrivalBefore, _ := coordinator.Snapshot(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 5})
	for name, invalid := range map[string]isa.BarrierEffect{
		"duplicate":      arrival(0, 2),
		"count mismatch": arrival(1, 1),
	} {
		if _, err := coordinator.Stage(invalid); err == nil {
			t.Fatalf("%s arrival succeeded", name)
		}
		arrivalAfter, _ := coordinator.Snapshot(arrivalBefore.Key)
		if !reflect.DeepEqual(arrivalAfter, arrivalBefore) {
			t.Fatalf("%s changed arrival record: before=%+v after=%+v", name, arrivalBefore, arrivalAfter)
		}
	}

	// All eight IDs and both CTA namespaces remain independent.
	for id := uint8(0); id < 8; id++ {
		stage, stageErr := coordinator.Stage(waitEffect(2, 2, id, false))
		if stageErr != nil {
			t.Fatalf("stage CTA1/id%d: %v", id, stageErr)
		}
		if commitErr := stage.Commit(); commitErr != nil {
			t.Fatalf("commit CTA1/id%d: %v", id, commitErr)
		}
	}
	unchanged, _ := coordinator.Snapshot(before.Key)
	if !reflect.DeepEqual(unchanged, before) {
		t.Fatalf("other CTA/IDs changed CTA0 key: before=%+v after=%+v", before, unchanged)
	}

	event := isa.BarrierEffect{WarpID: 0, AddressWarp: 0, ID: 6, Kind: isa.BarrierArrive,
		Event: true, ExpectCount: 32, SizeMinusOne: 31, Phase: true, DrainLSU: true}
	eventStage, err := coordinator.Stage(event)
	if err != nil || eventStage.Commit() != nil {
		t.Fatalf("max event attach stage=%v err=%v", eventStage, err)
	}
	if _, err := coordinator.Stage(isa.BarrierEffect{WarpID: 1, AddressWarp: 0, ID: 6, Kind: isa.BarrierArrive,
		Event: true, ExpectCount: 1, ParticipantCount: 1, Phase: true, DrainLSU: true}); err == nil {
		t.Fatal("event overflow succeeded")
	}
}

func TestCoreRejectsInvalidBarrierWithoutPartialArchitecturalOrCoordinatorCommit(t *testing.T) {
	manager, _, owners, sources := makeTwoWarpBarrierCore(t)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 2}
	setBarrierInstruction(t, owners[0], sources[0], "bar", 0, 2, 2, true)
	beforeState, _ := owners[0].Snapshot()
	beforeBarrier, _ := manager.Barrier(key)
	beforeSlot, _ := manager.Slot(0)
	step, err := manager.Step(state.ReadContext{})
	afterState, _ := owners[0].Snapshot()
	afterBarrier, _ := manager.Barrier(key)
	afterSlot, _ := manager.Slot(0)
	if err == nil || step.NextLifecycle != beforeSlot.Lifecycle || step.BlockReason != beforeSlot.BlockReason ||
		afterState != beforeState || !reflect.DeepEqual(afterBarrier, beforeBarrier) || !reflect.DeepEqual(afterSlot, beforeSlot) {
		t.Fatalf("global rejection step=%+v err=%v stateChanged=%t barrierBefore=%+v barrierAfter=%+v slotBefore=%+v slotAfter=%+v", step, err, afterState != beforeState, beforeBarrier, afterBarrier, beforeSlot, afterSlot)
	}
}

func TestCoreRejectsBarrierEffectWhenCanonicalPhaseRecordChangesDuringIssue(t *testing.T) {
	manager, ctas, owners, sources := makeTwoWarpBarrierCore(t)
	key := core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 2}
	setBarrierInstruction(t, owners[0], sources[0], "bar.arrive", 0, 2, 1, false)
	coordinator, err := core.NewBarrierCoordinator(ctas)
	if err != nil {
		t.Fatal(err)
	}
	sources[0].beforeRead = func() {
		stage, stageErr := coordinator.Stage(isa.BarrierEffect{
			WarpID: 1, AddressWarp: 0, ID: 2, Kind: isa.BarrierArrive,
			Arrive: true, ParticipantCount: 1, Phase: true, DrainLSU: true,
		})
		if stageErr != nil {
			t.Errorf("injected canonical update stage: %v", stageErr)
			return
		}
		if commitErr := stage.Commit(); commitErr != nil {
			t.Errorf("injected canonical update commit: %v", commitErr)
		}
	}
	before, _ := owners[0].Snapshot()
	beforeSlot, _ := manager.Slot(0)
	step, err := manager.Step(state.ReadContext{BarrierPhase: true})
	after, _ := owners[0].Snapshot()
	afterSlot, _ := manager.Slot(0)
	record, _ := manager.Barrier(key)
	if err == nil || step.NextLifecycle != beforeSlot.Lifecycle || step.BlockReason != beforeSlot.BlockReason ||
		after != before || !reflect.DeepEqual(afterSlot, beforeSlot) || !record.Phase || record.Arrivals != 0 {
		t.Fatalf("stale phase issue step=%+v err=%v changed=%t record=%+v slotBefore=%+v slotAfter=%+v", step, err, after != before, record, beforeSlot, afterSlot)
	}
}
