package runner

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

// MemorySystemOptions overrides the static default binding. Bind must return
// the current full residency identity; LocalOwner revalidates it at service.
// Neither callback may allocate replacement backing bytes.
type MemorySystemOptions struct {
	reuse      *runnerMemory // internal, transferred only after full drain
	Config     memsys.Config
	Backend    memsys.BackendFactory
	Bind       func(model.Token) memsys.Identity
	LocalOwner memsys.LocalOwner
}
type appliedStore struct {
	identity memsys.Identity
	token    model.Token
	result   effects.MemoryResult
}
type runnerMemory struct {
	system                        *memsys.System
	bind                          func(model.Token) memsys.Identity
	localOwner                    memsys.LocalOwner
	fetchSeq, dataSeq             uint64
	fetch                         memsys.WordOffer
	data                          memsys.SIMDOffer
	fetchTokens, dataTokens       map[memsys.Identity]model.Token
	stores                        []appliedStore
	cancelledFetch, cancelledData map[memsys.Identity]bool
	accepted                      map[memsys.Identity]bool
	complete                      []memsys.Identity
	fetchPop, dataPop             bool
}

func newRunnerMemory(owner warp.MemoryService, o *MemorySystemOptions) (*runnerMemory, error) {
	atomic, ok := owner.(warp.AtomicMemoryService)
	if !ok {
		return nil, fmt.Errorf("T12 backing requires atomic byte-enable writes")
	}
	if o.Bind == nil || o.LocalOwner == nil {
		return nil, fmt.Errorf("T12 requires explicit residency binding and local owner resolver")
	}
	if o.reuse != nil {
		return o.reuse, nil
	}
	m := &runnerMemory{localOwner: o.LocalOwner}
	s, err := memsys.NewSystemWithBackend(atomic, func(id memsys.Identity) (warp.AtomicMemoryService, error) { return m.localOwner(id) }, o.Config, o.Backend)
	if err != nil {
		return nil, err
	}
	*m = runnerMemory{localOwner: o.LocalOwner, system: s, bind: o.Bind, cancelledFetch: make(map[memsys.Identity]bool), cancelledData: make(map[memsys.Identity]bool), accepted: make(map[memsys.Identity]bool), fetchTokens: make(map[memsys.Identity]model.Token), dataTokens: make(map[memsys.Identity]model.Token)}
	return m, nil
}
func (m *runnerMemory) identity(t model.Token, seq uint64) memsys.Identity {
	id := m.bind(t)
	id.Token = t.ID
	id.Epoch = t.Epoch
	id.Warp = uint32(t.Warp)
	id.Subrequest = uint64(t.Uop)
	id.Transaction = seq
	return id
}
func (m *runnerMemory) receive(r *MultiRunner, cycle uint64) error {
	m.fetchPop, m.dataPop = false, false
	var pending []appliedStore
	for _, s := range m.stores {
		if m.cancelledData[s.identity] {
			continue
		}
		if !m.accepted[s.identity] {
			pending = append(pending, s)
			continue
		}
		if _, err := r.effects.AcceptMemoryResult(cycle, s.token, s.result); err != nil {
			return err
		}
	}
	m.stores = pending
	for _, id := range m.complete {
		delete(m.accepted, id)
		delete(m.cancelledData, id)
	}
	m.complete = nil
	f, d := m.system.Responses()
	if f.Valid && m.cancelledFetch[f.Response.Identity] {
		m.fetchPop = true
		delete(m.fetchTokens, f.Response.Identity)
		delete(m.cancelledFetch, f.Response.Identity)
	} else if f.Valid && !r.fetchResponse.Valid {
		t, ok := m.fetchTokens[f.Response.Identity]
		if !ok {
			return fmt.Errorf("unknown cache fetch identity")
		}
		if f.Response.Err != nil {
			return &warp.Fault{Kind: warp.FaultInstructionAccess, PC: t.PC, Cause: f.Response.Err}
		}
		r.fetchResponse = model.Response{Valid: true, ID: t.ID, Epoch: t.Epoch, Warp: t.Warp, Mask: t.Mask, Word: binary.LittleEndian.Uint32(f.Response.Data[:4])}
	}
	if d.Valid && m.cancelledData[d.Response.Identity] {
		m.dataPop = true
	} else if d.Valid && m.accepted[d.Response.Identity] {
		t, ok := m.dataTokens[d.Response.Identity]
		if !ok {
			return fmt.Errorf("unknown LSU response identity")
		}
		// Combinational view of the existing RTL split response register, NOT
		// another elastic holding slot. Dequeue and functional receipt occur
		// only when the LSU's actual ready accepts this fragment in step.
		r.response = model.Response{Valid: true, ID: t.ID, Epoch: t.Epoch, Warp: t.Warp, Uop: t.Uop, Mask: d.Response.Mask}
	}
	return nil
}

