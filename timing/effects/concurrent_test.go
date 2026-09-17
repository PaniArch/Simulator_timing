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

func TestConcurrentHeldCSRWindowWritesBeforeAcceptance(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	csr := concurrentToken(t, 1, 0x340091f3, 0x100) // csrrw x3,mscratch,x1
	if err := c.Begin(csr.Token); err != nil {
		t.Fatal(err)
	}
	read := csr
	read.Token.ReadMask, read.Token.LastRead = 1, true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	for cycle := uint64(1); cycle < 4; cycle++ {
		concurrentEdge(t, c, cycle, model.CoreReport{CSRRequest: csr, CSRRequestWindow: true})
		if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{0x7777, 0x7777, 0x7777, 0x7777}) {
			t.Fatal("held CSR wrote destination before WB", got)
		}
		if done, err := c.Reap(); err != nil || len(done) != 0 {
			t.Fatal("held CSR retired", done, err)
		}
	}
	concurrentEdge(t, c, 4, model.CoreReport{CSRRequest: csr, CSRRequestWindow: true, Executed: [4]model.Signal{{}, {}, csr}})
	concurrentEdge(t, c, 5, model.CoreReport{Writeback: csr})
	// The accepted result samples MSCRATCH after the prior held windows wrote
	// six. If writes were incorrectly gated by ready, this would return zero.
	if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{6, 6, 6, 6}) {
		t.Fatal("CSR did not re-read after held request writes", got)
	}
	concurrentEdge(t, c, 6, model.CoreReport{PendingRelease: csr})
	if done, err := c.Reap(); err != nil || len(done) != 1 {
		t.Fatal("accepted CSR did not finish once", done, err)
	}
}

func TestConcurrentCSRFlagsOldEdgePriority(t *testing.T) {
	for _, test := range []struct {
		name string
		word uint32
		want uint32
	}{
		{"fflags-write", 0x001011f3, 0},
		{"frm-write-preserves-flags", 0x002111f3, 0x61},
		{"fcsr-write", 0x003011f3, 0},
		{"read-only", 0x001021f3, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				c, owners, _ := concurrentSetup(t)
				fpID, csrID := uint64(1), uint64(2)
				if reverse {
					fpID, csrID = csrID, fpID
				}
				fp := concurrentToken(t, fpID, 0x182081d3, 0x100) // 1/3 sets NX
				csr := concurrentToken(t, csrID, test.word, 0x104)
				admissions := []model.Signal{fp, csr}
				if reverse {
					admissions = []model.Signal{csr, fp}
				}
				for _, s := range admissions {
					if err := c.Begin(s.Token); err != nil {
						t.Fatal(err)
					}
				}

				read := fp
				read.Token.ReadMask, read.Token.LastRead = fp.Token.Used, true
				concurrentEdge(t, c, 0, model.CoreReport{Read: read})
				concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{{}, {}, {}, fp}})
				read = csr
				read.Token.ReadMask, read.Token.LastRead = csr.Token.Used, true
				for n, source := range csr.Token.Sources {
					if source == 0 {
						read.Token.ReadMask &^= 1 << n
					}
				}
				concurrentEdge(t, c, 2, model.CoreReport{Read: read})
				concurrentEdge(t, c, 3, model.CoreReport{Flags: fp, CSRRequest: csr, CSRRequestWindow: true, Executed: [4]model.Signal{{}, {}, csr}})
				if got := snap(t, owners[0]).FCSR(); got != test.want {
					t.Fatal("lost RTL FCSR priority", reverse, got, test.want)
				}
				concurrentEdge(t, c, 4, model.CoreReport{Writeback: csr})
				if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{}) {
					t.Fatal("CSR read forwarded same-edge flags", got)
				}
				if err := c.Observe(5, model.CoreReport{Flags: fp}, [4]state.ReadContext{}); err == nil {
					t.Fatal("combined flags receipt replayed")
				}
			}
		})
	}
}

