package memsys

import (
	"errors"
	"testing"

	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/support/memory"
)

type localHarness struct {
	t          *testing.T
	m          *LocalMemory
	cycle      uint64
	routes     [2]*core.CTAMemory
	addresses  [2]uint32
	generation uint64
}

func newLocalHarness(t *testing.T) *localHarness {
	t.Helper()
	h := &localHarness{t: t, generation: 1}
	ctas := core.NewCTAManager()
	global, _ := memory.New(64)
	for id := 0; id < 2; id++ {
		view, err := ctas.Admit(core.CTAConfig{ID: uint32(id), WarpIDs: []uint8{uint8(id)}, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{4, 1, 1}, Entry: 0x100, LocalMemorySize: 128, ClusterSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		h.addresses[id] = view.LocalMemory.Address
		h.routes[id], err = core.NewCTAMemory(ctas, uint8(id), global)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	h.m, err = NewLocalMemory(func(id Identity) (warp.AtomicMemoryService, error) {
		if id.Kernel != 7 || id.CTA > 1 || id.WarpGeneration != h.generation || id.Warp != uint32(id.CTA) {
			return nil, errors.New("stale local residency")
		}
		return h.routes[id.CTA], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func (h *localHarness) request(id uint64, cta int, address uint32, write bool) WordRequest {
	return WordRequest{Identity: Identity{Kernel: 7, CTA: uint64(cta), Warp: uint32(cta), WarpGeneration: 1, Transaction: id}, Tag: id + 100, Address: address, Write: write, ByteEnable: 15}
}
func (h *localHarness) step(offers []WordOffer, ready bool) CacheEdge {
	h.t.Helper()
	if offers == nil {
		offers = make([]WordOffer, 4)
	}
	e, err := h.m.Step(h.cycle, offers, []bool{ready, ready, ready, ready})
	if err != nil {
		h.t.Fatal(err)
	}
	h.cycle++
	return e
}
func TestLocalMemoryBankConflictAndReadLatency(t *testing.T) {
	h := newLocalHarness(t)
	offers := make([]WordOffer, 4)
	for p := 0; p < 4; p++ {
		offers[p] = WordOffer{true, h.request(1, 0, h.addresses[0]+uint32(p*16), false)}
	}
	accepted := [4]int{-1, -1, -1, -1}
	returned := 0
	for cycle := 0; cycle < 15; cycle++ {
		e := h.step(offers, true)
		for p := 0; p < 4; p++ {
			if e.Accepted[p] {
				accepted[p] = cycle
				offers[p] = WordOffer{}
			}
			if e.Replies[p].Delivered {
				if cycle != accepted[p]+3 {
					t.Fatalf("port %d return=%d accept=%d", p, cycle, accepted[p])
				}
				returned++
			}
		}
	}
	if accepted != [4]int{0, 1, 2, 3} || returned != 4 || !h.m.Drained() {
		t.Fatalf("priority bank service: %v replies=%d", accepted, returned)
	}
	// Distinct word-bank bits allow all four ports on the same edge.
	for p := 0; p < 4; p++ {
		offers[p] = WordOffer{true, h.request(2, 0, h.addresses[0]+uint32(p*4), false)}
	}
	e := h.step(offers, true)
	for _, a := range e.Accepted {
		if !a {
			t.Fatal("independent bank stalled")
		}
	}
}
func TestLocalMemoryByteWritesRDWHazardAndCTAScope(t *testing.T) {
	h := newLocalHarness(t)
	base := h.addresses[0]
	store := h.request(1, 0, base, true)
	store.ByteEnable = 5
	store.Data = [8]byte{10, 99, 12, 99}
	offers := make([]WordOffer, 4)
	offers[0] = WordOffer{true, store}
	h.step(offers, true) // C0 queues store.
	load := h.request(2, 0, base, false)
	offers[0] = WordOffer{true, load}
	e := h.step(offers, true) // C1 applies store and queues load.
	if len(e.Stores) != 1 || e.Stores[0].Err != nil {
		t.Fatal("store application missing")
	}
	var got WordResponse
	returned := -1
	for cycle := 2; cycle < 9; cycle++ {
		e = h.step(nil, true)
		if e.Replies[0].Delivered {
			returned = cycle
			got = e.Replies[0].Response
		}
	}
	if returned != 5 || got.Data != [8]byte{10, 0, 12} {
		t.Fatalf("RDW bubble/data: cycle=%d bytes=%v", returned, got.Data)
	}
	var bytes [4]byte
	if err := h.routes[0].Read(base, bytes[:]); err != nil || bytes != [4]byte{10, 0, 12} {
		t.Fatal("original CTA owner was not updated", err)
	}
	if err := h.routes[1].Read(h.addresses[1], bytes[:]); err != nil || bytes != [4]byte{} {
		t.Fatal("other CTA aliased", err)
	}
}
func TestLocalMemoryHeldResponseAndStaleOwner(t *testing.T) {
	h := newLocalHarness(t)
	base := h.addresses[0]
	// Fill output buffer and SRAM tag register under downstream backpressure.
	pending := 1
	offers := make([]WordOffer, 4)
	for cycle := 0; cycle < 12; cycle++ {
		if pending <= 6 {
			offers[0] = WordOffer{true, h.request(uint64(pending), 0, base, false)}
		} else {
			offers[0] = WordOffer{}
		}
		e := h.step(offers, false)
		if e.Accepted[0] {
			pending++
		}
	}
	if pending != 6 {
		t.Fatalf("expected 2 request + 1 SRAM + 2 response slots: accepted=%d", pending-1)
	}
	snapshot := h.m.Responses()[0].Response
	h.generation = 2 // queued old requests must fault, never resolve to a new owner.
	got, failed := 0, 0
	for cycle := 0; cycle < 25; cycle++ {
		if pending <= 6 {
			offers[0] = WordOffer{true, h.request(uint64(pending), 0, base, false)}
		} else {
			offers[0] = WordOffer{}
		}
		e := h.step(offers, true)
		if e.Accepted[0] {
			pending++
		}
		if e.Replies[0].Delivered {
			got++
			r := e.Replies[0].Response
			if got == 1 && r != snapshot {
				t.Fatal("held data changed")
			}
			if r.Err != nil {
				failed++
			}
		}
	}
	if got != 6 || failed != 3 || !h.m.Drained() || h.m.HasResidency(7, 0) {
		t.Fatalf("retirement/fault counts %d %d", got, failed)
	}
}
