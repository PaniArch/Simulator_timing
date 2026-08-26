package device_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"vortex.local/simulator/core"
	"vortex.local/simulator/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/support/memory"
)

type trackedBacking struct {
	owner  *memory.Memory
	reads  []uint32
	writes []uint32
}

func newTrackedBacking(t *testing.T, size uint64) *trackedBacking {
	t.Helper()
	owner, err := memory.New(size)
	if err != nil {
		t.Fatal(err)
	}
	return &trackedBacking{owner: owner}
}

func (m *trackedBacking) Read(address uint32, destination []byte) error {
	m.reads = append(m.reads, address)
	return m.owner.Read(address, destination)
}

func (m *trackedBacking) Write(address uint32, source []byte) error {
	m.writes = append(m.writes, address)
	return m.owner.Write(address, source)
}

func (m *trackedBacking) WriteBatch(addresses []uint32, sources [][]byte) error {
	m.writes = append(m.writes, addresses...)
	return m.owner.WriteBatch(addresses, sources)
}

func (m *trackedBacking) putWord(t *testing.T, address, word uint32) {
	t.Helper()
	var bytes [4]byte
	binary.LittleEndian.PutUint32(bytes[:], word)
	if err := m.owner.Write(address, bytes[:]); err != nil {
		t.Fatal(err)
	}
}

func (m *trackedBacking) loadWord(t *testing.T, address uint32) uint32 {
	t.Helper()
	var bytes [4]byte
	if err := m.owner.Read(address, bytes[:]); err != nil {
		t.Fatal(err)
	}
	return binary.LittleEndian.Uint32(bytes[:])
}

func (m *trackedBacking) resetObservations() { m.reads, m.writes = nil, nil }

func kernelWord(t *testing.T, name string) uint32 {
	t.Helper()
	for _, entry := range isa.Catalog() {
		if entry.Name == name {
			return entry.Example
		}
	}
	t.Fatalf("missing catalog instruction %q", name)
	return 0
}

func withRS1(word uint32, register uint32) uint32 {
	return word&^(uint32(0x1f)<<15) | register<<15
}

func kernelLaunch(gridX, clusterX uint32) device.LaunchState {
	return device.LaunchState{
		StartupPC: 0x100, KernelEntryPC: 0x180, ParameterAddress: 0x40,
		GridDimensions: [3]uint32{gridX, 1, 1}, BlockDimensions: [3]uint32{1, 1, 1},
		BlockSize: 1, WarpStep: [3]uint32{4, 0, 0},
		ClusterDimensions: [3]uint32{clusterX, 1, 1},
	}
}

func installReusableTerminate(t *testing.T, backing *trackedBacking) {
	t.Helper()
	for index := uint32(0); index < 4; index++ {
		backing.putWord(t, 0x100+index*4, 0x00000013) // addi x0,x0,0
	}
	backing.putWord(t, 0x110, withRS1(kernelWord(t, "tmc"), 0))
}

