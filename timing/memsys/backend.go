// Package memsys implements cycle-stepped storage components. The external
// backend is a deterministic software boundary, not a DRAM timing model.
package memsys

import (
	"fmt"
	"math"
	"reflect"

	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing"
)

const SectorBytes = 64

type Operation uint8

const (
	Read Operation = iota
	Write
	Visibility
)

// Identity survives slot reuse. Transaction must increase per client, including
// across residency changes. Cache-generated writebacks may use zero residency.
type Identity struct {
	Kernel, CTA, WarpGeneration, Token, Epoch, Subrequest, Transaction uint64
	Warp                                                               uint32
}

type Request struct {
	Identity   Identity
	Tag        uint64
	Operation  Operation
	Address    uint32 // byte address, aligned to SectorBytes
	ByteEnable uint64
	Data       [SectorBytes]byte
}
type Offer struct {
	Valid   bool
	Request Request
}
type Response struct {
	Identity                                Identity
	Tag                                     uint64
	Operation                               Operation
	Data                                    [SectorBytes]byte
	ByteEnable                              uint64
	AcceptedCycle, CompletedCycle, Sequence uint64
	Err                                     error
}
type Reply struct {
	Valid, Delivered bool
	Response         Response
}
type Edge struct {
	Accepted []bool
	Replies  []Reply
}

type Config struct {
	Latency                                       uint64
	AcceptsPerCycle, MaxInflight, ReturnsPerCycle int
}

func DefaultConfig() (Config, error) {
	values := make([]int, 4)
	for i, k := range []string{"latency_cycles", "accepts_per_cycle", "max_inflight", "returns_per_cycle"} {
		v, err := timing.Number("memory_contracts", "mc-backend", "parameters", k)
		if err != nil {
			return Config{}, err
		}
		values[i] = v
	}
	line, err := timing.Number("config", "cfg-memory", "values", "line_and_sector_bytes")
	if err != nil {
		return Config{}, err
	}
	if line != SectorBytes {
		return Config{}, fmt.Errorf("unsupported external sector size %d", line)
	}
	return Config{uint64(values[0]), values[1], values[2], values[3]}, nil
}

type entry struct {
	client   int
	request  Request
	due      uint64
	response Response
	serviced bool
}
type Backend struct {
	owner           warp.AtomicMemoryService
	config          Config
	queue           []entry
	held            []Offer
	last            []uint64
	used            []bool
	nextClient      int
	sequence, cycle uint64
	started         bool
	writeError      error
}

// New binds the original backing owner. Atomic byte-run writes avoid partially
// applying a sector if one enabled range faults. It allocates no backing memory.
func New(owner warp.AtomicMemoryService, config Config, clients int) (*Backend, error) {
	if owner == nil || (reflect.ValueOf(owner).Kind() == reflect.Pointer && reflect.ValueOf(owner).IsNil()) {
		return nil, fmt.Errorf("nil backing owner")
	}
	if config.Latency == 0 || config.AcceptsPerCycle <= 0 || config.MaxInflight <= 0 || config.ReturnsPerCycle <= 0 || clients <= 0 {
		return nil, fmt.Errorf("invalid backend configuration")
	}
	return &Backend{owner: owner, config: config, held: make([]Offer, clients), last: make([]uint64, clients), used: make([]bool, clients)}, nil
}
func (b *Backend) Outstanding() int { return len(b.queue) }

