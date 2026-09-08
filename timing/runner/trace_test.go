package runner_test

import (
	"encoding/binary"
	"testing"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/runner"
)

func TestResidentTraceAndLocalLatency(t *testing.T) {
	owner, ram := setup(t)
	r, err := runner.New(owner, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 7})
	if err != nil {
		t.Fatal(err)
	}
	active := map[string]map[uint64]bool{}
	divider := 0
	kinds := map[string]bool{}
	err = r.Run(600, func(record runner.Record) {
		for _, resource := range record.ResourcesAfter {
			if len(resource.Residents) != resource.Occupancy || resource.Occupancy > resource.Capacity {
				t.Fatal("incomplete resident snapshot", resource)
			}
		}
		for _, event := range record.Events {
			kinds[event.Kind] = true
			if event.Resource == "tm-idiv-result" && event.Kind != "leave" {
				divider++
			}
			if event.Kind != "enter" && event.Kind != "leave" && event.Kind != "stay" && event.Kind != "advance" {
				continue
			}
			if active[event.Resource] == nil {
				active[event.Resource] = map[uint64]bool{}
			}
			resident := active[event.Resource]
			switch event.Kind {
			case "enter":
				if resident[event.Token.ID] {
					t.Fatal("duplicate resource entry", event)
				}
				resident[event.Token.ID] = true
			case "leave":
				if !resident[event.Token.ID] {
					t.Fatal("leave without enter", event)
				}
				delete(resident, event.Token.ID)
			default:
				if !resident[event.Token.ID] {
					t.Fatal("residence without enter", event)
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if divider != 33 {
		t.Fatal("divider latency invisible or incorrect", divider)
	}
	for resource, residents := range active {
		if len(residents) != 0 {
			t.Fatal("undrained trace", resource)
		}
	}
	for _, kind := range []string{"enter", "stay", "advance", "leave", "complete", "read", "accept"} {
		if !kinds[kind] {
			t.Fatal("missing trace event", kind)
		}
	}
}

func TestPackedProgramFillsRequestCapacityAndRecovers(t *testing.T) {
	owner, ram := setup(t)
	pack, tmc := uint32(0), uint32(0)
	for _, entry := range isa.Catalog() {
		if entry.Name == "vx_packlb_f" {
			pack = entry.Example&^uint32(31<<7|31<<15|31<<20) | 3<<7 | 1<<15 | 2<<20
		}
		if entry.Name == "tmc" {
			tmc = entry.Example &^ uint32(31<<15)
		}
	}
	for i, word := range []uint32{pack, tmc} {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], word)
		if err := ram.Write(0x100+uint32(i)*4, b[:]); err != nil {
			t.Fatal(err)
		}
	}
	if err := owner.WriteRegister(isa.Register{File: isa.Integer, Index: 2}, 15, isa.LaneValues{1, 1, 1, 1}); err != nil {
		t.Fatal(err)
	}
	r, err := runner.New(owner, ram, runner.Options{Backend: "std", PeriodPS: 1, FetchCycles: 2, MemoryCycles: 5, Ready: func(c uint64) bool { return c < 10 || c >= 100 }})
	if err != nil {
		t.Fatal(err)
	}
	full := false
	requests := 0
	wb := map[uint8]int{}
	err = r.Run(400, func(record runner.Record) {
		for _, resource := range record.ResourcesAfter {
			if resource.Occupancy > resource.Capacity {
				t.Fatal("capacity overflow", resource)
			}
			if resource.ID == "b-lsu-req" && resource.Occupancy == resource.Capacity {
				full = true
			}
		}
		if record.Report.MemoryAccepted {
			if record.Cycle < 100 {
				t.Fatal("request bypassed backpressure")
			}
			requests++
		}
		if record.Report.Writeback.Valid && record.Report.Writeback.Token.ID == 1 {
			wb[record.Report.Writeback.Token.Uop]++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Completed() || !full || requests != 4 || len(wb) != 4 {
		t.Fatal("capacity/recovery", full, requests, wb, r.Completed())
	}
	for _, count := range wb {
		if count != 1 {
			t.Fatal("duplicate packed completion")
		}
	}
}
