package runner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/support/memory"
)

// Compare complete ordered observations and all architectural state over two
// launches. Packed loads distinguish hardware EOPs from macro retirement;
// six CTAs require generation reuse. Flush budgets use the same partition as Run.
func TestStateCostLaunchChunkEquivalence(t *testing.T) {
	var want []baselineResult
	var wantTrace []MultiRecord
	for _, mode := range baselineChunks {
		for _, diag := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/diag=%t", mode.name, diag), func(t *testing.T) {
				ram, err := memory.New(8192)
				if err != nil {
					t.Fatal(err)
				}
				put := func(a, w uint32) {
					var b [4]byte
					binary.LittleEndian.PutUint32(b[:], w)
					if err := ram.Write(a, b[:]); err != nil {
						t.Fatal(err)
					}
				}
				for i, w := range []uint32{0x40000093, 0x0820918b, 0x13, 0x13, 0xb} {
					put(0x100+uint32(i)*4, w)
				}
				put(0x400, 0x01020304)
				launch := device.LaunchState{StartupPC: 0x100, KernelEntryPC: 0x100, GridDimensions: [3]uint32{6, 1, 1}, BlockDimensions: [3]uint32{3, 1, 1}, BlockSize: 3, ClusterDimensions: [3]uint32{1, 1, 1}}
				k, err := NewKernel(launch, ram, Options{Backend: "std", PeriodPS: 1, TraceMemory: true, Ready: func(c uint64) bool { return c%5 != 0 }})
				if err != nil {
					t.Fatal(err)
				}
				// Fix only the observational device namespace before execution, as in the
				// frozen baseline. Production identity allocation is checked separately.
				k.deviceID = 7101
				clock, system := k.runner.clock, k.runner.hierarchy.system
				var got []baselineResult
				var trace []MultiRecord
				var frozen [][]byte
				var observer func(MultiRecord)
				if diag {
					observer = func(r MultiRecord) {
						trace = append(trace, r)
						b, err := json.Marshal(r)
						if err != nil {
							t.Fatal(err)
						}
						frozen = append(frozen, b)
					}
				}
				for run := 0; run < 2; run++ {
					f := &baselineFixture{kernel: k, multi: k.runner, ram: ram}
					before := diagnosticState(t, f)
					if err := k.Run(0, observer); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, diagnosticState(t, f)) {
						t.Fatal("zero budget changed launch")
					}
					if err := f.drive(mode.chunks, observer); err != nil {
						t.Fatal(err)
					}
					execution := diagnosticState(t, f)
					if !execution.Status.Complete || !execution.Status.MemoryDrained || execution.Counters.Instret != uint64(48*(run+1)) {
						t.Fatal("lost packed EOPs or completion", execution.Status, execution.Counters)
					}
					var macros uint64
					for _, n := range execution.Retired {
						macros += n
					}
					if macros != 30 {
						t.Fatal("macro retirement changed", macros)
					}
					got = append(got, execution)
					for _, op := range []func(uint64) (bool, error){k.MakeVisible, k.FlushCaches} {
						done := false
						for call := 0; call < 10000 && !done; call++ {
							done, err = op(mode.chunks[call%len(mode.chunks)])
							if err != nil {
								t.Fatal(err)
							}
						}
						if !done {
							t.Fatal("flush exceeded bound")
						}
						got = append(got, diagnosticState(t, f))
					}
					if k.Counters().Instret != execution.Counters.Instret || k.Counters().Cycle < execution.Counters.Cycle || k.Counters().Cycle > execution.Counters.Cycle+1 {
						t.Fatal("flush counter tail changed")
					}
					if run == 0 {
						old, status, counters := k, k.Status(), k.Counters()
						put(0x400, 0x08070605)
						k, err = k.NextLaunch(launch)
						if err != nil {
							t.Fatal(err)
						}
						if k.runner.clock != clock || k.runner.hierarchy.system != system || k.Counters() != counters || k.Status().StartCycle != status.Cycle || k.Status().LaunchID != 2 {
							t.Fatal("successor lost device ownership")
						}
						if err := old.Run(1, nil); err == nil {
							t.Fatal("old launch advanced")
						}
						if !reflect.DeepEqual(old.Status(), status) {
							t.Fatal("transferred status changed")
						}
					}
				}
				if want == nil {
					want, wantTrace = got, trace
				} else {
					if !reflect.DeepEqual(got, want) {
						t.Fatal("launch state, ordered lifecycle events, memory or counters differ")
					}
					if diag && !reflect.DeepEqual(trace, wantTrace) {
						t.Fatal("ordered launch trace differs")
					}
				}
				for i, r := range trace {
					b, err := json.Marshal(r)
					if err != nil || !reflect.DeepEqual(b, frozen[i]) {
						t.Fatalf("launch history %d mutated", i)
					}
				}
			})
		}
	}
}

