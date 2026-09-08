package state

import (
	"fmt"
	"vortex.local/simulator/isa"
)

// OperandCapture is detached instruction context, never a candidate WarpState
// replacement. Source positions are sampled independently at bank-read events;
// two occurrences of one register can therefore retain different sampled values.
type OperandCapture struct {
	snapshot WarpSnapshot
	decoded  isa.Decoded
	context  ReadContext
	values   [3]isa.LaneValues
	read     uint8
}

func NewOperandCapture(snapshot WarpSnapshot, word uint32, context ReadContext) (*OperandCapture, error) {
	decoded, err := isa.Decode(word)
	if err != nil {
		return nil, err
	}
	bounds, err := validateContext(context)
	if err != nil {
		return nil, err
	}
	context.Bounds = bounds
	if context.BarrierPhases != nil {
		phases := *context.BarrierPhases
		context.BarrierPhases = &phases
	}
	c := &OperandCapture{snapshot: snapshot, decoded: decoded, context: context}
	for i, r := range decoded.Sources {
		if r.File == isa.Integer && r.Index == 0 {
			c.read |= 1 << i
		}
	}
	return c, nil
}
func (c *OperandCapture) PC() uint32         { return c.snapshot.PC() }
func (c *OperandCapture) Mask() isa.LaneMask { return c.snapshot.ActiveMask() }
func (c *OperandCapture) ReadMask() uint8    { return c.read }
func (c *OperandCapture) Complete() bool     { return c.read == uint8((1<<len(c.decoded.Sources))-1) }
func (c *OperandCapture) Read(snapshot WarpSnapshot, positions uint8) error {
	if snapshot.WarpID() != c.snapshot.WarpID() || snapshot.PC() != c.PC() || snapshot.ActiveMask() != c.Mask() {
		return fmt.Errorf("operand context changed before read")
	}
	allowed := uint8((1 << len(c.decoded.Sources)) - 1)
	if positions & ^allowed != 0 || positions&c.read != 0 {
		return fmt.Errorf("invalid or duplicate operand read")
	}
	values := c.values
	for i, r := range c.decoded.Sources {
		if positions&(1<<i) != 0 {
			v, err := snapshot.ReadRegister(r)
			if err != nil {
				return err
			}
			values[i] = v
		}
	}
	c.values = values
	c.read |= positions
	return nil
}
func (c *OperandCapture) Evaluate() (isa.InstructionEffects, error) {
	return c.EvaluateAt(c.snapshot, c.context)
}

// EvaluateAt allows CSR/FRM/control owners to supply context at their actual
// execution event while preserving the captured source operands and PC/mask.
// Callers must select that event explicitly; this method applies no effects.
func (c *OperandCapture) EvaluateAt(contextSnapshot WarpSnapshot, context ReadContext) (isa.InstructionEffects, error) {
	if !c.Complete() {
		return isa.InstructionEffects{}, fmt.Errorf("operands are incomplete")
	}
	if contextSnapshot.WarpID() != c.snapshot.WarpID() || contextSnapshot.PC() != c.PC() || contextSnapshot.ActiveMask() != c.Mask() {
		return isa.InstructionEffects{}, fmt.Errorf("execution context does not match captured instruction")
	}
	return contextSnapshot.evaluateOperands(c.decoded, context, c.values)
}
