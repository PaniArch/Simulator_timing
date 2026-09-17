package memsys

import (
	"errors"
	"reflect"
	"testing"
)

// This is the shared conv3/dogfood/fence/softmax/stencil3d failure mechanism:
// a stalled low-priority word is hidden by another batch of the same SIMD
// request. Cache producer replies are controlled; all adapter/coalescer/split
// requests, progress, previews and ready signals use the public interfaces.
func TestSameParentFragmentReselection(t *testing.T) {
	s, err := NewSIMDSplit()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCoalescer()
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewGlobalAdapter()
	if err != nil {
		t.Fatal(err)
	}
	r := simd(1)
	r.Identity.WarpGeneration = 3
	r.Identity.Epoch = 2
	for lane := range r.Lanes {
		r.Lanes[lane] = LaneRequest{Address: uint32(lane * 16), ByteEnable: 15}
	}
	var cycle uint64
	var accepted bool
	var released bool
	var words [][2]WordOffer
	var batches []BatchID
	var returned uint8
	completions := 0
	step := func(replies []WordReply, ready bool) (SIMDReply, SplitEdge) {
		t.Helper()
		paths, batch := s.Outputs(), c.Output()
		packed, err := a.Preview(replies)
		if err != nil {
			t.Fatal(err)
		}
		expanded, err := c.Preview(packed)
		if err != nil {
			t.Fatal(err)
		}
		reads := [2]SIMDReply{expanded, {}}
		pr := s.ReadReady(reads, ready)
		wr, err := a.ReadReady(replies, pr[0])
		if err != nil {
			t.Fatal(err)
		}
		ce := emptyCacheEdge()
		ce.Replies = append([]WordReply(nil), replies...)
		offers := a.Offers(batch)
		var channels uint8
		for p := range offers {
			ce.Accepted[p] = offers[p].Valid
			if ce.Accepted[p] {
				channels |= 1 << p
			}
			ce.Replies[p].Delivered = ce.Replies[p].Valid && wr[p]
		}
		if batch.Valid {
			words = append(words, [2]WordOffer{offers[0], offers[1]})
			batches = append(batches, batch.Batch.ID)
		}
		ae, err := a.Step(cycle, GlobalAdapterInput{Batch: batch, Cache: ce, ResponseReady: pr[0]})
		if err != nil {
			t.Fatal(err)
		}
		co, err := c.Step(cycle, CoalescerInput{Request: paths[0], OutputReady: ae.Accepted, OutputAcceptedMask: channels, Response: ae.Response, ResponseReady: pr[0]})
		if err != nil {
			t.Fatal(err)
		}
		if co.ResponseValid != expanded.Valid || (expanded.Valid && !reflect.DeepEqual(co.Response, expanded.Response)) {
			t.Fatal("Preview/Step provenance differs")
		}
		// As in System, whole-child release already accounts for all lanes;
		// subsequent adapter acceptance is not a second split progress event.
		var progress []SIMDResponse
		if !released {
			progress = ae.Progress
		}
		released = released || co.Accepted
		se, err := s.Step(cycle, SplitInput{Request: SIMDOffer{!accepted, r}, PathReady: [2]bool{co.Accepted, false}, Progress: [2][]SIMDResponse{progress, nil}, Reads: reads, ResponseReady: ready})
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		accepted = accepted || se.Accepted
		if se.ResponseDelivered {
			rsp := se.Response.Response
			if rsp.Identity != r.Identity || rsp.Tag != r.Tag || returned&rsp.Mask != 0 {
				t.Fatal("foreign or duplicate delivery")
			}
			for lane := range rsp.Data {
				if rsp.Mask&(1<<lane) != 0 && (rsp.Data[lane] != [4]byte{byte(lane + 1)} || rsp.Errors[lane] != nil) {
					t.Fatalf("lane %d data/error lost", lane)
				}
			}
			returned |= rsp.Mask
		}
		for _, id := range se.Complete {
			completions++
			if id != r.Identity || returned != r.Mask {
				t.Fatal("premature or foreign completion")
			}
		}
		cycle++
		return expanded, se
	}
	empty := func() []WordReply { return make([]WordReply, 2) }
	for i := 0; i < 12 && len(words) < 2; i++ {
		step(empty(), false)
	}
	if len(words) != 2 || batches[0] == batches[1] {
		t.Fatal("expected two distinct batches")
	}
	reply := func(batch, port, lane int) WordReply {
		w := words[batch][port].Request
		return WordReply{Valid: true, Response: WordResponse{Identity: w.Identity, Tag: w.Tag, ByteEnable: 255, Data: [8]byte{byte(lane + 1)}}}
	}
	reads := empty()
	reads[0] = reply(0, 0, 0)
	step(reads, false) // Fill split's actual single-entry response buffer.
	reads = empty()
	reads[1] = reply(0, 1, 2)
	old, se := step(reads, false)
	if old.Response.Mask != 4 || se.ReadReady[0] {
		t.Fatal("low-priority fragment was not stalled")
	}
	reads[0] = reply(1, 0, 1)
	// Even when hidden by the higher-priority batch, the old producer must not
	// change its data, error or identity. Failed validation must not advance it.
	for _, change := range []func(*WordReply){
		func(r *WordReply) { r.Response.Data[0]++ },
		func(r *WordReply) { r.Response.Err = errors.New("changed error") },
		func(r *WordReply) { r.Response.Identity.WarpGeneration++ },
		func(r *WordReply) { r.Valid = false },
	} {
		ce := emptyCacheEdge()
		ce.Replies = append([]WordReply(nil), reads...)
		change(&ce.Replies[1])
		if _, err := a.Step(cycle, GlobalAdapterInput{Cache: ce}); err == nil {
			t.Fatal("hidden stalled producer mutation accepted")
		}
	}
	now, se := step(reads, false)
	if now.Response.Mask != 2 || now.Response.Batch == old.Response.Batch || now.Response.Identity != old.Response.Identity || se.ReadReady[0] {
		t.Fatal("same-parent batch reselection not exercised")
	}
	step(reads, true) // Consume higher-priority batch, preserving the held port 1.
	reads[0] = WordReply{}
	again, _ := step(reads, true)
	if !reflect.DeepEqual(again, old) {
		t.Fatal("reselected fragment changed")
	}
	reads[1] = reply(1, 1, 3)
	step(reads, true)
	step(empty(), true)
	step(empty(), true)
	if returned != 15 || completions != 1 || !s.Drained() || !c.Drained() || !a.Drained() {
		t.Fatal("lost lanes, duplicate completion or leaked residency")
	}
	if _, err := a.Preview(reads); err == nil {
		t.Fatal("retired producer accepted twice")
	}
}