func (m *runnerMemory) acceptResponse(r *MultiRunner, cycle uint64, report model.CoreReport) error {
	// VX_fetch directly wires icache rsp_ready to fetch_if.ready. The
	// runner's preview must not create an additional instruction holding slot.
	if r.fetchResponse.Valid && report.FetchResponseReady && !m.fetchPop {
		f, _ := m.system.Responses()
		t, ok := m.fetchTokens[f.Response.Identity]
		if !f.Valid || !ok || t.ID != r.fetchResponse.ID || t.Epoch != r.fetchResponse.Epoch {
			return fmt.Errorf("fetch response handshake without owned cache output")
		}
		m.fetchPop = true
		delete(m.fetchTokens, f.Response.Identity)
	}
	if report.MemoryResponse.Valid && report.MemoryResponseReady && !m.dataPop {
		_, d := m.system.Responses()
		t, ok := m.dataTokens[d.Response.Identity]
		if !d.Valid || !ok || !m.accepted[d.Response.Identity] {
			return fmt.Errorf("LSU response handshake without owned memory output")
		}
		var accepted model.Response
		var err error
		if t.Path == model.FENCE {
			var completionError error
			for lane, e := range d.Response.Errors {
				if d.Response.Mask&(1<<lane) != 0 {
					completionError = errors.Join(completionError, e)
				}
			}
			accepted, err = r.effects.AcceptOrderingFragment(cycle, t, d.Response.Mask, completionError)
		} else {
			accepted, err = r.effects.AcceptMemoryResult(cycle, t, effects.MemoryResult{Mask: d.Response.Mask, Data: d.Response.Data, Errors: d.Response.Errors})
		}
		if err != nil {
			return err
		}
		if accepted != report.MemoryResponse {
			return fmt.Errorf("functional memory receipt differs from accepted RTL response")
		}
		m.dataPop = true
	}
	return nil
}
func (m *runnerMemory) step(r *MultiRunner, cycle uint64, report model.CoreReport) (memsys.SystemEdge, error) {
	if err := m.acceptResponse(r, cycle, report); err != nil {
		return memsys.SystemEdge{}, err
	}
	admit := r.options.Ready == nil || r.options.Ready(cycle)
	if report.FetchRequest.Valid && !m.fetch.Valid && admit {
		m.fetchSeq++
		t := report.FetchRequest.Token
		id := m.identity(t, m.fetchSeq)
		m.fetch = memsys.WordOffer{Valid: true, Request: memsys.WordRequest{Identity: id, Tag: m.fetchSeq, Address: t.PC, ByteEnable: 15}}
		m.fetchTokens[id] = t
	}
	if report.MemoryRequest.Valid && !m.data.Valid && admit {
		t := report.MemoryRequest.Token
		requests, packed, err := r.effects.MemoryRequests(t)
		if err != nil {
			return memsys.SystemEdge{}, err
		}
		m.dataSeq++
		id := m.identity(t, m.dataSeq)
		req := memsys.SIMDRequest{Identity: id, Tag: m.dataSeq, Mask: t.Mask, Write: t.Path == model.STORE, Flush: t.Path == model.FENCE}
		// The frozen local aperture is validated by LocalMemory; classify the same
		// architectural address range here, with its base supplied by Timing IR.
		lane := func(n uint8, address uint32, mask uint8, data uint32) {
			v := memsys.LaneRequest{Address: address, ByteEnable: mask, Local: m.system.IsLocal(address), NonCacheable: m.system.IsIO(address)}
			binary.LittleEndian.PutUint32(v.Data[:], data)
			req.Lanes[n] = v
		}
		// VX_decode uses no source registers for FENCE; VX_opc_unit clears
		// the operand accumulator on each output fire. With offset/pack zero,
		// VX_lsu_slice sends address zero and default full word byte enable.
		if t.Path == model.FENCE {
			for p := 0; p < 4; p++ {
				if t.Mask&(1<<p) != 0 {
					lane(uint8(p), 0, 15, 0)
				}
			}
		}
		for _, q := range requests {
			lane(uint8(q.Lane), q.AlignedAddress, uint8(q.ByteMask), q.StoreData)
		}
		for _, q := range packed {
			lane(uint8(q.Lane), q.AlignedAddress, uint8(((1<<q.Width)-1)<<(q.Address-q.AlignedAddress)), 0)
		}
		m.data = memsys.SIMDOffer{Valid: true, Request: req}
		m.dataTokens[id] = t
	}
	e, err := m.system.Step(cycle, memsys.SystemInput{Trace: r.options.TraceMemory, Fetch: m.fetch, Memory: m.data, FetchReady: m.fetchPop, MemoryReady: m.dataPop, DataFlushReady: true, InstructionFlushReady: true})
	if err != nil {
		return e, err
	}
	if e.FetchAccepted {
		if m.cancelledFetch[m.fetch.Request.Identity] {
			e.FetchAccepted = false
		}
		m.fetch = memsys.WordOffer{}
	}
	if e.MemoryAccepted {
		m.accepted[m.data.Request.Identity] = true
		if m.cancelledData[m.data.Request.Identity] {
			// This handshake retires the old held transport, not the new
			// Core request potentially exposed after cancellation/restart.
			e.MemoryAccepted = false
		}
		m.data = memsys.SIMDOffer{}
	}
	for _, s := range e.Stores {
		t, ok := m.dataTokens[s.Identity]
		if !ok {
			return e, fmt.Errorf("unknown store identity")
		}
		m.stores = append(m.stores, appliedStore{s.Identity, t, effects.MemoryResult{Mask: s.Mask, Data: s.Data, Errors: s.Errors}})
	}
	m.complete = append(m.complete, e.Complete...)
	for _, id := range e.Complete {
		delete(m.dataTokens, id)
	}
	if len(e.WritebackErrors) > 0 {
		return e, fmt.Errorf("cache writeback failed: %w", e.WritebackErrors[0].Err)
	}
	return e, nil
}

