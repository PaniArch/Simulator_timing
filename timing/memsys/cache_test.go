package memsys

import (
	"errors"
	"reflect"
	"testing"

	"vortex.local/simulator/support/memory"
)

// All production links are exercised through public APIs, including previewed
// ready, finite buffering and actual backend acceptance. No queue is popped by
// the harness and no response data is read directly from the backing owner.
type cacheHarness struct {
	t       *testing.T
	caches  []*Cache
	backend *Backend
	owner   *countedOwner
	cycle   uint64
}

func newCacheHarness(t *testing.T, config Config, kinds ...CacheKind) *cacheHarness {
	t.Helper()
	h := &cacheHarness{t: t}
	ports := 0
	for _, kind := range kinds {
		c, err := NewCache(kind)
		if err != nil {
			t.Fatal(err)
		}
		h.caches = append(h.caches, c)
		ports += c.Spec().MemoryPorts
	}
	h.backend, h.owner = setup(t, config, ports, 1<<20)
	for i := 0; i < 100; i++ {
		h.step(nil)
	}
	for _, c := range h.caches {
		if !c.Drained() {
			t.Fatal("reset scan did not drain")
		}
	}
	return h
}
func (h *cacheHarness) step(inputs []CacheInput) []CacheEdge {
	h.t.Helper()
	if inputs == nil {
		inputs = make([]CacheInput, len(h.caches))
	}
	preview, err := h.backend.PreviewResponses(h.cycle)
	if err != nil {
		h.t.Fatal(err)
	}
	var offers []Offer
	var ready []bool
	offset := 0
	for _, c := range h.caches {
		offers = append(offers, c.MemoryOffers()...)
		for port := 0; port < c.Spec().MemoryPorts; port++ {
			r := preview[offset+port]
			ready = append(ready, r.Valid && c.MemoryResponseReady(port, r.Response))
		}
		offset += c.Spec().MemoryPorts
	}
	memory, err := h.backend.Step(h.cycle, offers, ready)
	if err != nil {
		h.t.Fatal(err)
	}
	results := make([]CacheEdge, len(h.caches))
	offset = 0
	for i, c := range h.caches {
		if inputs[i].Requests == nil {
			inputs[i].Requests = make([]WordOffer, c.Spec().CorePorts)
		}
		if inputs[i].ResponseReady == nil {
			inputs[i].ResponseReady = make([]bool, c.Spec().CorePorts)
			for p := range inputs[i].ResponseReady {
				inputs[i].ResponseReady[p] = true
			}
		}
		n := c.Spec().MemoryPorts
		inputs[i].Memory = Edge{memory.Accepted[offset : offset+n], memory.Replies[offset : offset+n]}
		offset += n
		results[i], err = c.Step(h.cycle, inputs[i])
		if err != nil {
			h.t.Fatalf("cache %d cycle %d: %v", i, h.cycle, err)
		}
	}
	h.cycle++
	return results
}
func word(id uint64, address uint32) WordRequest {
	return WordRequest{Identity: Identity{Kernel: 7, CTA: 11, WarpGeneration: 13, Token: id, Transaction: id}, Address: address, Tag: id + 1000, ByteEnable: 255}
}
func (h *cacheHarness) run(cache, port int, r WordRequest) WordResponse {
	h.t.Helper()
	accepted := false
	for i := 0; i < 20000; i++ {
		inputs := make([]CacheInput, len(h.caches))
		inputs[cache].Requests = make([]WordOffer, h.caches[cache].Spec().CorePorts)
		inputs[cache].Requests[port] = WordOffer{!accepted, r}
		e := h.step(inputs)[cache]
		accepted = accepted || e.Accepted[port]
		if !r.Write && e.Replies[port].Delivered {
			reply := e.Replies[port].Response
			if reply.Identity != r.Identity || reply.Tag != r.Tag {
				h.t.Fatal("foreign core response")
			}
			return reply
		}
		if r.Write {
			for _, receipt := range e.Stores {
				if receipt.Request.Identity == r.Identity {
					if receipt.Port != port {
						h.t.Fatal("store receipt routed to wrong port")
					}
					return WordResponse{Identity: r.Identity, Err: receipt.Err}
				}
			}
		}
	}
	h.t.Fatal("cache request timed out")
	return WordResponse{}
}
func (h *cacheHarness) flush(cache int, id uint64, ready bool) FlushReply {
	h.t.Helper()
	accepted := false
	for i := 0; i < 20000; i++ {
		inputs := make([]CacheInput, len(h.caches))
		inputs[cache] = CacheInput{Flush: FlushOffer{!accepted, Identity{Transaction: id}, id}, FlushReady: ready}
		e := h.step(inputs)[cache]
		accepted = accepted || e.FlushAccepted
		if e.Flush.Valid {
			if e.Flush.Identity.Transaction != id || e.Flush.Tag != id {
				h.t.Fatal("flush identity lost")
			}
			return e.Flush
		}
	}
	h.t.Fatal("cache flush timed out")
	return FlushReply{}
}

