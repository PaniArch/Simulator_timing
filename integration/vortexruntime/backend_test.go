package vortexruntime

import (
	"testing"

	"vortex.local/simulator/timing/memsys"
)

// The adapter changes only the byte owner: timing still comes from the existing
// finite external backend, not an immediate sparse-memory completion.
func TestSparseExternalBackendFixedLatency(t *testing.T) {
	d, err := NewDeviceWithMode(Timing)
	if err != nil {
		t.Fatal(err)
	}
	config, err := memsys.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Latency != 100 {
		t.Fatalf("IR external latency=%d, want 100", config.Latency)
	}
	putWord(t, d, 0x80000000, 0x12345678)
	b, err := memsys.New(d.Memory(), config, 1)
	if err != nil {
		t.Fatal(err)
	}
	for cycle := uint64(7); cycle <= 107; cycle++ {
		offers := []memsys.Offer{{}}
		if cycle == 7 {
			offers[0] = memsys.Offer{Valid: true, Request: memsys.Request{Identity: memsys.Identity{Transaction: 1}, Address: 0x80000000, Operation: memsys.Read}}
		}
		e, err := b.Step(cycle, offers, []bool{true})
		if err != nil {
			t.Fatal(err)
		}
		if cycle == 7 && !e.Accepted[0] {
			t.Fatal("request not accepted")
		}
		if cycle < 107 && e.Replies[0].Valid {
			t.Fatal("early external response", cycle)
		}
		if cycle == 107 {
			r := e.Replies[0]
			if !r.Delivered || r.Response.Err != nil || r.Response.AcceptedCycle != 7 || r.Response.CompletedCycle != 107 || r.Response.Data[0] != 0x78 {
				t.Fatal(r)
			}
		}
	}
}

func TestModeSelectionAndInvalidDescriptor(t *testing.T) {
	t.Setenv("SIMTIMING_MODE", "")
	d, err := NewDevice()
	if err != nil || d.mode != Timing {
		t.Fatal(d, err)
	}
	if _, err := NewDeviceWithMode("typo"); err == nil {
		t.Fatal("unknown mode silently accepted")
	}
	for _, mode := range []Mode{Timing, Functional} {
		d, _ := NewDeviceWithMode(mode)
		launchOneLane(t, d, 0x100)
		if err := d.WriteDCR(dcrStartupAddr1, 1); err != nil {
			t.Fatal(err)
		}
		if err := d.Start(); err == nil {
			t.Fatal("RV64 descriptor accepted")
		}
		if d.Busy() {
			t.Fatal("invalid launch became busy")
		}
	}
}
