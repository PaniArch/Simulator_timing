package memsys

import "fmt"

type bankKind uint8

const (
	bankCore bankKind = iota
	bankReplay
	bankFillOp
	bankInit
	bankFlush
)

type bankOperation struct {
	kind     bankKind
	request  WordRequest
	port     int
	set, way int
	handle   mshrHandle
	fill     bankFill
}
type bankCommit struct {
	op         bankOperation
	hit        bool
	allocation *mshrAllocationResult
	reply      WordResponse
	victim     eviction
}
type bankFill struct {
	handle mshrHandle
	data   [SectorBytes]byte
	err    error
}
type bankMemoryRequest struct {
	address  uint32
	write    bool
	data     [SectorBytes]byte
	handle   mshrHandle
	identity Identity
}
type bankResponse struct {
	port     int
	response WordResponse
}
type bankInput struct {
	request                    WordOffer
	port                       int
	responseReady, memoryReady bool
	fill                       *bankFill
	flushBegin                 bool
}
type bankEdge struct {
	accepted, fillAccepted bool
	response               *bankResponse
	responseDelivered      bool
	memory                 *bankMemoryRequest
	memoryAccepted         bool
	storeApplied           *WordRequest
	storePort              int
	storeError             error
	flushDone              bool
}
type bankFlushState uint8

const (
	flushIdle bankFlushState = iota
	flushWait
	flushWalk
	flushDrain
	flushDone
)

type cacheBank struct {
	spec       CacheSpec
	bank       int
	array      *cacheArray
	mshr       *cacheMSHR
	reserved   int // VX_pending_size includes accepted requests still before S0 allocation
	s0         *bankOperation
	s1         *bankCommit
	responses  []bankResponse
	memory     []bankMemoryRequest
	initNext   int
	flushing   bankFlushState
	flushIndex int
	forwarding bool
	forwarded  bankFill
}

func newCacheBank(s CacheSpec, bank int) *cacheBank {
	return &cacheBank{spec: s, bank: bank, array: newCacheArray(s, bank), mshr: newCacheMSHR(s.MSHR)}
}
func (b *cacheBank) empty() bool { return b.s0 == nil && b.s1 == nil && len(b.memory) == 0 }
func (b *cacheBank) drained() bool {
	return b.initNext == b.spec.Sets && b.flushing == flushIdle && b.empty() && b.reserved == 0 && len(b.responses) == 0
}
func (b *cacheBank) requestValid(r WordRequest) error {
	bank, _, _, _ := b.spec.DecodeAddress(r.Address)
	if r.Address%uint32(b.spec.WordBytes) != 0 || bank != b.bank || r.ByteEnable>>b.spec.WordBytes != 0 {
		return fmt.Errorf("invalid cache word address/bank/byte enable")
	}
	if r.Write && !b.spec.Writeback {
		return fmt.Errorf("store to instruction cache")
	}
	return nil
}

