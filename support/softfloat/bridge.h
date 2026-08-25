#ifndef SIMULATOR_SOFTFLOAT_BRIDGE_H
#define SIMULATOR_SOFTFLOAT_BRIDGE_H

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    uint32_t bits;
    uint8_t flags;
} sf_result32;

typedef struct {
    bool value;
    uint8_t flags;
} sf_compare_result;

sf_result32 sf_f32_add(uint32_t a, uint32_t b, uint8_t rounding_mode);
sf_result32 sf_f32_sub(uint32_t a, uint32_t b, uint8_t rounding_mode);
sf_result32 sf_f32_mul(uint32_t a, uint32_t b, uint8_t rounding_mode);
sf_result32 sf_f32_div(uint32_t a, uint32_t b, uint8_t rounding_mode);
sf_result32 sf_f32_sqrt(uint32_t a, uint8_t rounding_mode);
sf_result32 sf_f32_fma(uint32_t a, uint32_t b, uint32_t c, uint8_t rounding_mode);

sf_result32 sf_i32_to_f32(uint32_t bits, uint8_t rounding_mode);
sf_result32 sf_ui32_to_f32(uint32_t bits, uint8_t rounding_mode);
sf_result32 sf_f32_to_i32(uint32_t bits, uint8_t rounding_mode);
sf_result32 sf_f32_to_ui32(uint32_t bits, uint8_t rounding_mode);

sf_compare_result sf_f32_eq(uint32_t a, uint32_t b);
sf_compare_result sf_f32_lt(uint32_t a, uint32_t b);
sf_compare_result sf_f32_le(uint32_t a, uint32_t b);

#endif
