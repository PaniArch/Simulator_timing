package memsys

import (
	"errors"
	"testing"
)

type splitHarness struct {
	t     *testing.T
	s     *SIMDSplit
	cycle uint64
}

func newSplitHarness(t *testing.T) *splitHarness {
	s, err := NewSIMDSplit()
	if err != nil {
		t.Fatal(err)
	}
	return &splitHarness{t: t, s: s}
}
func (h *splitHarness) step(in SplitInput) SplitEdge {
	h.t.Helper()
	e, err := h.s.Step(h.cycle, in)
	if err != nil {
		h.t.Fatalf("cycle %d: %v", h.cycle, err)
	}
	h.cycle++
	return e
}
func completion(r SIMDRequest, mask uint8) SIMDResponse {
	return SIMDResponse{Identity: r.Identity, Tag: r.Tag, Mask: mask}
}
func TestSplitAsymmetricAcceptanceAndCompletion(t *testing.T) {
	for slow := 0; slow < 2; slow++ {
		for _, write := range []bool{false, true} {
			name := "global-slow"
			if slow == LocalPath {
				name = "local-slow"
			}
			if write {
				name += "/store"
			} else {
				name += "/load"
			}
			t.Run(name, func(t *testing.T) {
				h := newSplitHarness(t)
				fast := 1 - slow
				blocker := simd(1)
				blocker.Mask = 1
				blocker.Write = write
				blocker.Lanes[0].Local = slow == LocalPath
				if !h.step(SplitInput{Request: SIMDOffer{true, blocker}}).Accepted {
					t.Fatal("blocker not buffered")
				}
				r := simd(2)
				r.Mask = 3
				r.Write = write
				r.Lanes[0].Local = false
				r.Lanes[1].Local = true
				offer := SIMDOffer{true, r}
				e := h.step(SplitInput{Request: offer})
				if e.Accepted || !e.SubsetAccepted[fast] || e.SubsetAccepted[slow] {
					t.Fatal("asymmetric child buffer acceptance")
				}
				ready := [2]bool{}
				ready[fast] = true
				e = h.step(SplitInput{Request: offer, PathReady: ready})
				if !e.OutputDelivered[fast] || e.Accepted {
					t.Fatal("fast child not independently delivered")
				}
				in := SplitInput{Request: offer, PathReady: ready, ResponseReady: true}
				fastMask := uint8(1 << fast)
				if write {
					in.Stores[fast] = []SIMDResponse{completion(r, fastMask)}
				} else {
					in.Reads[fast] = SIMDReply{true, completion(r, fastMask)}
				}
				e = h.step(in)
				if len(e.Complete) != 0 {
					t.Fatal("partial completion finished mixed request")
				}
				for i := 0; i < 4; i++ {
					e = h.step(SplitInput{Request: offer, PathReady: ready, ResponseReady: true})
					if e.Outputs[fast].Valid || e.SubsetAccepted[fast] || e.Accepted || len(e.Complete) > 0 {
						t.Fatal("repeated fast subset or premature completion")
					}
				}
				if !h.s.HasResidency(7, 11) {
					t.Fatal("mixed tail lost residency")
				}
				ready[slow] = true
				e = h.step(SplitInput{Request: offer, PathReady: ready, ResponseReady: true})
				if !e.Accepted || !e.SubsetAccepted[slow] || !e.OutputDelivered[slow] || len(e.Complete) > 0 {
					t.Fatal("single-slot replacement did not restore input")
				}
				e = h.step(SplitInput{PathReady: ready, ResponseReady: true})
				if !e.OutputDelivered[slow] || e.OutputDelivered[fast] {
					t.Fatal("slow mixed subset missing or fast duplicated")
				}
				in = SplitInput{ResponseReady: true}
				if write {
					in.Stores[slow] = []SIMDResponse{completion(r, 1<<slow)}
				} else {
					in.Reads[slow] = SIMDReply{true, completion(r, 1<<slow)}
				}
				e = h.step(in)
				if !write {
					if len(e.Complete) != 0 {
						t.Fatal("load completed before response delivery")
					}
					e = h.step(SplitInput{ResponseReady: true})
				}
				if len(e.Complete) != 1 || e.Complete[0] != r.Identity {
					t.Fatal("mixed completion missing")
				}
				// Blocker's identity remains independently live until its own event.
				in = SplitInput{ResponseReady: true}
				if write {
					in.Stores[slow] = []SIMDResponse{completion(blocker, 1)}
				} else {
					in.Reads[slow] = SIMDReply{true, completion(blocker, 1)}
				}
				h.step(in)
				h.step(SplitInput{ResponseReady: true})
				if !h.s.Drained() {
					t.Fatal("split leaked ledger or buffers")
				}
			})
		}
	}
}
func TestSplitResponseArbitrationAndDuplicateProtection(t *testing.T) {
	h := newSplitHarness(t)
	r := simd(1)
	r.Mask = 3
	r.Lanes[1].Local = true
	h.step(SplitInput{Request: SIMDOffer{true, r}})
	// A response before the child is accepted must fail without advancing edge.
	bad := SplitInput{}
	bad.Reads[0] = SIMDReply{true, completion(r, 1)}
	if _, err := h.s.Step(h.cycle, bad); err == nil {
		t.Fatal("premature response accepted")
	}
	h.step(SplitInput{PathReady: [2]bool{true, true}})
	reads := [2]SIMDReply{{true, completion(r, 1)}, {true, completion(r, 2)}}
	e := h.step(SplitInput{Reads: reads})
	if !e.ReadReady[0] || e.ReadReady[1] {
		t.Fatal("initial round robin winner")
	}
	reads[0] = SIMDReply{}
	e = h.step(SplitInput{Reads: reads})
	if !e.Response.Valid || e.ReadReady[1] || e.ResponseDelivered {
		t.Fatal("full response buffer bypassed")
	}
	changed := reads
	changed[1].Response.Data[1][0] = 99
	if _, err := h.s.Step(h.cycle, SplitInput{Reads: changed}); err == nil {
		t.Fatal("changed stalled response accepted")
	}
	e = h.step(SplitInput{Reads: reads, ResponseReady: true})
	if !e.ResponseDelivered || !e.ReadReady[1] || len(e.Complete) != 0 {
		t.Fatal("single-slot response replacement or completion")
	}
	if _, err := h.s.Step(h.cycle, SplitInput{Reads: reads}); err == nil {
		t.Fatal("duplicate path response accepted")
	}
	e = h.step(SplitInput{ResponseReady: true})
	if !e.ResponseDelivered || e.Response.Response.Mask != 2 || len(e.Complete) != 1 || !h.s.Drained() {
		t.Fatal("response tail not retired")
	}
}
func TestSplitUnresolvedBeforeLocalSideEffect(t *testing.T) {
	h := newSplitHarness(t)
	r := simd(1)
	r.Write = true
	r.Lanes[1].Local = true
	r.Lanes[0].ByteEnable = 1
	r.Lanes[0].Data[0] = 1
	r.Lanes[2].ByteEnable = 1
	r.Lanes[2].Data[0] = 2
	if _, err := h.s.Step(0, SplitInput{Request: SIMDOffer{true, r}, PathReady: [2]bool{true, true}}); !errors.Is(err, ErrUnresolvedStoreOrder) {
		t.Fatal("missing explicit cross-group conflict")
	}
	if !h.s.Drained() {
		t.Fatal("local side accepted before global conflict rejection")
	}
}

