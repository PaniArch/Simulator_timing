SIMULATOR_ROOT := $(realpath ../..)
VORTEX_HOME ?= $(realpath $(SIMULATOR_ROOT)/../vortex)
RAMULATOR_HOME ?= $(VORTEX_HOME)/third_party/ramulator
OUT_DIR ?= $(SIMULATOR_ROOT)/.cache/vortex-runtime
CXX ?= g++
CPPFLAGS += $(shell python3 $(SIMULATOR_ROOT)/Vortex_rtl/ci/gen_config.py --config=$(SIMULATOR_ROOT)/Vortex_rtl/VX_config.toml --cflags='-DVX_CFG_XLEN=32')
CPPFLAGS += -I$(VORTEX_HOME)/sim/common -I$(VORTEX_HOME)/sw/common
CPPFLAGS += -I$(RAMULATOR_HOME)/src -I$(RAMULATOR_HOME)/ext/yaml-cpp/include -I$(RAMULATOR_HOME)/ext/spdlog/include
CXXFLAGS += -std=c++17 -O2 -fPIC

.PHONY: all
all: $(OUT_DIR)/libsimtiming-dram.so

$(OUT_DIR)/libsimtiming-dram.so: native/bridge.cpp $(VORTEX_HOME)/sim/common/dram_sim.cpp $(VORTEX_HOME)/sim/common/dram_sim.h $(SIMULATOR_ROOT)/Vortex_rtl/VX_config.toml Makefile
	mkdir -p $(OUT_DIR)
	$(CXX) $(CPPFLAGS) $(CXXFLAGS) native/bridge.cpp $(VORTEX_HOME)/sim/common/dram_sim.cpp \
		-shared -Wl,-z,defs -L$(RAMULATOR_HOME) -lramulator -Wl,-rpath,$(RAMULATOR_HOME) -o $@
