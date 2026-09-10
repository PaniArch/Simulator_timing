package memsys

import (
	"fmt"
	"math"

	"vortex.local/simulator/timing"
)

type WordReply struct {
	Valid, Delivered bool
	Response         WordResponse
}
type StoreReceipt struct {
	Port    int
	Request WordRequest
	Err     error
}
type FlushOffer struct {
	Valid    bool
	Identity Identity
	Tag      uint64
}
type FlushReply struct {
	Valid, Delivered bool
	Identity         Identity
	Tag              uint64
	Err              error
}
type CacheInput struct {
	Requests      []WordOffer
	ResponseReady []bool
	Memory        Edge
	Flush         FlushOffer
	FlushReady    bool
}
type CacheEdge struct {
	Accepted        []bool
	Replies         []WordReply
	Stores          []StoreReceipt
	FlushAccepted   bool
	Flush           FlushReply
	WritebackErrors []Response
}
type cachePacketKind uint8

const (
	packetFill cachePacketKind = iota
	packetWriteback
	packetNC
	packetFlush
)

type cachePacket struct {
	acceptedCycle uint64
	request       Request
	kind          cachePacketKind
	handle        mshrHandle
	origin        WordRequest
	port          int
}
type cacheIncoming struct {
	response Response
	packet   cachePacket
}

// cacheQueue uses the old-edge FIFO credit rule. A one-entry pipe alone may
// replace its departing value on the same edge (VX_elastic_buffer SIZE=1).
type cacheQueue[T any] struct {
	spec   timing.BufferSpec
	values []T
}

func (q *cacheQueue[T]) ready(pop bool) bool {
	return len(q.values) < q.spec.Size || (q.spec.Size == 1 && pop)
}
func (q *cacheQueue[T]) update(pop bool, push *T) {
	if pop {
		q.values = q.values[1:]
	}
	if push != nil {
		q.values = append(q.values, *push)
	}
}
func pick(start int, valid []bool) int {
	for offset := range valid {
		i := (start + offset) % len(valid)
		if valid[i] {
			return i
		}
	}
	return -1
}

type cacheControl uint8

const (
	controlIdle cacheControl = iota
	controlPulse
	controlBanks
	controlTail
	controlTicket
	controlReturn
	controlRelease
)

// Cache owns one frozen I- or D-cache instance. Call MemoryOffers and
// MemoryResponseReady before the external backend edge, then Step once with
// its handshake result. Core response data and queued requests are values.
type Cache struct {
	spec                                             CacheSpec
	banks                                            []*cacheBank
	core, coreOut                                    []cacheQueue[WordResponse]
	memory, memoryOut                                []cacheQueue[cachePacket]
	mrsq                                             []cacheQueue[cacheIncoming]
	refill                                           []cacheQueue[bankFill]
	requestRR, responseRR, ncRequestRR, ncResponseRR []int
	sequence, last                                   []uint64
	used                                             []bool
	held                                             []WordOffer
	sent                                             []map[uint64]cachePacket
	heldFlush                                        FlushOffer
	lastFlush                                        uint64
	usedFlush                                        bool
	inlineFlush                                      bool
	released                                         []bool
	control                                          cacheControl
	flush                                            FlushOffer
	flushDone                                        []bool
	flushErr, writeErr                               error
	writeErrSequence                                 uint64
	cycle                                            uint64
	started                                          bool
}

