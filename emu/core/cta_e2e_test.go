package core_test

import (
	"encoding/binary"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
)

func loadWordForCore(rd, rs1 uint32) uint32 { return rs1<<15 | 2<<12 | rd<<7 | 0x03 }

type ctaE2EResult struct {
	run        core.RunResult
	states     [isa.FrozenWarpCount]state.WarpSnapshot
	local      []byte
	completion core.CTACompletionSnapshot
	trace      []core.TraceRecord
}

func runSharedCTAProgram(t *testing.T, mutateTrace bool) ctaE2EResult {
	t.Helper()
	ctas := core.NewCTAManager()
	config := ctaConfig(0, []uint8{0, 1}, 6, 128)
	admitted, err := ctas.Admit(config)
	if err != nil {
		t.Fatal(err)
	}
	program, err := memory.New(0x300)
	if err != nil {
		t.Fatal(err)
	}
	bar := catalogWord(t, "bar")
	tmc := catalogWord(t, "tmc")
	for _, entry := range []uint32{0x100, 0x200} {
		words := []uint32{
			csrReadWord(0xcd3, 14),
			storeWordForCore(11, 10),
			bar,
			loadWordForCore(12, 13),
			bar,
			addiWord(1, 0, 0),
			tmc,
		}
		for index, word := range words {
			writeProgramWord(t, program, entry+uint32(index)*4, word)
		}
	}

	var owners [isa.FrozenWarpCount]*state.WarpState
	var routes [2]*core.CTAMemory
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initial := initial(id, id < 2)
		if id < 2 {
			initial.PC = 0x100 + uint32(id)*0x100
			if id == 1 {
				initial.ActiveMask = 0b0011 // six-thread CTA: two valid lanes in rank 1
			}
			for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
				initial.Lanes[lane].GPR[1] = 5 << 8 // AddressWarp 0, barrier ID 5
				initial.Lanes[lane].GPR[2] = 2
				initial.Lanes[lane].GPR[10] = admitted.LocalMemory.Address + uint32(id)*16 + uint32(lane)*4
				initial.Lanes[lane].GPR[11] = 0xc0000000 | uint32(id)<<8 | uint32(lane)
				initial.Lanes[lane].GPR[13] = admitted.LocalMemory.Address + uint32(1-id)*16 + uint32(lane)*4
			}
		}
		owners[id], err = state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		if id < 2 {
			routes[id], err = core.NewCTAMemory(ctas, id, program)
			if err == nil {
				executors[id], err = warp.NewWithServices(owners[id], program, routes[id])
			}
		} else {
			executors[id], err = warp.New(owners[id], program)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := core.NewWithCTAManager(executors, ctas)
	if err != nil {
		t.Fatal(err)
	}
	var records []core.TraceRecord
	run := manager.Run(core.RunOptions{StepBudget: 64, Trace: core.TraceFunc(func(record core.TraceRecord) {
		if mutateTrace {
			record.CTAID = 99
			if record.Barrier != nil {
				record.Barrier.After.Phase = !record.Barrier.After.Phase
				record.Barrier.Released = isa.AllWarps
			}
			if record.CTACompletion != nil && len(record.CTACompletion.Members) != 0 {
				record.CTACompletion.Members[0].OwnerFinished = false
			}
			if record.WarpResult.Effects != nil && len(record.WarpResult.Effects.Barriers) != 0 {
				record.WarpResult.Effects.Barriers[0].ID = 0
			}
			return
		}
		records = append(records, record)
	})})
	result := ctaE2EResult{run: run, trace: records}
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		result.states[id], _ = owners[id].Snapshot()
	}
	result.local = make([]byte, admitted.LocalMemory.Size)
	if err := routes[0].Read(admitted.LocalMemory.Address, result.local); err != nil {
		t.Fatal(err)
	}
	result.completion, err = manager.CTACompletion(0)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMultiWarpCTASharedLMEMBarrierTraceAndCompletion(t *testing.T) {
	baseline := runSharedCTAProgram(t, false)
	if baseline.run.Outcome != core.RunComplete || baseline.run.Attempts != 14 || baseline.run.Retired != 14 ||
		len(baseline.run.CTACompletions) != 1 || !baseline.run.CTACompletions[0].Complete || !baseline.completion.Complete {
		t.Fatalf("CTA run=%+v completion=%+v", baseline.run, baseline.completion)
	}
	blocked, released, phases := 0, 0, []bool{}
	for _, record := range baseline.trace {
		if !record.CTAValid || record.CTAID != 0 || record.CTACompletion == nil {
			t.Fatalf("trace lacks CTA correlation: %+v", record)
		}
		if record.Barrier == nil || !record.Barrier.Accepted {
			continue
		}
		if record.Barrier.Blocked {
			blocked++
		}
		if record.Barrier.Released != 0 {
			released++
			phases = append(phases, record.Barrier.After.Phase)
		}
	}
	if blocked != 2 || released != 2 || !reflect.DeepEqual(phases, []bool{true, false}) {
		t.Fatalf("barrier trace blocked=%d released=%d phases=%v", blocked, released, phases)
	}
	for id := uint8(0); id < 2; id++ {
		snapshot := baseline.states[id]
		if snapshot.Lifecycle() != state.WarpInactive || snapshot.ActiveMask() != 0 {
			t.Fatalf("warp %d did not finish: %+v", id, snapshot)
		}
		threadX, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 14})
		loaded, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 12})
		active := uint8(4)
		if id == 1 {
			active = 2
		}
		for lane := uint8(0); lane < active; lane++ {
			if threadX[lane] != uint32(id)*4+uint32(lane) {
				t.Fatalf("warp %d lane %d thread-x=%d", id, lane, threadX[lane])
			}
			want := uint32(0xc0000000) | uint32(1-id)<<8 | uint32(lane)
			if id == 0 && lane >= 2 {
				want = 0 // inactive lanes in the partial member never stored
			}
			if loaded[lane] != want {
				t.Fatalf("warp %d lane %d shared load=%#x want=%#x", id, lane, loaded[lane], want)
			}
		}
	}
	for id := uint32(0); id < 2; id++ {
		active := uint32(4)
		if id == 1 {
			active = 2
		}
		for lane := uint32(0); lane < active; lane++ {
			offset := id*16 + lane*4
			got := binary.LittleEndian.Uint32(baseline.local[offset : offset+4])
			want := uint32(0xc0000000) | id<<8 | lane
			if got != want {
				t.Fatalf("LMEM member %d lane %d=%#x want=%#x", id, lane, got, want)
			}
		}
	}

	mutated := runSharedCTAProgram(t, true)
	if mutated.run.Outcome != baseline.run.Outcome || mutated.run.Attempts != baseline.run.Attempts ||
		!reflect.DeepEqual(mutated.states, baseline.states) || !reflect.DeepEqual(mutated.local, baseline.local) ||
		!reflect.DeepEqual(mutated.completion, baseline.completion) {
		t.Fatal("trace mutation changed CTA, barrier, Warp, scheduler, or LMEM canonical state")
	}
}

