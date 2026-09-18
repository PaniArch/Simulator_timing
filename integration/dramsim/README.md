# Direct RTLSim DramSim backend

This optional backend compiles the existing `vortex/sim/common/dram_sim.cpp`
**without copying or modifying it**, and links its existing Ramulator library.
`native/bridge.cpp` only supplies an opaque C ABI and callback ticket ownership.
No DRAM scheduling algorithm is reimplemented in Go. Core, Cache, MSHR, LMEM,
coalescer and replacement policies are unchanged.

## Build and select

From `Simulator_timing`, with the existing Vortex/Ramulator build available:

```bash
source env/env.sh
module load compilers/gcc-12.2.0
make -C integration/dramsim CXX="$(command -v g++)"
make -C integration/vortex-runtime
export SIMTIMING_MEMORY_BACKEND=rtlsim-dram
export SIMTIMING_DRAM_LIBRARY="$PWD/.cache/vortex-runtime/libsimtiming-dram.so"
# Use the existing native runtime/benchmark command, with VORTEX_DRIVER=simtiming.
```

`OUT_DIR`, `VORTEX_HOME` and `RAMULATOR_HOME` can be overridden at build time.
The plugin records an rpath to the supplied Ramulator directory. Its C++ runtime
must be available on the execution node (the GCC module above supplies it).
Use an absolute plugin path. Missing/unloadable plugins fail explicitly, with no
fallback to fixed latency. `SIMTIMING_MEMORY_BACKEND=fixed` (the default) retains
the existing deterministic backend and needs no Ramulator installation.
Functional mode does not load DRAM. Runtime launch summaries identify the backend.

## Contract and limits

- Frozen platform binding: 2 channels, 64-byte bus, RTLSim default clock ratio 1.
  The plugin build checks frozen platform bank count and width. HBM2/FRFCFS,
  refresh, address mapping and subtransaction conversion come from the original
  `DramSim`, not a new parameter table. Byte addresses are passed exactly as the
  RTLSim processor passes them; do not pre-divide by the cache-line size.
- `AsyncBackend` preserves Cache request identities and holds responses under
  backpressure. Return FIFO order is per interleaved physical bus bank, shared
  across I/D clients: `(byte_address / 64) % 2` in the frozen configuration.
  Different bus banks may return out of order even to the same Cache client.
  These are neither L1 bank IDs nor Ramulator's internal bank IDs.
  It has an explicit bounded bridge queue: by default 16 in-flight
  requests, 1 acceptance and 1 response per Core cycle. These are bridge limits,
  not Ramulator controller parameters or a claim of RTL socket equivalence.
- The backend consumes previously completed responses, ticks `DramSim`, then
  submits this edge's new requests. Completions produced by the tick become
  visible on the next edge. Preview never advances DRAM. **No fixed 100-cycle
  delay is added** in this mode.
- As in the RTLSim external memory harness, reads snapshot backing bytes and
  writes apply byte enables at external bus acceptance. Cache store hits still
  obey the unchanged writeback policy; they do not bypass Cache to modify backing.
- The original `DramSim` acknowledges a write when Ramulator accepts it, not
  when the physical DRAM write drains. Its original 64-to-16-byte split and
  first-subrequest callback policy are retained. `Visibility` waits for older
  bridge acknowledgments; it is not a physical DRAM drain primitive.
- The DRAM instance persists across Kernel launches and cache flushes, and is
  released on device Close, including failed-run cleanup. Close is not a new
  architectural flush or a promise that every DRAM subtransaction has drained.
- The RTLSim outer per-bank FIFO ordering is preserved, but full RTL socket
  arbitration and response registration are not reproduced. Therefore this
  backend removes the fixed service-time abstraction, but does not itself prove
  full-system cycle equivalence or a universal error bound.

## Tests

`go test ./timing/memsys ./integration/dramsim ./integration/vortexruntime`
checks the adapter without requiring the external library. To exercise real
Ramulator, set `SIMTIMING_DRAM_TEST_LIBRARY` to the built plugin and run
`go test -v ./integration/dramsim`; the optional smoke checks read/write callbacks
and a second request group on the same instance. Run this on a compute node.

Native benchmark evidence and limitations are in
[the regression report](../../docs/runtime/rtlsim-timing-small-regression-20260917.md), section 13.

## Expanded paired suite

`scripts/vortex-dram-suite.py prepare` snapshots the validated libraries, GCC
runtime, Ramulator and all 31 existing supported benchmark inputs into a new
`.cache/dram-expanded/` directory. Its current input inventory is the frozen
20260917 regression campaign; it fails if the previously validated simulator
libraries have changed. It creates 31 small and 30 larger parameter cases
(`packld` has no size option), without rebuilding or changing the kernels.

Submit the printed directory's `run.sbatch` as a Slurm array `0-60%6`, export
`SIMTIMING_SUITE_ROOT` to that absolute directory, and direct Slurm output to
`slurm-%A_%a.log` there. Each task runs RTLSim and Timing+original DramSim with
the same input. The default limit is 1200 seconds **per backend**, inside a
45-minute job; increase the Slurm limit too if choosing a longer runner timeout.
Snapshots include content hashes, and each task checks its libraries and inputs.
Detailed RTL stdout is streamed to gzip; status/heartbeat JSON is updated every
10 seconds without requiring a new simulator trace feature.

Use `scripts/vortex-dram-suite.py aggregate --root <snapshot>` for JSON/CSV
statistics. Raw final cumulative PERF is compared (no startup subtraction,
no summing cumulative snapshots). Functional failure, incomplete native audit,
wrong backend, instruction-count mismatch or a recorded RTL timing issue cannot
be counted as a valid accuracy sample. Small and expanded inputs are reported
separately. The included wall times are observed execution costs, not a fair
simulator speed benchmark: the RTL library emits detailed trace and the model
does not. `scripts/test-vortex-dram-suite.py` tests parsing and exclusions.
