package memsys

import "fmt"

// Generation is a software guard against a late backend response after slot
// reuse. Slot is the RTL MSHR index; it alone is not a residency identity.
type mshrHandle struct {
	slot       int
	generation uint64
}
type mshrEntry struct {
	valid, finalized bool
	generation       uint64
	line             uint32
	request          WordRequest
	port             int
	next             *mshrHandle
}
type mshrAllocation struct {
	request WordRequest
	port    int
}
type mshrFinalization struct {
	handle   mshrHandle
	release  bool
	previous *mshrHandle
}
type mshrEvents struct {
	allocate *mshrAllocation
	finalize *mshrFinalization
	fill     *mshrHandle
	dequeue  bool
}
type mshrAllocationResult struct {
	handle   mshrHandle
	previous *mshrHandle
}
type cacheMSHR struct {
	entries []mshrEntry
	ready   bool
	free    int
	head    *mshrHandle
}

func newCacheMSHR(size int) *cacheMSHR { return &cacheMSHR{entries: make([]mshrEntry, size)} }
func (m *cacheMSHR) occupancy() int {
	n := 0
	for _, e := range m.entries {
		if e.valid {
			n++
		}
	}
	return n
}
func (m *cacheMSHR) entry(h mshrHandle) (mshrEntry, error) {
	if h.slot < 0 || h.slot >= len(m.entries) {
		return mshrEntry{}, fmt.Errorf("invalid MSHR slot")
	}
	e := m.entries[h.slot]
	if !e.valid || e.generation != h.generation {
		return mshrEntry{}, fmt.Errorf("stale MSHR handle")
	}
	return e, nil
}
func (m *cacheMSHR) replay() (mshrHandle, mshrEntry, bool) {
	if m.head == nil {
		return mshrHandle{}, mshrEntry{}, false
	}
	e, err := m.entry(*m.head)
	if err != nil {
		panic(err)
	}
	return *m.head, e, true
}
func handleCopy(h *mshrHandle) *mshrHandle {
	if h == nil {
		return nil
	}
	v := *h
	return &v
}

// step mirrors the combinational next-table update in VX_cache_mshr for plain
// reads/writes. Allocation uses a registered free index. The lowest-index free
// slot/tail priority comes from VX_priority_encoder's REVERSE=0, not map order.
// Errors are checked before changing any table, so late responses cannot release
// the entry belonging to a replacement generation.
func (m *cacheMSHR) step(ev mshrEvents) (*mshrAllocationResult, error) {
	var finalEntry mshrEntry
	if ev.finalize != nil {
		var err error
		finalEntry, err = m.entry(ev.finalize.handle)
		if err != nil {
			return nil, err
		}
		if finalEntry.finalized {
			return nil, fmt.Errorf("MSHR finalized twice")
		}
		if !ev.finalize.release && ev.finalize.previous != nil {
			prev, err := m.entry(*ev.finalize.previous)
			if err != nil {
				return nil, err
			}
			if prev.line != finalEntry.line || *ev.finalize.previous == ev.finalize.handle || prev.next != nil {
				return nil, fmt.Errorf("invalid MSHR predecessor")
			}
		}
	}
	if ev.fill != nil {
		e, err := m.entry(*ev.fill)
		if err != nil {
			return nil, err
		}
		if !e.finalized || m.head != nil {
			return nil, fmt.Errorf("fill before finalize or during active replay")
		}
	}
	if ev.dequeue {
		if m.head == nil {
			return nil, fmt.Errorf("MSHR dequeue without head")
		}
		e, err := m.entry(*m.head)
		if err != nil {
			return nil, err
		}
		if !e.finalized {
			return nil, fmt.Errorf("MSHR replay before finalize")
		}
		if ev.finalize != nil && ev.finalize.handle == *m.head {
			return nil, fmt.Errorf("duplicate MSHR release")
		}
	}
	if ev.allocate != nil {
		if !m.ready || m.entries[m.free].valid {
			return nil, fmt.Errorf("MSHR allocation without registered space")
		}
		if m.entries[m.free].generation == ^uint64(0) {
			return nil, fmt.Errorf("MSHR generation exhausted")
		}
	}
	next := append([]mshrEntry(nil), m.entries...)
	head := handleCopy(m.head)
	if ev.fill != nil {
		head = handleCopy(ev.fill)
	}
	if ev.dequeue {
		old := m.entries[m.head.slot]
		next[m.head.slot].valid = false
		head = handleCopy(old.next)
		// Tail finalize and dequeue can occur together. The new child is already
		// allocated, but next_index is not visible in the old table yet.
		if head == nil && ev.finalize != nil && !ev.finalize.release && ev.finalize.previous != nil && *ev.finalize.previous == *m.head {
			head = handleCopy(&ev.finalize.handle)
		}
	}
	if ev.finalize != nil {
		f := ev.finalize
		next[f.handle.slot].finalized = true
		if f.release {
			next[f.handle.slot].valid = false
		} else if f.previous != nil {
			next[f.previous.slot].next = handleCopy(&f.handle)
		}
	}
	var result *mshrAllocationResult
	if ev.allocate != nil {
		a := ev.allocate
		line := a.request.Address &^ uint32(SectorBytes-1)
		// Match old valid_table with next_table_x after the finalize link, excluding
		// the slot currently dequeuing. A released hit is not linked by the bank.
		var previous *mshrHandle
		for slot, e := range m.entries {
			if !e.valid || e.line != line || next[slot].next != nil {
				continue
			}
			if ev.dequeue && slot == m.head.slot {
				continue
			}
			h := mshrHandle{slot, e.generation}
			previous = &h
			break
		}
		slot := m.free
		h := mshrHandle{slot, m.entries[slot].generation + 1}
		next[slot] = mshrEntry{valid: true, generation: h.generation, line: line, request: a.request, port: a.port}
		result = &mshrAllocationResult{handle: h, previous: previous}
	}
	free, ready := 0, false
	for slot, e := range next {
		if !e.valid {
			free, ready = slot, true
			break
		}
	}
	m.entries, m.head, m.free, m.ready = next, head, free, ready
	return result, nil
}
