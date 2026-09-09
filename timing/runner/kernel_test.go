package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func TestKernelLaunchExecutesStartupEntryAndCTAContexts(t *testing.T) {
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	write := func(address, word uint32) {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := ram.Write(address, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	// startup: read launch entry then jump to it.
	write(0x100, 0xce1022f3)
	write(0x104, 0x00028067)
	// entry: parameter pointer, parameter load, CTA block index, per-CTA output
	// address, output value, physical LMEM CSR address, output address, stop.
	for i, w := range []uint32{0x340020f3, 0x0000a103, 0xcd6021f3, 0x00219193, 0x001181b3, 0x0021a823, 0xcdf02273, 0x0241a023, 0x0000000b} {
		write(0x200+uint32(i)*4, w)
	}
	write(0x800, 0x12345678)
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x800, GridDimensions: [3]uint32{4, 1, 1}, BlockDimensions: [3]uint32{1, 1, 1}, BlockSize: 1, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 11, Ready: func(c uint64) bool { return c%3 != 0 }})
	if err != nil {
		t.Fatal(err)
	}
	maxResident := 0
	for i := 0; i < 20 && !k.Status().Complete; i++ {
		if err := k.Run(50, func(r runner.MultiRecord) {
			if n := len(k.Status().Resident); n > maxResident {
				maxResident = n
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !k.Status().Complete || k.Status().Completed != 4 || maxResident < 2 {
		t.Fatal(k.Status(), maxResident)
	}
	for c := uint32(0); c < 4; c++ {
		for address, want := range map[uint32]uint32{0x810 + c*4: 0x12345678, 0x820 + c*4: 0xffff0000 + c*64} {
			var b [4]byte
			if err := ram.Read(address, b[:]); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(b[:]); got != want {
				t.Fatalf("address %#x got %#x want %#x", address, got, want)
			}
		}
	}
}

func TestKernelReentryMultiWarpCoordinatesAndResourceWait(t *testing.T) {
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
	csr := func(rd, address uint32) uint32 { return address<<20 | 2<<12 | rd<<7 | 0x73 }
	shl := func(rd, rs, n uint32) uint32 { return n<<20 | rs<<15 | 1<<12 | rd<<7 | 0x13 }
	add := func(rd, a, b uint32) uint32 { return b<<20 | a<<15 | rd<<7 | 0x33 }
	sw := func(rs, base, offset uint32) uint32 {
		return (offset>>5)<<25 | rs<<20 | base<<15 | 2<<12 | (offset&31)<<7 | 0x23
	}
	// One-time prologue then precisely five instructions. TMC leaves PC=0x114;
	// subsequent CTA dispatch must rewind to 0x100, not run the prologue again.
	put(0xfc, 0x001a0a13)
	for i, w := range []uint32{csr(5, 0xce1), 0x13, 0x000283e7, 0x13, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	words := []uint32{csr(1, 0x340), csr(2, 0xcd6), shl(2, 2, 5), add(1, 1, 2), csr(9, 0xcd1), shl(9, 9, 2), csr(10, 0xcc0), add(9, 9, 10), shl(9, 9, 2), add(1, 1, 9), csr(6, 0xcd3), csr(8, 0xcd4), shl(8, 8, 4), add(6, 6, 8), csr(13, 0xcdf), add(13, 13, 9), sw(6, 13, 0), 0x0006a303, sw(6, 1, 0), sw(20, 1, 0x200), 0x00038067}
	for i, w := range words {
		put(0x200+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: 0xfc, KernelEntryPC: 0x200, ParameterAddress: 0x800, GridDimensions: [3]uint32{6, 1, 1}, BlockDimensions: [3]uint32{3, 2, 1}, BlockSize: 6, WarpStep: [3]uint32{1, 1, 0}, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 8192}
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 80, Ready: func(c uint64) bool { return c%4 == 0 }})
	if err != nil {
		t.Fatal(err)
	}
	waited, reused, tail := false, false, false
	serviceCTA := map[uint64]uint32{}
	maxResident := 0
	if err := k.Run(5000, func(record runner.MultiRecord) {
		status := k.Status()
		var generation [4]uint32
		for _, c := range status.Resident {
			for _, m := range c.Resident.Members {
				generation[m.WarpID] = c.Launch.ID
			}
		}
		if record.Report.MemoryAccepted {
			token := record.Report.MemoryRequest.Token
			serviceCTA[token.ID] = generation[token.Warp]
		}
		for _, service := range record.Services {
			if service.Resource == "memory-service" {
				if want, ok := serviceCTA[service.Token.ID]; !ok || want != generation[service.Token.Warp] {
					t.Fatal("live service lost CTA identity", service)
				}
			}
		}
		waited = waited || status.Waiting
		maxResident = max(maxResident, len(status.Resident))
		for _, c := range status.Resident {
			reused = reused || c.Launch.ID >= 2
			for _, m := range c.Resident.Members {
				if !record.Warps[m.WarpID].Active {
					for _, service := range record.Services {
						if service.Token.Warp == m.WarpID {
							tail = true
						}
					}
				}
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || k.Status().Completed != 6 || !waited || !reused || maxResident != 2 {
		t.Fatal(k.Status(), waited, reused, maxResident)
	}
	if !tail {
		t.Fatal("test did not exercise an inactive Warp with a live service tail")
	}
	for c := uint32(0); c < 6; c++ {
		for lane, want := range []uint32{0, 1, 2, 16, 17, 18, 0, 0} {
			for addr, value := range map[uint32]uint32{0x800 + c*32 + uint32(lane)*4: want, 0xa00 + c*32 + uint32(lane)*4: func() uint32 {
				if lane < 6 {
					return 1
				}
				return 0
			}()} {
				var b [4]byte
				if err := ram.Read(addr, b[:]); err != nil {
					t.Fatal(err)
				}
				if got := binary.LittleEndian.Uint32(b[:]); got != value {
					t.Fatalf("CTA %d lane %d address %#x got %d want %d", c, lane, addr, got, value)
				}
			}
		}
	}
}

func TestKernelClusterWindowPrewrap(t *testing.T) {
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
	for i, w := range []uint32{0xce1022f3, 0x13, 0x000283e7, 0x13, 0xb} {
		put(0x100+uint32(i)*4, w)
	}
	// Output block ID using parameter + block ID * 4, then return to TMC.
	for i, w := range []uint32{0x340020f3, 0xcd602173, 0x00211193, 0x003080b3, 0x0020a023, 0x00038067} {
		put(0x200+uint32(i)*4, w)
	}
	launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x200, ParameterAddress: 0x800, GridDimensions: [3]uint32{6, 1, 1}, BlockDimensions: [3]uint32{1, 1, 1}, BlockSize: 1, ClusterDimensions: [3]uint32{3, 1, 1}, LocalMemorySize: 4096}
	k, err := runner.NewKernel(launch, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint32]bool{}
	waited := false
	if err := k.Run(2000, func(record runner.MultiRecord) {
		status := k.Status()
		waited = waited || status.Waiting
		for _, c := range status.Resident {
			seen[c.Launch.ID] = true
			if c.Resident.ID != c.Launch.ID%3 {
				t.Fatalf("cluster did not prewrap at slot 3: %+v", c)
			}
			if c.Resident.LocalMemory.Address != 0xffff0000+c.Resident.ID*4096 {
				t.Fatal(c)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !k.Status().Complete || len(seen) != 6 || !waited {
		t.Fatal(k.Status(), seen, waited)
	}
	for i := uint32(0); i < 6; i++ {
		var b [4]byte
		if err := ram.Read(0x800+i*4, b[:]); err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint32(b[:]) != i {
			t.Fatal(i, b)
		}
	}
}
