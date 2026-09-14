SIMULATOR_ROOT := $(realpath ../..)
WORKSPACE_ROOT := $(realpath $(SIMULATOR_ROOT)/..)

VORTEX_HOME ?= $(WORKSPACE_ROOT)/vortex
VORTEX_BUILD ?= $(WORKSPACE_ROOT)/build
OUT_DIR ?= $(SIMULATOR_ROOT)/.cache/vortex-runtime
XLEN ?= 32

GO_LIB := $(OUT_DIR)/libsimtiminggo.so
GO_HEADER := $(OUT_DIR)/libsimtiminggo.h
BACKEND := $(OUT_DIR)/libvortex-simtiming.so

XCONFIGS := $(shell python3 $(SIMULATOR_ROOT)/Vortex_rtl/ci/gen_config.py --config=$(SIMULATOR_ROOT)/Vortex_rtl/VX_config.toml --cflags='-DVX_CFG_XLEN=$(XLEN)')

CXXFLAGS := -std=c++17 -O2 -DNDEBUG -Wall -Wextra -Wfatal-errors -Werror -fPIC
CXXFLAGS += $(XCONFIGS)
CXXFLAGS += -I$(OUT_DIR)
CXXFLAGS += -I$(VORTEX_BUILD)/sw -I$(VORTEX_BUILD)/hw
CXXFLAGS += -I$(VORTEX_HOME)/sw/runtime/include -I$(VORTEX_HOME)/sw/runtime/common
CXXFLAGS += -I$(VORTEX_HOME)/sw -I$(VORTEX_HOME)/hw
CXXFLAGS += -I$(VORTEX_HOME)/sim/common -I$(VORTEX_HOME)/sim/simx -I$(VORTEX_HOME)/sw/common

NATIVE_SRCS := native/vortex.cpp \
	$(VORTEX_HOME)/sw/runtime/common/utils.cpp \
	$(VORTEX_HOME)/sim/common/cmd_processor.cpp

.PHONY: all check clean

all: $(BACKEND)

$(OUT_DIR):
	mkdir -p $@

$(GO_LIB): $(shell find $(SIMULATOR_ROOT) -type f -name '*.go' -not -path '*/.cache/*') | $(OUT_DIR)
	cd $(SIMULATOR_ROOT) && go build -mod=vendor -buildmode=c-shared -o $@ ./cmd/simtiming-go

$(GO_HEADER): $(GO_LIB)
	@test -s $@

$(BACKEND): $(NATIVE_SRCS) $(GO_LIB) $(GO_HEADER) Makefile $(SIMULATOR_ROOT)/Vortex_rtl/VX_config.toml
	/usr/bin/g++ $(CXXFLAGS) $(NATIVE_SRCS) \
		-shared -pthread -Wl,-z,defs -Wl,-rpath,'$$ORIGIN' \
		-L$(OUT_DIR) -lsimtiminggo -o $@

check: all
	@test -s $(GO_LIB)
	@test -s $(BACKEND)
	@ldd $(BACKEND)

clean:
	rm -f $(GO_LIB) $(GO_HEADER) $(BACKEND)
