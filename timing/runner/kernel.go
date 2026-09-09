package runner

import (
	"fmt"
	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/model"
)

// Kernel owns a resumable launch, CTA residency and the actual timing runner.
// Program, parameter and output bytes remain in the caller's backing memory.
type Kernel struct {
	events           []KernelEvent
	barrierEvents    map[BarrierEvent]bool
	nextBarrierEvent uint64
	barrierReleases  isa.WarpMask
	launch           device.LaunchState
	walker           *device.GridWalker
	memory           *core.ResidencyMemory
	runner           *MultiRunner
	resident         [4]*KernelCTA
	used             [4]bool
	pending          *device.CTA
	tail, usable     uint32
	completed        uint32
	failed           error
}
type KernelCTA struct {
	Launch   device.CTA
	Resident core.CTASnapshot
}
type KernelStatus struct {
	Cycle                uint64
	Generated, Completed uint32
	Resident             []KernelCTA
	Waiting              bool
	Complete             bool
}

func NewKernel(input device.LaunchState, memory device.BackingMemory, options Options) (*Kernel, error) {
	if memory == nil {
		return nil, fmt.Errorf("kernel requires loaded backing memory")
	}
	launch, err := device.ValidateLaunch(input)
	if err != nil {
		return nil, err
	}
	walker, err := device.NewGridWalker(launch)
	if err != nil {
		return nil, err
	}
	k := &Kernel{barrierEvents: make(map[BarrierEvent]bool), launch: launch, walker: walker, memory: core.NewResidencyMemory(), usable: 4}
	if launch.AlignedLocalMemorySize != 0 {
		k.usable = min(uint32(4), uint32(16384)/launch.AlignedLocalMemorySize)
	}
	var owners [4]*state.WarpState
	opts := MultiOptions{Options: options}
	opts.External = k.control
	for w := uint8(0); w < 4; w++ {
		initial := state.WarpInitial{Topology: state.FrozenTopology(), WarpID: w, Lifecycle: state.WarpInactive}
		for lane := uint8(0); lane < 4; lane++ {
			initial.Lanes = append(initial.Lanes, state.LaneInitial{ID: lane})
		}
		owners[w], err = state.NewWarp(initial)
		if err != nil {
			return nil, err
		}
		opts.DataMemory[w], err = k.memory.Route(w, memory)
		if err != nil {
			return nil, err
		}
	}
	opts.Spawn = func(token model.Token) (effects.SpawnBinding, error) {
		view, err := k.memory.ViewForWarp(token.Warp)
		if err != nil {
			return effects.SpawnBinding{}, err
		}
		cta := k.resident[view.ID]
		binding := effects.SpawnBinding{Pool: true, Active: isa.WarpMask(k.runner.core.ActiveWarps())}
		for _, member := range cta.Resident.Members {
			if member.WarpID != token.Warp {
				snapshot, err := owners[member.WarpID].Snapshot()
				if err != nil {
					return binding, err
				}
				binding.Targets = append(binding.Targets, state.WarpSpawnTarget{WarpID: member.WarpID, Owner: owners[member.WarpID], Expected: snapshot})
			}
		}
		return binding, nil
	}
	opts.Contexts = func() (contexts [4]state.ReadContext) {
		for _, cta := range k.resident {
			if cta != nil {
				for _, member := range cta.Resident.Members {
					contexts[member.WarpID].CTA, _ = k.memory.ViewForWarp(member.WarpID)
					phases, _ := k.memory.Barriers().PhaseView(cta.Resident.ID)
					contexts[member.WarpID].BarrierPhases = &phases
				}
			}
		}
		return
	}
	k.runner, err = NewMulti(owners, memory, opts)
	if err != nil {
		return nil, err
	}
	return k, nil
}
func (k *Kernel) Status() KernelStatus {
	s := KernelStatus{Cycle: k.runner.Cycle(), Generated: k.launch.TotalCTAs - k.walker.Remaining(), Completed: k.completed, Waiting: k.pending != nil}
	for _, c := range k.resident {
		if c != nil {
			copy := *c
			copy.Resident.Members = append([]core.WarpMembership(nil), c.Resident.Members...)
			s.Resident = append(s.Resident, copy)
		}
	}
	s.Complete = k.failed == nil && k.walker.Remaining() == 0 && k.pending == nil && len(s.Resident) == 0 && len(k.barrierEvents) == 0 && k.barrierReleases == 0
	return s
}

