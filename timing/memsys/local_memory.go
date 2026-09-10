package memsys

import (
	"fmt"
	"math"

	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing"
)

// LocalOwner resolves the original CTA byte owner at the service edge. It must
// check the complete residency identity before returning a warp-bound owner.
// In particular, a reused Warp slot must never resolve to the new CTA's bytes.
type LocalOwner func(Identity) (warp.AtomicMemoryService, error)
type localRequest struct {
	request WordRequest
	port    int
}
type localResponse struct {
	response WordResponse
	port     int
}
type LocalMemory struct {
	base, size  uint32
	owner       LocalOwner
	requests    [4]cacheQueue[localRequest]
	responses   [4]cacheQueue[WordResponse]
	pipe        [4]*localResponse
	lastWrite   [4]bool
	lastAddress [4]uint32
	held        [4]WordOffer
	last        [4]uint64
	used        [4]bool
	cycle       uint64
	started     bool
}

func NewLocalMemory(owner LocalOwner) (*LocalMemory, error) {
	if owner == nil {
		return nil, fmt.Errorf("local memory requires original owner resolver")
	}
	for _, p := range []struct {
		id, field string
		value     int
	}{{"cfg-memory", "local_banks", 4}, {"cfg-memory", "lsu_word_bytes", 4}} {
		v, err := timing.Number("config", p.id, "values", p.field)
		if err != nil {
			return nil, err
		}
		if v != p.value {
			return nil, fmt.Errorf("unsupported local memory shape")
		}
	}
	req, err := timing.Buffer("b-local-request-xbar")
	if err != nil {
		return nil, err
	}
	rsp, err := timing.Buffer("b-local-result")
	if err != nil {
		return nil, err
	}
	for _, id := range []string{"b-local-request-xbar", "b-local-result"} {
		a, err := timing.Arbiter(id)
		if err != nil {
			return nil, err
		}
		if a.Inputs != 4 || a.Policy != "P" || a.Sticky {
			return nil, fmt.Errorf("unsupported local crossbar")
		}
	}
	for _, field := range []string{"SRAM_OUT_REG", "TAG_PIPE_DEPTH"} {
		n, err := timing.Number("boundaries", "b-local-sram-tag", "rtl_parameters", field)
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, fmt.Errorf("unsupported local SRAM output")
		}
	}
	if req.Size != 2 || rsp.Size != 2 {
		return nil, fmt.Errorf("unsupported local memory buffers")
	}
	base, err := timing.Number("config", "cfg-memory", "values", "local_base_address")
	if err != nil {
		return nil, err
	}
	size, err := timing.Number("config", "cfg-memory", "values", "local_size_bytes")
	if err != nil {
		return nil, err
	}
	m := &LocalMemory{owner: owner, base: uint32(base), size: uint32(size)}
	for i := 0; i < 4; i++ {
		m.requests[i].spec = req
		m.responses[i].spec = rsp
	}
	return m, nil
}
func (m *LocalMemory) Responses() []WordReply {
	r := make([]WordReply, 4)
	for p, q := range m.responses {
		if len(q.values) > 0 {
			r[p] = WordReply{Valid: true, Response: q.values[0]}
		}
	}
	return r
}
func (m *LocalMemory) HasResidency(kernel, cta uint64) bool {
	has := func(id Identity) bool { return id.Kernel == kernel && id.CTA == cta }
	for i := 0; i < 4; i++ {
		for _, r := range m.requests[i].values {
			if has(r.request.Identity) {
				return true
			}
		}
		for _, r := range m.responses[i].values {
			if has(r.Identity) {
				return true
			}
		}
		if m.pipe[i] != nil && has(m.pipe[i].response.Identity) {
			return true
		}
	}
	return false
}
func (m *LocalMemory) Drained() bool {
	for i := 0; i < 4; i++ {
		if len(m.requests[i].values) > 0 || len(m.responses[i].values) > 0 || m.pipe[i] != nil {
			return false
		}
	}
	return true
}

