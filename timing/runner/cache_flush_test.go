package runner

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

type flushWriteFault struct {
	warp.AtomicMemoryService
	fail bool
}

func (m *flushWriteFault) WriteBatch(a []uint32, d [][]byte) error {
	if m.fail {
		return errors.New("injected cache flush writeback failure")
	}
	return m.AtomicMemoryService.WriteBatch(a, d)
}

func TestRuntimeCacheFlushReplacesCachedInstructions(t *testing.T) {
	for _, latency := range []uint64{3, 31, 7} {
		t.Run(fmt.Sprint(latency), func(t *testing.T) {
			ram, _ := memory.New(4096)
			old, updated := uint32(0x00100113), uint32(0x00900113)       // addi x2,1 / addi x2,9
			for i, word := range []uint32{old, 0x0030a023, 0x0000000b} { // sw x3,0(x1); stop
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], word)
				if err := ram.Write(0x100+uint32(i)*4, b[:]); err != nil {
					t.Fatal(err)
				}
			}
			initial := state.WarpInitial{Topology: state.FrozenTopology(), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
			for l := uint8(0); l < 4; l++ {
				lane := state.LaneInitial{ID: l}
				lane.GPR[1] = 0x100
				lane.GPR[3] = updated
				initial.Lanes = append(initial.Lanes, lane)
			}
			owner, err := state.NewWarp(initial)
			if err != nil {
				t.Fatal(err)
			}
			config, _ := memsys.DefaultConfig()
			config.Latency = latency
			backing := &flushWriteFault{AtomicMemoryService: ram}
			r, err := New(owner, backing, Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.FlushCaches(1); err == nil {
				t.Fatal("running execution silently drained")
			}
			execute := func(want uint32) {
				t.Helper()
				if err := r.Run(2000, nil); err != nil {
					t.Fatal(err)
				}
				s, _ := owner.Snapshot()
				v, _ := s.ReadRegister(isa.Register{File: isa.Integer, Index: 2})
				if !r.Completed() || v != (isa.LaneValues{want, want, want, want}) {
					t.Fatal("instruction data", r.Completed(), v, want)
				}
			}
			restart := func() {
				t.Helper()
				if err := owner.SetPC(0x100); err != nil {
					t.Fatal(err)
				}
				if err := owner.SetActiveMask(15, state.WarpRunning); err != nil {
					t.Fatal(err)
				}
				if err := r.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			word := func() uint32 {
				var b [4]byte
				if err := ram.Read(0x100, b[:]); err != nil {
					t.Fatal(err)
				}
				return binary.LittleEndian.Uint32(b[:])
			}
			execute(1)
			if word() != old {
				t.Fatal("store bypassed D cache")
			}
			if done, err := r.MakeVisible(4000); err != nil || !done {
				t.Fatal(done, err)
			}
			if word() != updated {
				t.Fatal("D writeback missing")
			}
			restart()
			execute(1) // MakeVisible and epoch Flush do not invalidate I.
			before, _ := owner.Snapshot()
			retired := r.Retired()
			coreCycles := r.multi.core.Cycles()
			backing.fail = latency == 7
			dDone, iAccepted := uint64(0), uint64(0)
			finished := false
			for n := 0; n < 4000; n++ {
				cycle := r.Cycle()
				priorD := r.multi.cacheFlush != nil && r.multi.cacheFlush.dataDone
				prior := r.multi.cacheFlush
				done, err := r.FlushCaches(1)
				if err != nil {
					if !backing.fail {
						t.Fatal(err)
					}
					if done || prior == nil || prior.dataDone || prior.instructionSent || !r.multi.failed {
						t.Fatal("failed D flush reported completion or started I", done, prior)
					}
					if _, err := r.FlushCaches(1); err == nil {
						t.Fatal("failed operation silently retried")
					}
					return
				}
				if done {
					if dDone == 0 || iAccepted <= dDone || cycle <= iAccepted {
						t.Fatal("joint completion order", dDone, iAccepted, cycle)
					}
					finished = true
					break
				}
				f := r.multi.cacheFlush
				if f.dataDone && dDone == 0 {
					dDone = cycle
				}
				if f.instructionSent && iAccepted == 0 {
					if !priorD {
						t.Fatal("I launched without old-edge D done")
					}
					iAccepted = cycle
				}
				if n == 0 {
					if err := r.multi.DispatchWarp(1, 0x100, 0, 15, true); err == nil {
						t.Fatal("dispatch stole cache flush")
					}
					if err := r.Flush(); err == nil {
						t.Fatal("reset stole flush")
					}
					if _, err := r.MakeVisible(1); err == nil {
						t.Fatal("visibility stole flush")
					}
					if err := r.Run(1, nil); err == nil {
						t.Fatal("Run stole flush")
					}
				}
			}
			after, _ := owner.Snapshot()
			if backing.fail {
				t.Fatal("writeback fault not observed")
			}
			if !finished || before != after || retired != r.Retired() || coreCycles != r.multi.core.Cycles() {
				t.Fatal("flush advanced architecture or failed")
			}
			restart()
			execute(9)
			// A completed operation rearms; the next call cannot reuse stale done.
			if done, err := r.FlushCaches(1); err != nil || done {
				t.Fatal("stale combined done", done, err)
			}
			if done, err := r.FlushCaches(4000); err != nil || !done {
				t.Fatal(done, err)
			}
		})
	}
}
