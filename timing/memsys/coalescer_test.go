package memsys

import (
	"errors"
	"reflect"
	"testing"
)

type coalescerHarness struct {
	t     *testing.T
	c     *Coalescer
	cycle uint64
}

func newCoalescerHarness(t *testing.T) *coalescerHarness {
	c, err := NewCoalescer()
	if err != nil {
		t.Fatal(err)
	}
	return &coalescerHarness{t: t, c: c}
}
func (h *coalescerHarness) step(in CoalescerInput) CoalescerEdge {
	h.t.Helper()
	e, err := h.c.Step(h.cycle, in)
	if err != nil {
		h.t.Fatalf("cycle %d: %v", h.cycle, err)
	}
	h.cycle++
	return e
}
func simd(id uint64) SIMDRequest {
	return SIMDRequest{Identity: Identity{Transaction: id, Kernel: 7, CTA: 11}, Tag: 100 + id, Mask: 15}
}
func (h *coalescerHarness) send(r SIMDRequest) CoalescedBatch {
	h.t.Helper()
	for i := 0; i < 2; i++ {
		e := h.step(CoalescerInput{Request: SIMDOffer{true, r}})
		if e.Accepted != (i == 1) {
			h.t.Fatal("WAIT/SEND acceptance")
		}
	}
	e := h.step(CoalescerInput{OutputReady: true})
	if !e.OutputDelivered {
		h.t.Fatal("missing registered output")
	}
	return e.Output.Batch
}
func TestCoalescerGroupingAndByteOverlap(t *testing.T) {
	r := simd(1)
	r.Write = true
	r.Lanes[0] = LaneRequest{Address: 0, ByteEnable: 3, Data: [4]byte{10, 11}}
	r.Lanes[1] = LaneRequest{Address: 0, ByteEnable: 6, Data: [4]byte{0, 21, 22}}
	r.Lanes[2] = LaneRequest{Address: 4, ByteEnable: 15, Data: [4]byte{30, 31, 32, 33}}
	r.Lanes[3] = LaneRequest{Address: 16, ByteEnable: 1, Data: [4]byte{40}}
	if err := validateGlobal(r); err != nil {
		t.Fatal(err)
	}
	b := makeBatch(r, 15)
	if b.Mask != 3 || b.LaneMask != 7 || b.Words[0].ByteEnable != 7 || b.Words[0].Data != [8]byte{10, 21, 22} || b.Words[1].ByteEnable != 240 || b.Words[1].Data != [8]byte{0, 0, 0, 0, 30, 31, 32, 33} {
		t.Fatalf("merge: %+v", b)
	}
	next := makeBatch(r, 8)
	if next.LaneMask != 8 || next.Mask != 2 || next.Words[1].Address != 16 {
		t.Fatal("same cache line must still produce separate word batch")
	}
	r.Lanes[2] = LaneRequest{Address: 0, ByteEnable: 7, Data: [4]byte{10, 21, 22}}
	if err := validateGlobal(r); err != nil {
		t.Fatal("identical cross-group bytes rejected", err)
	}
	r.Lanes[2].Data[1] = 99
	if !errors.Is(validateGlobal(r), ErrUnresolvedStoreOrder) {
		t.Fatal("cross-group conflict silently resolved")
	}
	h := newCoalescerHarness(t)
	if _, err := h.c.Step(0, CoalescerInput{Request: SIMDOffer{true, r}}); !errors.Is(err, ErrUnresolvedStoreOrder) || !h.c.Drained() {
		t.Fatal("conflict must fail before accepting any subset")
	}
	r.Mask = 3
	r.NoMerge = true
	b = makeBatch(r, 3)
	if b.LaneMask != 1 || makeBatch(r, 2).LaneMask != 2 {
		t.Fatal("no_merge failed")
	}
}
func TestCoalescerBatchesAndPartialResponses(t *testing.T) {
	h := newCoalescerHarness(t)
	r := simd(1)
	r.Lanes[0].Address = 0
	r.Lanes[1].Address = 8
	r.Lanes[2].Address = 4
	r.Lanes[3].Address = 16
	offer := SIMDOffer{true, r}
	if h.step(CoalescerInput{Request: offer}).Accepted {
		t.Fatal("accepted at WAIT")
	}
	if h.step(CoalescerInput{Request: offer}).Accepted {
		t.Fatal("accepted before final batch")
	}
	first := h.c.Output()
	for i := 0; i < 3; i++ {
		e := h.step(CoalescerInput{Request: offer})
		if e.Output != first || e.OutputDelivered {
			t.Fatal("unstable held output")
		}
	}
	e := h.step(CoalescerInput{Request: offer, OutputReady: true})
	if !e.OutputDelivered || e.Accepted {
		t.Fatal("first batch handshake")
	}
	if !h.step(CoalescerInput{Request: offer}).Accepted {
		t.Fatal("final batch must accept upstream in SEND")
	}
	second := h.step(CoalescerInput{OutputReady: true}).Output.Batch
	if first.Batch.LaneMask != 5 || second.LaneMask != 10 || first.Batch.ID == second.ID {
		t.Fatal("batch participant/slot mapping")
	}
	if !h.c.Empty(false) || h.c.Drained() {
		t.Fatal("empty is not response drain")
	}
	// Return channel 1 of the second batch first; only lane 3 expands.
	rsp := BatchReply{true, BatchResponse{ID: second.ID, Mask: 2, Data: [2][8]byte{{}, {1, 2, 3, 4, 5, 6, 7, 8}}}}
	held := h.step(CoalescerInput{Response: rsp})
	if !held.ResponseValid || held.ResponseDelivered || held.Response.Mask != 8 || held.Response.Data[3] != [4]byte{1, 2, 3, 4} {
		t.Fatal("partial response expansion")
	}
	again := h.step(CoalescerInput{Response: rsp, ResponseReady: true})
	if !reflect.DeepEqual(held.Response, again.Response) || !again.ResponseDelivered {
		t.Fatal("held response changed")
	}
	if _, err := h.c.Step(h.cycle, CoalescerInput{Response: rsp}); err == nil {
		t.Fatal("duplicate fragment accepted")
	}
	for _, fragment := range []BatchResponse{
		{ID: first.Batch.ID, Mask: 2, Data: [2][8]byte{{}, {0, 0, 0, 0, 9, 10, 11, 12}}},
		{ID: second.ID, Mask: 1}, {ID: first.Batch.ID, Mask: 1},
	} {
		e := h.step(CoalescerInput{Response: BatchReply{true, fragment}, ResponseReady: true})
		if e.Response.Identity != r.Identity || e.Response.Tag != r.Tag {
			t.Fatal("response identity lost")
		}
		if fragment.ID == first.Batch.ID && fragment.Mask == 2 && (e.Response.Mask != 4 || e.Response.Data[2] != [4]byte{9, 10, 11, 12}) {
			t.Fatal("upper word offset lost")
		}
	}
	if !h.c.Drained() || h.c.HasResidency(7, 11) {
		t.Fatal("response slot leak")
	}
}
func TestCoalescerFullRecoveryAndStaleGeneration(t *testing.T) {
	h := newCoalescerHarness(t)
	var batches []CoalescedBatch
	for i := 0; i < len(h.c.slots); i++ {
		r := simd(uint64(i + 1))
		r.Mask = 1
		batches = append(batches, h.send(r))
	}
	store := simd(9)
	store.Mask = 1
	store.Write = true
	for i := 0; i < 3; i++ {
		if h.step(CoalescerInput{Request: SIMDOffer{true, store}}).Accepted {
			t.Fatal("store bypassed full read index buffer")
		}
	}
	response := BatchReply{true, BatchResponse{ID: batches[0].ID, Mask: 1}}
	if h.step(CoalescerInput{Request: SIMDOffer{true, store}, Response: response, ResponseReady: true}).Accepted {
		t.Fatal("borrowed same-edge full release")
	}
	if h.step(CoalescerInput{Request: SIMDOffer{true, store}}).Accepted {
		t.Fatal("WAIT skipped")
	}
	if !h.step(CoalescerInput{Request: SIMDOffer{true, store}}).Accepted {
		t.Fatal("SEND did not recover")
	}
	h.step(CoalescerInput{OutputReady: true})
	r := simd(10)
	r.Mask = 1
	reused := h.send(r)
	if reused.ID.Slot != batches[0].ID.Slot || reused.ID.Generation == batches[0].ID.Generation {
		t.Fatal("slot generation not advanced")
	}
	if _, err := h.c.Step(h.cycle, CoalescerInput{Response: response, ResponseReady: true}); err == nil {
		t.Fatal("stale slot generation accepted")
	}
	for _, b := range append(batches[1:], reused) {
		h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: b.ID, Mask: 1}}, ResponseReady: true})
	}
	if !h.c.Drained() {
		t.Fatal("did not drain")
	}
}
func TestCoalescerLaneWidthsAndFault(t *testing.T) {
	for _, enable := range []uint8{1, 2, 3, 12, 15} {
		h := newCoalescerHarness(t)
		r := simd(1)
		r.Mask = 2
		r.Lanes[1] = LaneRequest{Address: 4, ByteEnable: enable, Data: [4]byte{1, 2, 3, 4}}
		b := h.send(r)
		if b.Words[0].ByteEnable != enable<<4 || b.LaneMask != 2 {
			t.Fatal("byte/half/word enable shifted incorrectly")
		}
		fault := errors.New("refill error")
		e := h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: b.ID, Mask: 1, Errors: [2]error{fault, nil}, Data: [2][8]byte{{1, 2, 3, 4, 5, 6, 7, 8}}}}, ResponseReady: true})
		if e.Response.Mask != 2 || !errors.Is(e.Response.Errors[1], fault) || e.Response.Data[1] != [4]byte{} {
			t.Fatal("fault identity/data")
		}
	}
}

func TestCoalescerRegisteredAllocatorAndHeldInputs(t *testing.T) {
	h := newCoalescerHarness(t)
	r := simd(1)
	r.Mask = 1
	a := h.send(r)
	r.Identity.Transaction = 2
	b := h.send(r)
	h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: a.ID, Mask: 1}}, ResponseReady: true})
	r.Identity.Transaction = 3
	// VX_allocator does not reselect acquire_addr on a non-full release.
	next := h.send(r)
	if a.ID.Slot != 0 || b.ID.Slot != 1 || next.ID.Slot != 2 {
		t.Fatal("release incorrectly changed registered allocation index")
	}
	r.Identity.Transaction = 4
	offered := SIMDOffer{true, r}
	h.step(CoalescerInput{Request: offered})
	changed := offered
	changed.Request.Tag++
	if _, err := h.c.Step(h.cycle, CoalescerInput{Request: changed}); err == nil {
		t.Fatal("stalled input changed")
	}
	if !h.step(CoalescerInput{Request: offered}).Accepted {
		t.Fatal("protocol rejection mutated SEND state")
	}
}
