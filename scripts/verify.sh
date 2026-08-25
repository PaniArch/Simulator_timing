#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
project_root="$(cd -- "${script_dir}/.." && pwd -P)"
expected_go="$(cd -- "${project_root}/../tools/go1.26.2/bin" && pwd -P)/go"
expected_version="go1.26.2"
expected_go_sha256="1a0f01bdb35c622c78bdc49f26f85dda53f9d7955f453f63c4c023b0dc7c754d"

fail() {
    echo "verify: error: $*" >&2
    exit 1
}

[[ "${SIMULATOR_ROOT:-}" == "${project_root}" ]] || \
    fail "environment is not active; run: source env/env.sh"

actual_go="$(command -v go)"
[[ "${actual_go}" == "${expected_go}" ]] || \
    fail "wrong go executable: ${actual_go}; expected ${expected_go}"

[[ "$(go env GOVERSION)" == "${expected_version}" ]] || \
    fail "wrong Go version: $(go env GOVERSION); expected ${expected_version}"
[[ "$(go env GOROOT)" == "$(dirname -- "$(dirname -- "${expected_go}")")" ]] || \
    fail "GOROOT does not point at the frozen toolchain"
[[ "$(go env GOTOOLCHAIN)" == "local" ]] || fail "GOTOOLCHAIN must be local"
[[ "${GOENV:-}" == "off" ]] || fail "GOENV must be off"
[[ "$(go env GOWORK)" == "off" ]] || fail "GOWORK must be off"
[[ "$(go env GOMODCACHE)" == "${project_root}/.cache/go-mod" ]] || \
    fail "GOMODCACHE is not project-local"
[[ "$(go env GOCACHE)" == "${project_root}/.cache/go-build" ]] || \
    fail "GOCACHE is not project-local"
[[ "$(go env GOPATH)" == "${project_root}/.cache/gopath" ]] || \
    fail "GOPATH is not project-local"
[[ "$(go env GOFLAGS)" == "-mod=vendor" ]] || fail "GOFLAGS must be -mod=vendor"
[[ "$(go env GOPROXY)" == "off" ]] || fail "GOPROXY must be off"
[[ "$(go env CGO_ENABLED)" == "1" ]] || fail "CGO_ENABLED must be 1"
[[ "$(go env CC)" == "/usr/bin/gcc" ]] || fail "CC must be /usr/bin/gcc"
[[ "${CXX:-}" == "/usr/bin/g++" ]] || fail "CXX must be /usr/bin/g++"
[[ "${AR:-}" == "/usr/bin/ar" ]] || fail "AR must be /usr/bin/ar"
[[ "$(gcc -dumpfullversion -dumpversion)" == "9.4.0" ]] || fail "GCC must be 9.4.0"
[[ "$(g++ -dumpfullversion -dumpversion)" == "9.4.0" ]] || fail "G++ must be 9.4.0"

read -r actual_go_sha256 _ < <(sha256sum "${expected_go}")
[[ "${actual_go_sha256}" == "${expected_go_sha256}" ]] || \
    fail "frozen Go binary checksum mismatch"

cd -- "${project_root}"

[[ "$(go env GOMOD)" == "${project_root}/go.mod" ]] || fail "go.mod is not active"
grep -qx 'go 1.26.0' go.mod || fail "go.mod must declare go 1.26.0"
grep -qx 'toolchain go1.26.2' go.mod || fail "go.mod must freeze toolchain go1.26.2"

echo "verify: SoftFloat"
./scripts/build-softfloat.sh

echo "verify: go mod verify"
go mod verify

if packages="$(go list -mod=vendor ./...)"; then
    if [[ -n "${packages}" ]]; then
        echo "verify: go build -mod=vendor ./..."
        go build -mod=vendor ./...

        echo "verify: go test -mod=vendor ./..."
        go test -mod=vendor ./...

        echo "verify: go vet -mod=vendor ./..."
        go vet -mod=vendor ./...
    else
        echo "verify: no Go packages yet; test and vet checks skipped"
    fi
else
    fail "go list -mod=vendor ./... failed"
fi

echo "verify: gofmt"
mapfile -d '' go_files < <(
    find . -type f -name '*.go' \
        -not -path './vendor/*' \
        -not -path './.cache/*' \
        -not -path './.env-cache/*' \
        -print0
)
if ((${#go_files[@]} > 0)); then
    unformatted="$(gofmt -l "${go_files[@]}")"
    [[ -z "${unformatted}" ]] || fail "gofmt required for:\n${unformatted}"
else
    echo "verify: no Go package files yet; formatting check skipped"
fi

echo "verify: git diff --check"
git diff --check

echo "verify: PASS (${expected_version}, vendor/offline mode)"
