package memsys

import (
	"errors"
	"reflect"
	"testing"

	"vortex.local/simulator/support/memory"
)

type countedOwner struct {
	*memory.Memory
	reads, writes int
	writeErr      error
}

func (m *countedOwner) Read(a uint32, d []byte) error { m.reads++; return m.Memory.Read(a, d) }
func (m *countedOwner) WriteBatch(a []uint32, d [][]byte) error {
	m.writes++
	if m.writeErr != nil {
		return m.writeErr
	}
	return m.Memory.WriteBatch(a, d)
}
func setup(t *testing.T, c Config, clients int, size uint64) (*Backend, *countedOwner) {
	t.Helper()
	m, err := memory.New(size)
	if err != nil {
		t.Fatal(err)
	}
	owner := &countedOwner{Memory: m}
	b, err := New(owner, c, clients)
	if err != nil {
		t.Fatal(err)
	}
	return b, owner
}
func offer(id uint64, op Operation, address uint32) Offer {
	return Offer{true, Request{Identity: Identity{Kernel: 3, CTA: 7, WarpGeneration: 11, Token: 17, Epoch: 19, Subrequest: 23, Transaction: id, Warp: 2}, Tag: 0xa5, Operation: op, Address: address, ByteEnable: ^uint64(0)}}
}
func edge(t *testing.T, b *Backend, c uint64, o []Offer, r []bool) Edge {
	t.Helper()
	e, err := b.Step(c, o, r)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestLatencyFromAcceptanceAndRoundRobin(t *testing.T) {
	for _, latency := range []uint64{1, 3, 100} {
		t.Run(stringName(latency), func(t *testing.T) {
			b, m := setup(t, Config{latency, 1, 8, 1}, 2, 128)
			if err := m.Memory.Write(0, []byte{0x12, 0x34}); err != nil {
				t.Fatal(err)
			}
			offers := []Offer{offer(1, Read, 0), offer(1, Read, 64)}
			first := edge(t, b, 10, offers, []bool{true, true})
			if !reflect.DeepEqual(first.Accepted, []bool{true, false}) {
				t.Fatalf("first arbitration: %+v", first)
			}
			// Port zero remains busy, but round-robin must grant the waiting D port.
			offers[0] = offer(2, Read, 0)
			second := edge(t, b, 11, offers, []bool{true, true})
			if !reflect.DeepEqual(second.Accepted, []bool{false, true}) {
				t.Fatalf("starved waiting port: %+v", second)
			}
			for cycle := uint64(12); cycle <= 12+latency; cycle++ {
				offers[1] = Offer{}
				e := edge(t, b, cycle, offers, []bool{true, true})
				if e.Accepted[0] {
					offers[0] = Offer{}
				}
				for _, reply := range e.Replies {
					if !reply.Valid {
						continue
					}
					if !reply.Delivered || reply.Response.CompletedCycle-reply.Response.AcceptedCycle != latency {
						t.Fatalf("wrong latency %+v", reply)
					}
					if reply.Response.Identity != offer(reply.Response.Identity.Transaction, Read, 0).Request.Identity || reply.Response.Tag != 0xa5 {
						t.Fatal("identity lost")
					}
				}
				if cycle == 11+latency && (!e.Replies[1].Valid || e.Replies[1].Response.AcceptedCycle != 11) {
					t.Fatalf("D latency counted before grant: %+v", e)
				}
			}
			if m.reads != 3 || b.Outstanding() != 0 {
				t.Fatalf("service count %d outstanding %d", m.reads, b.Outstanding())
			}
		})
	}
}
func stringName(n uint64) string {
	if n == 1 {
		return "one"
	}
	if n == 3 {
		return "three"
	}
	return "hundred"
}

func TestFullRecoveryStableResponseAndNoReservice(t *testing.T) {
	b, m := setup(t, Config{2, 1, 1, 1}, 2, 128)
	initial := offer(1, Read, 0)
	waiting := offer(1, Read, 64)
	edge(t, b, 0, []Offer{initial, {}}, []bool{false, false})
	e := edge(t, b, 1, []Offer{{}, waiting}, []bool{false, false})
	if e.Accepted[1] || m.reads != 0 {
		t.Fatal("full capacity or early service")
	}
	if err := m.Memory.Write(0, []byte{42}); err != nil {
		t.Fatal(err)
	}
	e = edge(t, b, 2, []Offer{{}, waiting}, []bool{false, false})
	saved := e.Replies[0].Response
	if !e.Replies[0].Valid || e.Replies[0].Delivered || saved.Data[0] != 42 || saved.CompletedCycle != 2 {
		t.Fatalf("bad due response %+v", e)
	}
	if err := m.Memory.Write(0, []byte{99}); err != nil {
		t.Fatal(err)
	}
	for c := uint64(3); c <= 5; c++ {
		e = edge(t, b, c, []Offer{{}, waiting}, []bool{false, false})
		if e.Replies[0].Response != saved || e.Accepted[1] || m.reads != 1 {
			t.Fatal("held response reread or changed")
		}
	}
	e = edge(t, b, 6, []Offer{{}, waiting}, []bool{true, false})
	if !e.Replies[0].Delivered || e.Accepted[1] {
		t.Fatal("retirement borrowed old full slot")
	}
	e = edge(t, b, 7, []Offer{{}, waiting}, []bool{true, true})
	if !e.Accepted[1] {
		t.Fatal("did not recover after retirement")
	}
	edge(t, b, 8, []Offer{{}, {}}, []bool{true, true})
	e = edge(t, b, 9, []Offer{{}, {}}, []bool{true, true})
	if !e.Replies[1].Delivered || e.Replies[1].Response.AcceptedCycle != 7 || m.reads != 2 {
		t.Fatal("recovery response wrong")
	}
}

func TestByteWritesReadVisibilityAndReturnBandwidth(t *testing.T) {
	b, m := setup(t, Config{2, 3, 8, 1}, 3, 128)
	initial := make([]byte, 64)
	for i := range initial {
		initial[i] = 9
	}
	if err := m.Memory.Write(0, initial); err != nil {
		t.Fatal(err)
	}
	w := offer(1, Write, 0)
	w.Request.ByteEnable = 1 | 1<<2 | uint64(1)<<63
	w.Request.Data[0], w.Request.Data[2], w.Request.Data[63] = 1, 2, 3
	requests := []Offer{w, offer(1, Read, 0), offer(1, Visibility, 0)}
	e := edge(t, b, 0, requests, []bool{false, false, false})
	if !reflect.DeepEqual(e.Accepted, []bool{true, true, true}) {
		t.Fatal("accept bandwidth")
	}
	// Caller mutation after acceptance cannot change saved write data.
	requests[0].Request.Data[0] = 88
	edge(t, b, 1, []Offer{{}, {}, {}}, []bool{false, false, false})
	if m.Snapshot()[0] != 9 || m.writes != 0 {
		t.Fatal("store visible before due")
	}
	e = edge(t, b, 2, []Offer{{}, {}, {}}, []bool{false, false, false})
	if m.writes != 1 || m.reads != 1 || m.Snapshot()[0] != 1 || m.Snapshot()[1] != 9 || m.Snapshot()[2] != 2 || m.Snapshot()[63] != 3 {
		t.Fatal("byte enable or service order wrong")
	}
	if !e.Replies[0].Valid || e.Replies[1].Valid {
		t.Fatal("return bandwidth/HOL violated")
	}
	for c := uint64(3); c <= 5; c++ {
		e = edge(t, b, c, []Offer{{}, {}, {}}, []bool{true, true, true})
		index := int(c - 3)
		if !e.Replies[index].Delivered {
			t.Fatalf("response order %+v", e)
		}
		if e.Replies[index].Response.Sequence != uint64(index+1) || e.Replies[index].Response.CompletedCycle != 2 {
			t.Fatal("acceptance/service sequence wrong")
		}
		if index == 1 && (e.Replies[index].Response.Data[0] != 1 || e.Replies[index].Response.Data[1] != 9) {
			t.Fatal("read bypassed prior write")
		}
	}
	if m.writes != 1 || m.reads != 1 || b.Outstanding() != 0 {
		t.Fatal("duplicate service")
	}
}

func TestFaultsAreReturnedOnceAndVisibilityFails(t *testing.T) {
	b, m := setup(t, Config{1, 3, 4, 3}, 3, 66)
	w := offer(1, Write, 64)
	w.Request.ByteEnable = 1 | 1<<3
	w.Request.Data[0] = 42
	requests := []Offer{w, offer(1, Read, 64), offer(1, Visibility, 0)}
	edge(t, b, 0, requests, []bool{true, true, true})
	e := edge(t, b, 1, []Offer{{}, {}, {}}, []bool{false, true, true})
	if !errors.Is(e.Replies[0].Response.Err, memory.ErrOutOfBounds) || m.Snapshot()[64] != 0 {
		t.Fatal("partial failed write")
	}
	for c := uint64(2); c < 4; c++ {
		edge(t, b, c, []Offer{{}, {}, {}}, []bool{false, true, true})
	}
	e = edge(t, b, 4, []Offer{{}, {}, {}}, []bool{true, true, true})
	for _, r := range e.Replies {
		if !r.Delivered || !errors.Is(r.Response.Err, memory.ErrOutOfBounds) {
			t.Fatalf("fault missing %+v", r)
		}
	}
	if e.Replies[1].Response.Data != [64]byte{} || m.writes != 1 || m.reads != 1 {
		t.Fatal("fault reread or data leaked")
	}
}

func TestRejectedProtocolEdgeHasNoEffects(t *testing.T) {
	b, m := setup(t, Config{1, 1, 1, 1}, 2, 128)
	waiting := offer(1, Write, 64)
	edge(t, b, 0, []Offer{offer(1, Write, 0), waiting}, []bool{true, true})
	changed := waiting
	changed.Request.Data[0] = 42
	if _, err := b.Step(1, []Offer{{}, changed}, []bool{true, true}); err == nil {
		t.Fatal("changed stalled request accepted")
	}
	if m.writes != 0 || b.Outstanding() != 1 {
		t.Fatal("bad edge mutated owner")
	}
	e := edge(t, b, 1, []Offer{{}, waiting}, []bool{true, true})
	if !e.Replies[0].Delivered || m.writes != 1 {
		t.Fatal("valid retry failed")
	}
	if _, err := b.Step(2, []Offer{offer(1, Write, 0), waiting}, []bool{true, true}); err == nil {
		t.Fatal("duplicate accepted transaction allowed")
	}
	edge(t, b, 2, []Offer{{}, waiting}, []bool{true, true})
	if _, err := b.Step(4, []Offer{{}, {}}, []bool{true, true}); err == nil {
		t.Fatal("cycle skip allowed")
	}
	edge(t, b, 3, []Offer{{}, {}}, []bool{true, true})
	if m.writes != 2 {
		t.Fatal("duplicate side effects")
	}
}

func TestDefaultsAndInvalidInputs(t *testing.T) {
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.Latency != 100 || c.AcceptsPerCycle != 1 || c.MaxInflight != 16 || c.ReturnsPerCycle != 1 {
		t.Fatalf("unexpected IR defaults %+v", c)
	}
	m, _ := memory.New(128)
	for _, bad := range []Config{{}, {1, 0, 1, 1}, {1, 1, 0, 1}, {1, 1, 1, 0}} {
		if _, err := New(m, bad, 2); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	var nilOwner *memory.Memory
	if _, err := New(nilOwner, c, 2); err == nil {
		t.Fatal("typed nil owner accepted")
	}
	b, _ := New(m, c, 1)
	o := offer(1, Read, 1)
	if _, err := b.Step(0, []Offer{o}, []bool{true}); err == nil {
		t.Fatal("unaligned request accepted")
	}
	if _, err := b.Step(^uint64(0), []Offer{offer(1, Read, 0)}, []bool{true}); err == nil {
		t.Fatal("due overflow")
	}
}

func TestBackendPreviewDoesNotServiceOrAdvance(t *testing.T) {
	b, m := setup(t, Config{2, 1, 4, 1}, 1, 64)
	edge(t, b, 0, []Offer{offer(1, Read, 0)}, []bool{false})
	early, err := b.PreviewResponses(1)
	if err != nil || early[0].Valid {
		t.Fatal("early preview")
	}
	edge(t, b, 1, []Offer{{}}, []bool{false})
	for i := 0; i < 3; i++ {
		p, err := b.PreviewResponses(2)
		if err != nil || !p[0].Valid || p[0].Response.Identity.Transaction != 1 || m.reads != 0 {
			t.Fatal("preview serviced bytes or changed identity")
		}
	}
	e := edge(t, b, 2, []Offer{{}}, []bool{true})
	if !e.Replies[0].Delivered || m.reads != 1 {
		t.Fatal("preview altered actual service")
	}
}
