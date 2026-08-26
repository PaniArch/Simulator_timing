package warp_test

import (
	"encoding/binary"
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/warp"
)

func customOperation(t *testing.T, name string, mutate func(uint32) uint32) isa.Decoded {
	t.Helper()
	word := catalogWord(t, name)
	if mutate != nil {
		word = mutate(word)
	}
	decoded, err := isa.Decode(word)
	if err != nil {
		t.Fatalf("decode %s word %#x: %v", name, word, err)
	}
	if decoded.Name != name {
		t.Fatalf("decoded %s, want %s", decoded.Name, name)
	}
	return decoded
}

func setInteger(initial *state.WarpInitial, register isa.Register, lane uint8, value uint32) {
	if register.File != isa.Integer {
		panic("custom test expected integer register")
	}
	initial.Lanes[lane].GPR[register.Index] = value
}

func integerVector(t *testing.T, owner *state.WarpState, register isa.Register) isa.LaneValues {
	t.Helper()
	values, err := owner.ReadRegister(register)
	if err != nil {
		t.Fatal(err)
	}
	return values
}

func TestStepTMCAndPredicateCommitCanonicalMaskAndLifecycle(t *testing.T) {
	t.Run("tmc-partial-then-zero", func(t *testing.T) {
		operation := customOperation(t, "tmc", nil)
		initial := initialWarp()
		initial.ActiveMask = 0b0101
		setInteger(&initial, operation.Sources[0], 2, 0b1010)
		setInteger(&initial, operation.Sources[0], 3, 0)
		owner := newState(t, initial)
		m := newMemory(t)
		putWord(t, m, 0x100, operation.Word)
		putWord(t, m, 0x104, operation.Word)
		w := memoryExecutor(t, owner, m)

		first := w.Step(state.ReadContext{})
		requireOutcome(t, first, warp.OutcomeRetired)
		afterFirst, _ := owner.Snapshot()
		if afterFirst.ActiveMask() != 0b1010 || afterFirst.Lifecycle() != state.WarpRunning || afterFirst.PC() != 0x104 {
			t.Fatalf("partial TMC result=%+v state=%+v", first, afterFirst)
		}

		second := w.Step(state.ReadContext{})
		requireOutcome(t, second, warp.OutcomeRetired)
		afterSecond, _ := owner.Snapshot()
		if afterSecond.ActiveMask() != 0 || afterSecond.Lifecycle() != state.WarpInactive || afterSecond.PC() != 0x108 {
			t.Fatalf("zero TMC result=%+v state=%+v", second, afterSecond)
		}
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeFinished)
	})

	t.Run("predicate-selected-and-fallback", func(t *testing.T) {
		operation := customOperation(t, "pred", nil)
		selected, rejected := uint32(1), uint32(0)
		if operation.ConditionNegated {
			selected, rejected = rejected, selected
		}

		initial := initialWarp()
		initial.ActiveMask = 0b1101
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Sources[0], lane, rejected)
		}
		setInteger(&initial, operation.Sources[0], 0, selected)
		setInteger(&initial, operation.Sources[0], 3, selected)
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		after, _ := owner.Snapshot()
		if after.ActiveMask() != 0b1001 || after.PC() != 0x104 {
			t.Fatalf("selected PRED result=%+v state=%+v", result, after)
		}

		initial = initialWarp()
		initial.ActiveMask = 0b1101
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Sources[0], lane, rejected)
		}
		setInteger(&initial, operation.Sources[1], 3, 0b0011)
		owner = newState(t, initial)
		result = executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		after, _ = owner.Snapshot()
		if after.ActiveMask() != 0b0011 || after.Lifecycle() != state.WarpRunning {
			t.Fatalf("fallback PRED result=%+v state=%+v", result, after)
		}

		initial = initialWarp()
		initial.ActiveMask = 0b1101
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Sources[0], lane, rejected)
			setInteger(&initial, operation.Sources[1], lane, 0)
		}
		owner = newState(t, initial)
		result = executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		after, _ = owner.Snapshot()
		if after.ActiveMask() != 0 || after.Lifecycle() != state.WarpInactive || after.PC() != 0x104 {
			t.Fatalf("zero PRED result=%+v state=%+v", result, after)
		}
	})
}

