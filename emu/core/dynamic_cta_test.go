package core_test

import (
	"bytes"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
)

func makeDynamicCTACore(t *testing.T, word uint32) (*core.Core, *core.CTAManager, [isa.FrozenWarpCount]*state.WarpState, [isa.FrozenWarpCount]*core.CTAMemory) {
	t.Helper()
	ctas := core.NewDynamicCTAManager()
	global, err := memory.New(256)
	if err != nil {
		t.Fatal(err)
	}
	var owners [isa.FrozenWarpCount]*state.WarpState
	var routes [isa.FrozenWarpCount]*core.CTAMemory
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		owners[id], err = state.NewWarp(initial(id, false))
		if err != nil {
			t.Fatal(err)
		}
		routes[id], err = core.NewCTAMemory(ctas, id, global)
		if err != nil {
			t.Fatal(err)
		}
		executors[id], err = warp.NewWithServices(owners[id], &wordSource{word: word}, routes[id])
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := core.NewWithCTAManager(executors, ctas)
	if err != nil {
		t.Fatal(err)
	}
	return manager, ctas, owners, routes
}

func dynamicCTA(blockID uint32, blockSize, lmem uint32) core.CTAConfig {
	return core.CTAConfig{
		StartupPC: 0x100, BlockID: [3]uint32{blockID, 0, 0},
		BlockDimensions: [3]uint32{3, 3, 1}, GridDimensions: [3]uint32{8, 1, 1},
		BlockSize: blockSize, WarpStep: [3]uint32{4, 0, 0},
		Entry: 0x240, ParameterAddress: 0x8000 + blockID*0x100,
		LocalMemorySize: lmem, ClusterDimensions: [3]uint32{1, 1, 1},
		ClusterSize: 1, IsFirstOfCluster: true,
	}
}

func TestDynamicCTAFirstDispatchPartialMaskCoordinatesContextAndAtomicFailure(t *testing.T) {
	tmc := catalogWord(t, "tmc")
	manager, ctas, owners, _ := makeDynamicCTACore(t, tmc)
	var registersBefore [isa.FrozenWarpCount]isa.LaneValues
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		registersBefore[id], _ = owners[id].ReadRegister(isa.Register{File: isa.Integer, Index: 5})
	}

	config := dynamicCTA(3, 6, 65)
	admitted, err := manager.AdmitCTA(config)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.ID != 0 || admitted.StartupPC != config.StartupPC || admitted.Entry != config.Entry ||
		admitted.ParameterAddress != config.ParameterAddress || admitted.BlockSize != 6 ||
		admitted.WarpStep != config.WarpStep || admitted.LocalMemory.Size != 128 || len(admitted.Members) != 2 {
		t.Fatalf("wrong dynamic CTA context: %+v", admitted)
	}
	if admitted.Members[0].WarpID != 0 || admitted.Members[0].ActiveMask != isa.AllLanes ||
		admitted.Members[1].WarpID != 1 || admitted.Members[1].ActiveMask != 0b0011 {
		t.Fatalf("block size did not independently produce warp masks: %+v", admitted.Members)
	}
	wantWarp0 := [3]isa.LaneValues{{0, 1, 2, 0}, {0, 0, 0, 1}, {0, 0, 0, 0}}
	wantWarp1 := [3]isa.LaneValues{{1, 2, 0, 1}, {1, 1, 2, 2}, {0, 0, 0, 0}}
	if admitted.Members[0].ThreadCoordinates != wantWarp0 || admitted.Members[1].ThreadCoordinates != wantWarp1 {
		t.Fatalf("warp-step coordinates mismatch: got=%v/%v want=%v/%v",
			admitted.Members[0].ThreadCoordinates, admitted.Members[1].ThreadCoordinates, wantWarp0, wantWarp1)
	}
	for id := uint8(0); id < 2; id++ {
		snapshot, snapshotErr := owners[id].Snapshot()
		if snapshotErr != nil || snapshot.PC() != config.StartupPC || snapshot.ActiveMask() != admitted.Members[id].ActiveMask ||
			snapshot.TrapCSRs().MScratch != config.ParameterAddress || snapshot.Lifecycle() != state.WarpRunning {
			t.Fatalf("launched warp %d=%+v err=%v", id, snapshot, snapshotErr)
		}
		registersAfter, _ := owners[id].ReadRegister(isa.Register{File: isa.Integer, Index: 5})
		if registersAfter != registersBefore[id] {
			t.Fatalf("launch replaced warp %d register state", id)
		}
		view, viewErr := ctas.ViewForWarp(id)
		if viewErr != nil || view.Entry != config.Entry || view.ParameterAddress != config.ParameterAddress ||
			view.BlockSize != config.BlockSize || view.WarpStep != config.WarpStep {
			t.Fatalf("CTA view warp %d=%+v err=%v", id, view, viewErr)
		}
		slot, slotErr := manager.Slot(id)
		if slotErr != nil || slot.Lifecycle != core.WarpRunnable || !slot.Participated || !slot.InitializedForKernel {
			t.Fatalf("slot %d=%+v err=%v", id, slot, slotErr)
		}
	}

	if _, err := manager.ReclaimCTA(admitted.ID); err == nil {
		t.Fatal("running CTA was reclaimed early")
	}
	beforeCTA, _ := ctas.Snapshot(admitted.ID)
	beforeSlots, _ := manager.Slots()
	var ownerBefore [isa.FrozenWarpCount]state.WarpSnapshot
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		ownerBefore[id], _ = owners[id].Snapshot()
	}
	if _, err := manager.AdmitCTA(dynamicCTA(4, 12, 64)); err == nil {
		t.Fatal("admission without three free warps succeeded")
	}
	noLMEM := dynamicCTA(4, 1, isa.FrozenLocalMemSize)
	if _, err := manager.AdmitCTA(noLMEM); err == nil {
		t.Fatal("admission without a free LMEM extent succeeded")
	}
	afterCTA, _ := ctas.Snapshot(admitted.ID)
	afterSlots, _ := manager.Slots()
	if !reflect.DeepEqual(afterCTA, beforeCTA) || !reflect.DeepEqual(afterSlots, beforeSlots) {
		t.Fatal("failed resource admission changed CTA or scheduler state")
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		after, _ := owners[id].Snapshot()
		if after != ownerBefore[id] {
			t.Fatalf("failed resource admission changed warp %d", id)
		}
	}
}

