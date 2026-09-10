package memsys

import "testing"

func localVector(id uint64) SIMDOffer {
	r := simd(id)
	for p := range r.Lanes {
		r.Lanes[p] = LaneRequest{Local: true, Address: 0xffff0000 + uint32(p*4), ByteEnable: 15}
	}
	return SIMDOffer{true, r}
}
func TestLocalAdapterFiniteBuffersAndIndependentAcceptance(t *testing.T) {
	a, err := NewLocalAdapter()
	if err != nil {
		t.Fatal(err)
	}
	edge := func() CacheEdge { return CacheEdge{Accepted: make([]bool, 4), Replies: make([]WordReply, 4)} }
	for cycle := uint64(0); cycle < 2; cycle++ {
		e, err := a.Step(cycle, LocalAdapterInput{Request: localVector(cycle + 1), Memory: edge()})
		if err != nil || !e.Accepted {
			t.Fatal("two-entry local request buffers", err)
		}
	}
	third := localVector(3)
	me := edge()
	me.Accepted[0] = true
	e, err := a.Step(2, LocalAdapterInput{Request: third, Memory: me})
	if err != nil || e.Accepted || len(e.Progress) != 0 {
		t.Fatal("full queue borrowed retirement credit", err)
	}
	e, err = a.Step(3, LocalAdapterInput{Request: third, Memory: edge()})
	if err != nil || e.Accepted || len(e.Progress) != 1 || e.Progress[0].Mask != 1 {
		t.Fatal("independent lane queue did not recover", err)
	}
	changed := third
	changed.Request.Tag++
	if _, err = a.Step(4, LocalAdapterInput{Request: changed, Memory: edge()}); err == nil {
		t.Fatal("changed held SIMD payload")
	}
	me = edge()
	for p := 1; p < 4; p++ {
		me.Accepted[p] = true
	}
	e, err = a.Step(4, LocalAdapterInput{Request: third, Memory: me})
	if err != nil || e.Accepted || len(e.Progress) != 0 {
		t.Fatal("repeated lane or same-edge credit", err)
	}
	e, err = a.Step(5, LocalAdapterInput{Request: third, Memory: edge()})
	if err != nil || !e.Accepted || len(e.Progress) != 3 {
		t.Fatal("remaining lanes did not finish", err)
	}
	// The early lane's progress is not emitted again while peers were blocked.
	for _, p := range e.Progress {
		if p.Mask == 1 {
			t.Fatal("duplicate early lane")
		}
	}
	if !a.HasResidency(7, 11) || a.Drained() {
		t.Fatal("queued requests lost residency")
	}
}
func TestLocalAdapterTagPackAndStaleResponse(t *testing.T) {
	a, err := NewLocalAdapter()
	if err != nil {
		t.Fatal(err)
	}
	empty := func() CacheEdge { return CacheEdge{Accepted: make([]bool, 4), Replies: make([]WordReply, 4)} }
	r := localVector(1)
	if _, err = a.Step(0, LocalAdapterInput{Request: r, Memory: empty()}); err != nil {
		t.Fatal(err)
	}
	offers := a.Offers()
	me := empty()
	for p := range me.Accepted {
		me.Accepted[p] = true
	}
	if _, err = a.Step(1, LocalAdapterInput{Memory: me}); err != nil {
		t.Fatal(err)
	}
	me = empty()
	for _, p := range []int{1, 3} {
		me.Replies[p] = WordReply{Valid: true, Response: WordResponse{Identity: offers[p].Request.Identity, Tag: offers[p].Request.Tag, ByteEnable: 15, Data: [8]byte{byte(10 + p)}}}
	}
	preview, err := a.Preview(me.Replies)
	if err != nil || preview.Response.Mask != 10 || preview.Response.Tag != r.Request.Tag {
		t.Fatal("matching tag pack", err)
	}
	for _, p := range []int{1, 3} {
		me.Replies[p].Delivered = true
	}
	e, err := a.Step(2, LocalAdapterInput{Memory: me, ResponseReady: true})
	if err != nil || !e.ResponseDelivered || e.Response.Response.Data[3][0] != 13 {
		t.Fatal("local lane data", err)
	}
	if _, err = a.Preview(me.Replies); err == nil {
		t.Fatal("replayed completed lane")
	}
}