func TestStepUniformSplitDoesNotCreateDivergenceRecord(t *testing.T) {
	operation := customOperation(t, "split", nil)
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	selected := uint32(1)
	if operation.ConditionNegated {
		selected = 0
	}
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		setInteger(&initial, operation.Sources[0], lane, selected)
	}
	owner := newState(t, initial)
	result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeRetired)
	after, _ := owner.Snapshot()
	if after.DivergenceWritePointer() != 0 || after.ActiveMask() != isa.AllLanes || after.PC() != 0x104 {
		t.Fatalf("uniform SPLIT result=%+v state=%+v", result, after)
	}
	values := integerVector(t, owner, operation.Destinations[0])
	if values != (isa.LaneValues{}) {
		t.Fatalf("uniform SPLIT pointer write=%#x", values)
	}
}

func TestRunDivergentSplitAndTwoJoinStepsReconverge(t *testing.T) {
	split := customOperation(t, "split", nil)
	join := customOperation(t, "join", func(word uint32) uint32 {
		return word&^(uint32(0x1f)<<15) | uint32(split.Destinations[0].Index)<<15
	})
	initial := initialWarp()
	initial.ActiveMask = 0b0101
	selected, rejected := uint32(1), uint32(0)
	if split.ConditionNegated {
		selected, rejected = rejected, selected
	}
	setInteger(&initial, split.Sources[0], 0, selected)
	setInteger(&initial, split.Sources[0], 2, rejected)
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, split.Word)
	putWord(t, m, 0x104, join.Word)
	putWord(t, m, 0x108, 0xffffffff)

	var records []warp.TraceRecord
	result := memoryExecutor(t, owner, m).Run(warp.RunOptions{
		StepBudget: 8,
		Trace: warp.TraceFunc(func(record warp.TraceRecord) {
			records = append(records, record)
		}),
	})
	if result.Outcome != warp.RunFault || result.Attempts != 4 || result.Retired != 3 ||
		result.Last == nil || result.Last.Fault.Kind != warp.FaultIllegalInstruction {
		t.Fatalf("divergent Run=%+v", result)
	}
	after, _ := owner.Snapshot()
	record, _ := after.DivergenceRecord(0)
	if after.DivergenceWritePointer() != 0 || record.Valid || after.ActiveMask() != 0b0101 || after.PC() != 0x108 {
		t.Fatalf("reconverged state=%+v record=%+v", after, record)
	}
	if len(records) != 4 || records[0].Effects.Divergence == nil || !records[0].Effects.Divergence.Push ||
		records[1].PC != 0x104 || !records[1].Effects.Divergence.MarkElseVisited || records[1].NextPC != 0x104 ||
		records[2].PC != 0x104 || !records[2].Effects.Divergence.Pop || records[2].NextPC != 0x108 {
		t.Fatalf("divergence trace=%+v", records)
	}
}

