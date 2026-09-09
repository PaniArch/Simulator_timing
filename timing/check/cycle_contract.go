package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// These are reviewed event endpoints, not simulator latency defaults. Requiring
// the complete set prevents a deleted rule from silently weakening the gate.
var cycleEndpoints = map[string]int{
	"cc-fetch-eligibility": 0, "cc-frontend-context": 0,
	"cc-ibuffer-accounting": 1, "cc-decode-unlock": 1,
	"cc-reserve-release": 1, "cc-special-dependencies": 1,
	"cc-fu-credit-lock": 1, "cc-issue-arbitration": 0,
	"cc-pending": 1, "cc-packed-release": 1,
	"cc-commit-visibility": 1, "cc-control-visibility": 1,
	"cc-control-collisions": -1, "cc-software-context-flush": 0,
}

func records(n *yaml.Node) map[string]map[string]*yaml.Node {
	out := map[string]map[string]*yaml.Node{}
	if n != nil {
		for _, r := range n.Content {
			m := fields(r)
			if m["id"] != nil {
				out[m["id"].Value] = m
			}
		}
	}
	return out
}

func exact(n *yaml.Node, tag, value string) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == tag && n.Value == value
}

func validateCycleContracts(top map[string]*yaml.Node, root string) error {
	contracts := records(top["cycle_contracts"])
	sources := fields(top["sources"])
	for id, offset := range cycleEndpoints {
		m := contracts[id]
		if m == nil {
			return fmt.Errorf("missing cycle contract %s", id)
		}
		wantStatus, wantClass := "FROZEN", "rtl_fact"
		if id == "cc-control-collisions" {
			wantStatus, wantClass = "UNRESOLVED", "unresolved_pair"
		} else if id == "cc-software-context-flush" {
			wantStatus, wantClass = "PROVISIONAL", "software_interface"
		}
		if !exact(m["status"], "!!str", wantStatus) || !exact(m["classification"], "!!str", wantClass) {
			return fmt.Errorf("cycle contract %s: evidence classification mismatch", id)
		}
		if offset < 0 {
			if !exact(m["consumer_offset"], "!!null", "null") || !exact(m["unknown_ref"], "!!str", "u-feedback") {
				return fmt.Errorf("cycle contract %s: unresolved endpoint must stay null with u-feedback", id)
			}
		} else if !exact(m["consumer_offset"], "!!int", fmt.Sprint(offset)) {
			return fmt.Errorf("cycle contract %s: reviewed consumer_offset mismatch", id)
		}
	}
	for id, m := range contracts {
		if err := required(m, "node", "classification", "condition", "trigger", "sampled_state", "next_state", "consumer", "simultaneous", "rule_refs"); err != nil {
			return fmt.Errorf("cycle contract %s: %w", id, err)
		}
		for _, key := range []string{"condition", "trigger", "sampled_state", "next_state", "consumer", "simultaneous"} {
			if m[key].Kind != yaml.ScalarNode || m[key].Tag != "!!str" {
				return fmt.Errorf("cycle contract %s: %s must be text", id, key)
			}
		}
		switch m["classification"].Value {
		case "rtl_fact":
			if m["status"].Value != "FROZEN" {
				return fmt.Errorf("cycle contract %s: rtl_fact must be FROZEN", id)
			}
			anchors := m["rtl_anchors"]
			if anchors == nil || anchors.Kind != yaml.SequenceNode || len(anchors.Content) == 0 {
				return fmt.Errorf("cycle contract %s: missing rtl_anchors", id)
			}
			for _, a := range anchors.Content {
				parts := strings.SplitN(a.Value, "::", 2)
				if a.Kind != yaml.ScalarNode || a.Tag != "!!str" || len(parts) != 2 || strings.TrimSpace(parts[1]) == "" {
					return fmt.Errorf("cycle contract %s: invalid RTL anchor", id)
				}
				src := fields(sources[parts[0]])
				if src["path"] == nil || !strings.HasPrefix(src["path"].Value, "Vortex_rtl/") {
					return fmt.Errorf("cycle contract %s: RTL anchor source is not frozen RTL", id)
				}
				data, err := os.ReadFile(filepath.Join(root, src["path"].Value))
				if err != nil || !strings.Contains(string(data), parts[1]) {
					return fmt.Errorf("cycle contract %s: RTL anchor not found: %s", id, a.Value)
				}
			}
		case "software_interface":
			if m["status"].Value != "PROVISIONAL" {
				return fmt.Errorf("cycle contract %s: software interface must be PROVISIONAL", id)
			}
		case "unresolved_pair":
			if m["status"].Value != "UNRESOLVED" || !nonempty(m["unknown_ref"]) || !exact(m["consumer_offset"], "!!null", "null") {
				return fmt.Errorf("cycle contract %s: unresolved pair needs null endpoint and unknown_ref", id)
			}
		default:
			return fmt.Errorf("cycle contract %s: unknown classification", id)
		}
		if m["classification"].Value != "unresolved_pair" {
			var offset int
			if m["consumer_offset"] == nil || m["consumer_offset"].Tag != "!!int" || m["consumer_offset"].Decode(&offset) != nil || offset < 0 {
				return fmt.Errorf("cycle contract %s: invalid consumer_offset", id)
			}
		}
	}
	// Cross-check the four-warp profile against existing source-backed resources,
	// so aliases cannot accidentally become independent capacities or policies.
	configs, resources, boundaries := records(top["config"]), records(top["resources"]), records(top["boundaries"])
	checks := []struct {
		label, key, tag, value string
		m                      map[string]*yaml.Node
	}{
		{"baseline", "warps_per_core", "!!int", "4", fields(configs["cfg-baseline"]["values"])},
		{"baseline", "issue_width", "!!int", "1", fields(configs["cfg-baseline"]["values"])},
		{"fetch", "warps", "!!int", "4", fields(contracts["cc-fetch-eligibility"]["parameters"])},
		{"fetch", "issue_slices", "!!int", "1", fields(contracts["cc-fetch-eligibility"]["parameters"])},
		{"fetch", "fetch_priority", "!!str", "lowest_eligible_wid", fields(contracts["cc-fetch-eligibility"]["parameters"])},
		{"fetch", "full_policy", "!!str", "prefer_nonfull_else_all_ready", fields(contracts["cc-fetch-eligibility"]["parameters"])},
		{"IBuffer", "instances", "!!int", "4", resources["res-ibuffer"]},
		{"IBuffer", "capacity", "!!int", "4", resources["res-ibuffer"]},
		{"hazards", "instances", "!!int", "4", resources["res-hazards"]},
		{"hazards", "register_busy_bits", "!!int", "64", resources["res-hazards"]},
		{"hazards", "special_busy_bits", "!!int", "2", resources["res-hazards"]},
		{"credits", "capacity", "!!int", "4", resources["res-credits"]},
		{"pending", "instances", "!!int", "4", resources["res-warp-pending"]},
		{"pending", "capacity", "!!int", "256", resources["res-warp-pending"]},
		{"pending", "almost_empty_threshold", "!!int", "1", resources["res-warp-pending"]},
		{"reserve", "release_then_reserve", "!!bool", "true", fields(contracts["cc-reserve-release"]["parameters"])},
		{"reserve", "readiness_registered", "!!bool", "true", fields(contracts["cc-reserve-release"]["parameters"])},
		{"reserve", "reserve_boundary", "!!str", "staging_output_fire", fields(contracts["cc-reserve-release"]["parameters"])},
		{"reserve", "dependency_set", "!!str", "used_sources_plus_wb_rd_plus_rd_or_wr_xregs", fields(contracts["cc-reserve-release"]["parameters"])},
		{"packed", "release_scope", "!!str", "uop_eop", fields(contracts["cc-packed-release"]["parameters"])},
		{"packed", "partial_wb_releases", "!!bool", "false", fields(contracts["cc-packed-release"]["parameters"])},
		{"packed", "packed_fu_lock", "!!int", "1", fields(contracts["cc-packed-release"]["parameters"])},
		{"packed", "packed_fu_unlock", "!!int", "1", fields(contracts["cc-packed-release"]["parameters"])},
	}
	for _, c := range checks {
		if !exact(c.m[c.key], c.tag, c.value) {
			return fmt.Errorf("multi-warp profile mismatch: %s.%s", c.label, c.key)
		}
	}
	for _, id := range []string{"b-schedule", "b-scoreboard-out", "b-commit"} {
		a := fields(boundaries[id]["arbitration"])
		policy, sticky := "P", "0"
		if id == "b-scoreboard-out" {
			policy, sticky = "R", "1"
		}
		if !exact(a["inputs"], "!!int", "4") || !exact(a["policy"], "!!str", policy) || !exact(a["sticky"], "!!int", sticky) || !exact(a["model"], "!!int", "1") {
			return fmt.Errorf("multi-warp arbitration mismatch: %s", id)
		}
	}
	return nil
}
