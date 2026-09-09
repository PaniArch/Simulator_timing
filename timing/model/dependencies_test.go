package model

import (
	"testing"
	"vortex.local/simulator/isa"
)

func TestDecodeScoreboardDependencies(t *testing.T) {
	tests := []struct {
		name              string
		word              uint32
		rd                uint8
		wb                bool
		sources           [3]uint8
		used, read, write uint8
	}{
		{"integer-zero", 0x00000013, 0, false, [3]uint8{}, 1, 0, 0},
		{"add", 0x003100b3, 1, true, [3]uint8{2, 3}, 3, 0, 0},
		{"float-zero", 0x00000053, 32, true, [3]uint8{32, 32}, 3, 0, 1},
		{"dynamic-rounding", 0x00007053, 32, true, [3]uint8{32, 32}, 3, 2, 1},
		{"sign-injection", 0x20000053, 32, true, [3]uint8{32, 32}, 3, 0, 0},
		{"class-to-x0", 0xe0001053, 0, false, [3]uint8{32}, 1, 0, 0},
		{"compare-to-x0", 0xa0002053, 0, false, [3]uint8{32, 32}, 3, 0, 1},
		{"fused-three-sources", 0x00000043, 32, true, [3]uint8{32, 32, 32}, 7, 0, 1},
		{"fcsr-read", 0x00302073, 0, false, [3]uint8{}, 1, 3, 0},
		{"frm-immediate-write", 0x0020d073, 0, false, [3]uint8{}, 0, 2, 2},
		{"fflags-write-zero", 0x00101073, 0, false, [3]uint8{}, 1, 1, 1},
		{"load-x0", 0x00002003, 0, false, [3]uint8{}, 1, 0, 0},
		{"load-f0", 0x00002007, 32, true, [3]uint8{}, 1, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := DecodeToken(Signal{Valid: true, Token: Token{Word: tc.word}})
			if err != nil {
				t.Fatal(err)
			}
			x := out.Token
			if x.Destination != tc.rd || x.Writeback != tc.wb || x.Sources != tc.sources || x.Used != tc.used || x.ReadSpecial != tc.read || x.WriteSpecial != tc.write {
				t.Fatalf("dependency metadata %+v", x)
			}
			if !x.FULock || !x.FUUnlock {
				t.Fatal("ordinary uop must carry lock/unlock 11")
			}
		})
	}
}

func TestDecodeWarpStallAndPackedMetadata(t *testing.T) {
	want := map[string]bool{"beq": true, "jal": true, "ecall": true, "mret": true, "tmc": true, "pred": true, "split": true, "join": true, "wspawn": true, "wsync": true, "bar": true, "bar.arrive": false, "bar.wait": true, "addi": false, "csrrw": false, "vx_packlb_f": false, "vx_packlh_f": false}
	seen := map[string]bool{}
	for _, e := range isa.Catalog() {
		expected, ok := want[e.Name]
		if !ok {
			continue
		}
		out, err := DecodeToken(Signal{Valid: true, Token: Token{Word: e.Example}})
		if err != nil {
			t.Fatal(e.Name, err)
		}
		if out.Token.WarpStall != expected {
			t.Fatalf("%s stall %t want %t", e.Name, out.Token.WarpStall, expected)
		}
		if out.Token.Pack != 0 && (!out.Token.FULock || !out.Token.FUUnlock || !out.Token.Writeback || out.Token.Destination < 32) {
			t.Fatal("packed metadata", out.Token)
		}
		seen[e.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing catalog case %s", name)
		}
	}
}
