package model

import (
	"reflect"
	"testing"
)

// runScheduled supplies only instruction bytes and explicitly delayed service
// responses. It never chooses a warp or waits for instruction retirement.
func runScheduled(t *testing.T, programs [4][]uint32, memoryDelay uint64, reverse bool) []CycleReport {
	t.Helper()
	var contexts [4]WarpContext
	for w, p := range programs {
		contexts[w] = WarpContext{Active: len(p) > 0, PC: uint32(0x100 * (w + 1)), Mask: 15, Epoch: uint64(w + 1)}
	}
	c, err := NewScheduledCore("std", contexts)
	if err != nil {
		t.Fatal(err)
	}
	type pending struct {
		s   Signal
		due uint64
	}
	var fetch, memory []pending
	var trace []CycleReport
	for edge := uint64(0); edge < 500; edge++ {
		fr, mr := Response{}, Response{}
		if len(fetch) > 0 && fetch[0].due <= edge {
			tok := fetch[0].s.Token
			pos := (tok.PC - contexts[tok.Warp].PC) / 4
			if int(pos) >= len(programs[tok.Warp]) {
				t.Fatalf("E%d fetched beyond control stall: %+v", edge, tok)
			}
			fr = responseFor(fetch[0].s)
			fr.Word = programs[tok.Warp][pos]
		}
		if len(memory) > 0 && memory[0].due <= edge {
			mr = responseFor(memory[0].s)
		}
		in := CoreInputs{FetchResponse: fr, MemoryResponse: mr, FetchReady: true, MemoryReady: true, ControlAllowed: true}
		p, err := c.Evaluate(in)
		if err != nil {
			t.Fatalf("E%d: %v", edge, err)
		}
		if reverse {
			q, err := c.Evaluate(in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.Report, q.Report) {
				t.Fatal("repeat evaluation changed report")
			}
			for i, j := 0, len(q.edits)-1; i < j; i, j = i+1, j-1 {
				q.edits[i], q.edits[j] = q.edits[j], q.edits[i]
			}
			p = q
		}
		commit(t, p.Transition)
		r := p.Report
		trace = append(trace, CycleReport{edge, r})
		if fr.Valid && r.FetchResponseReady {
			fetch = fetch[1:]
		}
		if mr.Valid && r.MemoryResponseReady {
			memory = memory[1:]
		}
		if r.FetchAccepted {
			fetch = append(fetch, pending{r.FetchRequest, edge + 2})
		}
		if r.MemoryAccepted {
			memory = append(memory, pending{r.MemoryRequest, edge + memoryDelay})
		}
		if c.Idle(len(fetch) == 0 && len(memory) == 0) {
			return trace
		}
	}
	t.Fatal("scheduled core failed to drain")
	return nil
}

func TestScheduledCoreFourWarpsAndLoadDependency(t *testing.T) {
	programs := [4][]uint32{
		{0x00002083, 0x00008133, 0x00000073}, // load x1; add x2,x1,x0; ecall
		{0x00100093, 0x00200113, 0x00000073},
		{0x00300093, 0x00400113, 0x00000073},
		{0x00500093, 0x00600113, 0x00000073},
	}
	trace := runScheduled(t, programs, 40, false)
	reversed := runScheduled(t, programs, 40, true)
	if !reflect.DeepEqual(trace, reversed) {
		t.Fatal("proposal order changed concurrent trace")
	}
	var fetches, issues, wbs [4][]uint64
	var maxResidents int
	for _, r := range trace {
		if !r.Scheduled {
			t.Fatal("scheduler missing")
		}
		if r.InstructionAccepted {
			fetches[r.Offered.Token.Warp] = append(fetches[r.Offered.Token.Warp], r.Cycle)
		}
		if r.Issued.Valid {
			issues[r.Issued.Token.Warp] = append(issues[r.Issued.Token.Warp], r.Cycle)
		}
		if r.Writeback.Valid {
			wbs[r.Writeback.Token.Warp] = append(wbs[r.Writeback.Token.Warp], r.Cycle)
		}
		unique := map[uint64]bool{}
		for _, resource := range r.Resources {
			if resource.Occupancy > resource.Capacity {
				t.Fatal("capacity exceeded", resource)
			}
			for _, resident := range resource.Residents {
				unique[resident.Token.ID] = true
			}
		}
		if len(unique) > maxResidents {
			maxResidents = len(unique)
		}
	}
	// Service response at E5 -> IBuffer E5 -> staging E6 -> reserve E7.
	// Decode unlock is consumed E6, so the next fetch can be selected E7.
	wantFetch := [4][]uint64{{0, 7, 14}, {1, 8, 15}, {2, 9, 16}, {3, 10, 17}}
	wantIssue := [4][]uint64{{7, 59, 60}, {8, 15, 22}, {9, 16, 23}, {10, 17, 24}}
	wantWB := [4][]uint64{{58, 67, 68}, {16, 23, 30}, {17, 24, 31}, {18, 25, 32}}
	if !reflect.DeepEqual(fetches, wantFetch) || !reflect.DeepEqual(issues, wantIssue) || !reflect.DeepEqual(wbs, wantWB) {
		t.Fatal("unexpected connected event cycles", fetches, issues, wbs)
	}
	if maxResidents < 4 {
		t.Fatal("no real concurrent residency")
	}
	for w := range programs {
		if len(fetches[w]) != 3 || len(issues[w]) != 3 || len(wbs[w]) != 3 {
			t.Fatalf("warp%d incomplete", w)
		}
	}
	if issues[0][1] != wbs[0][0]+1 {
		t.Fatal("dependent issue must follow final WB by one edge", issues, wbs)
	}
	for w := 1; w < 4; w++ {
		if wbs[w][2] >= wbs[0][0] {
			t.Fatal("load-blocked warp prevented other warps completing")
		}
	}
}

