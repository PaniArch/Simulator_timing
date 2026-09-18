package runner_test

import (
	"encoding/binary"
	"reflect"
	"testing"
	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/runner"
)

func multiSetup(t *testing.T) ([4]*state.WarpState, *memory.Memory) {
	owners, ram, _ := multiSetupMemory(t, false)
	return owners, ram
}

// A separate original CTA owner supplies the fast packed path. Timing and
// functional references use identical addresses/data; no service delay callback.
func multiSetupMemory(t *testing.T, localPacked bool) ([4]*state.WarpState, *memory.Memory, warp.MemoryService) {
	t.Helper()
	ram, err := memory.New(0x12000)
	if err != nil {
		t.Fatal(err)
	}
	var local warp.MemoryService = ram
	if localPacked {
		manager := core.NewCTAManager()
		_, err := manager.Admit(core.CTAConfig{ID: 3, WarpIDs: []uint8{3}, StartupPC: 0x400, BlockDimensions: [3]uint32{4, 1, 1}, GridDimensions: [3]uint32{1, 1, 1}, BlockSize: 4, WarpStep: [3]uint32{4, 0, 0}, LocalMemorySize: 64, ClusterDimensions: [3]uint32{1, 1, 1}, ClusterSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		local, err = core.NewCTAMemory(manager, 3, ram)
		if err != nil {
			t.Fatal(err)
		}
	}
	programs := [4][]uint32{
		{0x0000a183, 0x00400213, 0x022102b3, 0x0000000b}, // long load; independent add/mul; TMC
		{0x00000217, 0x00900293, 0x0050a223, 0x0000000b}, // AUIPC; add; store; TMC
		{0x002081d3, 0x10208253, 0x00600313, 0x0000000b}, // FADD; FMUL; integer; TMC
		{0x0820918b, 0x00700393, 0x0000000b},             // packed-byte f3,x1,x2; integer; TMC
	}
	var owners [4]*state.WarpState
	for w, program := range programs {
		init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: uint8(w), PC: uint32(0x100 * (w + 1)), ActiveMask: 15, Lifecycle: state.WarpRunning}
		for lane := uint8(0); lane < 4; lane++ {
			l := state.LaneInitial{ID: lane}
			l.GPR[1] = uint32(0x10800 + w*0x100 + int(lane)*16)
			l.GPR[2] = 3
			if w == 3 {
				l.GPR[2] = 1
				if localPacked {
					l.GPR[1] = isa.FrozenLocalMemBase + uint32(lane)*16
				}
			}
			l.FPR[1] = 0x3f800000
			l.FPR[2] = 0x40400000
			l.FPR[3] = 0xaaaaaaaa
			init.Lanes = append(init.Lanes, l)
			route := warp.MemoryService(ram)
			if w == 3 {
				route = local
			}
			if err = route.Write(l.GPR[1], []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
				t.Fatal(err)
			}
		}
		owners[w], err = state.NewWarp(init)
		if err != nil {
			t.Fatal(err)
		}
		for n, word := range program {
			var bytes [4]byte
			binary.LittleEndian.PutUint32(bytes[:], word)
			if err = ram.Write(init.PC+uint32(4*n), bytes[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return owners, ram, local
}

func TestMultiRunnerConcurrentFunctionalResults(t *testing.T) {
	refs, refRAM, refLocal := multiSetupMemory(t, true)
	for _, owner := range refs {
		route := warp.MemoryService(refRAM)
		snapshot, _ := owner.Snapshot()
		if snapshot.WarpID() == 3 {
			route = refLocal
		}
		functional, err := warp.NewWithMemory(owner, route)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 8; n++ {
			s, _ := owner.Snapshot()
			if s.ActiveMask() == 0 {
				break
			}
			result := functional.Step(state.ReadContext{})
			if result.Outcome != warp.OutcomeRetired {
				t.Fatal("reference", result)
			}
		}
	}
	var previous []runner.MultiRecord
	for trial := 0; trial < 3; trial++ {
		owners, ram, local := multiSetupMemory(t, true)
		options := runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: kernelMemoryConfig(100)}, DataMemory: [4]warp.MemoryService{nil, nil, nil, local}}
		if trial == 2 {
			options.Ready = func(c uint64) bool { return c%4 != 0 }

		}
		r, err := runner.NewMulti(owners, ram, options)
		if err != nil {
			t.Fatal(err)
		}
		var trace []runner.MultiRecord
		observe := func(record runner.MultiRecord) { trace = append(trace, record) }
		for i := 0; i < 2000 && r.InFlight() < 2 && !r.Completed(); i++ {
			if err = r.Run(1, observe); err != nil {
				t.Fatal(err)
			}
		}
		if r.Completed() || r.InFlight() < 2 {
			t.Fatal("no overlap or premature completion")
		}
		if err = r.Run(10000, observe); err != nil {
			t.Fatal(err)
		}
		if !r.Completed() || r.InFlight() != 0 || r.Retired() != [4]uint64{4, 4, 4, 3} {
			t.Fatal("incomplete concurrent run", r.Retired(), r.InFlight())
		}
		for w, owner := range owners {
			got, _ := owner.Snapshot()
			want, _ := refs[w].Snapshot()
			if got != want {
				t.Fatalf("warp%d final state differs\ngot %+v\nwant %+v", w, got, want)
			}
		}
		if done, err := r.MakeVisible(10000); err != nil || !done {
			t.Fatal("visibility", done, err)
		}
		got, _ := ram.ReadBytes(0, 0x12000)
		want, _ := refRAM.ReadBytes(0, 0x12000)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("final memory differs")
		}
		maxSameWarp := 0
		multiEvent := false
		inactivePending := false
		loadWB, control := uint64(0), uint64(0)
		var execution [4][]uint64
		for _, rec := range trace {
			byWarp := [4]map[uint64]bool{}
			for w := range byWarp {
				byWarp[w] = map[uint64]bool{}
			}
			for _, resource := range rec.ResourcesAfter {
				for _, resident := range resource.Residents {
					tok := resident.Token
					byWarp[tok.Warp][tok.ID] = true
				}
			}
			for _, ids := range byWarp {
				if len(ids) > maxSameWarp {
					maxSameWarp = len(ids)
				}
			}
			for _, w := range rec.Warps {
				if !w.Active && w.Pending > 0 {
					inactivePending = true
				}
			}
			if rec.Report.InstructionAccepted {
				tok := rec.Report.Offered.Token
				if !rec.Warps[tok.Warp].Runnable {
					t.Fatal("fetch selected non-runnable warp")
				}
			}
			if rec.Report.Issued.Valid {
				tok := rec.Report.Issued.Token
				candidate := rec.Report.IssueCandidates[tok.Warp]
				if rec.Report.IssueSelected != int(tok.Warp) || !candidate.Eligible || candidate.Staged.Token.ID != tok.ID {
					t.Fatal("issue trace inconsistent with staging")
				}
			}
			rr := rec.Report
			for _, event := range rr.Executed {
				if event.Valid {
					execution[event.Token.Warp] = append(execution[event.Token.Warp], rec.Cycle)
				}
			}
			if rr.Read.Valid && rr.Writeback.Valid && rr.Read.Token.ID != rr.Writeback.Token.ID {
				multiEvent = true
			}
			if rr.Writeback.Valid && rr.Writeback.Token.Warp == 0 && rr.Writeback.Token.PC == 0x100 {
				loadWB = rec.Cycle
			}
			if rr.Control.Valid && rr.Control.Token.Warp == 0 {
				control = rec.Cycle
			}
		}
		if maxSameWarp < 2 || !multiEvent || !inactivePending || control == 0 || loadWB <= control {
			t.Fatal("missing same-warp/mixed event/late WB overlap", maxSameWarp, multiEvent, control, loadWB)
		}
		for w, cycles := range execution {
			progress := 0
			for _, cycle := range cycles {
				if cycle < loadWB {
					progress++
				}
			}
			required := 2
			if w == 3 {
				required = 1
			} // packed uops retain the RTL WAW release boundary
			if progress < required {
				t.Fatalf("warp%d lacks actual execution while load waits: %v, WB=%d", w, cycles, loadWB)
			}
		}
		if trial == 2 {
			firstResponseWarp := uint8(255)
			for _, record := range trace {
				if record.Report.MemoryResponse.Valid && record.Report.MemoryResponseReady {
					firstResponseWarp = record.Report.MemoryResponse.Warp
					break
				}
			}
			if firstResponseWarp != 3 {
				t.Fatal("request identity did not support reordered service", firstResponseWarp)
			}
		}
		if trial == 0 {
			previous = trace
		} else if trial == 1 && !reflect.DeepEqual(previous, trace) {
			t.Fatal("nondeterministic concurrent trace")
		}
	}
}