func NewCache(kind CacheKind) (*Cache, error) {
	s, err := FrozenCacheSpec(kind)
	if err != nil {
		return nil, err
	}
	c := &Cache{spec: s, banks: make([]*cacheBank, s.Banks), core: make([]cacheQueue[WordResponse], s.CorePorts), memory: make([]cacheQueue[cachePacket], s.MemoryPorts), mrsq: make([]cacheQueue[cacheIncoming], s.MemoryPorts), refill: make([]cacheQueue[bankFill], s.Banks), requestRR: make([]int, s.Banks), responseRR: make([]int, s.CorePorts), ncRequestRR: make([]int, s.MemoryPorts), ncResponseRR: make([]int, s.CorePorts), sequence: make([]uint64, s.MemoryPorts), last: make([]uint64, s.CorePorts), used: make([]bool, s.CorePorts), held: make([]WordOffer, s.CorePorts), sent: make([]map[uint64]cachePacket, s.MemoryPorts), flushDone: make([]bool, s.Banks)}
	for i := range c.banks {
		c.banks[i] = newCacheBank(s, i)
		c.refill[i].spec = s.RefillCrossbar
	}
	for i := range c.core {
		c.core[i].spec = s.OuterCoreResponse
	}
	for i := range c.memory {
		c.memory[i].spec = s.OuterMemoryRequest
		c.mrsq[i].spec = s.MemoryResponse
		c.sent[i] = make(map[uint64]cachePacket)
	}
	if kind == DataCache {
		rsp, err := timing.Buffer("b-dcache-nc-response")
		if err != nil {
			return nil, err
		}
		req, err := timing.Buffer("b-dcache-nc-memory-request")
		if err != nil {
			return nil, err
		}
		c.coreOut = make([]cacheQueue[WordResponse], s.CorePorts)
		c.memoryOut = make([]cacheQueue[cachePacket], s.MemoryPorts)
		for i := range c.coreOut {
			c.coreOut[i].spec = rsp
			c.memoryOut[i].spec = req
		}
	}
	return c, nil
}
func (c *Cache) Spec() CacheSpec { return c.spec }
func (c *Cache) finalMemory() []cacheQueue[cachePacket] {
	if c.spec.Kind == DataCache {
		return c.memoryOut
	}
	return c.memory
}
func (c *Cache) finalCore() []cacheQueue[WordResponse] {
	if c.spec.Kind == DataCache {
		return c.coreOut
	}
	return c.core
}
func (c *Cache) MemoryOffers() []Offer {
	result := make([]Offer, c.spec.MemoryPorts)
	for i, q := range c.finalMemory() {
		if len(q.values) > 0 {
			result[i] = Offer{true, q.values[0].request}
		}
	}
	return result
}

