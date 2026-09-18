package runner

import (
	"fmt"
	"vortex.local/simulator/emu/core"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// VX_cta_dispatch: IDLE accepts a CTA independently of free Warp count;
// DISPATCH selects a free wid into warp_fire_r, consumed on the next edge.
// CTA metadata/LMEM and physical Warp ownership have different lifetimes.
type ctaDispatch struct {
	slot         uint32
	selected     *core.WarpMembership
	selectedMask isa.WarpMask
}

type contextWrite struct {
	cycle, generation uint64
	warp              uint8
}

// Frozen VX_cta_dispatch: ceil((4-1)/TID_STEP=2) = two pipeline
// registers, followed by cta_warp_ram's write edge. A read at that edge
// still observes old RAM (RDW_MODE=R); expose the new view afterwards.
func (k *Kernel) commitContextWrites(cycle uint64) {
	remaining := k.contextWrites[:0]
	for _, w := range k.contextWrites {
		if w.cycle > cycle {
			remaining = append(remaining, w)
			continue
		}
		if k.warpGenerations[w.warp] != w.generation {
			k.failed = fmt.Errorf("stale CTA context pipeline write")
			continue
		}
		k.contextReady[w.warp] = true
	}
	k.contextWrites = remaining
}

func (k *Kernel) warpEvent(kind string, slot uint32, m core.WarpMembership) {
	k.event(kind, k.resident[slot].Launch.ID, int(slot), 1<<m.WarpID)
	e := &k.events[len(k.events)-1]
	e.Warp, e.Rank, e.WarpGeneration = m.WarpID, m.Rank, k.warpGenerations[m.WarpID]
}

func (k *Kernel) residency() error {
	for slot, c := range k.resident {
		// VX_cta_dispatch clears slot_valid on the last delayed warp_done,
		// independently of commit/pending/memory tails. Token bindings and
		// physical LMEM leases retain those tails outside the slot table.
		if c != nil && c.Dispatched == k.launch.WarpsPerCTA && c.RetiredRanks == isa.WarpMask((1<<k.launch.WarpsPerCTA)-1) && !k.memory.BarrierPending(uint32(slot)) {
			for _, m := range c.Resident.Members {
				k.warpEvent("warp-released", uint32(slot), m)
			}
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
	if d := k.dispatch; d != nil {
		c := k.resident[d.slot]
		// Selection reads pre-edge state. Never select the registered offer again,
		// nor a wid already dispatched for this CTA (dispatched_warps in RTL).
		candidate := uint8(4)
		for w := uint8(0); w < 4; w++ {
			if d.selectedMask.Active(w) || !k.runner.warpDispatchable(w) {
				continue
			}
			if view, err := k.memory.ViewForWarp(w); err == nil && k.memory.BarrierPending(view.ID) {
				continue
			}
			candidate = w
			break
		}
		if d.selected != nil {
			m := *d.selected
			if err := k.runner.DispatchWarp(m.WarpID, c.Launch.StartupPC, c.Launch.ParameterAddress, m.ActiveMask, !k.used[m.WarpID]); err != nil {
				return err
			}
			k.used[m.WarpID] = true
			k.contextReady[m.WarpID] = false
			k.contextWrites = append(k.contextWrites, contextWrite{k.runner.Cycle() + 2, k.warpGenerations[m.WarpID], m.WarpID})
			c.Dispatched++
			k.warpEvent("warp-dispatched", d.slot, m)
			d.selected = nil
			if c.Dispatched == k.launch.WarpsPerCTA {
				k.dispatch = nil
				return nil // last fire returns FSM to IDLE on this edge
			}
		}
		if candidate == 4 {
			return nil
		}
		if k.warpGenerations[candidate] == ^uint64(0) {
			return fmt.Errorf("Warp generation overflow")
		}
		if view, err := k.memory.ViewForWarp(candidate); err == nil {
			old := k.resident[view.ID]
			for _, member := range old.Resident.Members {
				if member.WarpID == candidate {
					k.warpEvent("warp-released", view.ID, member)
				}
			}
			snapshot, err := k.memory.DetachWarp(candidate)
			if err != nil {
				return err
			}
			old.Resident = snapshot
		}
		snapshot, err := k.memory.BindWarp(d.slot, c.Dispatched, candidate)
		if err != nil {
			return err
		}
		c.Resident = snapshot
		m := snapshot.Members[len(snapshot.Members)-1]
		k.warpGenerations[candidate]++
		owner, err := k.memory.PinLocal(candidate)
		if err != nil {
			return err
		}
		k.localOwners[[2]uint64{uint64(candidate), k.warpGenerations[candidate]}] = owner
		d.selected = &m
		d.selectedMask |= 1 << candidate
		k.warpEvent("warp-selected", d.slot, m)
		return nil
	}
	if k.pending == nil {
		if c, ok := k.walker.Next(); ok {
			k.pending = &c
			k.event("generated", c.ID, -1, 0)
		} else {
			return nil
		}
	}
	if k.retirementWrite {
		return nil
	} // rem_warps_write_r owns the table write port
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
	if k.generations[base] == ^uint64(0) {
		return fmt.Errorf("CTA generation overflow")
	}
	config := c.CoreConfig()
	config.ID = base
	for w := uint8(0); uint32(w) < k.launch.WarpsPerCTA; w++ {
		config.WarpIDs = append(config.WarpIDs, w)
	}
	snapshot, err := k.memory.Reserve(config, base*k.launch.AlignedLocalMemorySize)
	if err != nil {
		return err
	}
	k.generations[base]++
	k.resident[base] = &KernelCTA{Launch: *c, Resident: snapshot}
	k.dispatch = &ctaDispatch{slot: base}
	k.event("admitted", c.ID, int(base), 0)
	k.pending = nil
	k.tail = (base + 1) % k.usable
	return nil
}

// Retirement mirrors warp_done_r -> warp_done_r_dly -> rem_warps write.
// Capture logical ownership at TMC, before a physical wid can be reused. The
// independent quiescence predicate may hold the CTA longer for transport tails.
type warpRetirement struct {
	slot       uint32
	generation uint64
	rank       uint32
}

func (k *Kernel) advanceRetirement(record MultiRecord) error {
	k.retirementWrite = len(k.retirePipe[1]) != 0
	for _, r := range k.retirePipe[1] {
		c := k.resident[r.slot]
		if c == nil || k.generations[r.slot] != r.generation {
			return fmt.Errorf("stale CTA retirement identity")
		}
		c.RetiredRanks |= 1 << r.rank
	}
	k.retirePipe[1], k.retirePipe[0] = k.retirePipe[0], nil
	for _, f := range record.Report.Wakeups {
		if f.Kind == model.FeedbackTMC && f.UpdateMask && f.Mask == 0 {
			view, err := k.memory.ViewForWarp(f.Token.Warp)
			if err != nil {
				return fmt.Errorf("retirement without Warp binding: %w", err)
			}
			k.retirePipe[0] = append(k.retirePipe[0], warpRetirement{view.ID, k.generations[view.ID], view.Rank})
		}
		if f.Kind == model.FeedbackSpawn {
			for w := uint8(0); w < 4; w++ {
				if f.Targets&(1<<w) != 0 {
					if view, err := k.memory.ViewForWarp(w); err == nil {
						k.resident[view.ID].RetiredRanks &^= 1 << view.Rank
					}
				}
			}
		}
	}
	return nil
}
