package memsys

import "testing"

// All links use public old-edge views and actual handshakes; no fabricated
// memory completion or direct backing read substitutes for a response.
func TestMixedPathCacheAndLMEM(t *testing.T) {
	for _, latency := range []uint64{1, 30} {
		h := newCacheHarness(t, Config{latency, 1, 2, 1}, DataCache)
		local := newLocalHarness(t)
		split, err := NewSIMDSplit()
		if err != nil {
			t.Fatal(err)
		}
		co, err := NewCoalescer()
		if err != nil {
			t.Fatal(err)
		}
		ga, err := NewGlobalAdapter()
		if err != nil {
			t.Fatal(err)
		}
		la, err := NewLocalAdapter()
		if err != nil {
			t.Fatal(err)
		}
		// Which global lanes have started while their coalescer input is held.
		progress := make(map[Identity]uint8)
		released := make(map[Identity]bool)
		run := func(r SIMDRequest) SIMDResponse {
			t.Helper()
			accepted := false
			done := false
			data := SIMDResponse{}
			returned := uint8(0)
			for i := 0; i < 1500; i++ {
				cycle := h.cycle
				paths := split.Outputs()
				batch := co.Output()
				gp, err := ga.Preview(h.caches[0].Responses())
				if err != nil {
					t.Fatal(err)
				}
				gread, err := co.Preview(gp)
				if err != nil {
					t.Fatal(err)
				}
				lread, err := la.Preview(local.m.Responses())
				if err != nil {
					t.Fatal(err)
				}
				reads := [2]SIMDReply{gread, lread}
				outputReady := i%7 >= 3
				pathReady := split.ReadReady(reads, outputReady)
				gr, err := ga.ReadReady(h.caches[0].Responses(), pathReady[0])
				if err != nil {
					t.Fatal(err)
				}
				lr, err := la.ReadReady(local.m.Responses(), pathReady[1])
				if err != nil {
					t.Fatal(err)
				}
				ce := h.step([]CacheInput{{Requests: ga.Offers(batch), ResponseReady: gr}})[0]
				me, err := local.m.Step(cycle, la.Offers(), lr)
				if err != nil {
					t.Fatal(err)
				}
				ge, err := ga.Step(cycle, GlobalAdapterInput{Batch: batch, Cache: ce, ResponseReady: pathReady[0]})
				if err != nil {
					t.Fatal(err)
				}
				le, err := la.Step(cycle, LocalAdapterInput{Request: paths[1], Memory: me, ResponseReady: pathReady[1]})
				if err != nil {
					t.Fatal(err)
				}
				var mask uint8
				for p, a := range ce.Accepted {
					if a {
						mask |= 1 << p
					}
				}
				coe, err := co.Step(cycle, CoalescerInput{Request: paths[0], OutputReady: ge.Accepted, OutputAcceptedMask: mask, Response: ge.Response, ResponseReady: pathReady[0]})
				if err != nil {
					t.Fatal(err)
				}
				in := SplitInput{Request: SIMDOffer{!accepted, r}, PathReady: [2]bool{coe.Accepted, le.Accepted}, Reads: reads, Stores: [2][]SIMDResponse{ge.Stores, le.Stores}, ResponseReady: outputReady}
				for _, p := range ge.Progress {
					if !released[p.Identity] {
						p.Mask &^= progress[p.Identity]
						if p.Mask != 0 {
							in.Progress[0] = append(in.Progress[0], p)
							progress[p.Identity] |= p.Mask
						}
					}
				}
				if coe.Accepted {
					released[paths[0].Request.Identity] = true
				}
				// local progress is queue acceptance, and is unique per lane.
				in.Progress[1] = le.Progress
				se, err := split.Step(cycle, in)
				if err != nil {
					t.Fatalf("mixed cycle %d: %v", cycle, err)
				}
				accepted = accepted || se.Accepted
				if se.ResponseDelivered {
					rsp := se.Response.Response
					if rsp.Identity != r.Identity || rsp.Tag != r.Tag || returned&rsp.Mask != 0 {
						t.Fatal("mixed identity or duplicate lane")
					}
					returned |= rsp.Mask
					for lane := 0; lane < 4; lane++ {
						if rsp.Mask&(1<<lane) != 0 {
							if rsp.Errors[lane] != nil {
								t.Fatal(rsp.Errors[lane])
							}
							data.Data[lane] = rsp.Data[lane]
						}
					}
				}
				for _, s := range se.Stores {
					if s.Identity != r.Identity || returned&s.Mask != 0 {
						t.Fatal("duplicate mixed store")
					}
					returned |= s.Mask
					for _, err := range s.Errors {
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, id := range se.Complete {
					if id != r.Identity {
						t.Fatal("foreign completion")
					}
					done = true
				}
				if done {
					if !accepted || returned != r.Mask {
						t.Fatal("mixed completed early")
					}
					return data
				}
			}
			t.Fatal("mixed path timed out")
			return data
		}
		r := simd(1)
		r.Identity.CTA = 0
		r.Identity.WarpGeneration = 1
		r.Write = true
		for lane := 0; lane < 4; lane++ {
			r.Lanes[lane] = LaneRequest{Address: uint32(lane * 16), ByteEnable: 15, Data: [4]byte{byte(10 + lane), 1, 2, 3}}
		}
		r.Lanes[1].Local = true
		r.Lanes[1].Address = local.addresses[0]
		r.Lanes[3].Local = true
		r.Lanes[3].Address = local.addresses[0] + 16 // same LMEM bank
		run(r)
		if h.owner.writes != 0 {
			t.Fatal("mixed store bypassed cache")
		}
		r.Write = false
		r.Identity.Transaction = 2
		r.Tag = 102
		result := run(r)
		for lane := 0; lane < 4; lane++ {
			if result.Data[lane] != r.Lanes[lane].Data {
				t.Fatalf("mixed lane %d data=%v", lane, result.Data[lane])
			}
		}
		if !split.Drained() || !co.Drained() || !ga.Drained() || !la.Drained() || !local.m.Drained() {
			t.Fatal("mixed residency tail leak")
		}
	}
}
