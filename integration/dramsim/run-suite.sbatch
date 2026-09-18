#!/bin/bash
#SBATCH --partition=long_cpu
#SBATCH --cpus-per-task=2
#SBATCH --mem=4G
#SBATCH --time=00:45:00
#SBATCH --job-name=dram-expanded
set -euo pipefail
test -n "${SIMTIMING_SUITE_ROOT:?suite snapshot required}"
test -n "${SLURM_ARRAY_TASK_ID:?array index required}"
python3 "$SIMTIMING_SUITE_ROOT/runner.py" run --root "$SIMTIMING_SUITE_ROOT" --index "$SLURM_ARRAY_TASK_ID"