// Cancellation removes architectural consumers, not exposed transport.
// Once offered, the entire SIMD request is irrevocable at this software boundary:
// keep remaining lanes stable until accepted and drain every result. No abort
// signal exists on the component interfaces, and applied bytes are not undone.
func (m *runnerMemory) cancel(scope model.Cancellation) {
	for id, t := range m.fetchTokens {
		if scope.Matches(t) {
			m.cancelledFetch[id] = true
		}
	}
	for id, t := range m.dataTokens {
		if scope.Matches(t) {
			m.cancelledData[id] = true
		}
	}
	for _, s := range m.stores {
		if scope.Matches(s.token) {
			m.cancelledData[s.identity] = true
		}
	}
}

// drained includes receipts waiting for receive on the next edge. In
// particular, a cancelled load can deliver its final response after the Core
// stopped; component drain alone must not strand its cancellation identity.
func (m *runnerMemory) drained() bool {
	return m.system.Drained() && !m.fetch.Valid && !m.data.Valid &&
		len(m.stores) == 0 && len(m.complete) == 0 &&
		len(m.fetchTokens) == 0 && len(m.dataTokens) == 0 &&
		len(m.cancelledFetch) == 0 && len(m.cancelledData) == 0 && len(m.accepted) == 0
}

func (m *runnerMemory) warpPending(w uint8) bool {
	if m.fetch.Valid && m.fetch.Request.Identity.Warp == uint32(w) {
		return true
	}
	if m.data.Valid && m.data.Request.Identity.Warp == uint32(w) {
		return true
	}
	for _, entries := range []map[memsys.Identity]bool{m.cancelledFetch, m.cancelledData, m.accepted} {
		for id := range entries {
			if id.Warp == uint32(w) {
				return true
			}
		}
	}

	for _, id := range m.complete {
		if id.Warp == uint32(w) {
			return true
		}
	}
	for _, t := range m.fetchTokens {
		if t.Warp == w {
			return true
		}
	}
	for _, t := range m.dataTokens {
		if t.Warp == w {
			return true
		}
	}
	for _, s := range m.stores {
		if s.token.Warp == w {
			return true
		}
	}
	return false
}

func (m *runnerMemory) services() []ServiceState {
	var out []ServiceState
	for _, t := range m.fetchTokens {
		out = append(out, ServiceState{Resource: "instruction-cache", Token: t})
	}
	for _, t := range m.dataTokens {
		out = append(out, ServiceState{Resource: "memory-system", Token: t})
	}
	for _, s := range m.stores {
		if _, ok := m.dataTokens[s.identity]; !ok {
			out = append(out, ServiceState{Resource: "memory-system", Token: s.token})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Token.ID != b.Token.ID {
			return a.Token.ID < b.Token.ID
		}
		if a.Token.Uop != b.Token.Uop {
			return a.Token.Uop < b.Token.Uop
		}
		return a.Resource < b.Resource
	})
	return out
}