func TestSplitPartialProgressBeforeChildInputRelease(t *testing.T) {
	h := newSplitHarness(t)
	r := simd(1)
	r.Mask = 3
	h.step(SplitInput{Request: SIMDOffer{true, r}})
	// A coalescer can consume a first batch while holding its multi-batch input.
	in := SplitInput{ResponseReady: true}
	in.Progress[0] = []SIMDResponse{completion(r, 1)}
	in.Reads[0] = SIMDReply{true, completion(r, 1)}
	h.step(in)
	e := h.step(SplitInput{ResponseReady: true})
	if !e.ResponseDelivered || len(e.Complete) > 0 || !h.s.Outputs()[0].Valid {
		t.Fatal("partial batch released whole child")
	}
	in = SplitInput{ResponseReady: true}
	in.Progress[0] = []SIMDResponse{completion(r, 2)}
	in.Reads[0] = SIMDReply{true, completion(r, 2)}
	h.step(in)
	e = h.step(SplitInput{ResponseReady: true})
	if len(e.Complete) != 0 || !h.s.HasResidency(7, 11) {
		t.Fatal("request buffer still references residency")
	}
	e = h.step(SplitInput{PathReady: [2]bool{true, false}})
	if len(e.Complete) != 1 || !h.s.Drained() {
		t.Fatal("final child release did not complete")
	}
}
