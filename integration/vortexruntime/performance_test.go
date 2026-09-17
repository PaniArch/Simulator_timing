package vortexruntime

import (
	"encoding/json"
	"io"
	"reflect"
	"sync"
	"testing"
	"vortex.local/simulator/isa"
)

// A hardware snapshot must not become readable between the terminal audit's
// Write and Close. Test both launch and flush publication, including a second
// launch whose previous cumulative snapshot is already available.
func TestMPMPublicationWaitsForAudit(t *testing.T) {
	d, _ := NewDeviceWithMode(Timing)
	launchOneLane(t, d, 0x100)
	putWord(t, d, 0x100, 0xb)
	for run := 0; run < 2; run++ {
		for _, event := range []string{"launch-finish", "cache-flush"} {
			func() {
				before := d.LastRun()
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(release) })
				var terminal auditRecord
				d.auditPath = "injected"
				d.auditOpen = func(string) (io.WriteCloser, error) {
					var blocked bool
					return auditSink{write: func(b []byte) (int, error) {
						var record auditRecord
						if err := json.Unmarshal(b, &record); err != nil {
							return 0, err
						}
						blocked = record.Event == event
						if blocked {
							terminal = record
						}
						return len(b), nil
					}, close: func() error {
						if blocked {
							close(entered)
							<-release
						}
						return nil
					}}, nil
				}
				done := make(chan error, 1)
				if event == "launch-finish" {
					if err := d.Start(); err != nil {
						t.Fatal(err)
					}
				} else {
					go func() { _, err := d.ReadDCR(dcrCacheFlush, 0); done <- err }()
				}
				// Synchronize on the event itself. The package test timeout bounds
				// a missing event; host/race instrumentation speed is not part of
				// the simulated publication contract being asserted here.
				<-entered
				for n := 0; n < 20; n++ {
					if !d.Busy() {
						t.Fatal("published idle before audit")
					}
					if _, err := d.ReadDCR(dcrMPMValue, 0); err == nil {
						t.Fatal("published counter before audit")
					}
					if !reflect.DeepEqual(before, d.LastRun()) || d.LastError() != "" {
						t.Fatal("read mutated publication")
					}
				}
				once.Do(func() { close(release) })
				if event == "cache-flush" {
					if err := <-done; err != nil {
						t.Fatal(err)
					}
				}
				waitIdle(t, d)
				after := d.LastRun()
				if !reflect.DeepEqual(terminal.Summary, &after) || after.HardwareCounters == nil || after.LaunchID != uint64(run+1) {
					t.Fatal("audit/counter/launch mismatch", terminal.Summary, after)
				}
				v, err := d.ReadDCR(dcrMPMValue, 2<<16)
				if err != nil || uint64(v) != after.HardwareCounters.Instret {
					t.Fatal(v, err, after)
				}
			}()
		}
	}
}

func TestMPMTagDecodeAndUnavailable(t *testing.T) {
	d, _ := NewDeviceWithMode(Timing)
	for _, tag := range []uint32{0, 2 << 16, 32 << 16, 34 << 16} {
		v, e := d.ReadDCR(dcrMPMValue, tag)
		if e != nil || v != 0 {
			t.Fatal(v, e)
		}
	}
	d.lastRun.HardwareCounters = &isa.CounterView{Cycle: 0xabc12345678, Instret: 0xdef87654321}
	for _, c := range []struct{ tag, want uint32 }{
		{0, 0x12345678}, {32 << 16, 0xabc}, {2 << 16, 0x87654321}, {34 << 16, 0xdef},
		{7<<22 | 2<<16, 0x87654321}, {3 << 16, 0},
	} {
		v, e := d.ReadDCR(dcrMPMValue, c.tag)
		if e != nil || v != c.want {
			t.Fatal(c, v, e)
		}
	}
	before := d.LastRun()
	for _, tag := range []uint32{1, 1 << 30, 1 << 16, 33 << 16, 1<<22 | 3<<16, 255<<22 | 31<<16} {
		if _, e := d.ReadDCR(dcrMPMValue, tag); e == nil {
			t.Fatal("unsupported read silently succeeded", tag)
		}
	}
	if d.LastError() != "" || !reflect.DeepEqual(before, d.LastRun()) {
		t.Fatal("read changed execution/audit state")
	}
	// LastRun must not expose the published pointer to a caller.
	copy := d.LastRun()
	copy.HardwareCounters.Instret = 0
	if !reflect.DeepEqual(before, d.LastRun()) {
		t.Fatal("mutable summary alias")
	}
	for _, state := range []string{"busy", "latch", "closed", "functional"} {
		d.busy = state == "busy"
		d.busyLatch = state == "latch"
		d.closed = state == "closed"
		d.mode = Timing
		if state == "functional" {
			d.mode = Functional
		}
		if _, e := d.ReadDCR(dcrMPMValue, 0); e == nil {
			t.Fatal("unavailable hardware read", state)
		}
	}
}

func TestNativeMPMCumulativeLaunchAndFlush(t *testing.T) {
	d, _ := NewDeviceWithMode(Timing)
	launchOneLane(t, d, 0x100)
	putWord(t, d, 0x100, 0x40000093)
	putWord(t, d, 0x104, 0x0820918b) // packed load: four EOPs, one receipt
	putWord(t, d, 0x108, 0xb)
	putWord(t, d, 0x400, 0x01020304)
	var before uint32
	for run := uint32(1); run <= 2; run++ {
		if e := d.Start(); e != nil {
			t.Fatal(e)
		}
		// Reads concurrent with execution either reject busy or see a stable idle
		// snapshot; they never touch Kernel state or clear busyLatch.
		for n := 0; n < 20; n++ {
			_, _ = d.ReadDCR(dcrMPMValue, 0)
		}
		waitIdle(t, d)
		if d.LastError() != "" {
			t.Fatal(d.LastError())
		}
		s := d.LastRun()
		if s.Retired != 3 || s.HardwareCounters == nil || s.HardwareCounters.Instret != uint64(6*run) {
			t.Fatal(s)
		}
		v, e := d.ReadDCR(dcrMPMValue, 2<<16)
		if e != nil || v != 6*run {
			t.Fatal(v, e)
		}
		cycles, e := d.ReadDCR(dcrMPMValue, 0)
		if e != nil || cycles <= before {
			t.Fatal(cycles, e)
		}
		// Polls are observational and the completed counters match the terminal audit snapshot.
		for n := 0; n < 3; n++ {
			got, _ := d.ReadDCR(dcrMPMValue, 0)
			if got != cycles {
				t.Fatal(got, cycles)
			}
		}
		if _, e := d.ReadDCR(dcrCacheFlush, 0); e != nil {
			t.Fatal(e)
		}
		after, e := d.ReadDCR(dcrMPMValue, 0)
		if e != nil || after < cycles || after > cycles+1 {
			t.Fatal("busy tail", cycles, after, e)
		}
		got, _ := d.ReadDCR(dcrMPMValue, 2<<16)
		if got != v {
			t.Fatal("flush retired instructions")
		}
		before = after
	}
}
