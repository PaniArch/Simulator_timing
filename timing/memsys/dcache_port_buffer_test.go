package memsys

import (
	"testing"

	"vortex.local/simulator/emu/warp"
)

func TestDCachePortBufferEdges(t *testing.T) {
	b, err := newDCachePortBuffer()
	if err != nil {
		t.Fatal(err)
	}
	a, c, d := WordOffer{true, word(1, 0)}, WordOffer{true, word(2, 128)}, WordOffer{true, word(3, 256)}
	// The full edge pops A but must reject D; replacement is legal only with
	// old occupancy one. Distinct payloads catch dropped/repeated/stale slots.
	cases := []struct {
		in, want    WordOffer
		pop, accept bool
		size        int
	}{
		{a, WordOffer{}, false, true, 1},
		{c, a, false, true, 2},
		{d, a, false, false, 2},
		{d, a, true, false, 1},
		{d, c, true, true, 1},
		{WordOffer{}, d, true, false, 0},
	}
	for edge, tc := range cases {
		core := []WordOffer{tc.in, c}
		out, flush := b.output(core)
		if out[0] != tc.want || out[1] != c || flush.Valid {
			t.Fatalf("edge %d output=%+v flush=%+v", edge, out, flush)
		}
		accepted, fa := b.advance(core, FlushOffer{}, CacheEdge{Accepted: []bool{tc.pop, true}})
		if accepted[0] != tc.accept || !accepted[1] || fa || len(b.queue.values) != tc.size {
			t.Fatalf("edge %d accept=%v size=%d", edge, accepted, len(b.queue.values))
		}
		if b.Drained() != (tc.size == 0) {
			t.Fatalf("edge %d early drain", edge)
		}
	}
}

func TestDCachePortBufferFlushSharesSlotsAndRetainsIdentity(t *testing.T) {
	b, err := newDCachePortBuffer()
	if err != nil {
		t.Fatal(err)
	}
	w := WordOffer{true, word(1, 0)}
	w.Request.Identity.Kernel, w.Request.Identity.CTA = 9, 7
	f := FlushOffer{Valid: true, Identity: Identity{Kernel: 9, CTA: 8, Transaction: 1}, Tag: 19}
	noCache := CacheEdge{Accepted: []bool{false, false}}
	core := []WordOffer{w, {}}
	accepted, fa := b.advance(core, f, noCache)
	if !accepted[0] || fa {
		t.Fatal("core did not win flush arbitration")
	}
	changed := f
	changed.Tag++
	if b.validate(changed) == nil {
		t.Fatal("changed backpressured flush accepted")
	}
	if err := b.validate(f); err != nil {
		t.Fatal(err)
	}
	core[0] = WordOffer{}
	_, fa = b.advance(core, f, noCache)
	if !fa || !b.HasResidency(9, 7) || !b.HasResidency(9, 8) || b.Drained() {
		t.Fatal("queued ownership lost")
	}
	out, flush := b.output(core)
	if out[0] != w || flush.Valid {
		t.Fatal("flush overtook store")
	}
	b.advance(core, FlushOffer{}, CacheEdge{Accepted: []bool{true, false}})
	out, flush = b.output(core)
	if out[0].Valid || flush != f {
		t.Fatal("synthetic flush not in registered slot")
	}
	b.advance(core, FlushOffer{}, CacheEdge{Accepted: []bool{false, false}, FlushAccepted: true})
	if b.Drained() || !b.HasResidency(9, 8) || b.HasResidency(9, 7) {
		t.Fatal("flush acceptance released response tail")
	}
	b.advance(core, FlushOffer{}, CacheEdge{Accepted: []bool{false, false}, Flush: FlushReply{Valid: true}})
	if b.Drained() {
		t.Fatal("backpressured flush completed early")
	}
	b.advance(core, FlushOffer{}, CacheEdge{Accepted: []bool{false, false}, Flush: FlushReply{Valid: true, Delivered: true}})
	if !b.Drained() || b.HasResidency(9, 8) {
		t.Fatal("flush tail leaked")
	}
	if b.validate(f) == nil {
		t.Fatal("repeated flush accepted")
	}
}

