package effects

import (
	"fmt"
	"sort"
	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/emu/warp"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// Identity names an instruction; packed uops/lane fragments retain independent
// receipts inside its engine. No identity is inferred from canonical PC.
type Identity struct {
	Epoch uint64
	Warp  uint8
	ID    uint64
}

func identity(t model.Token) Identity { return Identity{t.Epoch, t.Warp, t.ID} }

// Concurrent owns in-flight instruction engines and one control frontier per
// explicit Warp owner. Adapter remains the reusable single-instruction receipt
// engine; the map, not a shared current pointer, routes concurrent events.
type Concurrent struct {
	invalidations    []model.Cancellation
	owners           [4]*state.WarpState
	streams          [4]*state.EffectStream
	external         [4]ExternalOwner
	memory           [4]warp.MemoryService
	entries          map[Identity]*Adapter
	lastID           [4]uint64
	epoch, lastCycle uint64
	observed, failed bool
}

func NewConcurrent(owners [4]*state.WarpState, epoch uint64, memory warp.MemoryService, external [4]ExternalOwner) (*Concurrent, error) {
	return NewConcurrentWithMemory(owners, epoch, [4]warp.MemoryService{memory, memory, memory, memory}, external)
}

// NewConcurrentWithMemory binds one data service per Warp. Each instruction
// retains that service until all its memory receipts complete; fetch is owned
// separately by the runner. Residency owners must not retarget live services.
func NewConcurrentWithMemory(owners [4]*state.WarpState, epoch uint64, memory [4]warp.MemoryService, external [4]ExternalOwner) (*Concurrent, error) {
	for _, service := range memory {
		if service == nil {
			return nil, fmt.Errorf("explicit memory owner required for every warp")
		}
	}
	c := &Concurrent{owners: owners, external: external, memory: memory, epoch: epoch, entries: map[Identity]*Adapter{}}
	for w, owner := range owners {
		if owner == nil {
			return nil, fmt.Errorf("four explicit warp owners required")
		}
		s, err := owner.Snapshot()
		if err != nil {
			return nil, err
		}
		if int(s.WarpID()) != w {
			return nil, fmt.Errorf("warp owner/slot mismatch")
		}
		c.streams[w], err = state.NewEffectStream(owner)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}
func (c *Concurrent) InFlight() int                 { return len(c.entries) }
func (c *Concurrent) Begin(token model.Token) error { return c.begin(token, nil) }
func (c *Concurrent) begin(token model.Token, binding *SpawnBinding) error {
	for _, scope := range c.invalidations {
		if scope.Matches(token) {
			return fmt.Errorf("cancelled instruction admission")
		}
	}
	if c.failed || token.Warp >= 4 || token.Epoch != c.epoch || token.ID <= c.lastID[token.Warp] {
		return fmt.Errorf("invalid concurrent instruction admission")
	}
	a, err := New(c.owners[token.Warp], c.epoch, c.external[token.Warp])
	if err != nil {
		return err
	}
	if err = a.BindMemory(c.memory[token.Warp]); err != nil {
		return err
	}
	a.stream = c.streams[token.Warp]
	if binding != nil {
		a.spawnPool = binding.Pool
		for _, target := range binding.Targets {

			if target.WarpID >= 4 || target.Owner != c.owners[target.WarpID] {
				return fmt.Errorf("foreign spawn owner")
			}
		}
		if err = a.BindSpawn(binding.Active, binding.Targets); err != nil {
			return err
		}
	}
	if err = a.Begin(token); err != nil {
		return err
	}
	c.entries[identity(token)] = a
	c.lastID[token.Warp] = token.ID
	return nil
}
func (c *Concurrent) keys() []Identity {
	keys := make([]Identity, 0, len(c.entries))
	for key := range c.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Warp != keys[j].Warp {
			return keys[i].Warp < keys[j].Warp
		}
		return keys[i].ID < keys[j].ID
	})
	return keys
}
func (c *Concurrent) lookup(token model.Token) (*Adapter, error) {
	a := c.entries[identity(token)]
	if a == nil {
		return nil, fmt.Errorf("unknown concurrent instruction identity %+v", identity(token))
	}
	if err := a.check(model.Signal{Valid: true, Token: token}); err != nil {
		return nil, err
	}
	return a, nil
}

