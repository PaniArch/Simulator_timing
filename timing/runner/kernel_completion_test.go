package runner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"vortex.local/simulator/emu/device"
)

// Keep the previous predicate as an independent regression oracle; do not
// compare only Status.Complete, which now delegates to executionComplete.
func legacyCompletion(k *Kernel) bool {
	if k.transferredStatus != nil {
		return k.transferredStatus.Complete
	}
	count := 0
	for _, c := range k.resident {
		if c != nil {
			count++
		}
	}
	return k.failed == nil && !k.retirementWrite && len(k.retirePipe[0]) == 0 && len(k.retirePipe[1]) == 0 && k.walker.Remaining() == 0 && k.pending == nil && count == 0 && len(k.barrierEvents) == 0 && k.barrierReleases == 0
}

func TestKernelCompletionAndStatusOwnership(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprintf("fault=%t", fault), func(t *testing.T) {
			f := newBaselineFixture(t, "kernel", true, fault)
			k := f.kernel
			var history []KernelStatus
			var encoded [][]byte
			sawResident := false
			for i := 0; i < 1000; i++ {
				s := k.Status()
				if k.executionComplete() != legacyCompletion(k) || s.Complete != legacyCompletion(k) {
					t.Fatalf("completion at cycle %d", s.Cycle)
				}
				history = append(history, s)
				data, err := json.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				encoded = append(encoded, data)
				other := k.Status()
				if len(other.Resident) != 0 {
					sawResident = true
					other.Resident[0].Generation++
					if len(other.Resident[0].Resident.Members) != 0 {
						other.Resident[0].Resident.Members[0].Rank++
					}
					if !reflect.DeepEqual(s, k.Status()) {
						t.Fatal("caller mutation reached kernel")
					}
				}
				if s.Complete {
					break
				}
				if err := k.Run(1, nil); err != nil {
					if !fault {
						t.Fatal(err)
					}
					if k.executionComplete() || legacyCompletion(k) {
						t.Fatal("failed kernel complete")
					}
					break
				}
			}
			if !sawResident || (!fault && !k.executionComplete()) {
				t.Fatal("workload did not exercise residency/completion")
			}
			for i, s := range history {
				data, err := json.Marshal(s)
				if err != nil || string(data) != string(encoded[i]) {
					t.Fatalf("history %d changed", i)
				}
			}
		})
	}
}

func TestKernelCompletionGates(t *testing.T) {
	// An exhausted walker isolates each lifecycle gate without advancing an edge.
	f := newBaselineFixture(t, "kernel", false, false)
	if err := f.drive([]uint64{32}, nil); err != nil {
		t.Fatal(err)
	}
	k := f.kernel
	for _, test := range []struct {
		name   string
		change func(*Kernel)
	}{
		{"failure", func(k *Kernel) { k.failed = fmt.Errorf("failure") }},
		{"retirement-write", func(k *Kernel) { k.retirementWrite = true }},
		{"retire-0", func(k *Kernel) { k.retirePipe[0] = []warpRetirement{{}} }},
		{"retire-1", func(k *Kernel) { k.retirePipe[1] = []warpRetirement{{}} }},
		{"pending", func(k *Kernel) { k.pending = &device.CTA{} }},
		{"resident", func(k *Kernel) { k.resident[3] = &KernelCTA{} }},
		{"barrier-event", func(k *Kernel) { k.barrierEvents = map[BarrierEvent]bool{{}: true} }},
		{"barrier-release", func(k *Kernel) { k.barrierReleases = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := *k
			test.change(&copy)
			if copy.executionComplete() || legacyCompletion(&copy) {
				t.Fatal("gate ignored")
			}
		})
	}
	if !k.executionComplete() {
		t.Fatal("completed fixture changed")
	}
	for _, complete := range []bool{false, true} {
		copy := *k
		copy.transferredStatus = &KernelStatus{Complete: complete}
		copy.failed = fmt.Errorf("ignored after transfer")
		if copy.executionComplete() != complete {
			t.Fatal("transfer snapshot ignored")
		}
	}
}

// Compare only the old internal query and the new predicate at the same live
// residency boundary. Fixture construction and simulation are outside timing.
func BenchmarkKernelCompletionRead(b *testing.B) {
	f := newBaselineFixture(b, "kernel", false, false)
	if err := f.kernel.Run(8, nil); err != nil {
		b.Fatal(err)
	}
	if len(f.kernel.Status().Resident) == 0 {
		b.Fatal("missing resident")
	}
	for _, diagnostic := range []bool{true, false} {
		b.Run(fmt.Sprintf("status=%t", diagnostic), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var complete bool
				if diagnostic {
					complete = f.kernel.Status().Complete
				} else {
					complete = f.kernel.executionComplete()
				}
				if complete {
					b.Fatal("unexpected completion")
				}
			}
		})
	}
}
