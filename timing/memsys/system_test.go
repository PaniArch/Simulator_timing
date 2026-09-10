package memsys

import (
	"testing"
	"vortex.local/simulator/emu/warp"
)

func TestSystemFetchMixedDataAndFlush(t *testing.T) {
	for _, latency := range []uint64{1, 30} {
		local := newLocalHarness(t)
		_, owner := setup(t, Config{latency, 1, 2, 1}, 1, 1<<20)
		s, err := NewSystem(owner, func(id Identity) (warp.AtomicMemoryService, error) { return local.routes[0], nil }, Config{latency, 1, 2, 1})
		if err != nil {
			t.Fatal(err)
		}
		var cycle uint64
		step := func(in SystemInput) SystemEdge {
			t.Helper()
			f, m := s.Responses()
			e, err := s.Step(cycle, in)
			cycle++
			if err != nil {
				t.Fatal(err)
			}
			if e.Fetch.Valid != f.Valid || (f.Valid && e.Fetch.Response != f.Response) || e.Memory != m {
				t.Fatal("old-edge preview changed")
			}
			return e
		}
		for i := 0; i < 100; i++ {
			step(SystemInput{})
		}
		r := simd(1)
		r.Identity.CTA = 0
		r.Identity.WarpGeneration = 1
		r.Write = true
		for lane := 0; lane < 4; lane++ {
			r.Lanes[lane] = LaneRequest{Address: uint32(lane * 16), ByteEnable: 15, Data: [4]byte{byte(lane + 10), 1, 2, 3}}
		}
		r.Lanes[1].Local = true
		r.Lanes[1].Address = local.addresses[0]
		r.Lanes[3].Local = true
		r.Lanes[3].Address = local.addresses[0] + 16
		fetch := word(1, 4096)
		fetch.ByteEnable = 15
		fetchAccepted, fetchReturned := false, false
		var fetchAcceptCycle uint64
		for phase := 0; phase < 2; phase++ {
			if phase == 1 {
				r.Write = false
				r.Identity.Transaction++
				r.Tag++
			}
			accepted, done := false, false
			var mask uint8
			for i := 0; i < 2500; i++ {
				e := step(SystemInput{Fetch: WordOffer{!fetchAccepted, fetch}, Memory: SIMDOffer{!accepted, r}, FetchReady: i%5 == 0, MemoryReady: i%7 >= 3})
				if e.FetchAccepted {
					fetchAcceptCycle = cycle - 1
				}
				fetchAccepted = fetchAccepted || e.FetchAccepted
				accepted = accepted || e.MemoryAccepted
				if e.Fetch.Delivered {
					if cycle-1 < fetchAcceptCycle+latency {
						t.Fatal("cold fetch bypassed backend latency")
					}
					if fetchReturned || e.Fetch.Response.Identity != fetch.Identity || e.Fetch.Response.Err != nil {
						t.Fatal("fetch identity/error/repetition")
					}
					fetchReturned = true
				}
				if e.MemoryDelivered {
					rsp := e.Memory.Response
					if rsp.Identity != r.Identity || mask&rsp.Mask != 0 {
						t.Fatal("load identity/repetition")
					}
					mask |= rsp.Mask
					for lane := 0; lane < 4; lane++ {
						if rsp.Mask&(1<<lane) != 0 && (rsp.Data[lane] != r.Lanes[lane].Data || rsp.Errors[lane] != nil) {
							t.Fatal("load did not return stored cache/LMEM bytes")
						}
					}
				}
				for _, st := range e.Stores {
					if !r.Write || st.Identity != r.Identity || mask&st.Mask != 0 {
						t.Fatal("store identity/repetition")
					}
					mask |= st.Mask
					for _, err := range st.Errors {
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, id := range e.Complete {
					if id != r.Identity || !accepted || mask != 15 {
						t.Fatal("early completion")
					}
					done = true
				}
				if done {
					break
				}
			}
			if !done {
				t.Fatal("system data timeout")
			}
			if owner.writes != 0 {
				t.Fatal("cached store reached backing before flush")
			}
		}
		// Flush travels through real writeback and visibility acknowledgement, even
		// with both flush consumers backpressured. Fetch may still be outstanding.
		ia, da, idone, ddone := false, false, false, false
		flush := FlushOffer{Valid: true, Identity: Identity{Transaction: 1}, Tag: 55}
		for i := 0; i < 3000; i++ {
			fi, fd := flush, flush
			fi.Valid = !ia
			fd.Valid = !da
			e := step(SystemInput{FetchReady: true, MemoryReady: true, InstructionFlush: fi, DataFlush: fd, InstructionFlushReady: i%4 == 0, DataFlushReady: i%6 == 0})
			ia = ia || e.InstructionFlushAccepted
			da = da || e.DataFlushAccepted
			if e.Fetch.Delivered {
				fetchReturned = true
			}
			if e.InstructionFlush.Delivered {
				if e.InstructionFlush.Err != nil {
					t.Fatal(e.InstructionFlush.Err)
				}
				idone = true
			}
			if e.DataFlush.Delivered {
				if e.DataFlush.Err != nil {
					t.Fatal(e.DataFlush.Err)
				}
				ddone = true
			}
			if idone && ddone && s.Drained() {
				break
			}
		}
		if !fetchReturned || !idone || !ddone || !s.Drained() || owner.writes == 0 || len(s.progress) != 0 || len(s.released) != 0 {
			t.Fatal("system completion/visibility/tail leak")
		}
		for _, lane := range []int{0, 2} {
			b := make([]byte, 4)
			if err := owner.Read(r.Lanes[lane].Address, b); err != nil {
				t.Fatal(err)
			}
			if [4]byte(b) != r.Lanes[lane].Data {
				t.Fatal("flush lost data")
			}
		}
	}
}

func TestSystemProtocolFaultIsTerminal(t *testing.T) {
	_, owner := setup(t, Config{1, 1, 2, 1}, 1, 64)
	s, err := NewSystem(owner, func(Identity) (warp.AtomicMemoryService, error) { return owner, nil }, Config{1, 1, 2, 1})
	if err != nil {
		t.Fatal(err)
	}
	if next, err := s.NextCycle(); err != nil || next != 0 {
		t.Fatal("fresh recovery cycle", next, err)
	}
	if _, err = s.Step(0, SystemInput{}); err != nil {
		t.Fatal(err)
	}
	if next, err := s.NextCycle(); err != nil || next != 1 {
		t.Fatal("committed recovery cycle", next, err)
	}
	_, err = s.Step(2, SystemInput{})
	if err == nil {
		t.Fatal("accepted skipped edge")
	}
	if _, recoveryErr := s.NextCycle(); recoveryErr != err {
		t.Fatal("protocol fault offered unsafe recovery", recoveryErr)
	}
	_, again := s.Step(1, SystemInput{})
	if again != err {
		t.Fatal("retried partially advanced system")
	}
}