func TestConcurrentHeldCSRWithFlags(t *testing.T) {
	c, owners, _ := concurrentSetup(t)
	fp := concurrentToken(t, 1, 0x182081d3, 0x100)
	csr := concurrentToken(t, 2, 0x001021f3, 0x104) // read fflags
	for _, signal := range []model.Signal{fp, csr} {
		if err := c.Begin(signal.Token); err != nil {
			t.Fatal(err)
		}
	}
	read := fp
	read.Token.ReadMask, read.Token.LastRead = 3, true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{{}, {}, {}, fp}})
	read = csr
	read.Token.ReadMask, read.Token.LastRead = 0, true
	concurrentEdge(t, c, 2, model.CoreReport{Read: read})
	concurrentEdge(t, c, 3, model.CoreReport{Flags: fp, CSRRequest: csr, CSRRequestWindow: true})
	if got := snap(t, owners[0]).FCSR(); got != 1 {
		t.Fatal("held CSR lost FPU flags", got)
	}
	concurrentEdge(t, c, 4, model.CoreReport{CSRRequest: csr, CSRRequestWindow: true, Executed: [4]model.Signal{{}, {}, csr}})
	concurrentEdge(t, c, 5, model.CoreReport{Writeback: csr})
	if got := reg(t, snap(t, owners[0]), isa.Integer, 3); got != (isa.LaneValues{1, 1, 1, 1}) {
		t.Fatal("accepted CSR did not read next-edge flags", got)
	}
}

