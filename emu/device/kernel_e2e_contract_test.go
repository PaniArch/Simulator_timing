package device_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
)

func e2eAddImmediate(rd, rs1 uint32, immediate int32) uint32 {
	return (uint32(immediate)&0xfff)<<20 | rs1<<15 | rd<<7 | 0x13
}

func e2eShiftLeftImmediate(rd, rs1, amount uint32) uint32 {
	return amount<<20 | rs1<<15 | 1<<12 | rd<<7 | 0x13
}

func e2eRegisterOp(rd, rs1, rs2, function7 uint32) uint32 {
	return function7<<25 | rs2<<20 | rs1<<15 | rd<<7 | 0x33
}

func e2eLoadWord(rd, rs1 uint32) uint32 {
	return rs1<<15 | 2<<12 | rd<<7 | 0x03
}

func e2eStoreWord(rs2, rs1 uint32) uint32 {
	return rs2<<20 | rs1<<15 | 2<<12 | 0x23
}

func e2eCSRRead(rd uint32, address uint16) uint32 {
	return uint32(address)<<20 | 2<<12 | rd<<7 | 0x73
}

func e2eJALR(rd, rs1 uint32) uint32 { return rs1<<15 | rd<<7 | 0x67 }

func e2eJAL(rd uint32, offset int32) uint32 {
	immediate := uint32(offset)
	return (immediate>>20&1)<<31 |
		(immediate>>1&0x3ff)<<21 |
		(immediate>>11&1)<<20 |
		(immediate>>12&0xff)<<12 |
		rd<<7 | 0x6f
}

func e2eWithRegisters(word, rs1, rs2 uint32) uint32 {
	word &^= uint32(0x1f)<<15 | uint32(0x1f)<<20
	return word | rs1<<15 | rs2<<20
}

func installKernelContractProgram(t *testing.T, backing *trackedBacking) {
	t.Helper()
	// A first-use-only prologue leaves x30 as an observable marker, then
	// enters the five-instruction dispatch window. Recycled Warps rewind from
	// 0x134 to 0x120 and therefore repeat only this window.
	backing.putWord(t, 0x100, e2eAddImmediate(30, 0, 1))
	backing.putWord(t, 0x104, e2eJAL(0, 0x1c)) // 0x104 -> 0x120
	backing.putWord(t, 0x120, e2eCSRRead(20, 0xce1))
	backing.putWord(t, 0x124, e2eCSRRead(21, 0x340))
	backing.putWord(t, 0x128, e2eJALR(1, 20))
	backing.putWord(t, 0x12c, e2eAddImmediate(0, 0, 0))
	backing.putWord(t, 0x130, withRS1(kernelWord(t, "tmc"), 0))

	barrier := e2eWithRegisters(kernelWord(t, "bar"), 15, 16)
	body := []uint32{
		e2eCSRRead(10, 0xcd3),            // CTA thread x
		e2eCSRRead(11, 0xcd6),            // block x
		e2eCSRRead(12, 0xcdf),            // CTA-scoped LMEM address
		e2eShiftLeftImmediate(13, 10, 2), // per-thread LMEM word offset
		e2eRegisterOp(13, 12, 13, 0),     // local address
		e2eLoadWord(19, 21),              // output base from parameter
		e2eLoadWord(22, 21) | 4<<20,      // input base from parameter+4
		e2eShiftLeftImmediate(25, 11, 2), // block input offset
		e2eRegisterOp(25, 22, 25, 0),     // staged input address
		e2eLoadWord(22, 25),              // staged input for this block
		e2eShiftLeftImmediate(14, 11, 4), // block x * 16
		e2eRegisterOp(14, 14, 10, 0),     // + thread x
		e2eRegisterOp(14, 14, 22, 0),     // + staged scalar
		e2eRegisterOp(14, 14, 30, 0),     // + startup marker
		e2eStoreWord(14, 13),             // LMEM route, never global backing
		e2eCSRRead(17, 0xcc1),            // physical warp id
		e2eCSRRead(18, 0xcd1),            // CTA rank
		e2eRegisterOp(15, 17, 18, 0x20),  // first CTA warp = wid-rank
		e2eAddImmediate(25, 0, 1<<8),     // barrier ID 1
		e2eRegisterOp(15, 15, 25, 0),     // canonical AddressWarp/ID
		e2eAddImmediate(16, 0, 2),        // two participating warps
		barrier,
		e2eLoadWord(27, 13),              // consume LMEM after barrier
		e2eShiftLeftImmediate(23, 11, 2), // block x * 4
		e2eShiftLeftImmediate(25, 11, 1), // block x * 2
		e2eRegisterOp(23, 23, 25, 0),     // block x * 6
		e2eRegisterOp(23, 23, 10, 0),     // + thread x
		e2eShiftLeftImmediate(23, 23, 2), // output byte offset
		e2eRegisterOp(24, 19, 23, 0),     // global output address
		e2eStoreWord(27, 24),             // only active lanes issue writes
		e2eJALR(0, 1),                    // return to dispatch window
	}
	for index, word := range body {
		backing.putWord(t, 0x200+uint32(index)*4, word)
	}
}

