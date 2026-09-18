package runner

import (
	"fmt"
	"sync/atomic"
	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/effects"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/model"
)

// Pure observation namespace: never consulted by scheduling or memory routing.
var nextTraceDevice atomic.Uint64

// Kernel owns a resumable launch, CTA residency and the actual timing runner.
// Program, parameter and output bytes remain in the caller's backing memory.
type Kernel struct {
	deviceID                uint64
	launchID, startCycle    uint64
	transferred             bool
	transferredStatus       *KernelStatus
	backing                 device.BackingMemory
	options                 Options
	visibilityID            uint64
	generations             [4]uint64
	warpGenerations         [4]uint64
	tokenBindings           map[uint64]WarpBinding
	localOwners             map[[2]uint64]warp.AtomicMemoryService
	bindingRefs             map[[2]uint64]int
	dispatch                *ctaDispatch
	retirePipe              [2][]warpRetirement
	retirementWrite         bool
	visibilitySent, visible bool

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
	started          bool // KMU running is registered from start, before first valid
	hardwareDone     bool
	hardwareEndCycle uint64
	contextReady     [4]bool
	contextWrites    []contextWrite
}
type KernelCTA struct {
	// Detached lifecycle observations; StoppedWarps describes canonical TMC state,
	// while MultiRecord.Warps.Active reports the registered fetch scheduler state.
	Generation                 uint64
	Dispatched                 uint32
	RetiredRanks               isa.WarpMask
	StoppedWarps               isa.WarpMask
	MemoryPending, Reclaimable bool
	Launch                     device.CTA
	Resident                   core.CTASnapshot
}

// KernelStatus reports device-continuous edges. LaunchCycles is elapsed time
// since StartCycle, including any explicit post-execution memory operations.
// Runtime snapshots it at execution completion before separately counting flush.
type KernelStatus struct {
	RTLTimingIssue                string `json:"rtl_timing_issue,omitempty"`
	DeviceID                      uint64
	MemoryDrained, BackingVisible bool

	Cycle                              uint64
	LaunchID, StartCycle, LaunchCycles uint64
	Generated, Completed               uint32
	Resident                           []KernelCTA
	Waiting                            bool
	Complete                           bool
	HardwareComplete                   bool
	HardwareEndCycle, HardwareCycles   uint64
}

func NewKernel(input device.LaunchState, memory device.BackingMemory, options Options) (*Kernel, error) {
	if memory == nil {
		return nil, fmt.Errorf("kernel requires loaded backing memory")
	}
	return newKernel(input, memory, options, nil, nil)
}

