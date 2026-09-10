package memsys

import "testing"

func TestInlineFlushReleasesOriginalWord(t *testing.T) {
	for _, kind := range []CacheKind{InstructionCache, DataCache} {
		h := newCacheHarness(t, Config{3, 1, 8, 1}, kind)
		if kind == DataCache {
			w := word(1, 0)
			w.Write = true
			w.Data = [8]byte{9, 8, 7, 6, 5, 4, 3, 2}
			h.run(0, 0, w)
			if h.owner.writes != 0 {
				t.Fatal("store bypass")
			}
		}
		f := word(2, 0)
		f.Flush = true
		if kind == InstructionCache {
			f.ByteEnable = 15
		}
		offers := make([]WordOffer, h.caches[0].Spec().CorePorts)
		offers[0] = WordOffer{true, f}
		if len(offers) > 1 {
			offers[1] = WordOffer{true, word(3, 128)}
		}
		first, normal, returned := -1, -1, false
		for cycle := 0; cycle < 1800; cycle++ {
			for _, o := range h.caches[0].MemoryOffers() {
				if o.Valid && o.Request.Operation == Visibility {
					t.Fatal("inline flush invented visibility ticket")
				}
			}
			e := h.step([]CacheInput{{Requests: offers, ResponseReady: make([]bool, len(offers))}})[0]
			if e.Flush.Valid || e.FlushAccepted {
				t.Fatal("inline flush used independent control response")
			}
			for p, a := range e.Accepted {
				if a {
					if p == 0 {
						first = cycle
					} else {
						normal = cycle
					}
					offers[p] = WordOffer{}
				}
			}
			if e.Replies[0].Valid {
				if e.Replies[0].Response.Identity != f.Identity || e.Replies[0].Response.Tag != f.Tag || e.Replies[0].Response.Err != nil {
					t.Fatal("original flush identity lost")
				}
				if kind == DataCache && e.Replies[0].Response.Data != ([8]byte{9, 8, 7, 6, 5, 4, 3, 2}) {
					t.Fatal("flush original read lost data")
				}
				returned = true
			}
			if returned && (len(offers) == 1 || normal >= 0) {
				break
			}
		}
		if first < h.caches[0].Spec().Sets || !returned || (len(offers) > 1 && normal <= first) {
			t.Fatalf("scan/admission order first=%d normal=%d returned=%v", first, normal, returned)
		}
		for i := 0; i < 30; i++ {
			h.step(nil)
		}
		if !h.caches[0].Drained() {
			t.Fatal("inline flush tail leak")
		}
	}
}

func TestCoalescerRetainsFlushAttribute(t *testing.T) {
	r := simd(1)
	r.Flush = true
	b := makeBatch(r, r.Mask)
	for p, w := range b.Words {
		if b.Mask&(1<<p) != 0 && !w.Flush {
			t.Fatal("lost flush attribute")
		}
	}
}

func TestInlineFlushDoesNotLockOuterNCBypass(t *testing.T) {
	h := newCacheHarness(t, Config{3, 1, 8, 1}, DataCache)
	f, n := word(1, 0), word(1, 128)
	f.Flush = true
	n.NonCacheable = true
	n.Flush = true
	offers := []WordOffer{{true, f}, {true, n}}
	e := h.step([]CacheInput{{Requests: offers}})[0]
	if e.Accepted[0] || !e.Accepted[1] {
		t.Fatal("cache scan locked outer NC bypass or accepted flush early")
	}
	offers[1] = WordOffer{}
	for i := 0; i < 1500; i++ {
		e = h.step([]CacheInput{{Requests: offers}})[0]
		if e.Accepted[0] {
			offers[0] = WordOffer{}
		}
		if !offers[0].Valid && h.caches[0].Drained() {
			return
		}
	}
	t.Fatal("inline/NC flush failed to drain")
}