func TestStepCrossLaneVoteAndShuffleInstructionsRetire(t *testing.T) {
	votes := []struct {
		name string
		want uint32
	}{
		{name: "vote.all", want: 0},
		{name: "vote.any", want: 1},
		{name: "vote.uni", want: 0},
		{name: "vote.ballot", want: 0b1001},
	}
	for _, test := range votes {
		t.Run(test.name, func(t *testing.T) {
			operation := customOperation(t, test.name, nil)
			initial := initialWarp()
			initial.ActiveMask = 0b1101
			for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
				setInteger(&initial, operation.Destinations[0], lane, uint32(0xd0+lane))
			}
			predicates := isa.LaneValues{1, 1, 0, 1}
			for lane, value := range predicates {
				setInteger(&initial, operation.Sources[0], uint8(lane), value)
			}
			inactiveWant := initial.Lanes[1].GPR[operation.Destinations[0].Index]
			owner := newState(t, initial)
			result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
			requireOutcome(t, result, warp.OutcomeRetired)
			got := integerVector(t, owner, operation.Destinations[0])
			if got[0] != test.want || got[2] != test.want || got[3] != test.want || got[1] != inactiveWant ||
				result.Effects.RegisterWrites[0].Mask != 0b1101 {
				t.Fatalf("%s result=%+v values=%#x", test.name, result, got)
			}
		})
	}

	shuffles := []struct {
		name string
		ctrl uint32
		want isa.LaneValues
	}{
		{name: "shfl.up", ctrl: 1 | 3<<6, want: isa.LaneValues{10, 10, 20, 30}},
		{name: "shfl.down", ctrl: 1 | 3<<6, want: isa.LaneValues{20, 30, 40, 40}},
		{name: "shfl.bfly", ctrl: 1 | 3<<6, want: isa.LaneValues{20, 10, 40, 30}},
		{name: "shfl.idx", ctrl: 2 | 3<<6, want: isa.LaneValues{30, 30, 30, 30}},
	}
	for _, test := range shuffles {
		t.Run(test.name, func(t *testing.T) {
			operation := customOperation(t, test.name, nil)
			initial := initialWarp()
			initial.ActiveMask = isa.AllLanes
			for lane, value := range (isa.LaneValues{10, 20, 30, 40}) {
				setInteger(&initial, operation.Sources[0], uint8(lane), value)
				setInteger(&initial, operation.Sources[1], uint8(lane), test.ctrl)
			}
			owner := newState(t, initial)
			result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
			requireOutcome(t, result, warp.OutcomeRetired)
			if got := integerVector(t, owner, operation.Destinations[0]); got != test.want {
				t.Fatalf("%s result=%+v values=%#x want=%#x", test.name, result, got, test.want)
			}
		})
	}
}

func TestStepShuffleInactiveFallbackAndWGatherWriteMaskException(t *testing.T) {
	t.Run("shuffle-inactive-source-fallback", func(t *testing.T) {
		operation := customOperation(t, "shfl.down", nil)
		initial := initialWarp()
		initial.ActiveMask = 0b0101
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Destinations[0], lane, uint32(0xe0+lane))
			setInteger(&initial, operation.Sources[0], lane, uint32(10+10*lane))
			setInteger(&initial, operation.Sources[1], lane, 1|3<<6)
		}
		inactiveOne := initial.Lanes[1].GPR[operation.Destinations[0].Index]
		inactiveThree := initial.Lanes[3].GPR[operation.Destinations[0].Index]
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		got := integerVector(t, owner, operation.Destinations[0])
		if got != (isa.LaneValues{10, inactiveOne, 30, inactiveThree}) {
			t.Fatalf("shuffle fallback result=%+v values=%#x", result, got)
		}
	})

	t.Run("wgather-writes-input-inactive-lanes", func(t *testing.T) {
		operation := customOperation(t, "wgather", func(word uint32) uint32 {
			return word&^(uint32(3)<<25) | uint32(1)<<25
		})
		initial := initialWarp()
		initial.ActiveMask = 0b0101 // nominal source lane 1 inactive; fallback lane is 2
		for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Destinations[0], lane, uint32(0xf0+lane))
		}
		setInteger(&initial, operation.Sources[0], 2, 0xaa)
		setInteger(&initial, operation.Sources[1], 2, 0xbb)
		setInteger(&initial, operation.Sources[2], 2, 0xcc)
		sourceLaneWant := initial.Lanes[1].GPR[operation.Destinations[0].Index]
		owner := newState(t, initial)
		result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		got := integerVector(t, owner, operation.Destinations[0])
		if got != (isa.LaneValues{0xcc, sourceLaneWant, 0xaa, 0xbb}) ||
			result.Effects.RegisterWrites[0].Mask != 0b1101 {
			t.Fatalf("WGATHER result=%+v values=%#x", result, got)
		}
	})
}

