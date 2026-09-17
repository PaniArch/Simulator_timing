package memsys

import (
	"reflect"
	"testing"
)

func TestFragmentTraceIsObservational(t *testing.T) {
	left, stepLeft := newPortSystem(t)
	right, stepRight := newPortSystem(t)
	req := simd(1)
	req.Mask = 3
	req.Lanes[0] = LaneRequest{Address: 128, ByteEnable: 15}
	req.Lanes[1] = LaneRequest{Address: right.local.base, ByteEnable: 15, Local: true}
	accepted := false
	local, global, cache := false, false, false
	complete := false
	for cycle := 0; cycle < 600; cycle++ {
		in := SystemInput{Memory: SIMDOffer{!accepted, req}, MemoryReady: cycle > 300}
		a := stepLeft(in)
		in.Trace = true
		b := stepRight(in)
		for _, f := range b.Transfers {
			switch f.Boundary {
			case "global-adapter":
				global = true
				if f.Parent != req.Identity || f.Batch.Generation == 0 {
					t.Fatal(f)
				}
			case "local-memory":
				local = true
				if f.Parent != req.Identity || f.LaneMask != 2 || f.Port != 1 {
					t.Fatal(f)
				}
			case "data-cache":
				cache = true
			}
		}
		b.Transfers = nil
		if !reflect.DeepEqual(a, b) || left.Drained() != right.Drained() {
			t.Fatal("observation changed handshake", cycle, a, b)
		}
		accepted = accepted || a.MemoryAccepted
		complete = complete || len(a.Complete) > 0
	}
	if !local || !global || !cache || !complete || !left.Drained() {
		t.Fatal(local, global, cache, complete, left.Drained())
	}
}