// Observe validates the complete report before invoking an engine. All engines
// receive snapshots taken BEFORE any engine applies visible effects this edge.
// Map traversal can therefore neither forward a same-edge WB into a read nor
// select a CSR/control collision priority. FFLAGS and CSR share one old-edge transaction.
func (c *Concurrent) Observe(cycle uint64, r model.CoreReport, contexts [4]state.ReadContext) (err error) {
	if c.failed {
		return fmt.Errorf("concurrent effects require reset")
	}
	defer func() {
		if err != nil {
			c.failed = true
		}
	}()
	if c.observed && cycle <= c.lastCycle {
		return fmt.Errorf("repeated or reordered concurrent edge")
	}
	if r.Branch.Valid && r.Control.Valid && r.Branch.Token.Warp == r.Control.Token.Warp {
		control, lookupErr := c.lookup(r.Control.Token)
		if lookupErr != nil {
			return lookupErr
		}
		// All other WCTL operations retain wstall until feedback. BAR.arrive
		// unlocks at decode and can overlap a younger branch. Its sequential
		// control delivery precedes the branch in the instruction-order walk.
		if control.current.decoded.Barrier != isa.BarrierArrive || r.Control.Token.ID >= r.Branch.Token.ID {
			return fmt.Errorf("overlapping blocking controls violate warp fetch stall")
		}
	}
	reports := map[Identity]model.CoreReport{}
	route := func(s model.Signal, set func(*model.CoreReport)) error {
		if !s.Valid {
			return nil
		}
		if _, e := c.lookup(s.Token); e != nil {
			return e
		}
		key := identity(s.Token)
		part := reports[key]
		set(&part)
		reports[key] = part
		return nil
	}
	for n, s := range []model.Signal{r.Read, r.Writeback, r.PendingRelease, r.Branch, r.Control, r.Flags, r.CSRRequest} {
		if err = route(s, func(p *model.CoreReport) {
			switch n {
			case 0:
				p.Read = s
			case 1:
				p.Writeback = s
			case 2:
				p.PendingRelease = s
			case 3:
				p.Branch = s
			case 4:
				p.Control = s
			case 5:
				p.Flags = s
			case 6:
				p.CSRRequest = s
				p.CSRRequestWindow = r.CSRRequestWindow
			}
		}); err != nil {
			return err
		}
	}
	for class, s := range r.Executed {
		if err = route(s, func(p *model.CoreReport) { p.Executed[class] = s }); err != nil {
			return err
		}
	}
	if r.MemoryAccepted {
		if !r.MemoryRequest.Valid {
			return fmt.Errorf("memory acceptance without request")
		}
		if err = route(r.MemoryRequest, func(p *model.CoreReport) { p.MemoryRequest = r.MemoryRequest; p.MemoryAccepted = true }); err != nil {
			return err
		}
	}
	if r.MemoryResponse.Valid {
		rsp := r.MemoryResponse
		key := Identity{rsp.Epoch, rsp.Warp, rsp.ID}
		a := c.entries[key]
		if a == nil {
			return fmt.Errorf("memory response without in-flight identity")
		}
		part, e := a.responsePart(rsp)
		if e != nil {
			return e
		}
		if r.MemoryResponseReady && part.accepted {
			return fmt.Errorf("duplicate concurrent response acceptance")
		}
		report := reports[key]
		report.MemoryResponse = rsp
		report.MemoryResponseReady = r.MemoryResponseReady
		reports[key] = report
	}
	keys := c.keys()
	for _, key := range keys {
		part := reports[key]
		part.Scheduler, part.Scheduled = r.Scheduler, r.Scheduled
		reports[key] = part
		if err = c.entries[key].validateReport(cycle, reports[key]); err != nil {
			return err
		}
	}
	// Copy pointer-bearing external context before any callback can mutate an
	// external owner; each engine must see the same old-edge view.
	for w := range contexts {
		if contexts[w].Bounds != nil {
			bounds := *contexts[w].Bounds
			contexts[w].Bounds = &bounds
		}
		if contexts[w].BarrierPhases != nil {
			phases := *contexts[w].BarrierPhases
			contexts[w].BarrierPhases = &phases
		}
	}
	var snapshots [4]state.WarpSnapshot
	for w, owner := range c.owners {
		snapshots[w], err = owner.Snapshot()
		if err != nil {
			return err
		}
	}
	// Trap CSR operands resolve at the scheduler feedback edge, before any
	// same-edge software CSR or other owner mutation.
	var trapKey Identity
	hasTrap := false
	if r.Branch.Valid {
		trapKey = identity(r.Branch.Token)
		a := c.entries[trapKey]
		if a.isTrap() {
			hasTrap = true
			if err = a.refreshTrap(cycle, snapshots[trapKey.Warp]); err != nil {
				return err
			}
		}
	}
	pairFlags := r.Flags.Valid && r.CSRRequest.Valid && r.Flags.Token.Warp == r.CSRRequest.Token.Warp
	pairTrap := hasTrap && r.CSRRequest.Valid && trapKey.Warp == r.CSRRequest.Token.Warp
	if pairTrap && r.Control.Valid && r.Control.Token.Warp == r.Branch.Token.Warp {
		// A prior nonblocking BAR.arrive may share this edge with CSR and trap.
		// Deliver its external event before the newer trap advances the control
		// frontier. All evaluations still use the snapshots captured above.
		barKey := identity(r.Control.Token)
		if err = c.entries[barKey].observeAt(cycle, reports[barKey], contexts[barKey.Warp], snapshots[barKey.Warp]); err != nil {
			return err
		}
		var remaining []Identity
		for _, key := range keys {
			if key != barKey {
				remaining = append(remaining, key)
			}
		}
		keys = remaining
	}
	if pairFlags || pairTrap {
		csrKey := identity(r.CSRRequest.Token)
		csr := c.entries[csrKey]
		if pairFlags {
			flag := c.entries[identity(r.Flags.Token)]
			if flag.current.delivery == nil {
				return fmt.Errorf("flags before execution")
			}
			csr.csrFlags = flag.current.delivery
		}
		if pairTrap {
			csr.csrTrap = c.entries[trapKey].current.delivery
		}
		defer func() { csr.csrFlags, csr.csrTrap = nil, nil }()
		if err = csr.observeAt(cycle, reports[csrKey], contexts[csrKey.Warp], snapshots[csrKey.Warp]); err != nil {
			return err
		}
		if pairFlags {
			key := identity(r.Flags.Token)
			part := reports[key]
			part.Flags = model.Signal{} // joint transaction consumed this receipt
			reports[key] = part
		}
		if pairTrap {
			part := reports[trapKey]
			part.Branch = model.Signal{} // joint transaction consumed this receipt
			reports[trapKey] = part
		}
		var remaining []Identity
		for _, key := range keys {
			if key != csrKey {
				remaining = append(remaining, key)
			}
		}
		keys = remaining
	}

	for _, key := range keys {
		a := c.entries[key]
		activating := a.current.spawnAt != nil && cycle >= *a.current.spawnAt && (!r.Scheduled || r.Scheduler.SingleActive)
		if err = a.observeAt(cycle, reports[key], contexts[key.Warp], snapshots[key.Warp]); err != nil {
			return err
		}
		if activating {
			targets, err := a.selectedSpawnTargets()
			if err != nil {
				return err
			}
			for _, target := range targets {
				c.streams[target.WarpID].RecordActivation(c.lastID[target.WarpID])
			}
		}
	}
	c.observed = true
	c.lastCycle = cycle
	return nil
}
func (c *Concurrent) Service(cycle uint64, token model.Token, mask uint8) (r model.Response, err error) {
	if c.failed {
		return r, fmt.Errorf("concurrent effects require reset")
	}
	defer func() {
		if err != nil {
			c.failed = true
		}
	}()
	a, err := c.lookup(token)
	if err != nil {
		return r, err
	}
	return a.Service(cycle, token, mask)
}

