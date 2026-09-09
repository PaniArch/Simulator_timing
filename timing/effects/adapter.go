// Package effects binds timing observations to existing functional state owners.
package effects

import (
	"fmt"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing"
	"vortex.local/simulator/timing/model"
)

type ExternalOwner func(isa.InstructionEffects) error

type instruction struct {
	trapCycle          uint64
	trapRefreshed      bool
	spawnAt            *uint64
	feedback           *model.SchedulerFeedback
	packed             *packedInstruction
	memory             *memoryInstruction
	joinAt             *uint64
	token              model.Token
	decoded            isa.Decoded
	capture            *state.OperandCapture
	delivery           *state.EffectDelivery
	writes             isa.LaneMask
	evaluated, pending bool
}

// Adapter owns transient identity/capture/receipt state only. Its initial scope
// is a single instruction in flight; memory completion is handled by
// the service adapter, never emulated by an early Warp.Step call.
type Adapter struct {
	csrTrap       *state.EffectDelivery // optional same-edge scheduler trap receipt
	csrFlags      *state.EffectDelivery // same-edge FPU receipt paired by Concurrent
	stream        *state.EffectStream
	spawnTargets  []state.WarpSpawnTarget
	spawnBound    bool
	spawnPool     bool
	memory        warp.MemoryService
	joinLatency   uint64
	failed        bool
	owner         *state.WarpState
	epoch, lastID uint64
	current       *instruction
	external      ExternalOwner
	lastCycle     uint64
	observed      bool
}

func New(owner *state.WarpState, epoch uint64, external ExternalOwner) (*Adapter, error) {
	if owner == nil {
		return nil, fmt.Errorf("nil canonical warp owner")
	}
	latency, err := timing.Number("boundaries", "b-simt-feedback", "rtl_parameters", "OUT_REG")
	if err != nil {
		return nil, err
	}
	if latency != 1 {
		return nil, fmt.Errorf("unsupported JOIN feedback register")
	}
	return &Adapter{owner: owner, epoch: epoch, external: external, joinLatency: uint64(latency)}, nil
}
func (a *Adapter) Begin(token model.Token) error {
	if a.failed {
		return fmt.Errorf("effect adapter requires reset")
	}
	if a.current != nil {
		return fmt.Errorf("previous instruction has not finished")
	}
	snapshot, err := a.owner.Snapshot()
	if err != nil {
		return err
	}
	if token.Epoch != a.epoch || token.ID <= a.lastID || token.Warp != snapshot.WarpID() || (a.stream == nil && (token.PC != snapshot.PC() || isa.LaneMask(token.Mask) != snapshot.ActiveMask())) {
		return fmt.Errorf("invalid instruction identity or canonical context")
	}
	if a.stream != nil {
		if _, err = snapshot.WithInstructionContext(state.InstructionContext{WarpID: token.Warp, PC: token.PC, Mask: isa.LaneMask(token.Mask)}); err != nil {
			return err
		}
	}
	routed, err := model.DecodeToken(model.Signal{Valid: true, Token: token})
	if err != nil {
		return err
	}
	decoded, err := isa.Decode(token.Word)
	if err != nil {
		return err
	}
	if routed.Token.Class == 1 && a.memory == nil {
		return fmt.Errorf("memory owner required")
	}
	a.current = &instruction{token: routed.Token, decoded: decoded}
	if decoded.Memory.Packed != 0 {
		a.current.token.Uops = decoded.Memory.Packed
		a.current.packed = &packedInstruction{uops: map[uint8]*packedUop{}}
	}
	a.lastID = token.ID
	return nil
}
func (a *Adapter) check(s model.Signal) error {
	if !s.Valid {
		return nil
	}
	if a.current == nil {
		return fmt.Errorf("event without active instruction")
	}
	want, got := a.current.token, s.Token
	if got.ID != want.ID || got.Epoch != a.epoch || got.Warp != want.Warp || got.PC != want.PC || got.Word != want.Word || got.Class != want.Class || got.Path != want.Path || got.Uop >= want.Uops || got.Uops != want.Uops || got.Mask == 0 || got.Mask & ^want.Mask != 0 {
		return fmt.Errorf("foreign, stale or inconsistent timing event")
	}
	return nil
}

