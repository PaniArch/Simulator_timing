package memsys

import (
	"fmt"
	"math"
	"reflect"

	"vortex.local/simulator/timing"
)

type globalWordRecord struct {
	batch      CoalescedBatch
	words      [2]WordRequest
	sent, done uint8
	released   bool
}
type GlobalAdapter struct {
	sequence, cycle uint64
	started         bool
	held            BatchOffer
	delivered       uint8
	records         map[uint64]*globalWordRecord
	heldReads       [2]WordReply
}

// Cache supplies word-sink acceptances plus actual Cache responses/receipts.
// System substitutes b-dflush input acceptance on port 0; a sent word may
// therefore still be buffered upstream of Cache until a later edge.
type GlobalAdapterInput struct {
	Batch         BatchOffer
	Cache         CacheEdge
	ResponseReady bool
}
type GlobalAdapterEdge struct {
	Accepted          bool
	Response          BatchReply
	ResponseDelivered bool
	Progress, Stores  []SIMDResponse
}

func NewGlobalAdapter() (*GlobalAdapter, error) {
	for _, id := range []string{"b-global-adapter-request", "b-global-adapter-response"} {
		b, err := timing.Buffer(id)
		if err != nil {
			return nil, err
		}
		if b.Size != 0 {
			return nil, fmt.Errorf("unsupported global adapter buffering")
		}
	}
	a, err := timing.Arbiter("b-global-adapter-response")
	if err != nil {
		return nil, err
	}
	if a.Inputs != 2 || a.Policy != "P" || a.Sticky {
		return nil, fmt.Errorf("unsupported global adapter pack arbitration")
	}
	return &GlobalAdapter{sequence: 1, records: make(map[uint64]*globalWordRecord)}, nil
}

// Offers implements the zero-buffer stream_unpack boundary. A new wire
// transaction names the whole batch, so both word ports carry the same tag.
// The caller holds Batch until Accepted, even if one port accepted earlier.
func (a *GlobalAdapter) Offers(b BatchOffer) []WordOffer {
	out := make([]WordOffer, 2)
	if !b.Valid {
		return out
	}
	for p := 0; p < 2; p++ {
		if b.Batch.Mask&(1<<p) != 0 && a.delivered&(1<<p) == 0 {
			w := b.Batch.Words[p]
			w.Identity = b.Batch.Identity
			w.Identity.Transaction = a.sequence
			w.Tag = a.sequence
			out[p] = WordOffer{true, w}
		}
	}
	return out
}

