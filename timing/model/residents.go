package model

// Resident is a detached component-owned observation. Remaining counts local
// arithmetic stages only, never external service latency. Reason describes the
// local readiness condition; it does not claim a downstream arbitration grant.
type Resident struct {
	Token     Token
	Position  int
	Remaining int
	Reason    string
}

func (b *Buffer) Residents() []Resident {
	r := make([]Resident, 0, len(b.queue))
	for index, token := range b.queue {
		reason := "queue-order"
		if index == 0 {
			reason = "awaiting-transfer"
		}
		r = append(r, Resident{Token: token, Position: index, Reason: reason})
	}
	return r
}
func (p *Pipeline) Residents() []Resident {
	r := []Resident{}
	for index, s := range p.stages {
		if s.Valid {
			reason := "execution-latency"
			if index == len(p.stages)-1 {
				reason = "awaiting-transfer"
			}
			r = append(r, Resident{Token: s.Token, Position: index, Remaining: len(p.stages) - 1 - index, Reason: reason})
		}
	}
	return r
}
func (p *STDPath) Residents() []Resident {
	r := p.pipe.Residents()
	tail := p.out.Residents()
	for i := range tail {
		tail[i].Position += p.pipe.Capacity()
	}
	return append(r, tail...)
}
func (d *Divider) Residents() []Resident {
	if !d.loaded.Valid {
		return nil
	}
	reason := "execution-latency"
	if d.remaining == 0 {
		reason = "awaiting-transfer"
	}
	return []Resident{{Token: d.loaded.Token, Remaining: d.remaining, Reason: reason}}
}
func (w *WaitPool) Residents() []Resident {
	r := make([]Resident, 0, len(w.entries))
	for index, entry := range w.entries {
		r = append(r, Resident{Token: entry.Token, Position: index, Reason: "response-coverage"})
	}
	return r
}
func (c *CSR) Residents() []Resident    { return c.out.Residents() }
func (m *Merge) Residents() []Resident  { return m.out.Residents() }
func (c *Commit) Residents() []Resident { return c.out.Residents() }
func (s *Sequencer) Residents() []Resident {
	if !s.active.Valid {
		return nil
	}
	return []Resident{{Token: s.active.Token, Reason: "packed-uop-dispatch"}}
}
