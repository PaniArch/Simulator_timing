package model

import (
	"fmt"
	"strings"

	"vortex.local/simulator/isa"
)

// Path names the timing subpath only. ISA Decode remains the sole legality and
// operand decoder; all functional equations remain in the existing evaluator.
type Path string

const (
	INT     Path = "int"
	MUL     Path = "mul"
	DIV     Path = "div"
	LOAD    Path = "load"
	STORE   Path = "store"
	FENCE   Path = "fence"
	WCTL    Path = "wctl"
	CSRPath Path = "csr"
	FMA     Path = "fma"
	DIVSQRT Path = "divsqrt"
	CVT     Path = "cvt"
	NCP     Path = "ncp"
)

// DecodeToken is combinational (b-decode SIZE=0), and can feed ibuffer at the
// response acceptance edge. It neither reads nor changes canonical state.
func DecodeToken(input Signal) (Signal, error) {
	if !input.Valid {
		return Signal{}, nil
	}
	d, err := isa.Decode(input.Token.Word)
	if err != nil {
		return Signal{}, err
	}
	t := input.Token
	t.Sources = [3]uint8{}
	t.Used = 0
	t.ReadMask = 0
	t.LastRead = false
	t.Pack = 0
	t.Uop = 0
	t.Uops = 1
	t.End = true
	if len(d.Sources) > len(t.Sources) {
		return Signal{}, fmt.Errorf("unsupported source count")
	}
	for i, r := range d.Sources {
		t.Sources[i] = r.Index
		if r.File == isa.Float {
			t.Sources[i] += 32
		}
		t.Used |= 1 << i // VX_decode keeps used_rs for x0; WB/Collector suppress it.
	}
	t.Branch = d.Control == isa.ControlBranch || d.Control == isa.ControlJump || d.Control == isa.ControlTrap || d.Control == isa.ControlTrapReturn
	t.Destination, t.Writeback = 0, false
	if len(d.Destinations) > 1 {
		return Signal{}, fmt.Errorf("unsupported destination count")
	}
	for _, r := range d.Destinations {
		t.Destination = r.Index
		if r.File == isa.Float {
			t.Destination += 32
		}
		t.Writeback = t.Destination != 0
	}
	t.FULock, t.FUUnlock = true, true
	t.ReadSpecial, t.WriteSpecial = 0, 0
	if d.Category == isa.CategoryRV32F && d.Memory.Kind == isa.MemoryNone {
		if d.Rounding == isa.Dynamic {
			t.ReadSpecial = 2
		}
		if !strings.HasPrefix(d.Name, "fsgnj") && !strings.HasPrefix(d.Name, "fmv.") && d.Name != "fclass.s" {
			t.WriteSpecial = 1
		}
	}
	if d.CSR != isa.CSRNone {
		switch d.CSRAddress {
		case 1:
			t.ReadSpecial = 1
		case 2:
			t.ReadSpecial = 2
		case 3:
			t.ReadSpecial = 3
		}
		// VX_decode.csr_write uses the encoded rs1/zimm field, never its value.
		if d.CSR == isa.CSRRW || (d.Word>>15)&31 != 0 {
			t.WriteSpecial = t.ReadSpecial
		}
	}
	t.Class = 0
	t.Path = INT
	switch d.Memory.Kind {
	case isa.MemoryLoad:
		t.Class = 1
		t.Path = LOAD
		if d.Memory.Packed != 0 {
			t.Pack = d.Memory.Bytes
		}
	case isa.MemoryStore:
		t.Class = 1
		t.Path = STORE
	case isa.MemoryFence:
		t.Class = 1
		t.Path = FENCE
	default:
		switch d.Category {
		case isa.CategoryRV32I, isa.CategoryZicond:
		case isa.CategoryRV32M:
			if strings.HasPrefix(d.Name, "mul") {
				t.Path = MUL
			} else {
				t.Path = DIV
			}
		case isa.CategorySystem:
			if d.CSR != isa.CSRNone {
				t.Class = 2
				t.Path = CSRPath
			}
		case isa.CategoryFence:
			t.Class = 1
			t.Path = FENCE
		case isa.CategoryCustom:
			if !strings.HasPrefix(d.Name, "vote.") && !strings.HasPrefix(d.Name, "shfl.") && d.Name != "wgather" {
				t.Class = 2
				t.Path = WCTL
			}
		case isa.CategoryRV32F:
			t.Class = 3
			switch {
			case strings.HasPrefix(d.Name, "fcvt."):
				t.Path = CVT
			case d.Name == "fdiv.s" || d.Name == "fsqrt.s":
				t.Path = DIVSQRT
			case d.Name == "fadd.s" || d.Name == "fsub.s" || d.Name == "fmul.s" || strings.HasPrefix(d.Name, "fmadd.") || strings.HasPrefix(d.Name, "fmsub.") || strings.HasPrefix(d.Name, "fnmadd.") || strings.HasPrefix(d.Name, "fnmsub."):
				t.Path = FMA
			default:
				t.Path = NCP
			}
		default:
			return Signal{}, fmt.Errorf("unsupported timing category %s", d.Category)
		}
	}
	t.WarpStall = t.Branch || (t.Path == WCTL && d.Barrier != isa.BarrierArrive)
	return Signal{Valid: true, Token: t}, nil
}