// Step performs one edge, beginning at any cycle, then requiring contiguous
// cycles. Offers and ready are indexed by stable client port (e.g. I=0,D=1).
// Old occupancy governs admission: response retirement frees slots next edge.
// Round-robin chooses at most one request per client per edge. Response order
// is global acceptance order, with head-of-line blocking and at most one reply
// per client per edge. All due requests service once, independently of ready.
// Protocol errors return before any state or backing mutation. Memory errors
// are delivered in the corresponding response, never retried automatically.
func (b *Backend) Step(cycle uint64, offers []Offer, ready []bool) (Edge, error) {
	if len(offers) != len(b.held) || len(ready) != len(b.held) {
		return Edge{}, fmt.Errorf("wrong client port count")
	}
	if b.started && (b.cycle == math.MaxUint64 || cycle != b.cycle+1) {
		return Edge{}, fmt.Errorf("noncontiguous backend cycle")
	}
	for i, o := range offers {
		if b.held[i].Valid && o != b.held[i] {
			return Edge{}, fmt.Errorf("client %d changed stalled request", i)
		}
		if !o.Valid {
			continue
		}
		r := o.Request
		if r.Operation > Visibility || (r.Operation != Visibility && r.Address%SectorBytes != 0) {
			return Edge{}, fmt.Errorf("invalid request on client %d", i)
		}
		if b.used[i] && r.Identity.Transaction <= b.last[i] {
			return Edge{}, fmt.Errorf("repeated or stale transaction on client %d", i)
		}
		if cycle > math.MaxUint64-b.config.Latency {
			return Edge{}, fmt.Errorf("backend due cycle overflow")
		}
	}
	if b.sequence > math.MaxUint64-uint64(len(offers)) {
		return Edge{}, fmt.Errorf("backend sequence overflow")
	}
	result := Edge{Accepted: make([]bool, len(offers)), Replies: make([]Reply, len(offers))}
	slots := b.config.MaxInflight - len(b.queue)
	// Service in acceptance order, so same-due writes precede later reads/tickets.
	for i := range b.queue {
		e := &b.queue[i]
		if e.serviced || e.due > cycle {
			continue
		}
		r := e.request
		switch r.Operation {
		case Read:
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
			if len(addresses) != 0 {
				e.response.Err = b.owner.WriteBatch(addresses, data)
			}
			if e.response.Err != nil && b.writeError == nil {
				b.writeError = e.response.Err
			}
		case Visibility:
			e.response.Err = b.writeError
		}
		e.serviced = true
		e.response.CompletedCycle = cycle
	}
	retired := 0
	for i := 0; i < len(b.queue) && i < b.config.ReturnsPerCycle; i++ {
		e := b.queue[i]
		if !e.serviced || result.Replies[e.client].Valid {
			break
		}
		result.Replies[e.client] = Reply{true, ready[e.client], e.response}
		if !ready[e.client] {
			break
		}
		retired++
	}
	b.queue = b.queue[retired:]
	accepted := 0
	next := b.nextClient
	for offset := 0; offset < len(offers); offset++ {
		client := (b.nextClient + offset) % len(offers)
		o := offers[client]
		if !o.Valid || accepted >= b.config.AcceptsPerCycle || accepted >= slots {
			continue
		}
		b.sequence++
		r := o.Request
		mask := r.ByteEnable
		if r.Operation == Read {
			mask = ^uint64(0)
		}
		if r.Operation == Visibility {
			mask = 0
		}
		b.queue = append(b.queue, entry{client: client, request: r, due: cycle + b.config.Latency, response: Response{Identity: r.Identity, Tag: r.Tag, Operation: r.Operation, ByteEnable: mask, AcceptedCycle: cycle, Sequence: b.sequence}})
		result.Accepted[client] = true
		b.last[client], b.used[client] = r.Identity.Transaction, true
		accepted++
		next = (client + 1) % len(offers)
	}
	b.nextClient = next
	for i, o := range offers {
		b.held[i] = Offer{}
		if o.Valid && !result.Accepted[i] {
			b.held[i] = o
		}
	}
	b.cycle, b.started = cycle, true
	return result, nil
}

// PreviewResponses predicts response headers for a Step edge without accessing
// backing data. Consumers use identity to compute path-specific ready. It lists
// the maximal return prefix; Step truncates it at the first backpressured client.
func (b *Backend) PreviewResponses(cycle uint64) ([]Reply, error) {
	if b.started && (b.cycle == math.MaxUint64 || cycle != b.cycle+1) {
		return nil, fmt.Errorf("noncontiguous backend preview cycle")
	}
	result := make([]Reply, len(b.held))
	for i, e := range b.queue {
		if i >= b.config.ReturnsPerCycle || e.due > cycle || result[e.client].Valid {
			break
		}
		result[e.client] = Reply{Valid: true, Response: e.response}
	}
	return result, nil
}
