#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"

"${script_dir}/verify-emu.sh"
"${script_dir}/verify-offline.sh"

echo "verify-all: PASS"
