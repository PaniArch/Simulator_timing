// Thin ABI only. DRAM scheduling/address conversion stays in the unmodified
// vortex/sim/common/dram_sim.cpp linked by the Makefile.
#include "dram_sim.h"
#include <deque>
#include <exception>
#include <memory>
#include <string>
#include <unordered_map>

static_assert(VX_CFG_PLATFORM_MEMORY_DATA_SIZE == 64, "bridge requires frozen 64-byte memory bus");
static_assert(VX_CFG_PLATFORM_MEMORY_NUM_BANKS == 2, "runtime bridge currently binds the frozen two-bank platform");
static_assert(VX_CFG_PLATFORM_MEMORY_INTERLEAVE == 1, "bridge requires interleaved physical bus banks");

struct Context;
struct Ticket { Context* owner; uint64_t id; };
struct Context {
  std::unique_ptr<vortex::DramSim> dram;
  std::deque<uint64_t> completed;
  std::unordered_map<uint64_t,std::unique_ptr<Ticket>> tickets;
  std::string error;
};
static bool complete(void* arg) {
  auto* t=static_cast<Ticket*>(arg);
  t->owner->completed.push_back(t->id);
  return true;
}
extern "C" {
void* simdram_create(uint32_t channels,uint32_t bytes,float ratio) {
  try {
    auto c=std::make_unique<Context>();
    c->dram=std::make_unique<vortex::DramSim>(channels,bytes,ratio);
    return c.release();
  } catch (...) { return nullptr; }
}
int simdram_submit(void* ctx,uint64_t id,uint64_t address,int write) {
  auto* c=static_cast<Context*>(ctx);
  try {
    if (c->tickets.count(id)) { c->error="duplicate transaction"; return -1; }
    auto t=std::make_unique<Ticket>(Ticket{c,id}); auto* arg=t.get();
    c->tickets.emplace(id,std::move(t));
    c->dram->send_request(address,write!=0,complete,arg);
    return 0;
  } catch (const std::exception& e) { c->error=e.what(); return -1; }
}
int simdram_tick(void* ctx) {
  auto* c=static_cast<Context*>(ctx);
  try { c->dram->tick(); return 0; }
  catch (const std::exception& e) { c->error=e.what(); return -1; }
}
int simdram_poll(void* ctx,uint64_t* id) {
  auto* c=static_cast<Context*>(ctx);
  if (c->completed.empty()) return 0;
  *id=c->completed.front(); c->completed.pop_front(); c->tickets.erase(*id); return 1;
}
const char* simdram_error(void* ctx) { return static_cast<Context*>(ctx)->error.c_str(); }
void simdram_destroy(void* ctx) {
  auto* c=static_cast<Context*>(ctx);
  // Destroy the engine before releasing callback tokens. No reset between kernels.
  c->dram.reset(); delete c;
}
}
