# Development environment

## Frozen toolchains

| Tool | Frozen executable | Version |
| --- | --- | --- |
| Go | `/hpc2hdd/home/zekaiwang/vortex-work/tools/go1.26.2/bin/go` | go1.26.2 linux/amd64 |
| C | `/usr/bin/gcc` | GCC 9.4.0 |
| C++ | `/usr/bin/g++` | G++ 9.4.0 |
| Archiver | `/usr/bin/ar` | GNU Binutils 2.34 |

The project is `/hpc2hdd/home/zekaiwang/vortex-work/Simulator_v0` and its Go
module is `vortex.local/simulator` with language version Go 1.26.0. Because the
repository has no Git remote, that stable local module path was chosen
deliberately and must not change outside an environment migration.

On 2026-08-25 the unsourced login shell resolved `go` to the separate
`MGPUSim/env/go/bin/go` installation (Go 1.26.0). That binary attempted an
automatic go1.26.2 toolchain lookup when used in this module. The older
`/opt/hkust/go/bin/go` path recorded during initial E0 setup is no longer
present. Neither server state is used after activating this project.

The frozen Go 1.26.2 tree came from the already present official toolchain
module `golang.org/toolchain@v0.0.1-go1.26.2.linux-amd64`:

- module zip hash: `h1:mCBp0gCL9gQVqXpC60jQ7R46JDxL73qeF8hv6SnV2ss=`
- installed `bin/go` SHA-256: `1a0f01bdb35c622c78bdc49f26f85dda53f9d7955f453f63c4c023b0dc7c754d`

## Enter and verify

Every development shell starts with:

```bash
cd /hpc2hdd/home/zekaiwang/vortex-work/Simulator_v0
source env/env.sh
```

The entry point fixes the Go and C/C++ executables, enables cgo, sets
`GOTOOLCHAIN=local`, disables user `GOENV` and workspace discovery, and uses
project-local Go and SoftFloat build caches. It also exports:

- `VORTEX_ROOT=/hpc2hdd/home/zekaiwang/vortex-work/vortex`
- `SOFTFLOAT_SOURCE_ROOT=$VORTEX_ROOT/third_party/softfloat`
- `SOFTFLOAT_BUILD_ROOT=$SIMULATOR_ROOT/.cache/softfloat/build`
- `SIMULATOR_LOG_DIR=$SIMULATOR_ROOT/logs`

Confirm that no unrelated Go or compiler is active with:

```bash
command -v go gcc g++
go version
go env GOROOT GOTOOLCHAIN GOMODCACHE GOCACHE CGO_ENABLED CC
gcc --version
g++ --version
```

Then run the only complete validation entry point:

```bash
./scripts/verify.sh
```

It validates toolchain paths and versions, verifies and incrementally builds
SoftFloat in the project cache, then runs module verification, build, tests,
vet, gofmt, and Git whitespace checks.

## Cache, vendor, and recovery

`.cache/` is disposable and ignored by Git. Sourcing `env/env.sh` recreates its
directory layout. `scripts/build-softfloat.sh` can reconstruct the exact static
library from the pinned Vortex submodule without network access or writes to the
Vortex source tree. Go dependencies remain offline with `GOPROXY=off`,
`GOTOOLCHAIN=local`, and `-mod=vendor`.

There is currently no third-party Go module import, so `go.sum` is empty and
`vendor/README.md` records the empty Go vendor set. The SoftFloat C archive is a
generated cache artifact and is not committed. If any cache is damaged, move it
aside, source the environment again, and rerun `./scripts/verify.sh`; dependency
versions are never reselected during recovery.

