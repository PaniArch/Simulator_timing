# T11 cycle-control milestone

The cycle-control milestone is implemented for the frozen four-Warp, single-Core explicit-owner timing profile. Kernel launch/CTA dispatch orchestration belongs to the subsequent T11 milestones; this document does not claim that the entire T11 task or RTL cycle-equivalence work is complete.

## State sources and RTL evidence

| State or event | Frozen source | Implementation and observable boundary |
| --- | --- | --- |
| Hardware pending | `VX_issue_slice.sv:99-101`, `VX_issue.sv:92-98`, `VX_scheduler.sv:555-576` | `instructionAccounting` registers the scoreboard-to-OPC handshake, then increments per-uop pending; registered EOP commit decrements it. `Core.HardwarePending` and `WarpObservation.HardwarePending` expose old-edge counts. |
| Instret | `VX_commit.sv` committed EOP mask; `VX_scheduler.sv:539-553` | `Core.Instret` counts registered commit notifications, including every packed uop, independent of software receipt reclamation. Partial WB does not release pending or increment instret. |
| Cycle and active Warp CSR | `VX_scheduler.sv:398,581-589` | `Core.Cycles` is the 44-bit old-busy-qualified counter; `Core.ActiveWarps` is the registered scheduler mask. MultiRunner supplies old-edge hardware values in CSR contexts, retaining caller CTA/barrier metadata. |
| LSU drained | `VX_core.sv:583`, `VX_mem_scheduler.sv:197-205` | `Core.LSUSchedulerDrained` combines request storage and outstanding load tags. The baseline coalescer is disabled. Result buffers and downstream store service tails do not extend this predicate. |
| WSYNC | `VX_wctl_unit.sv:182-184` | Tests its own Warp's old pending <=1, including itself. Software effect liveness does not gate execution. |
| BAR | `VX_wctl_unit.sv:186-187` | Tests the shared LSU drain predicate. External coordinator wake is a distinct registered event. |
| WSPAWN | `VX_scheduler.sv:193-202,330-343` | Commits the instruction while retaining its activation receipt. Activation waits for registered single-active, then initializes target PC/mask/MSCRATCH and releases the source. |
| Software effects | `timing/effects` per-event receipts | `Retired()` remains the compatibility macro receipt-completion metric, not architectural instret. `Pending()`/`DrainBefore()` are diagnostic software summaries and have no runner control-gate call sites. |
| External memory completion | `timing/runner` request queues and effects service receipts | Store bytes become visible at the explicit service event. Final completion waits for tails; BAR is not interpreted as global store visibility. |

All RTL paths above are relative to `Vortex_rtl/hw/rtl/core/`, except `VX_mem_scheduler.sv` under `Vortex_rtl/hw/rtl/libs/`. Source identities, paths and software closures are also recorded in `timing/ir.yaml`.

The pending ledger uses tokens to support existing bounded software cancellation; RTL uses counters. Cancellation does not manufacture commits. Software Flush discards transient work while preserving counters; constructing a new Core initializes counters to zero. The current explicit-owner profile has no CTA dispatcher busy input. That signal must be added with Kernel dispatch.

## Feedback and CSR priority

`VX_scheduler.sv:174-299` orders CTA initialization, decode unlock, pending spawn, TMC, SPLIT, JOIN, barrier unlock, WSYNC unlock, branch, schedule stall, fetch PC advance, and optional asynchronous trap handling. The implemented producer set uses a detached feedback copy and per-field priority; schedule stall and fetch PC advance remain last ordinary assignments. Input slice order cannot select the winner. Duplicate producer/identity, stale epoch and malformed PC/mask checks remain.

`VX_decode.sv:600-603` clears wstall for BAR.arrive, so its feedback may overlap a younger branch. Concurrent delivers that barrier event before the younger control advances the stream frontier. Other WCTL controls retain wstall (`:564-609`), preventing another blocking same-Warp control from being fetched before release. Such illegal input pairs retain a specific error; they are not left as an UNRESOLVED normal execution path.

`VX_csr_unit.sv:71,108,116` qualifies reads/writes by `execute_if.valid && cta_read_done`, independently of result readiness. Every held CSR request window re-evaluates the old-edge CSR value using latched RF operands and applies only CSREvent. It does not write the destination, advance control or retire. Acceptance creates the one final WB receipt. A later held window can observe a hardware trap write from the preceding edge.

`VX_csr_data.sv:105-125` first accumulates FFLAGS, then applies the software write to its addressed field. `DeliverCSRWithFlags` uses one old-image StageEffects transaction; both receipts advance on successful commit. Reads never forward same-edge flags. FRM-only writes preserve incoming flags, whereas FFLAGS/FCSR writes override the addressed field.

