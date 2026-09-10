package memsys

import (
	"errors"
	"testing"

	"vortex.local/simulator/support/memory"
)

type bankHarness struct {
	t               *testing.T
	bank            *cacheBank
	backend         *Backend
	owner           *countedOwner
	cycle, sequence uint64
	pending         map[uint64]bankMemoryRequest
	held            Offer
	returned        *bankFill
}

func newBankHarness(t *testing.T, kind CacheKind, latency uint64) *bankHarness {
	t.Helper()
	s := cacheSpec(t, kind)
	backend, owner := setup(t, Config{latency, 1, 64, 1}, 1, 1<<20)
	h := &bankHarness{t: t, bank: newCacheBank(s, 0), backend: backend, owner: owner, pending: make(map[uint64]bankMemoryRequest)}
	for i := 0; i < s.Sets+2; i++ {
		h.step(bankInput{responseReady: true}, true)
	}
	if !h.bank.drained() {
		t.Fatal("init pipeline not drained")
	}
	return h
}
func (h *bankHarness) step(in bankInput, backendAccept bool) bankEdge {
	h.t.Helper()
	if !h.held.Valid && len(h.bank.memory) > 0 && backendAccept {
		v := h.bank.memory[0]
		h.sequence++
		op := Read
		if v.write {
			op = Write
		}
		h.held = Offer{Valid: true, Request: Request{Identity: Identity{Transaction: h.sequence}, Operation: op, Address: v.address, ByteEnable: ^uint64(0), Data: v.data}}
	}
	// One explicit test-bridge response register, using only backend handshakes.
	oldReturn := h.returned
	e, err := h.backend.Step(h.cycle, []Offer{h.held}, []bool{oldReturn == nil})
	if err != nil {
		h.t.Fatal(err)
	}
	if e.Accepted[0] {
		h.pending[h.held.Request.Identity.Transaction] = h.bank.memory[0]
		h.held = Offer{}
		in.memoryReady = true
	}
	in.fill = oldReturn
	var arrived *bankFill
	if e.Replies[0].Delivered {
		response := e.Replies[0].Response
		v, ok := h.pending[response.Identity.Transaction]
		if !ok {
			h.t.Fatal("foreign backend response")
		}
		delete(h.pending, response.Identity.Transaction)
		if !v.write {
			arrived = &bankFill{handle: v.handle, data: response.Data, err: response.Err}
		}
	}
	r, err := h.bank.step(in)
	if err != nil {
		h.t.Fatalf("cycle %d: %v", h.cycle, err)
	}
	if r.fillAccepted {
		h.returned = nil
	}
	if arrived != nil {
		h.returned = arrived
	}
	h.cycle++
	return r
}
func (h *bankHarness) run(r WordRequest) WordResponse {
	h.t.Helper()
	sent := false
	for i := 0; i < 20000; i++ {
		e := h.step(bankInput{request: WordOffer{Valid: !sent, Request: r}, responseReady: true}, true)
		if e.accepted {
			sent = true
		}
		if r.Write && e.storeApplied != nil && e.storeApplied.Identity == r.Identity {
			return WordResponse{Identity: r.Identity, Err: e.storeError}
		}
		if !r.Write && e.responseDelivered && e.response.response.Identity == r.Identity {
			return e.response.response
		}
	}
	h.t.Fatal("bank request timed out")
	return WordResponse{}
}
func TestCacheBankColdMissHitsAndStoreVisibility(t *testing.T) {
	h := newBankHarness(t, DataCache, 5)
	if err := h.owner.Memory.Write(0, []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	first := h.run(WordRequest{Identity: Identity{Token: 1}})
	if first.Data != [8]byte{1, 2, 3, 4, 5, 6, 7, 8} || h.owner.reads != 1 {
		t.Fatalf("cold fill %+v", first)
	}
	second := h.run(WordRequest{Identity: Identity{Token: 2}, Address: 8})
	if second.Data != [8]byte{} || h.owner.reads != 1 {
		t.Fatal("same-line word requested another refill")
	}
	h.run(WordRequest{Identity: Identity{Token: 3}, Write: true, ByteEnable: 0x41, Data: [8]byte{99, 0, 0, 0, 0, 0, 88}})
	after := h.run(WordRequest{Identity: Identity{Token: 4}})
	if after.Data != [8]byte{99, 2, 3, 4, 5, 6, 88, 8} {
		t.Fatalf("store/load %+v", after)
	}
	if h.owner.writes != 0 || h.owner.Snapshot()[0] != 1 {
		t.Fatal("cache store bypassed writeback")
	}
	for i := 0; i < 4; i++ {
		h.step(bankInput{responseReady: true}, true)
	}
	if !h.bank.drained() {
		t.Fatal("dirty resident line counted as outstanding")
	}
}
func TestCacheBankMSHRFullAndReplayStoreOrder(t *testing.T) {
	h := newBankHarness(t, DataCache, 30)
	requests := make([]WordRequest, 20)
	for i := range requests {
		requests[i] = WordRequest{Identity: Identity{Token: uint64(i + 1)}, Address: 0}
	}
	requests[3].Write = true
	requests[3].ByteEnable = 1
	requests[3].Data[0] = 77
	accepted, loads, stores := 0, 0, 0
	full := false
	for i := 0; i < 1000 && (loads+stores < len(requests)); i++ {
		var o WordOffer
		// The first 16 accesses form the miss chain. Later hits are submitted
		// after it retires: chain order does not imply order against new hits
		// admitted during fill forwarding (covered separately below).
		if accepted < len(requests) && (accepted < 16 || loads+stores >= 16) {
			o = WordOffer{true, requests[accepted]}
		}
		e := h.step(bankInput{request: o, responseReady: true}, true)
		if e.accepted {
			accepted++
		}
		if h.bank.reserved == h.bank.spec.MSHR {
			full = true
		}
		if e.responseDelivered {
			loads++
			r := e.response.response
			if r.Identity.Token < 4 && r.Data[0] != 0 {
				t.Fatal("older read observed younger write")
			}
			if r.Identity.Token > 4 && r.Data[0] != 77 {
				t.Fatalf("younger read bypassed replay write: %+v", r)
			}
		}
		if e.storeApplied != nil {
			stores++
		}
	}
	if !full || accepted != 20 || loads != 19 || stores != 1 || h.owner.reads != 1 || h.bank.reserved != 0 {
		t.Fatalf("full/recovery counts %v %d %d %d reads=%d reserved=%d", full, accepted, loads, stores, h.owner.reads, h.bank.reserved)
	}
}
func TestCacheBankDirtyEvictionAndFlush(t *testing.T) {
	h := newBankHarness(t, DataCache, 4)
	stride := uint32(h.bank.spec.Sets * h.bank.spec.Banks * SectorBytes)
	h.run(WordRequest{Identity: Identity{Token: 1}, Write: true, ByteEnable: 1, Data: [8]byte{42}})
	for i := uint32(1); i < 5; i++ {
		h.run(WordRequest{Identity: Identity{Token: uint64(i + 1)}, Address: i * stride})
	}
	for i := 0; i < 20; i++ {
		h.step(bankInput{responseReady: true}, true)
	}
	if h.owner.writes != 1 || h.owner.Snapshot()[0] != 42 {
		t.Fatal("dirty replacement did not write backing")
	}
	h.run(WordRequest{Identity: Identity{Token: 10}, Address: 4 * stride, Write: true, ByteEnable: 1, Data: [8]byte{88}})
	h.step(bankInput{flushBegin: true, responseReady: true}, true)
	done := false
	for i := 0; i < 1000; i++ {
		e := h.step(bankInput{responseReady: true}, true)
		done = done || e.flushDone
		if done && h.bank.drained() && h.backend.Outstanding() == 0 {
			break
		}
	}
	valid, dirty := h.bank.array.residentCounts()
	if !done || valid != 0 || dirty != 0 || h.owner.Snapshot()[4*stride] != 88 {
		t.Fatal("flush scan/data tail incomplete")
	}
	if h.owner.writes != 2 {
		t.Fatal("flush resubmitted writes")
	}
}
func TestCacheBankResponseBackpressureAndFault(t *testing.T) {
	h := newBankHarness(t, InstructionCache, 2)
	h.run(WordRequest{Identity: Identity{Token: 1}})
	sent, received := 0, 0
	var held *bankResponse
	for i := 0; i < 40; i++ {
		ready := i >= 20
		r := WordRequest{Identity: Identity{Token: uint64(10 + sent)}}
		e := h.step(bankInput{request: WordOffer{sent < 6, r}, responseReady: ready}, true)
		if e.accepted {
			sent++
		}
		if e.response != nil && !ready {
			if held == nil {
				held = e.response
			} else if *held != *e.response {
				t.Fatal("stalled cache response changed")
			}
		}
		if e.responseDelivered {
			received++
		}
	}
	if sent != 6 || received != 6 || h.owner.reads != 1 {
		t.Fatalf("response recovery %d %d", sent, received)
	}
	bad := h.run(WordRequest{Identity: Identity{Token: 99}, Address: 1 << 20})
	if !errors.Is(bad.Err, memory.ErrOutOfBounds) || bad.Data != [8]byte{} {
		t.Fatal("fill fault missing")
	}
}

func TestCacheBankMemoryBackpressure(t *testing.T) {
	h := newBankHarness(t, DataCache, 3)
	accepted, received := 0, 0
	var held *bankMemoryRequest
	for cycle := 0; cycle < 500; cycle++ {
		allow := cycle >= 80
		r := WordRequest{Identity: Identity{Token: uint64(accepted + 1)}, Address: uint32(accepted * 128)}
		e := h.step(bankInput{request: WordOffer{accepted < 16, r}, responseReady: true}, allow)
		if e.accepted {
			accepted++
		}
		if !allow && e.memory != nil {
			if held == nil {
				held = e.memory
			} else if *held != *e.memory {
				t.Fatal("memory request changed under backpressure")
			}
		}
		if len(h.bank.memory) > h.bank.spec.MemoryRequest.Size || h.bank.reserved > h.bank.spec.MSHR {
			t.Fatal("bank overcommitted reserved queues")
		}
		if e.responseDelivered {
			received++
		}
		if accepted == 16 && received == 16 {
			break
		}
	}
	if accepted != 16 || received != 16 || h.owner.reads != 16 {
		t.Fatalf("memory recovery %d %d %d", accepted, received, h.owner.reads)
	}
}

func TestCacheBankFlushDuringInit(t *testing.T) {
	s := cacheSpec(t, InstructionCache)
	b := newCacheBank(s, 0)
	done := false
	for cycle := 0; cycle < s.Sets+2; cycle++ {
		e, err := b.step(bankInput{flushBegin: cycle == 1, responseReady: true, memoryReady: true})
		if err != nil {
			t.Fatal(err)
		}
		if e.flushDone {
			done = true
			if cycle != s.Sets {
				t.Fatalf("flush init completion cycle %d", cycle)
			}
		}
	}
	if !done || b.flushing != flushIdle {
		t.Fatal("flush during init restarted a second scan")
	}
}

func TestCacheBankNonzeroFlushWaitsForWriteQueue(t *testing.T) {
	s := cacheSpec(t, DataCache)
	b := newCacheBank(s, 1)
	for i := 0; i < s.Sets+2; i++ {
		if _, err := b.step(bankInput{}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := b.array.fill(64, [64]byte{7})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.array.write(WordRequest{Address: 64, Write: true, ByteEnable: 1, Data: [8]byte{9}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.step(bankInput{flushBegin: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < s.Sets*s.Ways+10; i++ {
		e, err := b.step(bankInput{})
		if err != nil {
			t.Fatal(err)
		}
		if e.flushDone {
			t.Fatal("nonzero bank completed before dirty request queue drained")
		}
	}
	if b.flushing != flushDrain || len(b.memory) != 1 {
		t.Fatal("flush tail not retained")
	}
	done := false
	for i := 0; i < 4; i++ {
		e, err := b.step(bankInput{memoryReady: true})
		if err != nil {
			t.Fatal(err)
		}
		done = done || e.flushDone
	}
	if !done || !b.drained() {
		t.Fatal("flush did not resume after write acceptance")
	}
}

// VX_cache_bank: fwd_head masks replay_mux, leaving creq_grant asserted.
// A same-line pending store is NOT a core_req_ready condition. At LATENCY=2
// fill writes at F+1, the newcomer accesses the array at F+2, before the old
// store replays. Forwarded reads still sample the staged, unmodified sector.
func TestCacheBankNewHitDuringForwardChain(t *testing.T) {
	for _, write := range []bool{false, true} {
		name := "read"
		if write {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			h := newBankHarness(t, DataCache, 30)
			for id := uint64(1); id <= 5; id++ {
				r := WordRequest{Identity: Identity{Token: id}, Address: 0, ByteEnable: 1}
				if id == 4 {
					r.Write = true
					r.Data[0] = 77
				}
				if !h.step(bankInput{request: WordOffer{true, r}, responseReady: true}, true).accepted {
					t.Fatal("initial miss chain not accepted consecutively")
				}
			}
			var fillCycle uint64
			for i := 0; i < 100; i++ {
				cycle := h.cycle
				if h.step(bankInput{responseReady: true}, true).fillAccepted {
					fillCycle = cycle
					break
				}
			}
			if fillCycle == 0 {
				t.Fatal("missing refill")
			}
			newcomer := WordRequest{Identity: Identity{Token: 6}, Address: 0, ByteEnable: 1, Write: write}
			newcomer.Data[0] = 99
			responses := map[uint64]WordResponse{}
			responseCycles := map[uint64]uint64{}
			storeCycles := map[uint64]uint64{}
			for offset := uint64(1); offset <= 20; offset++ {
				in := bankInput{responseReady: true}
				if offset == 1 {
					in.request = WordOffer{true, newcomer}
				}
				e := h.step(in, true)
				if offset == 1 && !e.accepted {
					t.Fatal("RTL creq_grant must admit newcomer at F+1 while read head forwards")
				}
				if e.responseDelivered {
					id := e.response.response.Identity.Token
					responses[id] = e.response.response
					responseCycles[id] = fillCycle + offset
				}
				if e.storeApplied != nil {
					storeCycles[e.storeApplied.Identity.Token] = fillCycle + offset
				}
			}
			for _, id := range []uint64{1, 2, 3, 5} {
				want := byte(0)
				if id == 5 {
					want = 77
				}
				r, ok := responses[id]
				if !ok || r.Data[0] != want {
					t.Fatalf("chain read %d: got %+v, want byte %d", id, r, want)
				}
			}
			if responseCycles[1] != fillCycle+2 || responseCycles[2] != fillCycle+3 {
				t.Fatal("leading fill-forward edges changed")
			}
			if write {
				if storeCycles[6] != fillCycle+3 || storeCycles[4] != fillCycle+6 || responseCycles[3] != fillCycle+4 || responseCycles[5] != fillCycle+8 {
					t.Fatalf("write/replay edges: stores=%v reads=%v fill=%d", storeCycles, responseCycles, fillCycle)
				}
			} else {
				r, ok := responses[6]
				if !ok || r.Data[0] != 0 || responseCycles[6] != fillCycle+4 || storeCycles[4] != fillCycle+6 || responseCycles[3] != fillCycle+6 || responseCycles[5] != fillCycle+8 {
					t.Fatalf("new hit must read before replay store: responses=%v cycles=%v stores=%v fill=%d", responses, responseCycles, storeCycles, fillCycle)
				}
			}
			r, hit, err := h.bank.array.read(newcomer)
			if err != nil || !hit || r.Data[0] != 77 || h.owner.writes != 0 || !h.bank.drained() {
				t.Fatalf("final replay store/cache ownership: %+v hit=%v err=%v", r, hit, err)
			}
		})
	}
}

// Frozen LATENCY=2 means PIPE_EX=0 and RTL fill_inflight is constant zero.
// Once a one-read chain forwards, the following fill may enter while the
// previous fill still occupies S1; it must not incur a software interlock.
func TestCacheBankFillAfterSingleForward(t *testing.T) {
	h := newBankHarness(t, DataCache, 30)
	for id := uint64(1); id <= 2; id++ {
		r := WordRequest{Identity: Identity{Token: id}, Address: uint32((id - 1) * 128)}
		if !h.step(bankInput{request: WordOffer{true, r}, responseReady: true}, true).accepted {
			t.Fatal("miss acceptance")
		}
	}
	var fills []uint64
	replies := 0
	for i := 0; i < 100; i++ {
		cycle := h.cycle
		e := h.step(bankInput{responseReady: true}, true)
		if e.fillAccepted {
			fills = append(fills, cycle)
		}
		if e.responseDelivered {
			replies++
		}
	}
	if len(fills) != 2 || fills[1] != fills[0]+2 || replies != 2 || !h.bank.drained() {
		t.Fatalf("zero PIPE_EX fill admission: fills=%v replies=%d", fills, replies)
	}
}
