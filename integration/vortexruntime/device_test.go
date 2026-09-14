package vortexruntime

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func putWord(t *testing.T, d *Device, address, word uint32) {
	t.Helper()
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], word)
	if err := d.Memory().Write(address, data[:]); err != nil {
		t.Fatal(err)
	}
}

func launchOneLane(t *testing.T, d *Device, startup uint32) {
	t.Helper()
	writes := map[uint32]uint32{
		dcrStartupAddr0: startup, dcrKernelEntry0: startup, dcrStartupArg0: 0x80,
		dcrBlockDimX: 1, dcrBlockDimY: 1, dcrBlockDimZ: 1,
		dcrGridDimX: 1, dcrGridDimY: 1, dcrGridDimZ: 1,
		dcrBlockSize: 1, dcrWarpStepX: 0, dcrWarpStepY: 0, dcrWarpStepZ: 0,
		dcrClusterDimX: 1, dcrClusterDimY: 1, dcrClusterDimZ: 1,
	}
	for address, value := range writes {
		if err := d.WriteDCR(address, value); err != nil {
			t.Fatal(err)
		}
	}
}

func waitIdle(t *testing.T, d *Device) {
	t.Helper()
	// Race instrumentation makes the existing cycle model substantially slower;
	// this is a wall-clock test guard, not a simulated-cycle or result relaxation.
	deadline := time.Now().Add(3 * time.Minute)
	for d.Busy() {
		if time.Now().After(deadline) {
			t.Fatal("kernel did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestNativeLaunchCompletesAndWritesMemory(t *testing.T) {
	for _, mode := range []Mode{Timing, Functional} {
		t.Run(string(mode), func(t *testing.T) {
			d, err := NewDeviceWithMode(mode)
			if err != nil {
				t.Fatal(err)
			}
			launchOneLane(t, d, 0x100)
			putWord(t, d, 0x100, 0x02a00093) // addi x1,x0,42
			putWord(t, d, 0x104, 0x10102023) // sw x1,256(x0)
			putWord(t, d, 0x108, 0x0000000b) // tmc x0
			if err := d.Start(); err != nil {
				t.Fatal(err)
			}
			waitIdle(t, d)
			if got := d.LastError(); got != "" {
				t.Fatal(got)
			}
			if mode == Timing {
				if d.LastRun().BackingVisible || d.LastRun().Cycles < 100 {
					t.Fatal(d.LastRun())
				}
				var before [4]byte
				if err := d.Memory().Read(0x100, before[:]); err != nil {
					t.Fatal(err)
				}
				if binary.LittleEndian.Uint32(before[:]) != 0x02a00093 {
					t.Fatal("store bypassed D cache")
				}
			}
			if _, err := d.ReadDCR(dcrCacheFlush, 0); err != nil {
				t.Fatal(err)
			}
			var data [4]byte
			if err := d.Memory().Read(0x100, data[:]); err != nil {
				t.Fatal(err)
			}
			// Program and result overlap at 0x100 in this tiny fixture; the store must
			// replace the first word with 42.
			if got := binary.LittleEndian.Uint32(data[:]); got != 42 {
				t.Fatalf("stored=%d", got)
			}
			if summary := d.LastRun(); summary.Completed != 1 || summary.Outcome.String() != "complete" {
				t.Fatalf("summary=%+v", summary)
			}
			if !d.LastRun().BackingVisible {
				t.Fatal("flush did not publish backing bytes")
			}
			// Reload the same VMA after the real CP flush, then launch again.
			putWord(t, d, 0x100, 0x02b00093)
			if err := d.Start(); err != nil {
				t.Fatal(err)
			}
			waitIdle(t, d)
			if d.LastError() != "" {
				t.Fatal(d.LastError())
			}
			if _, err := d.ReadDCR(dcrCacheFlush, 0); err != nil {
				t.Fatal(err)
			}
			if err := d.Memory().Read(0x100, data[:]); err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint32(data[:]) != 43 {
				t.Fatal("reload did not execute new code")
			}
		})
	}
}

func TestDescriptorAndSimulatorErrorsAreClassified(t *testing.T) {
	d, _ := NewDevice()
	if err := d.Start(); err == nil || !strings.Contains(err.Error(), string(OriginConnection)) {
		t.Fatalf("descriptor error=%v", err)
	}

	d, _ = NewDevice()
	launchOneLane(t, d, 0x100)
	putWord(t, d, 0x100, 0xffffffff)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, d)
	if got := d.LastError(); !strings.Contains(got, string(OriginSimulator)) {
		t.Fatalf("simulator error=%q", got)
	}
}