func TestKernelRunClusterBackpressureReuseCompletionAndDetachedTrace(t *testing.T) {
	backing := newTrackedBacking(t, 0x400)
	installReusableTerminate(t, backing)
	executor, err := device.NewKernelExecutor(kernelLaunch(6, 2), backing)
	if err != nil {
		t.Fatal(err)
	}
	var sequences []uint64
	result := executor.Run(device.KernelRunOptions{StepBudget: 100, Trace: device.KernelTraceFunc(func(record device.KernelTraceRecord) {
		sequences = append(sequences, record.Sequence)
		if record.CTA != nil {
			record.CTA.CTA.ID = 0xffffffff
			if len(record.CTA.Resident.Members) != 0 {
				record.CTA.Resident.Members[0].WarpID = 3
			}
		}
		if record.Core != nil && record.Core.WarpResult.Decoded != nil {
			record.Core.WarpResult.Decoded.Name = "mutated-by-sink"
		}
	})})
	if result.Outcome != device.KernelComplete || !result.Complete || result.Err != nil ||
		result.Generated != 6 || result.Admitted != 6 || result.Completed != 6 ||
		result.Attempts != 30 || result.Retired != 30 {
		t.Fatalf("wrong clustered kernel result: %+v", result)
	}
	if len(result.CTAEvents) != 18 || len(sequences) != 48 {
		t.Fatalf("wrong trace/event sizes: events=%d traces=%d", len(result.CTAEvents), len(sequences))
	}
	for index, sequence := range sequences {
		if sequence != uint64(index) {
			t.Fatalf("trace sequence[%d]=%d", index, sequence)
		}
	}
	var generated, admitted, completed []device.CTAEvent
	for _, event := range result.CTAEvents {
		if event.CTA.ID == 0xffffffff || len(event.Resident.Members) != 0 && event.Resident.Members[0].WarpID == 3 && event.CTA.ID == 0 {
			t.Fatal("trace sink mutated detached result events")
		}
		switch event.Kind {
		case device.CTAGenerated:
			generated = append(generated, event)
		case device.CTAAdmitted:
			admitted = append(admitted, event)
		case device.CTACompleted:
			completed = append(completed, event)
		}
	}
	if len(generated) != 6 || len(admitted) != 6 || len(completed) != 6 {
		t.Fatalf("event kind counts=%d/%d/%d", len(generated), len(admitted), len(completed))
	}
	for index := 0; index < 6; index += 2 {
		if generated[index].CTA.ClusterOrigin != generated[index+1].CTA.ClusterOrigin ||
			!admitted[index].CTA.IsFirstOfCluster || admitted[index+1].CTA.IsFirstOfCluster ||
			admitted[index].CTA.ClusterOrigin != admitted[index+1].CTA.ClusterOrigin {
			t.Fatalf("cluster %d was not generated/admitted as a group: %+v/%+v", index/2, admitted[index], admitted[index+1])
		}
	}
	if result.LastCore == nil || result.LastCore.WarpResult.Decoded == nil || result.LastCore.WarpResult.Decoded.Name == "mutated-by-sink" {
		t.Fatal("Core/Warp result was not detached from trace sink")
	}
	if result.Last == nil || result.Last.CTA == nil || result.Last.CTA.Kind != device.CTACompleted {
		t.Fatalf("last trace did not retain final completion: %+v", result.Last)
	}

	// A second Run creates fresh scheduling owners while preserving the exact
	// caller memory and deterministically executes the same grid again.
	again := executor.Run(device.KernelRunOptions{StepBudget: 100})
	if !again.Complete || again.Attempts != result.Attempts || again.Completed != 6 {
		t.Fatalf("fresh second run=%+v", again)
	}
}

func TestKernelUsesOneExternalBackingForFetchArgumentsGlobalWritesAndOutput(t *testing.T) {
	backing := newTrackedBacking(t, 0x400)
	program := []uint32{
		0x00000013,                       // one-time startup instruction
		0x00000013,                       // one-time startup instruction
		0x340022f3,                       // csrrs x5,mscratch,x0
		0x0002a303,                       // lw x6,0(x5)
		0x00130313,                       // addi x6,x6,1
		0x0062a223,                       // sw x6,4(x5)
		withRS1(kernelWord(t, "tmc"), 0), // terminate
	}
	for index, word := range program {
		backing.putWord(t, 0x100+uint32(index)*4, word)
	}
	backing.putWord(t, 0x40, 41)
	backing.putWord(t, 0x44, 0xdeadbeef)
	backing.resetObservations()
	executor, err := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
	if err != nil {
		t.Fatal(err)
	}
	result := executor.Run(device.KernelRunOptions{StepBudget: 20})
	if !result.Complete || result.Outcome != device.KernelComplete || result.Attempts != 7 {
		t.Fatalf("memory kernel result=%+v", result)
	}
	if got := backing.loadWord(t, 0x44); got != 42 {
		t.Fatalf("caller did not observe output in original memory: %#x", got)
	}
	if got := backing.loadWord(t, 0x40); got != 41 || backing.loadWord(t, 0x100) != program[0] {
		t.Fatal("executor cleared or replaced argument/program bytes")
	}
	if !containsAddress(backing.reads, 0x100) || !containsAddress(backing.reads, 0x40) || containsAddress(backing.reads, 0x180) ||
		len(backing.writes) != 1 || backing.writes[0] != 0x44 {
		t.Fatalf("one backing route not observed: reads=%#v writes=%#v", backing.reads, backing.writes)
	}
}

