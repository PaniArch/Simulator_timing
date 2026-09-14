package main

/*
#include <stddef.h>
#include <stdint.h>
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"

	"vortex.local/simulator/integration/vortexruntime"
)

var devices = struct {
	sync.RWMutex
	next uint64
	byID map[uint64]*vortexruntime.Device
}{next: 1, byID: make(map[uint64]*vortexruntime.Device)}

func lookup(id C.uint64_t) *vortexruntime.Device {
	devices.RLock()
	defer devices.RUnlock()
	return devices.byID[uint64(id)]
}

//export simtiming_create
func simtiming_create() C.uint64_t {
	owner, err := vortexruntime.NewDevice()
	if err != nil {
		return 0
	}
	devices.Lock()
	id := devices.next
	devices.next++
	devices.byID[id] = owner
	devices.Unlock()
	return C.uint64_t(id)
}

//export simtiming_destroy
func simtiming_destroy(id C.uint64_t) C.int {
	devices.Lock()
	owner := devices.byID[uint64(id)]
	defer devices.Unlock()
	if owner == nil {
		return -1
	}
	if err := owner.Close(); err != nil {
		return -1
	}
	delete(devices.byID, uint64(id))
	return 0
}

func bytesAt(pointer unsafe.Pointer, size C.size_t) ([]byte, error) {
	if uint64(size) > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("C buffer length %d exceeds Go int", uint64(size))
	}
	if size == 0 {
		return nil, nil
	}
	if pointer == nil {
		return nil, fmt.Errorf("nil C buffer with length %d", uint64(size))
	}
	return unsafe.Slice((*byte)(pointer), int(size)), nil
}

//export simtiming_mem_read
func simtiming_mem_read(id C.uint64_t, address C.uint64_t, destination unsafe.Pointer, size C.size_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return -1
	}
	if uint64(address) > uint64(^uint32(0)) {
		owner.RecordConnectionError("memory-read", fmt.Errorf("address %#x exceeds RV32", uint64(address)))
		return -1
	}
	data, err := bytesAt(destination, size)
	if err == nil {
		err = owner.Memory().Read(uint32(address), data)
	}
	if err != nil {
		owner.RecordConnectionError("memory-read", err)
		return -1
	}
	return 0
}

//export simtiming_mem_write
func simtiming_mem_write(id C.uint64_t, address C.uint64_t, source unsafe.Pointer, size C.size_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return -1
	}
	if uint64(address) > uint64(^uint32(0)) {
		owner.RecordConnectionError("memory-write", fmt.Errorf("address %#x exceeds RV32", uint64(address)))
		return -1
	}
	data, err := bytesAt(source, size)
	if err == nil {
		err = owner.Memory().Write(uint32(address), data)
	}
	if err != nil {
		owner.RecordConnectionError("memory-write", err)
		return -1
	}
	return 0
}

//export simtiming_dcr_write
func simtiming_dcr_write(id C.uint64_t, address, value C.uint32_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return -1
	}
	if err := owner.WriteDCR(uint32(address), uint32(value)); err != nil {
		return -1
	}
	return 0
}

//export simtiming_dcr_read
func simtiming_dcr_read(id C.uint64_t, address, tag C.uint32_t, output *C.uint32_t) C.int {
	owner := lookup(id)
	if owner == nil || output == nil {
		return -1
	}
	value, err := owner.ReadDCR(uint32(address), uint32(tag))
	if err != nil {
		return -1
	}
	*output = C.uint32_t(value)
	return 0
}

//export simtiming_start
func simtiming_start(id C.uint64_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return -1
	}
	if err := owner.Start(); err != nil {
		return -1
	}
	return 0
}

//export simtiming_busy
func simtiming_busy(id C.uint64_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return 0
	}
	if owner.Busy() {
		return 1
	}
	return 0
}

//export simtiming_error
func simtiming_error(id C.uint64_t, output *C.char, capacity C.size_t) C.int {
	owner := lookup(id)
	if owner == nil {
		return -1
	}
	message := owner.LastError()
	if message == "" {
		if output != nil && capacity != 0 {
			*output = 0
		}
		return 0
	}
	if output != nil && capacity != 0 {
		buffer := unsafe.Slice((*byte)(unsafe.Pointer(output)), int(capacity))
		count := min(len(message), len(buffer)-1)
		copy(buffer, message[:count])
		buffer[count] = 0
	}
	return 1
}

func main() {}