// A backend error occurs after real requests have advanced, unlike the illegal
// decode baseline. Its failed edge and latched stop must be partition independent.
func TestStateCostDelayedFaultChunks(t *testing.T) {
	var want baselineResult
	var wantTrace []MultiRecord
	for index, mode := range baselineChunks {
		for _, diag := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/diag=%t", mode.name, diag), func(t *testing.T) {
				seed, launch := lifecycleKernel(t)
				ram := seed.backing.(*memory.Memory)
				var b [4]byte
				binary.LittleEndian.PutUint32(b[:], 0xb)
				if err := ram.Write(0x10800, b[:]); err != nil {
					t.Fatal(err)
				}
				launch.StartupPC, launch.KernelEntryPC = 0x10800, 0x10800
				backing := &recoveryReadFault{AtomicMemoryService: ram, fail: true}
				k, err := NewKernel(launch, backing, Options{Backend: "std", PeriodPS: 1, TraceMemory: true})
				if err != nil {
					t.Fatal(err)
				}
				k.deviceID = 7102
				f := &baselineFixture{kernel: k, multi: k.runner, ram: ram}
				var trace []MultiRecord
				var observe func(MultiRecord)
				if diag {
					observe = func(r MultiRecord) { trace = append(trace, r) }
				}
				for call := 0; call < 2000; call++ {
					err = k.Run(mode.chunks[call%len(mode.chunks)], observe)
					if err != nil {
						break
					}
				}
				if err == nil || backing.reads != 1 {
					t.Fatal("missing backend fault", err, backing.reads)
				}
				got := diagnosticState(t, f)
				got.Failure = err.Error()
				if got.Status.Complete || got.Cycle == 0 {
					t.Fatal("fault was treated as completion")
				}
				if diag && (len(trace) == 0 || trace[len(trace)-1].Cycle != got.Cycle) {
					t.Fatal("failed edge counted or observation lost")
				}
				if index == 0 && diag {
					want, wantTrace = got, trace
				} else {
					if !reflect.DeepEqual(got, want) {
						t.Fatal("backend failure state differs")
					}
					if diag && !reflect.DeepEqual(trace, wantTrace) {
						t.Fatal("ordered backend failure trace differs")
					}
				}
				stopped := diagnosticState(t, f)
				for _, budget := range []uint64{0, 1, 32} {
					if err := k.Run(budget, observe); err == nil {
						t.Fatal("failed kernel resumed")
					}
				}
				if _, err := k.NextLaunch(launch); err == nil {
					t.Fatal("faulted hierarchy transferred")
				}
				if _, err := k.MakeVisible(32); err == nil {
					t.Fatal("faulted kernel made visible")
				}
				if _, err := k.FlushCaches(32); err == nil {
					t.Fatal("faulted kernel flushed")
				}
				if !reflect.DeepEqual(stopped, diagnosticState(t, f)) || backing.reads != 1 {
					t.Fatal("latched fault advanced state")
				}
			})
		}
	}
}
