package dramsim

import (
	"os"
	"testing"
)

func TestMissingLibraryFailsExplicitly(t *testing.T) {
	if _, err := Open("/nonexistent/simulator-dram.so", 2, 64, 1); err == nil {
		t.Fatal("missing plugin silently accepted")
	}
}

func TestRTLSimDramSimSmoke(t *testing.T) {
	path := os.Getenv("SIMTIMING_DRAM_TEST_LIBRARY")
	if path == "" {
		t.Skip("optional native DramSim library not requested")
	}
	t.Chdir(t.TempDir()) // original DramSim writes ramulator.stats.log on finalize
	d, err := Open(path, 2, 64, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for i := uint64(1); i <= 8; i++ {
		if err := d.Submit(i, uint32(i*64), i%2 == 0); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[uint64]bool{}
	for cycle := 0; cycle < 10000 && len(seen) < 8; cycle++ {
		done, err := d.Tick()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range done {
			if id < 1 || id > 8 || seen[id] {
				t.Fatalf("bad completion %d", id)
			}
			seen[id] = true
			t.Logf("id=%d completion edge=%d", id, cycle)
		}
	}
	if len(seen) != 8 {
		t.Fatalf("incomplete %+v", seen)
	}
	// A second group uses the SAME DRAM instance (as successive kernels do).
	if err := d.Submit(9, 64, false); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 10000; cycle++ {
		done, err := d.Tick()
		if err != nil {
			t.Fatal(err)
		}
		if len(done) > 0 {
			if len(done) != 1 || done[0] != 9 {
				t.Fatal(done)
			}
			return
		}
	}
	t.Fatal("second group did not finish")
}
