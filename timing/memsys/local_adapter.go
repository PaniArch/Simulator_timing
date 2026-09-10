package memsys

import (
	"fmt"
	"math"
	"reflect"
	"vortex.local/simulator/timing"
)

type localWordRecord struct {
	request    SIMDRequest
	words      [4]WordRequest
	sent, done uint8
	released   bool
}
type LocalAdapter struct {
	queues                [4]cacheQueue[WordRequest]
	records               map[uint64]*localWordRecord
	sequence, cycle, last uint64
	started, used         bool
	held                  SIMDOffer
	delivered             uint8
	heldReads             [4]WordReply
}
type LocalAdapterInput struct {
	Request       SIMDOffer
	Memory        CacheEdge
	ResponseReady bool
}
type LocalAdapterEdge struct {
	Accepted          bool
	Response          SIMDReply
	ResponseDelivered bool
	Progress, Stores  []SIMDResponse
}

func NewLocalAdapter() (*LocalAdapter, error) {
	q, err := timing.Buffer("b-local-adapter-request")
	if err != nil {
		return nil, err
	}
	rsp, err := timing.Buffer("b-local-adapter-response")
	if err != nil {
		return nil, err
	}
	arb, err := timing.Arbiter("b-local-adapter-response")
	if err != nil {
		return nil, err
	}
	if q.Size != 2 || rsp.Size != 0 || arb.Inputs != 4 || arb.Policy != "P" || arb.Sticky {
		return nil, fmt.Errorf("unsupported local adapter shape")
	}
	a := &LocalAdapter{sequence: 1, records: make(map[uint64]*localWordRecord)}
	for p := range a.queues {
		a.queues[p].spec = q
	}
	return a, nil
}
func (a *LocalAdapter) Offers() []WordOffer {
	out := make([]WordOffer, 4)
	for p, q := range a.queues {
		if len(q.values) > 0 {
			out[p] = WordOffer{true, q.values[0]}
		}
	}
	return out
}
func (a *LocalAdapter) Preview(reads []WordReply) (SIMDReply, error) {
	if len(reads) != 4 {
		return SIMDReply{}, fmt.Errorf("local adapter port count")
	}
	var result SIMDReply
	var selected uint64
	for p, r := range reads {
		if r.Valid {
			id := r.Response.Identity.Transaction
			rec, ok := a.records[id]
			if !ok || rec.request.Write || rec.sent&(1<<p) == 0 || rec.done&(1<<p) != 0 || r.Response.Identity != rec.words[p].Identity || r.Response.Tag != rec.words[p].Tag || r.Response.ByteEnable != 15 {
				return SIMDReply{}, fmt.Errorf("foreign or duplicate local response")
			}
			if !result.Valid {
				selected = id
				result = SIMDReply{true, SIMDResponse{Identity: rec.request.Identity, Tag: rec.request.Tag}}
			}
			if id == selected {
				result.Response.Mask |= 1 << p
				copy(result.Response.Data[p][:], r.Response.Data[:4])
				result.Response.Errors[p] = r.Response.Err
			}
		}
	}
	return result, nil
}
func (a *LocalAdapter) ReadReady(reads []WordReply, ready bool) ([]bool, error) {
	r, err := a.Preview(reads)
	if err != nil {
		return nil, err
	}
	out := make([]bool, 4)
	for p := range out {
		out[p] = ready && r.Valid && r.Response.Mask&(1<<p) != 0
	}
	return out, nil
}
func (a *LocalAdapter) HasResidency(kernel, cta uint64) bool {
	for _, r := range a.records {
		if r.request.Identity.Kernel == kernel && r.request.Identity.CTA == cta {
			return true
		}
	}
	return a.held.Valid && a.held.Request.Identity.Kernel == kernel && a.held.Request.Identity.CTA == cta
}
func (a *LocalAdapter) Drained() bool { return len(a.records) == 0 && !a.held.Valid }
func (a *LocalAdapter) Step(cycle uint64, in LocalAdapterInput) (LocalAdapterEdge, error) {
	if a.started && (a.cycle == math.MaxUint64 || cycle != a.cycle+1) {
		return LocalAdapterEdge{}, fmt.Errorf("noncontiguous local adapter cycle")
	}
	if len(in.Memory.Accepted) != 4 || len(in.Memory.Replies) != 4 {
		return LocalAdapterEdge{}, fmt.Errorf("local adapter port count")
	}
	if a.held.Valid && a.held != in.Request {
		return LocalAdapterEdge{}, fmt.Errorf("changed stalled local SIMD request")
	}
	if in.Request.Valid {
		r := in.Request.Request
		if err := validateSIMD(r); err != nil {
			return LocalAdapterEdge{}, err
		}
		if a.sequence == math.MaxUint64 || (a.used && r.Identity.Transaction <= a.last) {
			return LocalAdapterEdge{}, fmt.Errorf("stale local input transaction")
		}
		for p, l := range r.Lanes {
			if r.Mask&(1<<p) != 0 && (!l.Local || l.NonCacheable) {
				return LocalAdapterEdge{}, fmt.Errorf("nonlocal lane at local adapter")
			}
		}
	}
	offers := a.Offers()
	for p, v := range in.Memory.Accepted {
		if v && !offers[p].Valid {
			return LocalAdapterEdge{}, fmt.Errorf("local acceptance without offer")
		}
	}
	response, err := a.Preview(in.Memory.Replies)
	if err != nil {
		return LocalAdapterEdge{}, err
	}
	for p, r := range in.Memory.Replies {
		held := r
		held.Delivered = false
		if a.heldReads[p].Valid && !reflect.DeepEqual(a.heldReads[p], held) {
			return LocalAdapterEdge{}, fmt.Errorf("changed stalled local response")
		}
		if r.Delivered != (r.Valid && response.Valid && response.Response.Mask&(1<<p) != 0 && in.ResponseReady) {
			return LocalAdapterEdge{}, fmt.Errorf("local response handshake mismatch")
		}
	}
	seen := make(map[uint64]uint8)
	for _, r := range in.Memory.Stores {
		rec, ok := a.records[r.Request.Identity.Transaction]
		if !ok || r.Port < 0 || r.Port >= 4 || !rec.request.Write || rec.words[r.Port] != r.Request || rec.sent&(1<<r.Port) == 0 || (rec.done|seen[r.Request.Identity.Transaction])&(1<<r.Port) != 0 {
			return LocalAdapterEdge{}, fmt.Errorf("foreign or duplicate local store receipt")
		}
		seen[r.Request.Identity.Transaction] |= 1 << r.Port
	}
	e := LocalAdapterEdge{Response: response, ResponseDelivered: response.Valid && in.ResponseReady}
	for p, r := range in.Memory.Replies {
		if r.Delivered {
			a.records[r.Response.Identity.Transaction].done |= 1 << p
		}
		a.heldReads[p] = WordReply{}
		if r.Valid && !r.Delivered {
			r.Delivered = false
			a.heldReads[p] = r
		}
	}
	for _, r := range in.Memory.Stores {
		rec := a.records[r.Request.Identity.Transaction]
		rec.done |= 1 << r.Port
		v := SIMDResponse{Identity: rec.request.Identity, Tag: rec.request.Tag, Mask: 1 << r.Port}
		v.Errors[r.Port] = r.Err
		e.Stores = append(e.Stores, v)
	}
	var pushes [4]*WordRequest
	if in.Request.Valid {
		r := in.Request.Request
		for p, l := range r.Lanes {
			if r.Mask&(1<<p) != 0 && a.delivered&(1<<p) == 0 && a.queues[p].ready(false) {
				rec, ok := a.records[a.sequence]
				if !ok {
					rec = &localWordRecord{request: r}
					a.records[a.sequence] = rec
				}
				w := WordRequest{Identity: r.Identity, Tag: a.sequence, Address: l.Address, Write: r.Write, Flush: r.Flush, ByteEnable: l.ByteEnable}
				w.Identity.Transaction = a.sequence
				copy(w.Data[:4], l.Data[:])
				rec.words[p] = w
				pushes[p] = &w
				a.delivered |= 1 << p
				e.Progress = append(e.Progress, SIMDResponse{Identity: r.Identity, Tag: r.Tag, Mask: 1 << p})
			}
		}
		e.Accepted = a.delivered&r.Mask == r.Mask
		if e.Accepted {
			a.records[a.sequence].released = true
			a.sequence++
			a.delivered = 0
			a.last = r.Identity.Transaction
			a.used = true
		}
	}
	for p := 0; p < 4; p++ {
		if in.Memory.Accepted[p] {
			w := offers[p].Request
			a.records[w.Identity.Transaction].sent |= 1 << p
		}
		a.queues[p].update(in.Memory.Accepted[p], pushes[p])
	}
	for id, rec := range a.records {
		if rec.released && rec.done == rec.request.Mask {
			delete(a.records, id)
		}
	}
	a.held = SIMDOffer{}
	if in.Request.Valid && !e.Accepted {
		a.held = in.Request
	}
	a.cycle, a.started = cycle, true
	return e, nil
}
