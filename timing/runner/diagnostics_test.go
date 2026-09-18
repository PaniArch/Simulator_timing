package runner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// Poison exported containers in a delivered record, including nested resource,
// feedback/memory slices and per-token binding maps. Owners must stay unchanged.
func poisonDiagnosticSlices(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			poisonDiagnosticSlices(v.Field(i))
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			poisonDiagnosticSlices(v.Index(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			poisonDiagnosticSlices(v.Index(i))
			if v.Index(i).CanSet() {
				v.Index(i).SetZero()
			}
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			v.SetMapIndex(key, reflect.Value{})
		}
	}
}

func diagnosticState(t *testing.T, f *baselineFixture) baselineResult {
	t.Helper()
	r := baselineResult{Cycle: f.multi.Cycle(), Retired: f.multi.Retired(), Counters: isa.CounterView{Cycle: f.multi.core.Cycles(), Instret: f.multi.core.Instret()}}
	for w, owner := range f.multi.owners {
		var err error
		r.Owners[w], err = owner.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	r.Before, err = f.ram.ReadBytes(0, f.ram.Size())
	if err != nil {
		t.Fatal(err)
	}
	if f.kernel != nil {
		r.Status = f.kernel.Status()
		r.Events = f.kernel.TakeEvents()
	}
	return r
}

// Reference runs observe every edge. The paired runner alternates nil and
// non-nil callbacks across Run boundaries, with independent memory trace flags.
// Ordered records at observed edges and full state at *every* budget boundary
// must match, including the failure edge and attempted continuation afterward.
func TestDiagnosticObserverSwitching(t *testing.T) {
	for _, kind := range []string{"multi", "kernel"} {
		for _, fault := range []bool{false, true} {
			for _, traceMemory := range []bool{false, true} {
				for _, mode := range baselineChunks {
					t.Run(fmt.Sprintf("%s/fault=%t/memory=%t/%s", kind, fault, traceMemory, mode.name), func(t *testing.T) {
						ref := newBaselineFixture(t, kind, traceMemory, fault)
						got := newBaselineFixture(t, kind, traceMemory, fault)
						var saved []MultiRecord
						var frozen [][]byte
						observed, unobserved := 0, 0
						for call := 0; call < 10000; call++ {
							var want []MultiRecord
							n := mode.chunks[call%len(mode.chunks)]
							// Zero budgets must neither consume pending observations nor call back.
							if call%5 == 0 {
								n = 0
							}
							errRef := ref.run(n, func(r MultiRecord) { want = append(want, r) })
							var observe func(MultiRecord)
							index := 0
							if call%2 == 0 {
								observe = func(r MultiRecord) {
									if index >= len(want) || !reflect.DeepEqual(r, want[index]) {
										t.Fatalf("ordered record differs at call %d record %d cycle %d", call, index, r.Cycle)
									}
									index++
									observed++
									if call%4 == 0 {
										// Exercise caller mutation independently of retained history.
										poisonDiagnosticSlices(reflect.ValueOf(&r).Elem())
									} else {
										saved = append(saved, r)
										data, err := json.Marshal(r)
										if err != nil {
											t.Fatal(err)
										}
										frozen = append(frozen, data)
									}
								}
							} else {
								unobserved += len(want)
							}
							errGot := got.run(n, observe)
							if fmt.Sprint(errGot) != fmt.Sprint(errRef) {
								t.Fatalf("error changed: %v / %v", errGot, errRef)
							}
							if observe != nil && index != len(want) {
								t.Fatal("missing callbacks")
							}
							if !reflect.DeepEqual(diagnosticState(t, got), diagnosticState(t, ref)) || got.complete() != ref.complete() {
								t.Fatalf("state changed at call %d", call)
							}
							if errGot != nil {
								if got.run(7, nil) == nil || ref.run(7, nil) == nil {
									t.Fatal("failed execution resumed")
								}
								break
							}
							if got.complete() {
								break
							}
							if call == 9999 {
								t.Fatal("budget bound exceeded")
							}
						}
						if observed == 0 || unobserved == 0 || len(saved) == 0 {
							t.Fatal("missing diagnostic modes")
						}
						if !fault {
							for _, f := range []*baselineFixture{ref, got} {
								done := false
								for j := 0; j < 10000 && !done; j++ {
									var err error
									if f.kernel != nil {
										done, err = f.kernel.FlushCaches(mode.chunks[j%len(mode.chunks)])
									} else {
										done, err = f.multi.FlushCaches(mode.chunks[j%len(mode.chunks)])
									}
									if err != nil {
										t.Fatal(err)
									}
								}
								if !done {
									t.Fatal("flush budget exceeded")
								}
							}
						}
						if !reflect.DeepEqual(diagnosticState(t, got), diagnosticState(t, ref)) {
							t.Fatal("final state/flush differs")
						}
						for i, r := range saved {
							data, err := json.Marshal(r)
							if err != nil || !reflect.DeepEqual(data, frozen[i]) {
								t.Fatalf("retained record %d mutated", i)
							}
						}
					})
				}
			}
		}
	}
}

// Recovery notifications are consumed at their original edge even when that
// edge has no observer; enabling diagnostics must not replay discarded events.
func TestDiagnosticRecoverySwitching(t *testing.T) {
	for _, visibleRecovery := range []bool{false, true} {
		for _, mode := range baselineChunks {
			t.Run(fmt.Sprintf("visible=%t/%s", visibleRecovery, mode.name), func(t *testing.T) {
				ref := newBaselineFixture(t, "multi", true, false)
				got := newBaselineFixture(t, "multi", true, false)
				normalize := func(r MultiRecord) MultiRecord {
					// Only the existing cross-epoch Services inventory limitation is normalized.
					// Events, memory handshakes and every other ordered sequence stay intact.
					sort.Slice(r.Services, func(i, j int) bool {
						a, _ := json.Marshal(r.Services[i])
						b, _ := json.Marshal(r.Services[j])
						return string(a) < string(b)
					})
					return r
				}
				run := func(n uint64, visible bool) {
					t.Helper()
					var expected []MultiRecord
					if err := ref.run(n, func(r MultiRecord) { expected = append(expected, normalize(r)) }); err != nil {
						t.Fatal(err)
					}
					var observe func(MultiRecord)
					index := 0
					if visible {
						observe = func(r MultiRecord) {
							if index >= len(expected) || !reflect.DeepEqual(normalize(r), expected[index]) {
								t.Fatalf("recovery trace mismatch at cycle %d", r.Cycle)
							}
							index++
						}
					}
					if err := got.run(n, observe); err != nil {
						t.Fatal(err)
					}
					if visible && index != len(expected) {
						t.Fatal("missing recovery callback")
					}
					if !reflect.DeepEqual(diagnosticState(t, got), diagnosticState(t, ref)) {
						t.Fatal("recovery state mismatch")
					}
				}
				run(5, true)
				for _, f := range []*baselineFixture{ref, got} {
					if err := f.multi.Cancel(model.Cancellation{Warp: 0, Epoch: 1, Through: 20}); err != nil {
						t.Fatal(err)
					}
					if err := f.multi.Restart(0, model.WarpContext{Active: true, PC: 0x100, Mask: 15, Epoch: 1}); err != nil {
						t.Fatal(err)
					}
				}
				run(0, !visibleRecovery)
				run(1, visibleRecovery)
				run(4, !visibleRecovery)
				for _, f := range []*baselineFixture{ref, got} {
					if err := f.multi.Flush(); err != nil {
						t.Fatal(err)
					}
				}
				run(0, !visibleRecovery)
				run(1, visibleRecovery)
				for j := 0; !got.complete() && j < 10000; j++ {
					run(mode.chunks[j%len(mode.chunks)], j%2 == 0)
				}
				if !got.complete() || !ref.complete() {
					t.Fatal("recovery never completed")
				}
				for _, operation := range []func(*MultiRunner, uint64) (bool, error){(*MultiRunner).MakeVisible, (*MultiRunner).FlushCaches} {
					for _, f := range []*baselineFixture{ref, got} {
						done := false
						for j := 0; j < 10000 && !done; j++ {
							var err error
							done, err = operation(f.multi, mode.chunks[j%len(mode.chunks)])
							if err != nil {
								t.Fatal(err)
							}
						}
						if !done {
							t.Fatal("visibility/flush exceeded bound")
						}
					}
					if !reflect.DeepEqual(diagnosticState(t, got), diagnosticState(t, ref)) {
						t.Fatal("visibility/flush differs")
					}
				}
			})
		}
	}
}

// Kernel's internal retirement and barrier callback must remain active with no
// user observer, including repeated barrier phases and CTA generation reuse.
func TestDiagnosticKernelBarrierControl(t *testing.T) {
	fixture := func(t *testing.T) *baselineFixture {
		t.Helper()
		f := newBaselineFixture(t, "kernel", true, false)
		var bar uint32
		for _, e := range isa.Catalog() {
			if e.Name == "bar" {
				bar = e.Example&^uint32(31<<7|31<<15|31<<20) | 1<<15 | 2<<20
			}
		}
		if bar == 0 {
			t.Fatal("missing BAR")
		}
		// Address the first physical Warp of this CTA (warp ID minus rank).
		// Two Warp arrivals per phase, then increment x3; two phases before TMC.
		for i, word := range []uint32{0xcc1<<20 | 2<<12 | 1<<7 | 0x73, 0xcd1<<20 | 2<<12 | 4<<7 | 0x73, 0x404080b3, 0x00200113, bar, 0x00118193, bar, 0x00118193, 0x0000000b} {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], word)
			if err := f.ram.Write(0x100+uint32(i)*4, b[:]); err != nil {
				t.Fatal(err)
			}
		}
		launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, ParameterAddress: 0x800, ClusterDimensions: [3]uint32{1, 1, 1}, LocalMemorySize: 64}
		launch.BlockSize = 8
		launch.BlockDimensions = [3]uint32{8, 1, 1}
		launch.WarpStep = [3]uint32{4, 0, 0}
		launch.GridDimensions = [3]uint32{3, 1, 1}
		var err error
		f.kernel, err = NewKernel(launch, f.ram, f.kernel.options)
		if err != nil {
			t.Fatal(err)
		}
		f.kernel.deviceID = 7001
		f.multi = f.kernel.runner
		return f
	}
	for _, mode := range baselineChunks {
		for _, switching := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/switching=%t", mode.name, switching), func(t *testing.T) {
				ref, got := fixture(t), fixture(t)
				wakes := 0
				for call := 0; call < 10000 && !got.complete(); call++ {
					var want []MultiRecord
					n := mode.chunks[call%len(mode.chunks)]
					if err := ref.run(n, func(r MultiRecord) {
						want = append(want, r)
						for _, f := range r.Report.Wakeups {
							if f.Kind == model.FeedbackWake {
								wakes++
							}
						}
					}); err != nil {
						t.Fatal(err)
					}
					var observe func(MultiRecord)
					index := 0
					if switching && call%2 == 0 {
						observe = func(r MultiRecord) {
							if index >= len(want) || !reflect.DeepEqual(r, want[index]) {
								t.Fatal("barrier trace differs")
							}
							index++
						}
					}
					if err := got.run(n, observe); err != nil {
						t.Fatal(err)
					}
					if observe != nil && index != len(want) {
						t.Fatal("barrier callback missing")
					}
					if !reflect.DeepEqual(diagnosticState(t, got), diagnosticState(t, ref)) {
						t.Fatal("barrier control differs")
					}
				}
				if !got.complete() || !ref.complete() || wakes != 12 || got.kernel.Status().Completed != 3 {
					t.Fatalf("missing retirement/barrier completion: wakes=%d status=%+v", wakes, got.kernel.Status())
				}
			})
		}
	}
}
