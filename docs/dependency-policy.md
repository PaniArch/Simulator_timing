# Dependency policy

The cycle baseline is offline by default. `env/env.sh` fixes:

- `GOTOOLCHAIN=local`
- `GOFLAGS=-mod=vendor`
- `GOPROXY=off`
- `GOSUMDB=off`

Normal Harness work must not run `go get`, install external Go modules, clone
dependency repositories, or download dependencies with curl/wget. All builds
and tests must resolve from the committed `vendor/` tree and the frozen local
toolchain capsule.

Akita is pinned exactly at `github.com/sarchlab/akita/v5 v5.0.0-beta.10`.
MGPUSim is not a module dependency. Adding or changing a framework dependency
requires a separately reviewed support-baseline update with regenerated
`go.mod`, `go.sum`, `vendor/`, and a fresh empty-cache offline verification.
