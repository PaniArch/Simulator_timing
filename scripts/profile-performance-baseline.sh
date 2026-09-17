#!/usr/bin/env bash
# Bounded, repository-only workloads. Run after ordinary benchmem sampling.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."
source env/env.sh
baseline_out="${1:-${SIMULATOR_RUNTIME_ROOT}/performance-closure}"
mkdir -p -- "$baseline_out"
baseline_out="$(cd -- "$baseline_out" && pwd -P)"
baseline_mode="${2:-sample}"
case "$baseline_mode" in
    sample|reports-only) ;;
    *) echo 'usage: bash scripts/profile-performance-baseline.sh [output-directory] [sample|reports-only]' >&2; exit 2 ;;
esac

profile_case() {
    local name="$1" package="$2" pattern="$3" repeats="$4" focus="$5"
    if [[ "$baseline_mode" == sample ]]; then
        go test -mod=vendor -run '^$' -bench "$pattern" -benchtime="${repeats}x" \
            -benchmem -count=1 -timeout=10m \
            -cpuprofile "$baseline_out/$name.cpu" \
            -memprofile "$baseline_out/$name.alloc" \
            -o "$baseline_out/$name.test" "$package" \
            > "$baseline_out/$name.profile-bench.txt" 2>&1
    fi
    for metric in cpu alloc_space alloc_objects; do
        local profile="$baseline_out/$name.alloc"
        if [[ "$metric" == cpu ]]; then profile="$baseline_out/$name.cpu"; fi
        go tool pprof -top -cum -nodecount=60 -sample_index="$metric" \
            "$baseline_out/$name.test" "$profile" \
            > "$baseline_out/$name.$metric.txt"
        # Percentages here use only stacks containing the execution entry.
        # GC worker stacks cannot be attributed to this entry by this filter.
        go tool pprof -top -cum -nodecount=60 -sample_index="$metric" \
            -focus="$focus" -relative_percentages \
            "$baseline_out/$name.test" "$profile" \
            > "$baseline_out/$name.$metric.execute.txt"
        go tool pprof -top -cum -nodecount=0 -nodefraction=0 \
            -sample_index="$metric" -focus="$focus" -relative_percentages \
            "$baseline_out/$name.test" "$profile" \
            > "$baseline_out/$name.$metric.detail.txt"
    done
}

profile_case clock ./timing/model '^BenchmarkClockBaseline$/single/execute$' 1000 'model\.\(\*Clock\)\.Run'
for kind in multi kernel; do
    for diag in false true; do
        profile_case "$kind-$diag" ./timing/runner \
            "^BenchmarkRunnerBaseline$/$kind/single/diag=$diag/execute$" 5 \
            'runner\.\(\*baselineFixture\)\.drive'
    done
done
