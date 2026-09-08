#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
source "${script_dir}/../env/env.sh"
cd "${SIMULATOR_ROOT}"
# Diagnostics stay visible on stderr; successful validation has no stdout payload.
go test ./timing/check >&2
go run ./timing/check "${SIMULATOR_ROOT}" >&2
