package main

import (
	"flag"
	"os"
	"testing"
)

func TestBuiltInProgramEntry(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	os.Args = []string{"timing-run", "-cycles", "600"}
	flag.CommandLine = flag.NewFlagSet("timing-run", flag.ContinueOnError)
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
