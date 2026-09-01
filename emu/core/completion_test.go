package core_test

import (
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func TestCTACompletionUsesOnlyExplicitMembersSchedulerAndBarrierOwner(t *testing.T) {
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(ctaConfig(0, []uint8{0, 1}, 8, 64)); err != nil {
		t.Fatal(err)
	}
	var initials [isa.FrozenWarpCount]state.WarpInitial
	var words [isa.FrozenWarpCount]uint32
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id < 2)
		words[id] = catalogWord(t, "tmc")
		for lane := range initials[id].Lanes {
			initials[id].Lanes[lane].GPR[1] = 0
		}
	}
	manager, _, _ := makeCTAReadyCoreFromInitials(t, initials, words, ctas)
	coordinator, err := core.NewBarrierCoordinator(ctas)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := coordinator.Stage(isa.BarrierEffect{WarpID: 0, AddressWarp: 0, ID: 0,
		Kind: isa.BarrierArrive, Arrive: true, ParticipantCount: 2, SizeMinusOne: 1, DrainLSU: true})
	if err != nil || pending.Commit() != nil {
		t.Fatalf("pending arrival stage=%v err=%v", pending, err)
	}
	run := manager.Run(core.RunOptions{StepBudget: 8})
	completion, err := manager.CTACompletion(0)
	if err != nil || run.Outcome != core.RunBlocked || completion.Complete || !completion.PendingBarrier ||
		len(completion.Members) != 2 || !completion.Members[0].OwnerFinished || !completion.Members[1].OwnerFinished {
		t.Fatalf("pending completion=%+v run=%+v err=%v", completion, run, err)
	}
	legacy, err := manager.Complete()
	if err != nil || legacy {
		t.Fatalf("Core aggregate misreported pending CTA complete=%t err=%v", legacy, err)
	}

	// Completing the same phase clears only the canonical pending record. Both
	// members were already normally observed finished, so completion can now
	// become true without rewriting either Warp or scheduler state.
	last, err := coordinator.Stage(isa.BarrierEffect{WarpID: 1, AddressWarp: 0, ID: 0,
		Kind: isa.BarrierArrive, Arrive: true, ParticipantCount: 2, SizeMinusOne: 1, DrainLSU: true})
	if err != nil || last.Commit() != nil {
		t.Fatalf("last arrival stage=%v err=%v", last, err)
	}
	resumed := manager.Run(core.RunOptions{StepBudget: 1})
	completion, _ = manager.CTACompletion(0)
	if resumed.Outcome != core.RunComplete || !completion.Complete || completion.PendingBarrier {
		t.Fatalf("cleared completion=%+v run=%+v", completion, resumed)
	}
	completion.Members[0].OwnerFinished = false
	again, _ := manager.CTACompletion(0)
	if !again.Complete || !again.Members[0].OwnerFinished {
		t.Fatalf("detached completion rewrote owner: %+v", again)
	}
}

func TestCTACompletionIsolationAcrossResidentCTAs(t *testing.T) {
	ctas := core.NewCTAManager()
	if _, err := ctas.Admit(ctaConfig(0, []uint8{0}, 4, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := ctas.Admit(ctaConfig(1, []uint8{1}, 4, 64)); err != nil {
		t.Fatal(err)
	}
	var initials [isa.FrozenWarpCount]state.WarpInitial
	var words [isa.FrozenWarpCount]uint32
	for id := uint8(0); id < isa.FrozenWarpCount; id++ {
		initials[id] = initial(id, id < 2)
		words[id] = 0x0000006f // keep CTA 1 runnable
	}
	words[0] = catalogWord(t, "tmc")
	for lane := range initials[0].Lanes {
		initials[0].Lanes[lane].GPR[1] = 0
	}
	manager, _, _ := makeCTAReadyCoreFromInitials(t, initials, words, ctas)
	run := manager.Run(core.RunOptions{StepBudget: 1})
	cta0, _ := manager.CTACompletion(0)
	cta1, _ := manager.CTACompletion(1)
	all, _ := manager.Complete()
	if run.Outcome != core.RunBudgetExceeded || !cta0.Complete || cta1.Complete || all || len(run.CTACompletions) != 2 {
		t.Fatalf("isolated completion run=%+v CTA0=%+v CTA1=%+v all=%t", run, cta0, cta1, all)
	}
}

func TestCoreRunReportsAllBarrierWaitersBlockedThenResumesToCTAComplete(t *testing.T) {
	manager, ctas, owners, sources := makeTwoWarpBarrierCore(t)
	coordinator, _ := core.NewBarrierCoordinator(ctas)
	event, err := coordinator.Stage(isa.BarrierEffect{WarpID: 0, AddressWarp: 0, ID: 4,
		Kind: isa.BarrierArrive, Event: true, ExpectCount: 1, ParticipantCount: 1,
		Phase: true, DrainLSU: true})
	if err != nil || event.Commit() != nil {
		t.Fatalf("event stage=%v err=%v", event, err)
	}
	for id := uint8(0); id < 2; id++ {
		setBarrierInstruction(t, owners[id], sources[id], "bar", 0, 4, 2, false)
	}
	blocked := manager.Run(core.RunOptions{StepBudget: 8})
	completion, _ := manager.CTACompletion(0)
	if blocked.Outcome != core.RunBlocked || blocked.Attempts != 2 || blocked.Retired != 2 ||
		completion.Complete || !completion.PendingBarrier || completion.BlockedMembers != 0b0011 {
		t.Fatalf("blocked run=%+v completion=%+v", blocked, completion)
	}
	for id := uint8(0); id < 2; id++ {
		sources[id].word = catalogWord(t, "tmc")
		if err := owners[id].WriteRegister(isa.Register{File: isa.Integer, Index: 1}, isa.AllLanes, isa.LaneValues{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.CompleteBarrierEvent(core.BarrierKey{CTAID: 0, AddressWarp: 0, ID: 4}); err != nil {
		t.Fatal(err)
	}
	resumed := manager.Run(core.RunOptions{StepBudget: 8})
	completion, _ = manager.CTACompletion(0)
	if resumed.Outcome != core.RunComplete || resumed.Attempts != 2 || !completion.Complete || completion.BlockedMembers != 0 {
		t.Fatalf("resumed run=%+v completion=%+v", resumed, completion)
	}
}
