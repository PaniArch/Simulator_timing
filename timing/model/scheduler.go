package model

import (
	"fmt"
	"vortex.local/simulator/timing"
)

// WarpContext is front-end state supplied explicitly by the caller at Core
// construction. It is not a canonical WarpState and contains no register data.
type WarpContext struct {
	Active, Stalled bool
	PC              uint32
	Mask            uint8
	Epoch           uint64
}

type SchedulerState struct {
	Parked          [4]bool // software cancellation gate, independent of RTL decode/control unlock
	SingleActive    bool    // registered old active-warp count for pending spawn
	Warps           [4]WarpContext
	IBufferCount    [4]uint8 // schedule acceptance through physical IBuffer pop
	IBufferFull     [4]bool
	AllIBuffersFull bool
	DecodeUnlock    Signal    // registered decode event, consumed at the next edge
	LastControl     [4]uint64 // software receipt frontier; not an RTL register
}

// Scheduler implements the frozen lowest-warp fetch selector and its registered
// accounting. b-schedule owns the accepted payload under backpressure; Output
// itself is combinational, just like VX_scheduler.schedule_warps.
type Scheduler struct {
	state  SchedulerState
	nextID uint64
	rev    revision
}

func NewScheduler(warps [4]WarpContext) (*Scheduler, error) {
	n, err := timing.Number("config", "cfg-baseline", "values", "warps_per_core")
	if err != nil {
		return nil, err
	}
	if n != len(warps) {
		return nil, fmt.Errorf("unsupported scheduler warp configuration")
	}
	lanes, err := timing.Number("config", "cfg-baseline", "values", "threads_per_warp")
	if err != nil {
		return nil, err
	}
	ibuf, err := timing.Buffer("b-ibuffer")
	if err != nil {
		return nil, err
	}
	if lanes != 4 || ibuf.Size != 4 {
		return nil, fmt.Errorf("unsupported scheduler lane/IBuffer configuration")
	}
	for _, w := range warps {
		if w.Mask & ^uint8(15) != 0 || w.Active && w.Mask == 0 || w.PC&3 != 0 {
			return nil, fmt.Errorf("invalid initial front-end context")
		}
	}
	return &Scheduler{state: SchedulerState{Warps: warps}, nextID: 1}, nil
}
func (s *Scheduler) State() SchedulerState { return s.state }
func (s *Scheduler) Output() Signal {
	for w, c := range s.state.Warps {
		if c.Active && !c.Stalled && !s.state.Parked[w] && (s.state.AllIBuffersFull || !s.state.IBufferFull[w]) {
			return Signal{Valid: true, Token: Token{ID: s.nextID, Epoch: c.Epoch, Warp: uint8(w), PC: c.PC, Mask: c.Mask}}
		}
	}
	return Signal{}
}

// Evaluate samples the internal schedule handshake separately from the accepted
// b-schedule OUTPUT into Fetch. decode is the physical IBuffer insertion event;
// its unlock is registered here and therefore cannot enable a fetch this edge.
// feedback contains already-resolved producer values, never a canonical owner.
// Its changes become selectable only after CommitEdge.
func (s *Scheduler) Evaluate(scheduleReady bool, fetchAccepted Signal, pops [4]bool, decode Signal, feedback ...SchedulerFeedback) (Transition, error) {
	for _, signal := range []Signal{fetchAccepted, decode} {
		if signal.Valid && signal.Token.Warp >= 4 {
			return Transition{}, fmt.Errorf("invalid scheduler feedback warp")
		}
	}
	selected := s.Output()
	t := transfer(Signal{}, selected, false, scheduleReady)
	next := s.state
	nextID := s.nextID
	activeCount := 0
	for _, w := range s.state.Warps {
		if w.Active {
			activeCount++
		}
	}
	next.SingleActive = activeCount == 1
	if err := s.validateFeedback(feedback); err != nil {
		return Transition{}, err
	}
	// Local RTL assignment order is explicit. None of these edits is installed
	// until the same CommitEdge as schedule/Fetch/IBuffer component proposals.
	if old := s.state.DecodeUnlock; old.Valid && !old.Token.WarpStall {
		next.Warps[old.Token.Warp].Stalled = false
	}
	next.DecodeUnlock = decode
	if t.Completed {
		if nextID == ^uint64(0) {
			return Transition{}, fmt.Errorf("instruction identity exhausted")
		}
		nextID++
	}
	next.AllIBuffersFull = true
	for w := range next.Warps {
		incr := t.Completed && int(selected.Token.Warp) == w
		count := s.state.IBufferCount[w]
		if pops[w] && count == 0 && !incr {
			return Transition{}, fmt.Errorf("IBuffer pop without scheduled instruction")
		}
		if incr {
			count++
		}
		if pops[w] {
			count--
		}
		// VX_scheduler uses a 3-bit count and equality to IBUF_SIZE, including
		// the all-full fallback. Do not clamp the counter to physical FIFO size.
		count &= 7
		next.IBufferCount[w] = count
		next.IBufferFull[w] = count == 4
		next.AllIBuffersFull = next.AllIBuffersFull && next.IBufferFull[w]
	}
	for _, event := range orderedFeedback(feedback) {
		w := event.Token.Warp
		context := &next.Warps[w]
		if event.UpdatePC {
			context.PC = event.PC
		}
		if event.UpdateMask {
			context.Mask = event.Mask
		}
		if event.Kind == FeedbackTMC {
			context.Active = event.Mask != 0
		}
		context.Stalled = false
		if event.Token.ID > next.LastControl[w] {
			next.LastControl[w] = event.Token.ID
		}
		if event.Kind == FeedbackSpawn {
			for target := range next.Warps {
				if event.Targets&(1<<target) != 0 {
					next.Warps[target].Active = true
					next.Warps[target].Stalled = false
					next.Warps[target].PC = event.TargetPC
					next.Warps[target].Mask = 1
				}
			}
		}
	}
	// VX_scheduler applies schedule stall and fetch PC advance after feedback.
	if t.Completed {
		next.Warps[selected.Token.Warp].Stalled = true
	}
	if fetchAccepted.Valid {
		tok := fetchAccepted.Token
		next.Warps[tok.Warp].PC = tok.PC + 4
	}
	t.edits = []mutation{s.rev.propose(func() { s.state, s.nextID = next, nextID })}
	return t, nil
}

// Flush stops autonomous fetch and discards transient accounting. Instruction
// identities remain monotonic. External requests must be cancelled by the owner
// before combining this proposal with the rest of the pipeline flush.
func (s *Scheduler) Flush() Transition {
	next := s.state
	for w := range next.Warps {
		next.Warps[w].Active = false
		next.Warps[w].Stalled = false
	}
	next.SingleActive = false
	next.Parked = [4]bool{}
	next.IBufferCount = [4]uint8{}
	next.IBufferFull = [4]bool{}
	next.AllIBuffersFull = false
	next.DecodeUnlock = Signal{}
	return Transition{edits: []mutation{s.rev.propose(func() { s.state = next })}}
}
