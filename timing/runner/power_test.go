package runner

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

func TestHardwareEndDoesNotWaitForStoreRefill(t *testing.T) {
	_, launch := rtlProbeKernel(t, []uint32{0xb}, [3]uint32{1, 1, 1}, 0)
	ram, err := memory.New(0x12000)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []uint32{0x000100b7, 0x02a00113, 0x0020a023, 0xb} {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], w)
		if err = ram.Write(0x100+uint32(i*4), data[:]); err != nil {
			t.Fatal(err)
		}
	}
	config := memsys.Config{Latency: 200, AcceptsPerCycle: 1, MaxInflight: 16, ReturnsPerCycle: 1}
	k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config})
	if err != nil {
		t.Fatal(err)
	}
	var end uint64
	for i := 0; i < 2000 && !k.Status().Complete; i++ {
		if err = k.Run(1, nil); err != nil {
			t.Fatal(err)
		}
		s := k.Status()
		if s.HardwareComplete && !s.MemoryDrained {
			if s.Complete {
				t.Fatal("hardware completion discarded live transport")
			}
			if end == 0 {
				end = s.HardwareEndCycle
			}
			if s.HardwareEndCycle != end {
				t.Fatal("hardware boundary moved with software tail")
			}
		}
	}
	s := k.Status()
	if end == 0 || !s.Complete || s.Cycle <= end || s.HardwareEndCycle != end {
		t.Fatal("missing independent hardware/transport boundaries", s, end)
	}
}

func TestPowerInitializationAndKMUStart(t *testing.T) {
	for _, chunk := range []uint64{1, 7, 1024} {
		seed, launch := rtlProbeKernel(t, []uint32{0xcdd020f3, 0xb}, [3]uint32{4, 1, 1}, 64)
		p, err := PowerOn(seed.backing, Options{Backend: "std", PeriodPS: 1})
		if err != nil {
			t.Fatal(err)
		}
		if done, err := p.Initialize(0); err != nil || done || p.Cycle() != 0 {
			t.Fatal(done, err, p.Cycle())
		}
		for {
			done, err := p.Initialize(chunk)
			if err != nil {
				t.Fatal(err)
			}
			if done {
				break
			}
			if p.Cycle() > 1000 {
				t.Fatal("reset did not settle")
			}
		}
		// Derived from the actual I-cache tag walk plus the two bank stages.
		spec, err := memsys.FrozenCacheSpec(memsys.InstructionCache)
		if err != nil {
			t.Fatal(err)
		}
		if p.Cycle() != uint64(spec.Sets+spec.Latency) {
			t.Fatal("init edges", p.Cycle(), spec)
		}
		invalid := launch
		invalid.StartupPC = 1
		if _, err = p.Start(invalid); err == nil {
			t.Fatal("invalid launch consumed device")
		}
		k, err := p.Start(launch)
		if err != nil {
			t.Fatal(err)
		}
		if k.runner.Cycle() != p.Cycle() || k.startCycle != p.Cycle() || k.Counters().Cycle != 0 {
			t.Fatal("initialization charged to kernel", k.Status())
		}
		if _, err = p.Start(launch); err == nil {
			t.Fatal("double transfer")
		}
		if _, err = p.Initialize(1); err == nil {
			t.Fatal("old device clock still writable")
		}
		start := k.startCycle
		var schedule uint64
		for edge := uint64(0); edge < 7; edge++ {
			if err = k.Run(1, func(r MultiRecord) {
				for _, e := range r.Events {
					if e.Resource == "b-schedule" && e.Kind == "enter" && schedule == 0 {
						schedule = r.Cycle
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			if edge == 0 && k.Status().Generated != 0 {
				t.Fatal("KMU valid bypassed start register")
			}
			if edge >= 3 && k.contextReady[0] != (edge >= 5) {
				t.Fatal("TID RAM write edge", edge, k.contextReady)
			}
		}
		if schedule != start+4 {
			t.Fatal("start -> accept -> select -> fire -> schedule", start, schedule)
		}
		if err = k.Run(3000, nil); err != nil {
			t.Fatal(err)
		}
		s := k.Status()
		if !s.Complete || !s.HardwareComplete || s.HardwareCycles == 0 || s.HardwareEndCycle > s.Cycle {
			t.Fatal(s)
		}
	}
}

func TestPowerCanStartDuringInitialization(t *testing.T) {
	seed, launch := rtlProbeKernel(t, []uint32{0xb}, [3]uint32{1, 1, 1}, 0)
	p, err := PowerOn(seed.backing, Options{Backend: "std", PeriodPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	if done, err := p.Initialize(5); err != nil || done {
		t.Fatal(done, err)
	}
	k, err := p.Start(launch)
	if err != nil {
		t.Fatal(err)
	}
	if k.startCycle != 5 || k.runner.hierarchy.system.ResetSettled() {
		t.Fatal("start skipped remaining initialization")
	}
	if err = k.Run(2000, nil); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete {
		t.Fatal(k.Status())
	}
}
