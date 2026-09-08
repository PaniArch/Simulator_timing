package timing

import (
	"strings"
	"testing"
)

func TestBufferParametersComeFromIR(t *testing.T) {
	for _, test := range []struct {
		id        string
		size, reg int
	}{
		{"b-decode", 0, 0}, {"b-alu-int-result", 1, 0}, {"b-schedule", 2, 1},
		{"b-scoreboard-staging", 1, 1}, {"b-scoreboard-out", 2, 1}, {"b-dispatch", 4, 1},
	} {
		s, err := Buffer(test.id)
		if err != nil || s.Size != test.size || s.OutReg != test.reg {
			t.Fatalf("%s: %+v %v", test.id, s, err)
		}
	}
	// A changed YAML value must affect the implementation, not a shadow default.
	changed := strings.Replace(string(document), "SIZE: 4\n    OUT_REG: 1", "SIZE: 8\n    OUT_REG: 1", 1)
	s, err := buffer([]byte(changed), "b-ibuffer")
	if err != nil || s.Size != 8 {
		t.Fatalf("numeric drift hidden: %+v %v", s, err)
	}
	for _, id := range []string{"missing", "b-opc-read"} {
		if _, err := Buffer(id); err == nil {
			t.Fatal("unsupported boundary accepted", id)
		}
	}
	for _, replacement := range []string{"SIZE: null", "SIZE: -1", "SIZE: 3"} {
		changed := strings.Replace(string(document), "SIZE: 4\n    OUT_REG: 1", replacement+"\n    OUT_REG: 1", 1)
		if _, err := buffer([]byte(changed), "b-ibuffer"); err == nil {
			t.Fatal("invalid size silently accepted", replacement)
		}
	}
	changed = strings.Replace(string(document), "SIZE: 2\n      OUT_REG: 1", "SIZE: null\n      OUT_REG: 1", 1)
	if _, err := buffer([]byte(changed), "b-scoreboard-out"); err == nil {
		t.Fatal("null encoding treated as zero")
	}
}

func TestRequiredNumberCannotSilentlyDefault(t *testing.T) {
	for _, test := range []struct {
		collection, id, field string
		want                  int
	}{
		{"timing_measurements", "tm-idiv-result", "value", 33},
		{"resources", "res-lsu-load-tags", "capacity", 8},
		{"timing_measurements", "tm-buffer-0", "value", 0},
	} {
		got, err := Number(test.collection, test.id, test.field)
		if err != nil || got != test.want {
			t.Fatalf("%s: %d %v", test.id, got, err)
		}
	}
	for _, value := range []string{"null", "false", "'3'"} {
		data := []byte("resources:\n- id: test\n  capacity: " + value + "\n")
		if _, err := number(data, "resources", "test", "capacity"); err == nil {
			t.Fatal("unknown/noninteger defaulted", value)
		}
	}
	if _, err := Number("resources", "res-lsu-load-tags", "missing"); err == nil {
		t.Fatal("missing field defaulted")
	}
}
