package effects_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

func concurrentSetup(t *testing.T) (*effects.Concurrent, [4]*state.WarpState, *memory.Memory) {
	t.Helper()
	var owners [4]*state.WarpState
	for w := range owners {
		init := initial()
		init.WarpID = uint8(w)
		var err error
		owners[w], err = state.NewWarp(init)
		if err != nil {
			t.Fatal(err)
		}
	}
	ram, err := memory.New(512)
	if err != nil {
		t.Fatal(err)
	}
	c, err := effects.NewConcurrent(owners, 9, ram, [4]effects.ExternalOwner{})
	if err != nil {
		t.Fatal(err)
	}
	return c, owners, ram
}
func concurrentToken(t *testing.T, id uint64, word, pc uint32) model.Signal {
	t.Helper()
	s, err := model.DecodeToken(model.Signal{Valid: true, Token: model.Token{ID: id, Epoch: 9, Warp: 0, Word: word, PC: pc, Mask: 15}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func concurrentEdge(t *testing.T, c *effects.Concurrent, cycle uint64, r model.CoreReport) {
	t.Helper()
	if err := c.Observe(cycle, r, [4]state.ReadContext{}); err != nil {
		t.Fatalf("E%d: %v", cycle, err)
	}
}

func TestConcurrentOldEdgeSnapshotAndIndividualReap(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	producer := concurrentToken(t, 1, 0x00900093, 0x100) // x1=9
	reader := concurrentToken(t, 2, 0x000081b3, 0x104)   // x3=x1+x0
	for _, s := range []model.Signal{producer, reader} {
		if err := c.Begin(s.Token); err != nil {
			t.Fatal(err)
		}
	}
	read := producer
	read.Token.LastRead = true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{producer}})
	read = reader
	read.Token.ReadMask = 1
	read.Token.LastRead = true
	// Seed an interface-level simultaneous read/WB to distinguish old-edge
	// sampling from iteration-order forwarding; not a legal RAW issue trace.
	concurrentEdge(t, c, 2, model.CoreReport{Read: read, Writeback: producer})
	concurrentEdge(t, c, 3, model.CoreReport{Executed: [4]model.Signal{reader}, PendingRelease: producer})
	done, err := c.Reap()
	if err != nil || len(done) != 1 || done[0].ID != 1 || c.InFlight() != 1 {
		t.Fatal("individual reap", done, err)
	}
	concurrentEdge(t, c, 4, model.CoreReport{Writeback: reader})
	concurrentEdge(t, c, 5, model.CoreReport{PendingRelease: reader})
	got := snap(t, owners[0])
	values := reg(t, got, isa.Integer, 3)
	if values != (isa.LaneValues{6, 6, 6, 6}) || got.PC() != 0x108 {
		t.Fatal("read observed same-edge write or wrong locked PC", values, got.PC())
	}
	done, err = c.Reap()
	if err != nil || len(done) != 1 || done[0].ID != 2 || c.InFlight() != 0 {
		t.Fatal(done, err)
	}
}

func TestConcurrentPrevalidatesAllIdentitiesBeforeWrites(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	s := concurrentToken(t, 1, 0x00900093, 0x100)
	if err := c.Begin(s.Token); err != nil {
		t.Fatal(err)
	}
	read := s
	read.Token.LastRead = true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{s}})
	before := snap(t, owners[0])
	foreign := s
	foreign.Token.ID = 999
	if err := c.Observe(2, model.CoreReport{Writeback: s, Flags: foreign}, [4]state.ReadContext{}); err == nil {
		t.Fatal("foreign event accepted")
	}
	if snap(t, owners[0]) != before {
		t.Fatal("earlier valid WB applied before later identity check")
	}
	if err := c.Observe(3, model.CoreReport{}, [4]state.ReadContext{}); err == nil {
		t.Fatal("failed edge retried")
	}
	if err := c.Reset(10); err != nil {
		t.Fatal(err)
	}
	if c.InFlight() != 0 || snap(t, owners[0]) != before {
		t.Fatal("reset changed visible state")
	}
	if err := c.Begin(s.Token); err == nil {
		t.Fatal("stale epoch admitted")
	}
	s.Token.Epoch = 10
	if err := c.Begin(s.Token); err != nil {
		t.Fatal(err)
	}
	if err := c.Begin(s.Token); err == nil {
		t.Fatal("duplicate identity admitted")
	}
}

