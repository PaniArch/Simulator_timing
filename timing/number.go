package timing

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Number reads a required integer by collection, stable ID and field path.
// Missing/null values never become zero. The static checker owns the complete
// schema and reference validation; this accessor owns implementation inputs.
func Number(collection, id string, path ...string) (int, error) {
	return number(document, collection, id, path...)
}

func number(data []byte, collection, id string, path ...string) (int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return 0, err
	}
	field := func(n *yaml.Node, key string) *yaml.Node {
		if n != nil && n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == key {
					return n.Content[i+1]
				}
			}
		}
		return nil
	}
	if len(root.Content) != 1 {
		return 0, fmt.Errorf("invalid IR root")
	}
	items := field(root.Content[0], collection)
	if items != nil && items.Kind == yaml.SequenceNode {
		for _, item := range items.Content {
			name := field(item, "id")
			if name == nil || name.Value != id {
				continue
			}
			n := item
			for _, key := range path {
				n = field(n, key)
			}
			if n == nil || n.Tag != "!!int" {
				return 0, fmt.Errorf("%s/%s/%v: required integer missing or unknown", collection, id, path)
			}
			var value int
			if err := n.Decode(&value); err != nil {
				return 0, err
			}
			return value, nil
		}
	}
	return 0, fmt.Errorf("unknown %s ID %q", collection, id)
}
