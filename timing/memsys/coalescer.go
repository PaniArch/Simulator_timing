package memsys

import (
	"errors"
	"fmt"
	"math"
	"reflect"

	"vortex.local/simulator/timing"
)

// LaneRequest is an aligned LSU word, not a byte-addressed ISA access. The
// functional issuer shifts subword bytes/enables into this four-byte word.
type LaneRequest struct {
	Address             uint32
	ByteEnable          uint8
	Data                [4]byte
	Local, NonCacheable bool
}
type SIMDRequest struct {
	Identity       Identity
	Tag            uint64
	Mask           uint8
	Write, NoMerge bool
	Flush          bool
	Lanes          [4]LaneRequest
}
type SIMDOffer struct {
	Valid   bool
	Request SIMDRequest
}
type SIMDResponse struct {
	Identity Identity
	// Batch preserves global load fragment provenance across expansion. A zero
	// generation denotes an unbatched response (local path or software event).
	// It is not the parent transaction or an additional hardware tag/credit.
	Batch  BatchID
	Tag    uint64
	Mask   uint8
	Data   [4][4]byte
	Errors [4]error
}

// BatchID extends the RTL slot with a software generation against stale replies.
type BatchID struct {
	Slot       int
	Generation uint64
}
type CoalescedBatch struct {
	ID             BatchID
	Identity       Identity
	Tag            uint64
	Mask, LaneMask uint8
	Write          bool
	Words          [2]WordRequest
}
type BatchOffer struct {
	Valid bool
	Batch CoalescedBatch
}
type BatchResponse struct {
	ID     BatchID
	Mask   uint8
	Data   [2][8]byte
	Errors [2]error
}
type BatchReply struct {
	Valid    bool
	Response BatchResponse
}
type CoalescerInput struct {
	// Per-channel unpack progress before the entire batch is accepted.
	OutputAcceptedMask uint8
	Request            SIMDOffer
	OutputReady        bool
	Response           BatchReply
	ResponseReady      bool
}
type CoalescerEdge struct {
	Accepted                         bool
	Output                           BatchOffer
	OutputDelivered                  bool
	Response                         SIMDResponse
	ResponseValid, ResponseDelivered bool
}
type coalescerSlot struct {
	valid            bool
	sent             uint8
	generation       uint64
	request          SIMDRequest
	lanes, remaining uint8
}
type Coalescer struct {
	acquire       int
	heldResponse  BatchReply
	slots         []coalescerSlot
	held          SIMDOffer
	output        BatchOffer
	send          bool
	remaining     uint8
	prepared      CoalescedBatch
	cycle, last   uint64
	started, used bool
}

var ErrUnresolvedStoreOrder = errors.New("UNRESOLVED: cross-group overlapping stores have different values")

func NewCoalescer() (*Coalescer, error) {
	fields := []string{"input_lanes", "input_data_bytes", "output_channels", "output_data_bytes"}
	expected := []int{4, 4, 2, 8}
	for i, f := range fields {
		n, err := timing.Number("resources", "res-coalescer-tags", f)
		if err != nil {
			return nil, err
		}
		if n != expected[i] {
			return nil, fmt.Errorf("unsupported coalescer %s=%d", f, n)
		}
	}
	n, err := timing.Number("resources", "res-coalescer-tags", "capacity")
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, fmt.Errorf("invalid coalescer capacity")
	}
	return &Coalescer{slots: make([]coalescerSlot, n), remaining: 15}, nil
}

