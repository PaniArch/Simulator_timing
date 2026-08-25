#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
expected_commit="b51ef8f3201669b2288104c28546fc72532a1ea4"
expected_makefile_sha256="156c6a52b10a00f2914eb7d9505cf87cfbf7d63a93c9ea9c1300fbaeab147b4a"
expected_platform_sha256="2ae1992fc5f0d35e65ee3fd5ca2a4471385b78b0317403f66b304cb799a777d1"

fail() {
    echo "build-softfloat: error: $*" >&2
    exit 1
}

[[ "${SIMULATOR_ROOT:-}" == "${project_root}" ]] || \
    fail "environment is not active; run: source env/env.sh"
[[ "${CGO_ENABLED:-}" == "1" ]] || fail "CGO_ENABLED must be 1"
[[ "${CC:-}" == "/usr/bin/gcc" ]] || fail "CC must be /usr/bin/gcc"
[[ "$(command -v gcc)" == "/usr/bin/gcc" ]] || fail "gcc does not resolve to /usr/bin/gcc"

softfloat_root="${SOFTFLOAT_SOURCE_ROOT:-${VORTEX_ROOT}/third_party/softfloat}"
source_dir="${softfloat_root}/source"
upstream_build_dir="${softfloat_root}/build/Linux-x86_64-GCC"
build_dir="${SOFTFLOAT_BUILD_ROOT:-${project_root}/.cache/softfloat/build}"
makefile="${upstream_build_dir}/Makefile"
platform="${upstream_build_dir}/platform.h"

[[ -d "${source_dir}" ]] || fail "SoftFloat source is missing: ${source_dir}"
[[ -f "${makefile}" ]] || fail "SoftFloat Makefile is missing: ${makefile}"
[[ -f "${platform}" ]] || fail "SoftFloat platform.h is missing: ${platform}"

actual_commit="$(git -C "${softfloat_root}" rev-parse HEAD)"
[[ "${actual_commit}" == "${expected_commit}" ]] || \
    fail "SoftFloat commit is ${actual_commit}; expected ${expected_commit}"
[[ -z "$(git -C "${softfloat_root}" status --short)" ]] || \
    fail "SoftFloat source tree has local changes"

read -r actual_makefile_sha256 _ < <(sha256sum "${makefile}")
read -r actual_platform_sha256 _ < <(sha256sum "${platform}")
[[ "${actual_makefile_sha256}" == "${expected_makefile_sha256}" ]] || \
    fail "SoftFloat Makefile checksum mismatch"
[[ "${actual_platform_sha256}" == "${expected_platform_sha256}" ]] || \
    fail "SoftFloat platform.h checksum mismatch"

mkdir -p -- "${build_dir}"
if [[ ! -f "${build_dir}/platform.h" ]] || ! cmp -s "${platform}" "${build_dir}/platform.h"; then
    install -m 0644 "${platform}" "${build_dir}/platform.h"
fi

echo "build-softfloat: building Release 3e (${expected_commit:0:12})"
make -s -C "${build_dir}" -f "${makefile}" \
    SOURCE_DIR="${source_dir}" \
    SPECIALIZE_TYPE=RISCV \
    SOFTFLOAT_OPTS="-fPIC -DSOFTFLOAT_ROUND_ODD -DINLINE_LEVEL=5 -DSOFTFLOAT_FAST_DIV32TO16 -DSOFTFLOAT_FAST_DIV64TO32"

archive="${build_dir}/softfloat.a"
[[ -s "${archive}" ]] || fail "SoftFloat archive was not produced"
for symbol in f32_add f32_sub f32_mul f32_mulAdd f32_div f32_sqrt \
    f32_to_i32 f32_to_ui32 i32_to_f32 ui32_to_f32 f32_eq f32_lt f32_le; do
    nm -g --defined-only "${archive}" | grep -Eq "[[:space:]]${symbol}$" || \
        fail "SoftFloat archive is missing symbol ${symbol}"
done

echo "build-softfloat: PASS (${archive})"
