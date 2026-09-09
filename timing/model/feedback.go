package model

import (
	"fmt"
	"sort"
)

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

func (s *Scheduler) validateFeedback(events []SchedulerFeedback) error {
	seen := [4]map[FeedbackKind]bool{}
	identities := [4]map[uint64]bool{}
	for _, e := range events {
		w := e.Token.Warp
		if w >= 4 || e.Token.ID == 0 {
			return fmt.Errorf("invalid feedback identity")
		}
		old := s.state.Warps[w]
		if seen[w] == nil {
			seen[w] = map[FeedbackKind]bool{}
			identities[w] = map[uint64]bool{}
		}
		if seen[w][e.Kind] || identities[w][e.Token.ID] {
			return fmt.Errorf("duplicate scheduler producer or instruction feedback")
		}
		seen[w][e.Kind], identities[w][e.Token.ID] = true, true
		if e.Token.Epoch != old.Epoch || e.Token.ID <= s.state.LastControl[w] {
			return fmt.Errorf("stale or repeated scheduler feedback")
		}
		if !old.Active {
			return fmt.Errorf("feedback requires an active warp")
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

	}
	return nil
}

// orderedFeedback follows VX_scheduler's per-field assignment order. Copy the
// slice so evaluating a proposal never mutates caller-owned producer events.
func orderedFeedback(events []SchedulerFeedback) []SchedulerFeedback {
	priority := map[FeedbackKind]int{FeedbackSpawn: 0, FeedbackTMC: 1, FeedbackSplit: 2, FeedbackJoin: 3, FeedbackWake: 4, FeedbackBranch: 5}
	next := append([]SchedulerFeedback(nil), events...)
	sort.SliceStable(next, func(i, j int) bool { return priority[next[i].Kind] < priority[next[j].Kind] })
	return next
}
