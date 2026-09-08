package effects_test

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

type byteOwner struct {
	*memory.Memory
	batches, writes int
	failRead        bool
}

var errService = errors.New("injected memory read failure")

func (m *byteOwner) Read(address uint32, bytes []byte) error {
	if m.failRead {
		return errService
	}
	return m.Memory.Read(address, bytes)
}
func (m *byteOwner) Write(address uint32, bytes []byte) error {
	m.writes++
	return m.Memory.Write(address, bytes)
}
func (m *byteOwner) WriteBatch(addresses []uint32, bytes [][]byte) error {
	m.batches++
	return m.Memory.WriteBatch(addresses, bytes)
}
func memoryInitial(t *testing.T, word uint32) (state.WarpInitial, *byteOwner) {
	t.Helper()
	init := initial()
	ram, e := memory.New(512)
	if e != nil {
		t.Fatal(e)
	}
	for lane := range init.Lanes {
		init.Lanes[lane].GPR[1] = uint32(32 + 8*lane)
		init.Lanes[lane].GPR[2] = uint32(0x12348000 + lane)
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(0x87654380+lane))
		if e = ram.Write(uint32(32+8*lane), b); e != nil {
			t.Fatal(e)
		}
	}
	code := make([]byte, 4)
	binary.LittleEndian.PutUint32(code, word)
	if e = ram.Write(init.PC, code); e != nil {
		t.Fatal(e)
	}
	return init, &byteOwner{Memory: ram}
}
func TestMemoryServiceAndPartialWBVisibility(t *testing.T) {
	for _, test := range []struct {
		word uint32
		fail bool
	}{
		{0x0000a183, false}, {0x00008183, false}, {0x0020a023, false}, {0x0000000f, false},
		{0x0000a183, true}, {0x0020a023, true},
	} {
		word := test.word
		isFence := word == 0x0000000f
		orderingCalls := 0
		init, ram := memoryInitial(t, word)
		_, refRAM := memoryInitial(t, word)
		owner, e := state.NewWarp(init)
		if e != nil {
			t.Fatal(e)
		}
		reference, _ := state.NewWarp(init)
		executor, e := warp.NewWithMemory(reference, refRAM)
		if e != nil {
			t.Fatal(e)
		}
		want := executor.Step(state.ReadContext{})
		if want.Outcome != warp.OutcomeRetired {
			t.Fatal(want)
		}
		core, e := model.NewCore("std")
		if e != nil {
			t.Fatal(e)
		}
		adapter, _ := effects.New(owner, 9, func(e isa.InstructionEffects) error {
			if !isFence || e.Ordering == nil {
				t.Fatal("unexpected forwarded group", e)
			}
			orderingCalls++
			return nil
		})
		if e = adapter.BindMemory(ram); e != nil {
			t.Fatal(e)
		}
		tok := model.Token{ID: 1, Epoch: 9, PC: init.PC, Mask: 15, Word: word}
		if e = adapter.Begin(tok); e != nil {
			t.Fatal(e)
		}
		in := model.Signal{Valid: true, Token: tok}
		fetch := model.Response{}
		response := model.Response{}
		var request model.Token
		due := uint64(1000)
		parts := 0
		pending := false
		done := false
		isStore := word == 0x0020a023
		originalBytes, _ := ram.ReadBytes(32, 28)
		for cycle := uint64(0); cycle < 150; cycle++ {
			beforeService := snap(t, owner)
			if cycle == due || !isStore && !isFence && cycle == due+6 {
				mask := uint8(5)
				if parts == 1 {
					mask = 10
				}
				if isStore || isFence {
					mask = 15
				}
				ram.failRead = test.fail
				response, e = adapter.Service(cycle, request, mask)
				if test.fail {
					var fault *warp.Fault
					if !errors.As(e, &fault) || fault.Kind != warp.FaultArchitectural || !errors.Is(e, errService) || len(fault.Architectural) == 0 {
						t.Fatal("untyped service fault", e)
					}
					if !reflect.DeepEqual(beforeService, snap(t, owner)) || response.Valid || ram.batches != 0 || ram.writes != 0 {
						t.Fatal("fault leaked effects")
					}
					if _, retryErr := adapter.Service(cycle+1, request, mask); retryErr == nil {
						t.Fatal("failed service replay accepted")
					}
					if adapter.Finish() == nil {
						t.Fatal("fault retired")
					}
					done = true
					break
				}
				if e != nil {
					t.Fatal("service", e)
				}
				parts++
				afterService := snap(t, owner)
				// Stores may have already completed in the LSU, but only the service writes bytes.
				if isStore {
					if !pending || ram.batches != 1 || ram.writes != 0 {
						t.Fatal("store completion/tail/atomic owner", pending, ram.batches, ram.writes)
					}
				} else if !reflect.DeepEqual(beforeService, afterService) {
					t.Fatal("load service changed canonical registers before WB")
				}
			}
			if cycle < due {
				if orderingCalls != 0 {
					t.Fatal("ordering before service")
				}
				got, _ := ram.ReadBytes(32, 28)
				if !reflect.DeepEqual(got, originalBytes) {
					t.Fatal("bytes changed before service")
				}
			}
			p, e := core.Evaluate(model.CoreInputs{Instruction: in, FetchResponse: fetch, MemoryResponse: response, FetchReady: true, MemoryReady: true, Eligible: true, ControlAllowed: true})
			if e != nil {
				t.Fatal(e)
			}
			if e = model.CommitEdge(p.Transition); e != nil {
				t.Fatal(e)
			}
			before := snap(t, owner)
			if e = adapter.Observe(cycle, p.Report, state.ReadContext{}); e != nil {
				t.Fatal("observe", cycle, e)
			}
			after := snap(t, owner)
			for lane := 0; lane < 4; lane++ {
				old, new := reg(t, before, isa.Integer, 3)[lane], reg(t, after, isa.Integer, 3)[lane]
				if old != new && (!p.Report.Writeback.Valid || p.Report.Writeback.Token.Mask&(1<<lane) == 0) {
					t.Fatal("register changed outside partial WB")
				}
			}
			if p.Report.InstructionAccepted {
				in = model.Signal{}
			}
			if fetch.Valid && p.Report.FetchResponseReady {
				fetch = model.Response{}
			}
			if p.Report.FetchAccepted {
				f := p.Report.FetchRequest.Token
				fetch = model.Response{Valid: true, ID: f.ID, Epoch: f.Epoch, Warp: f.Warp, Mask: f.Mask, Word: word}
			}
			if response.Valid && p.Report.MemoryResponseReady {
				response = model.Response{}
			}
			if p.Report.MemoryAccepted {
				request = p.Report.MemoryRequest.Token
				due = cycle + 15
			}
			if p.Report.PendingRelease.Valid {
				pending = true
			}
			expectedParts := 2
			if isStore || isFence {
				expectedParts = 1
			}
			if pending && parts == expectedParts && core.Idle(true) {
				if e = adapter.Finish(); e != nil {
					t.Fatal(e)
				}
				done = true
				break
			}
		}
		if test.fail {
			if !done {
				t.Fatal("fault test did not reach service")
			}
			continue
		}
		if isFence && orderingCalls != 1 {
			t.Fatal("ordering receipt", orderingCalls)
		}
		if !done || !reflect.DeepEqual(snap(t, owner), snap(t, reference)) {
			t.Fatal("memory final state mismatch", word, done, snap(t, owner), snap(t, reference))
		}
		got, _ := ram.ReadBytes(0, 512)
		expected, _ := refRAM.ReadBytes(0, 512)
		if !reflect.DeepEqual(got, expected) {
			t.Fatal("memory bytes differ from functional owner")
		}
	}
}