func newKernel(input device.LaunchState, memory device.BackingMemory, options Options, previous *Kernel, powered *PoweredDevice) (*Kernel, error) {
	launch, err := device.ValidateLaunch(input)
	if err != nil {
		return nil, err
	}
	walker, err := device.NewGridWalker(launch)
	if err != nil {
		return nil, err
	}
	k := &Kernel{launchID: 1, backing: memory, options: options, barrierEvents: make(map[BarrierEvent]bool), launch: launch, walker: walker, memory: core.NewResidencyMemory(), usable: 4}
	k.tokenBindings = make(map[uint64]WarpBinding)
	k.localOwners = make(map[[2]uint64]warp.AtomicMemoryService)
	k.bindingRefs = make(map[[2]uint64]int)
	if launch.AlignedLocalMemorySize != 0 {
		k.usable = min(uint32(4), uint32(16384)/launch.AlignedLocalMemorySize)
	}
	if previous != nil {
		k.launchID = previous.launchID + 1
		k.startCycle = previous.runner.Cycle()
		// VX_cta_dispatch.tail_r survives a context/launch change. Only reset
		// clears it; base_tail wraps it when the new LMEM stride reduces capacity.
		k.tail = previous.tail
		if k.tail >= k.usable {
			k.tail = 0
		}
	}
	var owners [4]*state.WarpState
	opts := MultiOptions{Options: options}
	opts.External = k.control
	config, err := memsys.DefaultConfig()
	if err != nil {
		return nil, err
	}
	if options.MemoryConfig != nil {
		config = *options.MemoryConfig
	}
	opts.MemorySystem = &MemorySystemOptions{Config: config, Backend: options.MemoryBackend}
	if previous != nil {
		opts.MemorySystem.reuse = previous.runner.hierarchy
	}
	if powered != nil {
		opts.MemorySystem.reuse = powered.memory
	}
	opts.MemorySystem.Bind = func(t model.Token) memsys.Identity {
		b := k.bindToken(t)
		if !b.Valid {
			return memsys.Identity{}
		}
		return memsys.Identity{Kernel: k.launchID, CTA: uint64(b.Slot), WarpGeneration: b.WarpGeneration}
	}
	opts.MemorySystem.LocalOwner = func(id memsys.Identity) (warp.AtomicMemoryService, error) {
		b := k.tokenBindings[id.Token]
		if id.Kernel != k.launchID || id.CTA >= 4 || id.Warp >= 4 || !b.Valid || id.Warp != uint32(b.PhysicalWarp) || id.CTA != uint64(b.Slot) || id.WarpGeneration != b.WarpGeneration {
			return nil, fmt.Errorf("stale kernel local residency")
		}
		owner := k.localOwners[[2]uint64{uint64(id.Warp), id.WarpGeneration}]
		if owner == nil {
			return nil, fmt.Errorf("local route requires atomic owner")
		}
		return owner, nil
	}
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
			if member.WarpID != token.Warp && member.Rank < cta.Dispatched {
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
					// Context values are computed by the existing RTL-width owner,
					// but are not exposed before the TID pipeline writes warp RAM.
					if !k.contextReady[member.WarpID] {
						continue
					}
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
	if powered != nil {
		k.runner.clock = powered.clock
		k.startCycle = powered.clock.Cycle()
		k.runner.hierarchy.bind = opts.MemorySystem.Bind
		k.runner.hierarchy.localOwner = opts.MemorySystem.LocalOwner
	}
	if previous != nil {
		if err := k.runner.core.ContinueCounters(previous.runner.core); err != nil {
			return nil, err
		}
		k.deviceID = previous.deviceID
		k.runner.clock = previous.runner.clock
		k.runner.controlSequence = previous.runner.controlSequence
		k.runner.hierarchy.bind = opts.MemorySystem.Bind
		k.runner.hierarchy.localOwner = opts.MemorySystem.LocalOwner
		status := previous.Status()
		previous.transferredStatus = &status
		previous.transferred = true
		k.runner.launchIdentity = k.launchID
	}
	if previous == nil {
		k.deviceID = nextTraceDevice.Add(1)
	}
	return k, nil
}
func (k *Kernel) Status() KernelStatus {
	if k.transferredStatus != nil {
		return *k.transferredStatus
	}
	s := KernelStatus{DeviceID: k.deviceID, LaunchID: k.launchID, StartCycle: k.startCycle, LaunchCycles: k.runner.Cycle() - k.startCycle, MemoryDrained: k.runner.hierarchy.drained(), BackingVisible: k.visible, Cycle: k.runner.Cycle(), Generated: k.launch.TotalCTAs - k.walker.Remaining(), Completed: k.completed, Waiting: k.pending != nil || k.dispatch != nil}
	for slot, c := range k.resident {
		if c != nil {
			copy := k.observeCTA(slot, c)
			copy.Resident.Members = append([]core.WarpMembership(nil), c.Resident.Members...)
			s.Resident = append(s.Resident, copy)
		}
	}
	s.Complete = k.executionComplete()
	s.RTLTimingIssue = k.runner.hierarchy.system.RTLTimingIssue()
	s.HardwareComplete, s.HardwareEndCycle = k.hardwareDone, k.hardwareEndCycle
	if k.hardwareDone {
		s.HardwareCycles = k.hardwareEndCycle - k.startCycle
	}
	return s
}

// executionComplete reads the existing lifecycle owners, without building the
// public diagnostic snapshot. Execution completion is not backing visibility or
// a host visibility operation; slot release and final tail checks are independent.
func (k *Kernel) executionComplete() bool {
	if k.transferredStatus != nil {
		return k.transferredStatus.Complete
	}
	if !k.hardwareDone || k.failed != nil || k.retirementWrite || len(k.retirePipe[0]) != 0 || len(k.retirePipe[1]) != 0 || k.walker.Remaining() != 0 || k.pending != nil || len(k.barrierEvents) != 0 || k.barrierReleases != 0 {
		return false
	}
	for _, c := range k.resident {
		if c != nil {
			return false
		}
	}
	// Releasing the hardware CTA slot does not retire its old pipeline or
	// memory work. The independent runner completion condition keeps all
	// outstanding receipts alive after the residency table becomes empty.
	return k.runner.stopped
}

// Run advances at most budget actual pipeline edges. Budget exhaustion is
// resumable. Allocation waits leave all already-resident Warps running.
func (k *Kernel) Run(budget uint64, observe func(MultiRecord)) error {
	if k.transferred {
		return fmt.Errorf("kernel memory ownership transferred")
	}
	if k.runner.cacheFlush != nil {
		return fmt.Errorf("finish FlushCaches before Run")
	}
	if k.failed != nil {
		return k.failed
	}
	for n := uint64(0); n < budget; n++ {
		dispatching := k.dispatch != nil
		// VX_kmu.running samples start on this edge; valid can reach CTA only
		// on the following edge. Cache reset progress remains concurrent.
		starting := !k.started
		if !starting {
			if err := k.residency(); err != nil {
				k.failed = err
				return err
			}
		}
		// VX_cta_dispatch.busy = old DISPATCH state || kmu_bus_if_fire.
		// Include the last Warp fire even though residency just returned to IDLE.
		k.runner.dispatchBusy = dispatching || k.dispatch != nil
		if k.executionComplete() {
			return nil
		}
		// The CTA dispatcher remains clocked even while no Warp is active.
		k.runner.stopped = false
		if err := k.runner.run(1, observe != nil, func(record MultiRecord) {
			if record.Report.InstructionAccepted && record.Report.Offered.Valid {
				k.bindToken(record.Report.Offered.Token)
			}
			if err := k.advanceRetirement(record); err != nil {
				k.failed = err
			}
			if err := k.releaseBarriers(); err != nil {
				k.failed = err
			}
			k.commitContextWrites(record.Cycle)
			if !k.hardwareDone && k.walker.Remaining() == 0 && k.pending == nil && k.dispatch == nil && !k.runner.core.SchedulerBusy() && k.runner.core.LSUSchedulerDrained() && k.runner.hierarchy.system.MemUnitEmpty() {
				k.hardwareDone, k.hardwareEndCycle = true, record.Cycle+1
			}
			if observe != nil {
				record.DeviceID, record.LaunchID = k.deviceID, k.launchID
				record.Bindings = k.traceBindings()
				record.TokenBindings = make(map[uint64]WarpBinding, len(k.tokenBindings))
				for id, b := range k.tokenBindings {
					record.TokenBindings[id] = b
				}
			}
			for _, tokens := range [][]model.Token{record.Finished, record.Cancelled} {
				for _, token := range tokens {
					if b, ok := k.tokenBindings[token.ID]; ok {
						k.bindingRefs[[2]uint64{uint64(b.PhysicalWarp), b.WarpGeneration}]--
						delete(k.tokenBindings, token.ID)
					}
				}
			}
			for key := range k.localOwners {
				if key[1] != k.warpGenerations[key[0]] && k.bindingRefs[key] == 0 {
					delete(k.localOwners, key)
					delete(k.bindingRefs, key)
				}
			}
			// Deliver only after consuming lifecycle identities. Observers may
			// mutate detached slices/maps; they cannot alter ownership cleanup.
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
		k.started = true
	}
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

// MakeVisible explicitly writes dirty D-cache data to the original backing.
// It is resumable and only legal after execution/CTA reclamation completes.
// These post-execution memory edges do not execute or retire instructions.
func (k *Kernel) MakeVisible(budget uint64) (bool, error) {
	if k.transferred {
		return false, fmt.Errorf("kernel memory ownership transferred")
	}
	if k.runner.cacheFlush != nil {
		return false, fmt.Errorf("finish FlushCaches before MakeVisible")
	}
	if k.failed != nil {
		return false, k.failed
	}
	if !k.executionComplete() {
		return false, fmt.Errorf("visibility requires completed kernel execution")
	}
	if k.visible {
		return true, nil
	}
	if k.visibilityID == 0 {
		id, err := k.runner.nextControlTransaction()
		if err != nil {
			return false, err
		}
		k.visibilityID = id
	}
	err := k.runner.clock.Run(budget, func(cycle uint64) (bool, error) {
		e, err := k.runner.hierarchy.system.Step(cycle, memsys.SystemInput{FetchReady: true, MemoryReady: true, DataFlush: memsys.FlushOffer{Valid: !k.visibilitySent, Identity: memsys.Identity{Kernel: k.launchID, Transaction: k.visibilityID}, Tag: k.visibilityID}, DataFlushReady: true})
		if err != nil {
			return false, err
		}
		if err := k.runner.core.ClockIdleCounters(); err != nil {
			return false, err
		}
		k.visibilitySent = k.visibilitySent || e.DataFlushAccepted
		for _, r := range e.WritebackErrors {
			if r.Err != nil {
				return false, r.Err
			}
		}
		if e.DataFlush.Delivered {
			if e.DataFlush.Err != nil {
				return false, e.DataFlush.Err
			}
			k.visible = true
		}
		return k.visible, nil
	})
	if err != nil {
		k.failed = err
	}
	return k.visible, err
}

// NextLaunch transfers the device memory hierarchy and clock to a fresh execution
// context. It does not reset, flush, invalidate or make host writes coherent.
// Callers must serialize ownership; native runtime additionally requires a real
// D/I flush before host access. Failed construction leaves this Kernel usable.
func (k *Kernel) NextLaunch(input device.LaunchState) (*Kernel, error) {
	if k.transferred || k.failed != nil || k.runner.failed || !k.executionComplete() ||
		!k.runner.hierarchy.drained() || k.runner.cacheFlush != nil || (k.visibilityID != 0 && !k.visible) {
		return nil, fmt.Errorf("next launch requires healthy completed drained memory owner")
	}
	if k.launchID == ^uint64(0) {
		return nil, fmt.Errorf("launch identity overflow")
	}
	nextCycle, err := k.runner.hierarchy.system.NextCycle()
	if err != nil {
		return nil, err
	}
	if nextCycle != k.runner.Cycle() {
		return nil, fmt.Errorf("device clock and memory edge disagree")
	}
	return newKernel(input, k.backing, k.options, k, nil)
}
