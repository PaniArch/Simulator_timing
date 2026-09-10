package memsys

import "testing"

func adapterBatch(id uint64, write bool) BatchOffer {
	b := CoalescedBatch{ID: BatchID{int(id), 1}, Identity: Identity{Kernel: 7, CTA: 11, Transaction: id}, Tag: 100 + id, Mask: 3, LaneMask: 15, Write: write}
	for p := 0; p < 2; p++ {
		b.Words[p] = WordRequest{Address: uint32(p * 64), Write: write, ByteEnable: 255}
	}
	return BatchOffer{true, b}
}
func emptyCacheEdge() CacheEdge {
	return CacheEdge{Accepted: make([]bool, 2), Replies: make([]WordReply, 2)}
}
func TestGlobalAdapterPartialAcceptAndPack(t *testing.T) {
	a, err := NewGlobalAdapter()
	if err != nil {
		t.Fatal(err)
	}
	b := adapterBatch(1, false)
	words := a.Offers(b)
	edge := emptyCacheEdge()
	edge.Accepted[1] = true
	e, err := a.Step(0, GlobalAdapterInput{Batch: b, Cache: edge})
	if err != nil || e.Accepted || len(e.Progress) != 1 || e.Progress[0].Mask != 12 {
		t.Fatalf("partial acceptance %+v %v", e, err)
	}
	if a.Offers(b)[1].Valid || !a.Offers(b)[0].Valid {
		t.Fatal("unpack repeated accepted port")
	}
	// First word can respond while the other port remains blocked.
	edge = emptyCacheEdge()
	edge.Replies[1] = WordReply{true, true, WordResponse{Identity: words[1].Request.Identity, Tag: words[1].Request.Tag, ByteEnable: 255, Data: [8]byte{9}}}
	e, err = a.Step(1, GlobalAdapterInput{Batch: b, Cache: edge, ResponseReady: true})
	if err != nil || !e.ResponseDelivered || e.Response.Response.Mask != 2 || e.Response.Response.ID != b.Batch.ID || a.Drained() {
		t.Fatalf("partial response %+v %v", e, err)
	}
	edge = emptyCacheEdge()
	edge.Accepted[0] = true
	e, err = a.Step(2, GlobalAdapterInput{Batch: b, Cache: edge})
	if err != nil || !e.Accepted {
		t.Fatalf("final port %v", err)
	}
	b2 := adapterBatch(2, false)
	words2 := a.Offers(b2)
	edge = emptyCacheEdge()
	edge.Accepted[1] = true
	if _, err = a.Step(3, GlobalAdapterInput{Batch: b2, Cache: edge}); err != nil {
		t.Fatal(err)
	}
	reads := []WordReply{
		{Valid: true, Response: WordResponse{Identity: words[0].Request.Identity, Tag: words[0].Request.Tag, ByteEnable: 255}},
		{Valid: true, Response: WordResponse{Identity: words2[1].Request.Identity, Tag: words2[1].Request.Tag, ByteEnable: 255}},
	}
	packed, err := a.Preview(reads)
	if err != nil || packed.Response.ID != b.Batch.ID || packed.Response.Mask != 1 {
		t.Fatal("different tags incorrectly packed", err)
	}
	ready, err := a.ReadReady(reads, true)
	if err != nil || !ready[0] || ready[1] {
		t.Fatal("priority tag ready", err)
	}
	edge = emptyCacheEdge()
	edge.Replies = reads
	edge.Replies[0].Delivered = true
	if _, err = a.Step(4, GlobalAdapterInput{Batch: b2, Cache: edge, ResponseReady: true}); err != nil {
		t.Fatal(err)
	}
	// The unselected port remains held and keeps b2 residency live.
	if !a.HasResidency(7, 11) {
		t.Fatal("response ownership lost")
	}
	if _, err = a.Preview(reads); err == nil {
		t.Fatal("retired response repeated")
	}
}
func TestGlobalAdapterCacheCoalescerData(t *testing.T) {
	h := newCacheHarness(t, Config{7, 1, 4, 1}, DataCache)
	a, err := NewGlobalAdapter()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCoalescer()
	if err != nil {
		t.Fatal(err)
	}
	run := func(r SIMDRequest) SIMDResponse {
		t.Helper()
		accepted := false
		done := uint8(0)
		result := SIMDResponse{}
		for i := 0; i < 1000; i++ {
			cycle := h.cycle
			b := c.Output()
			offers := a.Offers(b)
			ready, err := a.ReadReady(h.caches[0].Responses(), true)
			if err != nil {
				t.Fatal(err)
			}
			ce := h.step([]CacheInput{{Requests: offers, ResponseReady: ready}})[0]
			ae, err := a.Step(cycle, GlobalAdapterInput{Batch: b, Cache: ce, ResponseReady: true})
			if err != nil {
				t.Fatal(err)
			}
			var channels uint8
			for p, v := range ce.Accepted {
				if v {
					channels |= 1 << p
				}
			}
			co, err := c.Step(cycle, CoalescerInput{Request: SIMDOffer{!accepted, r}, OutputReady: ae.Accepted, OutputAcceptedMask: channels, Response: ae.Response, ResponseReady: true})
			if err != nil {
				t.Fatal(err)
			}
			accepted = accepted || co.Accepted
			if r.Write {
				for _, s := range ae.Stores {
					if s.Identity != r.Identity || s.Tag != r.Tag || s.Mask&done != 0 {
						t.Fatal("store identity/duplicate")
					}
					done |= s.Mask
					for lane := range s.Errors {
						if s.Errors[lane] != nil {
							t.Fatal(s.Errors[lane])
						}
					}
				}
			} else if co.ResponseDelivered {
				if co.Response.Identity != r.Identity || co.Response.Tag != r.Tag || co.Response.Mask&done != 0 {
					t.Fatal("load identity/duplicate")
				}
				done |= co.Response.Mask
				for lane := 0; lane < 4; lane++ {
					if co.Response.Mask&(1<<lane) != 0 {
						result.Data[lane] = co.Response.Data[lane]
					}
				}
			}
			if accepted && done == r.Mask {
				return result
			}
		}
		t.Fatal("composed path timed out")
		return result
	}
	r := simd(1)
	r.Write = true
	for lane, addr := range []uint32{0, 4, 64, 80} {
		r.Lanes[lane] = LaneRequest{Address: addr, ByteEnable: 15, Data: [4]byte{byte(lane + 10), 2, 3, 4}}
	}
	run(r)
	if h.owner.writes != 0 {
		t.Fatal("cached store bypassed writeback")
	}
	r.Identity.Transaction = 2
	r.Tag = 102
	r.Write = false
	got := run(r)
	for lane := range r.Lanes {
		if got.Data[lane] != r.Lanes[lane].Data {
			t.Fatalf("lane %d got %v", lane, got.Data[lane])
		}
	}
	if !a.Drained() || !c.Drained() {
		t.Fatal("adapter/coalescer ownership leak")
	}
}
func TestCoalescerResponseBeforeWholeBatchAccepted(t *testing.T) {
	h := newCoalescerHarness(t)
	r := simd(1)
	h.step(CoalescerInput{Request: SIMDOffer{true, r}})
	h.step(CoalescerInput{Request: SIMDOffer{true, r}})
	b := h.c.Output().Batch
	h.step(CoalescerInput{OutputAcceptedMask: 1})
	e := h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: b.ID, Mask: 1}}, ResponseReady: true})
	if !e.ResponseDelivered || e.Response.Mask != 3 || !h.c.Output().Valid {
		t.Fatal("partial channel response blocked behind whole-batch acceptance")
	}
	h.step(CoalescerInput{OutputAcceptedMask: 2, OutputReady: true})
	h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: b.ID, Mask: 2}}, ResponseReady: true})
	if !h.c.Drained() {
		t.Fatal("partial batch response leak")
	}
}
