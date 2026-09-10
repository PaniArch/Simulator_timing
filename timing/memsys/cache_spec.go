package memsys

import (
	"fmt"

	"vortex.local/simulator/timing"
)

type CacheKind string

const (
	InstructionCache CacheKind = "icache"
	DataCache        CacheKind = "dcache"
)

// CacheSpec is an evaluated view of the frozen IR, never a second parameter
// table. Sets means sets per bank; MSHR and bank queues are also per bank.
type CacheSpec struct {
	Kind                                                                                 CacheKind
	Bytes, Banks, Ways, Sets, WordBytes, LineBytes, CorePorts, MemoryPorts               int
	Latency, MSHR                                                                        int
	Writeback                                                                            bool
	CoreResponse, MemoryRequest, MemoryResponse                                          timing.BufferSpec
	OuterCoreResponse, OuterMemoryRequest, RefillCrossbar                                timing.BufferSpec
	CoreRequest                                                                          timing.BufferSpec
	RequestArbitration, ResponseArbitration, NCRequestArbitration, NCResponseArbitration timing.ArbiterSpec
}

func FrozenCacheSpec(kind CacheKind) (CacheSpec, error) {
	s := CacheSpec{Kind: kind}
	if kind != InstructionCache && kind != DataCache {
		return s, fmt.Errorf("unknown cache kind %q", kind)
	}
	var failure error
	number := func(collection, id string, path ...string) int {
		if failure != nil {
			return 0
		}
		v, err := timing.Number(collection, id, path...)
		if err != nil {
			failure = err
		}
		return v
	}
	config := func(field string) int { return number("config", "cfg-memory", "values", field) }
	prefix := string(kind)
	s.Bytes = config(prefix + "_size_bytes")
	s.Banks = config(prefix + "_banks")
	s.Ways = config(prefix + "_ways")
	s.WordBytes = config(prefix + "_word_bytes")
	s.LineBytes = config("line_and_sector_bytes")
	s.CorePorts = config(prefix + "_core_ports")
	s.MemoryPorts = config(prefix + "_memory_ports")
	s.Latency = number("resources", "res-"+prefix, "rtl_parameters", "LATENCY")
	s.MSHR = number("resources", "res-"+prefix, "rtl_parameters", "MSHR_SIZE")
	if kind == DataCache {
		s.Writeback = config("dcache_writeback") != 0
	}
	queue := func(suffix, field string) timing.BufferSpec {
		id := "b-" + prefix + "-" + suffix
		return timing.BufferSpec{ID: id, Size: number("boundaries", id, "rtl_parameters", field), OutReg: number("boundaries", id, "rtl_parameters", "OUT_REG")}
	}
	s.CoreResponse = queue("bank-crsq", "SIZE")
	s.MemoryRequest = queue("bank-mreq", "DEPTH")
	s.MemoryResponse = queue("memory-response-queue", "SIZE")
	encoded := func(suffix string) timing.BufferSpec {
		if failure != nil {
			return timing.BufferSpec{}
		}
		v, err := timing.Buffer("b-" + prefix + "-" + suffix)
		if err != nil {
			failure = err
		}
		return v
	}
	s.CoreRequest = encoded("core-request-bank-xbar")
	s.OuterCoreResponse = encoded("outer-core-response")
	s.OuterMemoryRequest = encoded("outer-memory-request")
	s.RefillCrossbar = encoded("memory-response-xbar")
	arbitration := func(id string) timing.ArbiterSpec {
		if failure != nil {
			return timing.ArbiterSpec{}
		}
		a, err := timing.Arbiter(id)
		if err != nil {
			failure = err
		}
		return a
	}
	s.RequestArbitration = arbitration("b-" + prefix + "-core-request-bank-xbar")
	s.ResponseArbitration = arbitration("b-" + prefix + "-outer-core-response")
	if kind == DataCache {
		s.NCRequestArbitration = arbitration("b-dcache-nc-memory-request")
		s.NCResponseArbitration = arbitration("b-dcache-nc-response")
	}
	if failure != nil {
		return s, failure
	}
	if s.LineBytes != SectorBytes || s.Bytes <= 0 || s.Banks <= 0 || s.Ways <= 0 || s.Bytes%(s.Banks*s.Ways*s.LineBytes) != 0 {
		return s, fmt.Errorf("unsupported cache geometry")
	}
	s.Sets = s.Bytes / (s.Banks * s.Ways * s.LineBytes)
	if s.Latency != 2 || s.MSHR <= 0 || s.CorePorts != s.Banks || s.MemoryPorts != s.Banks || (s.WordBytes != 4 && s.WordBytes != 8) || config("replacement_enum_fifo") != 1 {
		return s, fmt.Errorf("unsupported frozen cache pipeline/topology")
	}
	if failure != nil {
		return s, failure
	}
	if s.CoreRequest.Size != 0 || s.RequestArbitration.Inputs != s.CorePorts || s.ResponseArbitration.Inputs != s.Banks {
		return s, fmt.Errorf("unsupported cache request boundary")
	}
	profiles := []timing.ArbiterSpec{s.RequestArbitration, s.ResponseArbitration}
	if kind == DataCache {
		if s.NCRequestArbitration.Inputs != 2 || s.NCResponseArbitration.Inputs != 2 {
			return s, fmt.Errorf("unsupported cache bypass arbitration")
		}
		profiles = append(profiles, s.NCRequestArbitration, s.NCResponseArbitration)
	}
	for _, a := range profiles {
		if a.Policy != "R" || a.Sticky {
			return s, fmt.Errorf("unsupported cache arbitration policy")
		}
	}
	return s, nil
}

// DecodeAddress consumes a byte address. Bank interleaving is at line granularity,
// unlike the word granularity used by coalescing and LMEM banking.
func (s CacheSpec) DecodeAddress(address uint32) (bank, set, word int, tag uint32) {
	line := uint64(address) / uint64(s.LineBytes)
	bank = int(line % uint64(s.Banks))
	set = int(line / uint64(s.Banks) % uint64(s.Sets))
	word = int(uint64(address)%uint64(s.LineBytes)) / s.WordBytes
	tag = uint32(line / uint64(s.Banks*s.Sets))
	return
}
func (s CacheSpec) lineAddress(bank, set int, tag uint32) uint32 {
	return uint32(((uint64(tag)*uint64(s.Sets)+uint64(set))*uint64(s.Banks) + uint64(bank)) * uint64(s.LineBytes))
}
