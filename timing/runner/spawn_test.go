package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func TestMultiRunnerExplicitSpawnOwners(t *testing.T) {
	for _, bind := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound-blocks", true: "bound-activates"}[bind], func(t *testing.T) {
			var owners [4]*state.WarpState
			for w := range owners {
				init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: uint8(w), PC: 0x100, Lifecycle: state.WarpInactive}
				if w == 0 {
					init.ActiveMask = 15
					init.Lifecycle = state.WarpRunning
				}
				for lane := uint8(0); lane < 4; lane++ {
					init.Lanes = append(init.Lanes, state.LaneInitial{ID: lane})
				}
				var err error
				owners[w], err = state.NewWarp(init)
				if err != nil {
					t.Fatal(err)
				}
			}
			ram, err := memory.New(4096)
			if err != nil {
				t.Fatal(err)
			}
			spawn := customWord(t, "wspawn", 0, 1) | 2<<20
			for base, program := range map[uint32][]uint32{0x100: {0x00400093, 0x20000113, spawn, customWord(t, "tmc", 0, 0)}, 0x200: {0x00700193, customWord(t, "tmc", 0, 0)}} {
				for n, word := range program {
					var data [4]byte
					binary.LittleEndian.PutUint32(data[:], word)
					if err = ram.Write(base+uint32(n*4), data[:]); err != nil {
						t.Fatal(err)
					}
				}
			}
			options := runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 10}}
			if bind {
				options.Spawn = func(tok model.Token) (effects.SpawnBinding, error) {
					b := effects.SpawnBinding{Active: 1}
					for w := 1; w < 4; w++ {
						s, _ := owners[w].Snapshot()
						b.Targets = append(b.Targets, state.WarpSpawnTarget{WarpID: uint8(w), Owner: owners[w], Expected: s})
					}
					return b, nil
				}
			}
			r, err := runner.NewMulti(owners, ram, options)
			if err != nil {
				t.Fatal(err)
			}
			executed, sideband, activation := uint64(0), uint64(0), uint64(0)
			if err = r.Run(300, func(rec runner.MultiRecord) {
				if e := rec.Report.Executed[2]; e.Valid && e.Token.Word == spawn {
					executed = rec.Cycle
				}
				if e := rec.Report.Control; e.Valid && e.Token.Word == spawn {
					sideband = rec.Cycle
				}
				for _, f := range rec.Report.Wakeups {
					if f.Kind == model.FeedbackSpawn {
						activation = rec.Cycle
						for w := 1; w < 4; w++ {
							s, _ := owners[w].Snapshot()
							if s.PC() != 0x200 || s.ActiveMask() != 1 {
								t.Fatal("spawn owners not atomically visible", w, s)
							}
						}
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			if !bind {
				if r.Completed() || executed != 0 || r.InFlight() == 0 {
					t.Fatal("unbound spawn escaped gate")
				}
				return
			}
			if !r.Completed() || executed == 0 || sideband != executed+1 || activation != sideband+1 {
				t.Fatal("spawn registers/completion", executed, sideband, activation, r.Retired())
			}
			for w := 1; w < 4; w++ {
				s, _ := owners[w].Snapshot()
				v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
				if s.ActiveMask() != 0 || s.PC() != 0x208 || v != (isa.LaneValues{7, 0, 0, 0}) {
					t.Fatal("spawned warp result", w, v, s.PC())
				}
			}
		})
	}
}
