package warp_test

import (
	"bytes"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
)

func instructionField(word uint32, shift uint, value uint8) uint32 {
	return word&^(uint32(0x1f)<<shift) | uint32(value&0x1f)<<shift
}

func simtStreamFixture(t *testing.T) (*warp.Warp, *state.WarpState, *memory.Memory) {
	t.Helper()
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	integerInitial := map[uint8]isa.LaneValues{
		1:  {10, 20, 30, 40},
		2:  {1, 1, 0, 0},
		3:  {1 | 3<<6, 1 | 3<<6, 1 | 3<<6, 1 | 3<<6},
		4:  {0x101, 0x202, 0x303, 0x404},
		5:  {100, 200, 300, 400},
		9:  {0x90, 0x91, 0x92, 0x93},
		11: {0xa1, 0xa2, 0xa3, 0xa4},
		12: {0xb1, 0xb2, 0xb3, 0xb4},
		20: {0x300, 0x304, 0x308, 0x30c},
		21: {0, 0, 0, uint32(isa.AllLanes)},
		22: {0, 0, 0, 0},
	}
	for register, values := range integerInitial {
		for lane, value := range values {
			initial.Lanes[lane].GPR[register] = value
		}
	}
	f1 := isa.LaneValues{0x3f800000, 0x40000000, 0x40400000, 0x40800000}
	for lane, value := range f1 {
		initial.Lanes[lane].FPR[1] = value
		initial.Lanes[lane].FPR[2] = 0x3f800000
		initial.Lanes[lane].FPR[3] = 0xdead0000 + uint32(lane)
	}
	owner := newState(t, initial)
	m := newMemory(t)

	tmcKeep := customOperation(t, "tmc", func(word uint32) uint32 {
		return instructionField(word, 15, 21)
	})
	tmcTerminate := customOperation(t, "tmc", func(word uint32) uint32 {
		return instructionField(word, 15, 22)
	})
	split := customOperation(t, "split", func(word uint32) uint32 {
		word = instructionField(word, 7, 23)
		word = instructionField(word, 15, 2)
		return word &^ (uint32(1) << 20) // non-negated predicate
	})
	join := customOperation(t, "join", func(word uint32) uint32 {
		return instructionField(word, 15, 23)
	})
	vote := customOperation(t, "vote.ballot", func(word uint32) uint32 {
		return instructionField(instructionField(word, 7, 6), 15, 2)
	})
	shuffle := customOperation(t, "shfl.down", func(word uint32) uint32 {
		word = instructionField(word, 7, 7)
		word = instructionField(word, 15, 10)
		return instructionField(word, 20, 3)
	})
	gather := customOperation(t, "wgather", func(word uint32) uint32 {
		word = instructionField(word, 7, 9)
		word = instructionField(word, 15, 10)
		word = instructionField(word, 20, 11)
		word = instructionField(word, 27, 12)
		return word &^ (uint32(3) << 25) // nominal source lane 0
	})

	program := map[uint32]uint32{
		0x100: 0x00108513,             // addi x10,x1,1 (all lanes)
		0x104: 0x002081d3,             // fadd.s f3,f1,f2 (all lanes)
		0x108: storeWord(0, 4, 20, 2), // sw x4,0(x20) (four addresses)
		0x10c: tmcKeep.Word,           // explicit all-lane mask control
		0x110: split.Word,             // lanes 0/1 then, lanes 2/3 deferred
		0x114: 0x00128293,             // addi x5,x5,1 (each path once)
		0x118: vote.Word,              // vote.ballot x6,x2
		0x11c: shuffle.Word,           // shfl.down x7,x10,x3
		0x120: gather.Word,            // wgather x9,x10,x11,x12
		0x124: join.Word,              // mark deferred path, then pop
		0x128: loadWord(0, 20, 2, 8),  // lw x8,0(x20) after reconvergence
		0x12c: tmcTerminate.Word,      // zero mask termination
	}
	for pc, word := range program {
		putWord(t, m, pc, word)
	}
	return memoryExecutor(t, owner, m), owner, m
}