`VX_scheduler.sv:244-255` reads MTVEC/MEPC and saved masks at branch feedback, not ALU opcode recognition. Trap delivery is created at that edge, and the read-only scheduler preview uses the same old image. `:355-380` applies software CSR writes before hardware trap-entry MEPC/MCAUSE/MTVAL writes. `DeliverCSRWithTrap` validates producers independently against the common old image and merges only their written fields before one commit. Software MTVEC/MSTATUS/FCSR updates survive; hardware trap-entry writes win conflicts. MRET redirects using old MEPC and restores the saved thread mask while preserving a same-edge software MEPC write. FFLAGS may join this transaction. A prior BAR.arrive event is consumed before a joint trap advances the control frontier.

No all-Core drain or ignored producer event is used to resolve these overlaps. Snapshot capture precedes owner changes; delivered receipts are not replayed. Invalid identities are validated before applying owner events, and failed joint transactions advance no receipt.

## WSPAWN and outstanding work

Concurrent binds explicit target owners while other Warps remain active. The source executes and commits without waiting for software records, then retains a pending activation until the old registered single-active gate permits it. Other Warps continue using the SFU and can terminate normally.

`DeliverWarpSpawnAtActivation` uses live target images and changes only PC/mask/MSCRATCH, preserving unrelated data and outstanding WB/service identities. The source uses its locked instruction PC rather than requiring an older ordinary receipt to advance canonical PC first. Target activation frontiers prevent old sequential PC receipts from rewinding the new activation. Older register and service receipts remain valid. Standalone StageWarpSpawn retains its original pre-issue snapshot validation; the timing activation entry point is explicit.

Tests exercise both delayed single-active release and activation while old target loads/source stores are still outstanding. Final target PC and new-program outputs survive those late completions. Instruction IDs remain monotonic across activation; Kernel slot-generation management remains subsequent work.

## Regression and acceptance evidence

- AC-001/002: `timing/model/accounting_test.go` checks registered issue, simultaneous issue/commit, packed-uop units, partial/final WB, cancellation, LSU tags versus result queues, busy-qualified cycles and 44-bit wrap. The Markdown/YAML distinguish hardware predicates, receipts and external service events.
- AC-003/006: runner WSYNC, BAR release and spawn tests check own-Warp waits, unrelated progress, request backpressure, registered release, source/target tails and new activation PC protection. `TestBarrierReleaseDoesNotWaitForExternalStoreTail` proves resumed work can finish before stores become externally visible, while overall completion still waits for stores.
- AC-004: `timing/runner/counters_test.go` executes packed instructions and CSR reads through four Warps, checks every old-edge instret against actual EOP notifications, and distinguishes 32 uop commits from 20 macro receipts. CSR register results match execution-edge hardware counters and active masks despite deliberately incorrect caller counter values.
- AC-005/006: scheduler feedback tests check reversed input order and per-field/frontend priority. CSR buffer and effects tests cover held windows, stable output, FFLAGS/FRM/FCSR/read-only priority, duplicate receipts and atomic stale-input failure. `timing/effects/trap_test.go` checks prior-edge versus same-edge writes, held CSR after hardware update, and BAR/FFLAGS/CSR/trap sharing an edge. `timing/runner/trap_test.go` executes ECALL/handler/MRET round trips through all four scheduled Warps.

The old BAR assertion waited for load pending-release. It now checks final response acceptance because `VX_mem_scheduler.ibuf_pop = crsp_fire && crsp_eop`; downstream WB/result latency must not extend BAR drain. The old blanket frontend-collision rejection is replaced by RTL assignment-priority assertions. No required validation script or checker rule was weakened. `verify-timing.sh` was made executable because the frozen command invokes it directly.

YAML `audit-csr-stall` and `audit-t11-control` close the implemented normal-path questions. The retained `u-feedback`/null collision endpoint is explicitly limited to future dispatcher/asynchronous/nonbaseline producers and whole-RTL timing equivalence. It is not used to reject normal execution in this profile. Cache/LMEM bank timing, external memory internals, complete Kernel/CTA orchestration and RTL equivalence remain outside this milestone.

Required validation: `./scripts/verify-timing.sh` (silent stdout) and `./scripts/verify-offline.sh` (local vendor-only build/test/vet with empty caches).

Final milestone validation: both frozen commands passed after the trap sampling/merge and BAR/store-tail changes. `verify-timing.sh` returned exit 0 with no stdout; `verify-offline.sh` passed environment checks, vendor-only build, all tests and vet using empty local caches. The cycle-control milestone is ready for independent verification.
