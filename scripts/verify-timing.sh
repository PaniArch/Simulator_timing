#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_dir}/../env/env.sh"
cd "${SIMULATOR_ROOT}"
go test ./timing/check
go run ./timing/check "${SIMULATOR_ROOT}"
