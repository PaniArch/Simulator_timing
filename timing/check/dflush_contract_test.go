package main

import (
	"go.yaml.in/yaml/v3"
	"os"
	"strings"
	"testing"
)

func TestDFlushProductionWitness(t *testing.T) {
	if err := probeDFlushSystem(); err != nil {
		t.Fatal(err)
	}
}

func TestDFlushContractMutations(t *testing.T) {
	data, err := os.ReadFile("../ir.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, file, from, to string }{
		{"wrong-port", "core/VX_mem_unit.sv", ".cache_bus_if (dcache_bus_if[0])", ".cache_bus_if (dcache_bus_if[1])"},
		{"no-register", "core/VX_mem_unit.sv", ".REQ_OUT_BUF (3)", ".REQ_OUT_BUF (0)"},
		{"core-bypasses-arb", "core/VX_dcr_flush.sv", "`ASSIGN_VX_MEM_BUS_IF (dcache_arb_in_if[0], core_bus_if);", "// `ASSIGN_VX_MEM_BUS_IF (dcache_arb_in_if[0], core_bus_if);"},
		{"combinational-ready", "libs/VX_stream_buffer.sv", "assign ready_in  = valid_in_r;", "assign ready_in = ready_out;"},
		{"ir-bypass", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			top := fields(doc.Content[0])
			if tc.file == "" {
				fields(records(top["boundaries"])["b-dflush"]["rtl_parameters"])["OUT_BUF"].Value = "0"
			}
			read := func(path string) ([]byte, error) {
				b, err := readDFlushRoot("../..")(path)
				if strings.HasSuffix(path, tc.file) && tc.file != "" {
					if !strings.Contains(string(b), tc.from) {
						t.Fatal("mutation missed fixture")
					}
					b = []byte(strings.ReplaceAll(string(b), tc.from, tc.to))
				}
				return b, err
			}
			if err := validateDFlushContract(top, read); err == nil {
				t.Fatal("accepted broken b-dflush binding")
			}
		})
	}
}
