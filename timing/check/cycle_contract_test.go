package main

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestMultiWarpContractMutations(t *testing.T) {
	root := "../.."
	data, err := os.ReadFile(root + "/timing/ir.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(data, root); err != nil {
		t.Fatal(err)
	}
	contract := func(top map[string]*yaml.Node, id string) map[string]*yaml.Node {
		return records(top["cycle_contracts"])[id]
	}
	params := func(top map[string]*yaml.Node, id string) map[string]*yaml.Node {
		return fields(contract(top, id)["parameters"])
	}
	tests := []struct {
		name, want string
		mutate     func(map[string]*yaml.Node)
	}{
		{"deleted-event", "missing cycle contract", func(m map[string]*yaml.Node) {
			m["cycle_contracts"].Content = m["cycle_contracts"].Content[1:]
		}},
		{"missing-old-state", "missing/empty sampled_state", func(m map[string]*yaml.Node) {
			contract(m, "cc-reserve-release")["sampled_state"].Value = ""
		}},
		{"missing-simultaneous-policy", "missing/empty simultaneous", func(m map[string]*yaml.Node) {
			contract(m, "cc-fu-credit-lock")["simultaneous"].Value = ""
		}},
		{"missing-condition", "missing/empty condition", func(m map[string]*yaml.Node) {
			contract(m, "cc-packed-release")["condition"].Value = ""
		}},
		{"consumer-is-list", "consumer must be text", func(m map[string]*yaml.Node) {
			n := contract(m, "cc-pending")["consumer"]
			*n = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "WB"}}}
		}},
		{"same-edge-wb-bypass", "consumer_offset mismatch", func(m map[string]*yaml.Node) {
			contract(m, "cc-reserve-release")["consumer_offset"].Value = "0"
		}},
		{"negative-offset", "consumer_offset mismatch", func(m map[string]*yaml.Node) {
			contract(m, "cc-decode-unlock")["consumer_offset"].Value = "-1"
		}},
		{"string-offset", "consumer_offset mismatch", func(m map[string]*yaml.Node) {
			contract(m, "cc-pending")["consumer_offset"].Tag = "!!str"
		}},
		{"unresolved-default-zero", "unresolved endpoint must stay null", func(m map[string]*yaml.Node) {
			n := contract(m, "cc-control-collisions")["consumer_offset"]
			n.Tag, n.Value = "!!int", "0"
		}},
		{"software-promoted-to-fact", "classification mismatch", func(m map[string]*yaml.Node) {
			contract(m, "cc-software-context-flush")["status"].Value = "FROZEN"
		}},
		{"unresolved-promoted", "classification mismatch", func(m map[string]*yaml.Node) {
			contract(m, "cc-control-collisions")["status"].Value = "RESOLVED"
		}},
		{"missing-anchors", "missing rtl_anchors", func(m map[string]*yaml.Node) {
			contract(m, "cc-fetch-eligibility")["rtl_anchors"].Content = nil
		}},
		{"invented-signal", "RTL anchor not found", func(m map[string]*yaml.Node) {
			contract(m, "cc-reserve-release")["rtl_anchors"].Content[0].Value = "scoreboard::nonexistent_task10_bypass = 1;"
		}},
		{"software-as-rtl", "anchor source is not frozen RTL", func(m map[string]*yaml.Node) {
			contract(m, "cc-frontend-context")["rtl_anchors"].Content[0].Value = "cycle-effects::Begin"
		}},
		{"unknown-anchor-source", "anchor source is not frozen RTL", func(m map[string]*yaml.Node) {
			contract(m, "cc-pending")["rtl_anchors"].Content[0].Value = "nonexistent::valid"
		}},
		{"dangling-rule", "dangling rule_refs", func(m map[string]*yaml.Node) {
			contract(m, "cc-issue-arbitration")["rule_refs"].Content[0].Value = "missing-rule"
		}},
		{"no-sticky", "arbitration mismatch", func(m map[string]*yaml.Node) {
			fields(records(m["boundaries"])["b-scoreboard-out"]["arbitration"])["sticky"].Value = "0"
		}},
		{"wrong-fetch-policy", "arbitration mismatch", func(m map[string]*yaml.Node) {
			fields(records(m["boundaries"])["b-schedule"]["arbitration"])["policy"].Value = "R"
		}},
		{"wrong-commit-policy", "arbitration mismatch", func(m map[string]*yaml.Node) {
			fields(records(m["boundaries"])["b-commit"]["arbitration"])["policy"].Value = "R"
		}},
		{"wrong-warp-count", "profile mismatch", func(m map[string]*yaml.Node) {
			fields(records(m["config"])["cfg-baseline"]["values"])["warps_per_core"].Value = "8"
		}},
		{"all-full-fallback-removed", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-fetch-eligibility")["full_policy"].Value = "always_block_full"
		}},
		{"wrong-pending-threshold", "profile mismatch", func(m map[string]*yaml.Node) {
			records(m["resources"])["res-warp-pending"]["almost_empty_threshold"].Value = "0"
		}},
		{"missing-hazard-namespace", "profile mismatch", func(m map[string]*yaml.Node) {
			records(m["resources"])["res-hazards"]["register_busy_bits"].Value = "32"
		}},
		{"reserve-before-release", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-reserve-release")["release_then_reserve"].Value = "false"
		}},
		{"reserve-at-staging-input", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-reserve-release")["reserve_boundary"].Value = "ibuffer_fire"
		}},
		{"packed-macro-release", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-packed-release")["release_scope"].Value = "last_element"
		}},
		{"partial-wb-release", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-packed-release")["partial_wb_releases"].Value = "true"
		}},
		{"invented-packed-burst-lock", "profile mismatch", func(m map[string]*yaml.Node) {
			params(m, "cc-packed-release")["packed_fu_unlock"].Value = "0"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(fields(doc.Content[0]))
			changed, err := yaml.Marshal(&doc)
			if err != nil {
				t.Fatal(err)
			}
			err = validate(changed, root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q rejection, got %v", tc.want, err)
			}
		})
	}
}
