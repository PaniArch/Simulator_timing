# Cycle baseline

## Provenance

- Repository: `PaniArch/Simulator_dev`
- Parent: `9f578ecb9079920cc810bd7cbc15c008ac5f4f58`
- Parent message: `feat(t7): finish kernel functional execution`
- Branch: `main` (baseline preparation branch originally named `cycle-baseline`)

The parent is the standalone T7 functional finish. The baseline does not use
later `main` implementation changes.

## Pre-migration reference

The original `./scripts/verify.sh` was run from the exact parent with the frozen
Go 1.26.2 and SoftFloat environment. Build, tests, vet, formatting, module
verification, and whitespace checks all passed.

```text
environment-check: PASS (/hpc2hdd/home/zekaiwang/vortex-work/Simulator_dev1/.harness-environment/v1)
verify: SoftFloat
SoftFloat archive: /hpc2hdd/home/zekaiwang/vortex-work/Simulator_dev1/.harness-environment/v1/softfloat/lib/softfloat.a
verify: go mod verify
all modules verified
verify: go build -mod=vendor ./...
verify: go test -mod=vendor ./...
verify: go vet -mod=vendor ./...
verify: gofmt
verify: git diff --check
verify: PASS (go1.26.2, vendor/offline mode)
```

Before migration the repository contained 9 Go packages, 31 test files, and
196 top-level `Test`, `Example`, or `Benchmark` functions. After the pure
directory/import migration, the functional tree still contains the same 31
test files and 196 functions, and `scripts/verify-emu.sh` passes.

## Baseline boundary

The migration only moves `state/`, `warp/`, `core/`, and `device/` under
`emu/`, updates Go import paths, archives T0-T7 and the functional architecture
contract under `emu/docs/`, and reorganizes validation/documentation paths.
It does not change ISA, State, Warp, Core, CTA, Barrier, memory, or Kernel
behavior.

The frozen RTL reference is bundled under the repository-root `Vortex_rtl/`
directory. It is derived from source commit
`85a88fe250b0da483cb33ed34126d675ecb93c1c`; all RTL, configuration, and helper
source contents remain unchanged. Only its README is localized for this
repository and the corresponding manifest entry is updated. Nested Git metadata
is excluded so the parent repository tracks the actual snapshot contents.

Akita v5.0.0-beta.10 is the only new direct framework dependency. It is used by
an isolated dependency availability test and does not define Vortex timing
semantics. `timing/` contains only its scope README.

The final `scripts/verify-all.sh` gate passes with a newly created empty module
cache and build cache while `GOPROXY=off`, `GOSUMDB=off`, and
`GOFLAGS=-mod=vendor` are enforced.
