#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == --mode ]]; then
    export SIMTIMING_MODE="${2:?missing mode}"
    shift 2
fi
export SIMTIMING_MODE="${SIMTIMING_MODE:-timing}"
case "${SIMTIMING_MODE}" in timing|functional) ;; *) echo "error: mode must be timing or functional" >&2; exit 2;; esac

if (($# < 1)); then
    echo "usage: $0 [--mode timing|functional] <benchmark> [arguments...]" >&2
    exit 2
fi

benchmark="$1"
shift

case "${benchmark}" in
    async_barrier|conv3|demo|diverge|dogfood|dotproduct|dotproduct2|dropout|fence|io_addr|jacobi|madmax|mstress|multikernel|occupancy|packld|pathfinder|raycast|relu|sgemm|sgemm2|sgemmx|sgemv|softmax|sort|stencil3d|vecadd|wgather) ;;
    *)
        echo "error: benchmark is outside the runtime launcher allowlist (not a timing coverage guarantee): ${benchmark}" >&2
        exit 2
        ;;
esac

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
simulator_root="$(cd -- "${script_dir}/.." && pwd -P)"
workspace_root="$(dirname -- "${simulator_root}")"
vortex_build="${VORTEX_BUILD:-${workspace_root}/build}"
backend_dir="${SIMTIMING_BACKEND_DIR:-${simulator_root}/.cache/vortex-runtime}"
runtime_dir="${vortex_build}/sw/runtime"
benchmark_dir="${vortex_build}/tests/regression/${benchmark}"

[[ -s "${backend_dir}/libvortex-simtiming.so" ]] || {
    echo "error: simtiming backend is missing; run scripts/build-vortex-runtime.sh" >&2
    exit 2
}
[[ -s "${runtime_dir}/libvortex.so" ]] || {
    echo "error: native Vortex runtime is missing: ${runtime_dir}/libvortex.so" >&2
    exit 2
}
[[ -x "${benchmark_dir}/${benchmark}" && -s "${benchmark_dir}/kernel.vxbin" ]] || {
    echo "error: built benchmark or kernel is missing: ${benchmark_dir}" >&2
    exit 2
}

export VORTEX_DRIVER=simtiming
export LD_LIBRARY_PATH="${backend_dir}:${runtime_dir}${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"

cd -- "${benchmark_dir}"
exec "./${benchmark}" "$@"
