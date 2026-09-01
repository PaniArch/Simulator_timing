# Simulator_dev1

`Simulator_dev1` is the support baseline for later Vortex cycle-simulation
experiments. It is derived directly from T7 Finish commit
`9f578ecb9079920cc810bd7cbc15c008ac5f4f58`.

- `isa/` and `support/` are shared functional layers.
- `emu/` contains the unchanged T7 functional execution chain and its
  architecture contract.
- `Vortex_rtl/` contains the bundled frozen RTL reference snapshot.
- `timing/` is an empty development boundary for future Harness work.
- Akita v5.0.0-beta.10 is pinned in `vendor/` for offline use.

The baseline contains no Vortex timing model, cycle parameters, or later host
integration code.

Run `./scripts/verify-all.sh` for the complete functional and empty-cache
offline gate.
