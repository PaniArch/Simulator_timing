package main

import (
	"go.yaml.in/yaml/v3"
	"os"
	"strings"
	"testing"
)

// Mutations start from the real valid IR so rejection cannot be explained by
// an unrelated missing fixture field. Nothing is written into source inputs.
func TestIRAndInvalidMutations(t *testing.T) {
	root := "../.."
	data, err := os.ReadFile(root + "/timing/ir.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(data, root); err != nil {
		t.Fatal(err)
	}
	first := func(top map[string]*yaml.Node, c string) map[string]*yaml.Node { return fields(top[c].Content[0]) }
	tests := []struct {
		name, want string
		mutate     func(map[string]*yaml.Node)
	}{
		{"duplicate-id", "duplicate ID", func(m map[string]*yaml.Node) { m["nodes"].Content = append(m["nodes"].Content, m["nodes"].Content[0]) }},
		{"dangling-port", "dangling from", func(m map[string]*yaml.Node) { first(m, "channels")["from"].Value = "p-missing" }},
		{"wrong-reference-type", "dangling node", func(m map[string]*yaml.Node) { first(m, "resources")["node"].Value = "res-alu" }},
		{"missing-source", "source file missing", func(m map[string]*yaml.Node) { fields(fields(m["sources"])["bundle"])["path"].Value = "missing-rtl.sv" }},
		{"invalid-status", "invalid status", func(m map[string]*yaml.Node) { first(m, "nodes")["status"].Value = "CONFIRMED" }},
		{"unitless-latency", "missing/empty unit", func(m map[string]*yaml.Node) { first(m, "timing_measurements")["unit"].Value = "" }},
		{"missing-endpoint", "missing/empty end_event", func(m map[string]*yaml.Node) { first(m, "timing_measurements")["end_event"].Value = "" }},
		{"unknown-gap-missing", "missing/empty missing", func(m map[string]*yaml.Node) { first(m, "unknowns")["missing"].Value = "" }},
		{"orphan-null", "needs unknown_ref", func(m map[string]*yaml.Node) {
			n := first(m, "timing_measurements")["value"]
			n.Tag = "!!null"
			n.Value = "null"
		}},
		{"negative-latency", "invalid measurement value", func(m map[string]*yaml.Node) { first(m, "timing_measurements")["value"].Value = "-1" }},
		{"unknown-evidence", "invalid evidence", func(m map[string]*yaml.Node) { first(m, "nodes")["evidence"].Content[0].Value = "absent::signal" }},
		{"unknown-functional-id", "unknown functional issue", func(m map[string]*yaml.Node) {
			first(m, "unknowns")["functional_refs"].Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "U-NONEXISTENT-99"}}
		}},
		{"duplicate-key", "duplicate key", func(m map[string]*yaml.Node) {
			n := m["nodes"].Content[0]
			n.Content = append(n.Content, n.Content[0], n.Content[1])
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(fields(doc.Content[0]))
			bad, err := yaml.Marshal(&doc)
			if err != nil {
				t.Fatal(err)
			}
			err = validate(bad, root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q rejection, got %v", tc.want, err)
			}
		})
	}
	t.Run("syntax", func(t *testing.T) {
		if validate([]byte("nodes: ["), root) == nil {
			t.Fatal("accepted broken YAML")
		}
	})
	t.Run("extra-document", func(t *testing.T) {
		if validate(append(data, []byte("\n---\nextra: true\n")...), root) == nil {
			t.Fatal("accepted extra document")
		}
	})
}
