#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"

fail() {
    echo "environment-check: error: $*" >&2
    exit 1
}

[[ -n "${SIMULATOR_ENV_ROOT:-}" ]] || fail "SIMULATOR_ENV_ROOT is not set"
[[ -n "${SIMULATOR_RUNTIME_ROOT:-}" ]] || fail "SIMULATOR_RUNTIME_ROOT is not set"
environment_root="$(cd -- "${SIMULATOR_ENV_ROOT}" && pwd -P)"
[[ "${environment_root}" == "/opt/simulator-environment" || \
   "${environment_root}" == "${project_root}/.harness-environment/v1" ]] || \
    fail "environment root is outside the supported fixed locations: ${environment_root}"

[[ -x "${environment_root}/go1.26.2/bin/go" ]] || fail "pinned Go executable is missing"
[[ -f "${environment_root}/softfloat/.harness-source-commit" ]] || fail "SoftFloat commit marker is missing"
[[ -f "${environment_root}/softfloat/source/include/softfloat.h" ]] || fail "SoftFloat headers are missing"
[[ -s "${environment_root}/softfloat/lib/softfloat.a" ]] || fail "frozen SoftFloat archive is missing"
[[ -z "$(find "${environment_root}" -type l -print -quit)" ]] || fail "environment contains a symbolic link"
[[ -z "$(find "${environment_root}" -perm /222 -print -quit)" ]] || fail "environment contains a writable entry"

if grep -R -n -E '/hpc2hdd/home/zekaiwang/vortex-work/(tools|vortex)(/|$)|\.\./(tools|vortex)(/|$)' \
    "${project_root}/env" "${project_root}/scripts" "${project_root}/support" "${project_root}/docs" >/dev/null; then
    fail "project environment files still reference an external tools or Vortex checkout"
fi

echo "environment-check: PASS (${environment_root})"