// Reap releases only individually completed receipts and service tails. It
// never requires the Core to become idle, and returns detached identities.
func (c *Concurrent) Reap() ([]model.Token, error) {
	if c.failed {
		return nil, fmt.Errorf("concurrent effects require reset")
	}
	var done []model.Token
	for _, key := range c.keys() {
		a := c.entries[key]
		i := a.current
		if !i.pending || i.spawnAt != nil || i.memory != nil && !a.memoryFinished() || i.packed != nil && !a.packedFinished() {
			continue
		}
		token := i.token
		if err := a.Finish(); err != nil {
			return nil, err
		}
		delete(c.entries, key)
		done = append(done, token)
	}
	return done, nil
}
func (c *Concurrent) ControlAllowed(signal model.Signal, context state.ReadContext) bool {
	if c.failed {
		return false
	}
	if !signal.Valid {
		return true
	}
	a, err := c.lookup(signal.Token)
	return err == nil && a.ControlAllowed(context)
}
func (c *Concurrent) Reset(epoch uint64) error {
	if epoch <= c.epoch {
		return fmt.Errorf("epoch must increase")
	}
	next, err := NewConcurrentWithMemory(c.owners, epoch, c.memory, c.external)
	if err != nil {
		return err
	}
	for _, a := range c.entries {
		cancelEngine(a)
	}
	next.lastCycle, next.observed = c.lastCycle, c.observed
	*c = *next
	return nil
}
func cancelEngine(a *Adapter) {
	i := a.current
	if i == nil {
		return
	}
	if i.delivery != nil {
		i.delivery.Cancel()
	}
	if i.memory != nil {
		if i.memory.control != nil {
			i.memory.control.Cancel()
		}
		for _, p := range i.memory.parts {
			p.delivery.Cancel()
		}
	}
	if i.packed != nil {
		if i.packed.control != nil {
			i.packed.control.Cancel()
		}
		for _, u := range i.packed.uops {
			for _, p := range u.parts {
				p.delivery.Cancel()
			}
		}
	}
}
