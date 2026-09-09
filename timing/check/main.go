// Command check validates only this repository's static Timing IR.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

var collections = []string{"config", "nodes", "resources", "boundaries", "ports", "channels", "rules", "unknowns", "audit", "timing_measurements", "cycle_contracts"}
var references = map[string]string{"node": "nodes", "nodes": "nodes", "from": "ports", "to": "ports", "resource_refs": "resources", "boundary_refs": "boundaries", "rule_refs": "rules", "unknown_ref": "unknowns", "affected": "nodes", "config_ref": "config", "encoding_ref": "buffer_encoding"}

func fields(n *yaml.Node) map[string]*yaml.Node {
	m := map[string]*yaml.Node{}
	if n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			m[n.Content[i].Value] = n.Content[i+1]
		}
	}
	return m
}
func nonempty(n *yaml.Node) bool {
	return n != nil && n.Tag != "!!null" && (strings.TrimSpace(n.Value) != "" || len(n.Content) > 0)
}
func required(m map[string]*yaml.Node, keys ...string) error {
	for _, k := range keys {
		if !nonempty(m[k]) {
			return fmt.Errorf("missing/empty %s", k)
		}
	}
	return nil
}
func scalars(n *yaml.Node) []*yaml.Node {
	if n.Kind == yaml.SequenceNode {
		return n.Content
	}
	return []*yaml.Node{n}
}

