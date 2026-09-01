// Package dependencycheck verifies baseline framework availability without
// defining any Vortex timing behavior.
package dependencycheck

import (
	"testing"

	akitiming "github.com/sarchlab/akita/v5/timing"
)

func TestAkitaSerialEngineIsAvailable(t *testing.T) {
	engine := akitiming.NewSerialEngine()
	if engine == nil {
		t.Fatal("akita returned a nil serial engine")
	}
	if engine.CurrentTime() != 0 {
		t.Fatalf("new Akita engine time = %d, want 0", engine.CurrentTime())
	}
	if err := engine.Run(); err != nil {
		t.Fatalf("run empty Akita engine: %v", err)
	}
}
