// timing-run runs a deterministic single-warp demonstration or a raw RV32 image.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/runner"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	program := flag.String("program", "", "raw little-endian image loaded at 0x100; empty runs built-in demo")
	budget := flag.Uint64("cycles", 2000, "cycle budget")
	config, err := memsys.DefaultConfig()
	if err != nil {
		return err
	}
	mem := flag.Uint64("backend-cycles", config.Latency, "external backend latency in cycles")
	visibility := flag.Uint64("visibility-cycles", 0, "explicit post-execution writeback budget; zero skips backing visibility")
	trace := flag.Bool("trace", false, "emit per-cycle JSON records on stdout")
	flag.Parse()
	init := state.WarpInitial{Topology: state.FrozenTopology(), PC: 0x100, ActiveMask: 15, Lifecycle: state.WarpRunning}
	for lane := uint8(0); lane < 4; lane++ {
		v := state.LaneInitial{ID: lane}
		v.GPR[1] = 64 + 8*uint32(lane)
		init.Lanes = append(init.Lanes, v)
	}
	owner, err := state.NewWarp(init)
	if err != nil {
		return err
	}
	ram, err := memory.New(65536)
	if err != nil {
		return err
	}
	image := []byte{}
	if *program != "" {
		image, err = os.ReadFile(*program)
		if err != nil {
			return err
		}
	} else {
		tmc := uint32(0)
		for _, entry := range isa.Catalog() {
			if entry.Name == "tmc" {
				tmc = entry.Example &^ uint32(31<<15)
			}
		}
		words := []uint32{0x00900113, 0x022101b3, 0x0221c233, 0x0040a023, 0x0000a283, 0x00228463, 0x06300313, tmc}
		image = make([]byte, len(words)*4)
		for i, word := range words {
			binary.LittleEndian.PutUint32(image[i*4:], word)
		}
	}
	if len(image) == 0 || len(image)%4 != 0 {
		return fmt.Errorf("program must contain whole RV32 words")
	}
	if err = ram.Write(0x100, image); err != nil {
		return err
	}
	config.Latency = *mem
	r, err := runner.New(owner, ram, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	var traceErr error
	if err = r.Run(*budget, func(record runner.Record) {
		if *trace && traceErr == nil {
			traceErr = encoder.Encode(record)
		}
	}); err != nil {
		return err
	}
	if traceErr != nil {
		return traceErr
	}
	executionCycles := r.Cycle()
	visible := false
	if r.Completed() && *visibility != 0 {
		visible, err = r.MakeVisible(*visibility)
	}
	fmt.Fprintf(os.Stderr, "execution_cycles=%d cycles=%d backing_visible=%t\n", executionCycles, r.Cycle(), visible)
	if err != nil {
		return err
	}
	snapshot, _ := owner.Snapshot()
	fmt.Fprintf(os.Stderr, "cycles=%d retired=%d pc=%#x mask=%#x completed=%t\n", r.Cycle(), r.Retired(), snapshot.PC(), snapshot.ActiveMask(), r.Completed())
	if !r.Completed() {
		return fmt.Errorf("cycle budget exhausted with preserved pending state")
	}
	if *visibility != 0 && !visible {
		return fmt.Errorf("visibility budget exhausted; backing output is not guaranteed visible")
	}
	return nil
}
