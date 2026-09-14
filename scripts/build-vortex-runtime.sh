#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
simulator_root="$(cd -- "${script_dir}/.." && pwd -P)"
workspace_root="$(dirname -- "${simulator_root}")"

source "${simulator_root}/env/env.sh"

export VORTEX_HOME="${VORTEX_HOME:-${workspace_root}/vortex}"
export VORTEX_BUILD="${VORTEX_BUILD:-${workspace_root}/build}"

[[ -f "${VORTEX_HOME}/VX_config.toml" ]] || {
    echo "error: Vortex source tree is missing: ${VORTEX_HOME}" >&2
    exit 1
}
[[ -f "${VORTEX_BUILD}/sw/runtime/libvortex.so" ]] || {
    echo "error: built native runtime is missing: ${VORTEX_BUILD}/sw/runtime/libvortex.so" >&2
    exit 1
}

make -C "${simulator_root}/integration/vortex-runtime" \
    VORTEX_HOME="${VORTEX_HOME}" VORTEX_BUILD="${VORTEX_BUILD}" check
