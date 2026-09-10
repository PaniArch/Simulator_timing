package memsys

import (
	"fmt"
	"math"

	"vortex.local/simulator/emu/warp"
)

// System connects the independently clocked L1 and LSU components. The backend
// arbitrates the concatenated I/D memory ports: this is the documented software
// external-memory boundary, not a model of the RTL socket interconnect.
// Owners retain all backing bytes; System never services an ISA request itself.
type System struct {
	instruction, data *Cache
	backend           *Backend
	local             *LocalMemory
	split             *SIMDSplit
	coalescer         *Coalescer
	globalAdapter     *GlobalAdapter
	localAdapter      *LocalAdapter
	progress          map[Identity]uint8
	released          map[Identity]bool
	started           bool
	cycle             uint64
	fault             error
}

type SystemInput struct {
	Fetch                                 WordOffer
	Memory                                SIMDOffer
	FetchReady, MemoryReady               bool // consumers of returned data
	InstructionFlush, DataFlush           FlushOffer
	InstructionFlushReady, DataFlushReady bool
}

type SystemEdge struct {
	FetchAccepted, MemoryAccepted               bool
	Fetch                                       WordReply
	Memory                                      SIMDReply
	MemoryDelivered                             bool
	Stores                                      []SIMDResponse // actual application events, without response backpressure
	Complete                                    []Identity     // all lanes delivered/applied and child references released
	InstructionFlushAccepted, DataFlushAccepted bool
	InstructionFlush, DataFlush                 FlushReply
	WritebackErrors                             []Response
}

func NewSystem(owner warp.AtomicMemoryService, localOwner LocalOwner, config Config) (*System, error) {
	s := &System{progress: make(map[Identity]uint8), released: make(map[Identity]bool)}
	var err error
	if s.instruction, err = NewCache(InstructionCache); err != nil {
		return nil, err
	}
	if s.data, err = NewCache(DataCache); err != nil {
		return nil, err
	}
	if s.backend, err = New(owner, config, s.instruction.Spec().MemoryPorts+s.data.Spec().MemoryPorts); err != nil {
		return nil, err
	}
	if s.local, err = NewLocalMemory(localOwner); err != nil {
		return nil, err
	}
	if s.split, err = NewSIMDSplit(); err != nil {
		return nil, err
	}
	if s.coalescer, err = NewCoalescer(); err != nil {
		return nil, err
	}
	if s.globalAdapter, err = NewGlobalAdapter(); err != nil {
		return nil, err
	}
	if s.localAdapter, err = NewLocalAdapter(); err != nil {
		return nil, err
	}
	return s, nil
}

// Responses returns detached, pre-edge data. It neither clocks nor reads owners.
// Evaluate the consumer's ready signals from this view, then call Step once.
func (s *System) Responses() (WordReply, SIMDReply) {
	return s.instruction.Responses()[0], s.split.Response()
}

func (s *System) Drained() bool {
	return s.instruction.Drained() && s.data.Drained() && s.local.Drained() &&
		s.split.Drained() && s.coalescer.Drained() && s.globalAdapter.Drained() &&
		s.localAdapter.Drained() && s.backend.Outstanding() == 0
}

func (s *System) HasResidency(kernel, cta uint64) bool {
	return s.instruction.HasResidency(kernel, cta) || s.data.HasResidency(kernel, cta) ||
		s.local.HasResidency(kernel, cta) || s.split.HasResidency(kernel, cta) ||
		s.coalescer.HasResidency(kernel, cta) || s.globalAdapter.HasResidency(kernel, cta) ||
		s.localAdapter.HasResidency(kernel, cta)
}

