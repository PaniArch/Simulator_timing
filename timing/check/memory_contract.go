package main

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Validate the executable schema of the memory contract, while retaining the
// original config/resources/boundaries as the only RTL parameter table.
func validateMemoryContracts(top map[string]*yaml.Node) error {
	lookup := func(collection, id string) map[string]*yaml.Node {
		for _, n := range top[collection].Content {
			if fields(n)["id"].Value == id {
				return fields(n)
			}
		}
		return nil
	}
	specs := map[string][]string{
		"mc-cache":      {"config_ref", "resource_refs", "boundary_refs", "geometry", "queues", "ordering"},
		"mc-coalescer":  {"config_ref", "resource_refs", "grouping", "bytes", "acceptance", "response", "backpressure", "empty"},
		"mc-local":      {"config_ref", "resource_refs", "routing", "banking", "timing", "adapter", "stores"},
		"mc-interface":  {"acceptance", "request_fields", "response_fields", "identity", "load_completion", "store_completion", "lifecycle", "ownership"},
		"mc-control":    {"predicates", "fence", "flush"},
		"mc-backend":    {"parameters", "timing", "visibility", "scope"},
		"mc-unresolved": {"unknown_ref", "gaps"},
	}
	for id, keys := range specs {
		m := lookup("memory_contracts", id)
		if m == nil {
			return fmt.Errorf("missing memory contract %s", id)
		}
		if err := required(m, append(keys, "origin")...); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		origin, status := "rtl", "FROZEN"
		if id == "mc-interface" || id == "mc-backend" {
			origin, status = "software_contract", "PROVISIONAL"
		}
		if id == "mc-unresolved" {
			origin, status = "unresolved", "UNRESOLVED"
		}
		if m["origin"].Value != origin || m["status"].Value != status {
			return fmt.Errorf("%s: memory origin/status mismatch", id)
		}
	}
	for _, n := range top["memory_contracts"].Content {
		if _, ok := specs[fields(n)["id"].Value]; !ok {
			return fmt.Errorf("unknown memory contract")
		}
	}
	nested := map[string]map[string][]string{
		"mc-cache": {
			"geometry": {"lane_bytes_ref", "coalesced_bytes_ref", "refill_bytes_ref", "bank_select", "set_select", "sets_per_bank", "service"},
			"queues":   {"mshr_scope", "mreq_depth", "crsq_depth", "mrsq_scope", "admission"},
			"ordering": {"input_priority", "replacement", "miss", "store", "visibility"},
		},
		"mc-control": {"predicates": {"hardware_pending", "lsu_scheduler_drained", "core_busy", "mem_unit_empty", "bank_empty", "software_complete", "backing_visible"}},
	}
	for id, sections := range nested {
		for section, keys := range sections {
			if err := required(fields(lookup("memory_contracts", id)[section]), keys...); err != nil {
				return fmt.Errorf("%s.%s: %w", id, section, err)
			}
		}
	}
	geometry := fields(lookup("memory_contracts", "mc-cache")["geometry"])
	expectedRefs := map[string]string{"lane_bytes_ref": "cfg-memory.lsu_word_bytes", "coalesced_bytes_ref": "cfg-memory.dcache_word_bytes", "refill_bytes_ref": "cfg-memory.line_and_sector_bytes"}
	for _, k := range []string{"lane_bytes_ref", "coalesced_bytes_ref", "refill_bytes_ref"} {
		parts := strings.Split(geometry[k].Value, ".")
		if len(parts) != 2 || lookup("config", parts[0]) == nil {
			return fmt.Errorf("dangling memory parameter %s", k)
		}
		value := fields(lookup("config", parts[0])["values"])[parts[1]]
		var v int
		if value == nil || value.Tag != "!!int" || value.Decode(&v) != nil || v <= 0 {
			return fmt.Errorf("invalid memory parameter %s", k)
		}
		if geometry[k].Value != expectedRefs[k] {
			return fmt.Errorf("wrong memory granularity reference %s", k)
		}
	}
	integer := func(m map[string]*yaml.Node, k string) (int, error) {
		var v int
		if m[k] == nil || m[k].Tag != "!!int" || m[k].Decode(&v) != nil || v < 0 {
			return 0, fmt.Errorf("invalid memory integer %s", k)
		}
		return v, nil
	}
	backend := fields(lookup("memory_contracts", "mc-backend")["parameters"])
	for _, k := range []string{"latency_cycles", "accepts_per_cycle", "max_inflight", "returns_per_cycle"} {
		v, err := integer(backend, k)
		if err != nil {
			return err
		}
		if v == 0 {
			return fmt.Errorf("memory backend %s must be positive", k)
		}
	}
	pow2 := func(v int) int {
		n := 1
		for n < v {
			n *= 2
		}
		return n
	}
	cfg := fields(lookup("config", "cfg-memory")["values"])
	for _, cache := range []string{"icache", "dcache"} {
		res := fields(lookup("resources", "res-"+cache)["rtl_parameters"])
		values := map[string]int{}
		for _, k := range []string{"LATENCY", "MSHR_SIZE", "MREQ_SIZE", "CRSQ_SIZE", "MRSQ_SIZE"} {
			v, err := integer(res, k)
			if err != nil {
				return err
			}
			values[k] = v
		}
		if values["LATENCY"] < 2 || values["MSHR_SIZE"] < 1 {
			return fmt.Errorf("invalid cache pipeline/MSHR")
		}
		floor := 2 * values["LATENCY"]
		wb := 0
		if cache == "dcache" {
			var err error
			wb, err = integer(cfg, "dcache_writeback")
			if err != nil {
				return err
			}
		}
		if wb != 0 && floor < values["MSHR_SIZE"] {
			floor = values["MSHR_SIZE"]
		}
		for _, q := range []struct {
			name, field string
			want        int
		}{
			{"bank-mreq", "DEPTH", pow2(floor + values["MREQ_SIZE"])},
			{"bank-crsq", "SIZE", pow2(2 + values["CRSQ_SIZE"])},
			{"memory-response-queue", "SIZE", values["MRSQ_SIZE"]},
		} {
			boundary := lookup("boundaries", "b-"+cache+"-"+q.name)
			got, err := integer(fields(boundary["rtl_parameters"]), q.field)
			if err != nil {
				return err
			}
			if got != q.want {
				return fmt.Errorf("%s %s memory queue derivation: got %d want %d", cache, q.name, got, q.want)
			}
		}
	}
	return nil
}