func TestSplitFragmentStabilityAndLedger(t *testing.T) {
	h := newSplitHarness(t)
	r := simd(1)
	h.step(SplitInput{Request: SIMDOffer{true, r}})
	h.step(SplitInput{PathReady: [2]bool{true, false}})
	fragment := func(mask uint8) SIMDReply {
		rsp := completion(r, mask)
		rsp.Batch = BatchID{Slot: 1, Generation: 7}
		return SIMDReply{true, rsp}
	}
	h.step(SplitInput{Reads: [2]SIMDReply{fragment(1), {}}})
	held := fragment(4)
	h.step(SplitInput{Reads: [2]SIMDReply{held, {}}})
	for _, change := range []func(*SIMDReply){
		func(r *SIMDReply) { r.Response.Mask = 2 },
		func(r *SIMDReply) { r.Response.Tag++ },
		func(r *SIMDReply) { r.Response.Data[2][0]++ },
		func(r *SIMDReply) { r.Response.Errors[2] = errors.New("changed") },
		func(r *SIMDReply) { r.Response.Identity.CTA++ },
		func(r *SIMDReply) { r.Response.Identity.Epoch++ },
		func(r *SIMDReply) { r.Response.Identity.WarpGeneration++ },
		func(r *SIMDReply) { r.Response.Batch.Generation++; r.Response.Mask = 1 },
		func(r *SIMDReply) { r.Valid = false },
	} {
		bad := held
		change(&bad)
		if _, err := h.s.Step(h.cycle, SplitInput{Reads: [2]SIMDReply{bad, {}}}); err == nil {
			t.Fatal("illegal fragment accepted")
		}
	}
	// The same batch may gain another channel while stalled.
	grown := fragment(12)
	h.step(SplitInput{Reads: [2]SIMDReply{grown, {}}, ResponseReady: true})
	h.step(SplitInput{ResponseReady: true})
	h.step(SplitInput{Reads: [2]SIMDReply{fragment(2), {}}, ResponseReady: true})
	if e := h.step(SplitInput{ResponseReady: true}); len(e.Complete) != 1 || !h.s.Drained() {
		t.Fatal("valid recovery failed")
	}
}

func TestCoalescerRejectsUnsentFragment(t *testing.T) {
	h := newCoalescerHarness(t)
	r := simd(1)
	h.step(CoalescerInput{Request: SIMDOffer{true, r}})
	h.step(CoalescerInput{Request: SIMDOffer{true, r}})
	b := h.c.Output().Batch
	rsp := BatchReply{true, BatchResponse{ID: b.ID, Mask: 1}}
	if _, err := h.c.Preview(rsp); err == nil {
		t.Fatal("unsent preview accepted")
	}
	if _, err := h.c.Step(h.cycle, CoalescerInput{Response: rsp, ResponseReady: true}); err == nil {
		t.Fatal("unsent completion accepted")
	}
	h.step(CoalescerInput{OutputReady: true})
	h.step(CoalescerInput{Response: rsp})
	bad := rsp
	bad.Response.Data[0][0]++
	if _, err := h.c.Step(h.cycle, CoalescerInput{Response: bad}); err == nil {
		t.Fatal("changed stalled batch accepted")
	}
	h.step(CoalescerInput{Response: rsp, ResponseReady: true})
	h.step(CoalescerInput{Response: BatchReply{true, BatchResponse{ID: b.ID, Mask: 2}}, ResponseReady: true})
	if !h.c.Drained() {
		t.Fatal("rejected input corrupted slot")
	}
}
