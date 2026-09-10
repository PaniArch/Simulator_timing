package memsys

import (
	"testing"
)

func cacheSpec(t *testing.T, k CacheKind) CacheSpec {
	t.Helper()
	s, err := FrozenCacheSpec(k)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func initializedArray(t *testing.T, k CacheKind, bank int) *cacheArray {
	t.Helper()
	a := newCacheArray(cacheSpec(t, k), bank)
	for set := range a.lines {
		a.initSet(set)
	}
	return a
}
func TestCacheSpecGeometry(t *testing.T) {
	for _, kind := range []CacheKind{InstructionCache, DataCache} {
		s := cacheSpec(t, kind)
		if s.Bytes != 16384 || s.Ways != 4 || s.LineBytes != 64 || s.MSHR != 16 || s.Latency != 2 {
			t.Fatalf("frozen geometry %+v", s)
		}
		if s.CoreResponse.Size != 2 || s.RefillCrossbar.Size != 2 {
			t.Fatalf("queue decoding %+v", s)
		}
		if kind == InstructionCache {
			if s.Sets != 64 || s.WordBytes != 4 || s.MemoryRequest.Size != 4 || s.MemoryResponse.Size != 0 || s.Writeback {
				t.Fatalf("I-cache %+v", s)
			}
		} else {
			if s.Sets != 32 || s.WordBytes != 8 || s.MemoryRequest.Size != 16 || s.MemoryResponse.Size != 4 || !s.Writeback {
				t.Fatalf("D-cache %+v", s)
			}
			b0, set0, w0, _ := s.DecodeAddress(0)
			b1, set1, w1, _ := s.DecodeAddress(8)
			b2, set2, _, _ := s.DecodeAddress(64)
			if b0 != b1 || set0 != set1 || w0 == w1 || b2 == b0 || set2 != set0 {
				t.Fatal("word, line, bank conflated")
			}
		}
		for _, addr := range []uint32{0, 8, 64, 4096, 0xffffffc0} {
			b, set, _, tag := s.DecodeAddress(addr)
			if s.lineAddress(b, set, tag) != addr&^uint32(63) {
				t.Fatal("address round trip")
			}
		}
	}
}
func TestCacheArrayFIFOAndDirtySnapshot(t *testing.T) {
	a := initializedArray(t, DataCache, 0)
	stride := uint32(a.spec.Banks * a.spec.Sets * a.spec.LineBytes)
	for i := uint32(0); i < 4; i++ {
		data := [64]byte{byte(i + 1)}
		if v, err := a.fill(i*stride, data); err != nil || v.Valid {
			t.Fatalf("cold fill %v %+v", err, v)
		}
	}
	// Hits to first line do not promote its FIFO age.
	write := WordRequest{Address: 0, Write: true, ByteEnable: 0x85, Data: [8]byte{10, 99, 20, 99, 99, 99, 99, 30}}
	if hit, err := a.write(write); err != nil || !hit {
		t.Fatal("write miss", err)
	}
	for i := 0; i < 5; i++ {
		r, hit, err := a.read(WordRequest{Address: 0})
		if err != nil || !hit || r.Data != [8]byte{10, 0, 20, 0, 0, 0, 0, 30} {
			t.Fatalf("read %+v %v", r, err)
		}
	}
	valid, dirty := a.residentCounts()
	if valid != 4 || dirty != 1 {
		t.Fatal("wrong resident state")
	}
	victim, err := a.fill(4*stride, [64]byte{55})
	if err != nil || !victim.Valid || victim.Address != 0 || victim.Data[0] != 10 || victim.Data[7] != 30 {
		t.Fatalf("FIFO eviction %+v %v", victim, err)
	}
	// Replacing or updating the array cannot alter the captured writeback.
	_, _ = a.write(WordRequest{Address: 4 * stride, Write: true, ByteEnable: 1, Data: [8]byte{88}})
	if victim.Data[0] != 10 {
		t.Fatal("writeback aliases new line")
	}
	if _, hit, _ := a.lookup(0); hit {
		t.Fatal("FIFO used hit recency")
	}
	r, hit, _ := a.read(WordRequest{Address: 4*stride + 8})
	if !hit || r.Data != [8]byte{} {
		t.Fatal("same line word offset")
	}
	// Flushing a way returns its dirty bytes and invalidates only that way.
	v := a.flushWay(0, 0)
	if !v.Valid || v.Address != 4*stride || v.Data[0] != 88 {
		t.Fatal("flush lost dirty bytes")
	}
	valid, dirty = a.residentCounts()
	if valid != 3 || dirty != 0 {
		t.Fatal("flush cleared unrelated ways")
	}
}
func TestICacheArrayReadOnlyAndInitialization(t *testing.T) {
	a := newCacheArray(cacheSpec(t, InstructionCache), 0)
	if _, _, err := a.read(WordRequest{}); err == nil {
		t.Fatal("uninitialized set accessible")
	}
	a.initSet(0)
	if _, err := a.fill(0, [64]byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	r, hit, err := a.read(WordRequest{Address: 4, Tag: 19, Identity: Identity{Token: 7}})
	if err != nil || !hit || r.Data != [8]byte{5, 6, 7, 8} || r.Tag != 19 || r.Identity.Token != 7 || r.ByteEnable != 15 {
		t.Fatalf("instruction word %+v", r)
	}
	if _, err := a.write(WordRequest{Write: true, ByteEnable: 1}); err == nil {
		t.Fatal("I-cache store allowed")
	}
}
func mstep(t *testing.T, m *cacheMSHR, ev mshrEvents) *mshrAllocationResult {
	t.Helper()
	r, err := m.step(ev)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func allocate(t *testing.T, m *cacheMSHR, addr uint32) *mshrAllocationResult {
	t.Helper()
	return mstep(t, m, mshrEvents{allocate: &mshrAllocation{request: WordRequest{Address: addr}}})
}
func TestMSHRCapacityChainsAndRecovery(t *testing.T) {
	m := newCacheMSHR(2)
	if _, err := m.step(mshrEvents{allocate: &mshrAllocation{}}); err == nil {
		t.Fatal("registered allocation ready ignored")
	}
	mstep(t, m, mshrEvents{})
	a := allocate(t, m, 0)
	if a.previous != nil {
		t.Fatal("first miss has predecessor")
	}
	b := mstep(t, m, mshrEvents{allocate: &mshrAllocation{request: WordRequest{Address: 8}}, finalize: &mshrFinalization{handle: a.handle}})
	if b.previous == nil || *b.previous != a.handle || m.ready {
		t.Fatal("same-line chain or capacity")
	}
	if _, err := m.step(mshrEvents{allocate: &mshrAllocation{}}); err == nil {
		t.Fatal("full MSHR accepted")
	}
	mstep(t, m, mshrEvents{finalize: &mshrFinalization{handle: b.handle, previous: b.previous}})
	mstep(t, m, mshrEvents{fill: &a.handle})
	h, _, valid := m.replay()
	if !valid || h != a.handle {
		t.Fatal("fill did not start head")
	}
	mstep(t, m, mshrEvents{dequeue: true})
	h, _, valid = m.replay()
	if !valid || h != b.handle || !m.ready {
		t.Fatal("chain did not advance/free")
	}
	c := mstep(t, m, mshrEvents{allocate: &mshrAllocation{request: WordRequest{Address: 0}}, dequeue: true})
	if c.previous != nil {
		t.Fatal("allocated behind disappearing tail")
	}
	if c.handle.slot != a.handle.slot || c.handle.generation == a.handle.generation {
		t.Fatal("slot reuse lacks generation")
	}
	if _, err := m.step(mshrEvents{fill: &a.handle}); err == nil {
		t.Fatal("stale fill released replacement")
	}
	if m.occupancy() != 1 {
		t.Fatal("stale fill mutated pool")
	}
	mstep(t, m, mshrEvents{finalize: &mshrFinalization{handle: c.handle, release: true}})
	if m.occupancy() != 0 {
		t.Fatal("hit reservation not freed")
	}
}
func TestMSHRTailFinalizeAndDequeueSameEdge(t *testing.T) {
	m := newCacheMSHR(3)
	mstep(t, m, mshrEvents{})
	a := allocate(t, m, 0)
	mstep(t, m, mshrEvents{finalize: &mshrFinalization{handle: a.handle}})
	mstep(t, m, mshrEvents{fill: &a.handle})
	b := allocate(t, m, 8)
	mstep(t, m, mshrEvents{dequeue: true, finalize: &mshrFinalization{handle: b.handle, previous: b.previous}})
	h, _, valid := m.replay()
	if !valid || h != b.handle {
		t.Fatal("late chain member orphaned")
	}
	mstep(t, m, mshrEvents{dequeue: true})
	if m.occupancy() != 0 {
		t.Fatal("chain leaked")
	}
}
func TestMSHRHitDoesNotJoinPendingChain(t *testing.T) {
	m := newCacheMSHR(3)
	mstep(t, m, mshrEvents{})
	a := allocate(t, m, 0)
	mstep(t, m, mshrEvents{finalize: &mshrFinalization{handle: a.handle}})
	b := allocate(t, m, 8)
	mstep(t, m, mshrEvents{finalize: &mshrFinalization{handle: b.handle, release: true, previous: b.previous}})
	mstep(t, m, mshrEvents{fill: &a.handle})
	mstep(t, m, mshrEvents{dequeue: true})
	if m.occupancy() != 0 || m.head != nil {
		t.Fatal("hit attached then double-freed")
	}
}