// MemoryResponseReady uses only the prospective response identity, not its
// data. Backend.PreviewResponses supplies these headers without servicing data.
// Software write acknowledgements/tickets consume no fictitious RTL MRSQ slot.
func (c *Cache) MemoryResponseReady(port int, r Response) bool {
	if port < 0 || port >= len(c.sent) {
		return false
	}
	p, ok := c.sent[port][r.Identity.Transaction]
	if !ok {
		return false
	}
	switch p.kind {
	case packetFill:
		if c.spec.MemoryResponse.Size == 0 {
			return c.refill[port].ready(false)
		}
		return c.mrsq[port].ready(false)
	case packetNC:
		if p.origin.Write {
			return true
		}
		valid := []bool{true, len(c.core[port].values) > 0}
		return c.coreOut[port].ready(false) && pick(c.ncResponseRR[port], valid) == 0
	default:
		return true
	}
}
func (c *Cache) wire(port int, p cachePacket) cachePacket {
	c.sequence[port]++
	p.request.Identity.Transaction = c.sequence[port]
	return p
}
func (c *Cache) fromBank(port int, v bankMemoryRequest) cachePacket {
	op, kind := Read, packetFill
	identity := v.identity
	if v.write {
		op, kind = Write, packetWriteback
		identity = Identity{}
	}
	return cachePacket{request: Request{Identity: identity, Tag: uint64(v.handle.slot), Operation: op, Address: v.address, ByteEnable: ^uint64(0), Data: v.data}, kind: kind, handle: v.handle, port: port}
}
func (c *Cache) noncached(port int, r WordRequest) cachePacket {
	offset := int(r.Address % SectorBytes)
	op := Read
	if r.Write {
		op = Write
	}
	p := cachePacket{kind: packetNC, origin: r, port: port, request: Request{Identity: r.Identity, Tag: r.Tag, Operation: op, Address: r.Address &^ uint32(SectorBytes-1), ByteEnable: uint64(r.ByteEnable) << offset}}
	copy(p.request.Data[offset:offset+c.spec.WordBytes], r.Data[:c.spec.WordBytes])
	return p
}
func (c *Cache) validate(cycle uint64, in CacheInput) error {
	if len(in.Requests) != c.spec.CorePorts || len(in.ResponseReady) != c.spec.CorePorts || len(in.Memory.Accepted) != c.spec.MemoryPorts || len(in.Memory.Replies) != c.spec.MemoryPorts {
		return fmt.Errorf("cache port count mismatch")
	}
	if c.started && (c.cycle == math.MaxUint64 || cycle != c.cycle+1) {
		return fmt.Errorf("noncontiguous cache cycle")
	}
	for i, o := range in.Requests {
		if c.held[i].Valid && c.held[i] != o {
			return fmt.Errorf("cache client %d changed stalled request", i)
		}
		if !o.Valid {
			continue
		}
		if c.used[i] && o.Request.Identity.Transaction <= c.last[i] {
			return fmt.Errorf("stale cache client transaction")
		}
		bank, _, _, _ := c.spec.DecodeAddress(o.Request.Address)
		if err := c.banks[bank].requestValid(o.Request); err != nil {
			return err
		}
		if o.Request.NonCacheable && c.spec.Kind != DataCache {
			return fmt.Errorf("instruction cache has no NC path")
		}
	}
	if c.heldFlush.Valid && c.heldFlush != in.Flush {
		return fmt.Errorf("changed stalled cache flush")
	}
	if in.Flush.Valid && c.usedFlush && in.Flush.Identity.Transaction <= c.lastFlush {
		return fmt.Errorf("stale cache flush transaction")
	}
	offers := c.MemoryOffers()
	for i, r := range in.Memory.Replies {
		if c.sequence[i] == math.MaxUint64 {
			return fmt.Errorf("cache external transaction exhausted")
		}
		if in.Memory.Accepted[i] && !offers[i].Valid {
			return fmt.Errorf("external acceptance without cache offer")
		}
		if r.Delivered && !r.Valid {
			return fmt.Errorf("invalid external response handshake")
		}
		if !r.Valid {
			continue
		}
		p, ok := c.sent[i][r.Response.Identity.Transaction]
		if !ok || p.request.Identity != r.Response.Identity || p.request.Tag != r.Response.Tag || p.request.Operation != r.Response.Operation {
			return fmt.Errorf("foreign or repeated cache memory response")
		}
		if r.Delivered && !c.MemoryResponseReady(i, r.Response) {
			return fmt.Errorf("cache response delivered under backpressure")
		}
		if r.Response.AcceptedCycle != p.acceptedCycle || r.Response.CompletedCycle <= p.acceptedCycle || r.Response.CompletedCycle > cycle || r.Response.Sequence == 0 {
			return fmt.Errorf("invalid cache memory response timing")
		}
		mask := p.request.ByteEnable
		if p.request.Operation == Read {
			mask = ^uint64(0)
		}
		if p.request.Operation == Visibility {
			mask = 0
		}
		if r.Response.ByteEnable != mask {
			return fmt.Errorf("invalid cache memory response mask")
		}
		if p.kind == packetFill {
			if _, err := c.banks[i].mshr.entry(p.handle); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Cache) Step(cycle uint64, in CacheInput) (CacheEdge, error) {
	if err := c.validate(cycle, in); err != nil {
		return CacheEdge{}, err
	}
	e := CacheEdge{Accepted: make([]bool, c.spec.CorePorts), Replies: make([]WordReply, c.spec.CorePorts)}
	finalCore := c.finalCore()
	corePop := make([]bool, c.spec.CorePorts)
	outCorePush := make([]*WordResponse, c.spec.CorePorts)
	for i, q := range finalCore {
		if len(q.values) > 0 {
			e.Replies[i] = WordReply{true, in.ResponseReady[i], q.values[0]}
		}
	}
	// Decode old external replies and arbitrate cached/NC responses into the
	// actual D-cache bypass response buffers before deciding pipe replacement.
	incoming := make([]*cacheIncoming, c.spec.MemoryPorts)
	for port, r := range in.Memory.Replies {
		var nc *cachePacket
		if r.Valid {
			p := c.sent[port][r.Response.Identity.Transaction]
			if p.kind == packetNC && !p.origin.Write {
				nc = &p
			}
		}
		if c.spec.Kind == DataCache {
			choice := pick(c.ncResponseRR[port], []bool{nc != nil, len(c.core[port].values) > 0})
			if c.coreOut[port].ready(false) && choice >= 0 {
				if choice == 1 {
					v := c.core[port].values[0]
					outCorePush[port] = &v
					corePop[port] = true
					c.ncResponseRR[port] = 0
				} else if r.Delivered {
					v := WordResponse{Identity: nc.origin.Identity, Tag: nc.origin.Tag, ByteEnable: uint8((1 << c.spec.WordBytes) - 1), Err: r.Response.Err}
					offset := int(nc.origin.Address % SectorBytes)
					copy(v.Data[:c.spec.WordBytes], r.Response.Data[offset:offset+c.spec.WordBytes])
					if v.Err != nil {
						v.Data = [8]byte{}
					}
					outCorePush[port] = &v
					c.ncResponseRR[port] = 1
				}
			}
		} else {
			corePop[port] = e.Replies[port].Delivered
		}
		if !r.Delivered {
			continue
		}
		p := c.sent[port][r.Response.Identity.Transaction]
		switch p.kind {
		case packetFill:
			v := cacheIncoming{r.Response, p}
			incoming[port] = &v
		case packetWriteback:
			if r.Response.Err != nil {
				if c.writeErr == nil || r.Response.Sequence < c.writeErrSequence {
					c.writeErr = r.Response.Err
					c.writeErrSequence = r.Response.Sequence
				}
				e.WritebackErrors = append(e.WritebackErrors, r.Response)
			}
		case packetNC:
			if p.origin.Write {
				e.Stores = append(e.Stores, StoreReceipt{p.port, p.origin, r.Response.Err})
			}
		case packetFlush:
			c.flushErr = r.Response.Err
			if c.flushErr == nil {
				c.flushErr = c.writeErr
			}
			c.control = controlReturn
		}
		delete(c.sent[port], r.Response.Identity.Transaction)
	}
	if c.control == controlReturn && c.flushErr == nil {
		c.flushErr = c.writeErr
	}
	oldControl := c.control
	// Completion stays valid until consumed. Unlike bank done, this follows the
	// downstream Visibility ticket; already captured core responses may remain.
	if oldControl == controlReturn {
		e.Flush = FlushReply{true, in.FlushReady, c.flush.Identity, c.flush.Tag, c.flushErr}
	}
	flushMask := make([]bool, c.spec.CorePorts)
	anyFlush := false
	for p, o := range in.Requests {
		flushMask[p] = o.Valid && o.Request.Flush && !o.Request.NonCacheable
		anyFlush = anyFlush || flushMask[p]
	}
	startInline := oldControl == controlIdle && anyFlush && !in.Flush.Valid
	blockCore := oldControl != controlIdle || in.Flush.Valid || anyFlush
	enabled := func(port int) bool {
		if c.inlineFlush && oldControl == controlRelease {
			return !anyFlush || c.released[port]
		}
		return !blockCore
	}
	if startInline {
		c.inlineFlush = true
		clear(c.flushDone)
	}

	if oldControl == controlIdle && in.Flush.Valid {
		e.FlushAccepted = true
		c.inlineFlush = false
		c.flush = in.Flush
		clear(c.flushDone)
		c.flushErr = nil
	}
	memoryPop := make([]bool, c.spec.MemoryPorts)
	outMemoryPush := make([]*cachePacket, c.spec.MemoryPorts)
	for port := range c.memory {
		if c.spec.Kind == DataCache {
			nc := (!blockCore || c.inlineFlush) && in.Requests[port].Valid && in.Requests[port].Request.NonCacheable
			choice := pick(c.ncRequestRR[port], []bool{nc, len(c.memory[port].values) > 0})
			if c.memoryOut[port].ready(false) && choice >= 0 {
				var p cachePacket
				if choice == 0 {
					p = c.noncached(port, in.Requests[port].Request)
					e.Accepted[port] = true
				} else {
					p = c.memory[port].values[0]
					memoryPop[port] = true
				}
				p = c.wire(port, p)
				outMemoryPush[port] = &p
				c.ncRequestRR[port] = (choice + 1) % 2
			}
		} else {
			memoryPop[port] = in.Memory.Accepted[port]
		}
	}
	bankInputs := make([]bankInput, c.spec.Banks)
	responseWinner := make([]int, c.spec.CorePorts)
	for port := range c.core {
		valid := make([]bool, c.spec.Banks)
		for bank, b := range c.banks {
			valid[bank] = len(b.responses) > 0 && b.responses[0].port == port
		}
		responseWinner[port] = pick(c.responseRR[port], valid)
		if winner := responseWinner[port]; winner >= 0 && c.core[port].ready(corePop[port]) {
			bankInputs[winner].responseReady = true
		}
	}
	requestWinner := make([]int, c.spec.Banks)
	refillPush := make([]*bankFill, c.spec.Banks)
	mrsqPop := make([]bool, c.spec.MemoryPorts)
	for bank := range c.banks {
		valid := make([]bool, c.spec.CorePorts)
		for port, o := range in.Requests {
			if enabled(port) && o.Valid && !o.Request.NonCacheable {
				target, _, _, _ := c.spec.DecodeAddress(o.Request.Address)
				valid[port] = target == bank
			}
		}
		winner := pick(c.requestRR[bank], valid)
		requestWinner[bank] = winner
		if winner >= 0 {
			bankInputs[bank].request = in.Requests[winner]
			bankInputs[bank].port = winner
		}
		bankInputs[bank].memoryReady = c.memory[bank].ready(memoryPop[bank])
		if len(c.refill[bank].values) > 0 {
			v := c.refill[bank].values[0]
			bankInputs[bank].fill = &v
		}
		bankInputs[bank].flushBegin = oldControl == controlPulse
		var inc *cacheIncoming
		if c.spec.MemoryResponse.Size == 0 {
			inc = incoming[bank]
		} else if len(c.mrsq[bank].values) > 0 {
			v := c.mrsq[bank].values[0]
			inc = &v
		}
		if inc != nil && c.refill[bank].ready(false) {
			v := bankFill{inc.packet.handle, inc.response.Data, inc.response.Err}
			refillPush[bank] = &v
			mrsqPop[bank] = c.spec.MemoryResponse.Size != 0
		}
	}
	// Capture tail before these edges; dirty/valid residency is intentionally not
	// part of this predicate. All prior writebacks must have crossed the link.
	tailEmpty := true
	for i, b := range c.banks {
		tailEmpty = tailEmpty && b.empty() && b.reserved == 0 && len(c.memory[i].values) == 0
		if c.spec.Kind == DataCache {
			tailEmpty = tailEmpty && len(c.memoryOut[i].values) == 0
		}
	}
	corePush := make([]*WordResponse, c.spec.CorePorts)
	memoryPush := make([]*cachePacket, c.spec.MemoryPorts)
	for bank, b := range c.banks {
		r, err := b.step(bankInputs[bank])
		if err != nil {
			return CacheEdge{}, err
		}
		if r.accepted {
			port := requestWinner[bank]
			e.Accepted[port] = true
			c.requestRR[bank] = (port + 1) % c.spec.CorePorts
		}
		if r.responseDelivered {
			port := r.response.port
			v := r.response.response
			corePush[port] = &v
			c.responseRR[port] = (bank + 1) % c.spec.Banks
		}
		if r.memoryAccepted {
			p := c.fromBank(bank, *r.memory)
			if c.spec.Kind == InstructionCache {
				p = c.wire(bank, p)
			}
			memoryPush[bank] = &p
		}
		if r.storeApplied != nil {
			e.Stores = append(e.Stores, StoreReceipt{r.storePort, *r.storeApplied, r.storeError})
		}
		if r.flushDone {
			c.flushDone[bank] = true
		}
		c.refill[bank].update(r.fillAccepted, refillPush[bank])
	}
	switch oldControl {
	case controlIdle:
		if e.FlushAccepted || startInline {
			c.control = controlPulse
		}
	case controlPulse:
		c.control = controlBanks
	case controlBanks:
		done := true
		for _, v := range c.flushDone {
			done = done && v
		}
		if done {
			if c.inlineFlush {
				c.control = controlRelease
				c.released = append([]bool(nil), flushMask...)
			} else {
				c.control = controlTail
			}
		}
	case controlRelease:
		pending := false
		for p := range c.released {
			if e.Accepted[p] {
				c.released[p] = false
			}
			pending = pending || c.released[p]
		}
		if !pending {
			c.control = controlIdle
			c.inlineFlush = false
		}
	case controlTail:
		if tailEmpty && c.memory[0].ready(memoryPop[0]) {
			p := cachePacket{kind: packetFlush, request: Request{Identity: c.flush.Identity, Tag: c.flush.Tag, Operation: Visibility}}
			if c.spec.Kind == InstructionCache {
				p = c.wire(0, p)
			}
			memoryPush[0] = &p
			c.control = controlTicket
		}
	case controlReturn:
		if e.Flush.Delivered {
			c.control = controlIdle
		}
	}
	for port := range c.memory {
		if in.Memory.Accepted[port] {
			p := c.finalMemory()[port].values[0]
			p.acceptedCycle = cycle
			c.sent[port][p.request.Identity.Transaction] = p
		}
		if c.spec.Kind == DataCache {
			c.memoryOut[port].update(in.Memory.Accepted[port], outMemoryPush[port])
			c.coreOut[port].update(e.Replies[port].Delivered, outCorePush[port])
		}
		c.memory[port].update(memoryPop[port], memoryPush[port])
		c.core[port].update(corePop[port], corePush[port])
		if c.spec.MemoryResponse.Size != 0 {
			c.mrsq[port].update(mrsqPop[port], incoming[port])
		}
	}
	for i, o := range in.Requests {
		c.held[i] = WordOffer{}
		if o.Valid && !e.Accepted[i] {
			c.held[i] = o
		}
		if e.Accepted[i] {
			c.last[i] = o.Request.Identity.Transaction
			c.used[i] = true
		}
	}
	c.heldFlush = FlushOffer{}
	if in.Flush.Valid && !e.FlushAccepted {
		c.heldFlush = in.Flush
	}
	if e.FlushAccepted {
		c.lastFlush, c.usedFlush = in.Flush.Identity.Transaction, true
	}
	c.cycle, c.started = cycle, true
	return e, nil
}

// Drained includes live transport and software response ownership, but not
// valid/dirty cache lines. Flush completion and Drained are separate predicates.
func (c *Cache) Drained() bool {
	if c.control != controlIdle {
		return false
	}
	for i, b := range c.banks {
		if !b.drained() || len(c.core[i].values) != 0 || len(c.memory[i].values) != 0 || len(c.mrsq[i].values) != 0 || len(c.refill[i].values) != 0 || len(c.sent[i]) != 0 {
			return false
		}
		if c.spec.Kind == DataCache && (len(c.coreOut[i].values) != 0 || len(c.memoryOut[i].values) != 0) {
			return false
		}
	}
	return true
}

// CacheState is a detached resource view. Counts refer to physical resources;
// a request may have aliases in multiple fields, so totals must not be added.
type CacheBankState struct {
	Reserved, Pipeline, CoreResponses, MemoryRequests, RefillResponses int
	ValidLines, DirtyLines                                             int
	Initializing                                                       bool
	Flushing                                                           bool
}
type CacheState struct {
	Banks                                                                                                      []CacheBankState
	CoreBuffers, CoreBypassBuffers, MemoryBuffers, MemoryBypassBuffers, MemoryResponseBuffers, ExternalPending []int
	FlushActive, Drained                                                                                       bool
	WritebackError                                                                                             error
}

func (c *Cache) State() CacheState {
	s := CacheState{Banks: make([]CacheBankState, len(c.banks)), FlushActive: c.control != controlIdle, Drained: c.Drained(), WritebackError: c.writeErr}
	for i, b := range c.banks {
		valid, dirty := b.array.residentCounts()
		pipeline := 0
		if b.s0 != nil {
			pipeline++
		}
		if b.s1 != nil {
			pipeline++
		}
		s.Banks[i] = CacheBankState{b.reserved, pipeline, len(b.responses), len(b.memory), len(c.refill[i].values), valid, dirty, b.initNext < b.spec.Sets, b.flushing != flushIdle}
		s.CoreBuffers = append(s.CoreBuffers, len(c.core[i].values))
		s.MemoryBuffers = append(s.MemoryBuffers, len(c.memory[i].values))
		s.MemoryResponseBuffers = append(s.MemoryResponseBuffers, len(c.mrsq[i].values))
		s.ExternalPending = append(s.ExternalPending, len(c.sent[i]))
		if c.spec.Kind == DataCache {
			s.CoreBypassBuffers = append(s.CoreBypassBuffers, len(c.coreOut[i].values))
			s.MemoryBypassBuffers = append(s.MemoryBypassBuffers, len(c.memoryOut[i].values))
		}
	}
	return s
}

// HasResidency reports accepted work which can still use this residency.
// Cache-owned writebacks and resident dirty lines carry no CTA ownership;
// unaccepted input offers remain the upstream producer's responsibility.
func (c *Cache) HasResidency(kernel, cta uint64) bool {
	belongs := func(id Identity) bool { return id.Kernel == kernel && id.CTA == cta }
	packetBelongs := func(p cachePacket) bool { return p.kind != packetWriteback && belongs(p.request.Identity) }
	if c.control != controlIdle && !c.inlineFlush && belongs(c.flush.Identity) {
		return true
	}
	for i, b := range c.banks {
		for _, e := range b.mshr.entries {
			if e.valid && belongs(e.request.Identity) {
				return true
			}
		}
		if b.s0 != nil && b.s0.kind < bankInit && belongs(b.s0.request.Identity) {
			return true
		}
		if b.s1 != nil && b.s1.op.kind < bankInit && belongs(b.s1.op.request.Identity) {
			return true
		}
		for _, r := range b.responses {
			if belongs(r.response.Identity) {
				return true
			}
		}
		for _, r := range b.memory {
			if !r.write && belongs(r.identity) {
				return true
			}
		}
		for _, r := range c.core[i].values {
			if belongs(r.Identity) {
				return true
			}
		}
		for _, p := range c.memory[i].values {
			if packetBelongs(p) {
				return true
			}
		}
		for _, p := range c.sent[i] {
			if packetBelongs(p) {
				return true
			}
		}
		for _, p := range c.mrsq[i].values {
			if packetBelongs(p.packet) {
				return true
			}
		}
		if c.spec.Kind == DataCache {
			for _, r := range c.coreOut[i].values {
				if belongs(r.Identity) {
					return true
				}
			}
			for _, p := range c.memoryOut[i].values {
				if packetBelongs(p) {
					return true
				}
			}
		}
	}
	return false
}

// Responses exposes detached old-edge payloads for downstream combinational
// ready calculation. Delivered is false until Step applies the handshake.
func (c *Cache) Responses() []WordReply {
	replies := make([]WordReply, c.spec.CorePorts)
	for p, q := range c.finalCore() {
		if len(q.values) > 0 {
			replies[p] = WordReply{Valid: true, Response: q.values[0]}
		}
	}
	return replies
}