func TestConcurrentNonblockingBarrierAndBranchFeedback(t *testing.T) {
	_, owners, ram := concurrentSetup(t)
	if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 15, isa.LaneValues{}); err != nil {
		t.Fatal(err)
	}
	if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 2}, 15, isa.LaneValues{1, 1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c, err := effects.NewConcurrent(owners, 9, ram, [4]effects.ExternalOwner{func(e isa.InstructionEffects) error { calls += len(e.Barriers); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	var word uint32
	for _, entry := range isa.Catalog() {
		if entry.Name == "bar.arrive" {
			word = entry.Example&^uint32(31<<15|31<<20) | 1<<15 | 2<<20
		}
	}
	bar := concurrentToken(t, 1, word, 0x100)
	branch := concurrentToken(t, 2, 0x00000463, 0x104)
	if bar.Token.WarpStall || !branch.Token.WarpStall {
		t.Fatal("test does not represent nonblocking arrive")
	}
	for _, signal := range []model.Signal{bar, branch} {
		if err := c.Begin(signal.Token); err != nil {
			t.Fatal(err)
		}
	}
	read := bar
	read.Token.ReadMask, read.Token.LastRead = bar.Token.Used, true
	concurrentEdge(t, c, 0, model.CoreReport{Read: read})
	read = branch
	read.Token.ReadMask, read.Token.LastRead = 0, true
	concurrentEdge(t, c, 1, model.CoreReport{Read: read})
	concurrentEdge(t, c, 2, model.CoreReport{Executed: [4]model.Signal{branch, {}, bar}})
	concurrentEdge(t, c, 3, model.CoreReport{Branch: branch, Control: bar})
	if calls != 1 || snap(t, owners[0]).PC() != 0x10c {
		t.Fatal("barrier event or branch redirect lost", calls, snap(t, owners[0]).PC())
	}
	concurrentEdge(t, c, 4, model.CoreReport{Writeback: bar})
	concurrentEdge(t, c, 5, model.CoreReport{PendingRelease: bar, Writeback: branch})
	concurrentEdge(t, c, 6, model.CoreReport{PendingRelease: branch})
	if done, err := c.Reap(); err != nil || len(done) != 2 {
		t.Fatal("overlap did not complete both instructions", done, err)
	}
}

func TestConcurrentDelayedNonblockingBarrierFeedback(t *testing.T) {
	for _, younger := range []string{"branch", "alu"} {
		t.Run(younger, func(t *testing.T) {
			_, owners, ram := concurrentSetup(t)
			if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 15, isa.LaneValues{}); err != nil {
				t.Fatal(err)
			}
			if err := owners[0].WriteRegister(isa.Register{File: isa.Integer, Index: 2}, 15, isa.LaneValues{1, 1, 1, 1}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			c, err := effects.NewConcurrent(owners, 9, ram, [4]effects.ExternalOwner{func(e isa.InstructionEffects) error { calls += len(e.Barriers); return nil }})
			if err != nil {
				t.Fatal(err)
			}
			var word uint32
			for _, entry := range isa.Catalog() {
				if entry.Name == "bar.arrive" {
					word = entry.Example&^uint32(31<<15|31<<20) | 1<<15 | 2<<20
				}
			}
			bar := concurrentToken(t, 1, word, 0x100)
			word2 := uint32(0x00000463)
			if younger == "alu" {
				word2 = 0x00700193
			}
			branch := concurrentToken(t, 2, word2, 0x104)
			if bar.Token.WarpStall || (younger == "branch" && !branch.Token.WarpStall) {
				t.Fatal("test does not represent nonblocking arrive")
			}
			for _, signal := range []model.Signal{bar, branch} {
				if err := c.Begin(signal.Token); err != nil {
					t.Fatal(err)
				}
			}
			read := bar
			read.Token.ReadMask, read.Token.LastRead = bar.Token.Used, true
			concurrentEdge(t, c, 0, model.CoreReport{Read: read})
			read = branch
			read.Token.ReadMask, read.Token.LastRead = 0, true
			concurrentEdge(t, c, 1, model.CoreReport{Read: read})
			concurrentEdge(t, c, 2, model.CoreReport{Executed: [4]model.Signal{branch, {}, bar}})
			if c.ControlAllowed(bar, state.ReadContext{PendingLSU: true}) || !c.ControlAllowed(bar, state.ReadContext{}) {
				t.Fatal("LSU admission gate changed")
			}
			if younger == "branch" {
				concurrentEdge(t, c, 3, model.CoreReport{Branch: branch})
			} else {
				concurrentEdge(t, c, 3, model.CoreReport{Writeback: branch})
			}
			report4 := model.CoreReport{}
			if younger == "alu" {
				report4.PendingRelease = branch
			}
			concurrentEdge(t, c, 4, report4)
			before := snap(t, owners[0])
			if calls != 0 {
				t.Fatal("early arrival")
			}
			concurrentEdge(t, c, 5, model.CoreReport{Control: bar})
			if snap(t, owners[0]) != before {
				t.Fatal("late arrival changed canonical state")
			}
			if calls != 1 || snap(t, owners[0]).PC() != map[string]uint32{"branch": 0x10c, "alu": 0x108}[younger] {
				t.Fatal("barrier event or branch redirect lost", calls, snap(t, owners[0]).PC())
			}
			concurrentEdge(t, c, 6, model.CoreReport{Writeback: bar})
			report := model.CoreReport{PendingRelease: bar}
			if younger == "branch" {
				report.Writeback = branch
			}
			concurrentEdge(t, c, 7, report)
			if younger == "branch" {
				concurrentEdge(t, c, 8, model.CoreReport{PendingRelease: branch})
			}
			if done, err := c.Reap(); err != nil || len(done) != 2 {
				t.Fatal("overlap did not complete both instructions", done, err)
			}
			if err := c.Observe(9, model.CoreReport{Control: bar}, [4]state.ReadContext{}); err == nil || calls != 1 {
				t.Fatal("duplicate reaped arrival accepted")
			}
		})
	}
}

func TestConcurrentDelayedArrivalRejectsStaleIdentity(t *testing.T) {
	for _, scenario := range []string{"epoch", "cancelled-residency", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			_, owners, ram := concurrentSetup(t)
			calls := 0
			c, err := effects.NewConcurrent(owners, 9, ram, [4]effects.ExternalOwner{func(isa.InstructionEffects) error { calls++; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			var word uint32
			for _, e := range isa.Catalog() {
				if e.Name == "bar.arrive" {
					word = e.Example&^uint32(31<<15|31<<20) | 1<<15 | 2<<20
				}
			}
			bar := concurrentToken(t, 1, word, 0x100)
			if err = c.Begin(bar.Token); err != nil {
				t.Fatal(err)
			}
			read := bar
			read.Token.ReadMask, read.Token.LastRead = bar.Token.Used, true
			concurrentEdge(t, c, 0, model.CoreReport{Read: read})
			concurrentEdge(t, c, 1, model.CoreReport{Executed: [4]model.Signal{{}, {}, bar}})
			switch scenario {
			case "epoch":
				if err = c.Reset(10); err != nil {
					t.Fatal(err)
				}
			case "cancelled-residency":
				if _, err = c.Cancel(model.Cancellation{Warp: 0, Epoch: 9, Through: 1}); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				concurrentEdge(t, c, 2, model.CoreReport{Control: bar})
			}
			before := snap(t, owners[0])
			beforeCalls := calls
			if err = c.Observe(3, model.CoreReport{Control: bar}, [4]state.ReadContext{}); err == nil {
				t.Fatal("invalid arrival accepted")
			}
			if calls != beforeCalls || snap(t, owners[0]) != before {
				t.Fatal("invalid arrival mutated owner")
			}
		})
	}
}