func newPortSystem(t *testing.T) (*System, func(SystemInput) SystemEdge) {
	t.Helper()
	_, owner := setup(t, Config{100, 1, 16, 1}, 1, 1<<20)
	s, err := NewSystem(owner, func(Identity) (warp.AtomicMemoryService, error) { return owner, nil }, Config{100, 1, 16, 1})
	if err != nil {
		t.Fatal(err)
	}
	var cycle uint64
	step := func(in SystemInput) SystemEdge {
		t.Helper()
		e, err := s.Step(cycle, in)
		if err != nil {
			t.Fatalf("edge %d: %v", cycle, err)
		}
		cycle++
		return e
	}
	for i := 0; i < 100; i++ {
		step(SystemInput{})
	}
	return s, step
}

// Minimal production trace of runtime §10.3: four non-coalescing lane
// addresses hit the same bank. Port 1 arrives first, port 0 one edge later;
// the two WAIT/SEND batches become four consecutive Cache acceptances.
func TestSystemDFlushPortPhase(t *testing.T) {
	for _, write := range []bool{false, true} {
		s, step := newPortSystem(t)
		r := simd(1)
		r.Write = write
		for lane := 0; lane < 4; lane++ {
			r.Lanes[lane] = LaneRequest{Address: uint32(lane * 128), ByteEnable: 15, Data: [4]byte{byte(lane + 1)}}
		}
		accepted, complete := false, false
		var ports, edges []int
		var adapter0, cache0 int
		for i := 0; i < 1000; i++ {
			e := step(SystemInput{Memory: SIMDOffer{!accepted, r}, MemoryReady: i%5 == 0})
			accepted = accepted || e.MemoryAccepted
			if e.DataAdapterAccepted[0] {
				adapter0++
			}
			if e.DataCacheAccepted[0] {
				cache0++
			}
			for p, a := range e.DataCacheAccepted {
				if a {
					ports = append(ports, p)
					edges = append(edges, i)
				}
			}
			if adapter0 > cache0 && (!s.HasResidency(r.Identity.Kernel, r.Identity.CTA) || s.Drained()) {
				t.Fatal("buffered request lost residency")
			}
			if len(e.Complete) > 0 {
				if complete || len(e.Complete) != 1 || e.Complete[0] != r.Identity {
					t.Fatal("duplicate/foreign complete")
				}
				complete = true
			}
			if complete && s.Drained() {
				break
			}
		}
		if !complete || !s.Drained() || adapter0 != 2 || cache0 != 2 || len(ports) != 4 {
			t.Fatalf("write=%v incomplete trace %v %v", write, ports, edges)
		}
		for i, p := range []int{1, 0, 1, 0} {
			if ports[i] != p || edges[i] != edges[0]+i {
				t.Fatalf("write=%v phase ports=%v edges=%v", write, ports, edges)
			}
		}
	}
}

func TestSystemDFlushInlineAndIndependent(t *testing.T) {
	for _, inline := range []bool{false, true} {
		s, step := newPortSystem(t)
		r := simd(1)
		r.Mask = 1
		r.Flush = inline
		r.Write = !inline
		r.Lanes[0] = LaneRequest{Address: 0, ByteEnable: 15, Data: [4]byte{7}}
		f := FlushOffer{Valid: true, Identity: Identity{Kernel: 8, CTA: 9, Transaction: 1}, Tag: 77}
		accepted, complete, fa, fd := false, false, false, false
		queueEdge, cacheEdge := -1, -1
		for i := 0; i < 1500; i++ {
			in := SystemInput{Memory: SIMDOffer{!accepted, r}, MemoryReady: true, DataFlushReady: i%5 == 0}
			// Start independent flush with the store already accepted by adapter,
			// while its Cache application/response tail has not yet drained.
			if !inline && accepted && !fa {
				in.DataFlush = f
			}
			e := step(in)
			accepted = accepted || e.MemoryAccepted
			if e.DataFlushAccepted {
				fa = true
				queueEdge = i
			}
			if e.DataCacheFlushAccepted {
				cacheEdge = i
			}
			if e.DataFlush.Delivered {
				fd = true
				if e.DataFlush.Err != nil || e.DataFlush.Identity != f.Identity {
					t.Fatal("flush identity/error")
				}
			}
			if len(e.Complete) > 0 {
				if complete {
					t.Fatal("duplicate completion")
				}
				complete = true
			}
			if complete && (inline || fd) && s.Drained() {
				break
			}
		}
		if !complete || !s.Drained() {
			t.Fatal("flush/request tail failed to drain")
		}
		if inline {
			if fa || fd {
				t.Fatal("inline flush escaped word path")
			}
		} else if !fd || cacheEdge <= queueEdge {
			t.Fatalf("independent flush bypassed register: queue=%d cache=%d", queueEdge, cacheEdge)
		}
	}
}

