#!/bin/bash
#SBATCH --job-name=simtiming-supported
#SBATCH --nodes=1
#SBATCH --ntasks=1
#SBATCH --cpus-per-task=4
#SBATCH --mem=8G

set -euo pipefail
# A failed case must not suppress the rest of the declared suite. Each child
# depends on this job ending, so two chains never exceed two running cases.
advance() {
    local code=$?
    trap - EXIT
    if [[ "${code}" == 2 || -e "${SIMTIMING_SUITE_ROOT}/STOP" ]]; then
        echo "CHAIN_PAUSED=${SIMTIMING_CHAIN} (infrastructure failure or STOP)"
        exit 2
    fi
    python3 "${SIMTIMING_SUITE_ROOT:?}/runner.py" advance \
        --root "${SIMTIMING_SUITE_ROOT}" --chain "${SIMTIMING_CHAIN:?}" \
        --position "${SIMTIMING_POSITION:?}" --parent "${SLURM_JOB_ID:?}" || code=2
    exit "${code}"
}
trap advance EXIT
trap 'exit 143' TERM
# Required C++ shared libraries are in the immutable suite snapshot. Do not use
# the submitting shell's module function on a different compute-node image.
python3 "${SIMTIMING_SUITE_ROOT:?}/runner.py" run \
    --root "${SIMTIMING_SUITE_ROOT}" --index "${SIMTIMING_CASE_INDEX:?}"
