// timing-token emits a diagnostic token trace; it does not execute ISA effects.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"vortex.local/simulator/timing/model"
)

func main() {
	var o model.TokenOptions
	var wordsText string
	flag.StringVar(&wordsText, "words", "", "comma-separated hexadecimal instruction words (token stream)")
	flag.StringVar(&o.Backend, "backend", "", "explicit backend: std")
	flag.Uint64Var(&o.PeriodPS, "period-ps", 0, "explicit simulation time scale")
	flag.Uint64Var(&o.FetchCycles, "fetch-cycles", 0, "diagnostic fetch service delay")
	flag.Uint64Var(&o.MemoryCycles, "memory-cycles", 0, "diagnostic memory service delay")
	flag.Uint64Var(&o.Budget, "cycles", 1000, "maximum common edges")
	flag.Parse()
	fail := func(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	if wordsText == "" {
		fail(fmt.Errorf("-words is required"))
	}
	words := []uint32{}
	for _, word := range strings.Split(wordsText, ",") {
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(word), "0x"), 16, 32)
		if err != nil {
			fail(err)
		}
		words = append(words, uint32(n))
	}
	run, err := model.RunTokens(words, o)
	encoder := json.NewEncoder(os.Stdout)
	for _, cycle := range run.Cycles {
		if e := encoder.Encode(cycle); e != nil {
			fail(e)
		}
	}
	if err != nil {
		fail(err)
	}
}
