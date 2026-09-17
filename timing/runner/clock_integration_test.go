package runner

import (
	"encoding/binary"
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

// Cross callback boundaries on the same Clock: execution, epoch reset,
// execution, resumable D visibility, and resumable D/I flush. Compare complete
// records as well as architectural results at identical simulated boundaries.
func TestPersistentClockEpochVisibilityChunks(t *testing.T) {
	type result struct {
		Records  []MultiRecord
		Owners   [4]state.WarpSnapshot
		Memory   []byte
		Cycles   [3]uint64
		Counters [3]isa.CounterView
		Retired  [4]uint64
	}
	var want result
	for index, mode := range baselineChunks {
		t.Run(mode.name, func(t *testing.T) {
			f := newBaselineFixture(t, "multi", true, false)
			r := f.multi
			clock := r.clock
			var got result
			observe := func(rec MultiRecord) {
				// Services is a current inventory, not an event sequence. The
				// existing production comparator omits epoch for equal ID/uop/
				// resource keys, so old and new epoch tails can exchange order.
				// Compare every service value without imposing that map order;
				// Events, transfers, resources and all other slices stay ordered.
				rec.Services = append([]ServiceState(nil), rec.Services...)
				sort.Slice(rec.Services, func(i, j int) bool {
					a, _ := json.Marshal(rec.Services[i])
					b, _ := json.Marshal(rec.Services[j])
					return string(a) < string(b)
				})
				got.Records = append(got.Records, rec)
			}
			// Reset before instruction execution, while cache initialization is live.
			for j := 0; r.Cycle() < 5; j++ {
				if err := r.Run(min(mode.chunks[j%len(mode.chunks)], 5-r.Cycle()), observe); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Flush(); err != nil {
				t.Fatal(err)
			}
			if r.Cycle() != 5 || r.epoch != 2 || r.clock != clock {
				t.Fatal("epoch reset changed clock ownership or cycle")
			}
			if err := f.drive(mode.chunks, observe); err != nil {
				t.Fatal(err)
			}
			save := func(i int) {
				got.Cycles[i] = r.Cycle()
				got.Counters[i] = isa.CounterView{Cycle: r.core.Cycles(), Instret: r.core.Instret()}
				next, err := r.hierarchy.system.NextCycle()
				if err != nil || next != r.Cycle() || r.clock != clock {
					t.Fatal("memory/clock alignment", next, r.Cycle(), err)
				}
			}
			save(0)
			for stage, operation := range []func(uint64) (bool, error){r.MakeVisible, r.FlushCaches} {
				before := r.Cycle()
				if done, err := operation(0); done || err != nil || r.Cycle() != before {
					t.Fatal("zero budget advanced control operation", done, err)
				}
				done := false
				for j := 0; j < 10000 && !done; j++ {
					var err error
					done, err = operation(mode.chunks[j%len(mode.chunks)])
					if err != nil {
						t.Fatal(err)
					}
				}
				if !done || r.Cycle() <= before {
					t.Fatal("control operation did not finish")
				}
				save(stage + 1)
			}
			before := r.Cycle()
			if done, err := r.MakeVisible(32); !done || err != nil || r.Cycle() != before {
				t.Fatal("completed visibility repeated edges", done, err)
			}
			for w, owner := range r.owners {
				var err error
				got.Owners[w], err = owner.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
			}
			var err error
			got.Memory, err = f.ram.ReadBytes(0, 4096)
			if err != nil {
				t.Fatal(err)
			}
			got.Retired = r.Retired()
			if binary.LittleEndian.Uint32(got.Memory[0x804:]) != 17 {
				t.Fatal("lost store")
			}
			if index == 0 {
				want = got
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("epoch/visibility/cache flush result differs: cycles %v/%v counters %v/%v", got.Cycles, want.Cycles, got.Counters, want.Counters)
			}
		})
	}
}
