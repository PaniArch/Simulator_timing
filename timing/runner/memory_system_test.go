package runner_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
	"vortex.local/simulator/timing/runner"
)

func realMemorySetup(t *testing.T) ([4]*state.WarpState, *memory.Memory) {
	owners, ram := multiSetup(t)
	// Warp 1 observes its own dirty cache word before any backing visibility.
	for i, w := range []uint32{0x0040a303, 0x0000000b} {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], w)
		if err := ram.Write(0x20c+uint32(i*4), b[:]); err != nil {
			t.Fatal(err)
		}
	}
	return owners, ram
}

func TestMultiRunnerRealMemory(t *testing.T) {
	refs, ramRef := realMemorySetup(t)
	for _, owner := range refs {
		f, err := warp.NewWithMemory(owner, ramRef)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			s, _ := owner.Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			if e := f.Step(state.ReadContext{}); e.Outcome != warp.OutcomeRetired {
				t.Fatal(e)
			}
		}
	}
	for _, latency := range []uint64{1, 30} {
		owners, ram := realMemorySetup(t)
		r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1,
			FetchCycles: 1000000, MemoryCycles: 1000000, MemoryConfig: &memsys.Config{Latency: latency, AcceptsPerCycle: 1, MaxInflight: 8, ReturnsPerCycle: 1}},
			MemoryDelay: func(model.Token) uint64 { t.Fatal("legacy service callback invoked"); return 0 },
		})
		if err != nil {
			t.Fatal(err)
		}
		fetched, loaded := 0, 0
		waiting, independent := false, false
		err = r.Run(12000, func(rec runner.MultiRecord) {
			if rec.Report.MemoryAccepted && rec.Report.MemoryRequest.Token.Warp == 0 {
				waiting = true
			}
			if waiting && rec.Report.Issued.Valid && rec.Report.Issued.Token.Warp != 0 {
				independent = true
			}
			if rec.Report.Writeback.Valid && rec.Report.Writeback.Token.Warp == 0 && rec.Report.Writeback.Token.PC == 0x100 {
				waiting = false
			}
			if rec.Report.FetchAccepted {
				fetched++
			}
			if rec.Report.MemoryAccepted {
				loaded++
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if latency == 30 && !independent {
			t.Fatal("unrelated warp did not progress during load miss")
		}
		b := make([]byte, 4)
		if err := ram.Read(0x10904, b); err != nil {
			t.Fatal(err)
		}
		if binary.LittleEndian.Uint32(b) == 9 {
			t.Fatal("dirty store bypassed cache into backing")
		}
		if !r.Completed() || fetched == 0 || loaded == 0 {
			t.Fatal("real memory execution incomplete", r.Retired())
		}
		if done, err := r.MakeVisible(1); err != nil || done {
			t.Fatal("visibility skipped real scan", done, err)
		}
		if done, err := r.MakeVisible(10000); err != nil || !done {
			t.Fatal("visibility failed", done, err)
		}
		gotBytes, _ := ram.ReadBytes(0, 0x12000)
		wantBytes, _ := ramRef.ReadBytes(0, 0x12000)
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Fatal("default memory output differs after writeback")
		}
		for w, owner := range owners {
			got, _ := owner.Snapshot()
			want, _ := refs[w].Snapshot()
			if got != want {
				t.Fatalf("latency %d warp %d state mismatch", latency, w)
			}
		}
	}
}

