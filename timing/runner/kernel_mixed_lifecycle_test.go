package runner

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

type kernelReadCounter struct {
	*memory.Memory
	globalRefills int
}

func (m *kernelReadCounter) Read(a uint32, d []byte) error {
	if a >= 0x10800 && a < 0x10c00 {
		m.globalRefills++
	}
	return m.Memory.Read(a, d)
}
func TestKernelMixedMemoryLifecycle(t *testing.T) {
	type metrics struct {
		cycles, stalls, serviceEdges, requestBlocked uint64
		refills                                      int
	}
	results := map[string]metrics{}
	for _, tc := range []struct {
		name    string
		latency uint64
		stride  uint32
		period  uint64
	}{
		{"baseline", 20, 6, 1}, {"slow", 100, 6, 1}, {"dense", 20, 4, 1}, {"throttled", 20, 6, 4}, {"repeat", 20, 6, 1},
	} {
		latency := tc.latency
		measure := metrics{}
		ram, _ := memory.New(0x12000)
		put := func(pc, word uint32) {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], word)
			if err := ram.Write(pc, b[:]); err != nil {
				t.Fatal(err)
			}
		}
		csr := func(rd, a uint32) uint32 { return a<<20 | 2<<12 | rd<<7 | 0x73 }
		alu := func(rd, a, b, fun uint32) uint32 { return b<<20 | a<<15 | fun<<12 | rd<<7 | 0x33 }
		shift := func(rd, a, n uint32) uint32 { return n<<20 | a<<15 | 1<<12 | rd<<7 | 0x13 }
		imm := func(rd, a, n uint32) uint32 { return n<<20 | a<<15 | rd<<7 | 0x13 }
		for i, w := range []uint32{csr(5, 0xce1), 0x13, 0x000283e7, 0x13, 0xb} {
			put(0x100+uint32(i)*4, w)
		}
		// Each SIMD instruction has even lanes in LMEM and odd lanes in global.
		// One Warp/CTA, six CTAs and four physical slots force generation reuse.
		words := []uint32{
			csr(1, 0xcdf), csr(2, 0xcc0), csr(3, 0x340), csr(4, 0xcd6),
			shift(5, 2, 2), alu(1, 1, 5, 0), shift(6, 4, tc.stride), alu(3, 3, 6, 0), alu(3, 3, 5, 0),
			0x00117313,                                        // andi x6,x2,1
			0x40600333,                                        // sub x6,x0,x6
			alu(8, 1, 3, 4), alu(8, 8, 6, 7), alu(8, 8, 1, 4), // select local/global
			shift(9, 4, 4), alu(9, 9, 2, 0), imm(9, 9, 10),
			0x00942023, 0x00042503, // sw x9,0(x8); lw x10,0(x8)
			csr(11, 0x340), imm(11, 11, 1024), shift(12, 4, 4), alu(11, 11, 12, 0), alu(11, 11, 5, 0),
			0x00a5a023, 0x00038067, // output all lanes; return startup
		}
		for i, w := range words {
			put(0x200+uint32(i)*4, w)
		}
		config, _ := memsys.DefaultConfig()
		config.Latency = latency
		counted := &kernelReadCounter{Memory: ram}
		// Cache miss-pattern assertions require RAM above the RTL I/O aperture.
		k, err := NewKernel(device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x10800, GridDimensions: [3]uint32{6, 1, 1}, BlockDimensions: [3]uint32{4, 1, 1}, BlockSize: 4, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}, counted, Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config, Ready: func(c uint64) bool { return c%tc.period == 0 }})
		if err != nil {
			t.Fatal(err)
		}
		observed := map[uint32]isa.LaneValues{}
		generations := map[int]uint64{}
		reused := false
		stoppedTail := false
		collect := func() {
			for _, e := range k.TakeEvents() {
				if e.Kind == "admitted" {
					g := k.generations[e.Slot]
					if prior := generations[e.Slot]; prior != 0 {
						if g <= prior {
							t.Fatal("generation reused")
						}
						reused = true
					}
					generations[e.Slot] = g
				}
			}
		}
		if err := k.Run(6000, func(r MultiRecord) {
			measure.serviceEdges += uint64(len(r.Services))
			for _, w := range r.Warps {
				if w.Active && !w.Runnable {
					measure.stalls++
				}
			}
			if r.Report.MemoryRequest.Valid && !r.Report.MemoryAccepted {
				measure.requestBlocked++
			}
			status := k.Status()
			for _, c := range status.Resident {
				if c.MemoryPending && c.Reclaimable {
					t.Fatal("live residency reclaimable")
				}
				stoppedTail = stoppedTail || (c.StoppedWarps != 0 && c.MemoryPending)
				if len(c.Resident.Members) == 0 {
					continue
				}
				w := c.Resident.Members[0].WarpID
				if c.StoppedWarps.Active(w) {
					s, _ := k.runner.owners[w].Snapshot()
					v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 10})
					observed[c.Launch.ID] = v
				}
			}
			collect()
		}); err != nil {
			t.Fatal(err)
		}
		collect()
		if !k.Status().Complete || !k.Status().MemoryDrained || k.Status().BackingVisible || !reused {
			t.Fatal("lifecycle", latency, k.Status(), reused)
		}
		measure.cycles = k.Status().Cycle
		measure.refills = counted.globalRefills
		results[tc.name] = measure
		t.Log(tc.name, measure)
		if tc.name == "slow" && !stoppedTail {
			t.Fatal("missing stopped Warp memory tail")
		}
		if done, err := k.MakeVisible(1); err != nil || done {
			t.Fatal("visibility skipped writeback", done, err)
		}
		if done, err := k.MakeVisible(4000); err != nil || !done {
			t.Fatal(done, err)
		}
		for c := uint32(0); c < 6; c++ {
			expected := isa.LaneValues{}
			for lane := uint32(0); lane < 4; lane++ {
				expected[lane] = 10 + c*16 + lane
				var b [4]byte
				if err := ram.Read(0x10c00+c*16+lane*4, b[:]); err != nil {
					t.Fatal(err)
				}
				if got := binary.LittleEndian.Uint32(b[:]); got != expected[lane] {
					t.Fatal("output", latency, c, lane, got, expected[lane])
				}
			}
			if observed[c] != expected {
				t.Fatal("mixed return register", latency, c, observed[c], expected)
			}
		}
	}
	base := results["baseline"]
	if results["repeat"] != base {
		t.Fatal("nondeterministic observations", results)
	}
	if results["slow"].cycles <= base.cycles || results["slow"].serviceEdges <= base.serviceEdges || results["slow"].stalls <= base.stalls {
		t.Fatal("latency had no expected timing effect", results)
	}
	if results["dense"].refills >= base.refills {
		t.Fatal("dense pattern did not reduce misses", results)
	}
	if results["throttled"].cycles <= base.cycles || results["throttled"].requestBlocked <= base.requestBlocked {
		t.Fatal("request backpressure not observed", results)
	}
}
