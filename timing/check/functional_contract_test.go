package main

import (
	"os"
	"strings"
	"testing"
)

func TestFunctionalContractRejectsIRReplacement(t *testing.T) {
	contract, err := os.ReadFile("../../emu/docs/architecture.md")
	if err != nil {
		t.Fatal(err)
	}
	if err = validateFunctionalContract(contract); err != nil {
		t.Fatal(err)
	}
	ir, err := os.ReadFile("../../timing/ir.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err = validateFunctionalContract(ir); err == nil {
		t.Fatal("Timing IR accepted as functional contract")
	}
	for _, heading := range []string{"## 4. ", "## 5. ", "## 10. ", "## 11. ", "## 15. ", "## 16. "} {
		t.Run(heading, func(t *testing.T) {
			mutated := strings.Replace(string(contract), heading, "## Removed. ", 1)
			if err := validateFunctionalContract([]byte(mutated)); err == nil {
				t.Fatal("missing ownership/audit/implementation section accepted")
			}
		})
	}
}
