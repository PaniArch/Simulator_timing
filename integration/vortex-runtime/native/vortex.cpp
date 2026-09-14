// Native Vortex transport backend for Simulator_timing.
//
// The common Vortex runtime retains ownership of buffers, queues, modules,
// events, arguments and CP commands. This file provides the ordinary backend
// callback table and routes device memory / launch DCRs to the selected Go
// execution owner.

#include <VX_types.h>
#include <common.h>
#include <constants.h>
#include <cmd_processor.h>
#include <util.h>

#include <algorithm>
#include <chrono>
#include <cstring>
#include <iostream>
#include <map>
#include <mutex>
#include <stdint.h>
#include <stdlib.h>
#include <thread>

static_assert(VX_CFG_XLEN == 32 && VX_CFG_NUM_CORES == 1 && VX_CFG_NUM_CLUSTERS == 1
              && VX_CFG_NUM_WARPS == 4 && VX_CFG_NUM_THREADS == 4,
              "Simulator_timing requires the frozen single-core RV32 topology");

#include <libsimtiminggo.h>

using namespace vortex;

class vx_device {
public:
  vx_device()
      : handle_(simtiming_create()),
        cp_(make_cp_hooks()) {}

  ~vx_device() {
    while (handle_ != 0 && simtiming_busy(handle_) != 0)
      std::this_thread::sleep_for(std::chrono::milliseconds(1));
    for (auto& region : host_regions_)
      free(reinterpret_cast<void*>(region.first));
    host_regions_.clear();
    if (handle_ != 0)
      (void)simtiming_destroy(handle_);
  }

  int init() {
    if (handle_ == 0) {
      std::cerr << "[SIMTIMING] origin=external-connection op=device-open: "
                   "Go backend creation failed" << std::endl;
      return -1;
    }
    return 0;
  }

  int cp_reg_write(uint32_t off, uint32_t value) {
    cp_.mmio_write(off, value);
    for (int i = 0; i < 256 && cp_.busy(); ++i)
      cp_.tick();
    return check_bridge_error();
  }

  int cp_reg_read(uint32_t off, uint32_t* value) {
    if (value == nullptr)
      return -1;
    for (int i = 0; i < 256 && cp_.busy(); ++i)
      cp_.tick();
    *value = cp_.mmio_read(off);
    return check_bridge_error();
  }

  int host_mem_alloc(uint64_t size, void** host_ptr, uint64_t* cp_addr) {
    if (host_ptr == nullptr || cp_addr == nullptr || size == 0
        || size > SIZE_MAX - (CACHE_BLOCK_SIZE - 1))
      return -1;
    uint64_t asize = aligned_size(size, CACHE_BLOCK_SIZE);
    void* ptr = aligned_alloc(CACHE_BLOCK_SIZE, asize);
    if (ptr == nullptr)
      return -1;
    std::lock_guard<std::mutex> guard(host_mu_);
    host_regions_[reinterpret_cast<uint64_t>(ptr)] = asize;
    *host_ptr = ptr;
    *cp_addr = reinterpret_cast<uint64_t>(ptr);
    return 0;
  }

  int host_mem_free(uint64_t cp_addr) {
    {
      std::lock_guard<std::mutex> guard(host_mu_);
      auto iter = host_regions_.find(cp_addr);
      if (iter == host_regions_.end())
        return -1;
      host_regions_.erase(iter);
    }
    free(reinterpret_cast<void*>(cp_addr));
    return 0;
  }

private:
  void* host_region_ptr(uint64_t address, std::size_t bytes) {
    std::lock_guard<std::mutex> guard(host_mu_);
    if (host_regions_.empty())
      return nullptr;
    auto iter = host_regions_.upper_bound(address);
    if (iter == host_regions_.begin())
      return nullptr;
    --iter;
    if (address < iter->first)
      return nullptr;
    const uint64_t offset = address - iter->first;
    if (offset > iter->second || bytes > iter->second - offset)
      return nullptr;
    return reinterpret_cast<void*>(address);
  }

  void note_transport_error(const char* operation, uint64_t address,
                            std::size_t bytes) {
    std::lock_guard<std::mutex> guard(error_mu_);
    if (!transport_error_.empty())
      return;
    transport_error_ = std::string("origin=external-connection op=") +
                       operation + ": address=" + std::to_string(address) +
                       " bytes=" + std::to_string(bytes);
  }

  int check_bridge_error() {
    char message[2048] = {};
    int status = simtiming_error(handle_, message, sizeof(message));
    std::string error;
    if (status < 0) {
      error = "origin=external-connection op=error-query: invalid Go device handle";
    } else if (status > 0) {
      error = message;
    }
    {
      std::lock_guard<std::mutex> guard(error_mu_);
      if (error.empty())
        error = transport_error_;
      if (!error.empty() && !error_reported_) {
        std::cerr << "[SIMTIMING] " << error << std::endl;
        error_reported_ = true;
      }
    }
    return error.empty() ? 0 : -1;
  }

  vortex::CommandProcessor::Hooks make_cp_hooks() {
    vortex::CommandProcessor::Hooks hooks;
    hooks.dram_read = [this](uint64_t address, void* destination,
                             std::size_t bytes) {
      if (void* host = host_region_ptr(address, bytes)) {
        std::memcpy(destination, host, bytes);
        return;
      }
      if (simtiming_mem_read(handle_, address, destination, bytes) != 0) {
        std::memset(destination, 0, bytes);
        note_transport_error("memory-read", address, bytes);
      }
    };
    hooks.dram_write = [this](uint64_t address, const void* source,
                              std::size_t bytes) {
      if (void* host = host_region_ptr(address, bytes)) {
        std::memcpy(host, source, bytes);
        return;
      }
      if (simtiming_mem_write(handle_, address, const_cast<void*>(source), bytes) != 0)
        note_transport_error("memory-write", address, bytes);
    };
    hooks.vortex_dcr_write = [this](uint32_t address, uint32_t value) {
      if (simtiming_dcr_write(handle_, address, value) != 0)
        note_transport_error("dcr-write", address, sizeof(value));
    };
    hooks.vortex_dcr_read = [this](uint32_t address, uint32_t tag) -> uint32_t {
      uint32_t value = 0;
      if (simtiming_dcr_read(handle_, address, tag, &value) != 0)
        note_transport_error("dcr-read", address, sizeof(value));
      return value;
    };
    hooks.vortex_start = [this]() {
      if (simtiming_start(handle_) != 0)
        note_transport_error("launch-start", 0, 0);
    };
    hooks.vortex_busy = [this]() -> bool {
      return simtiming_busy(handle_) != 0;
    };
    return hooks;
  }

  uint64_t handle_;
  vortex::CommandProcessor cp_;
  std::mutex host_mu_;
  std::map<uint64_t, uint64_t> host_regions_;
  std::mutex error_mu_;
  std::string transport_error_;
  bool error_reported_ = false;
};

#include <callbacks.inc>