func TestTwoCTAsSameBarrierIDAndVirtualLMEMAddressRemainIsolated(t *testing.T) {
	ctas := core.NewCTAManager()
	cta0, err := ctas.Admit(ctaConfig(0, []uint8{0, 1}, 8, 64))
	if err != nil {
		t.Fatal(err)
	}
	cta1, err := ctas.Admit(ctaConfig(1, []uint8{2, 3}, 8, 64))
	if err != nil {
		t.Fatal(err)
	}
	if cta0.LocalMemory.Address != cta1.LocalMemory.Address || cta0.LocalMemory.Address != isa.FrozenLocalMemBase {
		t.Fatalf("CTAs do not share one LMEM virtual base: CTA0=%+v CTA1=%+v", cta0.LocalMemory, cta1.LocalMemory)
	}
	program, _ := memory.New(0x400)
	bar, tmc := catalogWord(t, "bar"), catalogWord(t, "tmc")
	var owners [isa.FrozenWarpCount]*state.WarpState
	var routes [isa.FrozenWarpCount]*core.CTAMemory
	executors := make([]*warp.Warp, isa.FrozenWarpCount)
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		entry := uint32(0x100) + uint32(id)*0x80
		for index, word := range []uint32{storeWordForCore(11, 10), bar, loadWordForCore(12, 10), addiWord(1, 0, 0), tmc} {
			writeProgramWord(t, program, entry+uint32(index)*4, word)
		}
		initial := initial(id, true)
		initial.PC = entry
		ctaID, addressWarp, rank, base := uint32(0), uint8(0), uint32(id), cta0.LocalMemory.Address
		if id >= 2 {
			ctaID, addressWarp, rank, base = 1, 2, uint32(id-2), cta1.LocalMemory.Address
		}
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			initial.Lanes[lane].GPR[1] = uint32(addressWarp) | 6<<8
			initial.Lanes[lane].GPR[2] = 2
			initial.Lanes[lane].GPR[10] = base + rank*16 + uint32(lane)*4
			initial.Lanes[lane].GPR[11] = 0xd0000000 | ctaID<<16 | uint32(id)<<8 | uint32(lane)
		}
		owners[id], err = state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
		routes[id], err = core.NewCTAMemory(ctas, id, program)
		if err == nil {
			executors[id], err = warp.NewWithServices(owners[id], program, routes[id])
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	manager, err := core.NewWithCTAManager(executors, ctas)
	if err != nil {
		t.Fatal(err)
	}
	var barrierKeys []core.BarrierKey
	run := manager.Run(core.RunOptions{StepBudget: 64, Trace: core.TraceFunc(func(record core.TraceRecord) {
		if record.Barrier != nil && record.Barrier.Accepted {
			barrierKeys = append(barrierKeys, record.Barrier.Key)
		}
	})})
	completions, _ := manager.CTACompletions()
	if run.Outcome != core.RunComplete || run.Attempts != 20 || len(completions) != 2 ||
		!completions[0].Complete || !completions[1].Complete {
		t.Fatalf("two-CTA run=%+v completions=%+v", run, completions)
	}
	found := [2]int{}
	for _, key := range barrierKeys {
		if key.ID != 6 || key.CTAID > 1 {
			t.Fatalf("unexpected barrier key %+v", key)
		}
		found[key.CTAID]++
	}
	if found != [2]int{2, 2} {
		t.Fatalf("barrier namespaces observed=%v keys=%v", found, barrierKeys)
	}
	for ctaIndex, admitted := range []core.CTASnapshot{cta0, cta1} {
		image := make([]byte, admitted.LocalMemory.Size)
		member := uint8(ctaIndex * 2)
		if err := routes[member].Read(admitted.LocalMemory.Address, image); err != nil {
			t.Fatal(err)
		}
		for rank := uint32(0); rank < 2; rank++ {
			wid := uint32(ctaIndex*2) + rank
			for lane := uint32(0); lane < isa.FrozenLaneCount; lane++ {
				offset := rank*16 + lane*4 // identical numeric virtual addresses in both CTAs
				got := binary.LittleEndian.Uint32(image[offset : offset+4])
				want := uint32(0xd0000000) | uint32(ctaIndex)<<16 | wid<<8 | lane
				if got != want {
					t.Fatalf("CTA%d offset %#x=%#x want=%#x", ctaIndex, offset, got, want)
				}
			}
		}
	}
	phase0, _ := manager.Barrier(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 6})
	phase1, _ := manager.Barrier(core.BarrierKey{CTAID: 1, AddressWarp: 2, ID: 6})
	if !phase0.Phase || !phase1.Phase {
		t.Fatalf("isolated phases CTA0=%+v CTA1=%+v", phase0, phase1)
	}
}