// Initialization supplies real Cache backpressure: the global port 0 word is
// accepted into b-dflush while port 1 is held and LMEM independently applies.
func TestSystemDFlushMixedPartialAcceptance(t *testing.T) {
	local := newLocalHarness(t)
	_, owner := setup(t, Config{100, 1, 16, 1}, 1, 1<<20)
	s, err := NewSystem(owner, func(Identity) (warp.AtomicMemoryService, error) { return local.routes[0], nil }, Config{100, 1, 16, 1})
	if err != nil {
		t.Fatal(err)
	}
	r := simd(1)
	r.Identity.CTA = 0
	r.Identity.WarpGeneration = 1
	r.Write = true
	for lane := 0; lane < 4; lane++ {
		r.Lanes[lane] = LaneRequest{Address: uint32(lane * 128), ByteEnable: 15, Data: [4]byte{byte(lane + 10)}}
	}
	r.Lanes[1].Local = true
	r.Lanes[1].Address = local.addresses[0]
	accepted, partial, localEarly, complete := false, false, false, false
	var applied uint8
	cacheStarted := false
	for cycle := uint64(0); cycle < 1200; cycle++ {
		e, err := s.Step(cycle, SystemInput{Memory: SIMDOffer{!accepted, r}, MemoryReady: true})
		if err != nil {
			t.Fatalf("edge %d: %v", cycle, err)
		}
		accepted = accepted || e.MemoryAccepted
		cacheStarted = cacheStarted || e.DataCacheAccepted[0] || e.DataCacheAccepted[1]
		if e.DataAdapterAccepted[0] && !e.DataAdapterAccepted[1] && !e.DataCacheAccepted[0] {
			partial = true
		}
		for _, st := range e.Stores {
			if st.Identity != r.Identity || applied&st.Mask != 0 {
				t.Fatal("mixed subset replay/identity loss")
			}
			for lane, err := range st.Errors {
				if st.Mask&(1<<lane) != 0 && err != nil {
					t.Fatal(err)
				}
			}
			applied |= st.Mask
			if st.Mask&2 != 0 && !cacheStarted {
				localEarly = true
			}
		}
		for _, id := range e.Complete {
			if id != r.Identity || complete || applied != 15 || !accepted {
				t.Fatal("early/duplicate completion")
			}
			complete = true
		}
		if !complete && (!s.HasResidency(r.Identity.Kernel, r.Identity.CTA) || s.Drained()) {
			t.Fatal("partial transport lost residency")
		}
		if complete && s.Drained() {
			break
		}
	}
	if !partial || !localEarly || !complete || !s.Drained() {
		t.Fatalf("partial=%v localEarly=%v complete=%v", partial, localEarly, complete)
	}
}

func TestSystemFaultRetainsBufferedRequest(t *testing.T) {
	_, owner := setup(t, Config{100, 1, 16, 1}, 1, 1<<20)
	s, err := NewSystem(owner, func(Identity) (warp.AtomicMemoryService, error) { return owner, nil }, Config{100, 1, 16, 1})
	if err != nil {
		t.Fatal(err)
	}
	r := simd(1)
	r.Mask = 1
	r.Write = true
	r.Lanes[0] = LaneRequest{Address: 0, ByteEnable: 15, Data: [4]byte{8}}
	accepted := false
	for cycle := uint64(0); cycle < 20; cycle++ {
		e, err := s.Step(cycle, SystemInput{Memory: SIMDOffer{!accepted, r}})
		if err != nil {
			t.Fatal(err)
		}
		accepted = accepted || e.MemoryAccepted
		if !e.DataAdapterAccepted[0] {
			continue
		}
		if e.DataCacheAccepted[0] {
			t.Fatal("fixture bypassed cold cache")
		}
		saved := s.dataPort.queue.values[0]
		_, fault := s.Step(cycle+2, SystemInput{})
		if fault == nil || s.Drained() || !s.HasResidency(r.Identity.Kernel, r.Identity.CTA) {
			t.Fatal("protocol fault released buffered identity")
		}
		if _, again := s.Step(cycle+1, SystemInput{}); again != fault {
			t.Fatal("terminal edge retried")
		}
		if s.dataPort.queue.values[0] != saved || owner.writes != 0 {
			t.Fatal("fault replayed/dropped buffer")
		}
		return
	}
	t.Fatal("did not reach buffered request")
}
