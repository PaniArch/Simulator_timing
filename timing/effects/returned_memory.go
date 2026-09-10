package effects

import (
	"fmt"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/timing/model"
)

// MemoryResult carries actual aligned-word data or store application receipts
// from the timing memory path. Only Mask lanes are complete. A load uses these
// bytes once; a store has already applied them and must never be replayed here.
type MemoryResult struct {
	Mask   uint8
	Data   [4][4]byte
	Errors [4]error
}

// MemoryRequests returns detached functional address/byte-enable requests after
// execute. It does not service memory or mark the scheduler port accepted.
func (a *Adapter) MemoryRequests(token model.Token) ([]isa.MemoryRequest, []isa.PackedLoadRequest, error) {
	if a.failed {
		return nil, nil, fmt.Errorf("effect adapter requires reset")
	}
	if err := a.check(model.Signal{Valid: true, Token: token}); err != nil {
		return nil, nil, err
	}
	i := a.current
	if i.packed != nil {
		u := i.packed.uops[token.Uop]
		if u == nil || !u.executed {
			return nil, nil, fmt.Errorf("packed addresses before execute")
		}
		return nil, append([]isa.PackedLoadRequest(nil), u.requests...), nil
	}
	if i.memory == nil {
		return nil, nil, fmt.Errorf("memory addresses before execute")
	}
	return append([]isa.MemoryRequest(nil), i.memory.requests...), nil, nil
}
func (a *Adapter) AcceptMemoryResult(cycle uint64, token model.Token, result MemoryResult) (model.Response, error) {
	if token.Path != model.LOAD && token.Path != model.STORE {
		return model.Response{}, fmt.Errorf("data result for non-memory path")
	}
	return a.serviceMemory(cycle, token, result.Mask, &result)
}
func (c *Concurrent) MemoryRequests(token model.Token) ([]isa.MemoryRequest, []isa.PackedLoadRequest, error) {
	if c.failed {
		return nil, nil, fmt.Errorf("concurrent effects require reset")
	}
	a, err := c.lookup(token)
	if err != nil {
		return nil, nil, err
	}
	return a.MemoryRequests(token)
}
func (c *Concurrent) AcceptMemoryResult(cycle uint64, token model.Token, result MemoryResult) (r model.Response, err error) {
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
	return a.AcceptMemoryResult(cycle, token, result)
}

// AcceptOrderingResult acknowledges a completed timing-system ordering request.
// The caller must first observe the actual flush response for this token. This
// does not initiate a flush, drain resources, or call the legacy ordering owner.
func (a *Adapter) AcceptOrderingResult(cycle uint64, token model.Token, completionError error) (model.Response, error) {
	return a.AcceptOrderingFragment(cycle, token, token.Mask, completionError)
}

// AcceptOrderingFragment preserves the scheduler partial-response mask.
func (a *Adapter) AcceptOrderingFragment(cycle uint64, token model.Token, mask uint8, completionError error) (model.Response, error) {
	if a.failed {
		return model.Response{}, fmt.Errorf("effect adapter requires reset")
	}
	if token.Path != model.FENCE {
		return model.Response{}, fmt.Errorf("ordering result for non-FENCE path")
	}
	if completionError != nil {
		if err := a.check(model.Signal{Valid: true, Token: token}); err != nil {
			return model.Response{}, err
		}
		a.failed = true
		return model.Response{}, fmt.Errorf("FENCE completion at PC %#x: %w", token.PC, completionError)
	}
	return a.serviceMemory(cycle, token, mask, &MemoryResult{})
}
func (c *Concurrent) AcceptOrderingResult(cycle uint64, token model.Token, completionError error) (r model.Response, err error) {
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
	return a.AcceptOrderingResult(cycle, token, completionError)
}

func (c *Concurrent) AcceptOrderingFragment(cycle uint64, token model.Token, mask uint8, completionError error) (r model.Response, err error) {
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
	return a.AcceptOrderingFragment(cycle, token, mask, completionError)
}
