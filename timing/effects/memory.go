package effects

import (
	"errors"
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

type memoryPart struct {
	delivery          *state.EffectDelivery
	writes            isa.LaneMask
	accepted, written bool
}
type memoryInstruction struct {
	snapshot  state.WarpSnapshot
	requests  []isa.MemoryRequest
	sent      bool
	sentCycle uint64
	served    uint8
	parts     map[uint8]*memoryPart
	control   *state.EffectDelivery
}

// BindMemory binds the original byte owner; the adapter never allocates a
// backing store. Service timing and backpressure are supplied by the caller.
func (a *Adapter) BindMemory(memory warp.MemoryService) error {
	if memory == nil || a.current != nil {
		return fmt.Errorf("bind a non-nil memory owner while idle")
	}
	a.memory = memory
	return nil
}
func (a *Adapter) observeMemory(cycle uint64, r model.CoreReport) error {
	i, m := a.current, a.current.memory
	if r.MemoryAccepted {
		if m.sent {
			return fmt.Errorf("duplicate memory request acceptance")
		}
		m.sent = true
		m.sentCycle = cycle
	}
	response := r.MemoryResponse
	if response.Valid && r.MemoryResponseReady {
		if response.ID != i.token.ID || response.Epoch != i.token.Epoch || response.Warp != i.token.Warp || response.Uop != 0 {
			return fmt.Errorf("foreign memory response")
		}
		p := m.parts[response.Mask]
		if p == nil || p.accepted {
			return fmt.Errorf("response not serviced or already accepted")
		}
		p.accepted = true
	}
	if r.Writeback.Valid {
		if i.token.Path == model.LOAD || i.token.Path == model.FENCE {
			p := m.parts[r.Writeback.Token.Mask]
			if p == nil || !p.accepted || p.written {
				return fmt.Errorf("WB before response or repeated response fragment")
			}
			if err := p.delivery.Deliver(state.WritebackEvent, p.writes, nil); err != nil {
				return err
			}
			p.written = true
		} else {
			if err := i.delivery.Deliver(state.WritebackEvent, 0, nil); err != nil {
				return err
			}
		}
	}
	if r.PendingRelease.Valid {
		if i.pending {
			return fmt.Errorf("duplicate memory pending release")
		}
		if i.token.Path == model.STORE {
			if !i.delivery.Delivered(state.WritebackEvent) {
				return fmt.Errorf("store pending before WB")
			}
		} else {
			coverage := uint8(0)
			for mask, p := range m.parts {
				if p.written {
					coverage |= mask
				}
			}
			if coverage != i.token.Mask {
				return fmt.Errorf("load pending before full WB coverage")
			}
		}
		i.pending = true
	}
	return a.finishMemoryControl()
}
func (a *Adapter) finishMemoryControl() error {
	i := a.current
	m := i.memory
	if i.pending && m.served == i.token.Mask && m.control != nil && !m.control.Delivered(state.ControlEvent) {
		return m.control.Deliver(state.ControlEvent, 0, nil)
	}
	return nil
}
func (a *Adapter) memoryFinished() bool {
	i := a.current
	m := i.memory
	return m.sent && m.served == i.token.Mask && m.control != nil && m.control.Delivered(state.ControlEvent)
}

// Service is the explicit byte-owner visibility event, after a previous-edge
// scheduler-port acceptance. It samples load bytes or atomically writes the
// entire SIMT store once. A returned response must be held until the core accepts
// it; retries use that value, not a second Service call. Load masks are disjoint.
// Errors are fatal model/service errors until Reset; fault routing is separate.
func (a *Adapter) Service(cycle uint64, token model.Token, mask uint8) (response model.Response, err error) {
	if a.failed {
		return response, fmt.Errorf("effect adapter requires reset")
	}
	defer func() {
		if err != nil {
			a.failed = true
		}
	}()
	if err = a.check(model.Signal{Valid: true, Token: token}); err != nil {
		return response, err
	}
	i := a.current
	if i.packed != nil {
		return a.servicePacked(cycle, token, mask)
	}
	m := i.memory
	if m == nil || !m.sent || cycle <= m.sentCycle || cycle <= a.lastCycle {
		return response, fmt.Errorf("memory service before accepted request or past edge")
	}
	if mask == 0 || mask & ^i.token.Mask != 0 || mask&m.served != 0 {
		return response, fmt.Errorf("invalid or repeated service coverage")
	}
	if i.token.Path == model.STORE && mask != i.token.Mask {
		return response, fmt.Errorf("SIMT store requires one atomic full-mask service")
	}
	responses := []isa.MemoryResponse{}
	var serviceErr error
	for _, request := range m.requests {
		if mask&(1<<request.Lane) == 0 {
			continue
		}
		r := isa.MemoryResponse{Request: request}
		if request.Width != 1 && request.Width != 2 && request.Width != 4 {
			return response, fmt.Errorf("unsupported memory width")
		}
		bytes := make([]byte, request.Width)
		if readErr := a.memory.Read(request.Address, bytes); readErr != nil {
			serviceErr = errors.Join(serviceErr, readErr)
			r.Fault, r.Reason = isa.FaultLoadAccess, isa.FaultReasonMemoryService
			if request.Kind == isa.MemoryStore {
				r.Fault = isa.FaultStoreAccess
			}
		}
		if request.Kind == isa.MemoryLoad {
			for j, b := range bytes {
				r.Data |= uint32(b) << (8 * j)
			}
		}
		responses = append(responses, r)
	}
	var completed isa.InstructionEffects
	if i.token.Path == model.FENCE {
		// FENCE's evaluator already supplies the ordering request and sequential PC.
		completed, err = i.capture.Evaluate()
	} else {
		completed, err = m.snapshot.CompleteMemory(i.decoded, isa.LaneMask(mask), responses)
	}
	if err != nil {
		return response, err
	}
	if len(completed.Faults) != 0 {
		return response, architecturalFault(i.token.PC, completed.Faults, serviceErr)
	}
	// Validate detached completion before any byte mutation, using current owner.
	partEffects := isa.InstructionEffects{RegisterWrites: completed.RegisterWrites}
	delivery, err := a.owner.NewEffectDelivery(partEffects)
	if err != nil {
		return response, err
	}
	if m.control == nil {
		m.control, err = a.owner.NewEffectDelivery(isa.InstructionEffects{Control: completed.Control})
		if err != nil {
			return response, err
		}
	}
	if i.token.Path == model.STORE {
		err = i.delivery.Deliver(state.MemoryEvent, 0, func(isa.InstructionEffects) error { return writeRequests(a.memory, m.requests) })
		if err != nil {
			return response, err
		}
	} else if i.token.Path == model.FENCE {
		// The service caller authorizes completion only once its previous tail drained.
		if a.external == nil {
			return response, fmt.Errorf("FENCE ordering owner required")
		}
		if err = i.delivery.Deliver(state.MemoryEvent, 0, a.external); err != nil {
			return response, err
		}
	}
	m.served |= mask
	p := &memoryPart{delivery: delivery}
	for _, w := range completed.RegisterWrites {
		p.writes |= w.Mask
	}
	m.parts[mask] = p
	if err = a.finishMemoryControl(); err != nil {
		return response, err
	}
	if i.token.Path != model.STORE {
		response = model.Response{Valid: true, ID: token.ID, Epoch: token.Epoch, Warp: token.Warp, Uop: token.Uop, Mask: mask}
	}
	return response, nil
}
func writeRequests(memory warp.MemoryService, requests []isa.MemoryRequest) error {
	addresses := []uint32{}
	sources := [][]byte{}
	for _, r := range requests {
		offset := r.Address - r.AlignedAddress
		if r.Kind != isa.MemoryStore || offset+uint32(r.Width) > 4 || r.ByteMask != uint8(((uint16(1)<<r.Width)-1)<<offset) {
			return fmt.Errorf("invalid store request byte mask")
		}
		data := make([]byte, r.Width)
		value := r.StoreData >> (8 * offset)
		for j := range data {
			data[j] = byte(value >> (8 * j))
		}
		addresses = append(addresses, r.Address)
		sources = append(sources, data)
	}
	if len(addresses) == 1 {
		return memory.Write(addresses[0], sources[0])
	}
	atomic, ok := memory.(warp.AtomicMemoryService)
	if !ok {
		return fmt.Errorf("memory owner lacks atomic SIMT store support")
	}
	return atomic.WriteBatch(addresses, sources)
}

func (a *Adapter) responsePart(response model.Response) (*memoryPart, error) {
	if a.current == nil {
		return nil, fmt.Errorf("memory response without instruction")
	}
	i := a.current
	w := i.token
	if response.ID != w.ID || response.Epoch != w.Epoch || response.Warp != w.Warp || response.Uop >= w.Uops {
		return nil, fmt.Errorf("foreign memory response")
	}
	var part *memoryPart
	if i.memory != nil {
		part = i.memory.parts[response.Mask]
	}
	if i.packed != nil {
		if u := i.packed.uops[response.Uop]; u != nil {
			part = u.parts[response.Mask]
		}
	}
	if part == nil {
		return nil, fmt.Errorf("unserviced memory response")
	}
	return part, nil
}