func TestKernelTypedNonCompletionOutcomes(t *testing.T) {
	t.Run("budget-after-grid-admission", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		installReusableTerminate(t, backing)
		executor, _ := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		result := executor.Run(device.KernelRunOptions{StepBudget: 0})
		var budget *device.KernelBudgetExceededError
		if result.Outcome != device.KernelBudgetExceeded || result.Complete || !errors.As(result.Err, &budget) ||
			result.Generated != 1 || result.Admitted != 1 || result.Completed != 0 {
			t.Fatalf("budget result=%+v err=%T", result, result.Err)
		}
	})
	t.Run("fault", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		backing.putWord(t, 0x100, 0xffffffff)
		executor, _ := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		result := executor.Run(device.KernelRunOptions{StepBudget: 2})
		if result.Outcome != device.KernelFault || result.Complete || result.Attempts != 1 {
			t.Fatalf("fault result=%+v", result)
		}
	})
	t.Run("trap", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		backing.putWord(t, 0x100, 0x00000073) // ecall
		executor, _ := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		result := executor.Run(device.KernelRunOptions{StepBudget: 2})
		if result.Outcome != device.KernelTrap || result.Complete || result.Attempts != 1 {
			t.Fatalf("trap result=%+v", result)
		}
	})
	t.Run("deferred", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		backing.putWord(t, 0x100, kernelWord(t, "wsync"))
		executor, _ := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		result := executor.Run(device.KernelRunOptions{StepBudget: 2, Context: func(uint64) state.ReadContext {
			return state.ReadContext{PendingPriorWork: true}
		}})
		var deferred *device.KernelDeferredError
		if result.Outcome != device.KernelDeferred || result.Complete || !errors.As(result.Err, &deferred) ||
			deferred.Reason != core.BlockPendingWork {
			t.Fatalf("deferred result=%+v err=%T", result, result.Err)
		}
	})
	t.Run("blocked", func(t *testing.T) {
		backing := newTrackedBacking(t, 0x400)
		arrive := kernelWord(t, "bar.arrive")
		wait := kernelWord(t, "bar.wait")
		rs2 := (arrive >> 20) & 0x1f
		if (wait>>20)&0x1f != rs2 || rs2 == 0 {
			t.Fatal("barrier catalog examples do not share a writable rs2")
		}
		backing.putWord(t, 0x100, 0x80000037|rs2<<7)                               // lui rs2,0x80000
		backing.putWord(t, 0x104, uint32(1)<<20|rs2<<15|uint32(0)<<12|rs2<<7|0x13) // addi rs2,rs2,1
		backing.putWord(t, 0x108, arrive)                                          // attach one pending event
		backing.putWord(t, 0x10c, rs2<<7|0x13)                                     // addi rs2,x0,0
		backing.putWord(t, 0x110, wait)                                            // wait on canonical phase 0
		executor, _ := device.NewKernelExecutor(kernelLaunch(1, 1), backing)
		result := executor.Run(device.KernelRunOptions{StepBudget: 10})
		var blocked *device.KernelBlockedError
		if result.Outcome != device.KernelBlocked || result.Complete || !errors.As(result.Err, &blocked) ||
			blocked.ResidentCTAs != 1 || result.Attempts != 5 {
			t.Fatalf("blocked result=%+v err=%T", result, result.Err)
		}
	})
	t.Run("empty-grid-complete", func(t *testing.T) {
		backing := newTrackedBacking(t, 1)
		executor, err := device.NewKernelExecutor(kernelLaunch(0, 1), backing)
		if err != nil {
			t.Fatal(err)
		}
		result := executor.Run(device.KernelRunOptions{})
		if !result.Complete || result.Outcome != device.KernelComplete || result.Attempts != 0 || len(backing.reads) != 0 {
			t.Fatalf("empty-grid result=%+v reads=%v", result, backing.reads)
		}
	})
}

func containsAddress(addresses []uint32, want uint32) bool {
	for _, address := range addresses {
		if address == want {
			return true
		}
	}
	return false
}
