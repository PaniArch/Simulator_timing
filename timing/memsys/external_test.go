package memsys

import (
	"fmt"
	"reflect"
	"testing"
)

type fakeDRAM struct {
	submitted []uint64
	completed []uint64
	ticks     int
}

func TestAsyncDRAMPhysicalBankOrdering(t *testing.T) {
	for _, sameClient := range []bool{false, true} {
		for _, sameBank := range []bool{false, true} {
			name := fmt.Sprintf("sameClient=%t/sameBank=%t", sameClient, sameBank)
			t.Run(name, func(t *testing.T) {
				c := Config{100, 2, 4, 2}
				_, owner := setup(t, c, 2, 256)
				d := &fakeDRAM{}
				b, err := NewAsync(owner, c, 2, 2, d)
				if err != nil {
					t.Fatal(err)
				}
				step := func(cycle uint64, offers []Offer, ready []bool) Edge {
					t.Helper()
					e, err := b.Step(cycle, offers, ready)
					if err != nil {
						t.Fatal(err)
					}
					return e
				}
				step(0, []Offer{offer(1, Read, 0), {}}, []bool{true, true})
				client, addr := 1, uint32(64)
				if sameClient {
					client = 0
				}
				if sameBank {
					addr = 128
				}
				offers := make([]Offer, 2)
				offers[client] = offer(2, Read, addr)
				step(1, offers, []bool{true, true})
				d.completed = []uint64{2}
				step(2, make([]Offer, 2), []bool{true, true})
				p, err := b.PreviewResponses(3)
				if err != nil {
					t.Fatal(err)
				}
				if p[client].Valid == sameBank {
					t.Fatalf("response ordering must depend on bus bank, not client: %+v", p)
				}
				// Stall the younger reply while the older bank completes.
				d.completed = []uint64{1}
				step(3, make([]Offer, 2), []bool{false, false})
				var ids []uint64
				for cycle := uint64(4); cycle < 8; cycle++ {
					e := step(cycle, make([]Offer, 2), []bool{true, true})
					for _, r := range e.Replies {
						if r.Delivered {
							ids = append(ids, r.Response.Sequence)
						}
					}
				}
				if len(ids) != 2 || b.Outstanding() != 0 {
					t.Fatalf("lost/duplicate replies: %v", ids)
				}
				if sameBank && !reflect.DeepEqual(ids, []uint64{1, 2}) {
					t.Fatalf("bank FIFO: %v", ids)
				}
				if sameClient && !sameBank && !reflect.DeepEqual(ids, []uint64{2, 1}) {
					t.Fatalf("held reply changed: %v", ids)
				}
			})
		}
	}
}

func TestAsyncDRAMRejectsInvalidBusBanks(t *testing.T) {
	for _, banks := range []int{0, -1, 3} {
		if _, err := NewAsync(nil, Config{}, 1, banks, &fakeDRAM{}); err == nil {
			t.Fatalf("accepted %d banks", banks)
		}
	}
}

func (d *fakeDRAM) Submit(id uint64, _ uint32, _ bool) error {
	d.submitted = append(d.submitted, id)
	return nil
}
func (d *fakeDRAM) Tick() ([]uint64, error) {
	d.ticks++
	r := d.completed
	d.completed = nil
	return r, nil
}