func TestScheduledCorePackedWAWAndOtherWarp(t *testing.T) {
	trace := runScheduled(t, [4][]uint32{{0x0800100b, 0x00000073}, {0x00100093, 0x00200113, 0x00000073}}, 12, false)
	var issues, wbs []uint64
	otherFinished := uint64(0)
	for _, r := range trace {
		if r.Issued.Valid && r.Issued.Token.Pack != 0 {
			issues = append(issues, r.Cycle)
		}
		if r.Writeback.Valid && r.Writeback.Token.Pack != 0 {
			wbs = append(wbs, r.Cycle)
		}
		if r.Writeback.Valid && r.Writeback.Token.Warp == 1 {
			otherFinished = r.Cycle
		}
	}
	if len(issues) != 4 || len(wbs) != 4 {
		t.Fatal("packed uop accounting", issues, wbs)
	}
	for u := 1; u < 4; u++ {
		if issues[u] != wbs[u-1]+1 {
			t.Fatal("packed WAW release boundary", issues, wbs)
		}
	}
	if otherFinished >= wbs[3] {
		t.Fatal("packed warp blocked another warp")
	}
	if !reflect.DeepEqual(issues, []uint64{8, 32, 56, 80}) || !reflect.DeepEqual(wbs, []uint64{31, 55, 79, 103}) || otherFinished != 30 {
		t.Fatal("packed connected cycles", issues, wbs, otherFinished)
	}
}

func TestScheduledFrontendResourceBackpressure(t *testing.T) {
	// Hold ALU Dispatch output until E120. Warp0 keeps offering independent
	// ALU operations, while Warp1 uses LSU credits and the shared Collector.
	// Responses below model an explicit diagnostic FU, not memory latency.
	contexts := [4]WarpContext{{Active: true, PC: 0x100, Mask: 15}, {Active: true, PC: 0x200, Mask: 15}}
	f, err := NewScheduledFrontend(contexts)
	if err != nil {
		t.Fatal(err)
	}
	type service struct {
		s   Signal
		due uint64
	}
	var fetch, wb []service
	var issued [2][]uint64
	var pops [2][]uint64
	var sawFull bool
	var held Signal
	for edge := uint64(0); edge < 240; edge++ {
		response := Response{}
		if len(fetch) > 0 && fetch[0].due <= edge {
			tok := fetch[0].s.Token
			response = responseFor(fetch[0].s)
			n := (tok.PC - contexts[tok.Warp].PC) / 4
			if n < 12 {
				response.Word = 0x00000013
				if tok.Warp == 1 {
					response.Word = 0x00002003
				}
			} else if n == 12 {
				response.Word = 0x00000073
			} else {
				t.Fatal("control stall bypass")
			}
		}
		writeback := Signal{}
		if len(wb) > 0 && wb[0].due <= edge {
			writeback = wb[0].s
			wb = wb[1:]
		}
		downstream := [4]bool{edge >= 120, true, true, true}
		before := f.Outputs()[0]
		if held.Valid && before != held {
			t.Fatal("held Dispatch token changed", edge)
		}
		held = Signal{}
		if before.Valid && !downstream[0] {
			held = before
		}
		p, err := f.Evaluate(Signal{}, response, true, true, downstream, writeback)
		if err != nil {
			t.Fatalf("E%d %v", edge, err)
		}
		score := f.ScoreboardState()
		if edge < 120 && score.Credits[0] > 3 {
			t.Fatal("spaced ALU stream bypassed old goingfull guard")
		}
		for w, pop := range p.IBufferPop {
			if pop && w < 2 {
				pops[w] = append(pops[w], edge)
			}
		}
		if p.Issued.Valid {
			issued[p.Issued.Token.Warp] = append(issued[p.Issued.Token.Warp], edge)
		}
		for _, r := range f.Resources() {
			if r.ID == "b-ibuffer/warp0" && r.Occupancy == 4 {
				sawFull = true
			}
			if r.Occupancy > r.Capacity {
				t.Fatal(r)
			}
		}
		commit(t, p.Transition)
		if response.Valid && p.ResponseReady {
			fetch = fetch[1:]
		}
		if p.RequestAccepted {
			fetch = append(fetch, service{p.Request, edge + 2})
		}
		for class, released := range p.Releases {
			if released {
				wb = append(wb, service{p.Outputs[class], edge + 2})
			}
		}
	}
	if !sawFull || len(issued[0]) != 13 || len(issued[1]) != 13 || len(pops[0]) != 13 || len(pops[1]) != 13 {
		t.Fatal("buffering/drain", sawFull, issued, pops)
	}
	if f.Credits() != [4]int{} {
		t.Fatal("credits did not balance", f.Credits())
	}
	wantIssue := [2][]uint64{{7, 14, 21, 123, 124, 125, 130, 131, 132, 138, 145, 152, 159}, {8, 15, 22, 29, 36, 43, 50, 57, 64, 71, 78, 85, 122}}
	wantPop := [2][]uint64{{6, 13, 20, 27, 123, 124, 125, 130, 131, 137, 144, 151, 158}, {7, 14, 21, 28, 35, 42, 49, 56, 63, 70, 77, 84, 91}}
	if !reflect.DeepEqual(issued, wantIssue) || !reflect.DeepEqual(pops, wantPop) {
		t.Fatal("resource wakeup/pop cycles", issued, pops)
	}
	for _, r := range f.Resources() {
		if r.Occupancy != 0 {
			t.Fatal("resource not drained", r)
		}
	}
}

