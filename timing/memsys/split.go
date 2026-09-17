package memsys

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"vortex.local/simulator/timing"
)

const (
	GlobalPath = 0
	LocalPath  = 1
)

type SIMDReply struct {
	Valid    bool
	Response SIMDResponse
}
type SplitInput struct {
	// Partial downstream work may start before a coalescer releases its input.
	// These identity/tag/mask events report that progress without popping the child.
	Progress  [2][]SIMDResponse
	Request   SIMDOffer
	PathReady [2]bool
	Reads     [2]SIMDReply
	// Software application events; stores do not compete for a load response slot.
	Stores        [2][]SIMDResponse
	ResponseReady bool
}
type SplitEdge struct {
	Accepted          bool
	SubsetAccepted    [2]bool
	Outputs           [2]SIMDOffer
	OutputDelivered   [2]bool
	ReadReady         [2]bool
	Response          SIMDReply
	ResponseDelivered bool
	// Events must be consumed by the caller. Complete follows all lane delivery
	// (loads) or application (stores), not merely input acceptance.
	Stores   []SIMDResponse
	Complete []Identity
}
type splitRecord struct {
	request                  SIMDRequest
	masks                    [2]uint8
	sent, arrived, completed uint8
	queued, released         uint8
	accepted                 bool
}
type SIMDSplit struct {
	requests      [2]cacheQueue[SIMDRequest]
	responses     cacheQueue[SIMDResponse]
	rr            int
	held          SIMDOffer
	heldReads     [2]SIMDReply
	submitted     uint8
	records       map[Identity]*splitRecord
	cycle, last   uint64
	started, used bool
}

func NewSIMDSplit() (*SIMDSplit, error) {
	s := &SIMDSplit{records: make(map[Identity]*splitRecord)}
	for p, id := range []string{"b-split-global_out_buf", "b-split-local_out_buf"} {
		b, err := timing.Buffer(id)
		if err != nil {
			return nil, err
		}
		if b.Size != 1 {
			return nil, fmt.Errorf("unsupported split request buffer")
		}
		s.requests[p].spec = b
	}
	b, err := timing.Buffer("b-split-rsp_out_buf")
	if err != nil {
		return nil, err
	}
	if b.Size != 1 {
		return nil, fmt.Errorf("unsupported split response buffer")
	}
	s.responses.spec = b
	a, err := timing.Arbiter("b-split-rsp_out_buf")
	if err != nil {
		return nil, err
	}
	if a.Inputs != 2 || a.Policy != "R" || a.Sticky {
		return nil, fmt.Errorf("unsupported split arbiter")
	}
	return s, nil
}
func splitMasks(r SIMDRequest) [2]uint8 {
	var masks [2]uint8
	for i, l := range r.Lanes {
		if r.Mask&(1<<i) != 0 {
			p := GlobalPath
			if l.Local {
				p = LocalPath
			}
			masks[p] |= 1 << i
		}
	}
	return masks
}
func (s *SIMDSplit) Outputs() [2]SIMDOffer {
	var outputs [2]SIMDOffer
	for p, q := range s.requests {
		if len(q.values) > 0 {
			outputs[p] = SIMDOffer{true, q.values[0]}
		}
	}
	return outputs
}

