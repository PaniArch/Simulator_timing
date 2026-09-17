package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
)

// Check actual frozen instance connections, not merely the existence of a
// boundary name. Comments cannot satisfy a connection. The executable probe
// below independently checks the production System endpoints.
func validateDFlushContract(top map[string]*yaml.Node, read func(string) ([]byte, error)) error {
	b := records(top["boundaries"])["b-dflush"]
	if !exact(b["node"], "!!str", "n-dflush") || !exact(fields(b["rtl_parameters"])["OUT_BUF"], "!!int", "3") {
		return fmt.Errorf("b-dflush must bind n-dflush with OUT_BUF=3")
	}
	checks := map[string][]string{
		"core/VX_mem_unit.sv":       {"if (i == 0 && j == 0) begin : g_flush_port", "VX_dcr_flush #(.WORD_SIZE (DCACHE_WORD_SIZE), .TAG_WIDTH (DCACHE_CORE_TAG_WIDTH), .REQ_OUT_BUF (3)) dcr_flush", ".core_bus_if (dcache_bus_tmp_if[0])", ".cache_bus_if (dcache_bus_if[0])", "`ASSIGN_VX_MEM_BUS_IF_EX (dcache_bus_if[i * DCACHE_CHANNELS + j], dcache_bus_tmp_if[j], DCACHE_TAG_WIDTH_BASE, DCACHE_CORE_TAG_WIDTH, 0)"},
		"core/VX_dcr_flush.sv":      {"`ASSIGN_VX_MEM_BUS_IF (dcache_arb_in_if[0], core_bus_if)", "`ASSIGN_VX_MEM_BUS_IF (dcache_arb_in_if[1], flush_bus_if)", ".REQ_OUT_BUF (REQ_OUT_BUF)", ".ARBITER (\"P\")", ".STICKY (1)", ".bus_in_if (dcache_arb_in_if)", ".bus_out_if (dcache_arb_out_if)", "`ASSIGN_VX_MEM_BUS_IF (cache_bus_if, dcache_arb_out_if[0])"},
		"mem/VX_mem_bus_arb.sv":     {".OUT_BUF (REQ_OUT_BUF)", ".valid_in (req_valid_in)", ".valid_out (req_valid_out)"},
		"libs/VX_stream_arb.sv":     {".SIZE (`TO_OUT_BUF_SIZE(OUT_BUF))", ".OUT_REG (`TO_OUT_BUF_REG(OUT_BUF))", ".ready_in (ready_out_w[o])", ".data_out ({sel_out[o], data_out[o]})"},
		"VX_platform.vh":            {"`define TO_OUT_BUF_SIZE(s) `MIN(s & 7, 2)", "`define TO_OUT_BUF_REG(s) (((s & 7) < 2) ? (s & 7) : ((s & 7) - 2))"},
		"libs/VX_elastic_buffer.sv": {"if (SIZE == 2) begin : g_eb2", ".OUT_REG (OUT_REG == 1)", "VX_stream_buffer"},
		"libs/VX_stream_buffer.sv":  {"assign ready_in = valid_in_r", "assign valid_out = valid_out_r", "assign data_out = data_out_r", "valid_in_r <= flow_out", "data_out_r <= valid_in_r ? data_in : buffer_r"},
	}
	comments := regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`)
	space := regexp.MustCompile(`\s+`)
	for file, terms := range checks {
		data, err := read(filepath.Join("Vortex_rtl/hw/rtl", file))
		if err != nil {
			return err
		}
		source := space.ReplaceAllString(comments.ReplaceAllString(string(data), ""), "")
		for _, term := range terms {
			if !strings.Contains(source, space.ReplaceAllString(term, "")) {
				return fmt.Errorf("b-dflush RTL connection missing in %s: %s", file, term)
			}
		}
	}
	return nil
}

// A production wiring witness: four non-coalescing words in one bank must
// reach Cache on ports 1,0,1,0 on consecutive edges, while port 0's adapter
// handshake precedes its Cache handshake. Both reads and writes must traverse
// the boundary. This detects a bypass even with an unused buffer instance.
func probeDFlushSystem() error {
	for _, write := range []bool{false, true} {
		ram, err := memory.New(4096)
		if err != nil {
			return err
		}
		cfg, err := memsys.DefaultConfig()
		if err != nil {
			return err
		}
		s, err := memsys.NewSystem(ram, func(memsys.Identity) (warp.AtomicMemoryService, error) { return ram, nil }, cfg)
		if err != nil {
			return err
		}
		r := memsys.SIMDRequest{Identity: memsys.Identity{Kernel: 1, CTA: 1, WarpGeneration: 1, Transaction: 1}, Tag: 1, Mask: 15, Write: write}
		for l := range r.Lanes {
			r.Lanes[l] = memsys.LaneRequest{Address: uint32(l * 128), ByteEnable: 15, Data: [4]byte{byte(l + 1)}}
		}
		accepted, complete := false, false
		var ports, cycles, upstream []int
		for c := 0; c < 1500; c++ {
			in := memsys.SystemInput{MemoryReady: true}
			if c >= 100 && !accepted {
				in.Memory = memsys.SIMDOffer{Valid: true, Request: r}
			}
			e, err := s.Step(uint64(c), in)
			if err != nil {
				return err
			}
			accepted = accepted || e.MemoryAccepted
			if e.DataAdapterAccepted[0] {
				upstream = append(upstream, c)
			}
			for p, fire := range e.DataCacheAccepted {
				if fire {
					ports = append(ports, p)
					cycles = append(cycles, c)
				}
			}
			if len(e.Complete) > 0 {
				if complete || len(e.Complete) != 1 || e.Complete[0] != r.Identity {
					return fmt.Errorf("b-dflush duplicate/foreign completion")
				}
				complete = true
			}
			if complete && s.Drained() {
				break
			}
		}
		if !complete || !s.Drained() || len(ports) != 4 || len(upstream) != 2 {
			return fmt.Errorf("b-dflush production transport incomplete")
		}
		for i, p := range []int{1, 0, 1, 0} {
			if ports[i] != p || cycles[i] != cycles[0]+i {
				return fmt.Errorf("b-dflush production port phase: ports=%v cycles=%v", ports, cycles)
			}
		}
		for i, c := range upstream {
			if cycles[2*i+1] != c+1 {
				return fmt.Errorf("b-dflush missing registered production edge")
			}
		}
	}
	return nil
}

func readDFlushRoot(root string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(root, path)) }
}
