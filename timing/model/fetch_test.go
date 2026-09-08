package model

import (
	"testing"

	"vortex.local/simulator/isa"
)

func TestFetchRequestBoundariesAndDecodeResponsePassthrough(t *testing.T) {
	f, err := NewFetch()
	if err != nil {
		t.Fatal(err)
	}
	in := valid(1)
	in.Token.PC = 0x100
	p, err := f.Evaluate(in, Response{}, true, true)
	if err != nil || !p.Accepted || p.RequestAccepted {
		t.Fatal("request capture", err)
	}
	commit(t, p.Transition)
	if f.Occupancy() != 1 {
		t.Fatal("context allocation not at request buffer input")
	}
	rsp := response(in.Token, 15)
	rsp.Word = 0x00100093
	if _, err = f.Evaluate(Signal{}, rsp, true, true); err == nil {
		t.Fatal("response before request sent")
	}
	p, err = f.Evaluate(Signal{}, Response{}, true, true)
	if err != nil || p.RequestAccepted {
		t.Fatal("passed two request registers", err)
	}
	commit(t, p.Transition)
	p, err = f.Evaluate(Signal{}, Response{}, false, true)
	if err != nil || !p.Request.Valid || p.RequestAccepted {
		t.Fatal("request output stall", err)
	}
	commit(t, p.Transition)
	p, err = f.Evaluate(Signal{}, Response{}, true, true)
	if err != nil || !p.RequestAccepted {
		t.Fatal("request not delivered", err)
	}
	commit(t, p.Transition)
	next := valid(2)
	p, err = f.Evaluate(next, rsp, true, false)
	if err != nil || p.Accepted || p.Completed || !p.Output.Valid {
		t.Fatal("fetch context overwrite/stall", err)
	}
	commit(t, p.Transition)
	ibuf := mustBuffer(t, "b-ibuffer")
	p, err = f.Evaluate(next, rsp, true, ibuf.Ready(false))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeToken(p.Output)
	if err != nil {
		t.Fatal(err)
	}
	b := ibuf.Evaluate(decoded, false)
	if !p.Completed || !b.Accepted || p.Accepted {
		t.Fatal("decode added latency or borrowed same-warp tag")
	}
	if err = CommitEdge(b, p.Transition); err != nil {
		t.Fatal(err)
	}
	if f.Occupancy() != 0 || ibuf.Contents()[0].Path != INT || ibuf.Contents()[0].PC != 0x100 {
		t.Fatal("response context lost")
	}
}

func TestDecodeUsesISACatalogAndRoutesSpecialCases(t *testing.T) {
	want := map[string]Path{
		"addi": INT, "mulhu": MUL, "remu": DIV, "fence": FENCE, "lw": LOAD, "sw": STORE,
		"flw": LOAD, "fsw": STORE, "vx_packlb_f": LOAD, "vx_packlh_f": LOAD,
		"csrrw": CSRPath, "mret": INT, "ecall": INT, "tmc": WCTL, "wspawn": WCTL,
		"vote.all": INT, "shfl.down": INT, "wgather": INT,
		"fadd.s": FMA, "fmadd.s": FMA, "fnmsub.s": FMA, "fsqrt.s": DIVSQRT,
		"fcvt.w.s": CVT, "fmin.s": NCP, "fmv.w.x": NCP,
	}
	seen := map[string]bool{}
	for _, entry := range isa.Catalog() {
		in := valid(1)
		in.Token.Word = entry.Example
		out, err := DecodeToken(in)
		if err != nil {
			t.Fatalf("%s: %v", entry.Name, err)
		}
		if out.Token.Path == "" || out.Token.Class > 3 {
			t.Fatal("missing timing path", entry.Name)
		}
		if path, ok := want[entry.Name]; ok {
			seen[entry.Name] = true
			if out.Token.Path != path {
				t.Fatalf("%s path %s want %s", entry.Name, out.Token.Path, path)
			}
		}
		if entry.Name == "vx_packlb_f" && out.Token.Pack != 1 || entry.Name == "vx_packlh_f" && out.Token.Pack != 2 {
			t.Fatal("packed kind wrong")
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatal("test case not in catalog", name)
		}
	}
	in := valid(1)
	in.Token.Word = 0
	if _, err := DecodeToken(in); err == nil {
		t.Fatal("illegal instruction bypassed ISA decoder")
	}
	// fadd.s f1,f0,f0 must read both f0 sources (RTL ID 32), unlike x0.
	in.Token.Word = 0x000000d3
	out, err := DecodeToken(in)
	if err != nil || out.Token.Sources[0] != 32 || out.Token.Sources[1] != 32 {
		t.Fatal("f0 namespace lost", err)
	}
}