// Step accepts word requests into the request xbar, then services only OLD
// bank queue heads. A bank has one SRAM port and one tag/data output register.
// Stores bypass response backpressure; reads after last-edge same-address
// stores insert the RTL is_rdw_hazard bubble. There is no precomputed due time.
func (m *LocalMemory) Step(cycle uint64, offers []WordOffer, ready []bool) (CacheEdge, error) {
	if len(offers) != 4 || len(ready) != 4 {
		return CacheEdge{}, fmt.Errorf("local memory port count")
	}
	if m.started && (m.cycle == math.MaxUint64 || cycle != m.cycle+1) {
		return CacheEdge{}, fmt.Errorf("noncontiguous local cycle")
	}
	for p, o := range offers {
		if m.held[p].Valid && m.held[p] != o {
			return CacheEdge{}, fmt.Errorf("changed stalled local word")
		}
		if o.Valid && (o.Request.Address%4 != 0 || o.Request.Address < m.base || uint64(o.Request.Address)+4 > uint64(m.base)+uint64(m.size) || o.Request.ByteEnable&^uint8(15) != 0 || o.Request.NonCacheable || (m.used[p] && o.Request.Identity.Transaction <= m.last[p])) {
			return CacheEdge{}, fmt.Errorf("invalid local word or stale transaction")
		}
	}
	e := CacheEdge{Accepted: make([]bool, 4), Replies: m.Responses()}
	var bankPop [4]bool
	var rspPush [4]*WordResponse
	// Response xbar: each destination independently grants the lowest bank.
	for p := 0; p < 4; p++ {
		e.Replies[p].Delivered = e.Replies[p].Valid && ready[p]
		if !m.responses[p].ready(false) {
			continue
		}
		for b := 0; b < 4; b++ {
			if m.pipe[b] != nil && m.pipe[b].port == p {
				v := m.pipe[b].response
				rspPush[p] = &v
				bankPop[b] = true
				break
			}
		}
	}
	var reqPop [4]bool
	var reqPush [4]*localRequest
	var nextPipe [4]*localResponse
	for b := 0; b < 4; b++ {
		if len(m.requests[b].values) == 0 {
			continue
		}
		r := m.requests[b].values[0]
		w := r.request
		hazard := m.lastWrite[b] && !w.Write && m.lastAddress[b] == w.Address
		if hazard || (!w.Write && m.pipe[b] != nil && !bankPop[b]) {
			continue
		}
		reqPop[b] = true
		owner, err := m.owner(w.Identity)
		if err == nil && owner == nil {
			err = fmt.Errorf("local resolver returned nil owner")
		}
		if w.Write {
			if err == nil {
				var addresses []uint32
				var data [][]byte
				for k := 0; k < 4; {
					if w.ByteEnable&(1<<k) == 0 {
						k++
						continue
					}
					start := k
					for k < 4 && w.ByteEnable&(1<<k) != 0 {
						k++
					}
					addresses = append(addresses, w.Address+uint32(start))
					data = append(data, append([]byte(nil), w.Data[start:k]...))
				}
				err = owner.WriteBatch(addresses, data)
			}
			e.Stores = append(e.Stores, StoreReceipt{Port: r.port, Request: w, Err: err})
		} else {
			reply := WordResponse{Identity: w.Identity, Tag: w.Tag, ByteEnable: 15}
			if err == nil {
				err = owner.Read(w.Address, reply.Data[:4])
			}
			reply.Err = err
			if err != nil {
				reply.Data = [8]byte{}
			}
			nextPipe[b] = &localResponse{reply, r.port}
		}
	}
	// Request xbar priority is explicit, not a map or bank visitation winner.
	for b := 0; b < 4; b++ {
		if m.requests[b].ready(false) {
			for p, o := range offers {
				if o.Valid && int(o.Request.Address/4%4) == b {
					v := localRequest{o.Request, p}
					reqPush[b] = &v
					e.Accepted[p] = true
					break
				}
			}
		}
	}
	for b := 0; b < 4; b++ {
		m.lastWrite[b] = false
		if reqPop[b] {
			w := m.requests[b].values[0].request
			m.lastWrite[b] = w.Write
			m.lastAddress[b] = w.Address
		}
		m.requests[b].update(reqPop[b], reqPush[b])
		if bankPop[b] {
			m.pipe[b] = nil
		}
		if nextPipe[b] != nil {
			m.pipe[b] = nextPipe[b]
		}
		m.responses[b].update(e.Replies[b].Delivered, rspPush[b])
		m.held[b] = WordOffer{}
		if offers[b].Valid && !e.Accepted[b] {
			m.held[b] = offers[b]
		}
		if e.Accepted[b] {
			m.last[b] = offers[b].Request.Identity.Transaction
			m.used[b] = true
		}
	}
	m.cycle, m.started = cycle, true
	return e, nil
}