func TestStepFutureOwnerCustomInstructionsRemainDeferred(t *testing.T) {
	for _, test := range []struct {
		name    string
		context state.ReadContext
	}{
		{name: "wspawn"},
		{name: "wsync"},
		{name: "wsync", context: state.ReadContext{PendingPriorWork: true}},
		{name: "bar"},
		{name: "bar.arrive"},
		{name: "bar.wait"},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation := customOperation(t, test.name, nil)
			owner := newState(t, initialWarp())
			before, _ := owner.Snapshot()
			result := executor(t, owner, &recordingSource{word: operation.Word}).Step(test.context)
			requireOutcome(t, result, warp.OutcomeDeferred)
			after, _ := owner.Snapshot()
			if before != after || result.NextPC != 0x100 || result.Effects == nil {
				t.Fatalf("future-owner %s result=%+v changed=%t", test.name, result, before != after)
			}
		})
	}
}

func TestStepRejectsDivergenceCapacityAndStaleViewWithoutLocalMutation(t *testing.T) {
	t.Run("capacity", func(t *testing.T) {
		operation := customOperation(t, "split", nil)
		initial := initialWarp()
		initial.ActiveMask = isa.AllLanes
		initial.Divergence = state.DivergenceInitial{
			WritePointer: state.FrozenDivergenceDepth,
			Records: []state.DivergenceRecordInitial{
				{Pointer: 0, OriginalMask: isa.AllLanes, NextPC: 0x120},
				{Pointer: 1, OriginalMask: isa.AllLanes, NextPC: 0x140},
				{Pointer: 2, OriginalMask: isa.AllLanes, NextPC: 0x160},
			},
		}
		selected, rejected := uint32(1), uint32(0)
		if operation.ConditionNegated {
			selected, rejected = rejected, selected
		}
		setInteger(&initial, operation.Sources[0], 0, selected)
		for lane := uint8(1); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Sources[0], lane, rejected)
		}
		owner := newState(t, initial)
		before, _ := owner.Snapshot()
		result := executor(t, owner, &recordingSource{word: operation.Word}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultEffectValidation || before != after {
			t.Fatalf("capacity result=%+v changed=%t", result, before != after)
		}
	})

	t.Run("stale-stack-view", func(t *testing.T) {
		operation := customOperation(t, "split", nil)
		initial := initialWarp()
		initial.ActiveMask = isa.AllLanes
		selected, rejected := uint32(1), uint32(0)
		if operation.ConditionNegated {
			selected, rejected = rejected, selected
		}
		setInteger(&initial, operation.Sources[0], 0, selected)
		for lane := uint8(1); lane < isa.FrozenLaneCount; lane++ {
			setInteger(&initial, operation.Sources[0], lane, rejected)
		}
		owner := newState(t, initial)
		source := &divergenceMutatingSource{owner: owner, word: operation.Word}
		result := executor(t, owner, source).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultEffectValidation || after != source.after || after.PC() != 0x100 {
			t.Fatalf("stale result=%+v state=%+v callback=%+v", result, after, source.after)
		}
	})
}

type divergenceMutatingSource struct {
	owner *state.WarpState
	word  uint32
	after state.WarpSnapshot
}

func (s *divergenceMutatingSource) Read(_ uint32, dst []byte) error {
	binary.LittleEndian.PutUint32(dst, s.word)
	if err := s.owner.SetDivergence(state.DivergenceInitial{
		WritePointer: 1,
		Records: []state.DivergenceRecordInitial{
			{Pointer: 0, OriginalMask: isa.AllLanes, NextPC: 0x140},
		},
	}); err != nil {
		return err
	}
	s.after, _ = s.owner.Snapshot()
	return nil
}
