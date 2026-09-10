package runner

import (
	"encoding/binary"
	"errors"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

type recoveryReadFault struct {
	warp.AtomicMemoryService
	fail  bool
	reads int
}

func (m *recoveryReadFault) Read(address uint32, dst []byte) error {
	if address == 0x800 {
		m.reads++
		if m.fail {
			m.fail = false
			return errors.New("injected refill fault")
		}
	}
	return m.AtomicMemoryService.Read(address, dst)
}

func TestMemoryFaultResetAlignsCommittedEdges(t *testing.T) {
	for _, afterMemory := range []bool{false, true} {
		t.Run(map[bool]string{false: "response-before-step", true: "effects-after-step"}[afterMemory], func(t *testing.T) {
			ram, _ := memory.New(4096)
			put := func(addr, word uint32) {
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], word)
				if err := ram.Write(addr, b[:]); err != nil {
					t.Fatal(err)
				}
			}
			put(0x800, 42)
			put(0x100, 0x0000a183) // lw x3,0(x1)
			put(0x104, 0x00118213) // addi x4,x3,1: keep TMC behind the faulting load
			put(0x108, 0x0000000b)
			if afterMemory {
				found := false
				for _, entry := range isa.Catalog() {
					if entry.Name == "wsync" {
						put(0x104, entry.Example&^uint32(31<<7|31<<15|31<<20))
						found = true
					}
				}
				if !found {
					t.Fatal("missing wsync")
				}
				put(0x108, 0x0000000b)
			}
			var owners [4]*state.WarpState
			for w := uint8(0); w < 4; w++ {
				init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, PC: 0x100, Lifecycle: state.WarpInactive}
				if w == 0 {
					init.ActiveMask = 15
					init.Lifecycle = state.WarpRunning
				}
				for l := uint8(0); l < 4; l++ {
					lane := state.LaneInitial{ID: l}
					lane.GPR[1] = 0x800
					init.Lanes = append(init.Lanes, lane)
				}
				var err error
				owners[w], err = state.NewWarp(init)
				if err != nil {
					t.Fatal(err)
				}
			}
			owner := &recoveryReadFault{AtomicMemoryService: ram, fail: !afterMemory}
			options := MultiOptions{Options: Options{Backend: "std", PeriodPS: 1, MemoryConfig: &memsys.Config{Latency: 10, AcceptsPerCycle: 1, MaxInflight: 8, ReturnsPerCycle: 1}}}
			injected := false
			if afterMemory {
				options.External = func(e isa.InstructionEffects) error {
					if len(e.WarpDrains) > 0 && !injected {
						injected = true
						return errors.New("injected ordering completion fault")
					}
					return nil
				}
			}
			r, err := NewMulti(owners, owner, options)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Run(3000, nil); err == nil {
				t.Fatal("fault did not stop execution")
			}
			cycle := r.Cycle()
			next, err := r.hierarchy.system.NextCycle()
			if err != nil {
				t.Fatal(err)
			}
			want := cycle
			if afterMemory {
				want++
			}
			if next != want {
				t.Fatal("wrong fault edge fixture", cycle, next)
			}
			before, _ := owners[0].Snapshot()
			if err = r.Flush(); err != nil {
				t.Fatal(err)
			}
			after, _ := owners[0].Snapshot()
			if before != after || r.Cycle() != next {
				t.Fatal("reset changed owner or skipped wrong edge")
			}
			if err = r.Run(3000, nil); err != nil {
				t.Fatal(err)
			}
			if !r.Completed() {
				t.Fatal("recovery did not finish")
			}
			final, _ := owners[0].Snapshot()
			value, _ := final.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
			if value != (isa.LaneValues{42, 42, 42, 42}) {
				t.Fatal("recovered load data", value)
			}
			// Abandoning a budgeted software visibility request also retains its
			// cache scan/backend ticket and drains its reply before completion.
			if done, err := r.MakeVisible(1); err != nil || done {
				t.Fatal("visibility fixture", done, err)
			}
			if err = r.Flush(); err != nil {
				t.Fatal(err)
			}
			if err = r.Run(3000, nil); err != nil {
				t.Fatal(err)
			}
			if !r.Completed() || r.memoryVisible {
				t.Fatal("abandoned visibility leaked or claimed success")
			}
			if !afterMemory && owner.reads != 2 {
				t.Fatal("refill replay count", owner.reads)
			}
		})
	}
}
