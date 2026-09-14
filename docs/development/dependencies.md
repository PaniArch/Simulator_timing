# Dependencies and support baseline

## Active dependencies

| Dependency | Version | Scope | Status | Reason |
| --- | --- | --- | --- | --- |
| Go | 1.26.2 | toolchain | FROZEN | Functional emulator and cycle-support baseline |
| GCC/G++ | 9.4.0 | cgo/build | FROZEN ON HOST | Compile the cgo bridge against the pinned SoftFloat archive |
| Berkeley SoftFloat | Release 3e, commit `b51ef8f3201669b2288104c28546fc72532a1ea4` | floating-point mathematics | USED | Deterministic F32 operations, rounding modes, and exception flags |
| Akita | v5.0.0-beta.10 | future timing framework | FROZEN / VENDORED | Offline framework baseline; no Vortex timing behavior yet |
| `go.yaml.in/yaml/v3` | v3.0.5 | timing IR / contract serialization | PINNED / VENDORED | Read and write the timing model's YAML contracts |
| `github.com/pelletier/go-toml/v2` | v2.4.3 | Vortex configuration | PINNED / VENDORED | Parse the frozen `Vortex_rtl/VX_config.toml` input |

SoftFloat is deployed once in the repository-owned, ignored environment capsule
`.harness-environment/v1/softfloat` and mounted read-only by Harness at
`/opt/simulator-environment/softfloat`. It is never discovered from another
Vortex checkout and is never downloaded during a Run.
The verified archive is stored at `softfloat/lib/softfloat.a` in that capsule
and linked in place. No Worker or validation turn extracts, copies, or
recompiles SoftFloat.
The source is BSD 3-clause licensed (`COPYING.txt` SHA-256
`145ea96b4a4a04a1a7738d2a2bf9e830f861971e69606187b018d9e8fc0b95c7`).
During one-time capsule provisioning, the archive was built with the frozen
Linux x86-64 Makefile, `RISCV` specialization, `-fPIC`,
`SOFTFLOAT_ROUND_ODD`, and the recorded fast division/inlining options. Normal
development and Harness runs do not rebuild it; GCC compiles only the cgo bridge
that links to the pinned archive.

`support/softfloat` exposes raw-bit binary32 add, subtract, multiply, divide,
square root, fused multiply-add, signed/unsigned 32-bit conversions, comparisons,
rounding selection, and exception flags. It is a mathematical service only; it
does not decode or implement a RISC-V instruction. Berkeley SoftFloat stores its
control and flag state globally in this build, so the Go wrapper serializes calls
to keep concurrent users deterministic.

The functional memory package uses only the Go standard library.

## Approved but not currently required

| Dependency | Version baseline | Scope | Status | Reason |
| --- | --- | --- | --- | --- |
| `github.com/google/go-cmp/cmp` | v0.7.0 | test only | APPROVED / NOT YET REQUIRED | Deterministic comparison of complex state |

Akita v5 is required only by `internal/dependencycheck`, which compiles and runs
an empty serial engine as an availability smoke test. The same package exercises
YAML and TOML round trips so both serializers remain present in the offline
vendor tree. `go mod tidy` and `go mod vendor` select and copy the required
packages; none of these APIs are used by `isa/`, `support/`, or `emu/`.

- module sum: `h1:eaVg8DYN0LDrCeh5WkLRcXP2UjGRJarl09N+xgTmARA=`
- go.mod sum: `h1:lpv/tSeBBx1W80ihELOsinlVKnYNrdPBisHG66SAuZI=`
- vendored packages: `hooking`, `internal/codec`, and `timing`

YAML v3.0.5 and go-toml v2.4.3 support the pinned Go 1.26.2 toolchain. YAML is
dual-covered by MIT and Apache-2.0 terms; go-toml is MIT licensed. Their license
files are included in `vendor/`. Both projects are maintained upstream and are
constrained here to exact module versions.

No additional GPU simulation framework is present in `go.mod`, `go.sum`, or
`vendor/`. Later dependency changes require a separately scoped support update.

## Policy

- Later tasks must not upgrade or switch Go, GCC, or SoftFloat silently.
- Do not use unpinned `go get ...@latest`.
- Do not add an unrecorded third-party dependency.
- A new dependency requires a concrete need, exact version, license and
  maintenance review, toolchain compatibility confirmation, updated dependency
  records and vendor metadata, and the complete verification run.
- Let the Go resolver manage transitive dependencies; do not add them by hand.
- Prefer stable standard-library facilities such as `debug/elf`,
  `encoding/binary`, `log/slog`, `math/big`, and `testing`.
- Do not add another floating-point library unless a later architecture task
  proves a capability gap in the pinned SoftFloat interface.
- Functional `isa/`, `support/`, and `emu/` packages must not depend on Akita.
  Future Akita use belongs under the separately developed timing layer.
