#!/usr/bin/env bash

# This file is the single supported environment entry point for Simulator_v0.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    echo "error: source this file: source env/env.sh" >&2
    exit 1
fi

_sim_env_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || return 1
export SIMULATOR_ROOT="$(cd -- "${_sim_env_dir}/.." && pwd -P)" || return 1
export VORTEX_WORK_ROOT="$(cd -- "${SIMULATOR_ROOT}/.." && pwd -P)" || return 1
export GO_TOOLCHAIN_ROOT="${VORTEX_WORK_ROOT}/tools/go1.26.2"

if [[ ! -x "${GO_TOOLCHAIN_ROOT}/bin/go" ]]; then
    echo "error: frozen Go toolchain is missing: ${GO_TOOLCHAIN_ROOT}/bin/go" >&2
    unset _sim_env_dir
    return 1
fi

export GOROOT="${GO_TOOLCHAIN_ROOT}"
export PATH="${GOROOT}/bin:${PATH}"
export GOTOOLCHAIN=local
export GOENV=off
export GOWORK=off
export GO111MODULE=on

export GOCACHE="${SIMULATOR_ROOT}/.cache/go-build"
export GOMODCACHE="${SIMULATOR_ROOT}/.cache/go-mod"
export GOPATH="${SIMULATOR_ROOT}/.cache/gopath"
export GOBIN="${SIMULATOR_ROOT}/.cache/bin"
export GOTMPDIR="${SIMULATOR_ROOT}/.cache/tmp"

# T0-T7 build and test commands are offline and vendor-backed by default.
export GOFLAGS=-mod=vendor
export GOPROXY=off
export GOSUMDB=sum.golang.org

mkdir -p -- "${GOCACHE}" "${GOMODCACHE}" "${GOPATH}" "${GOBIN}" "${GOTMPDIR}" || return 1
unset _sim_env_dir

