# T11 Kernel launch / residency implementation progress

## Implemented memory boundary

`runner.MultiOptions.DataMemory[warp]` selects the data service for that Warp;
absent entries use the global service passed to `NewMulti`. Fetch always reads
that global service, independently of the data route. The concurrent effects
owner binds the selected service into each instruction Adapter at Begin and
retains it through request acceptance, service, response, and receipt retirement.
Reset preserves the four routes. This is a software service boundary, not a
cache, LMEM bank, or DRAM timing model.

The existing runner request handshake (`FetchReady` / `MemoryReady`) is driven
by `Options.Ready`. Only accepted requests enter service queues. Queued token
identity (epoch, Warp, instruction, uop and mask) remains fixed. The explicit
positive service delay starts at acceptance; `MemoryDelay` can select a delay
per token. At service, loads sample bytes and stores make bytes visible through
the bound service's atomic batch contract. A produced response is held without
re-reading or re-writing bytes until the Core consumes it. Hardware store commit
can precede this service event. No FIFO forwarding or memory hierarchy ordering
beyond these events is implied.

`emu/core.CTAMemory` provides the reusable data routing implementation. Its
canonical CTAManager translates the common virtual local window through Warp
membership into distinct allocations in its one 16 KiB physical byte array;
non-local accesses use the caller's global memory. Atomic mixed global/local
writes validate local ranges before the external batch and then apply local
bytes. Membership must remain installed until that CTA's live requests and
software receipts have drained. Binding a service object does not freeze mutable
membership inside that object: the future residency coordinator must enforce
this lifetime rule before reclaim/reuse.

`TestMultiCTAMemoryRoutesThroughTiming` runs four static resident CTAs with the
same virtual local addresses, different values, variable service delays and
request backpressure. Actual timing fetch/execute/store/load/store produces
separate global outputs. It does not yet claim Kernel launch coverage.

## Implemented dispatch boundary

`model.Core.WarpQuiescent` checks only the selected slot: inactive scheduler,
empty frontend ownership, scoreboard state, registered issue/pending accounting,
and every pipeline token resource. `runner.MultiRunner.WarpQuiescent` also checks
canonical inactivity, software receipts, blocked/wake records, queued services
and held responses. This is a conservative resource-reuse gate; it is never used
for hardware WSYNC or BAR drain. Other Warps can remain active and in flight.

`MultiRunner.DispatchWarp` stages canonical `StageWarpLaunch` and scheduler
`Core.DispatchWarp` from that boundary. First use selects startup PC; reuse uses
the frozen 20-byte reentry rule, and mscratch receives the parameter address.
The scheduler retains the global monotonic instruction-ID allocator and all
other slots. The event is recorded as `cta-dispatch`. This synchronous operation
runs between pipeline edges (or in the post-edge observer); it does not claim
the RTL dispatcher's internal pipeline latency. It performs no external
callbacks, and the runner is single-threaded. Kernel allocation and context
installation must precede dispatch.

`TestCTADispatchPreservesOtherWorkAndRejectsStale` checks per-slot availability,
stale transaction rejection and retained hardware pending. The runner regression
`TestCTADispatchWhileOtherWarpsRun` activates a previously unused slot while other
Warps execute, reads mscratch through a real CSR instruction, checks the partial
lane mask and reaches pipeline/service drain without resetting the Core.

## RTL and reuse audit for the next implementation step

- `VX_kmu.sv` and `emu/device/launch.go`: LaunchState validation and GridWalker
  already encode frozen field widths, cluster traversal and resource demand.
  Reuse these value-level APIs; do not call the functional Kernel execution loop.
- `VX_cta_dispatch.sv` (`cta_fire`, `cta_init`, per-CTA context tables and thread
  coordinate pipeline) owns resource admission and Warp context generation.
  `emu/core/cta.go` provides allocation, membership, WarpStep coordinates and
  CTA CSR views. Its dynamic staging API is currently private and requires
  attachment to the functional Core and barrier owner. ResidencyMemory now
  reuses the metadata and byte owner without that attachment. The RTL
  IDLE FSM uses fixed-stride `base_slot * stride` placement and a round-robin
  tail with a free cluster window; the functional manager uses first-fit
  allocation. Kernel now selects explicit fixed-stride offsets and a slot
  window; its boundary-level dispatch latency remains a software abstraction.
