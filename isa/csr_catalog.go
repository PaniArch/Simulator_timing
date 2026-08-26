package isa

import "fmt"

const (
	FrozenWarpCount    = 4
	FrozenCoreCount    = 1
	FrozenBarrierCount = 8
	FrozenLocalMemBase = uint32(0xffff0000)
	FrozenCounterBits  = 44
	// RV32 MXL plus the frozen F/I/M/U/X standard-extension bits.
	FrozenMISA = uint32(0x40901120)
)

type CSRValueKind uint8

const (
	csrValueZero CSRValueKind = iota
	csrValueFFlags
	csrValueFRM
	csrValueFCSR
	csrValueMStatus
	csrValueMTVec
	csrValueMScratch
	csrValueMEPC
	csrValueMCause
	csrValueMTVal
	csrValueMISA
	csrValueThreadID
	csrValueHartID
	csrValueWarpID
	csrValueCoreID
	csrValueActiveWarps
	csrValueActiveThreads
	csrValueNumThreads
	csrValueNumWarps
	csrValueNumCores
	csrValueLocalMemBase
	csrValueNumBarriers
	csrValueCTAID
	csrValueCTARank
	csrValueCTASize
	csrValueCTAThreadX
	csrValueCTAThreadY
	csrValueCTAThreadZ
	csrValueCTABlockX
	csrValueCTABlockY
	csrValueCTABlockZ
	csrValueCTABlockDimX
	csrValueCTABlockDimY
	csrValueCTABlockDimZ
	csrValueCTAGridDimX
	csrValueCTAGridDimY
	csrValueCTAGridDimZ
	csrValueCTALMemAddress
	csrValueCTAClusterSize
	csrValueCTAEntry
	csrValueCycleLow
	csrValueCycleHigh
	csrValueInstretLow
	csrValueInstretHigh
)

// CSREntry is one strict frozen CSR address. Writable means a write reaches
// canonical storage; WriteIgnored means the RTL accepts the write address but
// this configuration implements it as a constant zero without storage.
type CSREntry struct {
	Name         string
	Address      uint16
	Scope        CSRScope
	Value        CSRValueKind
	Writable     bool
	WriteIgnored bool
	WriteMask    uint32
	RTLEvidence  []string
}

var (
	csrDataEvidence  = []string{"hw/VX_types.vh:348-429", "hw/rtl/core/VX_csr_data.sv:76-263"}
	csrLaneEvidence  = []string{"hw/rtl/core/VX_csr_unit.sv:121-187", "hw/rtl/core/VX_csr_data.sv:210-234"}
	csrTrapEvidence  = []string{"hw/rtl/core/VX_csr_data.sv:76-120,254-258", "hw/rtl/core/VX_scheduler.sv:245-255,358-381"}
	csrCountEvidence = []string{"hw/rtl/core/VX_csr_data.sv:21-27,236-263", "hw/rtl/core/VX_scheduler.sv:132,541-588"}
)

func csrEntry(name string, address uint16, scope CSRScope, value CSRValueKind, evidence []string) CSREntry {
	return CSREntry{Name: name, Address: address, Scope: scope, Value: value, RTLEvidence: evidence}
}

func csrRW(name string, address uint16, value CSRValueKind, mask uint32, evidence []string) CSREntry {
	e := csrEntry(name, address, CSRScopeWarp, value, evidence)
	e.Writable, e.WriteMask = true, mask
	return e
}

func csrZero(name string, address uint16) CSREntry {
	e := csrEntry(name, address, CSRScopeConstant, csrValueZero, csrDataEvidence)
	e.WriteIgnored = true
	return e
}

