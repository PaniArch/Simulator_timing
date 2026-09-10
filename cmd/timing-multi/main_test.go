package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

func TestExplicitVisibilityBudget(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	for _, budget := range []string{"1", "4000"} {
		os.Args = []string{"timing-multi", "-cycles", "2000", "-backend-cycles", "3", "-visibility-cycles", budget}
		flag.CommandLine = flag.NewFlagSet("timing-multi", flag.ContinueOnError)
		err := run()
		if budget == "1" {
			if err == nil || !strings.Contains(err.Error(), "visibility budget exhausted") {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
