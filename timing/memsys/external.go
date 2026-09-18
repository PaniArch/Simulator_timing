package memsys

import (
	"fmt"
	"math"

	"vortex.local/simulator/emu/warp"
)

// ExternalBackend is below the caches, not an ISA memory service.
type ExternalBackend interface {
	Outstanding() int
	PreviewResponses(uint64) ([]Reply, error)
	Step(uint64, []Offer, []bool) (Edge, error)
}

type BackendFactory func(warp.AtomicMemoryService, Config, int) (ExternalBackend, error)

// DRAMTiming drives an external timing library. Submit takes byte addresses.
// Completion of a write means the library's write acknowledgment, NOT physical
// DRAM drain. Tick returns IDs completed by that edge; Submit cannot callback.
type DRAMTiming interface {
	Submit(id uint64, address uint32, write bool) error
	Tick() ([]uint64, error)
}

// AsyncBackend retains an explicit bounded adapter queue. Data is sampled or
// applied at external acceptance, matching the RTLSim memory harness. Timing
// completion controls return to Cache, never a second backing-memory read.
// This is a bridge policy, not a model of the RTL socket interconnect.
type AsyncBackend struct {
	*Backend
	driver     DRAMTiming
	banks      int
	returnHeld []Reply
}

func NewAsync(owner warp.AtomicMemoryService, config Config, clients, banks int, driver DRAMTiming) (*AsyncBackend, error) {
	if banks < 1 || banks&(banks-1) != 0 {
		return nil, fmt.Errorf("DRAM bus banks must be a positive power of two")
	}
	if driver == nil {
		return nil, fmt.Errorf("nil DRAM timing driver")
	}
	b, err := New(owner, config, clients)
	if err != nil {
		return nil, err
	}
	return &AsyncBackend{Backend: b, driver: driver, banks: banks, returnHeld: make([]Reply, clients)}, nil
}

func (b *AsyncBackend) PreviewResponses(cycle uint64) ([]Reply, error) {
	if b.started && (b.cycle == math.MaxUint64 || cycle != b.cycle+1) {
		return nil, fmt.Errorf("noncontiguous DRAM cycle")
	}
	r := append([]Reply(nil), b.returnHeld...)
	count := 0
	for _, p := range r {
		if p.Valid {
			count++
		}
	}
	// RTLSim queues by interleaved physical bus bank, shared by I/D clients.
	// Different banks may complete out of order even for the same Cache port.
	seen := make([]bool, b.banks)
	for _, e := range b.queue {
		if e.request.Operation != Visibility {
			bank := int(e.request.Address/SectorBytes) % b.banks
			if seen[bank] {
				continue
			}
			seen[bank] = true
		}
		if e.serviced && !r[e.client].Valid && count < b.config.ReturnsPerCycle {
			r[e.client] = Reply{Valid: true, Response: e.response}
			count++
		}
	}
	return r, nil
}

