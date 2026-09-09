package timing

import (
	"strings"
	"testing"
)

func TestArbiterIRAndUnknowns(t *testing.T) {
	for _, tc := range []struct {
		id, policy string
		inputs     int
	}{{"b-alu-merge", "R", 2}, {"b-alu-muldiv-merge", "P", 2}, {"b-sfu-merge", "R", 2}, {"b-fpu-backend-out", "R", 4}, {"b-schedule", "P", 4}, {"b-commit", "P", 4}} {
		a, err := Arbiter(tc.id)
		if err != nil || a.Policy != tc.policy || a.Inputs != tc.inputs {
			t.Fatal(tc, a, err)
		}
	}
	fixture := "boundaries:\n- id: b\n  arbitration:\n    inputs: 2\n    policy: R\n    sticky: 0\n    model: 1\n"
	// The accessor follows YAML changes, while composition asserts port topology.
	changed := strings.Replace(fixture, "inputs: 2", "inputs: 4", 1)
	a, err := arbiter([]byte(changed), "b")
	if err != nil || a.Inputs != 4 {
		t.Fatal(a, err)
	}
	for _, tc := range []struct{ old, new string }{{"inputs: 2", "inputs: null"}, {"inputs: 2", "inputs: 0"}, {"policy: R", "policy: unknown"}, {"sticky: 0", "sticky: 2"}, {"model: 1", "model: 2"}, {"model: 1", "model: null"}} {
		if _, err := arbiter([]byte(strings.Replace(fixture, tc.old, tc.new, 1)), "b"); err == nil {
			t.Fatal("accepted unknown profile", tc)
		}
	}
	if _, err := Arbiter("b-decode"); err == nil {
		t.Fatal("missing arbitration silently defaulted")
	}
	if a, err := Arbiter("b-scoreboard-out"); err != nil || !a.Sticky || a.Inputs != 4 || a.Policy != "R" {
		t.Fatal("sticky issue profile", a, err)
	}
}
