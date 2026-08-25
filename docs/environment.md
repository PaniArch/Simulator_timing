# Development environment

## Frozen toolchains

| Tool | Frozen executable | Version |
| --- | --- | --- |
| Go | `/opt/simulator-environment/go1.26.2/bin/go` in Harness | go1.26.2 linux/amd64 |
| C | `/usr/bin/gcc` | GCC 9.4.0 |
| C++ | `/usr/bin/g++` | G++ 9.4.0 |
| Archiver | `/usr/bin/ar` | GNU Binutils 2.34 |

The Go module is `vortex.local/simulator` with language version Go 1.26.0. The
module path is a stable local identity and is independent of the repository's
configured Git remotes; it must not change outside an explicit module migration.

An unsourced shell may resolve a different host Go installation and trigger an
automatic toolchain lookup. That host state is not part of the development
baseline and is not used after activating this project.

The repository-owned environment capsule contains the frozen Go 1.26.2 tree
under `.harness-environment/v1/go1.26.2`. Harness mounts that exact directory
read-only at `/opt/simulator-environment`; no per-turn extraction or copy is
performed. The tree originally came from the already present official toolchain
module `golang.org/toolchain@v0.0.1-go1.26.2.linux-amd64`:

- module zip hash: `h1:mCBp0gCL9gQVqXpC60jQ7R46JDxL73qeF8hv6SnV2ss=`
- installed `bin/go` SHA-256: `1a0f01bdb35c622c78bdc49f26f85dda53f9d7955f453f63c4c023b0dc7c754d`

The same capsule contains the pinned SoftFloat 3e source, headers, and the
prebuilt `softfloat/lib/softfloat.a`. Worker and validation turns link this
read-only archive in place; they do not copy, extract, or rebuild it.

## Enter and verify

Every development shell starts with:

```bash
cd /absolute/path/to/Simulator_v0
source env/env.sh
```

The entry point fixes the Go and C/C++ executables, enables cgo, sets
`GOTOOLCHAIN=local`, disables user `GOENV` and workspace discovery, and uses
runtime-local Go caches together with the pinned, read-only SoftFloat archive.
It also exports:

- `SIMULATOR_ENV_ROOT=/opt/simulator-environment` under Harness
- `SOFTFLOAT_SOURCE_ROOT=$SIMULATOR_ENV_ROOT/softfloat`
- `SOFTFLOAT_ARCHIVE=$SIMULATOR_ENV_ROOT/softfloat/lib/softfloat.a`
- `SIMULATOR_LOG_DIR=$HARNESS_RUNTIME_DIR/logs` under Harness
- `SIMULATOR_LOG_DIR=$SIMULATOR_ROOT/.cache/logs` in a manually activated shell

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

It validates toolchain paths and versions, checks the frozen SoftFloat archive,
then runs module verification, build, tests, vet, gofmt, and Git whitespace
checks.

## Cache, vendor, and recovery

`.cache/` is disposable and ignored by Git. Sourcing `env/env.sh` recreates its
directory layout. `scripts/build-softfloat.sh` validates the exact static
library in the read-only pinned environment capsule without network access or
writes to another Vortex source tree. Go dependencies remain offline with `GOPROXY=off`,
`GOTOOLCHAIN=local`, and `-mod=vendor`.

There is currently no third-party Go module import, so `go.sum` is empty and
`vendor/README.md` records the empty Go vendor set. The SoftFloat archive is
part of the ignored, repository-local environment capsule rather than a
per-turn cache. Dependency versions are never reselected during recovery.