func contractLaunch(startup uint32) device.LaunchState {
	return device.LaunchState{
		StartupPC: startup, KernelEntryPC: 0x200, ParameterAddress: 0x80,
		GridDimensions: [3]uint32{3, 1, 1}, BlockDimensions: [3]uint32{6, 1, 1},
		BlockSize: 6, WarpStep: [3]uint32{4, 0, 0}, LocalMemorySize: 64,
		ClusterDimensions: [3]uint32{1, 1, 1},
	}
}

func TestKernelE2EStartupEntryParametersLMEMBarrierPartialWarpsAndReuse(t *testing.T) {
	const outputBase = uint32(0x800)
	const inputBase = uint32(0x700)
	backing := newTrackedBacking(t, 0x1000)
	installKernelContractProgram(t, backing)
	backing.putWord(t, 0x80, outputBase)
	backing.putWord(t, 0x84, inputBase)
	for block := uint32(0); block < 3; block++ {
		backing.putWord(t, inputBase+block*4, 7+block*100)
	}
	for index := uint32(0); index < 18; index++ {
		backing.putWord(t, outputBase+index*4, 0xdeadbeef)
	}
	backing.resetObservations()

	executor, err := device.NewKernelExecutor(contractLaunch(0x100), backing)
	if err != nil {
		t.Fatal(err)
	}
	var generated [][3]uint32
	var admissions []core.CTASnapshot
	barriers := 0
	result := executor.Run(device.KernelRunOptions{StepBudget: 300, Trace: device.KernelTraceFunc(func(record device.KernelTraceRecord) {
		if record.CTA != nil {
			if record.CTA.Kind == device.CTAGenerated {
				generated = append(generated, record.CTA.CTA.BlockID)
			}
			if record.CTA.Kind == device.CTAAdmitted {
				snapshot := record.CTA.Resident
				snapshot.Members = append([]core.WarpMembership(nil), snapshot.Members...)
				admissions = append(admissions, snapshot)
			}
			// Exercise both nested slices exposed to an untrusted observer.
			record.CTA.CTA.BlockID[0] = 99
			if len(record.CTA.Resident.Members) != 0 {
				record.CTA.Resident.Members[0].ThreadCoordinates[0][0] = 99
			}
		}
		if record.Core != nil {
			if record.Core.Barrier != nil && record.Core.Barrier.Accepted {
				barriers++
			}
			if effects := record.Core.WarpResult.Effects; effects != nil && len(effects.MemoryRequests) != 0 {
				effects.MemoryRequests[0].Address = 0
			}
		}
	})})
	if result.Outcome != device.KernelComplete || !result.Complete || result.Err != nil ||
		result.Generated != 3 || result.Admitted != 3 || result.Completed != 3 {
		t.Fatalf("kernel contract result=%+v", result)
	}
	if !reflect.DeepEqual(generated, [][3]uint32{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}) || len(admissions) != 3 {
		t.Fatalf("BlockID generation/admission=%v/%+v", generated, admissions)
	}
	for index, admission := range admissions {
		if admission.StartupPC != 0x100 || admission.Entry != 0x200 || admission.ParameterAddress != 0x80 ||
			admission.BlockSize != 6 || admission.WarpStep != [3]uint32{4, 0, 0} || len(admission.Members) != 2 ||
			admission.Members[0].ActiveMask != isa.AllLanes || admission.Members[1].ActiveMask != 0b0011 ||
			admission.Members[0].ThreadCoordinates[0] != (isa.LaneValues{0, 1, 2, 3}) ||
			admission.Members[1].ThreadCoordinates[0] != (isa.LaneValues{4, 5, 0, 1}) ||
			admission.Members[1].ThreadCoordinates[2] != (isa.LaneValues{0, 0, 1, 1}) {
			t.Fatalf("admission %d lost CTA/Warp context: %+v", index, admission)
		}
	}
	if barriers != 6 { // one accepted sync request per Warp and CTA
		t.Fatalf("accepted barrier transitions=%d, want 6", barriers)
	}
	for block := uint32(0); block < 3; block++ {
		for thread := uint32(0); thread < 6; thread++ {
			index := block*6 + thread
			want := uint32(7 + block*100 + 1 + block*16 + thread)
			if got := backing.loadWord(t, outputBase+index*4); got != want {
				t.Fatalf("output block=%d thread=%d got=%#x want=%#x", block, thread, got, want)
			}
		}
	}
	if len(backing.writes) != 18 {
		t.Fatalf("inactive lanes or LMEM leaked to global backing: writes=%#v", backing.writes)
	}
	sort.Slice(backing.writes, func(i, j int) bool { return backing.writes[i] < backing.writes[j] })
	for index, address := range backing.writes {
		if address != outputBase+uint32(index)*4 {
			t.Fatalf("global write[%d]=%#x", index, address)
		}
	}
	if !containsAddress(backing.reads, 0x100) || !containsAddress(backing.reads, 0x200) ||
		!containsAddress(backing.reads, 0x80) || !containsAddress(backing.reads, inputBase) ||
		containsAddress(backing.reads, isa.FrozenLocalMemBase) {
		t.Fatalf("fetch/argument/LMEM routing reads=%#v", backing.reads)
	}

	// Starting at entry bypasses the dispatch stub: entry/mscratch/return and
	// the startup marker are observably absent. It must not mimic a valid run.
	wrong := newTrackedBacking(t, 0x1000)
	installKernelContractProgram(t, wrong)
	wrong.putWord(t, 0x80, outputBase)
	wrong.putWord(t, 0x84, inputBase)
	wrong.putWord(t, inputBase, 7)
	wrongExecutor, err := device.NewKernelExecutor(contractLaunch(0x200), wrong)
	if err != nil {
		t.Fatal(err)
	}
	wrongResult := wrongExecutor.Run(device.KernelRunOptions{StepBudget: 100})
	if wrongResult.Complete {
		t.Fatalf("entry substituted for startup completed unexpectedly: %+v", wrongResult)
	}
	if got := wrong.loadWord(t, outputBase); got == 8 {
		t.Fatal("entry-substituted run reproduced startup-dependent output")
	}
}