// Run advances at most budget actual pipeline edges. Budget exhaustion is
// resumable. Allocation waits leave all already-resident Warps running.
func (k *Kernel) Run(budget uint64, observe func(MultiRecord)) error {
	if k.failed != nil {
		return k.failed
	}
	for n := uint64(0); n < budget; n++ {
		if err := k.residency(); err != nil {
			k.failed = err
			return err
		}
		if k.Status().Complete {
			return nil
		}
		if err := k.runner.Run(1, func(record MultiRecord) {
			if err := k.releaseBarriers(); err != nil {
				k.failed = err
			}
			if observe != nil {
				observe(record)
			}
		}); err != nil {
			k.failed = err
			return err
		}
		if k.failed != nil {
			return k.failed
		}
	}
	return nil
}
func (k *Kernel) residency() error {
	for slot, c := range k.resident {
		if c != nil {
			done := !k.memory.BarrierPending(uint32(slot))
			for _, m := range c.Resident.Members {
				done = done && k.runner.WarpQuiescent(m.WarpID)
			}
			if done {
				if err := k.memory.Release(uint32(slot)); err != nil {
					return err
				}
				var mask isa.WarpMask
				for _, m := range c.Resident.Members {
					mask |= 1 << m.WarpID
				}
				k.event("reclaimed", c.Launch.ID, slot, mask)
				k.resident[slot] = nil
				k.completed++
			}
		}
	}
	if k.pending == nil {
		if c, ok := k.walker.Next(); ok {
			k.pending = &c
			k.event("generated", c.ID, -1, 0)
		} else {
			return nil
		}
	}
	c := k.pending
	base := k.tail
	if c.IsFirstOfCluster && base+c.ClusterSize > k.usable {
		base = 0
	}
	width := uint32(1)
	if c.IsFirstOfCluster {
		width = c.ClusterSize
	}
	for i := uint32(0); i < width; i++ {
		if k.resident[base+i] != nil {
			return nil
		}
	}
	var ids []uint8
	for w := uint8(0); w < 4 && uint32(len(ids)) < k.launch.WarpsPerCTA; w++ {
		reserved := false
		for _, old := range k.resident {
			if old != nil {
				for _, m := range old.Resident.Members {
					reserved = reserved || m.WarpID == w
				}
			}
		}
		if !reserved && k.runner.WarpQuiescent(w) {
			ids = append(ids, w)
		}
	}
	if uint32(len(ids)) != k.launch.WarpsPerCTA {
		return nil
	}
	config := c.CoreConfig()
	config.ID = base
	config.WarpIDs = ids
	snapshot, err := k.memory.Admit(config, base*k.launch.AlignedLocalMemorySize)
	if err != nil {
		return err
	}
	k.resident[base] = &KernelCTA{Launch: *c, Resident: snapshot}
	for _, m := range snapshot.Members {
		if err := k.runner.DispatchWarp(m.WarpID, c.StartupPC, c.ParameterAddress, m.ActiveMask, !k.used[m.WarpID]); err != nil {
			return err
		}
		k.used[m.WarpID] = true
	}
	var mask isa.WarpMask
	for _, m := range snapshot.Members {
		mask |= 1 << m.WarpID
	}
	k.event("admitted", c.ID, int(base), mask)
	k.pending = nil
	k.tail = (base + 1) % k.usable
	return nil
}

// control receives the single WCTL delivery at its feedback edge. Wait tokens
// are registered by MultiRunner later on that edge; releases are therefore
// queued only in the post-edge callback for consumption on the following edge.
func (k *Kernel) control(e isa.InstructionEffects) error {
	for _, effect := range e.Barriers {
		stage, err := k.memory.Barriers().Stage(effect)
		if err != nil {
			return err
		}
		result := stage.Result()
		if effect.Event && uint64(effect.ExpectCount) > ^uint64(0)-k.nextBarrierEvent {
			return fmt.Errorf("barrier event identity exhausted")
		}
		if err := stage.Commit(); err != nil {
			return err
		}
		if effect.Event {
			for n := uint8(0); n < effect.ExpectCount; n++ {
				k.nextBarrierEvent++
				ticket := BarrierEvent{CTA: k.resident[result.Key.CTAID].Launch.ID, Slot: result.Key.CTAID, AddressWarp: result.Key.AddressWarp, BarrierID: result.Key.ID, Sequence: k.nextBarrierEvent, owner: k}
				k.barrierEvents[ticket] = true
			}
		}
		k.barrierReleases |= result.Releases
		if effect.Wait && !result.Block {
			k.barrierReleases |= 1 << effect.WarpID
		}
	}
	return nil
}
func (k *Kernel) releaseBarriers() error {
	for w := uint8(0); w < 4; w++ {
		if k.barrierReleases.Active(w) {
			token, ok := k.runner.blocked[w]
			if !ok {
				return fmt.Errorf("barrier release without registered waiter for Warp %d", w)
			}
			if err := k.runner.Release(token); err != nil {
				return err
			}
		}
	}
	k.barrierReleases = 0
	return nil
}
