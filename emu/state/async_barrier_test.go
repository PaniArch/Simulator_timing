package state_test

import (
	"errors"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func TestEffectStreamDelayedArrival(t *testing.T) {
	for _, scenario := range []string{"alu", "branch", "activation", "late-creation", "external-failure", "wait", "sync", "drain-wait", "event"} {
		t.Run(scenario, func(t *testing.T) {
			w := newWarp(t)
			s, err := state.NewEffectStream(w)
			if err != nil {
				t.Fatal(err)
			}
			ctx, e := streamEffects(t, w, 0x100, 0x00100093)
			e.Barriers = []isa.BarrierEffect{{WarpID: 2, Kind: isa.BarrierArrive, Arrive: true, DrainLSU: true}}
			e.WarpDrains = []isa.WarpDrainEffect{{WarpID: 2, Kind: isa.DrainLSU}}
			switch scenario {
			case "wait":
				e.Barriers[0].Kind = isa.BarrierWait
				e.Barriers[0].Wait = true
			case "sync":
				e.Barriers[0].Sync = true
			case "drain-wait":
				e.WarpDrains[0].Wait = true
			case "event":
				e.Barriers[0].Event = true
				e.Barriers[0].Arrive = false
			}
			d, err := s.NewDelivery(1, ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			word := uint32(0x00100093)
			if scenario == "branch" {
				word = 0x0080006f
			}
			nc, ne := streamEffects(t, w, 0x104, word)
			n, err := s.NewDelivery(2, nc, ne)
			if err != nil {
				t.Fatal(err)
			}
			if err = n.Deliver(state.ControlEvent, 0, nil); err != nil {
				t.Fatal(err)
			}
			if scenario == "activation" || scenario == "late-creation" {
				s.RecordActivation(2)
			}
			if scenario == "late-creation" {
				d, err = s.NewDelivery(1, ctx, e)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, w)
			calls := 0
			external := func(got isa.InstructionEffects) error {
				calls++
				if len(got.Barriers) != 1 || len(got.WarpDrains) != 1 {
					t.Fatal(got)
				}
				return nil
			}
			if scenario == "external-failure" {
				if err = d.Deliver(state.ControlEvent, 0, func(isa.InstructionEffects) error { return errors.New("owner rejected") }); err == nil || d.Delivered(state.ControlEvent) {
					t.Fatal("failed callback consumed receipt")
				}
				requireUnchanged(t, w, before)
			}
			err = d.Deliver(state.ControlEvent, 0, external)
			invalid := scenario == "activation" || scenario == "late-creation" || scenario == "wait" || scenario == "sync" || scenario == "drain-wait"
			if invalid {
				if err == nil || calls != 0 || d.Delivered(state.ControlEvent) {
					t.Fatal("stale/blocking group accepted", err, calls)
				}
			} else {
				if err != nil || calls != 1 || !d.Delivered(state.ControlEvent) {
					t.Fatal(err, calls)
				}
				if err = d.Deliver(state.ControlEvent, 0, external); err == nil || calls != 1 {
					t.Fatal("duplicate arrival")
				}
			}
			requireUnchanged(t, w, before)
			if s.ControlOrder() != 2 {
				t.Fatal("frontier changed")
			}
		})
	}
}
