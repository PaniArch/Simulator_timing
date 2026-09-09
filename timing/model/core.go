package model

import "fmt"

// Core is a transient four-warp pipeline composition, not an ISA executor.
// Functional owners and external memory bytes are deliberately outside it.
type Core struct {
	front  *Frontend
	alu    *ALU
	lsu    *LSU
	sfu    *SFU
	fpu    *STDFPU
	commit *Commit
}
type CoreInputs struct {
	Feedback                      []SchedulerFeedback // resolved old-edge branch/SIMT producer values
	Instruction                   Signal              // explicit-token mode only; leave invalid in scheduled mode
	FetchResponse, MemoryResponse Response
	FetchReady, MemoryReady       bool
	Eligible, ControlAllowed      bool // Eligible is the explicit-token diagnostic gate
}
type CoreReport struct {
	IssueCandidates                                                                             [4]IssueCandidate
	IssueSelected                                                                               int
	Wakeups                                                                                     []SchedulerFeedback // detached accepted feedback at this edge
	Scheduler                                                                                   SchedulerState
	Scheduled                                                                                   bool
	Scoreboard                                                                                  ScoreboardState
	Issued, Decoded                                                                             Signal
	IBufferPop                                                                                  [4]bool
	MemoryResponse                                                                              Response
	Executed                                                                                    [4]Signal
	CSRRequest                                                                                  Signal
	Resources                                                                                   []ResourceState
	Credits                                                                                     [4]int
	Offered, FetchRequest, MemoryRequest                                                        Signal
	InstructionAccepted, FetchAccepted, FetchResponseReady, MemoryAccepted, MemoryResponseReady bool
	Dispatched                                                                                  [4]bool
	Read, Writeback, PendingRelease, Branch, Control, Flags                                     Signal
	CSRRequestWindow                                                                            bool
}
type CoreTransition struct {
	Transition
	Report CoreReport
}

// NewCore retains caller-selected token admission for the existing functional
// runner. It uses the same four-warp resources and real scoreboard as scheduled mode.
func NewCore(backend string) (*Core, error) {
	if backend != "std" {
		return nil, fmt.Errorf("explicit supported FPU backend required: std")
	}
	c := &Core{}
	var err error
	if c.front, err = NewFrontend(); err != nil {
		return nil, err
	}
	if c.alu, err = NewALU(); err != nil {
		return nil, err
	}
	if c.lsu, err = NewLSU(); err != nil {
		return nil, err
	}
	if c.sfu, err = NewSFU(); err != nil {
		return nil, err
	}
	if c.fpu, err = NewSTDFPU(); err != nil {
		return nil, err
	}
	if c.commit, err = NewCommit(); err != nil {
		return nil, err
	}
	return c, nil
}