// Response is the detached old-edge output buffer view.
func (s *SIMDSplit) Response() SIMDReply {
	if len(s.responses.values) == 0 {
		return SIMDReply{}
	}
	return SIMDReply{true, s.responses.values[0]}
}
func (s *SIMDSplit) ReadReady(reads [2]SIMDReply, responseReady bool) [2]bool {
	var ready [2]bool
	selected := pick(s.rr, []bool{reads[0].Valid, reads[1].Valid})
	if selected >= 0 && s.responses.ready(len(s.responses.values) > 0 && responseReady) {
		ready[selected] = true
	}
	return ready
}
func (s *SIMDSplit) HasResidency(kernel, cta uint64) bool {
	for id := range s.records {
		if id.Kernel == kernel && id.CTA == cta {
			return true
		}
	}
	return false
}
func (s *SIMDSplit) Drained() bool {
	return len(s.records) == 0 && !s.held.Valid && len(s.responses.values) == 0 && len(s.requests[0].values) == 0 && len(s.requests[1].values) == 0
}
func (s *SIMDSplit) validate(cycle uint64, in SplitInput) error {
	if s.started && (s.cycle == math.MaxUint64 || cycle != s.cycle+1) {
		return fmt.Errorf("noncontiguous split cycle")
	}
	if s.held.Valid && s.held != in.Request {
		return fmt.Errorf("changed stalled split request")
	}
	if in.Request.Valid {
		r := in.Request.Request
		if err := validateSIMD(r); err != nil {
			return err
		}
		if s.used && r.Identity.Transaction <= s.last {
			return fmt.Errorf("stale split transaction")
		}
		// Reject unresolved global stores before either mixed subset can take effect.
		masks := splitMasks(r)
		if masks[GlobalPath] != 0 {
			g := r
			g.Mask = masks[GlobalPath]
			if err := validateGlobal(g); err != nil {
				return err
			}
		}
	}
	progress := make(map[Identity]uint8)
	for p := 0; p < 2; p++ {
		for _, r := range in.Progress[p] {
			rec, ok := s.records[r.Identity]
			if !ok || r.Tag != rec.request.Tag || r.Mask == 0 || r.Mask&^rec.masks[p] != 0 || r.Mask&^rec.queued != 0 || r.Mask&(rec.sent|progress[r.Identity]) != 0 {
				return fmt.Errorf("invalid split partial progress")
			}
			progress[r.Identity] |= r.Mask
		}
	}
	seen := make(map[Identity]uint8)
	check := func(p int, r SIMDResponse, write bool) error {
		rec, ok := s.records[r.Identity]
		if !ok || rec.request.Tag != r.Tag || rec.request.Write != write || r.Mask == 0 || r.Mask&^rec.masks[p] != 0 || r.Mask&^(rec.sent|progress[r.Identity]) != 0 || r.Mask&rec.arrived != 0 || r.Mask&seen[r.Identity] != 0 {
			return fmt.Errorf("foreign, premature or duplicate split completion")
		}
		seen[r.Identity] |= r.Mask
		return nil
	}
	for p := 0; p < 2; p++ {
		if s.heldReads[p].Valid && !in.Reads[p].Valid {
			return fmt.Errorf("dropped stalled path valid")
		}
		// The zero-buffer global pack can reselect another batch of the same
		// parent while stalled. Only the same fragment must retain its lanes;
		// the adapter separately enforces stability of every producer port,
		// including a previously selected port hidden by higher priority input.
		if s.heldReads[p].Valid && in.Reads[p].Valid && s.heldReads[p].Response.Identity == in.Reads[p].Response.Identity && s.heldReads[p].Response.Batch == in.Reads[p].Response.Batch {
			old, now := s.heldReads[p].Response, in.Reads[p].Response
			if old.Tag != now.Tag || old.Mask&^now.Mask != 0 {
				return fmt.Errorf("lost stalled path lanes")
			}
			for lane := 0; lane < 4; lane++ {
				if old.Mask&(1<<lane) != 0 && (old.Data[lane] != now.Data[lane] || !reflect.DeepEqual(old.Errors[lane], now.Errors[lane])) {
					return fmt.Errorf("changed stalled path response")
				}
			}
		}
		if in.Reads[p].Valid {
			if err := check(p, in.Reads[p].Response, false); err != nil {
				return err
			}
		}
		for _, r := range in.Stores[p] {
			if err := check(p, r, true); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *SIMDSplit) Step(cycle uint64, in SplitInput) (SplitEdge, error) {
	if err := s.validate(cycle, in); err != nil {
		return SplitEdge{}, err
	}
	for p := 0; p < 2; p++ {
		for _, r := range in.Progress[p] {
			s.records[r.Identity].sent |= r.Mask
		}
	}
	e := SplitEdge{Outputs: s.Outputs(), ReadReady: s.ReadReady(in.Reads, in.ResponseReady)}
	if len(s.responses.values) > 0 {
		e.Response = SIMDReply{true, s.responses.values[0]}
		e.ResponseDelivered = in.ResponseReady
	}
	if e.ResponseDelivered {
		r := e.Response.Response
		s.records[r.Identity].completed |= r.Mask
	}
	var rspPush *SIMDResponse
	for p := 0; p < 2; p++ {
		e.OutputDelivered[p] = e.Outputs[p].Valid && in.PathReady[p]
		if e.OutputDelivered[p] {
			r := e.Outputs[p].Request
			s.records[r.Identity].sent |= r.Mask
			s.records[r.Identity].released |= r.Mask
		}
		if in.Reads[p].Valid && e.ReadReady[p] {
			r := in.Reads[p].Response
			s.records[r.Identity].arrived |= r.Mask
			rspPush = &r
			s.rr = 1 - p
		}
		for _, r := range in.Stores[p] {
			rec := s.records[r.Identity]
			rec.arrived |= r.Mask
			rec.completed |= r.Mask
			e.Stores = append(e.Stores, r)
		}
	}
	var pushes [2]*SIMDRequest
	if in.Request.Valid {
		r := in.Request.Request
		masks := splitMasks(r)
		for p := 0; p < 2; p++ {
			// submitted tracks buffer acceptance, not downstream ready. It prevents
			// repeated child valid while the other child remains backpressured.
			if masks[p] != 0 && s.submitted&(1<<p) == 0 && s.requests[p].ready(e.OutputDelivered[p]) {
				rec, ok := s.records[r.Identity]
				if !ok {
					rec = &splitRecord{request: r, masks: masks}
					s.records[r.Identity] = rec
				}
				rec.queued |= masks[p]
				part := r
				part.Mask = masks[p]
				pushes[p] = &part
				s.submitted |= 1 << p
				e.SubsetAccepted[p] = true
			}
		}
		e.Accepted = (masks[0] == 0 || s.submitted&1 != 0) && (masks[1] == 0 || s.submitted&2 != 0)
		if e.Accepted {
			s.records[r.Identity].accepted = true
			s.submitted = 0
			s.last = r.Identity.Transaction
			s.used = true
		}
	}
	for p := 0; p < 2; p++ {
		s.requests[p].update(e.OutputDelivered[p], pushes[p])
		s.heldReads[p] = SIMDReply{}
		if in.Reads[p].Valid && !e.ReadReady[p] {
			s.heldReads[p] = in.Reads[p]
		}
	}
	s.responses.update(e.ResponseDelivered, rspPush)
	// The ledger is software bookkeeping, not an extra hardware queue/credit.
	// Sorting affects notification presentation only, never data or arbitration.
	for id, rec := range s.records {
		if rec.accepted && rec.completed == rec.request.Mask && rec.released == rec.request.Mask {
			e.Complete = append(e.Complete, id)
			delete(s.records, id)
		}
	}
	sort.Slice(e.Complete, func(i, j int) bool { return e.Complete[i].Transaction < e.Complete[j].Transaction })
	s.held = SIMDOffer{}
	if in.Request.Valid && !e.Accepted {
		s.held = in.Request
	}
	s.cycle, s.started = cycle, true
	return e, nil
}