func validate(data []byte, root string) error {
	var doc yaml.Node
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one YAML document")
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping root")
	}
	top := fields(doc.Content[0])
	ids := map[string]map[string]bool{}
	all := map[string]bool{}
	for _, c := range collections {
		n := top[c]
		if n == nil || n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
			return fmt.Errorf("missing collection %s", c)
		}
		ids[c] = map[string]bool{}
		for _, r := range n.Content {
			m := fields(r)
			if err := required(m, "id", "status", "evidence"); err != nil {
				return fmt.Errorf("%s: %w", c, err)
			}
			id := m["id"].Value
			if all[id] {
				return fmt.Errorf("duplicate ID %s", id)
			}
			all[id] = true
			ids[c][id] = true
		}
	}
	encoding := fields(top["buffer_encoding"])
	if err := required(encoding, "id", "status"); err != nil {
		return err
	}
	ids["buffer_encoding"] = map[string]bool{encoding["id"].Value: true}
	sources := fields(top["sources"])
	if len(sources) == 0 {
		return fmt.Errorf("missing sources")
	}
	for id, n := range sources {
		m := fields(n)
		if err := required(m, "path", "locator"); err != nil {
			return fmt.Errorf("source %s: %w", id, err)
		}
		p := filepath.Clean(m["path"].Value)
		if filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			return fmt.Errorf("source outside repository: %s", p)
		}
		info, err := os.Stat(filepath.Join(root, p))
		if err != nil || info.IsDir() {
			return fmt.Errorf("source file missing: %s", p)
		}
	}
	contract, err := os.ReadFile(filepath.Join(root, "emu/docs/architecture.md"))
	if err != nil {
		return err
	}
	if err := validateFunctionalContract(contract); err != nil {
		return err
	}
	var walk func(*yaml.Node, bool) error
	walk = func(n *yaml.Node, inUnknown bool) error {
		if n.Kind == yaml.AliasNode {
			return fmt.Errorf("aliases are not used by this IR")
		}
		if n.Kind == yaml.MappingNode {
			m := fields(n)
			seen := map[string]bool{}
			if s := m["status"]; s != nil {
				switch s.Value {
				case "FROZEN", "PROVISIONAL", "UNRESOLVED", "RESOLVED":
				default:
					return fmt.Errorf("invalid status %s", s.Value)
				}
			}
			for i := 0; i < len(n.Content); i += 2 {
				key, v := n.Content[i].Value, n.Content[i+1]
				if seen[key] {
					return fmt.Errorf("duplicate key %s", key)
				}
				seen[key] = true
				if target, ok := references[key]; ok && n != doc.Content[0] {
					for _, r := range scalars(v) {
						if r.Kind != yaml.ScalarNode || !ids[target][r.Value] {
							return fmt.Errorf("dangling %s: %s", key, r.Value)
						}
					}
				}
				if key == "evidence" {
					if v.Kind != yaml.SequenceNode {
						return fmt.Errorf("evidence must be sequence")
					}
					for _, e := range v.Content {
						parts := strings.SplitN(e.Value, "::", 2)
						if len(parts) != 2 || sources[parts[0]] == nil || parts[1] == "" {
							return fmt.Errorf("invalid evidence %s", e.Value)
						}
					}
				}
				if key == "functional_refs" {
					if v.Kind != yaml.SequenceNode {
						return fmt.Errorf("functional_refs must be sequence")
					}
					for _, r := range v.Content {
						if !strings.HasPrefix(r.Value, "U-") || !strings.Contains(string(contract), r.Value) {
							return fmt.Errorf("unknown functional issue %s", r.Value)
						}
					}
				}
				if v.Tag == "!!null" && !inUnknown && m["unknown_ref"] == nil {
					return fmt.Errorf("null %s needs unknown_ref", key)
				}
				if err := walk(v, inUnknown || key == "unknowns"); err != nil {
					return err
				}
			}
		} else {
			for _, v := range n.Content {
				if err := walk(v, inUnknown); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(doc.Content[0], false); err != nil {
		return err
	}
	for _, n := range top["unknowns"].Content {
		m := fields(n)
		if err := required(m, "question", "known", "missing", "affected", "deadline"); err != nil {
			return fmt.Errorf("unknown %s: %w", m["id"].Value, err)
		}
		if m["status"].Value != "UNRESOLVED" {
			return fmt.Errorf("unknown must remain UNRESOLVED")
		}
	}
	for _, n := range top["audit"].Content {
		m := fields(n)
		if err := required(m, "original_question", "decision", "validation", "closure_task"); err != nil {
			return err
		}
		if m["status"].Value != "RESOLVED" {
			return fmt.Errorf("audit must be RESOLVED")
		}
	}
	for _, n := range top["timing_measurements"].Content {
		m := fields(n)
		if err := required(m, "node", "kind", "unit", "condition", "start_event", "end_event"); err != nil {
			return fmt.Errorf("measurement %s: %w", m["id"].Value, err)
		}
		v := m["value"]
		if v == nil {
			return fmt.Errorf("missing measurement value")
		}
		if v.Tag != "!!int" && v.Tag != "!!float" && v.Tag != "!!null" {
			return fmt.Errorf("measurement value must be numeric or explicit unknown")
		}
		if v.Tag == "!!int" || v.Tag == "!!float" {
			var number float64
			if err := v.Decode(&number); err != nil || number < 0 {
				return fmt.Errorf("invalid measurement value")
			}
		}
		if m["resource_refs"] == nil || m["resource_refs"].Kind != yaml.SequenceNode {
			return fmt.Errorf("missing resource_refs")
		}
	}
	for _, n := range top["resources"].Content {
		m := fields(n)
		if err := required(m, "condition", "measurement"); err != nil {
			return err
		}
		if err := required(fields(m["measurement"]), "unit", "start", "end"); err != nil {
			return err
		}
		if m["capacity"] != nil && !nonempty(m["capacity_unit"]) {
			return fmt.Errorf("capacity lacks unit")
		}
	}
	for _, n := range top["boundaries"].Content {
		m := fields(n)
		if err := required(m, "condition", "measurement"); err != nil {
			return err
		}
		if err := required(fields(m["measurement"]), "unit", "start", "end"); err != nil {
			return err
		}
	}
	return validateCycleContracts(top, root)
}
func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	data, err := os.ReadFile(filepath.Join(root, "timing/ir.yaml"))
	if err == nil {
		err = validate(data, root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify-timing:", err)
		os.Exit(1)
	}
	fmt.Println("verify-timing: IR consistency PASS (not a timing-equivalence proof)")
}
