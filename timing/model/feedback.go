package model

import "fmt"

type FeedbackKind string

const (
	FeedbackSpawn  FeedbackKind = "spawn"
	FeedbackBranch FeedbackKind = "branch"
	FeedbackTMC    FeedbackKind = "tmc"
	FeedbackSplit  FeedbackKind = "split"
	FeedbackJoin   FeedbackKind = "join"
	FeedbackWake   FeedbackKind = "external-wake"
)

// SchedulerFeedback contains explicit functional producer results. JOIN is
// supplied at the split_join output edge, not its earlier SFU input edge.
// Normal wstall forbids younger fetches; this API does not implement software
// squash. Callers must cancel speculative work separately before recovery.
type SchedulerFeedback struct {
	Targets              uint8
	TargetPC             uint32
	Token                Token
	Kind                 FeedbackKind
	UpdatePC, UpdateMask bool
	PC                   uint32
	Mask                 uint8
}

func (s *Scheduler) validateFeedback(events []SchedulerFeedback, selected Signal, accepted bool, fetch, decode Signal) error {
	seen := [4]bool{}
	for _, e := range events {
		w := e.Token.Warp
		if w >= 4 || e.Token.ID == 0 {
			return fmt.Errorf("invalid feedback identity")
		}
		old := s.state.Warps[w]
		if seen[w] {
			return fmt.Errorf("UNRESOLVED simultaneous same-warp feedback")
		}
		seen[w] = true
		if e.Token.Epoch != old.Epoch || e.Token.ID <= s.state.LastControl[w] {
			return fmt.Errorf("stale or repeated scheduler feedback")
		}
		if !old.Active || !old.Stalled {
			return fmt.Errorf("feedback requires a blocked active warp")
		}
		if e.UpdatePC && e.PC&3 != 0 || e.UpdateMask && e.Mask & ^uint8(15) != 0 {
			return fmt.Errorf("invalid feedback PC/mask")
		}
		switch e.Kind {
		case FeedbackSpawn:
			if !s.state.SingleActive || e.Targets&^uint8(15) != 0 || e.Targets&(1<<w) != 0 || e.TargetPC&3 != 0 {
				return fmt.Errorf("invalid spawn gate/targets")
			}
			for target, context := range s.state.Warps {
				if target != int(w) && context.Active {
					return fmt.Errorf("spawn requires one active warp")
				}
				if e.Targets&(1<<target) != 0 && s.state.IBufferCount[target] != 0 {
					return fmt.Errorf("spawn target has pending frontend")
				}
			}
		case FeedbackBranch, FeedbackJoin:
		case FeedbackTMC:
			if !e.UpdateMask || e.UpdatePC {
				return fmt.Errorf("TMC requires mask-only update")
			}
		case FeedbackSplit:
			if e.UpdatePC {
				return fmt.Errorf("SPLIT does not redirect PC")
			}
		case FeedbackWake:
			if e.UpdatePC || e.UpdateMask {
				return fmt.Errorf("external wake cannot replace context")
			}
		default:
			return fmt.Errorf("unknown scheduler feedback kind")
		}
		if e.UpdateMask && e.Mask == 0 && e.Kind != FeedbackTMC {
			return fmt.Errorf("only TMC may deactivate a warp")
		}
		// Local RTL assignment order is documented, but this software interface
		// cannot infer reachability of conflicting producer events. Fail before
		// constructing mutations instead of choosing priority by call order.
		for _, event := range []Signal{fetch, decode, s.state.DecodeUnlock} {
			if event.Valid && event.Token.Warp == w {
				return fmt.Errorf("UNRESOLVED feedback overlapping same-warp frontend event")
			}
		}
		if accepted && selected.Token.Warp == w {
			return fmt.Errorf("UNRESOLVED feedback and same-warp scheduling")
		}
	}
	return nil
}
