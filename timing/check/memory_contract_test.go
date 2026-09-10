package main

import (
	"go.yaml.in/yaml/v3"
	"os"
	"strings"
	"testing"
)

func TestMemoryContractMutations(t *testing.T) {
	data, err := os.ReadFile("../../timing/ir.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, id, section, key, value, want string }{
		{"software-is-not-rtl", "mc-backend", "", "status", "FROZEN", "origin/status"},
		{"unresolved-is-not-frozen", "mc-unresolved", "", "origin", "rtl", "origin/status"},
		{"swapped-granularity", "mc-cache", "geometry", "lane_bytes_ref", "cfg-memory.line_and_sector_bytes", "wrong memory granularity"},
		{"parameter-reference", "mc-cache", "geometry", "lane_bytes_ref", "cfg-memory.absent", "invalid memory parameter"},
		{"missing-mask-contract", "mc-interface", "", "response_fields", "", "missing/empty response_fields"},
		{"zero-backend-capacity", "mc-backend", "parameters", "max_inflight", "0", "must be positive"},
		{"negative-latency", "mc-backend", "parameters", "latency_cycles", "-1", "invalid memory integer"},
		{"missing-pending-predicate", "mc-control", "predicates", "hardware_pending", "", "missing/empty hardware_pending"},
		{"unknown-evidence", "mc-cache", "", "evidence", "absent::claim", "invalid evidence"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			top := fields(doc.Content[0])
			for _, n := range top["memory_contracts"].Content {
				m := fields(n)
				if m["id"].Value != tc.id {
					continue
				}
				if tc.section != "" {
					m = fields(m[tc.section])
				}
				v := m[tc.key]
				if tc.key == "evidence" {
					v.Content[0].Value = tc.value
				} else {
					v.Value = tc.value
					v.Content = nil
				}
			}
			bad, err := yaml.Marshal(&doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := validate(bad, "../.."); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
	t.Run("derived-queue-depth", func(t *testing.T) {
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		top := fields(doc.Content[0])
		for _, n := range top["boundaries"].Content {
			m := fields(n)
			if m["id"].Value == "b-dcache-bank-mreq" {
				fields(m["rtl_parameters"])["DEPTH"].Value = "4"
			}
		}
		bad, err := yaml.Marshal(&doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := validate(bad, "../.."); err == nil || !strings.Contains(err.Error(), "memory queue derivation") {
			t.Fatalf("accepted inconsistent queue: %v", err)
		}
	})
}