func TestCachePublicColdHitDirtyEvictionAndFlush(t *testing.T) {
	h := newCacheHarness(t, Config{5, 1, 8, 1}, DataCache)
	if err := h.owner.Memory.Write(0, []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	r := h.run(0, 1, word(1, 0))
	if r.Data != [8]byte{1, 2, 3, 4, 5, 6, 7, 8} || h.owner.reads != 1 {
		t.Fatalf("cold miss %+v", r)
	}
	r = h.run(0, 0, word(1, 8))
	if r.Data != [8]byte{} || h.owner.reads != 1 {
		t.Fatal("same line word request conflated")
	}
	store := word(2, 0)
	store.Write = true
	store.ByteEnable = 0x81
	store.Data = [8]byte{42, 0, 0, 0, 0, 0, 0, 88}
	if r := h.run(0, 1, store); r.Err != nil {
		t.Fatal(r.Err)
	}
	r = h.run(0, 0, word(2, 0))
	if r.Data != [8]byte{42, 2, 3, 4, 5, 6, 7, 88} || h.owner.writes != 0 || h.owner.Snapshot()[0] != 1 {
		t.Fatal("cache bytes bypassed writeback")
	}
	for i := 0; i < 10; i++ {
		h.step(nil)
	}
	if !h.caches[0].Drained() || h.caches[0].State().Banks[0].DirtyLines != 1 {
		t.Fatal("dirty line treated as execution tail")
	}
	s := h.caches[0].Spec()
	stride := uint32(s.Sets * s.Banks * s.LineBytes)
	for i := uint32(1); i <= 4; i++ {
		h.run(0, 0, word(uint64(i+2), i*stride))
	}
	for i := 0; i < 30; i++ {
		h.step(nil)
	}
	if h.owner.writes != 1 || h.owner.Snapshot()[0] != 42 {
		t.Fatal("dirty victim not written")
	}
	store = word(3, 64)
	store.Write = true
	store.Data[0] = 77
	h.run(0, 1, store)
	reply := h.flush(0, 1, false)
	if reply.Err != nil || reply.Delivered || h.owner.Snapshot()[64] != 77 {
		t.Fatal("flush acknowledgement before visibility")
	}
	for i := 0; i < 5; i++ {
		e := h.step([]CacheInput{{FlushReady: false}})[0]
		if !reflect.DeepEqual(e.Flush, reply) {
			t.Fatal("held flush response changed")
		}
	}
	e := h.step([]CacheInput{{FlushReady: true}})[0]
	if !e.Flush.Delivered {
		t.Fatal("flush did not release")
	}
	for i := 0; i < 5; i++ {
		h.step(nil)
	}
	if !h.caches[0].Drained() {
		t.Fatal("flush leaked state")
	}
	for _, bank := range h.caches[0].State().Banks {
		if bank.ValidLines != 0 || bank.DirtyLines != 0 {
			t.Fatal("flush left resident lines")
		}
	}
}

func TestCacheBankPortArbitrationAndSharedBackend(t *testing.T) {
	h := newCacheHarness(t, Config{7, 1, 3, 1}, InstructionCache, DataCache)
	ireq := word(1, 128)
	ireq.ByteEnable = 15
	requests := []CacheInput{{Requests: []WordOffer{{true, ireq}}}, {Requests: []WordOffer{{true, word(1, 0)}, {true, word(1, 8)}}}}
	first := h.step(requests)
	if !first[0].Accepted[0] || !reflect.DeepEqual(first[1].Accepted, []bool{true, false}) {
		t.Fatalf("same bank grants %+v", first)
	}
	requests[0].Requests[0].Valid = false
	requests[1].Requests[0] = WordOffer{true, word(2, 64)}
	second := h.step(requests)
	if !reflect.DeepEqual(second[1].Accepted, []bool{true, true}) {
		t.Fatalf("different banks cannot accept together %+v", second)
	}
	gotI, gotD := 0, 0
	for cycle := 0; cycle < 1000 && (gotI < 1 || gotD < 3); cycle++ {
		e := h.step(nil)
		if e[0].Replies[0].Delivered {
			gotI++
		}
		for _, r := range e[1].Replies {
			if r.Delivered {
				gotD++
			}
		}
	}
	if gotI != 1 || gotD != 3 || h.owner.reads != 3 {
		t.Fatalf("shared cache/backend responses I=%d D=%d reads=%d", gotI, gotD, h.owner.reads)
	}
}

func TestCacheResponseBackpressureAcrossBanks(t *testing.T) {
	h := newCacheHarness(t, Config{2, 2, 16, 2}, DataCache)
	accepted, received := 0, 0
	var held *WordResponse
	for cycle := 0; cycle < 400; cycle++ {
		r := word(uint64(accepted+1), uint32((accepted%2)*64))
		e := h.step([]CacheInput{{Requests: []WordOffer{{accepted < 20, r}, {}}, ResponseReady: []bool{cycle >= 100, true}}})[0]
		if e.Accepted[0] {
			accepted++
		}
		if e.Replies[0].Valid && !e.Replies[0].Delivered {
			if held == nil {
				v := e.Replies[0].Response
				held = &v
			} else if !reflect.DeepEqual(*held, e.Replies[0].Response) {
				t.Fatal("response changed under backpressure")
			}
		}
		if e.Replies[0].Delivered {
			received++
			held = nil
		}
		if accepted == 20 && received == 20 {
			break
		}
	}
	if accepted != 20 || received != 20 {
		t.Fatalf("response recovery %d %d", accepted, received)
	}
}

func TestCacheNoncacheableAndFaultResponses(t *testing.T) {
	h := newCacheHarness(t, Config{3, 2, 8, 2}, DataCache)
	store := word(1, 520)
	store.NonCacheable = true
	store.Write = true
	store.ByteEnable = 5
	store.Data = [8]byte{11, 99, 22}
	h.run(0, 1, store)
	if h.owner.Snapshot()[520] != 11 || h.owner.Snapshot()[521] != 0 || h.owner.Snapshot()[522] != 22 {
		t.Fatal("NC byte enables")
	}
	r := word(2, 520)
	r.NonCacheable = true
	reply := h.run(0, 1, r)
	if reply.Data != [8]byte{11, 0, 22} {
		t.Fatalf("NC word split %+v", reply)
	}
	for _, b := range h.caches[0].State().Banks {
		if b.ValidLines != 0 {
			t.Fatal("NC access allocated cache line")
		}
	}
	bad := word(3, 1<<20)
	reply = h.run(0, 1, bad)
	if !errors.Is(reply.Err, memory.ErrOutOfBounds) || reply.Data != [8]byte{} {
		t.Fatal("refill fault lost")
	}
	bad = word(4, 1<<20)
	bad.NonCacheable = true
	bad.Write = true
	if reply := h.run(0, 1, bad); !errors.Is(reply.Err, memory.ErrOutOfBounds) {
		t.Fatal("NC store fault lost")
	}
}

func TestCacheHitPipelineLatencyAndBackToBackStore(t *testing.T) {
	for _, kind := range []CacheKind{InstructionCache, DataCache} {
		h := newCacheHarness(t, Config{3, 1, 8, 1}, kind)
		warm := word(1, 0)
		if kind == InstructionCache {
			warm.ByteEnable = 15
		}
		h.run(0, 0, warm)
		r := word(2, 0)
		if kind == InstructionCache {
			r.ByteEnable = 15
		}
		accepted := uint64(0)
		for i := 0; i < 50; i++ {
			at := h.cycle
			e := h.step([]CacheInput{{Requests: singlePortOffer(kind, r, accepted == 0)}})[0]
			if e.Accepted[0] {
				accepted = at
			}
			if e.Replies[0].Delivered {
				want := uint64(h.caches[0].Spec().Latency + 2)
				if kind == DataCache {
					want++
				}
				if at-accepted != want {
					t.Fatalf("%s hit pipeline %d want %d", kind, at-accepted, want)
				}
				break
			}
			if i == 49 {
				t.Fatal("hit response timeout")
			}
		}
		if kind == DataCache {
			store := word(3, 0)
			store.Write = true
			store.Data[0] = 55
			e := h.step([]CacheInput{{Requests: []WordOffer{{true, store}, {}}}})[0]
			if !e.Accepted[0] {
				t.Fatal("hot store not accepted")
			}
			e = h.step([]CacheInput{{Requests: []WordOffer{{}, {true, word(1, 0)}}}})[0]
			if !e.Accepted[1] {
				t.Fatal("transient hit reservation imposed extra same-line serialization")
			}
			got := false
			for i := 0; i < 20; i++ {
				e = h.step(nil)[0]
				if e.Replies[1].Delivered {
					got = true
					if e.Replies[1].Response.Data[0] != 55 {
						t.Fatal("younger hit read missed old store bytes")
					}
				}
			}
			if !got {
				t.Fatal("back-to-back load disappeared")
			}
		}
	}
}

func singlePortOffer(kind CacheKind, r WordRequest, valid bool) []WordOffer {
	n := 1
	if kind == DataCache {
		n = 2
	}
	o := make([]WordOffer, n)
	o[0] = WordOffer{valid, r}
	return o
}

func TestCacheWritebackErrorAndProtocolRejection(t *testing.T) {
	h := newCacheHarness(t, Config{2, 1, 8, 1}, DataCache)
	store := word(1, 0)
	store.Write = true
	store.Data[0] = 91
	h.run(0, 0, store)
	failed := errors.New("backing write rejected")
	h.owner.writeErr = failed
	reply := h.flush(0, 1, true)
	if !errors.Is(reply.Err, failed) || !errors.Is(h.caches[0].State().WritebackError, failed) || h.owner.Snapshot()[0] != 0 {
		t.Fatal("failed writeback reported visible success")
	}
	if h.owner.writes != 1 {
		t.Fatal("writeback retried")
	}
	c := h.caches[0]
	before := c.State()
	in := CacheInput{Requests: []WordOffer{{true, store}, {}}, ResponseReady: []bool{true, true}, Memory: Edge{make([]bool, 2), make([]Reply, 2)}}
	if _, err := c.Step(h.cycle, in); err == nil {
		t.Fatal("duplicate transaction accepted")
	}
	if !reflect.DeepEqual(before, c.State()) {
		t.Fatal("protocol rejection mutated cache")
	}
	in.Requests[0] = WordOffer{}
	in.Memory.Replies[0] = Reply{Valid: true, Delivered: true, Response: Response{Identity: Identity{Transaction: 99999}}}
	if _, err := c.Step(h.cycle, in); err == nil {
		t.Fatal("foreign refill accepted")
	}
	if !reflect.DeepEqual(before, c.State()) {
		t.Fatal("foreign response mutated cache")
	}
}

func TestCacheFiniteQueuesMSHRFullAndRecovery(t *testing.T) {
	h := newCacheHarness(t, Config{40, 1, 2, 1}, DataCache)
	for port := 0; port < 2; port++ {
		for i := 0; i < 20; i++ {
			if err := h.owner.Memory.Write(uint32(i*128+port*64), []byte{byte(i + 1 + port*100)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	accepted, received := [2]int{}, [2]int{}
	full := false
	for cycle := 0; cycle < 5000; cycle++ {
		input := CacheInput{Requests: make([]WordOffer, 2), ResponseReady: []bool{cycle >= 150, cycle >= 150}}
		for port := 0; port < 2; port++ {
			input.Requests[port] = WordOffer{accepted[port] < 20, word(uint64(accepted[port]+1), uint32(accepted[port]*128+port*64))}
		}
		e := h.step([]CacheInput{input})[0]
		for port := 0; port < 2; port++ {
			if e.Accepted[port] {
				accepted[port]++
			}
			if e.Replies[port].Delivered {
				received[port]++
				r := e.Replies[port].Response
				if r.Data[0] != byte(r.Identity.Token+uint64(port*100)) {
					t.Fatal("refill data routed to wrong request")
				}
			}
		}
		state := h.caches[0].State()
		for _, b := range state.Banks {
			full = full || b.Reserved == 16
			if b.Reserved > 16 || b.MemoryRequests > 16 || b.CoreResponses > 2 || b.RefillResponses > 2 {
				t.Fatal("bank resource overcommit")
			}
		}
		for _, n := range state.MemoryResponseBuffers {
			if n > 4 {
				t.Fatal("MRSQ overcommit")
			}
		}
		if received == [2]int{20, 20} {
			break
		}
	}
	if !full || accepted != [2]int{20, 20} || received != accepted || h.owner.reads != 40 {
		t.Fatalf("full/recovery %v accepted=%v received=%v reads=%d", full, accepted, received, h.owner.reads)
	}
	for i := 0; i < 10; i++ {
		h.step(nil)
	}
	if !h.caches[0].Drained() {
		t.Fatal("finite queues leaked requests")
	}
}

func TestCacheConcurrentRefillWritebackCompetition(t *testing.T) {
	h := newCacheHarness(t, Config{10, 1, 1, 1}, DataCache)
	const stride = 4096
	for i := 0; i < 4; i++ {
		for port := 0; port < 2; port++ {
			r := word(uint64(i+1), uint32(i*stride+port*64))
			r.Write = true
			r.ByteEnable = 1
			r.Data[0] = byte(10 + i + port*100)
			h.run(0, port, r)
		}
	}
	accepted := [2]bool{}
	applied := 0
	for cycle := 0; cycle < 1000 && applied < 2; cycle++ {
		input := CacheInput{Requests: make([]WordOffer, 2)}
		for port := 0; port < 2; port++ {
			r := word(5, uint32(4*stride+port*64))
			r.Write = true
			r.ByteEnable = 1
			r.Data[0] = byte(14 + port*100)
			input.Requests[port] = WordOffer{!accepted[port], r}
		}
		e := h.step([]CacheInput{input})[0]
		for port := 0; port < 2; port++ {
			accepted[port] = accepted[port] || e.Accepted[port]
		}
		applied += len(e.Stores)
	}
	if applied != 2 {
		t.Fatal("refill/writeback competition deadlocked")
	}
	for i := 0; i < 1000 && !h.caches[0].Drained(); i++ {
		h.step(nil)
	}
	if h.owner.writes != 2 || h.owner.Snapshot()[0] != 10 || h.owner.Snapshot()[64] != 110 {
		t.Fatal("dirty victim bytes lost under external contention")
	}
	r := h.run(0, 0, word(6, 0))
	if r.Data[0] != 10 {
		t.Fatal("reload overtook victim writeback")
	}
	if reply := h.flush(0, 1, true); reply.Err != nil {
		t.Fatal(reply.Err)
	}
	data := h.owner.Snapshot()
	for i := 0; i < 5; i++ {
		for port := 0; port < 2; port++ {
			if data[i*stride+port*64] != byte(10+i+port*100) {
				t.Fatal("final writeback visibility")
			}
		}
	}
	if h.owner.writes != 10 {
		t.Fatalf("writeback duplicated or lost: %d", h.owner.writes)
	}
}

func TestCacheResidencyIncludesHeldResponseNotDirtyLines(t *testing.T) {
	h := newCacheHarness(t, Config{3, 1, 8, 1}, DataCache)
	r := word(1, 0)
	sent, held := false, false
	for i := 0; i < 100; i++ {
		e := h.step([]CacheInput{{Requests: []WordOffer{{!sent, r}, {}}, ResponseReady: []bool{false, true}}})[0]
		sent = sent || e.Accepted[0]
		if sent && !h.caches[0].HasResidency(7, 11) {
			t.Fatal("lost outstanding residency")
		}
		if e.Replies[0].Valid {
			held = true
			break
		}
	}
	if !held || h.caches[0].HasResidency(7, 12) {
		t.Fatal("residency scope incorrect")
	}
	for i := 0; i < 5; i++ {
		h.step(nil)
	}
	if h.caches[0].HasResidency(7, 11) {
		t.Fatal("consumed response retained residency")
	}
	store := word(2, 0)
	store.Write = true
	store.Data[0] = 13
	h.run(0, 0, store)
	for i := 0; i < 5; i++ {
		h.step(nil)
	}
	if h.caches[0].State().Banks[0].DirtyLines != 1 || h.caches[0].HasResidency(7, 11) {
		t.Fatal("dirty line prevents residency reuse")
	}
}
