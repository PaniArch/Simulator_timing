package runner_test

import (
	"encoding/binary"
	"reflect"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func TestKernelBarrierEventRejectsOldGeneration(t *testing.T) {
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	put := func(pc, w uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], w)
		if err := ram.Write(pc, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	for i, w := range []uint32{0xce1022f3, 0x13, 0x000283e7, 0x13, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	expect := customWord(t, "bar.arrive", 11, 9) | 10<<20
	sync := customWord(t, "bar", 0, 9) | 10<<20
	words := []uint32{0xcc1024f3, 0x80000537, 0x00150513, expect, 0x00100513, sync, 0xcd602173, 0x340020f3, 0x00211193, 0x003080b3, 0x00110113, 0x0020a023, 0x00038067}
	for i, w := range words {
		put(0x200+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x800, GridDimensions: [3]uint32{2, 1, 1}, BlockDimensions: [3]uint32{1, 1, 1}, BlockSize: 1, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 16384}
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: kernelMemoryConfig(20)})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Run(1000, nil); err != nil {
		t.Fatal(err)
	}
	tickets := k.PendingBarrierEvents()
	if len(tickets) != 1 || k.Status().Complete || k.Status().Completed != 0 {
		t.Fatal(k.Status(), tickets)
	}
	old := tickets[0]
	if err := k.CompleteBarrierEvent(old); err != nil {
		t.Fatal(err)
	}
	if err := k.CompleteBarrierEvent(old); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := k.Run(1000, nil); err != nil {
		t.Fatal(err)
	}
	tickets = k.PendingBarrierEvents()
	if len(tickets) != 1 || tickets[0].Slot != old.Slot || tickets[0].CTA == old.CTA || k.Status().Completed != 1 {
		t.Fatal(k.Status(), tickets)
	}
	before := k.Status()
	if err := k.CompleteBarrierEvent(old); err == nil {
		t.Fatal("old completion accepted after reuse")
	}
	forged := tickets[0]
	forged.CTA = old.CTA
	if err := k.CompleteBarrierEvent(forged); err == nil {
		t.Fatal("modified generation accepted")
	}
	if !reflect.DeepEqual(before, k.Status()) || !reflect.DeepEqual(tickets, k.PendingBarrierEvents()) {
		t.Fatal("invalid completion changed state")
	}
	if err := k.CompleteBarrierEvent(tickets[0]); err != nil {
		t.Fatal(err)
	}
	if err := k.Run(1000, nil); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || k.Status().Completed != 2 || len(k.PendingBarrierEvents()) != 0 {
		t.Fatal(k.Status())
	}
	counts := map[string]int{}
	var previous uint64
	for _, event := range k.TakeEvents() {
		if event.Cycle < previous {
			t.Fatal("event order", event)
		}
		previous = event.Cycle
		counts[event.Kind]++
	}
	if counts["generated"] != 2 || counts["admitted"] != 2 || counts["reclaimed"] != 2 || len(k.TakeEvents()) != 0 {
		t.Fatal("residency events", counts)
	}
	for i := uint32(0); i < 2; i++ {
		var b [4]byte
		kernelVisible(t, k)
		if err := ram.Read(0x800+i*4, b[:]); err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint32(b[:]) != i+1 {
			t.Fatal(i, b)
		}
	}
}
