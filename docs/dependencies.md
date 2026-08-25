# Dependency baseline and policy

## Approved baseline

| Dependency | Version | Scope | Status | Reason |
| --- | --- | --- | --- | --- |
| Go | 1.26.2 | toolchain | FROZEN | Simulator and future Akita/MGPUSim compatibility |
| `github.com/pelletier/go-toml/v2` | v2.4.3 | config | APPROVED / NOT YET REQUIRED | Parse Vortex TOML without a custom parser |
| `github.com/google/go-cmp/cmp` | v0.7.0 | test only | APPROVED / NOT YET REQUIRED | Deterministic comparison of architecture effects and state |
| Akita v5 | v5.0.0-beta.10 compatibility baseline | adapter only | NOT INSTALLED | Optional future integration |
| MGPUSim v5 | future v5 adapter version | adapter only | NOT INSTALLED | Optional future integration |

The two approved packages are exact version baselines, not current imports.
There is no fake import or unused `require` directive: `go mod tidy` would
correctly remove either one until implementation actually needs it. When first
used, add only its exact approved version and regenerate `go.sum` and `vendor/`.

The approved TOML baseline declares Go 1.21.0 and is MIT-licensed. The approved
go-cmp baseline declares Go 1.21 and uses a BSD 3-clause license. Both are
compatible with the frozen Go 1.26.2 toolchain. Version provenance:

- <https://github.com/pelletier/go-toml/releases/tag/v2.4.3>
- <https://github.com/google/go-cmp/releases/tag/v0.7.0>

## Future compatibility, not core dependencies

MGPUSim v5 uses module `github.com/sarchlab/mgpusim/v5` and requires Go
1.26.0 or newer. Akita v5 uses module `github.com/sarchlab/akita/v5`; the
compatible baseline is v5.0.0-beta.10, requiring Go 1.26.0 or newer and
preferring toolchain Go 1.26.2.

Neither module, nor any of their indirect dependencies, belongs in the current
core dependency graph. A future MGPUSim adapter must live in a separate adapter
or integration layer. ISA, State, Warp, CTA, and other core packages must not
depend back on Akita or MGPUSim.

## Policy

- Later tasks must not upgrade or switch Go on their own.
- Do not use unpinned `go get ...@latest`.
- Do not add an unrecorded third-party dependency.
- Every new dependency requires a documented need, an exact version, a license
  and maintenance review, Go 1.26 compatibility confirmation, updated
  `go.mod`/`go.sum`/`vendor`, and the complete verification run.
- Let the Go module resolver manage transitive dependencies; do not add them by
  hand.
- Prefer the standard library where it reliably covers the need, including
  `math/big`, `debug/elf`, `encoding/binary`, `bytes`, `errors`, `fmt`, `io`,
  `math/bits`, `log/slog`, and `testing`.
- Do not add a softfloat or other ISA/FP library until T1 proves a concrete
  semantic gap. Such a proposal must explain why the standard library is
  insufficient and satisfy the full review and vendoring process above.
- Toolchain and third-party dependency changes are E0 environment decisions,
  not silent T1-T7 implementation choices.

