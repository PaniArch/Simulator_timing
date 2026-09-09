package state

import (
	"fmt"
	"vortex.local/simulator/isa"
)

// InstructionContext is the immutable fetch context of one pipeline token.
// It carries no register values or mutable owner; epoch/residency validation
// remains the caller's responsibility.
type InstructionContext struct {
	WarpID uint8
	PC     uint32
	Mask   isa.LaneMask
}

// WithInstructionContext returns a detached view with the explicitly latched
// instruction PC/mask. Register, CSR and divergence values still come from the
// supplied snapshot at the named read/execute event. The canonical owner is
// never modified. This does not relax NewOperandCapture's legacy checks.
func (s WarpSnapshot) WithInstructionContext(context InstructionContext) (WarpSnapshot, error) {
	if context.WarpID != s.WarpID() || context.PC&3 != 0 || !context.Mask.Valid() || context.Mask == 0 {
		return WarpSnapshot{}, fmt.Errorf("invalid latched instruction context")
	}
	s.pc = context.PC
	s.activeMask = context.Mask
	s.lifecycle = WarpRunning
	return s, nil
}

func NewLatchedOperandCapture(snapshot WarpSnapshot, word uint32, context ReadContext, instruction InstructionContext) (*OperandCapture, error) {
	view, err := snapshot.WithInstructionContext(instruction)
	if err != nil {
		return nil, err
	}
	c, err := NewOperandCapture(view, word, context)
	if err != nil {
		return nil, err
	}
	c.latched = true
	return c, nil
}