func TestKernelExecutionResumesSameBarrierOwnersAfterExternalEvents(t *testing.T) {
	backing := newTrackedBacking(t, 0x400)
	arrive := e2eWithRegisters(kernelWord(t, "bar.arrive"), 15, 16)
	barrier := e2eWithRegisters(kernelWord(t, "bar"), 15, 16)
	program := []uint32{
		0x80000837,                 // lui x16,0x80000
		e2eAddImmediate(16, 16, 1), // attach one event per Warp
		arrive,
		e2eAddImmediate(16, 0, 2), // two participants
		barrier,
		withRS1(kernelWord(t, "tmc"), 0),
	}
	for index, word := range program {
		backing.putWord(t, 0x100+uint32(index)*4, word)
	}
	launch := contractLaunch(0x100)
	launch.GridDimensions = [3]uint32{1, 1, 1}
	launch.KernelEntryPC = 0x180
	executor, err := device.NewKernelExecutor(launch, backing)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.NewExecution()
	if err != nil {
		t.Fatal(err)
	}
	var sequences []uint64
	trace := device.KernelTraceFunc(func(record device.KernelTraceRecord) { sequences = append(sequences, record.Sequence) })
	blocked := execution.Run(device.KernelRunOptions{StepBudget: 32, Trace: trace})
	var blockedError *device.KernelBlockedError
	if blocked.Outcome != device.KernelBlocked || blocked.Complete || !errors.As(blocked.Err, &blockedError) ||
		blocked.Generated != 1 || blocked.Admitted != 1 || blocked.Completed != 0 || blocked.LastCore == nil ||
		blocked.LastCore.Barrier == nil || !blocked.LastCore.Barrier.Blocked || blocked.LastCore.Barrier.After.Events != 2 ||
		!blocked.LastCore.Barrier.After.ArrivalsComplete {
		t.Fatalf("barrier did not hold an exhausted grid: %+v", blocked)
	}
	key := blocked.LastCore.Barrier.Key
	if err := execution.CompleteBarrierEvent(key); err != nil {
		t.Fatal(err)
	}
	if err := execution.CompleteBarrierEvent(key); err != nil {
		t.Fatal(err)
	}
	resumed := execution.Run(device.KernelRunOptions{StepBudget: 8, Trace: trace})
	if resumed.Outcome != device.KernelComplete || !resumed.Complete || resumed.Err != nil ||
		resumed.Generated != 0 || resumed.Admitted != 0 || resumed.Completed != 1 || resumed.Attempts != 2 {
		t.Fatalf("same execution did not resume to completion: %+v", resumed)
	}
	for index, sequence := range sequences {
		if sequence != uint64(index) {
			t.Fatalf("session trace sequence[%d]=%d", index, sequence)
		}
	}
	if err := execution.CompleteBarrierEvent(key); err == nil {
		t.Fatal("completed execution accepted a barrier event")
	}
	idempotent := execution.Run(device.KernelRunOptions{StepBudget: 1})
	if !idempotent.Complete || idempotent.Attempts != 0 || idempotent.Completed != 0 {
		t.Fatalf("completed execution was not stable: %+v", idempotent)
	}
}

