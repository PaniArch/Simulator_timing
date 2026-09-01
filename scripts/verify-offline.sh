#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
project_root="$(cd -- "${script_dir}/.." && pwd -P)"
source "${project_root}/env/env.sh"
"${project_root}/scripts/environment-check.sh"

offline_root="$(mktemp -d "${project_root}/.cache/offline-verify.XXXXXX")"
cleanup() {
    chmod -R u+w -- "${offline_root}" 2>/dev/null || true
    rm -rf -- "${offline_root}"
}
trap cleanup EXIT

export GOCACHE="${offline_root}/go-build"
export GOMODCACHE="${offline_root}/go-mod"
export GOPATH="${offline_root}/gopath"
export GOBIN="${offline_root}/bin"
export GOTMPDIR="${offline_root}/tmp"
export GOTOOLCHAIN=local
export GOENV=off
export GOWORK=off
export GOFLAGS=-mod=vendor
export GOPROXY=off
export GOSUMDB=off
mkdir -p -- "${GOCACHE}" "${GOMODCACHE}" "${GOPATH}" "${GOBIN}" "${GOTMPDIR}"

cd -- "${project_root}"

[[ "$(go env GOVERSION)" == "go1.26.2" ]]
[[ "$(go env GOTOOLCHAIN)" == "local" ]]
[[ "$(go env GOPROXY)" == "off" ]]
[[ "$(go env GOSUMDB)" == "off" ]]
[[ "$(go env GOFLAGS)" == "-mod=vendor" ]]

echo "verify-offline: go list"
go list -mod=vendor ./... >/dev/null
echo "verify-offline: go build"
go build -mod=vendor ./...
echo "verify-offline: go test"
go test -mod=vendor ./...
echo "verify-offline: go vet"
go vet -mod=vendor ./...
echo "verify-offline: PASS (empty caches, vendor only, network disabled)"
