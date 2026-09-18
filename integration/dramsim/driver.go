// Package dramsim loads a thin C ABI around the existing RTLSim DramSim.
// The optional shared library is not required by fixed-latency/offline tests.
package dramsim

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>
#include <stdio.h>
typedef struct {
 void *lib, *ctx;
 void* (*create)(uint32_t,uint32_t,float);
 int (*submit)(void*,uint64_t,uint64_t,int);
 int (*tick)(void*);
 int (*poll)(void*,uint64_t*);
 void (*destroy)(void*);
 const char* (*error)(void*);
} dram_api;
static dram_api* dram_open(const char* path, uint32_t channels, uint32_t bytes, float ratio, char* err, size_t n) {
 dram_api* a=(dram_api*)calloc(1,sizeof(*a));
 if (!a) { snprintf(err,n,"allocation failed"); return NULL; }
 a->lib=dlopen(path,RTLD_NOW|RTLD_LOCAL);
 if (!a->lib) { snprintf(err,n,"%s",dlerror()); free(a); return NULL; }
#define LOAD(field,symbol) do { *(void**)(&a->field)=dlsym(a->lib,symbol); if (!a->field) { snprintf(err,n,"missing %s",symbol); dlclose(a->lib); free(a); return NULL; } } while(0)
 LOAD(create,"simdram_create"); LOAD(submit,"simdram_submit"); LOAD(tick,"simdram_tick");
 LOAD(poll,"simdram_poll"); LOAD(destroy,"simdram_destroy"); LOAD(error,"simdram_error");
#undef LOAD
 a->ctx=a->create(channels,bytes,ratio);
 if (!a->ctx) { snprintf(err,n,"DramSim construction failed"); dlclose(a->lib); free(a); return NULL; }
 return a;
}
static int dram_submit(dram_api* a,uint64_t id,uint64_t addr,int write) { return a->submit(a->ctx,id,addr,write); }
static int dram_tick(dram_api* a) { return a->tick(a->ctx); }
static int dram_poll(dram_api* a,uint64_t* id) { return a->poll(a->ctx,id); }
static const char* dram_error(dram_api* a) { return a->error(a->ctx); }
static void dram_close(dram_api* a) { a->destroy(a->ctx); dlclose(a->lib); free(a); }
*/
import "C"

import (
	"fmt"
	"math"
	"unsafe"
)

type Driver struct{ api *C.dram_api }

func Open(path string, channels, bytes uint32, ratio float32) (*Driver, error) {
	if path == "" || channels == 0 || bytes != 64 || math.IsNaN(float64(ratio)) || math.IsInf(float64(ratio), 0) || ratio < 0.001 {
		return nil, fmt.Errorf("invalid DramSim configuration (64-byte sector required)")
	}
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	var msg [1024]C.char
	a := C.dram_open(p, C.uint32_t(channels), C.uint32_t(bytes), C.float(ratio), &msg[0], C.size_t(len(msg)))
	if a == nil {
		return nil, fmt.Errorf("load RTLSim DramSim: %s", C.GoString(&msg[0]))
	}
	return &Driver{api: a}, nil
}
func (d *Driver) Submit(id uint64, address uint32, write bool) error {
	if d.api == nil {
		return fmt.Errorf("DramSim closed")
	}
	w := 0
	if write {
		w = 1
	}
	if C.dram_submit(d.api, C.uint64_t(id), C.uint64_t(address), C.int(w)) != 0 {
		return fmt.Errorf("DramSim submit: %s", C.GoString(C.dram_error(d.api)))
	}
	return nil
}
func (d *Driver) Tick() ([]uint64, error) {
	if d.api == nil {
		return nil, fmt.Errorf("DramSim closed")
	}
	if C.dram_tick(d.api) != 0 {
		return nil, fmt.Errorf("DramSim tick: %s", C.GoString(C.dram_error(d.api)))
	}
	var result []uint64
	for {
		var id C.uint64_t
		if C.dram_poll(d.api, &id) == 0 {
			break
		}
		result = append(result, uint64(id))
	}
	return result, nil
}
func (d *Driver) Close() error {
	if d.api != nil {
		C.dram_close(d.api)
		d.api = nil
	}
	return nil
}