func TestKernelInvalidBudgetFaultAndFollowupExecutionRemainIsolated(t *testing.T) {
	t.Run("invalid-launch-does-not-touch-memory", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		launch := kernelLaunch(1, 1)
		launch.StartupPC++
		if _, err := device.NewKernelExecutor(launch, backing); !errors.Is(err, device.ErrInvalidLaunch) {
			t.Fatalf("invalid launch error=%v", err)
		}
		if len(backing.reads) != 0 || len(backing.writes) != 0 {
			t.Fatalf("invalid launch touched memory: reads=%v writes=%v", backing.reads, backing.writes)
		}
	})

	t.Run("budget-resumes-canonical-owner-state", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		installReusableTerminate(t, backing)
		executor, err := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		if err != nil {
			t.Fatal(err)
		}
		execution, err := executor.NewExecution()
		if err != nil {
			t.Fatal(err)
		}
		first := execution.Run(device.KernelRunOptions{StepBudget: 2})
		var budget *device.KernelBudgetExceededError
		if first.Outcome != device.KernelBudgetExceeded || first.Complete || !errors.As(first.Err, &budget) ||
			first.Generated != 1 || first.Admitted != 1 || first.Completed != 0 || first.Attempts != 2 {
			t.Fatalf("budget interval=%+v", first)
		}
		second := execution.Run(device.KernelRunOptions{StepBudget: 3})
		if second.Outcome != device.KernelComplete || !second.Complete || second.Attempts != 3 ||
			second.Generated != 0 || second.Admitted != 0 || second.Completed != 1 {
			t.Fatalf("budget continuation recreated or lost owners: %+v", second)
		}
	})

	t.Run("fault-does-not-write-and-fresh-followup-runs", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		backing.putWord(t, 0x100, 0xffffffff)
		backing.putWord(t, 0x300, 0xdecafbad)
		backing.resetObservations()
		executor, err := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		if err != nil {
			t.Fatal(err)
		}
		failed := executor.Run(device.KernelRunOptions{StepBudget: 2})
		if failed.Outcome != device.KernelFault || failed.Complete || len(backing.writes) != 0 ||
			backing.loadWord(t, 0x300) != 0xdecafbad {
			t.Fatalf("fault changed memory or completion: result=%+v writes=%v", failed, backing.writes)
		}
		installReusableTerminate(t, backing)
		backing.resetObservations()
		followup := executor.Run(device.KernelRunOptions{StepBudget: 5})
		if followup.Outcome != device.KernelComplete || !followup.Complete || followup.Attempts != 5 ||
			backing.loadWord(t, 0x300) != 0xdecafbad || len(backing.writes) != 0 {
			t.Fatalf("fresh followup inherited fault state: result=%+v writes=%v", followup, backing.writes)
		}
	})
}
