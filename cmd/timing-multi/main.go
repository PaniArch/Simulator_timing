// timing-multi runs four explicit owners through the concurrent timing Core.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"vortex.local/simulator/emu/state"
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
	config, err := memsys.DefaultConfig()
	if err != nil {
		return err
	}
	budget := flag.Uint64("cycles", 1000, "cycle budget")
	mem := flag.Uint64("backend-cycles", config.Latency, "external backend latency measured from request acceptance")
	visibility := flag.Uint64("visibility-cycles", 0, "explicit post-execution writeback budget; zero skips backing visibility")
	trace := flag.Bool("trace", false, "emit detached per-edge JSON records")
	flag.Parse()
	config.Latency = *mem
	ram, err := memory.New(4096)
	if err != nil {
		return err
	}
	programs := [4][]uint32{
		{0x0000a183, 0x00400213, 0x022102b3, 0x0000000b},
		{0x00000217, 0x00900293, 0x0050a223, 0x0000000b},
		{0x002081d3, 0x10208253, 0x00600313, 0x0000000b},
		{0x0820918b, 0x00700393, 0x0000000b},
	}
	var owners [4]*state.WarpState
	for w, program := range programs {
		init := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: uint8(w), PC: uint32(0x100 * (w + 1)), ActiveMask: 15, Lifecycle: state.WarpRunning}
		for lane := uint8(0); lane < 4; lane++ {
			l := state.LaneInitial{ID: lane}
			l.GPR[1] = uint32(0x800 + w*0x100 + int(lane)*16)
			l.GPR[2] = 3
			if w == 3 {
				l.GPR[2] = 1
			}
			l.FPR[1] = 0x3f800000
			l.FPR[2] = 0x40400000
			l.FPR[3] = 0xaaaaaaaa
			init.Lanes = append(init.Lanes, l)
			if err = ram.Write(l.GPR[1], []byte{1, 2, 3, 4, 5, 6, 7, 8}); err != nil {
				return err
			}
		}
		owners[w], err = state.NewWarp(init)
		if err != nil {
			return err
		}
		for n, word := range program {
			var bytes [4]byte
			binary.LittleEndian.PutUint32(bytes[:], word)
			if err = ram.Write(init.PC+uint32(4*n), bytes[:]); err != nil {
				return err
			}
		}
	}
	r, err := runner.NewMulti(owners, ram, runner.MultiOptions{Options: runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config}})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	var traceErr error
	if err = r.Run(*budget, func(record runner.MultiRecord) {
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
	fmt.Fprintf(os.Stderr, "cycles=%d retired=%v inflight=%d completed=%t\n", r.Cycle(), r.Retired(), r.InFlight(), r.Completed())
	if !r.Completed() {
		return fmt.Errorf("cycle budget exhausted; blocked/pending work is not completion")
	}
	if *visibility != 0 && !visible {
		return fmt.Errorf("visibility budget exhausted; backing output is not guaranteed visible")
	}
	return nil
}
