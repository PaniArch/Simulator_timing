# E0 Go environment

## Frozen baseline

- Project: `/hpc2hdd/home/zekaiwang/vortex-work/Simulator_v0`
- Module: `vortex.local/simulator`
- Language version: Go 1.26.0
- Frozen toolchain: Go 1.26.2 for `linux/amd64`
- Go executable: `/hpc2hdd/home/zekaiwang/vortex-work/tools/go1.26.2/bin/go`

`Simulator_v0` has no Git remote. The stable local module path
`vortex.local/simulator` was therefore selected deliberately. It must not be
changed casually; moving to a hosted module path is an environment-level
migration.

The server login environment currently resolves `go` to
`/opt/hkust/go/bin/go` (Go 1.21.3). E0 does not modify that installation or any
shell startup file. The frozen Go 1.26.2 tree was copied into the workspace tool
directory from the already present official Go toolchain module distribution:

- source module: `golang.org/toolchain@v0.0.1-go1.26.2.linux-amd64`
- source module zip hash: `h1:mCBp0gCL9gQVqXpC60jQ7R46JDxL73qeF8hv6SnV2ss=`
- installed `bin/go` SHA-256: `1a0f01bdb35c622c78bdc49f26f85dda53f9d7955f453f63c4c023b0dc7c754d`

The source tree was checked with `go version` and `go tool compile -V=full`
before and after copying. Both report `go1.26.2`.

## Use

Every T0-T7 Go session starts with:

```bash
cd /hpc2hdd/home/zekaiwang/vortex-work/Simulator_v0
source env/env.sh
```

The entry point fixes `GOROOT` and puts the frozen executable first on `PATH`.
It also sets `GOTOOLCHAIN=local`, disables user `GOENV` and workspace discovery,
uses project-local `GOCACHE`, `GOMODCACHE`, `GOPATH`, `GOBIN`, and `GOTMPDIR`,
and defaults to `GOFLAGS=-mod=vendor` plus `GOPROXY=off`.

Confirm the active shell with:

```bash
command -v go
go version
go env GOROOT GOTOOLCHAIN GOMODCACHE GOCACHE GOPATH GOFLAGS GOPROXY
```

`command -v go` must print the path under `vortex-work/tools/go1.26.2`, never
`/opt/hkust/go` or a user module-cache path.

Run the complete project check with:

```bash
./scripts/verify.sh
```

The script refuses an unsourced or altered environment and checks the frozen Go
binary checksum before module, test, vet, formatting, and Git whitespace checks.

## Vendor and offline policy

Normal T0-T7 work is network-independent: `GOTOOLCHAIN=local` forbids silent
toolchain switching, `GOPROXY=off` forbids module downloads, and builds/tests use
`-mod=vendor`. E0 has no imported third-party package yet, so `go.sum` is empty
and `vendor/README.md` records the intentionally empty vendor set. Once a real,
approved import exists, the exact version, `go.sum`, and generated vendor tree
must be committed together.

Dependency maintenance is an explicit environment-level operation. During such
an approved task only, override the offline defaults for the specific pinned
module, then regenerate all metadata:

```bash
GOPROXY=https://proxy.golang.org,direct GOFLAGS=-mod=mod go get example.org/module@vX.Y.Z
GOFLAGS=-mod=mod go mod tidy
GOFLAGS=-mod=mod go mod verify
GOFLAGS=-mod=mod go mod vendor
./scripts/verify.sh
```

Never leave `@latest` in the workflow or dependency records.

## Cache recovery

The `.cache/` directory is disposable and ignored by Git. If it is absent, a
fresh `source env/env.sh` recreates its directory layout. With the committed
vendor tree, `./scripts/verify.sh` rebuilds build/test cache without network and
without reselecting dependency versions. A corrupt cache may be moved aside and
recreated the same way. The frozen toolchain under `vortex-work/tools/go1.26.2`
is not a cache; if it is damaged, restore the exact official 1.26.2 distribution
and verify the recorded hashes rather than selecting a newer Go version.