func TestDynamicCTAWarpStepYWrapsWithoutXCarry(t *testing.T) {
	manager, _, _, _ := makeDynamicCTACore(t, catalogWord(t, "tmc"))
	config := dynamicCTA(0, 5, 64)
	config.BlockDimensions = [3]uint32{8, 2, 2}
	config.WarpStep = [3]uint32{0, 2, 0}
	admitted, err := manager.AdmitCTA(config)
	if err != nil {
		t.Fatal(err)
	}
	wantWarp1 := [3]isa.LaneValues{{0, 1, 2, 3}, {0, 0, 0, 0}, {1, 1, 1, 1}}
	if admitted.Members[1].ThreadCoordinates != wantWarp1 {
		t.Fatalf("independent WarpStep.Y wrap mismatch: got=%v want=%v", admitted.Members[1].ThreadCoordinates, wantWarp1)
	}
}

func TestDynamicCTAClusterAdmissionIsAllOrNothing(t *testing.T) {
	manager, ctas, owners, _ := makeDynamicCTACore(t, catalogWord(t, "tmc"))
	cluster := []core.CTAConfig{dynamicCTA(0, 1, 64), dynamicCTA(1, 1, 64)}
	for index := range cluster {
		cluster[index].ClusterDimensions = [3]uint32{2, 1, 1}
		cluster[index].ClusterSize = 2
		cluster[index].IsFirstOfCluster = index == 0
		cluster[index].ParameterAddress = cluster[0].ParameterAddress
	}
	invalid := append([]core.CTAConfig(nil), cluster...)
	invalid[1].Entry++
	if _, err := manager.AdmitCTACluster(invalid); err == nil {
		t.Fatal("cluster with invalid second CTA was partially admitted")
	}
	if completions, err := manager.CTACompletions(); err != nil || len(completions) != 0 {
		t.Fatalf("failed cluster retained residency: %+v err=%v", completions, err)
	}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		slot, _ := manager.Slot(id)
		snapshot, _ := owners[id].Snapshot()
		if slot.Lifecycle != core.WarpInactive || slot.Participated || ctas.HasWarp(id) ||
			snapshot.Lifecycle() != state.WarpInactive || snapshot.ActiveMask() != 0 {
			t.Fatalf("failed cluster changed warp %d slot=%+v state=%+v", id, slot, snapshot)
		}
	}
	admitted, err := manager.AdmitCTACluster(cluster)
	if err != nil || len(admitted) != 2 || admitted[0].Members[0].WarpID != 0 || admitted[1].Members[0].WarpID != 1 {
		t.Fatalf("valid cluster admission=%+v err=%v", admitted, err)
	}
}

