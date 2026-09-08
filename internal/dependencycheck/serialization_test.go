package dependencycheck

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

type serializationFixture struct {
	Name   string `yaml:"name" toml:"name"`
	Cycles uint64 `yaml:"cycles" toml:"cycles"`
}

func TestSerializationDependenciesAreAvailable(t *testing.T) {
	want := serializationFixture{Name: "issue", Cycles: 3}

	t.Run("yaml", func(t *testing.T) {
		data, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("marshal YAML: %v", err)
		}

		var got serializationFixture
		if err := yaml.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal YAML: %v", err)
		}
		if got != want {
			t.Fatalf("YAML round trip = %+v, want %+v", got, want)
		}
	})

	t.Run("toml", func(t *testing.T) {
		data, err := toml.Marshal(want)
		if err != nil {
			t.Fatalf("marshal TOML: %v", err)
		}

		var got serializationFixture
		if err := toml.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal TOML: %v", err)
		}
		if got != want {
			t.Fatalf("TOML round trip = %+v, want %+v", got, want)
		}
	})
}
