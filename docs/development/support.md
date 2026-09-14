# Foundation support packages

These packages are infrastructure only. They contain no Vortex decode,
instruction semantics, warp/SIMT state, scheduling, barriers, caches, LSU, or
execution model.

| Import path | Purpose |
| --- | --- |
| `vortex.local/simulator/support/softfloat` | Raw-bit IEEE-754 binary32 mathematics and exception flags |
| `vortex.local/simulator/support/memory` | Bounded byte-addressable little-endian memory |

Typical setup is:

```go
import (
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/support/softfloat"
)

mem, err := memory.New(1 << 20)
if err != nil {
	return err
}
sum, err := softfloat.F32Add(aBits, bBits, softfloat.RoundNearEven)
if err != nil {
	return err
}
_ = sum.Bits
_ = sum.Flags
```

Memory is contiguous and functional only; no timing or translation behavior is
implied. SoftFloat comparison functions expose the upstream quiet/signaling
behavior and do not choose any RISC-V instruction policy.
