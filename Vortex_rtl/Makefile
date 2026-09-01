PYTHON ?= python3
XLEN ?= 32

CONFIG_GEN := ci/gen_config.py
CONFIG_HEADERS := hw/VX_config.vh hw/VX_types.vh hw/vortex_opae.vh

.PHONY: all config

all: config

config: $(CONFIG_HEADERS)

hw/VX_config.vh: VX_config.toml $(CONFIG_GEN)
	XLEN=$(XLEN) $(PYTHON) $(CONFIG_GEN) --config $< --output $@ --format verilog

hw/VX_types.vh: VX_types.toml $(CONFIG_GEN)
	XLEN=$(XLEN) $(PYTHON) $(CONFIG_GEN) --config $< --output $@ --format verilog --resolved

hw/vortex_opae.vh: vortex_opae.toml $(CONFIG_GEN)
	XLEN=$(XLEN) $(PYTHON) $(CONFIG_GEN) --config $< --output $@ --format verilog
