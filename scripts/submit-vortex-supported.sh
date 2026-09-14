#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
simulator_root="$(cd -- "${script_dir}/.." && pwd -P)"
build="${VORTEX_BUILD:-$(dirname -- "${simulator_root}")/build}"
module load compilers/gcc-12.2.0
root="$(python3 "${script_dir}/vortex-supported.py" prepare \
    --repository "${simulator_root}" --build "${build}" \
    --timeout "${SIMTIMING_BENCH_TIMEOUT_SECONDS:-1500}")"
echo "RESULT_DIR=${root}"
python3 "${root}/runner.py" submit --root "${root}"
echo "AGGREGATE=python3 ${root}/runner.py aggregate --root ${root}"
