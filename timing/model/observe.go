package model

// ResourceState is an old-edge value snapshot, detached from component state.
// Different resources can hold aliases of one token; do not sum their entries
// to infer instruction concurrency. Context pools are identified separately.
type ResourceState struct {
	ID                  string
	Capacity, Occupancy int
	Output              Signal
	Residents           []Resident
}
type resource interface {
	ID() string
	Capacity() int
	Occupancy() int
	Residents() []Resident
}

func observed(c resource, s Signal) ResourceState {
	return ResourceState{ID: c.ID(), Capacity: c.Capacity(), Occupancy: c.Occupancy(), Output: s, Residents: c.Residents()}
}
func (f *Fetch) Resources() []ResourceState {
	return []ResourceState{observed(f.request, f.request.Output(Signal{})), observed(f.iflush, f.Request()), observed(f.tags, Signal{})}
}
func (i *Issue) Resources() []ResourceState {
	return []ResourceState{observed(i.staging, i.staging.Output(Signal{})), observed(i.out, i.Output(Signal{}))}
}
func (c *Collector) Resources() []ResourceState {
	return []ResourceState{observed(c.first, c.first.Output(Signal{})), observed(c.second, c.second.Output(Signal{})), observed(c.out, c.Output(Signal{}))}
}
func (d *Dispatch) Resources() []ResourceState {
	r := []ResourceState{}
	for i, q := range d.queues {
		s := observed(q, q.Output(Signal{}))
		s.ID += "/" + []string{"alu", "lsu", "sfu", "fpu"}[i]
		r = append(r, s)
	}
	return r
}
func (f *Frontend) Resources() []ResourceState {
	r := []ResourceState{observed(f.schedule, f.schedule.Output(Signal{})), observed(f.ibuf, f.ibuf.Output(Signal{})), observed(f.seq, f.seq.Output(f.ibuf.Output(Signal{})))}
	r = append(r, f.fetch.Resources()...)
	r = append(r, f.issue.Resources()...)
	r = append(r, f.opc.Resources()...)
	r = append(r, f.dispatch.Resources()...)
	return r
}
func (a *ALU) Resources() []ResourceState {
	return []ResourceState{observed(a.integer, a.integer.Output(Signal{})), observed(a.mul, a.mul.Output(Signal{})), observed(a.div, a.div.Output(Signal{})), observed(a.md, a.md.Output()), observed(a.result, a.Output())}
}
func (s *SFU) Resources() []ResourceState {
	return []ResourceState{observed(s.dispatch, s.ExecuteInput()), observed(s.wctl, s.wctl.Output(Signal{})), observed(s.csr, s.csr.Output(Signal{})), observed(s.result, s.result.Output()), observed(s.gather, s.Output())}
}
func (f *STDFPU) Resources() []ResourceState {
	r := []ResourceState{observed(f.tags, Signal{}), observed(f.result, f.Output())}
	for _, p := range f.paths {
		r = append(r, observed(p, p.Output(Signal{})))
	}
	return r
}
func (l *LSU) Resources() []ResourceState {
	return []ResourceState{observed(l.dispatch, l.dispatch.Output(Signal{})), observed(l.request, l.Request()), observed(l.tags, Signal{}), observed(l.load, l.load.Output(Signal{})), observed(l.store, l.store.Output(Signal{})), observed(l.result, l.result.Output()), observed(l.gather, l.Output())}
}
func (c *Commit) Pending() Signal { return c.feedback }
func (a *ALU) Branch() Signal     { return a.branch }
func (s *SFU) Control() Signal    { return s.control }
func (f *STDFPU) Flags() Signal   { return f.flags }
