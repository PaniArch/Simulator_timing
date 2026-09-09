package model

import (
	"reflect"
	"testing"
)

func TestALUTopologyAndBranchRegister(t *testing.T) {
	for _, tc := range []struct {
		path    Path
		latency int
	}{{INT, 2}, {MUL, 5}, {DIV, 35}} {
		t.Run(string(tc.path), func(t *testing.T) {
			a, err := NewALU()
			if err != nil {
				t.Fatal(err)
			}
			in := valid(1)
			in.Token.Path = tc.path
			in.Token.End = true
			in.Token.Branch = tc.path == INT
			for edge := 0; edge <= tc.latency+2; edge++ {
				p, err := a.Evaluate(in, true)
				if err != nil {
					t.Fatal(err)
				}
				if p.Completed != (edge == tc.latency) {
					t.Fatal("wrong composed latency", edge, p.Completed)
				}
				if p.Branch.Valid != (tc.path == INT && edge == 2) {
					t.Fatal("wrong branch register", edge, p.Branch)
				}
				if p.Accepted {
					in = Signal{}
				}
				commit(t, p.Transition)
			}
			if a.Occupancy() != 0 {
				t.Fatal("did not drain")
			}
		})
	}
}

func TestFrontendALUCommitCommonEdge(t *testing.T) {
	run := func(reverse bool) []uint64 {
		f, err := NewFrontend()
		if err != nil {
			t.Fatal(err)
		}
		a, err := NewALU()
		if err != nil {
			t.Fatal(err)
		}
		c, err := NewCommit()
		if err != nil {
			t.Fatal(err)
		}
		words := []uint32{0x00100093, 0x022081b3, 0x0220c1b3, 0x00000063}
		next := 0
		offered := Signal{}
		response := Response{}
		active := false
		trace := []uint64{}
		for edge := uint64(0); edge < 250; edge++ {
			if !active && next < len(words) {
				offered = valid(uint64(next + 1))
				offered.Token.PC = uint32(next * 4)
				active = true
			}
			// Deliberately evaluate this read-only probe in the opposite traversal order.
			// It must not consume state or alter the subsequent common-edge proposal.
			if reverse {
				if _, err := f.Evaluate(offered, response, true, true, [4]bool{}, c.Output()); err != nil {
					t.Fatal(err)
				}
			}
			cp := c.Evaluate([4]Signal{a.Output(), {}, {}, {}})
			ap, err := a.Evaluate(f.Outputs()[0], cp.Ready[0])
			if err != nil {
				t.Fatal(err)
			}
			fp, err := f.Evaluate(offered, response, true, true, [4]bool{ap.InputReady, false, false, false}, cp.Writeback)
			if err != nil {
				t.Fatal(err)
			}
			if fp.Accepted {
				offered = Signal{}
			}
			if fp.ResponseReady && response.Valid {
				response = Response{}
			}
			if fp.RequestAccepted {
				tok := fp.Request.Token
				response = Response{Valid: true, ID: tok.ID, Epoch: tok.Epoch, Warp: tok.Warp, Uop: tok.Uop, Mask: tok.Mask, Word: words[tok.ID-1]}
			}
			if cp.Writeback.Valid {
				trace = append(trace, edge, cp.Writeback.Token.ID)
			}
			if cp.PendingRelease.Valid {
				active = false
				next++
			}
			proposals := []Transition{fp.Transition, ap.Transition, cp.Transition}
			if reverse {
				proposals[0], proposals[2] = proposals[2], proposals[0]
			}
			if err := CommitEdge(proposals...); err != nil {
				t.Fatal(err)
			}
			if next == len(words) {
				if f.Credits() != [4]int{} || a.Occupancy() != 0 {
					t.Fatal("residual resources")
				}
				if len(trace) != 2*len(words) {
					t.Fatal("missing WB", trace)
				}
				return trace
			}
		}
		t.Fatal("pipeline did not finish")
		return nil
	}
	normal, reversed := run(false), run(true)
	if !reflect.DeepEqual(normal, reversed) {
		t.Fatal("traversal order changed cycles", normal, reversed)
	}
}