- `VX_scheduler.sv:179` applies CTA activation and mask, with startup PC for init
  and the RTL resume-PC rule otherwise; `:349` writes CTA parameters to mscratch.
  `emu/state/launch.go` already stages canonical Warp initialization. Timing
  admission now updates the scheduler through the normal DispatchWarp transaction.
  `model.Core.Restart` is a cancellation recovery API with different prerequisites
  and must not be repurposed as admission.

## Kernel API and current execution coverage

`runner.NewKernel(device.LaunchState, device.BackingMemory, runner.Options)`
validates the frozen launch and creates inactive canonical owners, a GridWalker,
one ResidencyMemory and one MultiRunner. `Kernel.Run(cycleBudget, observer)` is
resumable and advances actual timing edges. `Kernel.Status` returns detached
resident records, generated/completed counts and waiting/completion state.
Startup PC initializes first-use Warps, entry is exposed in CTA CSR, and the
parameter address initializes mscratch. No functional Core.Step or Warp.Run is
called. All four Warp data routes share the single physical LMEM owner.

`emu/core.ResidencyMemory` reuses CTA validation, WarpStep coordinates, CSR views
and atomic memory routing, without attaching a functional Core. It accepts an
explicit aligned physical offset and rejects overlapping allocations or occupied
Warp/CTA slots before mutation. Unlike the legacy static manager, its local
allocation address is `FrozenLocalMemBase + offset`, matching dispatcher
`cta_csrs.lmem_addr` at VX_cta_dispatch.sv:481. The local-memory test using common
virtual addresses describes the legacy static manager; Kernel addresses come
from this physical CSR placement.

Kernel admission uses a round-robin slot tail and fixed stride equal to aligned
launch local-memory size. The usable slot count is bounded by LMEM capacity;
first cluster members pre-wrap and wait for the whole slot window. Warps are
selected from free quiescent slots in increasing order. Admission currently
installs one whole CTA at a boundary before a pipeline edge, abstracting the
RTL per-Warp dispatch/context pipeline latency. It does not require unrelated
resident CTAs to finish. Reclaim retains membership until all member slots are
quiescent, including external services; physical SRAM bytes are preserved.

`TestKernelLaunchExecutesStartupEntryAndCTAContexts` exercises four resident CTAs
through startup entry-CSR read/jump, parameter read, block-index CSR output
addressing, physical local-memory CSR reads and global stores under backpressure.
`TestResidencyPlacementAndAtomicConflict` checks partial masks, alignment/size,
overlap and capacity rejection without membership mutation, and isolated release.

## Residency acceptance regressions

`TestKernelReentryMultiWarpCoordinatesAndResourceWait` executes six CTAs, each
with six threads in two Warps, through two resident CTA slots. It checks actual
CTA rank and X/Y CSR values, lane-local thread IDs, inactive tail lanes, and
local store/load to global output. The 80-cycle byte-service delay and request
backpressure produce inactive Warps with live stores: each queued service keeps
its original CTA mapping until drained. Waiting and subsequent slot reuse are
observed. A once-only prologue counter remains one after reuse, proving the
20-byte reentry window rather than a startup restart.

`TestKernelClusterWindowPrewrap` executes two clusters of three CTAs with four
physical slots. The next cluster must wrap past slot 3 to window [0,3), wait
for that window, preserve fixed physical LMEM bases, and produce all six outputs.
These tests run actual timing fetch, decode, execution and memory service.

The launch/residency milestone is implemented. The synchronization and reclamation implementation, event identities and
completion audit are documented in task11-kernel-sync.md. Dispatch
is currently a whole-CTA boundary transaction rather than the RTL per-Warp
context pipeline; cache/LMEM-bank internals and dispatcher latency remain explicit
abstractions. Normal resource waits are resumable budgeted execution, not errors.
