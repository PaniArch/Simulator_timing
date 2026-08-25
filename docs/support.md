# Foundation support packages

These packages are infrastructure only. They contain no Vortex decode,
instruction semantics, warp/SIMT state, scheduling, barriers, caches, LSU, or
execution model.

| Import path | Purpose |
| --- | --- |
| `vortex.local/simulator/support/softfloat` | Raw-bit IEEE-754 binary32 mathematics and exception flags |
| `vortex.local/simulator/support/memory` | Bounded byte-addressable little-endian memory |
| `vortex.local/simulator/support/elf32` | ELF32 RISC-V `PT_LOAD` loader with BSS zero-fill |
| `vortex.local/simulator/support/logging` | Project-local `log/slog` file logger |

Typical setup is:

```go
import (
	"vortex.local/simulator/support/elf32"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/support/softfloat"
)

mem, err := memory.New(1 << 20)
if err != nil {
	return err
}
image, err := elf32.LoadFile(programPath, mem)
if err != nil {
	return err
}
_ = image.Entry

sum, err := softfloat.F32Add(aBits, bBits, softfloat.RoundNearEven)
if err != nil {
	return err
}
_ = sum.Bits
_ = sum.Flags
```

The ELF loader writes loadable segments to `p_paddr`, matching the existing
Vortex loader. It does not execute the entry point or interpret symbols. Memory
is contiguous and functional only; no timing or translation behavior is
implied. SoftFloat comparison functions expose the upstream quiet/signaling
behavior and do not choose any RISC-V instruction policy.

With `env/env.sh` active, `logging.Open("experiment", nil)` appends to
`$SIMULATOR_LOG_DIR/experiment.log`. Harness places that directory in its
per-invocation runtime; a manually activated shell uses `.cache/logs`. Call
`Close` when the experiment ends.