// Preview packs only the tag group selected by priority port 0 then port 1.
// Distinct batch tags remain independent responses, even for the same line.
func (a *GlobalAdapter) Preview(reads []WordReply) (BatchReply, error) {
	if len(reads) != 2 {
		return BatchReply{}, fmt.Errorf("global adapter read port count")
	}
	var result BatchReply
	var selected uint64
	for p, r := range reads {
		if !r.Valid {
			continue
		}
		id := r.Response.Identity.Transaction
		rec, ok := a.records[id]
		if !ok || rec.batch.Write || rec.sent&(1<<p) == 0 || rec.done&(1<<p) != 0 || r.Response.Identity != rec.words[p].Identity || r.Response.Tag != rec.words[p].Tag || r.Response.ByteEnable != 255 {
			return BatchReply{}, fmt.Errorf("foreign or duplicate global word response")
		}
		if !result.Valid {
			selected = id
			result = BatchReply{Valid: true, Response: BatchResponse{ID: rec.batch.ID}}
		}
		if id == selected {
			result.Response.Mask |= 1 << p
			result.Response.Data[p] = r.Response.Data
			result.Response.Errors[p] = r.Response.Err
		}
	}
	return result, nil
}
func (a *GlobalAdapter) ReadReady(reads []WordReply, ready bool) ([]bool, error) {
	b, err := a.Preview(reads)
	if err != nil {
		return nil, err
	}
	result := make([]bool, 2)
	if b.Valid && ready {
		for p := range result {
			result[p] = b.Response.Mask&(1<<p) != 0
		}
	}
	return result, nil
}
func batchLaneMask(b CoalescedBatch, p int) uint8 { return b.LaneMask & (3 << uint(2*p)) }
func (a *GlobalAdapter) HasResidency(kernel, cta uint64) bool {
	for _, r := range a.records {
		if r.batch.Identity.Kernel == kernel && r.batch.Identity.CTA == cta {
			return true
		}
	}
	return a.held.Valid && a.held.Batch.Identity.Kernel == kernel && a.held.Batch.Identity.CTA == cta
}
func (a *GlobalAdapter) Drained() bool { return !a.held.Valid && len(a.records) == 0 }
func (a *GlobalAdapter) Step(cycle uint64, in GlobalAdapterInput) (GlobalAdapterEdge, error) {
	if a.started && (a.cycle == math.MaxUint64 || cycle != a.cycle+1) {
		return GlobalAdapterEdge{}, fmt.Errorf("noncontiguous adapter cycle")
	}
	if len(in.Cache.Accepted) != 2 || len(in.Cache.Replies) != 2 {
		return GlobalAdapterEdge{}, fmt.Errorf("global adapter port count")
	}
	if a.held.Valid && a.held != in.Batch {
		return GlobalAdapterEdge{}, fmt.Errorf("changed stalled batch")
	}
	if in.Batch.Valid {
		b := in.Batch.Batch
		if a.sequence == math.MaxUint64 || b.Mask == 0 || b.Mask&^uint8(3) != 0 || b.LaneMask == 0 || b.LaneMask&^uint8(15) != 0 {
			return GlobalAdapterEdge{}, fmt.Errorf("invalid global batch")
		}
		for p, w := range b.Words {
			if (b.Mask&(1<<p) != 0) != (batchLaneMask(b, p) != 0) {
				return GlobalAdapterEdge{}, fmt.Errorf("inconsistent batch lane coverage")
			}
			if b.Mask&(1<<p) != 0 && (w.Address%8 != 0 || w.Write != b.Write || batchLaneMask(b, p) == 0) {
				return GlobalAdapterEdge{}, fmt.Errorf("invalid global batch word")
			}
		}
	}
	offers := a.Offers(in.Batch)
	for p, accepted := range in.Cache.Accepted {
		if accepted && !offers[p].Valid {
			return GlobalAdapterEdge{}, fmt.Errorf("cache accepted without adapter offer")
		}
	}
	response, err := a.Preview(in.Cache.Replies)
	if err != nil {
		return GlobalAdapterEdge{}, err
	}
	for p, r := range in.Cache.Replies {
		// Delivered is an edge result, not part of the held payload.
		held := r
		held.Delivered = false
		if a.heldReads[p].Valid && !reflect.DeepEqual(a.heldReads[p], held) {
			return GlobalAdapterEdge{}, fmt.Errorf("changed stalled cache response")
		}
		want := r.Valid && response.Valid && response.Response.Mask&(1<<p) != 0 && in.ResponseReady
		if r.Delivered != want {
			return GlobalAdapterEdge{}, fmt.Errorf("incorrect adapter response handshake")
		}
	}
	storeSeen := make(map[uint64]uint8)
	for _, r := range in.Cache.Stores {
		rec, ok := a.records[r.Request.Identity.Transaction]
		if !ok || r.Port < 0 || r.Port >= 2 || !rec.batch.Write || rec.words[r.Port] != r.Request || rec.sent&(1<<r.Port) == 0 || (rec.done|storeSeen[r.Request.Identity.Transaction])&(1<<r.Port) != 0 {
			return GlobalAdapterEdge{}, fmt.Errorf("foreign or duplicate adapter store receipt")
		}
		storeSeen[r.Request.Identity.Transaction] |= 1 << r.Port
	}
	e := GlobalAdapterEdge{Response: response, ResponseDelivered: response.Valid && in.ResponseReady}
	for p, r := range in.Cache.Replies {
		if r.Delivered {
			rec := a.records[r.Response.Identity.Transaction]
			rec.done |= 1 << p
		}
		a.heldReads[p] = WordReply{}
		if r.Valid && !r.Delivered {
			r.Delivered = false
			a.heldReads[p] = r
		}
	}
	for _, r := range in.Cache.Stores {
		rec := a.records[r.Request.Identity.Transaction]
		rec.done |= 1 << r.Port
		event := SIMDResponse{Identity: rec.batch.Identity, Tag: rec.batch.Tag, Mask: batchLaneMask(rec.batch, r.Port)}
		for lane := 0; lane < 4; lane++ {
			if event.Mask&(1<<lane) != 0 {
				event.Errors[lane] = r.Err
			}
		}
		e.Stores = append(e.Stores, event)
	}
	for p, accepted := range in.Cache.Accepted {
		if accepted {
			rec, ok := a.records[a.sequence]
			if !ok {
				rec = &globalWordRecord{batch: in.Batch.Batch}
				a.records[a.sequence] = rec
			}
			rec.words[p] = offers[p].Request
			rec.sent |= 1 << p
			a.delivered |= 1 << p
			e.Progress = append(e.Progress, SIMDResponse{Identity: rec.batch.Identity, Tag: rec.batch.Tag, Mask: batchLaneMask(rec.batch, p)})
		}
	}
	e.Accepted = in.Batch.Valid && (a.delivered&in.Batch.Batch.Mask) == in.Batch.Batch.Mask
	if e.Accepted {
		a.records[a.sequence].released = true
		a.sequence++
		a.delivered = 0
	}
	for id, rec := range a.records {
		if rec.released && rec.done == rec.batch.Mask {
			delete(a.records, id)
		}
	}
	a.held = BatchOffer{}
	if in.Batch.Valid && !e.Accepted {
		a.held = in.Batch
	}
	a.cycle, a.started = cycle, true
	return e, nil
}
