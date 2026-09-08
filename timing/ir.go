// Package timing exposes the repository's source-backed Timing IR parameters.
package timing

import (
	_ "embed"
	"fmt"

	"go.yaml.in/yaml/v3"
)

//go:embed ir.yaml
var document []byte

// BufferSpec describes a physical elastic boundary, not a logical stage.
type BufferSpec struct {
	ID     string
	Size   int
	OutReg int
}

// Buffer resolves a stable boundary ID directly from the embedded IR. There is
// no second table of numeric defaults in the implementation.
func Buffer(id string) (BufferSpec, error) { return buffer(document, id) }

func buffer(data []byte, id string) (BufferSpec, error) {
	var ir struct {
		Boundaries []struct {
			ID         string `yaml:"id"`
			Kind       string `yaml:"kind"`
			Parameters struct {
				Size   *int `yaml:"SIZE"`
				OutReg *int `yaml:"OUT_REG"`
				OutBuf *int `yaml:"OUT_BUF"`
				Depth  *int `yaml:"DEPTH"`
			} `yaml:"rtl_parameters"`
		} `yaml:"boundaries"`
		Encoding struct {
			Values map[int]struct {
				Size   *int `yaml:"SIZE"`
				OutReg *int `yaml:"OUT_REG"`
			} `yaml:"values"`
		} `yaml:"buffer_encoding"`
	}
	if err := yaml.Unmarshal(data, &ir); err != nil {
		return BufferSpec{}, err
	}
	for _, b := range ir.Boundaries {
		if b.ID != id {
			continue
		}
		s := BufferSpec{ID: id}
		switch b.Kind {
		case "elastic":
			if b.Parameters.Size == nil || b.Parameters.OutReg == nil {
				return s, fmt.Errorf("%s: missing SIZE/OUT_REG", id)
			}
			s.Size, s.OutReg = *b.Parameters.Size, *b.Parameters.OutReg
		case "output_buffer_encoding", "internal_instance_parameters":
			if b.Parameters.OutBuf == nil {
				return s, fmt.Errorf("%s: missing OUT_BUF", id)
			}
			code := *b.Parameters.OutBuf
			v, ok := ir.Encoding.Values[code]
			if !ok || v.Size == nil || v.OutReg == nil {
				return s, fmt.Errorf("%s: unknown encoding %d", id, code)
			}
			s.Size, s.OutReg = *v.Size, *v.OutReg
		case "pipe_buffer":
			if b.Parameters.Depth == nil || *b.Parameters.Depth != 1 {
				return s, fmt.Errorf("%s: unsupported pipe depth", id)
			}
			s.Size, s.OutReg = *b.Parameters.Depth, 1
		default:
			return s, fmt.Errorf("%s: not an elastic boundary", id)
		}
		if s.Size < 0 || s.OutReg < 0 || s.OutReg > 1 || (s.Size > 2 && s.Size&(s.Size-1) != 0) {
			return s, fmt.Errorf("%s: unsupported buffer parameters", id)
		}
		return s, nil
	}
	return BufferSpec{}, fmt.Errorf("unknown boundary %q", id)
}