// step advances the bank arbitration and two pipeline registers. No event has
// a precomputed completion time: replay, refill, queue credit and stalled S1
// decide whether the registers can advance on this edge.
func (b *cacheBank) step(in bankInput) (bankEdge, error) {
	if in.request.Valid {
		if err := b.requestValid(in.request.Request); err != nil {
			return bankEdge{}, err
		}
		if in.port < 0 || in.port >= b.spec.CorePorts {
			return bankEdge{}, fmt.Errorf("invalid cache port")
		}
	}
	if in.flushBegin && b.flushing != flushIdle {
		return bankEdge{}, fmt.Errorf("duplicate bank flush")
	}
	if in.fill != nil {
		e, err := b.mshr.entry(in.fill.handle)
		if err != nil {
			return bankEdge{}, err
		}
		if !e.finalized {
			return bankEdge{}, fmt.Errorf("fill for unfinalized miss")
		}
	}
	result := bankEdge{flushDone: b.flushing == flushDone}
	if len(b.responses) > 0 {
		v := b.responses[0]
		result.response = &v
		result.responseDelivered = in.responseReady
	}
	if len(b.memory) > 0 {
		v := b.memory[0]
		result.memory = &v
		result.memoryAccepted = in.memoryReady
	}
	rspSpace := len(b.responses) < b.spec.CoreResponse.Size
	memSpace := len(b.memory) < b.spec.MemoryRequest.Size
	memAlmostFull := len(b.memory) >= b.spec.MemoryRequest.Size-b.spec.Latency
	commitRead := b.s1 != nil && (b.s1.op.kind == bankCore || b.s1.op.kind == bankReplay) && !b.s1.op.request.Write && b.s1.hit
	commitMemory := b.s1 != nil && (b.s1.victim.Valid || (b.s1.op.kind == bankCore && !b.s1.hit && b.s1.allocation.previous == nil))
	stalled := (commitRead && !rspSpace) || (commitMemory && !memSpace)
	// This component has no AMO path: the frozen ISA baseline disables EXT_A.
	events := mshrEvents{}
	var pushedResponse *bankResponse
	var pushedMemory *bankMemoryRequest
	if !stalled && b.s1 != nil {
		c := b.s1
		if c.op.kind == bankCore {
			events.finalize = &mshrFinalization{handle: c.allocation.handle, release: c.hit, previous: c.allocation.previous}
		}
		if commitRead {
			r := bankResponse{c.op.port, c.reply}
			pushedResponse = &r
		}
		if c.victim.Valid {
			v := bankMemoryRequest{address: c.victim.Address, write: true, data: c.victim.Data, identity: c.op.request.Identity}
			pushedMemory = &v
		} else if c.op.kind == bankCore && !c.hit && c.allocation.previous == nil {
			v := bankMemoryRequest{address: c.op.request.Address &^ uint32(SectorBytes-1), handle: c.allocation.handle, identity: c.op.request.Identity}
			pushedMemory = &v
		}
		if (c.op.kind == bankCore || c.op.kind == bankReplay) && c.op.request.Write && c.hit {
			v := c.op.request
			result.storeApplied = &v
			result.storePort = c.op.port
			result.storeError = c.reply.Err
		}
	}
	head, entry, replay := b.mshr.replay()
	forwardHead := b.forwarding && replay && !entry.request.Write && !commitRead && !stalled
	if forwardHead && rspSpace {
		response := WordResponse{Identity: entry.request.Identity, Tag: entry.request.Tag, ByteEnable: uint8((1 << b.spec.WordBytes) - 1), Err: b.forwarded.err}
		offset := int(entry.request.Address % SectorBytes)
		copy(response.Data[:b.spec.WordBytes], b.forwarded.data[offset:offset+b.spec.WordBytes])
		if response.Err != nil {
			response.Data = [8]byte{}
		}
		r := bankResponse{entry.port, response}
		pushedResponse = &r
		events.dequeue = true
	}
	var selected *bankOperation
	if b.initNext < b.spec.Sets {
		selected = &bankOperation{kind: bankInit, set: b.initNext}
	} else if !stalled {
		switch {
		case replay && !forwardHead:
			selected = &bankOperation{kind: bankReplay, request: entry.request, port: entry.port, handle: head, fill: b.forwarded}
			events.dequeue = true
		case in.fill != nil && !(b.forwarding && replay) && (!b.spec.Writeback || !memAlmostFull):
			selected = &bankOperation{kind: bankFillOp, fill: *in.fill, handle: in.fill.handle}
			events.fill = &in.fill.handle
			result.fillAccepted = true
		case b.flushing == flushWalk && (!b.spec.Writeback || !memAlmostFull):
			selected = &bankOperation{kind: bankFlush, set: b.flushIndex % b.spec.Sets, way: b.flushIndex / b.spec.Sets}
		case b.flushing == flushIdle && !in.flushBegin && in.request.Valid && !memAlmostFull && b.reserved < b.spec.MSHR:
			selected = &bankOperation{kind: bankCore, request: in.request.Request, port: in.port}
			result.accepted = true
		}
	}
	if stalled {
		selected = nil
	}
	if !stalled && b.s0 != nil && b.s0.kind == bankCore {
		events.allocate = &mshrAllocation{request: b.s0.request, port: b.s0.port}
	}
	// Read all relevant old-edge predicates before committing MSHR/table changes.
	bankEmpty := b.empty()
	mshrEmpty := b.reserved == 0
	allocation, err := b.mshr.step(events)
	if err != nil {
		return bankEdge{}, err
	}
	if result.responseDelivered {
		b.responses = b.responses[1:]
	}
	if result.memoryAccepted {
		b.memory = b.memory[1:]
	}
	if pushedResponse != nil {
		b.responses = append(b.responses, *pushedResponse)
	}
	if pushedMemory != nil {
		b.memory = append(b.memory, *pushedMemory)
	}
	if result.accepted {
		b.reserved++
	}
	if events.dequeue {
		b.reserved--
	}
	if events.finalize != nil && events.finalize.release {
		b.reserved--
	}
	if !stalled {
		var next *bankCommit
		if b.s0 != nil {
			op := *b.s0
			next = &bankCommit{op: op, allocation: allocation}
			switch op.kind {
			case bankInit:
				b.array.initSet(op.set)
			case bankCore, bankReplay:
				// Replays from a failed fill complete with the original error and never
				// install or update cache bytes. Software store receipts preserve faults.
				if op.kind == bankReplay && op.fill.err != nil {
					next.hit = true
					next.reply = WordResponse{Identity: op.request.Identity, Tag: op.request.Tag, ByteEnable: uint8((1 << b.spec.WordBytes) - 1), Err: op.fill.err}
				} else if op.request.Write {
					next.hit, err = b.array.write(op.request)
				} else {
					next.reply, next.hit, err = b.array.read(op.request)
				}
				if err != nil {
					return bankEdge{}, err
				}
				if op.kind == bankReplay && !next.hit {
					return bankEdge{}, fmt.Errorf("cache replay missed")
				}
			case bankFillOp:
				if op.fill.err == nil {
					next.victim, err = b.array.fill(op.request.Address, op.fill.data)
					if err != nil {
						return bankEdge{}, err
					}
				}
			case bankFlush:
				if b.spec.Writeback {
					next.victim = b.array.flushWay(op.set, op.way)
				} else {
					for way := 0; way < b.spec.Ways; way++ {
						b.array.flushWay(op.set, way)
					}
				}
			}
		}
		b.s1 = next
		if selected != nil && selected.kind == bankFillOp {
			e, lookupErr := b.mshr.entry(selected.handle)
			if lookupErr != nil {
				return bankEdge{}, lookupErr
			}
			selected.request = WordRequest{Address: e.line, Identity: e.request.Identity}
		}
		b.s0 = selected
	}
	if b.forwarding && replay && entry.request.Write {
		b.forwarding = false
	}
	if result.fillAccepted {
		b.forwarded = *in.fill
		b.forwarding = true
	}
	if selected != nil && selected.kind == bankInit {
		b.initNext++
	}
	switch b.flushing {
	case flushWait:
		if b.initNext == b.spec.Sets && mshrEmpty && bankEmpty {
			b.flushing = flushWalk
			b.flushIndex = 0
		}
	case flushWalk:
		if selected != nil && selected.kind == bankFlush {
			b.flushIndex++
			count := b.spec.Sets
			if b.spec.Writeback {
				count *= b.spec.Ways
			}
			if b.flushIndex == count {
				if b.bank == 0 {
					b.flushing = flushDone
				} else {
					b.flushing = flushDrain
				}
			}
		}
	case flushDrain:
		if bankEmpty {
			b.flushing = flushDone
		}
	case flushDone:
		b.flushing = flushIdle
	}
	if in.flushBegin {
		b.flushing = flushWait
	}
	// VX_cache_flush latches a flush during reset scan and acknowledges the
	// scan itself, rather than performing a second full invalidation pass.
	if selected != nil && selected.kind == bankInit && b.initNext == b.spec.Sets && b.flushing == flushWait {
		b.flushing = flushDone
	}
	return result, nil
}