func TestConcurrentPartialLoadAfterNewerTermination(t *testing.T) {
	c, owners, ram := concurrentSetup(t)
	if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 15, isa.LaneValues{64, 68, 72, 76}); err != nil {
		t.Fatal(err)
	}
	for lane := uint32(0); lane < 4; lane++ {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], 100+lane)
		if err := ram.Write(64+4*lane, data[:]); err != nil {
			t.Fatal(err)
		}
	}
	load := concurrentToken(t, 1, 0x0000a183, 0x100)
	tmc := concurrentToken(t, 2, 0x0000000b, 0x104)
	for _, s := range []model.Signal{load, tmc} {
		if err := c.Begin(s.Token); err != nil {
			t.Fatal(err)
		}
	}
	read := load
	read.Token.ReadMask = 1
	read.Token.LastRead = true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{{}, load}})
	read = tmc
	read.Token.LastRead = true
	concurrentEdge(t, c, 2, model.CoreReport{MemoryRequest: load, MemoryAccepted: true, Read: read})
	concurrentEdge(t, c, 3, model.CoreReport{Executed: [4]model.Signal{{}, {}, tmc}})
	concurrentEdge(t, c, 4, model.CoreReport{Control: tmc, Writeback: tmc})
	concurrentEdge(t, c, 5, model.CoreReport{PendingRelease: tmc})
	done, err := c.Reap()
	if err != nil || len(done) != 1 || done[0].ID != 2 {
		t.Fatal(done, err)
	}
	if snap(t, owners[0]).ActiveMask() != 0 {
		t.Fatal("newer termination not visible")
	}
	for n, mask := range []uint8{5, 10} {
		cycle := uint64(6 + 2*n)
		response, err := c.Service(cycle, load.Token, mask)
		if err != nil {
			t.Fatal(err)
		}
		concurrentEdge(t, c, cycle, model.CoreReport{MemoryResponse: response, MemoryResponseReady: true})
		wb := load
		wb.Token.Mask = mask
		wb.Token.End = n == 1
		concurrentEdge(t, c, cycle+1, model.CoreReport{Writeback: wb})
		if snap(t, owners[0]).PC() != 0x108 || snap(t, owners[0]).ActiveMask() != 0 {
			t.Fatal("partial WB restored old control")
		}
	}
	concurrentEdge(t, c, 10, model.CoreReport{PendingRelease: load})
	done, err = c.Reap()
	if err != nil || len(done) != 1 || done[0].ID != 1 || c.InFlight() != 0 {
		t.Fatal(done, err)
	}
	if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{100, 101, 102, 103}) {
		t.Fatal("partial load result", got)
	}
	if _, err = c.Service(11, load.Token, 5); err == nil {
		t.Fatal("serviced reaped identity")
	}
}

func TestConcurrentDrainTracksDecodedAndServiceWork(t *testing.T) {
	c, _, _ := concurrentSetup(t)
	load := concurrentToken(t, 1, 0x0000a183, 0x100)
	control := concurrentToken(t, 3, 0x0000000b, 0x108)
	other := concurrentToken(t, 2, 0x00900093, 0x104)
	other.Token.Warp = 1
	for _, s := range []model.Signal{load, other, control} {
		if err := c.Begin(s.Token); err != nil {
			t.Fatal(err)
		}
	}
	prior, lsu := c.DrainBefore(control.Token)
	if !prior || !lsu {
		t.Fatal("decoded but unexecuted LSU omitted from drain")
	}
	prior, lsu = c.DrainBefore(load.Token)
	if prior || lsu {
		t.Fatal("self/younger instruction counted as prior work")
	}
	probe := control.Token
	probe.Warp = 2
	if prior, lsu = c.DrainBefore(probe); prior || lsu {
		t.Fatal("other Warp blocks drain")
	}
	pending := c.Pending()
	pending[0].Token.ID = 999
	if c.Pending()[0].Token.ID != 1 {
		t.Fatal("pending view aliases mutable engine")
	}
	if err := c.Reset(10); err != nil {
		t.Fatal(err)
	}
	if len(c.Pending()) != 0 {
		t.Fatal("cancelled receipts remain pending")
	}
}

func TestConcurrentCancellationTombstonesUnbegunFetch(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	old := concurrentToken(t, 1, 0x0000a183, 0x100)
	if err := c.Begin(old.Token); err != nil {
		t.Fatal(err)
	}
	before := snap(t, owners[0])
	scope := model.Cancellation{Warp: 0, Epoch: 9, After: 1, Through: 3}
	if removed, err := c.Cancel(scope); err != nil || len(removed) != 0 {
		t.Fatal(removed, err)
	}
	stale := concurrentToken(t, 3, 0x00900093, 0x108)
	if err := c.Begin(stale.Token); err == nil {
		t.Fatal("cancelled outstanding fetch admitted")
	}
	future := concurrentToken(t, 4, 0x00900093, 0x10c)
	if err := c.Begin(future.Token); err != nil {
		t.Fatal("bounded tombstone rejected future work", err)
	}
	other := stale
	other.Token.Warp = 1
	if err := c.Begin(other.Token); err != nil {
		t.Fatal("other warp rejected", err)
	}
	if c.InFlight() != 3 || snap(t, owners[0]) != before {
		t.Fatal("cancellation damaged retained owner/work")
	}
	if removed, err := c.Cancel(model.Cancellation{Warp: 0, Epoch: 9, After: 3, Through: 4}); err != nil || len(removed) != 1 || removed[0].ID != 4 {
		t.Fatal(removed, err)
	}
	if c.InFlight() != 2 {
		t.Fatal("wrong retained contexts")
	}
}
