#!/usr/bin/env python3
"""Offline smoke of the built Go C ABI, without an external Vortex runtime.
Usage: python3 integration/vortex-runtime/test-mpm-abi.py /path/libsimtiming.so
"""
import ctypes as c
import os
import sys
import time

os.environ["SIMTIMING_MODE"] = "timing"
lib = c.CDLL(sys.argv[1])
lib.simtiming_create.restype = c.c_uint64
for name in ("destroy", "start", "busy"):
    getattr(lib, "simtiming_" + name).argtypes = [c.c_uint64]
lib.simtiming_dcr_write.argtypes = [c.c_uint64, c.c_uint32, c.c_uint32]
lib.simtiming_dcr_read.argtypes = [c.c_uint64, c.c_uint32, c.c_uint32, c.POINTER(c.c_uint32)]
lib.simtiming_mem_write.argtypes = [c.c_uint64, c.c_uint64, c.c_void_p, c.c_size_t]
device = lib.simtiming_create()
assert device


def read(address, tag=0):
    value = c.c_uint32(0xDEADBEEF)
    assert lib.simtiming_dcr_read(device, address, tag, c.byref(value)) == 0
    return value.value


assert read(1, 2 << 16) == 0
unknown = c.c_uint32(0xDEADBEEF)
assert lib.simtiming_dcr_read(device, 1, (1 << 22) | (3 << 16), c.byref(unknown)) == -1
assert unknown.value == 0xDEADBEEF  # an unavailable counter is not a valid zero
writes = {0x10: 0x100, 0x12: 0x100, 0x1D: 1}
for address in (0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x21, 0x22, 0x23):
    writes[address] = 1
for address, value in writes.items():
    assert lib.simtiming_dcr_write(device, address, value) == 0
program = (c.c_uint32 * 3)(0x40000093, 0x0820918B, 0xB)
assert lib.simtiming_mem_write(device, 0x100, program, c.sizeof(program)) == 0
payload = c.c_uint32(0x01020304)
assert lib.simtiming_mem_write(device, 0x400, c.byref(payload), 4) == 0
previous = 0
for launch in (1, 2):
    assert lib.simtiming_start(device) == 0
    deadline = time.monotonic() + 180
    while lib.simtiming_busy(device):
        assert time.monotonic() < deadline, "kernel did not become idle"
        time.sleep(0.001)
    assert read(1, 2 << 16) == launch * 6
    cycles = read(1)
    assert cycles > previous
    assert read(1, 32 << 16) == 0
    assert read(1) == cycles
    assert read(0) == 0  # real D/I flush
    previous = read(1)
    assert cycles <= previous <= cycles + 1
assert lib.simtiming_destroy(device) == 0
print("MPM C ABI: PASS (two launches, packed EOP, flush, unsupported counter)")