// NewScheduledCore accepts explicit front-end contexts and autonomously selects
// fetches. Functional owners and control-result values remain outside this API.
func NewScheduledCore(backend string, warps [4]WarpContext) (*Core, error) {
	c, err := NewCore(backend)
	if err != nil {
		return nil, err
	}
	c.front.scheduler, err = NewScheduler(warps)
	if err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Core) Resources() []ResourceState {
	r := c.front.Resources()
	r = append(r, c.alu.Resources()...)
	r = append(r, c.lsu.Resources()...)
	r = append(r, c.sfu.Resources()...)
	r = append(r, c.fpu.Resources()...)
	return append(r, observed(c.commit, c.commit.Output()))
}
func (c *Core) Idle(serviceIdle bool) bool {
	if c.front.scheduler != nil {
		s := c.front.scheduler
		if s.Output().Valid || s.state.DecodeUnlock.Valid || s.state.IBufferCount != [4]uint8{} {
			return false
		}
	}
	score := c.front.ScoreboardState()
	if score.Busy != [4]uint64{} || score.Special != [4]uint8{} || score.Credits != [4]int{} || score.Locked != [4]bool{} {
		return false
	}
	if !serviceIdle || c.commit.Pending().Valid || c.alu.Branch().Valid || c.sfu.Control().Valid || c.fpu.Flags().Valid || c.lsu.FenceLocked() {
		return false
	}
	for _, r := range c.Resources() {
		if r.Occupancy != 0 {
			return false
		}
	}
	return true
}
func (c *Core) Evaluate(in CoreInputs) (CoreTransition, error) {
	wb := c.commit.Evaluate([4]Signal{c.alu.Output(), c.lsu.Output(), c.sfu.Output(), c.fpu.Output()})
	outputs := c.front.Outputs()
	alu, err := c.alu.Evaluate(outputs[0], wb.Ready[0])
	if err != nil {
		return CoreTransition{}, err
	}
	lsu, err := c.lsu.Evaluate(outputs[1], in.MemoryResponse, in.MemoryReady, wb.Ready[1])
	if err != nil {
		return CoreTransition{}, err
	}
	sfu, err := c.sfu.Evaluate(outputs[2], wb.Ready[2], in.ControlAllowed)
	if err != nil {
		return CoreTransition{}, err
	}
	fpu, err := c.fpu.Evaluate(outputs[3], wb.Ready[3])
	if err != nil {
		return CoreTransition{}, err
	}
	front, err := c.front.Evaluate(in.Instruction, in.FetchResponse, in.FetchReady, in.Eligible, [4]bool{alu.InputReady, lsu.InputReady, sfu.InputReady, fpu.InputReady}, wb.Writeback, in.Feedback...)
	if err != nil {
		return CoreTransition{}, err
	}
	t := combine(transfer(front.Offered, wb.Writeback, front.InputReady, true), front.Transition, alu.Transition, lsu.Transition, sfu.Transition, fpu.Transition, wb.Transition)
	executed := [4]Signal{outputs[0], lsu.Execute, sfu.Execute, outputs[3]}
	executed[0].Valid = alu.Accepted
	executed[3].Valid = fpu.Accepted
	scheduler, scheduled := c.front.SchedulerState()
	report := CoreReport{IssueCandidates: c.IssueCandidates(), IssueSelected: front.IssueSelected, Wakeups: append([]SchedulerFeedback(nil), in.Feedback...), Scheduler: scheduler, Scheduled: scheduled, Scoreboard: c.front.ScoreboardState(), Issued: front.Issued, Decoded: front.Decoded, IBufferPop: front.IBufferPop, MemoryResponse: in.MemoryResponse, Executed: executed, CSRRequest: sfu.CSRRequest, Resources: c.Resources(), Credits: c.front.Credits(), Offered: front.Offered, FetchRequest: front.Request, MemoryRequest: lsu.Request, InstructionAccepted: front.Accepted, FetchAccepted: front.RequestAccepted, FetchResponseReady: front.ResponseReady, MemoryAccepted: lsu.RequestAccepted, MemoryResponseReady: lsu.ResponseReady, Dispatched: front.Releases, Read: front.Read, Writeback: wb.Writeback, PendingRelease: wb.PendingRelease, Branch: alu.Branch, Control: sfu.Control, Flags: fpu.Flags, CSRRequestWindow: sfu.CSRRequestWindow}
	return CoreTransition{Transition: t, Report: report}, nil
}
func (c *Core) Flush() Transition {
	return combine(Transition{}, c.front.Flush(), c.alu.Flush(), c.lsu.Flush(), c.sfu.Flush(), c.fpu.Flush(), c.commit.Flush())
}

// ControlInput identifies the old SFU execution candidate for caller-owned
// drain predicates. It carries no functional results or mutable owner.
func (c *Core) ControlInput() Signal { return c.sfu.ExecuteInput() }

// FeedbackSignals exposes registered producer identities, without functional values.
func (c *Core) FeedbackSignals() (Signal, Signal) { return c.alu.Branch(), c.sfu.Control() }
