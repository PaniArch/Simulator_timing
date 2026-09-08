package timing

import (
	"fmt"
	"go.yaml.in/yaml/v3"
)

// ArbiterSpec is the arbitration at a named buffered response boundary.
type ArbiterSpec struct {
	Inputs int
	Policy string
}

func Arbiter(id string) (ArbiterSpec, error) { return arbiter(document, id) }
func arbiter(data []byte, id string) (ArbiterSpec, error) {
	var ir struct {
		Boundaries []struct {
			ID          string `yaml:"id"`
			Arbitration struct {
				Inputs *int   `yaml:"inputs"`
				Policy string `yaml:"policy"`
				Sticky *int   `yaml:"sticky"`
				Model  *int   `yaml:"model"`
			} `yaml:"arbitration"`
		} `yaml:"boundaries"`
	}
	if err := yaml.Unmarshal(data, &ir); err != nil {
		return ArbiterSpec{}, err
	}
	for _, b := range ir.Boundaries {
		if b.ID != id {
			continue
		}
		a := b.Arbitration
		if a.Inputs == nil || *a.Inputs < 1 || a.Sticky == nil || *a.Sticky != 0 || (a.Policy != "P" && a.Policy != "R") || (a.Policy == "R" && (a.Model == nil || *a.Model != 1)) {
			return ArbiterSpec{}, fmt.Errorf("%s: missing or unsupported arbitration profile", id)
		}
		return ArbiterSpec{Inputs: *a.Inputs, Policy: a.Policy}, nil
	}
	return ArbiterSpec{}, fmt.Errorf("unknown arbitration boundary %q", id)
}
