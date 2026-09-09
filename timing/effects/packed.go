package effects

import (
	"errors"
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

type packedUop struct {
	capture                 *state.OperandCapture
	snapshot                state.WarpSnapshot
	requests                []isa.PackedLoadRequest
	parts                   map[uint8]*memoryPart
	executed, sent, pending bool
	sentCycle               uint64
	served                  uint8
}
type packedInstruction struct {
	uops      map[uint8]*packedUop
	responses []isa.PackedLoadResponse
	control   *state.EffectDelivery
}

func (a *Adapter) observePacked(cycle uint64, r model.CoreReport, context state.ReadContext, snapshot state.WarpSnapshot) error {
	i := a.current
	p := i.packed
	var err error
	if r.Read.Valid {
		id := r.Read.Token.Uop
		u := p.uops[id]
		if u == nil {
			capture, err := a.newCapture(snapshot, context)
			if err != nil {
				return err
			}
			u = &packedUop{capture: capture, parts: map[uint8]*memoryPart{}}
			p.uops[id] = u
		}
		if err = u.capture.Read(snapshot, r.Read.Token.ReadMask); err != nil {
			return err
		}
		if r.Read.Token.LastRead && !u.capture.Complete() {
			return fmt.Errorf("incomplete packed operand read")
		}
	}
	for class, event := range r.Executed {
		if !event.Valid {
			continue
		}
		u := p.uops[event.Token.Uop]
		if class != 1 || u == nil || u.executed {
			return fmt.Errorf("duplicate or premature packed execution")
		}
		e, err := u.capture.EvaluateAt(snapshot, context)
		if err != nil {
			return err
		}
		if len(e.Faults) != 0 {
			return architecturalFault(i.token.PC, e.Faults, nil)
		}
		// Full evaluator owns address/stride semantics; select this decoded uop only.
		for _, request := range e.PackedLoads {
			if request.Element == event.Token.Uop {
				u.requests = append(u.requests, request)
			}
		}
		u.executed = true
		u.snapshot = snapshot
	}
	if r.MemoryAccepted {
		u := p.uops[r.MemoryRequest.Token.Uop]
		if u == nil || !u.executed || u.sent {
			return fmt.Errorf("invalid packed request acceptance")
		}
		u.sent = true
		u.sentCycle = cycle
	}
	if r.MemoryResponse.Valid && r.MemoryResponseReady {
		part, err := a.responsePart(r.MemoryResponse)
		if err != nil {
			return err
		}
		if part.accepted {
			return fmt.Errorf("repeated packed response")
		}
		part.accepted = true
	}
	if r.Writeback.Valid {
		u := p.uops[r.Writeback.Token.Uop]
		if u == nil {
			return fmt.Errorf("packed WB without execution")
		}
		part := u.parts[r.Writeback.Token.Mask]
		if part == nil || !part.accepted || part.written {
			return fmt.Errorf("premature or repeated packed WB")
		}
		if err = part.delivery.Deliver(state.WritebackEvent, part.writes, nil); err != nil {
			return err
		}
		part.written = true
	}
	if r.PendingRelease.Valid {
		u := p.uops[r.PendingRelease.Token.Uop]
		if u == nil || u.pending {
			return fmt.Errorf("invalid packed pending release")
		}
		coverage := uint8(0)
		for mask, part := range u.parts {
			if part.written {
				coverage |= mask
			}
		}
		if coverage != i.token.Mask {
			return fmt.Errorf("packed pending before full lane WB")
		}
		u.pending = true
	}
	all := len(p.uops) == int(i.token.Uops)
	for _, u := range p.uops {
		all = all && u.pending
	}
	i.pending = all
	return a.finishPackedControl()
}
func (a *Adapter) servicePacked(cycle uint64, token model.Token, mask uint8) (model.Response, error) {
	i := a.current
	p := i.packed
	u := p.uops[token.Uop]
	if u == nil || !u.sent || cycle <= u.sentCycle || cycle <= a.lastCycle {
		return model.Response{}, fmt.Errorf("packed service before accepted request or past edge")
	}
	if mask == 0 || mask & ^i.token.Mask != 0 || mask&u.served != 0 {
		return model.Response{}, fmt.Errorf("invalid or repeated packed service coverage")
	}
	responses := []isa.PackedLoadResponse{}
	var serviceErr error
	for _, request := range u.requests {
		if mask&(1<<request.Lane) == 0 {
			continue
		}
		bytes := make([]byte, request.Width)
		r := isa.PackedLoadResponse{Request: request}
		if readErr := a.memory.Read(request.Address, bytes); readErr != nil {
			serviceErr = errors.Join(serviceErr, readErr)
			r.Fault, r.Reason = isa.FaultLoadAccess, isa.FaultReasonMemoryService
		}
		value := uint32(0)
		for j, b := range bytes {
			value |= uint32(b) << (8 * j)
		}
		r.Data = value
		responses = append(responses, r)
	}
	complete, err := u.snapshot.CompletePackedLoadPart(i.decoded, isa.LaneMask(mask), token.Uop, responses)
	if err != nil {
		return model.Response{}, err
	}
	if len(complete.Faults) != 0 {
		return model.Response{}, architecturalFault(i.token.PC, complete.Faults, serviceErr)
	}
	delivery, err := a.newDelivery(complete)
	if err != nil {
		return model.Response{}, err
	}
	part := &memoryPart{delivery: delivery}
	for _, w := range complete.RegisterWrites {
		part.writes |= w.Mask
	}
	u.parts[mask] = part
	u.served |= mask
	p.responses = append(p.responses, responses...)
	all := len(p.uops) == int(i.token.Uops)
	for _, entry := range p.uops {
		all = all && entry.served == i.token.Mask
	}
	if all {
		// Reuse the original all-elements completion for final instruction control.
		full, err := u.snapshot.CompletePackedLoad(i.decoded, isa.LaneMask(i.token.Mask), p.responses)
		if err != nil {
			return model.Response{}, err
		}
		p.control, err = a.newDelivery(isa.InstructionEffects{Control: full.Control})
		if err != nil {
			return model.Response{}, err
		}
	}
	if err = a.finishPackedControl(); err != nil {
		return model.Response{}, err
	}
	return model.Response{Valid: true, ID: token.ID, Epoch: token.Epoch, Warp: token.Warp, Uop: token.Uop, Mask: mask}, nil
}
func (a *Adapter) finishPackedControl() error {
	p := a.current.packed
	if a.current.pending && p.control != nil && !p.control.Delivered(state.ControlEvent) {
		return p.control.Deliver(state.ControlEvent, 0, nil)
	}
	return nil
}
func (a *Adapter) packedFinished() bool {
	p := a.current.packed
	return p.control != nil && p.control.Delivered(state.ControlEvent)
}
