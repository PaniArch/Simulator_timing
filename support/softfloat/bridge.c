#include "bridge.h"

#include <softfloat.h>

static float32_t sf_to_f32(uint32_t bits) {
    float32_t value = {bits};
    return value;
}

static void sf_begin(uint8_t rounding_mode) {
    softfloat_roundingMode = rounding_mode;
    softfloat_detectTininess = softfloat_tininess_afterRounding;
    softfloat_exceptionFlags = 0;
}

static sf_result32 sf_finish(float32_t value) {
    sf_result32 result = {value.v, (uint8_t)softfloat_exceptionFlags};
    return result;
}

static sf_result32 sf_finish_bits(uint32_t bits) {
    sf_result32 result = {bits, (uint8_t)softfloat_exceptionFlags};
    return result;
}

static sf_compare_result sf_finish_compare(bool value) {
    sf_compare_result result = {value, (uint8_t)softfloat_exceptionFlags};
    return result;
}

sf_result32 sf_f32_add(uint32_t a, uint32_t b, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_add(sf_to_f32(a), sf_to_f32(b)));
}

sf_result32 sf_f32_sub(uint32_t a, uint32_t b, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_sub(sf_to_f32(a), sf_to_f32(b)));
}

sf_result32 sf_f32_mul(uint32_t a, uint32_t b, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_mul(sf_to_f32(a), sf_to_f32(b)));
}

sf_result32 sf_f32_div(uint32_t a, uint32_t b, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_div(sf_to_f32(a), sf_to_f32(b)));
}

sf_result32 sf_f32_sqrt(uint32_t a, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_sqrt(sf_to_f32(a)));
}

sf_result32 sf_f32_fma(uint32_t a, uint32_t b, uint32_t c, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(f32_mulAdd(sf_to_f32(a), sf_to_f32(b), sf_to_f32(c)));
}

sf_result32 sf_i32_to_f32(uint32_t bits, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(i32_to_f32((int32_t)bits));
}

sf_result32 sf_ui32_to_f32(uint32_t bits, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish(ui32_to_f32(bits));
}

sf_result32 sf_f32_to_i32(uint32_t bits, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish_bits((uint32_t)f32_to_i32(sf_to_f32(bits), rounding_mode, true));
}

sf_result32 sf_f32_to_ui32(uint32_t bits, uint8_t rounding_mode) {
    sf_begin(rounding_mode);
    return sf_finish_bits((uint32_t)f32_to_ui32(sf_to_f32(bits), rounding_mode, true));
}

sf_compare_result sf_f32_eq(uint32_t a, uint32_t b) {
    sf_begin(softfloat_round_near_even);
    return sf_finish_compare(f32_eq(sf_to_f32(a), sf_to_f32(b)));
}

sf_compare_result sf_f32_lt(uint32_t a, uint32_t b) {
    sf_begin(softfloat_round_near_even);
    return sf_finish_compare(f32_lt(sf_to_f32(a), sf_to_f32(b)));
}

sf_compare_result sf_f32_le(uint32_t a, uint32_t b) {
    sf_begin(softfloat_round_near_even);
    return sf_finish_compare(f32_le(sf_to_f32(a), sf_to_f32(b)));
}
