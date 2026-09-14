#!/usr/bin/env bash
# Deliberately small native-runtime smoke set; not the full benchmark suite.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
simulator_root="$(cd -- "${script_dir}/.." && pwd -P)"
mkdir -p "${simulator_root}/.cache/runtime-smoke"
result_dir="$(mktemp -d "${simulator_root}/.cache/runtime-smoke/run-XXXXXX")"
printf 'mode\tbenchmark\texit_code\n' > "${result_dir}/summary.tsv"
export GOMAXPROCS="${GOMAXPROCS:-4}"
failed=0
for mode in timing functional; do
    for benchmark in vecadd demo relu; do
        args=(-n16)
        if [[ "${benchmark}" == demo ]]; then args=(-n4 -x4 -y1); fi
        echo "RUN ${mode} ${benchmark} ${args[*]}"
        code=0
        SIMTIMING_EVENT_LOG="${result_dir}/${mode}-${benchmark}.events.jsonl" \
            timeout "${SIMTIMING_BENCH_TIMEOUT:-180s}" \
            "${script_dir}/run-vortex-benchmark.sh" --mode "${mode}" "${benchmark}" "${args[@]}" \
            > "${result_dir}/${mode}-${benchmark}.stdout.log" \
            2> "${result_dir}/${mode}-${benchmark}.stderr.log" || code=$?
        printf '%s\t%s\t%d\n' "${mode}" "${benchmark}" "${code}" >> "${result_dir}/summary.tsv"
        echo "DONE ${mode} ${benchmark} exit=${code}"
        if ((code != 0)); then failed=1; fi
    done
done
echo "RESULT_DIR=${result_dir}"
cat "${result_dir}/summary.tsv"
exit "${failed}"
