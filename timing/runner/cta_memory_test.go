package runner_test

import (
	"encoding/binary"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

// All four CTAs use identical local addresses. Delayed stores and request
// backpressure must preserve their route while younger independent work runs.
func TestMultiCTAMemoryRoutesThroughTiming(t *testing.T) {
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	for n, word := range []uint32{0x0020a023, 0x0000a203, 0x0041a023, 0x0000000b} {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], word)
		if err := ram.Write(0x100+uint32(n)*4, data[:]); err != nil {
			t.Fatal(err)
		}
	}
	manager := core.NewCTAManager()
	var owners [4]*state.WarpState
	options := runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 7,
		Ready: func(c uint64) bool { return c%5 == 0 }}, MemoryDelay: func(token model.Token) uint64 { return 11 + uint64(token.Warp)*9 }}
	for w := uint8(0); w < 4; w++ {
		_, err := manager.Admit(core.CTAConfig{ID: uint32(w), WarpIDs: []uint8{w}, StartupPC: 0x100,
			BlockID: [3]uint32{uint32(w), 0, 0}, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{4, 1, 1},
			BlockSize: 4, WarpStep: [3]uint32{4, 0, 0}, LocalMemorySize: 64, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		options.DataMemory[w], err = core.NewCTAMemory(manager, w, ram)
		if err != nil {
			t.Fatal(err)
		}
		initial := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
		for lane := uint8(0); lane < 4; lane++ {
			l := state.LaneInitial{ID: lane}
			l.GPR[1] = isa.FrozenLocalMemBase + uint32(lane)*4
			l.GPR[2] = 100 + uint32(w)*16 + uint32(lane)
			l.GPR[3] = 0x800 + uint32(w)*16 + uint32(lane)*4
			initial.Lanes = append(initial.Lanes, l)
		}
		owners[w], err = state.NewWarp(initial)
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := runner.NewMulti(owners, ram, options)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(2000, nil); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("timing execution did not complete")
	}
	for w := uint8(0); w < 4; w++ {
		for lane := uint8(0); lane < 4; lane++ {
			var data [4]byte
			if err := ram.Read(0x800+uint32(w)*16+uint32(lane)*4, data[:]); err != nil {
				t.Fatal(err)
			}
			want := 100 + uint32(w)*16 + uint32(lane)
			if got := binary.LittleEndian.Uint32(data[:]); got != want {
				t.Fatalf("CTA %d lane %d global=%d want=%d", w, lane, got, want)
			}
			if err := options.DataMemory[w].Read(isa.FrozenLocalMemBase+uint32(lane)*4, data[:]); err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(data[:]); got != want {
				t.Fatalf("CTA %d local alias: %d", w, got)
			}
		}
	}
}
