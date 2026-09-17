package runner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

var baselineChunks = []struct {
	name   string
	chunks []uint64
}{
	{"single", []uint64{1}}, {"fixed", []uint64{32}}, {"irregular", []uint64{1, 7, 3, 29}},
}

type baselineFixture struct {
	multi  *MultiRunner
	kernel *Kernel
	ram    *memory.Memory
}

func newBaselineFixture(t testing.TB, kind string, diagnostics, fault bool) *baselineFixture {
	t.Helper()
	ram, err := memory.New(4096)
	if err != nil {
		t.Fatal(err)
	}
	put := func(address, word uint32) {
		var data [4]byte
		binary.LittleEndian.PutUint32(data[:], word)
		if err := ram.Write(address, data[:]); err != nil {
			t.Fatal(err)
		}
	}
	// Load, add, store to a separate word, stop. Kernel first reads its parameter
	// address. Four standalone warps have disjoint data; one Kernel CTA has 4 lanes.
	words := []uint32{0x0000a103, 0x00710113, 0x0020a223, 0x0000000b}
	if kind == "kernel" {
		words = append([]uint32{0x340020f3}, words...)
	}
	if fault {
		words[0] = 0xffffffff
	}
	for i, w := range words {
		put(0x100+uint32(i)*4, w)
	}
	for w := 0; w < 4; w++ {
		put(0x800+uint32(w)*16, uint32(10+w))
	}
	options := Options{Backend: "std", PeriodPS: 1, TraceMemory: diagnostics, MemoryConfig: &memsys.Config{Latency: 11, AcceptsPerCycle: 1, MaxInflight: 16, ReturnsPerCycle: 1}, Ready: func(c uint64) bool { return c%5 != 0 }}
	f := &baselineFixture{ram: ram}
	if kind == "kernel" {
		launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, ParameterAddress: 0x800, GridDimensions: [3]uint32{1, 1, 1}, BlockDimensions: [3]uint32{4, 1, 1}, BlockSize: 4, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
		f.kernel, err = NewKernel(launch, ram, options)
		if err != nil {
			t.Fatal(err)
		}
		// Fix the pure observation namespace before any execution. Routing identities,
		// generations, launch IDs and every identity in records remain compared intact.
		f.kernel.deviceID = 7001
		f.multi = f.kernel.runner
	} else {
		var owners [4]*state.WarpState
		for w := range owners {
			initial := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: uint8(w), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
			for lane := uint8(0); lane < 4; lane++ {
				l := state.LaneInitial{ID: lane}
				l.GPR[1] = 0x800 + uint32(w)*16
				initial.Lanes = append(initial.Lanes, l)
			}
			owners[w], err = state.NewWarp(initial)
			if err != nil {
				t.Fatal(err)
			}
		}
		f.multi, err = NewMulti(owners, ram, MultiOptions{Options: options})
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *baselineFixture) run(n uint64, observe func(MultiRecord)) error {
	if f.kernel != nil {
		return f.kernel.Run(n, observe)
	}
	return f.multi.Run(n, observe)
}
func (f *baselineFixture) complete() bool {
	if f.kernel != nil {
		return f.kernel.Status().Complete
	}
	return f.multi.Completed()
}
func (f *baselineFixture) drive(chunks []uint64, observe func(MultiRecord)) error {
	for j := 0; j < 10000; j++ {
		if f.complete() {
			return nil
		}
		if err := f.run(chunks[j%len(chunks)], observe); err != nil {
			return err
		}
	}
	return fmt.Errorf("baseline exceeded 10000 calls")
}

// Each execute operation runs one fresh, finite workload to completion. Fixture
// construction is outside timer/allocation accounting. init measures only that
// construction. Observer consumes records without retaining an unbounded trace.
func BenchmarkRunnerBaseline(b *testing.B) {
	for _, kind := range []string{"multi", "kernel"} {
		for _, mode := range baselineChunks {
			for _, diag := range []bool{false, true} {
				for _, phase := range []string{"init", "execute"} {
					b.Run(fmt.Sprintf("%s/%s/diag=%t/%s", kind, mode.name, diag, phase), func(b *testing.B) {
						var observe func(MultiRecord)
						var consumed uint64
						if diag {
							observe = func(r MultiRecord) { consumed += uint64(len(r.Events) + len(r.Finished)) }
						}
						b.ReportAllocs()
						var cycles uint64
						for i := 0; i < b.N; i++ {
							if phase == "execute" {
								b.StopTimer()
							}
							f := newBaselineFixture(b, kind, diag, false)
							if phase == "init" {
								continue
							}
							b.StartTimer()
							if err := f.drive(mode.chunks, observe); err != nil {
								b.Fatal(err)
							}
							cycles = f.multi.Cycle()
						}
						if phase == "execute" {
							b.ReportMetric(float64(cycles), "edges/op")
						}
						_ = consumed
					})
				}
			}
		}
	}
}

type baselineResult struct {
	Counters, FlushCounters isa.CounterView
	Cycle, FlushCycle       uint64
	Retired                 [4]uint64
	Owners                  [4]state.WarpSnapshot
	Before, After           []byte
	Status                  KernelStatus
	Events                  []KernelEvent
	Failure                 string
}

func baselineRun(t *testing.T, kind string, chunks []uint64, diag, fault bool) (baselineResult, []MultiRecord) {
	t.Helper()
	f := newBaselineFixture(t, kind, diag, fault)
	var records []MultiRecord
	var frozen [][]byte
	var observe func(MultiRecord)
	if diag {
		observe = func(r MultiRecord) {
			records = append(records, r)
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			frozen = append(frozen, data)
		}
	}
	err := f.drive(chunks, observe)
	result := baselineResult{Cycle: f.multi.Cycle(), Retired: f.multi.Retired(), Counters: isa.CounterView{Cycle: f.multi.core.Cycles(), Instret: f.multi.core.Instret()}}
	if fault {
		if err == nil {
			t.Fatal("expected decode failure")
		}
		result.Failure = err.Error()
		cycle := f.multi.Cycle()
		if f.run(32, observe) == nil || f.multi.Cycle() != cycle {
			t.Fatal("failed runner resumed")
		}
	} else if err != nil || !f.complete() {
		t.Fatal("incomplete", err)
	}
	for w, owner := range f.multi.owners {
		result.Owners[w], err = owner.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
	}
	result.Before, err = f.ram.ReadBytes(0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if !fault {
		done := false
		for j := 0; j < 10000 && !done; j++ {
			budget := chunks[j%len(chunks)]
			if f.kernel != nil {
				done, err = f.kernel.FlushCaches(budget)
			} else {
				done, err = f.multi.FlushCaches(budget)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !done {
			t.Fatal("flush incomplete")
		}
	}
	result.FlushCycle = f.multi.Cycle()
	result.FlushCounters = isa.CounterView{Cycle: f.multi.core.Cycles(), Instret: f.multi.core.Instret()}
	result.After, err = f.ram.ReadBytes(0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if f.kernel != nil {
		result.Status = f.kernel.Status()
		result.Events = f.kernel.TakeEvents()
	}
	// Full records are retained across all later edges and flush, checking detached
	// ownership as well as ordered equality between independent executions.
	for i, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(data, frozen[i]) {
			t.Fatalf("historical record %d mutated", i)
		}
	}
	return result, records
}
func TestRunnerBaselineChunkEquivalence(t *testing.T) {
	for _, kind := range []string{"multi", "kernel"} {
		for _, fault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fault=%t", kind, fault), func(t *testing.T) {
				want, trace := baselineRun(t, kind, []uint64{1}, true, fault)
				if len(trace) == 0 {
					t.Fatal("empty trace")
				}
				if !fault {
					expected := uint64(16)
					if kind == "kernel" {
						expected = 5
					}
					var total uint64
					for _, n := range want.Retired {
						total += n
					}
					if total != expected {
						t.Fatalf("retired %d want %d", total, expected)
					}
					if binary.LittleEndian.Uint32(want.After[0x804:]) != 17 || binary.LittleEndian.Uint32(want.Before[0x804:]) != 0 {
						t.Fatal("store/flush value")
					}
				}
				for _, mode := range baselineChunks {
					for _, diag := range []bool{false, true} {
						got, records := baselineRun(t, kind, mode.chunks, diag, fault)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("%s diag=%t architecture mismatch\ngot %+v\nwant %+v", mode.name, diag, got, want)
						}
						if diag && !reflect.DeepEqual(records, trace) {
							t.Fatalf("%s ordered full diagnostic records differ", mode.name)
						}
					}
				}
			})
		}
	}
}