func TestRunCompleteFourLaneSIMTStreamAndTrace(t *testing.T) {
	executor, owner, m := simtStreamFixture(t)
	var records []warp.TraceRecord
	result := executor.Run(warp.RunOptions{
		StepBudget: 32,
		Trace: warp.TraceFunc(func(record warp.TraceRecord) {
			records = append(records, record)
		}),
	})
	if result.Outcome != warp.RunFinished || result.Attempts != 18 || result.Retired != 17 ||
		result.Last == nil || result.Last.Outcome != warp.OutcomeFinished || result.Err != nil {
		t.Fatalf("SIMT Run result=%+v", result)
	}

	wantInteger := map[uint8]isa.LaneValues{
		5:  {101, 201, 301, 401},
		6:  {0b0011, 0b0011, 0, 0},
		7:  {21, 21, 41, 41},
		8:  {0x101, 0x202, 0x303, 0x404},
		9:  {0x90, 41, 0xa4, 0xb4},
		10: {11, 21, 31, 41},
		23: {0, 0, 0, 0},
	}
	for register, want := range wantInteger {
		got, err := owner.ReadRegister(isa.Register{File: isa.Integer, Index: register})
		if err != nil || got != want {
			t.Fatalf("x%d=%#x want=%#x err=%v", register, got, want, err)
		}
	}
	f3, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 3})
	if f3 != (isa.LaneValues{0x40000000, 0x40400000, 0x40800000, 0x40a00000}) {
		t.Fatalf("f3=%#x", f3)
	}
	for lane, want := range (isa.LaneValues{0x101, 0x202, 0x303, 0x404}) {
		got, err := m.Load32(0x300 + uint32(lane)*4)
		if err != nil || got != want {
			t.Fatalf("memory lane %d=%#x want=%#x err=%v", lane, got, want, err)
		}
	}
	after, _ := owner.Snapshot()
	stackRecord, _ := after.DivergenceRecord(0)
	if after.PC() != 0x130 || after.ActiveMask() != 0 || after.Lifecycle() != state.WarpInactive ||
		after.DivergenceWritePointer() != 0 || stackRecord.Valid {
		t.Fatalf("final SIMT state=%+v stack=%+v", after, stackRecord)
	}

	if len(records) != 18 {
		t.Fatalf("trace records=%d", len(records))
	}
	split := records[4]
	firstJoin := records[9]
	secondJoin := records[14]
	termination := records[16]
	finished := records[17]
	if split.ActiveMask != isa.AllLanes || split.NextActiveMask != 0b0011 ||
		split.DivergencePointer != 0 || split.NextDivergencePointer != 1 || split.NextLifecycle != state.WarpRunning ||
		firstJoin.ActiveMask != 0b0011 || firstJoin.NextActiveMask != 0b1100 || firstJoin.PC != 0x124 || firstJoin.NextPC != 0x114 ||
		firstJoin.DivergencePointer != 1 || firstJoin.NextDivergencePointer != 1 ||
		secondJoin.ActiveMask != 0b1100 || secondJoin.NextActiveMask != isa.AllLanes || secondJoin.NextPC != 0x128 ||
		secondJoin.DivergencePointer != 1 || secondJoin.NextDivergencePointer != 0 ||
		termination.ActiveMask != isa.AllLanes || termination.NextActiveMask != 0 || termination.NextLifecycle != state.WarpInactive ||
		finished.ActiveMask != 0 || finished.Lifecycle != state.WarpInactive || finished.RawValid || finished.Outcome != warp.OutcomeFinished {
		t.Fatalf("SIMT trace split=%+v firstJoin=%+v secondJoin=%+v termination=%+v finished=%+v",
			split, firstJoin, secondJoin, termination, finished)
	}
}

func TestRunSIMTTraceMutationCannotChangeExecution(t *testing.T) {
	plainExecutor, plainOwner, plainMemory := simtStreamFixture(t)
	plain := plainExecutor.Run(warp.RunOptions{StepBudget: 32})
	plainState, _ := plainOwner.Snapshot()
	plainBytes := plainMemory.Snapshot()

	mutatingExecutor, mutatingOwner, mutatingMemory := simtStreamFixture(t)
	mutating := mutatingExecutor.Run(warp.RunOptions{
		StepBudget: 32,
		Trace: warp.TraceFunc(func(record warp.TraceRecord) {
			record.ActiveMask = 0
			record.NextActiveMask = isa.AllLanes
			record.Lifecycle = state.WarpInactive
			record.NextDivergencePointer = state.FrozenDivergenceDepth
			if record.Decoded != nil {
				record.Decoded.Name = "corrupted"
				if len(record.Decoded.Sources) != 0 {
					record.Decoded.Sources[0].Index = 31
				}
			}
			for _, effects := range []*isa.InstructionEffects{record.IssuedEffects, record.Effects} {
				if effects == nil {
					continue
				}
				if len(effects.RegisterWrites) != 0 {
					effects.RegisterWrites[0].Mask = 0
					effects.RegisterWrites[0].Values[0] = 0xffffffff
				}
				if len(effects.WarpMasks) != 0 {
					effects.WarpMasks[0].Mask = 0
				}
				if effects.Divergence != nil {
					effects.Divergence.StackPointer = state.FrozenDivergenceDepth
					effects.Divergence.ExecuteMask = 0
				}
			}
		}),
	})
	mutatingState, _ := mutatingOwner.Snapshot()
	if mutating.Outcome != plain.Outcome || mutating.Attempts != plain.Attempts || mutating.Retired != plain.Retired ||
		mutating.Last == nil || mutating.Last.Outcome != warp.OutcomeFinished || mutatingState != plainState ||
		!bytes.Equal(mutatingMemory.Snapshot(), plainBytes) {
		t.Fatalf("trace mutation changed Run plain=%+v mutating=%+v state=%t memory=%t",
			plain, mutating, mutatingState != plainState, !bytes.Equal(mutatingMemory.Snapshot(), plainBytes))
	}
}

func TestRunStopsAtFirstFutureOwnerCustomInstruction(t *testing.T) {
	for _, name := range []string{"wspawn", "wsync", "bar", "bar.arrive", "bar.wait"} {
		t.Run(name, func(t *testing.T) {
			initial := initialWarp()
			initial.ActiveMask = isa.AllLanes
			owner := newState(t, initial)
			m := newMemory(t)
			putWord(t, m, 0x100, 0x00100093) // addi x1,x0,1
			putWord(t, m, 0x104, customOperation(t, name, nil).Word)
			result := memoryExecutor(t, owner, m).Run(warp.RunOptions{StepBudget: 8})
			values, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
			if result.Outcome != warp.RunDeferred || result.Attempts != 2 || result.Retired != 1 ||
				result.Last == nil || result.Last.PC != 0x104 || result.Last.NextPC != 0x104 ||
				values != (isa.LaneValues{1, 1, 1, 1}) {
				t.Fatalf("future-owner Run %s result=%+v x1=%#x", name, result, values)
			}
		})
	}
}