func TestAsyncDRAMAcceptanceSnapshotBackpressureAndVisibility(t *testing.T) {
	c := Config{100, 2, 2, 2}
	_, owner := setup(t, c, 2, 128)
	driver := &fakeDRAM{}
	b, err := NewAsync(owner, c, 2, 2, driver)
	if err != nil {
		t.Fatal(err)
	}
	step := func(cycle uint64, o []Offer, r []bool) Edge {
		t.Helper()
		e, err := b.Step(cycle, o, r)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	w := offer(1, Write, 0)
	w.Request.ByteEnable = 1
	w.Request.Data[0] = 42
	if err := owner.Memory.Write(64, []byte{42}); err != nil {
		t.Fatal(err)
	}
	e := step(0, []Offer{w, offer(1, Read, 64)}, []bool{true, true})
	if !e.Accepted[0] || !e.Accepted[1] || owner.writes != 1 || owner.reads != 1 {
		t.Fatalf("accept %+v", e)
	}
	// Changing backing afterwards must not change the accepted read's snapshot.
	if err := owner.Memory.Write(64, []byte{99}); err != nil {
		t.Fatal(err)
	}
	vis := offer(2, Visibility, 0)
	driver.completed = []uint64{2} // out of order across clients
	e = step(1, []Offer{vis, {}}, []bool{true, true})
	if e.Accepted[0] || e.Replies[1].Valid {
		t.Fatal("admission/callback exposed too early")
	}
	p1, _ := b.PreviewResponses(2)
	p2, _ := b.PreviewResponses(2)
	if !reflect.DeepEqual(p1, p2) || driver.ticks != 2 {
		t.Fatal("preview advanced DRAM")
	}
	e = step(2, []Offer{vis, {}}, []bool{true, false})
	if !e.Replies[1].Valid || e.Replies[1].Delivered || e.Replies[1].Response.Data[0] != 42 {
		t.Fatalf("held response %+v", e)
	}
	driver.completed = []uint64{1}
	e = step(3, []Offer{vis, {}}, []bool{true, true})
	if !e.Replies[1].Delivered || e.Accepted[0] {
		t.Fatal("old occupancy violated")
	}
	e = step(4, []Offer{vis, {}}, []bool{true, true})
	if !e.Replies[0].Delivered || !e.Accepted[0] {
		t.Fatal("write ack/visibility not accepted")
	}
	e = step(5, []Offer{{}, {}}, []bool{true, true})
	if !e.Replies[0].Delivered || e.Replies[0].Response.Operation != Visibility || b.Outstanding() != 0 {
		t.Fatalf("visibility %+v", e)
	}
	if len(driver.submitted) != 2 || owner.reads != 1 || owner.writes != 1 {
		t.Fatal("resubmitted or reserviced")
	}
}

func TestAsyncDRAMRejectsChangedHeldRequestAndDuplicateCompletion(t *testing.T) {
	c := Config{100, 1, 1, 1}
	_, owner := setup(t, c, 1, 128)
	d := &fakeDRAM{}
	b, _ := NewAsync(owner, c, 1, 2, d)
	if _, err := b.Step(0, []Offer{offer(1, Read, 0)}, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Step(1, []Offer{offer(2, Read, 0)}, []bool{true}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Step(2, []Offer{offer(2, Read, 64)}, []bool{true}); err == nil {
		t.Fatal("changed held request accepted")
	}
	if d.ticks != 2 {
		t.Fatal("invalid request mutated driver")
	}
	d.completed = []uint64{1, 1}
	if _, err := b.Step(2, []Offer{offer(2, Read, 0)}, []bool{true}); err == nil {
		t.Fatal("duplicate callback accepted")
	}
}

func TestAsyncDRAMHeldResponseSurvivesOtherCompletion(t *testing.T) {
	c := Config{100, 2, 4, 1}
	_, owner := setup(t, c, 2, 128)
	d := &fakeDRAM{}
	b, _ := NewAsync(owner, c, 2, 2, d)
	step := func(cycle uint64, ready []bool) Edge {
		t.Helper()
		e, err := b.Step(cycle, []Offer{{}, {}}, ready)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	if _, err := b.Step(0, []Offer{offer(1, Read, 0), offer(1, Read, 64)}, []bool{true, true}); err != nil {
		t.Fatal(err)
	}
	d.completed = []uint64{2}
	step(1, []bool{true, false})
	d.completed = []uint64{1}
	e := step(2, []bool{true, false})
	if !e.Replies[1].Valid || e.Replies[1].Delivered {
		t.Fatal("missing held response")
	}
	p, _ := b.PreviewResponses(3)
	if !p[1].Valid || p[0].Valid {
		t.Fatal("older completion preempted stalled valid")
	}
	e = step(3, []bool{true, true})
	if !e.Replies[1].Delivered {
		t.Fatal("held response lost")
	}
	e = step(4, []bool{true, true})
	if !e.Replies[0].Delivered || b.Outstanding() != 0 {
		t.Fatal("other completion lost")
	}
}
