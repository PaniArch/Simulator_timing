package memsys

import (
	"errors"
	"testing"
	"vortex.local/simulator/emu/warp"
)

func TestDFlushQueuedLoadResponseTail(t *testing.T) {
	s, step := newPortSystem(t)
	r := simd(1)
	r.Mask = 1
	r.Identity.WarpGeneration = 17
	r.Lanes[0] = LaneRequest{Address: 128, ByteEnable: 15}
	accepted := false
	var saved SIMDReply
	for i := 0; i < 500; i++ {
		e := step(SystemInput{Memory: SIMDOffer{!accepted, r}})
		accepted = accepted || e.MemoryAccepted
		if e.DataAdapterAccepted[0] {
			if !e.DataAdapterAccepted[0] || e.DataCacheAccepted[0] || len(s.dataPort.queue.values) != 1 {
				t.Fatal("single-lane handshake did not stop at register")
			}
		}
		_, reply := s.Responses()
		if reply.Valid {
			saved = reply
			break
		}
	}
	if !saved.Valid {
		t.Fatal("load response absent")
	}
	for i := 0; i < 8; i++ {
		e := step(SystemInput{})
		_, reply := s.Responses()
		if reply != saved || len(e.Complete) != 0 || s.Drained() || !s.HasResidency(r.Identity.Kernel, r.Identity.CTA) {
			t.Fatal("stalled response lost payload or residency")
		}
	}
	completed := 0
	for i := 0; i < 20; i++ {
		e := step(SystemInput{MemoryReady: true})
		for _, id := range e.Complete {
			if id != r.Identity {
				t.Fatal("generation/identity changed")
			}
			completed++
		}
	}
	if completed != 1 || !s.Drained() || s.HasResidency(r.Identity.Kernel, r.Identity.CTA) {
		t.Fatal("response tail not released exactly once")
	}
}

func TestDFlushStoreAndFlushFaultTail(t *testing.T) {
	for _, fail := range []bool{false, true} {
		_, owner := setup(t, Config{100, 1, 16, 1}, 1, 4096)
		injected := errors.New("writeback fault")
		if fail {
			owner.writeErr = injected
		}
		s, err := NewSystem(owner, func(Identity) (warp.AtomicMemoryService, error) { return owner, nil }, Config{100, 1, 16, 1})
		if err != nil {
			t.Fatal(err)
		}
		r := simd(1)
		r.Mask = 1
		r.Write = true
		r.Lanes[0] = LaneRequest{Address: 128, ByteEnable: 15, Data: [4]byte{93}}
		f := FlushOffer{Valid: true, Identity: Identity{Kernel: 7, CTA: 11, WarpGeneration: 18, Transaction: 1}, Tag: 701}
		accepted, fa, cacheFlush, replySeen := false, false, false, false
		queued := false
		applications, completions, errorsSeen := 0, 0, 0
		var reply FlushReply
		var cycle uint64
		for ; cycle < 1500; cycle++ {
			in := SystemInput{Memory: SIMDOffer{!accepted, r}, MemoryReady: true}
			// The cold Cache stalls the queued store. Inject flush behind that word.
			if queued && !fa {
				in.DataFlush = f
			}
			e, err := s.Step(cycle, in)
			if err != nil {
				t.Fatal(err)
			}
			accepted = accepted || e.MemoryAccepted
			if e.DataAdapterAccepted[0] {
				queued = true
				if e.DataCacheAccepted[0] || !e.DataAdapterAccepted[0] {
					t.Fatal("fixture did not queue store before Cache acceptance")
				}
			}
			if e.DataFlushAccepted {
				fa = true
				if len(s.dataPort.queue.values) != 2 {
					t.Fatal("flush did not share full cold buffer")
				}
			}
			cacheFlush = cacheFlush || e.DataCacheFlushAccepted
			for _, st := range e.Stores {
				if st.Identity != r.Identity || st.Mask != 1 || st.Errors[0] != nil {
					t.Fatal("store identity/application", st)
				}
				applications++
			}
			completions += len(e.Complete)
			errorsSeen += len(e.WritebackErrors)
			if e.DataFlush.Valid {
				reply = e.DataFlush
				replySeen = true
				cycle++
				break
			}
			if s.Drained() {
				t.Fatal("premature drain")
			}
		}
		if !replySeen || !cacheFlush || !fa || applications != 1 || completions != 1 || owner.writes != 1 {
			t.Fatal("lost/duplicate store or flush", applications, completions, owner.writes)
		}
		if reply.Identity != f.Identity || reply.Tag != f.Tag || reply.Delivered || errors.Is(reply.Err, injected) != fail {
			t.Fatal("flush outcome/identity", reply)
		}
		if fail && errorsSeen != 1 {
			t.Fatal("writeback error not delivered once", errorsSeen)
		}
		for n := 0; n < 8; n++ {
			e, err := s.Step(cycle, SystemInput{MemoryReady: true})
			cycle++
			if err != nil {
				t.Fatal(err)
			}
			if e.DataFlush != reply || s.Drained() || !s.HasResidency(7, 11) {
				t.Fatal("flush reply tail released under backpressure")
			}
		}
		e, err := s.Step(cycle, SystemInput{MemoryReady: true, DataFlushReady: true})
		cycle++
		if err != nil {
			t.Fatal(err)
		}
		if !e.DataFlush.Delivered {
			t.Fatal("flush reply not delivered")
		}
		for n := 0; n < 5; n++ {
			e, err = s.Step(cycle, SystemInput{MemoryReady: true, DataFlushReady: true})
			cycle++
			if err != nil || e.DataFlush.Valid || len(e.Stores) != 0 {
				t.Fatal("reply/store replay", e, err)
			}
		}
		if !s.Drained() || s.HasResidency(7, 11) {
			t.Fatal("completed flush leaked identity")
		}
		got := owner.Snapshot()[128]
		want := byte(93)
		if fail {
			want = 0
		}
		if got != want {
			t.Fatal("backing visibility", got, want)
		}
	}
}