// makeBatch follows g_seed_gen and g_data_merged: lowest remaining lane seed,
// address comparison within its DATA_RATIO group, ascending-j byte assignments.
func makeBatch(r SIMDRequest, remaining uint8) CoalescedBatch {
	b := CoalescedBatch{Identity: r.Identity, Tag: r.Tag, Write: r.Write}
	for group := 0; group < 2; group++ {
		seed := -1
		for j := 0; j < 2; j++ {
			lane := group*2 + j
			if r.Mask&remaining&(1<<lane) != 0 {
				seed = lane
				break
			}
		}
		if seed < 0 {
			continue
		}
		base := r.Lanes[seed].Address &^ uint32(7)
		b.Mask |= 1 << group
		w := WordRequest{Identity: r.Identity, Tag: r.Tag, Address: base, Write: r.Write, NonCacheable: r.Lanes[seed].NonCacheable, Flush: r.Flush}
		for j := 0; j < 2; j++ {
			lane := group*2 + j
			l := r.Lanes[lane]
			if r.Mask&(1<<lane) == 0 || l.Address&^uint32(7) != base || (r.NoMerge && lane != seed) {
				continue
			}
			b.LaneMask |= 1 << lane
			offset := int(l.Address % 8)
			for k := 0; k < 4; k++ {
				if l.ByteEnable&(1<<k) != 0 {
					w.ByteEnable |= 1 << (offset + k)
					w.Data[offset+k] = l.Data[k]
				}
			}
		}
		b.Words[group] = w
	}
	return b
}
func validateSIMD(r SIMDRequest) error {
	if r.Mask == 0 || r.Mask&^uint8(15) != 0 {
		return fmt.Errorf("invalid SIMD mask")
	}
	for i, l := range r.Lanes {
		if r.Mask&(1<<i) != 0 && (l.Address%4 != 0 || l.ByteEnable&^uint8(15) != 0) {
			return fmt.Errorf("invalid LSU word lane %d", i)
		}
	}
	return nil
}
func validateGlobal(r SIMDRequest) error {
	if err := validateSIMD(r); err != nil {
		return err
	}
	for i, l := range r.Lanes {
		if r.Mask&(1<<i) != 0 && l.Local {
			return fmt.Errorf("local lane at global coalescer")
		}
	}
	// Determine overlap AFTER the RTL within-group merge, not by picking a
	// global lane winner. Different attributes cannot be merged silently either.
	var batches []CoalescedBatch
	remaining := r.Mask
	for remaining != 0 {
		b := makeBatch(r, remaining)
		for group := 0; group < 2; group++ {
			for j := 0; j < 2; j++ {
				lane := group*2 + j
				if b.LaneMask&(1<<lane) != 0 && r.Lanes[lane].NonCacheable != b.Words[group].NonCacheable {
					return fmt.Errorf("UNRESOLVED: mixed attributes within coalesced word")
				}
			}
		}
		batches = append(batches, b)
		remaining &^= b.LaneMask
	}
	if !r.Write {
		return nil
	}
	for _, a := range batches {
		for _, b := range batches {
			if a.Mask&1 == 0 || b.Mask&2 == 0 {
				continue
			}
			x, y := a.Words[0], b.Words[1]
			if x.Address != y.Address {
				continue
			}
			overlap := x.ByteEnable & y.ByteEnable
			for k := 0; k < 8; k++ {
				if overlap&(1<<k) != 0 && x.Data[k] != y.Data[k] {
					return ErrUnresolvedStoreOrder
				}
			}
		}
	}
	return nil
}
func (c *Coalescer) free() int {
	for i, s := range c.slots {
		if !s.valid {
			return i
		}
	}
	return -1
}
func (c *Coalescer) Output() BatchOffer         { return c.output }
func (c *Coalescer) Empty(inputValid bool) bool { return !c.send && !c.output.Valid && !inputValid }
func (c *Coalescer) Drained() bool {
	if !c.Empty(c.held.Valid) {
		return false
	}
	for _, s := range c.slots {
		if s.valid {
			return false
		}
	}
	return true
}
func (c *Coalescer) HasResidency(kernel, cta uint64) bool {
	if c.held.Valid && c.held.Request.Identity.Kernel == kernel && c.held.Request.Identity.CTA == cta {
		return true
	}
	if c.output.Valid && c.output.Batch.Identity.Kernel == kernel && c.output.Batch.Identity.CTA == cta {
		return true
	}
	for _, s := range c.slots {
		if s.valid && s.request.Identity.Kernel == kernel && s.request.Identity.CTA == cta {
			return true
		}
	}
	return false
}
func (c *Coalescer) Step(cycle uint64, in CoalescerInput) (CoalescerEdge, error) {
	if c.started && (c.cycle == math.MaxUint64 || cycle != c.cycle+1) {
		return CoalescerEdge{}, fmt.Errorf("noncontiguous coalescer cycle")
	}
	if c.held.Valid && c.held != in.Request {
		return CoalescerEdge{}, fmt.Errorf("changed stalled SIMD request")
	}
	if in.Request.Valid {
		if err := validateGlobal(in.Request.Request); err != nil {
			return CoalescerEdge{}, err
		}
		if c.used && in.Request.Request.Identity.Transaction <= c.last {
			return CoalescerEdge{}, fmt.Errorf("stale SIMD transaction")
		}
	}
	if in.OutputAcceptedMask != 0 {
		if !c.output.Valid || in.OutputAcceptedMask&^c.output.Batch.Mask != 0 {
			return CoalescerEdge{}, fmt.Errorf("invalid coalescer channel progress")
		}
		if !c.output.Batch.Write && c.slots[c.output.Batch.ID.Slot].sent&in.OutputAcceptedMask != 0 {
			return CoalescerEdge{}, fmt.Errorf("duplicate coalescer channel progress")
		}
	}
	// Validate incoming fragments before changing any state. Read slots become
	// eligible per channel after unpack progress, or for all channels when
	// the registered batch crosses the whole-vector output.
	// The zero-buffer priority pack may change its selected tag or add a
	// matching channel while stalled. Already visible bytes of the same tag
	// must remain stable; individual producer ports are checked by the adapter.
	if c.heldResponse.Valid && !in.Response.Valid {
		return CoalescerEdge{}, fmt.Errorf("dropped stalled batch valid")
	}
	if c.heldResponse.Valid && in.Response.Valid && c.heldResponse.Response.ID == in.Response.Response.ID {
		old, now := c.heldResponse.Response, in.Response.Response
		if old.Mask&^now.Mask != 0 {
			return CoalescerEdge{}, fmt.Errorf("lost stalled batch channel")
		}
		for p := 0; p < 2; p++ {
			if old.Mask&(1<<p) != 0 && (old.Data[p] != now.Data[p] || !reflect.DeepEqual(old.Errors[p], now.Errors[p])) {
				return CoalescerEdge{}, fmt.Errorf("changed stalled batch data")
			}
		}
	}
	if in.Response.Valid {
		r := in.Response.Response
		if r.ID.Slot < 0 || r.ID.Slot >= len(c.slots) {
			return CoalescerEdge{}, fmt.Errorf("invalid coalescer response slot")
		}
		s := c.slots[r.ID.Slot]
		if !s.valid || r.Mask&^s.sent != 0 || s.generation != r.ID.Generation || r.Mask == 0 || r.Mask&^s.remaining != 0 {
			return CoalescerEdge{}, fmt.Errorf("stale, duplicate or unsent coalescer response")
		}
	}
	full := c.free() < 0 // WAIT tests old full, even if a response releases this edge.
	free := c.acquire
	if full {
		free = -1
	}
	if c.send && (free < 0 || (!c.prepared.Write && c.slots[free].generation == math.MaxUint64)) {
		return CoalescerEdge{}, fmt.Errorf("coalescer slot unavailable in SEND")
	}
	allocated := c.send && !c.prepared.Write
	released := false
	e := CoalescerEdge{Output: c.output, OutputDelivered: c.output.Valid && in.OutputReady}
	if in.Response.Valid {
		r := in.Response.Response
		s := &c.slots[r.ID.Slot]
		e.ResponseValid = true
		e.ResponseDelivered = in.ResponseReady
		e.Response = SIMDResponse{Identity: s.request.Identity, Batch: r.ID, Tag: s.request.Tag}
		for lane := 0; lane < 4; lane++ {
			group := lane / 2
			if s.lanes&(1<<lane) != 0 && r.Mask&(1<<group) != 0 {
				e.Response.Mask |= 1 << lane
				offset := int(s.request.Lanes[lane].Address % 8)
				copy(e.Response.Data[lane][:], r.Data[group][offset:offset+4])
				e.Response.Errors[lane] = r.Errors[group]
				if r.Errors[group] != nil {
					e.Response.Data[lane] = [4]byte{}
				}
			}
		}
		if e.ResponseDelivered {
			s.remaining &^= r.Mask
			if s.remaining == 0 {
				s.valid = false
				released = true
			}
		}
	}
	if in.OutputAcceptedMask != 0 && !c.output.Batch.Write {
		c.slots[c.output.Batch.ID.Slot].sent |= in.OutputAcceptedMask
	}
	if e.OutputDelivered {
		if !c.output.Batch.Write {
			c.slots[c.output.Batch.ID.Slot].sent = c.output.Batch.Mask
		}
		c.output = BatchOffer{}
	}
	if c.send {
		b := c.prepared
		if !b.Write {
			s := &c.slots[free]
			s.generation++
			b.ID = BatchID{free, s.generation}
			*s = coalescerSlot{valid: true, generation: s.generation, request: in.Request.Request, lanes: b.LaneMask, remaining: b.Mask}
		} else {
			b.ID = BatchID{Slot: free}
		} // RTL stores carry waddr but allocate no slot.
		c.output = BatchOffer{true, b}
		c.remaining &^= b.LaneMask
		e.Accepted = in.Request.Request.Mask&c.remaining == 0
		if e.Accepted {
			c.remaining = 15
			c.last = in.Request.Request.Identity.Transaction
			c.used = true
		}
		c.send = false
	} else if in.Request.Valid && !c.output.Valid && free >= 0 {
		c.prepared = makeBatch(in.Request.Request, c.remaining)
		c.send = true
	}
	c.held = SIMDOffer{}
	if in.Request.Valid && !e.Accepted {
		c.held = in.Request
	}
	if allocated || (released && full) {
		if next := c.free(); next >= 0 {
			c.acquire = next
		}
	}
	c.heldResponse = BatchReply{}
	if in.Response.Valid && !in.ResponseReady {
		c.heldResponse = in.Response
	}
	c.cycle, c.started = cycle, true
	return e, nil
}

