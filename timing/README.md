# Timing development area

This directory is reserved for the later Vortex cycle-approximate simulator.
Akita is available from the repository's offline vendor tree.

The cycle baseline contains no Vortex timing model and no scheduler, pipeline,
cache, LSU, latency, arbitration, or cycle parameters. Later Harness tasks will
derive the structure from the frozen RTL and may read the functional emulator
under `emu/` without inheriting its Warp/Core/Device organization.