func (b *AsyncBackend) Step(cycle uint64, offers []Offer, ready []bool) (Edge, error) {
	preview, err := b.PreviewResponses(cycle)
	if err != nil {
		return Edge{}, err
	}
	if len(offers) != len(b.held) || len(ready) != len(b.held) {
		return Edge{}, fmt.Errorf("wrong DRAM port count")
	}
	if cycle == math.MaxUint64 || b.sequence > math.MaxUint64-uint64(len(offers)) {
		return Edge{}, fmt.Errorf("DRAM counter overflow")
	}
	for i, o := range offers {
		if b.held[i].Valid && o != b.held[i] {
			return Edge{}, fmt.Errorf("client %d changed stalled DRAM request", i)
		}
		if !o.Valid {
			continue
		}
		r := o.Request
		if r.Operation > Visibility || (r.Operation != Visibility && r.Address%SectorBytes != 0) {
			return Edge{}, fmt.Errorf("invalid DRAM request")
		}
		if b.used[i] && r.Identity.Transaction <= b.last[i] {
			return Edge{}, fmt.Errorf("stale DRAM transaction")
		}
	}
	out := Edge{Accepted: make([]bool, len(offers)), Replies: make([]Reply, len(offers))}
	slots := b.config.MaxInflight - len(b.queue)
	retired := make(map[uint64]bool)
	count := 0
	for _, e := range b.queue {
		p := preview[e.client]
		if !p.Valid || p.Response.Sequence != e.response.Sequence || count >= b.config.ReturnsPerCycle {
			continue
		}
		out.Replies[e.client] = p
		out.Replies[e.client].Delivered = ready[e.client]
		if ready[e.client] {
			retired[e.response.Sequence] = true
			count++
		}
	}
	kept := b.queue[:0]
	for _, e := range b.queue {
		if !retired[e.response.Sequence] {
			kept = append(kept, e)
		}
	}
	b.queue = kept
	// RTLSim ticks DramSim before forwarding newly accepted bus requests.
	done, err := b.driver.Tick()
	if err != nil {
		return Edge{}, err
	}
	for _, id := range done {
		found := false
		for i := range b.queue {
			e := &b.queue[i]
			if e.response.Sequence != id {
				continue
			}
			if e.serviced || e.request.Operation == Visibility {
				return Edge{}, fmt.Errorf("duplicate DRAM completion %d", id)
			}
			e.serviced = true
			e.response.CompletedCycle = cycle + 1
			found = true
			break
		}
		if !found {
			return Edge{}, fmt.Errorf("unknown DRAM completion %d", id)
		}
	}
	accepted, next := 0, b.nextClient
	for offset := 0; offset < len(offers); offset++ {
		client := (b.nextClient + offset) % len(offers)
		o := offers[client]
		if !o.Valid || accepted >= slots || accepted >= b.config.AcceptsPerCycle {
			continue
		}
		b.sequence++
		r := o.Request
		e := entry{client: client, request: r, response: Response{Identity: r.Identity, Tag: r.Tag, Operation: r.Operation, ByteEnable: r.ByteEnable, AcceptedCycle: cycle, Sequence: b.sequence}}
		if r.Operation != Visibility {
			if err := b.driver.Submit(b.sequence, r.Address, r.Operation == Write); err != nil {
				return Edge{}, err
			}
		}
		switch r.Operation {
		case Read:
			e.response.ByteEnable = ^uint64(0)
			e.response.Err = b.owner.Read(r.Address, e.response.Data[:])
			if e.response.Err != nil {
				e.response.Data = [SectorBytes]byte{}
			}
		case Write:
			var addresses []uint32
			var data [][]byte
			for j := 0; j < SectorBytes; {
				if r.ByteEnable&(uint64(1)<<j) == 0 {
					j++
					continue
				}
				start := j
				for j < SectorBytes && r.ByteEnable&(uint64(1)<<j) != 0 {
					j++
				}
				addresses = append(addresses, r.Address+uint32(start))
				data = append(data, r.Data[start:j])
			}
			if len(addresses) > 0 {
				e.response.Err = b.owner.WriteBatch(addresses, data)
			}
			if e.response.Err != nil && b.writeError == nil {
				b.writeError = e.response.Err
			}
		case Visibility:
			e.response.ByteEnable = 0
			e.response.Err = b.writeError
		}
		b.queue = append(b.queue, e)
		out.Accepted[client] = true
		b.last[client], b.used[client] = r.Identity.Transaction, true
		accepted++
		next = (client + 1) % len(offers)
	}
	// A visibility ticket waits for all earlier library acknowledgments. This
	// is stronger than backing visibility but not a physical DRAM drain.
	olderDone := true
	for i := range b.queue {
		e := &b.queue[i]
		if e.request.Operation == Visibility && !e.serviced && olderDone {
			e.serviced = true
			e.response.CompletedCycle = cycle + 1
		}
		olderDone = olderDone && e.serviced
	}
	b.nextClient = next
	for i, r := range out.Replies {
		b.returnHeld[i] = Reply{}
		if r.Valid && !r.Delivered {
			b.returnHeld[i] = r
		}
	}
	for i, o := range offers {
		b.held[i] = Offer{}
		if o.Valid && !out.Accepted[i] {
			b.held[i] = o
		}
	}
	b.cycle, b.started = cycle, true
	return out, nil
}
