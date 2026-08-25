# Dependencies and support baseline

## Active dependencies

| Dependency | Version | Scope | Status | Reason |
| --- | --- | --- | --- | --- |
| Go | 1.26.2 | toolchain | FROZEN | Simulator and future Akita/MGPUSim compatibility |
| GCC/G++ | 9.4.0 | cgo/build | FROZEN ON HOST | Compile the cgo bridge against the pinned SoftFloat archive |
| Berkeley SoftFloat | Release 3e, commit `b51ef8f3201669b2288104c28546fc72532a1ea4` | floating-point mathematics | USED | Deterministic F32 operations, rounding modes, and exception flags |

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

The functional memory, ELF32 loader, and logging packages use only the Go
standard library. The ELF loader uses `debug/elf`; no external ELF package is
needed.

## Approved but not currently required

| Dependency | Version baseline | Scope | Status | Reason |
| --- | --- | --- | --- | --- |
| `github.com/pelletier/go-toml/v2` | v2.4.3 | config | APPROVED / NOT YET REQUIRED | Parse Vortex TOML without a custom parser |
| `github.com/google/go-cmp/cmp` | v0.7.0 | test only | APPROVED / NOT YET REQUIRED | Deterministic comparison of complex state |
| Akita v5 | v5.0.0-beta.10 | adapter only | NOT INSTALLED | Optional future integration |
| MGPUSim v5 | future v5 adapter version | adapter only | NOT INSTALLED | Optional future integration |

There is no fake import or unused `require` directive for approved packages.
When a real import appears, add only the approved exact version and regenerate
`go.mod`, `go.sum`, and `vendor/`.

MGPUSim v5 uses module `github.com/sarchlab/mgpusim/v5` and requires Go 1.26.0
or newer. Akita v5 uses `github.com/sarchlab/akita/v5`; the compatible baseline
is v5.0.0-beta.10, requiring Go 1.26.0 or newer and preferring Go 1.26.2. Neither
module nor its transitive graph belongs in the simulator core. A future adapter
must remain a separate integration layer.

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
- Core support and future core simulator packages must not depend on Akita or
  MGPUSim merely in anticipation of integration.