func buildCSRCatalog() []CSREntry {
	entries := []CSREntry{
		csrRW("fflags", 0x001, csrValueFFlags, 0x1f, csrDataEvidence),
		csrRW("frm", 0x002, csrValueFRM, 0x07, csrDataEvidence),
		csrRW("fcsr", 0x003, csrValueFCSR, 0xff, csrDataEvidence),
		csrZero("satp-disabled", 0x180),
		csrRW("mstatus", 0x300, csrValueMStatus, 0xffffffff, csrTrapEvidence),
		csrEntry("misa", 0x301, CSRScopeConstant, csrValueMISA, csrDataEvidence),
		csrZero("medeleg", 0x302),
		csrZero("mideleg", 0x303),
		csrZero("mie", 0x304),
		csrRW("mtvec", 0x305, csrValueMTVec, 0xffffffff, csrTrapEvidence),
		csrRW("mscratch", 0x340, csrValueMScratch, 0xffffffff, csrTrapEvidence),
		csrRW("mepc", 0x341, csrValueMEPC, 0xffffffff, csrTrapEvidence),
		csrRW("mcause", 0x342, csrValueMCause, 0xffffffff, csrTrapEvidence),
		csrRW("mtval", 0x343, csrValueMTVal, 0xffffffff, csrTrapEvidence),
		csrZero("pmpcfg0", 0x3a0),
		csrZero("pmpaddr0", 0x3b0),
		csrZero("mnstatus", 0x744),

		csrEntry("thread_id", 0xcc0, CSRScopeLane, csrValueThreadID, csrLaneEvidence),
		csrEntry("warp_id", 0xcc1, CSRScopeWarp, csrValueWarpID, csrLaneEvidence),
		csrEntry("core_id", 0xcc2, CSRScopeCore, csrValueCoreID, csrLaneEvidence),
		csrEntry("active_warps", 0xcc3, CSRScopeCore, csrValueActiveWarps, csrLaneEvidence),
		csrEntry("active_threads", 0xcc4, CSRScopeWarp, csrValueActiveThreads, csrLaneEvidence),

		csrEntry("cta_id", 0xcd0, CSRScopeCTA, csrValueCTAID, csrLaneEvidence),
		csrEntry("cta_rank", 0xcd1, CSRScopeCTA, csrValueCTARank, csrLaneEvidence),
		csrEntry("cta_size", 0xcd2, CSRScopeCTA, csrValueCTASize, csrLaneEvidence),
		csrEntry("cta_thread_id_x", 0xcd3, CSRScopeLane, csrValueCTAThreadX, csrLaneEvidence),
		csrEntry("cta_thread_id_y", 0xcd4, CSRScopeLane, csrValueCTAThreadY, csrLaneEvidence),
		csrEntry("cta_thread_id_z", 0xcd5, CSRScopeLane, csrValueCTAThreadZ, csrLaneEvidence),
		csrEntry("cta_block_id_x", 0xcd6, CSRScopeCTA, csrValueCTABlockX, csrLaneEvidence),
		csrEntry("cta_block_id_y", 0xcd7, CSRScopeCTA, csrValueCTABlockY, csrLaneEvidence),
		csrEntry("cta_block_id_z", 0xcd8, CSRScopeCTA, csrValueCTABlockZ, csrLaneEvidence),
		csrEntry("cta_block_dim_x", 0xcd9, CSRScopeCTA, csrValueCTABlockDimX, csrLaneEvidence),
		csrEntry("cta_block_dim_y", 0xcda, CSRScopeCTA, csrValueCTABlockDimY, csrLaneEvidence),
		csrEntry("cta_block_dim_z", 0xcdb, CSRScopeCTA, csrValueCTABlockDimZ, csrLaneEvidence),
		csrEntry("cta_grid_dim_x", 0xcdc, CSRScopeCTA, csrValueCTAGridDimX, csrLaneEvidence),
		csrEntry("cta_grid_dim_y", 0xcdd, CSRScopeCTA, csrValueCTAGridDimY, csrLaneEvidence),
		csrEntry("cta_grid_dim_z", 0xcde, CSRScopeCTA, csrValueCTAGridDimZ, csrLaneEvidence),
		csrEntry("cta_lmem_addr", 0xcdf, CSRScopeCTA, csrValueCTALMemAddress, csrLaneEvidence),
		csrEntry("cta_cluster_size", 0xce0, CSRScopeCTA, csrValueCTAClusterSize, csrLaneEvidence),
		csrEntry("cta_entry", 0xce1, CSRScopeCTA, csrValueCTAEntry, csrLaneEvidence),

		csrEntry("num_threads", 0xfc0, CSRScopeConstant, csrValueNumThreads, csrDataEvidence),
		csrEntry("num_warps", 0xfc1, CSRScopeConstant, csrValueNumWarps, csrDataEvidence),
		csrEntry("num_cores", 0xfc2, CSRScopeConstant, csrValueNumCores, csrDataEvidence),
		csrEntry("local_mem_base", 0xfc3, CSRScopeConstant, csrValueLocalMemBase, csrDataEvidence),
		csrEntry("num_barriers", 0xfc4, CSRScopeConstant, csrValueNumBarriers, csrDataEvidence),

		csrEntry("mvendorid", 0xf11, CSRScopeConstant, csrValueZero, csrDataEvidence),
		csrEntry("marchid", 0xf12, CSRScopeConstant, csrValueZero, csrDataEvidence),
		csrEntry("mimpid", 0xf13, CSRScopeConstant, csrValueZero, csrDataEvidence),
		csrEntry("mhartid", 0xf14, CSRScopeLane, csrValueHartID, csrLaneEvidence),

		csrEntry("mcycle", 0xb00, CSRScopeCounter, csrValueCycleLow, csrCountEvidence),
		csrEntry("mcycleh", 0xb80, CSRScopeCounter, csrValueCycleHigh, csrCountEvidence),
		csrEntry("minstret", 0xb02, CSRScopeCounter, csrValueInstretLow, csrCountEvidence),
		csrEntry("minstreth", 0xb82, CSRScopeCounter, csrValueInstretHigh, csrCountEvidence),
	}
	// Instruction-originated CSR requests force mpm_class=BASE(0). The user
	// MPM windows are therefore legal reads but deterministically return zero,
	// independent of whether PERF_ENABLE exists in another RTL build.
	for slot := uint16(0); slot < 32; slot++ {
		entries = append(entries,
			csrEntry(fmt.Sprintf("mpm_zero_%02d", slot), 0xb03+slot, CSRScopeCounter, csrValueZero, csrCountEvidence),
			csrEntry(fmt.Sprintf("mpm_zero_%02d_h", slot), 0xb83+slot, CSRScopeCounter, csrValueZero, csrCountEvidence),
		)
	}
	return entries
}

// csrCatalog is the single authority for strict CSR address legality.
var csrCatalog = buildCSRCatalog()

func CSRCatalog() []CSREntry {
	result := make([]CSREntry, len(csrCatalog))
	copy(result, csrCatalog)
	for i := range result {
		result[i].RTLEvidence = append([]string(nil), csrCatalog[i].RTLEvidence...)
	}
	return result
}

func findCSR(address uint16) (CSREntry, bool) {
	for _, entry := range csrCatalog {
		if entry.Address == address {
			return entry, true
		}
	}
	return CSREntry{}, false
}