// Observe consumes one already-committed common-edge report. All identities
// are checked before any owner operation. Reads and execution evaluation use
// an old-edge snapshot, before any visible effect in this report is applied.
// A failure is fatal to this adapter until Reset; previously-visible effects
// are not rolled back or replayed by retrying a partially delivered edge.
func (a *Adapter) Observe(cycle uint64, r model.CoreReport, context state.ReadContext) error {
	snapshot, err := a.owner.Snapshot()
	if err != nil {
		return err
	}
	return a.observeAt(cycle, r, context, snapshot)
}

func (a *Adapter) validateReport(cycle uint64, r model.CoreReport) error {
	if a.failed {
		return fmt.Errorf("effect adapter requires reset")
	}
	if a.observed && cycle <= a.lastCycle {
		return fmt.Errorf("repeated or reordered effect edge")
	}
	signals := []model.Signal{r.Read, r.Writeback, r.PendingRelease, r.Branch, r.Control, r.Flags, r.CSRRequest}
	signals = append(signals, r.Executed[:]...)
	if r.MemoryAccepted {
		signals = append(signals, r.MemoryRequest)
	}
	for _, s := range signals {
		if err := a.check(s); err != nil {
			return err
		}
	}
	if r.MemoryResponse.Valid {
		if _, err := a.responsePart(r.MemoryResponse); err != nil {
			return err
		}
	}
	if a.current != nil {
		tok := a.current.token
		if r.Branch.Valid && !tok.Branch || r.Control.Valid && tok.Path != model.WCTL || r.Flags.Valid && tok.Class != 3 || r.CSRRequest.Valid && tok.Path != model.CSRPath {
			return fmt.Errorf("wrong event kind for instruction")
		}
		if tok.Class != 1 && r.Writeback.Valid && !r.Writeback.Token.End {
			return fmt.Errorf("partial non-memory result outside full-width profile")
		}
	}
	return nil
}