func TestScheduledFrontendHeldFetchContextAndInactiveWarps(t *testing.T) {
	f, err := NewScheduledFrontend([4]WarpContext{
		{Active: true, PC: 0x100, Mask: 3, Epoch: 9},
		{Active: true, PC: 0x200, Mask: 12, Epoch: 8},
		{PC: 0x300, Mask: 15},
		{Active: true, Stalled: true, PC: 0x400, Mask: 15},
	})
	if err != nil {
		t.Fatal(err)
	}
	var held Signal
	var accepted []uint64
	type arrival struct {
		r   Response
		due uint64
	}
	var responses []arrival
	for edge := uint64(0); edge < 24; edge++ {
		response := Response{}
		if len(responses) > 0 && responses[0].due <= edge {
			response = responses[0].r
		}
		p, err := f.Evaluate(Signal{}, response, edge >= 10, true, [4]bool{true, true, true, true}, Signal{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Request.Valid && !p.RequestAccepted {
			if held.Valid && held != p.Request {
				t.Fatal("Fetch payload changed under backpressure", edge)
			}
			held = p.Request
		}
		if p.RequestAccepted {
			if held.Valid && held != p.Request {
				t.Fatal("Fetch acceptance changed held payload")
			}
			held = Signal{}
			accepted = append(accepted, edge)
			tok := p.Request.Token
			w := len(accepted) - 1
			if int(tok.Warp) != w || tok.PC != uint32(0x100*(w+1)) || tok.Mask != []uint8{3, 12}[w] || tok.Epoch != []uint64{9, 8}[w] || tok.ID != uint64(w+1) {
				t.Fatal("Fetch context/selection", tok)
			}
			r := responseFor(p.Request)
			r.Word = 0x00000073
			responses = append(responses, arrival{r, edge + 2})
		}
		if p.Decoded.Valid && (p.Decoded.Token.Warp > 1 || !p.Decoded.Token.WarpStall) {
			t.Fatal("decode context lost")
		}
		commit(t, p.Transition)
		if response.Valid && p.ResponseReady {
			responses = responses[1:]
		}
	}
	if !reflect.DeepEqual(accepted, []uint64{10, 11}) {
		t.Fatal("fetch acceptance cycles", accepted)
	}
	before := f.Resources()
	if _, err = f.Evaluate(valid(99), Response{}, true, true, [4]bool{}, Signal{}); err == nil {
		t.Fatal("autonomous scheduler accepted external selection")
	}
	if _, err = f.Evaluate(Signal{}, Response{Valid: true, Warp: 4}, true, true, [4]bool{}, Signal{}); err == nil {
		t.Fatal("invalid response warp")
	}
	if !reflect.DeepEqual(before, f.Resources()) {
		t.Fatal("invalid evaluation changed resources")
	}
	commit(t, f.Flush())
	state, ok := f.SchedulerState()
	if !ok {
		t.Fatal("scheduler disappeared")
	}
	for _, w := range state.Warps {
		if w.Active {
			t.Fatal("flush restarted autonomous fetch")
		}
	}
	for _, r := range f.Resources() {
		if r.Occupancy != 0 {
			t.Fatal("flush left per-warp resource", r)
		}
	}
}