// Step consumes old views and advances each component exactly once. A protocol
// error is terminal: earlier components may already have advanced or applied a
// store. Retrying the edge would replay side effects. Architectural data errors
// instead travel in responses/receipts and do not poison this clock interface.
func (s *System) Step(cycle uint64, in SystemInput) (out SystemEdge, err error) {
	if s.fault != nil {
		return out, s.fault
	}
	defer func() {
		if err != nil {
			s.fault = err
		}
	}()
	if s.started && (s.cycle == math.MaxUint64 || cycle != s.cycle+1) {
		return out, fmt.Errorf("noncontiguous memory system cycle")
	}
	s.started, s.cycle = true, cycle
	paths, batch := s.split.Outputs(), s.coalescer.Output()
	gp, err := s.globalAdapter.Preview(s.data.Responses())
	if err != nil {
		return out, err
	}
	gread, err := s.coalescer.Preview(gp)
	if err != nil {
		return out, err
	}
	lread, err := s.localAdapter.Preview(s.local.Responses())
	if err != nil {
		return out, err
	}
	reads := [2]SIMDReply{gread, lread}
	pathReady := s.split.ReadReady(reads, in.MemoryReady)
	gr, err := s.globalAdapter.ReadReady(s.data.Responses(), pathReady[0])
	if err != nil {
		return out, err
	}
	lr, err := s.localAdapter.ReadReady(s.local.Responses(), pathReady[1])
	if err != nil {
		return out, err
	}
	preview, err := s.backend.PreviewResponses(cycle)
	if err != nil {
		return out, err
	}
	var offers []Offer
	var ready []bool
	offset := 0
	for _, c := range []*Cache{s.instruction, s.data} {
		offers = append(offers, c.MemoryOffers()...)
		for p := 0; p < c.Spec().MemoryPorts; p++ {
			r := preview[offset+p]
			ready = append(ready, r.Valid && c.MemoryResponseReady(p, r.Response))
		}
		offset += c.Spec().MemoryPorts
	}
	memory, err := s.backend.Step(cycle, offers, ready)
	if err != nil {
		return out, err
	}
	n := s.instruction.Spec().MemoryPorts
	ie, err := s.instruction.Step(cycle, CacheInput{Requests: []WordOffer{in.Fetch}, ResponseReady: []bool{in.FetchReady}, Memory: Edge{memory.Accepted[:n], memory.Replies[:n]}, Flush: in.InstructionFlush, FlushReady: in.InstructionFlushReady})
	if err != nil {
		return out, err
	}
	de, err := s.data.Step(cycle, CacheInput{Requests: s.globalAdapter.Offers(batch), ResponseReady: gr, Memory: Edge{memory.Accepted[n:], memory.Replies[n:]}, Flush: in.DataFlush, FlushReady: in.DataFlushReady})
	if err != nil {
		return out, err
	}
	me, err := s.local.Step(cycle, s.localAdapter.Offers(), lr)
	if err != nil {
		return out, err
	}
	ge, err := s.globalAdapter.Step(cycle, GlobalAdapterInput{Batch: batch, Cache: de, ResponseReady: pathReady[0]})
	if err != nil {
		return out, err
	}
	le, err := s.localAdapter.Step(cycle, LocalAdapterInput{Request: paths[1], Memory: me, ResponseReady: pathReady[1]})
	if err != nil {
		return out, err
	}
	var mask uint8
	for p, accepted := range de.Accepted {
		if accepted {
			mask |= 1 << p
		}
	}
	ce, err := s.coalescer.Step(cycle, CoalescerInput{Request: paths[0], OutputReady: ge.Accepted, OutputAcceptedMask: mask, Response: ge.Response, ResponseReady: pathReady[0]})
	if err != nil {
		return out, err
	}
	si := SplitInput{Request: in.Memory, PathReady: [2]bool{ce.Accepted, le.Accepted}, Reads: reads, Stores: [2][]SIMDResponse{ge.Stores, le.Stores}, ResponseReady: in.MemoryReady}
	for _, p := range ge.Progress {
		if !s.released[p.Identity] {
			p.Mask &^= s.progress[p.Identity]
			if p.Mask != 0 {
				si.Progress[0] = append(si.Progress[0], p)
				s.progress[p.Identity] |= p.Mask
			}
		}
	}
	if ce.Accepted {
		s.released[paths[0].Request.Identity] = true
	}
	si.Progress[1] = le.Progress
	se, err := s.split.Step(cycle, si)
	if err != nil {
		return out, err
	}
	for _, id := range se.Complete {
		delete(s.progress, id)
		delete(s.released, id)
	}
	return SystemEdge{
		FetchAccepted: ie.Accepted[0], MemoryAccepted: se.Accepted,
		Fetch: ie.Replies[0], Memory: se.Response, MemoryDelivered: se.ResponseDelivered,
		Stores: se.Stores, Complete: se.Complete,
		InstructionFlushAccepted: ie.FlushAccepted, DataFlushAccepted: de.FlushAccepted,
		InstructionFlush: ie.Flush, DataFlush: de.Flush,
		WritebackErrors: append(ie.WritebackErrors, de.WritebackErrors...),
	}, nil
}

// IsLocal classifies the frozen architectural LMEM aperture loaded from IR.
func (s *System) IsLocal(address uint32) bool {
	return uint64(address) >= uint64(s.local.base) && uint64(address) < uint64(s.local.base)+uint64(s.local.size)
}

// NextCycle exposes clock alignment for software recovery without advancing any
// component. Protocol faults are terminal: some components may have advanced
// before validation failed, so no common next edge can safely be resumed.
func (s *System) NextCycle() (uint64, error) {
	if s.fault != nil {
		return 0, s.fault
	}
	if !s.started {
		return 0, nil
	}
	if s.cycle == math.MaxUint64 {
		return 0, fmt.Errorf("memory cycle exhausted")
	}
	return s.cycle + 1, nil
}