func TestDynamicCTAOutOfOrderReclaimLMEMIsolationPendingAndWarpReuse(t *testing.T) {
	manager, ctas, owners, routes := makeDynamicCTACore(t, catalogWord(t, "tmc"))
	first, err := manager.AdmitCTA(dynamicCTA(0, 1, 64))
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := dynamicCTA(1, 1, 64)
	second, err := manager.AdmitCTA(secondConfig)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != 0 || first.Members[0].WarpID != 0 || second.ID != 1 || second.Members[0].WarpID != 1 {
		t.Fatalf("unexpected concurrent residency: first=%+v second=%+v", first, second)
	}
	address := isa.FrozenLocalMemBase + 8
	if err := routes[0].Write(address, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := routes[1].Write(address, []byte{9, 8, 7, 6}); err != nil {
		t.Fatal(err)
	}
	read0, read1 := make([]byte, 4), make([]byte, 4)
	_ = routes[0].Read(address, read0)
	_ = routes[1].Read(address, read1)
	if !bytes.Equal(read0, []byte{1, 2, 3, 4}) || !bytes.Equal(read1, []byte{9, 8, 7, 6}) {
		t.Fatalf("resident LMEM aliased: %v/%v", read0, read1)
	}

	if err := manager.Block(0, core.BlockExplicit); err != nil {
		t.Fatal(err)
	}
	step, err := manager.Step(state.ReadContext{})
	if err != nil || step.WarpID != 1 || step.NextLifecycle != core.WarpFinished || step.CTAID != second.ID {
		t.Fatalf("out-of-order completion step=%+v err=%v", step, err)
	}
	finishedSecond, _ := owners[1].Snapshot()
	pending := true
	if err := manager.SetPendingWorkProvider(core.PendingWorkFunc(func(warpID uint8) (bool, error) {
		return warpID == 1 && pending, nil
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ReclaimCTA(second.ID); err == nil {
		t.Fatal("CTA with pending functional work was reclaimed")
	}
	if _, err := ctas.Snapshot(second.ID); err != nil || !ctas.HasWarp(1) {
		t.Fatalf("rejected reclaim changed membership: %v", err)
	}
	pending = false
	reclaimed, err := manager.ReclaimCTA(second.ID)
	if err != nil || reclaimed.ID != second.ID {
		t.Fatalf("out-of-order reclaim=%+v err=%v", reclaimed, err)
	}
	if _, err := ctas.Snapshot(second.ID); err == nil || ctas.HasWarp(1) {
		t.Fatal("reclaim retained CTA or reverse membership")
	}
	if err := routes[1].Read(address, make([]byte, 4)); err == nil {
		t.Fatal("reclaimed warp retained LMEM access")
	}
	if current, err := ctas.Snapshot(first.ID); err != nil || current.BlockID != first.BlockID {
		t.Fatalf("out-of-order reclaim disturbed resident CTA: %+v err=%v", current, err)
	}

	reuseConfig := dynamicCTA(2, 1, 64)
	reuseConfig.StartupPC = 0x500 // ignored for a warp already used in this kernel
	reuseConfig.Entry = 0x640
	reuseConfig.ParameterAddress = 0xa000
	reused, err := manager.AdmitCTA(reuseConfig)
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != second.ID || reused.Members[0].WarpID != 1 {
		t.Fatalf("freed CTA/warp slots were not reused: %+v", reused)
	}
	reusedState, _ := owners[1].Snapshot()
	if reusedState.PC() != finishedSecond.PC()-state.FrozenCTAReentryBytes || reusedState.PC() == reuseConfig.StartupPC ||
		reusedState.TrapCSRs().MScratch != reuseConfig.ParameterAddress || reusedState.ActiveMask() != 1 {
		t.Fatalf("wrong reused warp state: finishedPC=%#x reused=%+v", finishedSecond.PC(), reusedState)
	}
	view, err := ctas.ViewForWarp(1)
	if err != nil || view.Entry != reuseConfig.Entry || view.ParameterAddress != reuseConfig.ParameterAddress {
		t.Fatalf("reused CTA context=%+v err=%v", view, err)
	}
	if err := routes[1].Write(address, []byte{5, 5, 5, 5}); err != nil {
		t.Fatal(err)
	}
	read0 = make([]byte, 4)
	read1 = make([]byte, 4)
	_ = routes[0].Read(address, read0)
	_ = routes[1].Read(address, read1)
	if !bytes.Equal(read0, []byte{1, 2, 3, 4}) || !bytes.Equal(read1, []byte{5, 5, 5, 5}) {
		t.Fatalf("reused allocation aliased resident CTA: %v/%v", read0, read1)
	}
}
