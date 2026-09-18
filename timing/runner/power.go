package runner

import (
	"fmt"
	akita "github.com/sarchlab/akita/v5/timing"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

// PoweredDevice owns reset-time storage and its clock before a KMU launch.
// Callers serialize access. Start transfers ownership, including partial reset
// progress, exactly once; it never skips initialization or resets the clock.
type PoweredDevice struct {
	backing     device.BackingMemory
	options     Options
	memory      *runnerMemory
	clock       *model.Clock
	transferred bool
	failed      error
}

func PowerOn(backing device.BackingMemory, options Options) (*PoweredDevice, error) {
	if backing == nil {
		return nil, fmt.Errorf("device requires backing memory")
	}
	config, err := memsys.DefaultConfig()
	if err != nil {
		return nil, err
	}
	if options.MemoryConfig != nil {
		config = *options.MemoryConfig
	}
	clock, err := model.NewClock(akita.VTimeInPicoSec(options.PeriodPS))
	if err != nil {
		return nil, err
	}
	m, err := newRunnerMemory(backing, &MemorySystemOptions{Config: config, Backend: options.MemoryBackend,
		Bind: func(model.Token) memsys.Identity { return memsys.Identity{} },
		LocalOwner: func(memsys.Identity) (warp.AtomicMemoryService, error) {
			return nil, fmt.Errorf("LMEM access before launch")
		},
	})
	if err != nil {
		return nil, err
	}
	return &PoweredDevice{backing: backing, options: options, memory: m, clock: clock}, nil
}

func (d *PoweredDevice) Cycle() uint64 { return d.clock.Cycle() }

// Initialize clocks actual cache init operations and their pipeline tails.
// Readiness is derived from component state, never a fitted warmup constant.
// A caller may instead Start before readiness to model an early KMU start.
func (d *PoweredDevice) Initialize(budget uint64) (bool, error) {
	if d.transferred {
		return false, fmt.Errorf("device ownership transferred")
	}
	if d.failed != nil {
		return false, d.failed
	}
	if d.memory.system.ResetSettled() {
		return true, nil
	}
	d.failed = d.clock.Run(budget, func(cycle uint64) (bool, error) {
		_, err := d.memory.system.Step(cycle, memsys.SystemInput{FetchReady: true, MemoryReady: true})
		return d.memory.system.ResetSettled(), err
	})
	return d.failed == nil && d.memory.system.ResetSettled(), d.failed
}

func (d *PoweredDevice) Start(input device.LaunchState) (*Kernel, error) {
	if d.transferred {
		return nil, fmt.Errorf("device ownership transferred")
	}
	if d.failed != nil {
		return nil, d.failed
	}
	k, err := newKernel(input, d.backing, d.options, nil, d)
	if err != nil {
		return nil, err
	}
	d.transferred = true
	return k, nil
}
