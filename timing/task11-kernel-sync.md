# T11 Kernel synchronization and reclamation progress

The Kernel now owns the BarrierCoordinator associated with its ResidencyMemory.
This reuses the validated arrival/count/event/phase state machine in
`emu/core/barrier.go` without creating or stepping a functional Core. A phase
view is supplied with each Warp's CTA context. Barrier keys include resident
CTA slot, physical AddressWarp and barrier ID; the coordinator validates that
both source and address Warps belong to that CTA.

A WCTL effect reaches `Kernel.control` at the runner's control delivery edge.
It stages and commits the coordinator request, collecting releases. MultiRunner
registers the current wait token later on the same edge. The Kernel post-edge
callback then resolves releases to those exact blocked tokens and queues
`Release` for the following pipeline edge. An immediately satisfied WAIT or
last SYNC still follows this path; it never bypasses registered scheduler state.
Other Warp selection remains available while any participant waits.

ResidencyMemory refuses release while arrivals, waiters, participant counts,
events or completed-arrivals awaiting events remain. Successful release removes
inactive phase records as well as membership, so a newly resident CTA cannot
inherit the previous CTA's barrier phase. Kernel checks the barrier owner before
its existing per-member pipeline/effect/service drain condition.

`TestKernelRepeatedBarrierLocalExchange` runs four CTAs through two resident positions with two Warps
each. Each Warp writes local cells, synchronizes, reads the other Warp's cells,
outputs them globally and synchronizes again before the next iteration. Two
iterations reuse the same physical barrier address within each CTA, with distinct
CTA-dependent values. The test checks every lane and the exact thirty-two external
wake notifications under 40-cycle service delay and request backpressure.

RTL/software distinction: the existing VX_bar_unit request/phase rules are
reused from the functional coordinator; Kernel forwards notifications at the
registered WCTL boundary and releases on the next edge. Internal barrier RAM
pipeline timing remains abstract. Store bytes become visible at the explicit
service event, not at barrier arrival or hardware store commit. The current
Kernel service uses uniform configured delay, preserving accepted request order.

External event completion now uses `PendingBarrierEvents` and
`CompleteBarrierEvent`. Each once-only ticket is bound to the Kernel instance,
immutable GridWalker CTA ID, resident slot, barrier address and monotonic
expectation sequence. A copied ticket is valid, but a consumed, modified,
foreign-launch or prior-CTA ticket is rejected before any coordinator mutation.
This identity is software metadata, not an additional RTL barrier field.
Completing the final event releases already-registered wait tokens on the next
edge; completion before arrival simply decrements the expected event count.

`TakeEvents` drains detached, cycle-stamped generated/admitted/reclaimed and
barrier-complete records. CTA IDs identify generations; slot and Warp masks
identify physical resources. Call after each Run, including its completed return,
to collect the final reclamation boundary. Existing MultiRecord observations
continue to distinguish active/stalled masks, hardware pending, receipt counts,
queued memory services and exact wake tokens.

`TestKernelBarrierEventRejectsOldGeneration` executes two CTAs through one
resident slot. Each attaches an expected event and blocks at synchronization.
It proves pending events prevent completion, valid completion resumes execution,
and duplicate or modified old tickets cannot change the replacement CTA. Both
CTAs finish once with correct output and exactly one generated/admitted/reclaimed
event each.

Kernel Spawn is now bound to eligible owners within the source CTA. Since
operand registers may still have dependencies at decode, the binding is a pool:
actual target bits are resolved after RF capture/execution and must select only
members of that pool. Out-of-CTA bits fail explicitly before activation rather
than waiting forever. The existing registered global single-active gate remains
in force. Only selected targets receive activation and stream-frontier updates;
no target owner, metadata, or pending service is replaced.

`TestKernelSpawnRetainsCTAMembership` launches four Warps, lets the non-source
Warps stop, then uses count=2 to activate only Warp 1 from the larger pool. The
target starts at the supplied PC with lane 0 and copied mscratch, writes output,
and the CTA completes once. A request naming unowned slots is rejected.

The repeated local-exchange test now runs four CTAs through two resident
positions. It checks every output across reuse, thirty-two barrier wakes and
exactly one generation/admission/reclamation event per CTA. It observes a new
CTA admission while an older CTA still owns hardware pending or service work,
which rules out a global drain prerequisite.

## Completion and identity audit

- Stopping fetch is the scheduler active-mask transition, visible separately from
  registered hardware pending/EOP commit in MultiRecord.
- Store commit is not byte completion: accepted service tokens remain queued and
  hold their original route until the explicit service edge. Effect receipt
  retirement is another independent lifetime.
- WarpQuiescent checks one slot's pipeline, class credit/lock identity ledgers,
  feedback, receipts, blocked/wake tokens and queued/held services. CTA reclaim
  additionally checks its barrier state and all its members. Other CTAs continue.
- Kernel completion requires exhausted generation, no pending CTA, no residents,
  no external event tickets and no pending barrier release mask. Every owned
  slot has already passed the above drain conditions before losing membership.
- Reentry preserves the monotonically increasing instruction allocator and
  effect stream. A live memory token cannot cross reuse because its route is
  retained until drained. External completions can be replayed by callers, so
  those additionally carry once-only launch/CTA/expectation tickets. The old
  generation regression verifies rejection after the same slot is re-admitted.

The synchronization/reclamation milestone is implemented and ready for independent
verification. Remaining timing abstraction boundaries are whole-CTA dispatch
instead of the RTL dispatcher pipeline, abstract memory service rather than
cache/bank internals, and no multi-Core/global-barrier path in the frozen config.
