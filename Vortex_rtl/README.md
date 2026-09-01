# Bundled Vortex RTL reference

This directory is the read-only RTL and configuration evidence bundled with
`Simulator_dev1`.

- bundle source revision: `85a88fe250b0da483cb33ed34126d675ecb93c1c`
- contained Vortex source revision: `e2b9745b637ce8ac462be2f0e01b5d76542dc6c0`

Included:

- the complete `hw/rtl/` source tree, excluding the non-source `.DS_Store` file;
- `VX_config.toml`, `VX_types.toml`, and the AFU `vortex_opae.toml` configuration;
- generated Verilog configuration headers under `hw/`;
- the configuration generator and RTL packaging/build helper scripts;
- DPI declaration headers required by `VX_define.vh`.

Intentionally excluded from this reference bundle:

- `sw/`, `sim/`, `tests/`, `hw/unittest/`, logs, build objects, and Git metadata;
- DPI C++ implementations, because they call executable integer and floating-point
  reference semantics;
- executable backend implementations and prior experiment outputs.

Run `make config` to regenerate the Verilog configuration headers. The frozen
configuration uses `XLEN=32`; override `XLEN` only when intentionally creating a
different dataset variant.

`MANIFEST.sha256` records the bundled files for integrity checks. RTL,
configuration, and helper-source contents are retained verbatim. This README is
localized for `Simulator_dev1`, and its manifest entry is updated accordingly.

The OPAE wrapper refers to `afu_json_info.vh` and `platform_if.vh`. Those headers
are generated or supplied by the target platform toolchain and are not part of
this source snapshot. Core Vortex RTL and the frozen non-OPAE configuration do
not depend on them.