func (a *Adapter) observeAt(cycle uint64, r model.CoreReport, context state.ReadContext, snapshot state.WarpSnapshot) (err error) {
	if a.failed {
		return fmt.Errorf("effect adapter requires reset")
	}
	defer func() {
		if err != nil {
			a.failed = true
		}
	}()
	if err = a.validateReport(cycle, r); err != nil {
		return err
	}
	a.observed = true
	a.lastCycle = cycle
	// Successful groups are never replayed following a later group failure.
	defer func() {
		if err != nil && a.current != nil && a.current.delivery != nil {
			a.current.delivery.Cancel()
		}
	}()
	i := a.current
	if i == nil {
		return nil
	}
	if a.stream != nil {
		snapshot, err = snapshot.WithInstructionContext(a.instructionContext())
		if err != nil {
			return err
		}
	}
	if r.Branch.Valid && a.isTrap() {
		if err = a.refreshTrap(cycle, snapshot); err != nil {
			return err
		}
	}
	if i.packed != nil {
		return a.observePacked(cycle, r, context, snapshot)
	}
	if r.Read.Valid {
		if i.capture == nil {
			i.capture, err = a.newCapture(snapshot, context)
			if err != nil {
				return err
			}
		}
		if err = i.capture.Read(snapshot, r.Read.Token.ReadMask); err != nil {
			return err
		}
		if r.Read.Token.LastRead && !i.capture.Complete() {
			return fmt.Errorf("final read lacks operand coverage")
		}
	}
	// VX_csr_unit writes on csr_req_valid, independently of result readiness.
	// A held request re-reads old-edge CSR state and applies that edge's write;
	// it does not create a writeback or complete the instruction receipt.
	if r.CSRRequest.Valid && !r.Executed[2].Valid {
		if i.evaluated || i.capture == nil {
			return fmt.Errorf("premature or repeated CSR request after execution")
		}
		e, err := i.capture.EvaluateAt(snapshot, context)
		if err != nil {
			return err
		}
		if len(e.Faults) != 0 {
			return architecturalFault(i.token.PC, e.Faults, nil)
		}
		delivery, err := a.newDelivery(e)
		if err != nil {
			return err
		}
		if err = a.deliverCSR(delivery, e); err != nil {
			return err
		}
	}
	for class, s := range r.Executed {
		if !s.Valid {
			continue
		}
		if int(i.token.Class) != class || i.evaluated || i.capture == nil {
			return fmt.Errorf("duplicate or premature execution")
		}
		e, err := i.capture.EvaluateAt(snapshot, context)
		if err != nil {
			return err
		}
		if len(e.Faults) != 0 {
			return architecturalFault(i.token.PC, e.Faults, nil)
		}
		for _, drain := range e.WarpDrains {
			if drain.Wait {
				return fmt.Errorf("control executed before drain; use ControlAllowed with old-edge context")
			}
		}
		// Trap recognition in ALU does not read scheduler-owned CSR registers.
		// Build its delivery at feedback, avoiding execute-edge CSR validation.
		if !a.isTrap() {
			i.delivery, err = a.newDelivery(e)
			if err != nil {
				return err
			}
		}
		for _, w := range e.RegisterWrites {
			i.writes |= w.Mask
		}
		i.feedback = latchFeedback(i.token, i.decoded, e)
		if a.spawnPool && i.decoded.Control == isa.ControlWarpSpawn {
			if _, err := a.selectedSpawnTargets(); err != nil {
				return err
			}
		}
		i.evaluated = true
		if i.token.Class == 1 {
			i.memory = &memoryInstruction{snapshot: snapshot, requests: append([]isa.MemoryRequest(nil), e.MemoryRequests...), parts: map[uint8]*memoryPart{}}
		}
		if i.token.Path == model.CSRPath {
			if err = a.deliverCSR(i.delivery, e); err != nil {
				return err
			}
		}
	}
	if i.memory != nil {
		if err = a.observeMemory(cycle, r); err != nil {
			return err
		}
	}
	deliver := func(event state.VisibilityEvent, mask isa.LaneMask, external bool) error {
		if i.delivery == nil {
			return fmt.Errorf("visibility before execution")
		}
		var callback func(isa.InstructionEffects) error
		if external {
			callback = a.external
		}
		return i.delivery.Deliver(event, mask, callback)
	}
	if i.spawnAt != nil && cycle >= *i.spawnAt && (!r.Scheduled || r.Scheduler.SingleActive) {
		targets, e := a.selectedSpawnTargets()
		if e != nil {
			return e
		}
		if err = i.delivery.DeliverWarpSpawnAtActivation(targets); err != nil {
			return err
		}
		i.spawnAt = nil
	}
	if i.joinAt != nil && cycle >= *i.joinAt {
		if cycle != *i.joinAt {
			return fmt.Errorf("missed registered JOIN feedback edge")
		}
		if err = deliver(state.ControlEvent, 0, false); err != nil {
			return err
		}
		i.joinAt = nil
	}
	if r.Flags.Valid {
		if err = deliver(state.FFlagsEvent, 0, false); err != nil {
			return err
		}
	}
	if r.Writeback.Valid && i.memory == nil {
		if err = deliver(state.WritebackEvent, i.writes, false); err != nil {
			return err
		}
	}
	if r.Branch.Valid || r.Control.Valid {
		external := i.decoded.Control == isa.ControlWarpSpawn || i.decoded.Control == isa.ControlWarpSync || i.decoded.Barrier != isa.BarrierNone
		if i.decoded.Control == isa.ControlJoin {
			if i.joinAt != nil || i.delivery == nil || i.delivery.Delivered(state.ControlEvent) {
				return fmt.Errorf("duplicate or premature JOIN feedback")
			}
			due := cycle + a.joinLatency
			if due < cycle {
				return fmt.Errorf("JOIN cycle overflow")
			}
			i.joinAt = &due
		} else if i.decoded.Control == isa.ControlWarpSpawn {
			if !a.spawnBound || i.delivery == nil {
				return fmt.Errorf("spawn target owners not bound")
			}
			if a.stream != nil {
				if i.spawnAt != nil || i.delivery.Delivered(state.ControlEvent) || cycle == ^uint64(0) {
					return fmt.Errorf("duplicate/overflow spawn feedback")
				}
				due := cycle + 1
				i.spawnAt = &due
			} else if err = i.delivery.DeliverWarpSpawn(a.spawnTargets); err != nil {
				return err
			}
		} else if err = deliver(state.ControlEvent, 0, external); err != nil {
			return err
		}
	}
	if r.PendingRelease.Valid && i.memory == nil {
		if i.pending || i.delivery == nil || !i.delivery.Delivered(state.WritebackEvent) {
			return fmt.Errorf("premature or duplicate pending release")
		}
		if i.token.Branch || i.token.Path == model.WCTL {
			if !i.delivery.Delivered(state.ControlEvent) && i.spawnAt == nil {
				return fmt.Errorf("pending release before control visibility")
			}
		} else {
			if err = deliver(state.ControlEvent, 0, false); err != nil {
				return err
			}
		}
		if i.token.Class == 3 && !i.delivery.Delivered(state.FFlagsEvent) {
			return fmt.Errorf("pending release before FFLAGS")
		}
		i.pending = true
	}
	return nil
}
func (a *Adapter) Finish() error {
	if a.failed || a.current == nil || !a.current.pending || a.current.spawnAt != nil || a.current.memory != nil && !a.memoryFinished() || a.current != nil && a.current.packed != nil && !a.packedFinished() {
		return fmt.Errorf("instruction effects are incomplete")
	}
	a.current = nil
	a.spawnBound = false
	a.spawnTargets = nil
	a.spawnPool = false
	return nil
}