func TestRealMemoryCancelLoadAndRestart(t *testing.T) {
	owners, ram := realMemorySetup(t)
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1}, MemorySystem: &runner.MemorySystemOptions{
		Config: memsys.Config{Latency: 80, AcceptsPerCycle: 1, MaxInflight: 8, ReturnsPerCycle: 1},
		Bind: func(t model.Token) memsys.Identity {
			return memsys.Identity{Kernel: 1, CTA: uint64(t.Warp), WarpGeneration: 1}
		},
		LocalOwner: func(id memsys.Identity) (warp.AtomicMemoryService, error) { return ram, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	admitted := false
	for i := 0; i < 1500 && !admitted; i++ {
		if err = r.Run(1, func(rec runner.MultiRecord) {
			admitted = rec.Report.MemoryAccepted && rec.Report.MemoryRequest.Token.Warp == 0
		}); err != nil {
			t.Fatal(err)
		}
	}
	if !admitted {
		t.Fatal("load not admitted")
	}
	before, _ := owners[0].Snapshot()
	scope := model.Cancellation{Warp: 0, Epoch: 1, Through: 10000}
	if err = r.Cancel(scope); err != nil {
		t.Fatal(err)
	}
	// Restart before the old miss returns. Old cache transport remains alive;
	// its response must not complete the new effects instruction or alter registers.
	if err = r.Restart(0, model.WarpContext{Active: true, PC: before.PC(), Mask: uint8(before.ActiveMask()), Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	if err = r.Run(10000, func(rec runner.MultiRecord) {
		if rec.Report.Writeback.Valid && scope.Matches(rec.Report.Writeback.Token) {
			t.Fatal("cancelled miss wrote back")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() {
		t.Fatal("restart did not complete")
	}
	refs, refRAM := realMemorySetup(t)
	f, err := warp.NewWithMemory(refs[0], refRAM)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if e := f.Step(state.ReadContext{}); e.Outcome != warp.OutcomeRetired {
			t.Fatal(e)
		}
	}
	got, _ := owners[0].Snapshot()
	want, _ := refs[0].Snapshot()
	if got != want {
		t.Fatal("late response damaged restarted state")
	}
}

func TestRealMemoryFenceReturnsOriginalRequest(t *testing.T) {
	setup := func() ([4]*state.WarpState, *memory.Memory) {
		owners, ram := realMemorySetup(t)
		for i, w := range []uint32{0x0000000f, 0x0040a303, 0x0000000b} {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], w)
			if err := ram.Write(0x20c+uint32(4*i), b[:]); err != nil {
				t.Fatal(err)
			}
		}
		return owners, ram
	}
	refs, refRAM := setup()
	for _, o := range refs {
		f, e := warp.NewWithMemory(o, refRAM)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 8; i++ {
			s, _ := o.Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			if out := f.Step(state.ReadContext{}); out.Outcome != warp.OutcomeRetired {
				t.Fatal(out)
			}
		}
	}
	owners, ram := setup()
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1}, MemorySystem: &runner.MemorySystemOptions{
		Config: memsys.Config{Latency: 10, AcceptsPerCycle: 1, MaxInflight: 8, ReturnsPerCycle: 1},
		Bind: func(tok model.Token) memsys.Identity {
			return memsys.Identity{Kernel: 1, CTA: uint64(tok.Warp), WarpGeneration: 1}
		},
		LocalOwner: func(id memsys.Identity) (warp.AtomicMemoryService, error) { return ram, nil },
	}})
	if err != nil {
		t.Fatal(err)
	}
	var fence model.Token
	var mask uint8
	if err = r.Run(15000, func(rec runner.MultiRecord) {
		if rec.Report.MemoryAccepted && rec.Report.MemoryRequest.Token.Path == model.FENCE {
			fence = rec.Report.MemoryRequest.Token
		}
		rsp := rec.Report.MemoryResponse
		if rsp.Valid && rec.Report.MemoryResponseReady && fence.ID != 0 && rsp.ID == fence.ID {
			if rsp.Epoch != fence.Epoch || rsp.Warp != fence.Warp || mask&rsp.Mask != 0 {
				t.Fatal("FENCE identity or repeated response")
			}
			mask |= rsp.Mask
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || mask != 15 {
		t.Fatal("FENCE did not complete", mask)
	}
	for w, o := range owners {
		got, _ := o.Snapshot()
		want, _ := refs[w].Snapshot()
		if got != want {
			t.Fatal("fence program state", w)
		}
	}
	var bytes [4]byte
	if err := ram.Read(0x10904, bytes[:]); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(bytes[:]) != 9 {
		t.Fatal("FENCE failed to write back dirty data")
	}
}
