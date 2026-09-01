#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"

echo "verify-all: RTL snapshot integrity"
(
  cd "${script_dir}/../Vortex_rtl"
  sha256sum --check --quiet MANIFEST.sha256
)

"${script_dir}/verify-emu.sh"
"${script_dir}/verify-offline.sh"

echo "verify-all: PASS"