// Reset invalidates old residency events without mutating canonical state.
// The caller must also flush timing components and cancel external services.
func (a *Adapter) Reset(epoch uint64) error {
	if epoch <= a.epoch {
		return fmt.Errorf("epoch must increase")
	}
	if a.current != nil && a.current.delivery != nil {
		a.current.delivery.Cancel()
	}
	a.current = nil
	a.spawnBound = false
	a.spawnTargets = nil
	a.spawnPool = false
	a.failed = false
	a.epoch = epoch
	a.lastID = 0
	return nil
}

func (a *Adapter) instructionContext() state.InstructionContext {
	t := a.current.token
	return state.InstructionContext{WarpID: t.Warp, PC: t.PC, Mask: isa.LaneMask(t.Mask)}
}
func (a *Adapter) newCapture(snapshot state.WarpSnapshot, context state.ReadContext) (*state.OperandCapture, error) {
	if a.stream != nil {
		return state.NewLatchedOperandCapture(snapshot, a.current.token.Word, context, a.instructionContext())
	}
	return state.NewOperandCapture(snapshot, a.current.token.Word, context)
}
func (a *Adapter) newDelivery(e isa.InstructionEffects) (*state.EffectDelivery, error) {
	if a.stream != nil {
		return a.stream.NewDelivery(a.current.token.ID, a.instructionContext(), e)
	}
	return a.owner.NewEffectDelivery(e)
}

func (a *Adapter) deliverCSR(delivery *state.EffectDelivery, e isa.InstructionEffects) error {
	var callback func(isa.InstructionEffects) error
	for _, w := range e.CSRWrites {
		if w.Scope != isa.CSRScopeWarp && !(w.Scope == isa.CSRScopeConstant && w.Ignored) {
			callback = a.external
		}
	}
	if a.csrTrap != nil {
		return delivery.DeliverCSRWithTrap(a.csrTrap, a.csrFlags, callback)
	}
	if a.csrFlags != nil {
		return delivery.DeliverCSRWithFlags(a.csrFlags, callback)
	}
	return delivery.Deliver(state.CSREvent, 0, callback)
}
