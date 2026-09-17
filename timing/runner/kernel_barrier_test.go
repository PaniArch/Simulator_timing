package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func TestKernelRepeatedBarrierLocalExchange(t *testing.T) {
	for _, tc := range []struct {
		name           string
		delay, period  uint64
		startup, entry uint32
	}{
		{"fast", 4, 1, 0x100, 0x200}, {"backpressure", 40, 3, 0x400, 0x600}, {"store-tail", 120, 5, 0x100, 0x300},
	} {
		t.Run(tc.name, func(t *testing.T) { kernelExchange(t, tc.delay, tc.period, tc.startup, tc.entry) })
	}
}
func kernelExchange(t *testing.T, delay, period uint64, startup, entry uint32) {
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	put := func(pc, word uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := ram.Write(pc, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	csr := func(rd, a uint32) uint32 { return a<<20 | 2<<12 | rd<<7 | 0x73 }
	add := func(rd, a, b uint32) uint32 { return b<<20 | a<<15 | rd<<7 | 0x33 }
	shl := func(rd, a, n uint32) uint32 { return n<<20 | a<<15 | 1<<12 | rd<<7 | 0x13 }
	imm := func(rd, a, n uint32) uint32 { return n<<20 | a<<15 | rd<<7 | 0x13 }
	store := func(rs, base, offset uint32) uint32 {
		return (offset>>5)<<25 | rs<<20 | base<<15 | 2<<12 | (offset&31)<<7 | 0x23
	}
	for i, w := range []uint32{csr(5, 0xce1), 0x13, 0x000283e7, 0x13, 0xb} {
		put(startup+uint32(i)*4, w)
	}
	words := []uint32{csr(1, 0x340), 0x0000a583, imm(1, 1, 16), csr(2, 0xcd6), shl(12, 2, 4), shl(2, 2, 6), add(1, 1, 2), csr(3, 0xcd1), csr(4, 0xcc0), shl(4, 4, 2), shl(5, 3, 4), add(5, 5, 4), add(1, 1, 5), csr(6, 0xcdf), add(6, 6, 5), 0x0011c413, shl(8, 8, 4), add(8, 8, 4), csr(14, 0xcdf), add(8, 8, 14), csr(9, 0xcc1), 0x403484b3, imm(10, 0, 2), add(11, 11, 3), add(11, 11, 12)}
	bar := customWord(t, "bar", 0, 9) | 10<<20
	for round := uint32(0); round < 2; round++ {
		words = append(words, store(11, 6, 0), bar, 0x00042683, store(13, 1, round*256), bar, imm(11, 11, 1))
	}
	words = append(words, 0x00038067)
	for i, w := range words {
		put(entry+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: startup, KernelEntryPC: entry, ParameterAddress: 0x7f0, GridDimensions: [3]uint32{4, 1, 1}, BlockDimensions: [3]uint32{8, 1, 1}, BlockSize: 8, WarpStep: [3]uint32{4, 0, 0}, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
	put(0x7f0, 10)
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: kernelMemoryConfig(delay), Ready: func(c uint64) bool { return c%period == 0 }})
	if err != nil {
		t.Fatal(err)
	}
	// The backpressured case also crosses a native-style D/I flush boundary.
	// Change the input between launches so stale Cache/LMEM/barrier state cannot
	// silently reproduce the expected output from the first launch.
	launches := 1
	if delay == 40 {
		launches = 2
	}
	for run := 0; run < launches; run++ {
		wakeCount := 0
		overlap := false
		memoryOverlap := false
		tail := false
		lastService := map[uint32]uint64{}
		counts := map[string]map[uint32]int{}
		collect := func(events []runner.KernelEvent, record runner.MultiRecord) {
			for _, event := range events {
				if counts[event.Kind] == nil {
					counts[event.Kind] = map[uint32]int{}
				}
				counts[event.Kind][event.CTA]++
				if event.Kind == "reclaimed" && event.Cycle <= lastService[event.CTA] {
					t.Fatal("CTA reclaimed before byte service", event, lastService[event.CTA])
				}
				if event.Kind == "admitted" && event.CTA >= 2 {
					for _, other := range k.Status().Resident {
						if other.Launch.ID < event.CTA {
							memoryOverlap = memoryOverlap || other.MemoryPending
							for _, member := range other.Resident.Members {
								overlap = overlap || record.Warps[member.WarpID].HardwarePending != 0
								for _, service := range record.Services {
									overlap = overlap || service.Token.Warp == member.WarpID
								}
							}
						}
					}
				}
			}
		}
		if err := k.Run(5000, func(r runner.MultiRecord) {
			for _, cta := range k.Status().Resident {
				for _, member := range cta.Resident.Members {
					for _, service := range r.Services {
						if service.Resource == "memory-system" && service.Token.Warp == member.WarpID {
							lastService[cta.Launch.ID] = max(lastService[cta.Launch.ID], r.Cycle)
							tail = tail || !r.Warps[member.WarpID].Active
						}
					}
				}
			}
			if len(r.Services) != 0 && k.Status().Complete {
				t.Fatal("Kernel completed with a live service")
			}
			collect(k.TakeEvents(), r)
			for _, f := range r.Report.Wakeups {
				if f.Kind == "external-wake" {
					wakeCount++
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if !k.Status().Complete || wakeCount != 32 {
			t.Fatal(k.Status(), wakeCount)
		}
		collect(k.TakeEvents(), runner.MultiRecord{})
		if delay >= 120 && !memoryOverlap {
			t.Fatal("CTA reuse never overlapped another CTA memory tail")
		}
		if delay >= 120 && !tail {
			t.Fatal("long-delay case missed store tail")
		}
		if !overlap {
			t.Fatal("no admission overlapped older CTA work")
		}
		for _, kind := range []string{"generated", "admitted", "reclaimed"} {
			for c := uint32(0); c < 4; c++ {
				if counts[kind][c] != 1 {
					t.Fatal("CTA event count", kind, c, counts)
				}
			}
		}
		for c := uint32(0); c < 4; c++ {
			for rank := uint32(0); rank < 2; rank++ {
				for lane := uint32(0); lane < 4; lane++ {
					for round := uint32(0); round < 2; round++ {
						address := uint32(0x800) + c*64 + rank*16 + lane*4 + round*256
						var b [4]byte
						kernelVisible(t, k)
						if err := ram.Read(address, b[:]); err != nil {
							t.Fatal(err)
						}
						want := uint32(10+100*run) + (rank ^ 1) + c*16 + round
						if got := binary.LittleEndian.Uint32(b[:]); got != want {
							t.Fatalf("CTA %d rank %d lane %d round %d got %d want %d", c, rank, lane, round, got, want)
						}
					}
				}
			}
		}
		if run+1 < launches {
			if done, err := k.FlushCaches(4000); err != nil || !done {
				t.Fatal(done, err)
			}
			put(0x7f0, uint32(10+100*(run+1)))
			k, err = k.NextLaunch(launch)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
