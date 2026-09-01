#!/usr/bin/env bash

# This file is the single supported environment entry point for Simulator_dev1.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    echo "error: source this file: source env/env.sh" >&2
    exit 1
fi

_sim_env_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || return 1
export SIMULATOR_ROOT="$(cd -- "${_sim_env_dir}/.." && pwd -P)" || return 1
if [[ -d /opt/simulator-environment ]]; then
    export SIMULATOR_ENV_ROOT=/opt/simulator-environment
elif [[ -d "${SIMULATOR_ROOT}/.harness-environment/v1" ]]; then
    export SIMULATOR_ENV_ROOT="${SIMULATOR_ROOT}/.harness-environment/v1"
else
    echo "error: the pinned Simulator environment is unavailable" >&2
    echo "expected /opt/simulator-environment or ${SIMULATOR_ROOT}/.harness-environment/v1" >&2
    unset _sim_env_dir
    return 1
fi

export GO_TOOLCHAIN_ROOT="${SIMULATOR_ENV_ROOT}/go1.26.2"
export SOFTFLOAT_SOURCE_ROOT="${SIMULATOR_ENV_ROOT}/softfloat"
export SOFTFLOAT_ARCHIVE="${SOFTFLOAT_SOURCE_ROOT}/lib/softfloat.a"
export SIMULATOR_RUNTIME_ROOT="${HARNESS_RUNTIME_DIR:-${SIMULATOR_ROOT}/.cache}"
export SIMULATOR_LOG_DIR="${SIMULATOR_RUNTIME_ROOT}/logs"

if [[ ! -x "${GO_TOOLCHAIN_ROOT}/bin/go" ]]; then
    echo "error: frozen Go toolchain is missing: ${GO_TOOLCHAIN_ROOT}/bin/go" >&2
    unset _sim_env_dir
    return 1
fi

export GOROOT="${GO_TOOLCHAIN_ROOT}"
export PATH="${GOROOT}/bin:/usr/bin:/bin"
export GOTOOLCHAIN=local
export GOENV=off
export GOWORK=off
export GO111MODULE=on
export CGO_ENABLED=1
export CC=/usr/bin/gcc
export CXX=/usr/bin/g++
export AR=/usr/bin/ar

export GOCACHE="${SIMULATOR_RUNTIME_ROOT}/go-build"
export GOMODCACHE="${SIMULATOR_RUNTIME_ROOT}/go-mod"
export GOPATH="${SIMULATOR_RUNTIME_ROOT}/gopath"
export GOBIN="${SIMULATOR_RUNTIME_ROOT}/bin"
export GOTMPDIR="${SIMULATOR_RUNTIME_ROOT}/tmp"

# T0-T7 build and test commands are offline and vendor-backed by default.
export GOFLAGS=-mod=vendor
export GOPROXY=off
export GOSUMDB=off

if [[ ! -d "${SOFTFLOAT_SOURCE_ROOT}/source" ]]; then
    echo "error: Vortex SoftFloat source is missing: ${SOFTFLOAT_SOURCE_ROOT}" >&2
    unset _sim_env_dir
    return 1
fi
if [[ ! -r "${SOFTFLOAT_ARCHIVE}" ]]; then
    echo "error: frozen SoftFloat archive is missing: ${SOFTFLOAT_ARCHIVE}" >&2
    unset _sim_env_dir
    return 1
fi

mkdir -p -- "${GOCACHE}" "${GOMODCACHE}" "${GOPATH}" "${GOBIN}" "${GOTMPDIR}" \
    "${SIMULATOR_LOG_DIR}" || return 1

export CGO_CFLAGS="-I${SOFTFLOAT_SOURCE_ROOT}/source/include"
export CGO_LDFLAGS="${SOFTFLOAT_ARCHIVE}"
unset _sim_env_dir