// Preview expands a prospective channel response without consuming its slot.
// This combinational view allows the split response arbiter to compute ready.
func (c *Coalescer) Preview(in BatchReply) (SIMDReply, error) {
	if !in.Valid {
		return SIMDReply{}, nil
	}
	r := in.Response
	if r.ID.Slot < 0 || r.ID.Slot >= len(c.slots) {
		return SIMDReply{}, fmt.Errorf("invalid coalescer response slot")
	}
	s := c.slots[r.ID.Slot]
	if !s.valid || s.generation != r.ID.Generation || r.Mask == 0 || r.Mask&^s.sent != 0 || r.Mask&^s.remaining != 0 {
		return SIMDReply{}, fmt.Errorf("stale or unsent coalescer preview")
	}
	out := SIMDReply{true, SIMDResponse{Identity: s.request.Identity, Batch: r.ID, Tag: s.request.Tag}}
	for lane := 0; lane < 4; lane++ {
		p := lane / 2
		if s.lanes&(1<<lane) != 0 && r.Mask&(1<<p) != 0 {
			out.Response.Mask |= 1 << lane
			offset := int(s.request.Lanes[lane].Address % 8)
			copy(out.Response.Data[lane][:], r.Data[p][offset:offset+4])
			out.Response.Errors[lane] = r.Errors[p]
			if r.Errors[p] != nil {
				out.Response.Data[lane] = [4]byte{}
			}
		}
	}
	return out, nil
}
