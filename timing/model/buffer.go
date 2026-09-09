// Package model implements transient cycle resources. It owns no architectural
// registers, PC, CSR or memory bytes.
package model

import "vortex.local/simulator/timing"

// Token is a value-only identity carried through resources. Functional payloads
// can be associated with this identity without giving buffers mutable owners.
type Token struct {
	Branch bool // INT branch/trap sideband, decoded without functional evaluation
	ID     uint64
	Epoch  uint64
	Warp   uint8
	Uop    uint8
	PC     uint32
	Word   uint32
	// Sources use RTL register IDs (GPR 0..31, FPR 32..63). Used marks
	// actual reads; FPR f0 is not the suppressed integer zero register.
	Destination               uint8 // RTL register ID; Writeback suppresses integer x0
	Writeback                 bool
	ReadSpecial, WriteSpecial uint8 // bit 0 FFLAGS, bit 1 FRM
	FULock, FUUnlock          bool  // 11 ordinary/packed-load uop, 10 acquire, 01 release
	WarpStall                 bool  // decode scheduler unlock is !WarpStall
	Sources                   [3]uint8
	Used                      uint8
	ReadMask                  uint8
	LastRead                  bool
	Mask                      uint8
	End                       bool
	Class                     uint8
	Pack                      uint8
	Uops                      uint8
	Path                      Path
}

type Signal struct {
	Valid bool
	Token Token
}

// Buffer owns all storage at one Timing IR boundary.
type Buffer struct {
	spec  timing.BufferSpec
	queue []Token
	rev   revision
}

func NewBuffer(id string) (*Buffer, error) {
	s, err := timing.Buffer(id)
	if err != nil {
		return nil, err
	}
	return &Buffer{spec: s}, nil
}

func (b *Buffer) ID() string        { return b.spec.ID }
func (b *Buffer) Capacity() int     { return b.spec.Size }
func (b *Buffer) Occupancy() int    { return len(b.queue) }
func (b *Buffer) Contents() []Token { return append([]Token(nil), b.queue...) }

// Ready uses old storage only. Only the one-entry pipe couples ready to its
// downstream; stream/FIFO full cannot borrow a slot being released this edge.
func (b *Buffer) Ready(downstream bool) bool {
	switch b.spec.Size {
	case 0:
		return downstream
	case 1:
		return len(b.queue) == 0 || downstream
	default:
		return len(b.queue) < b.spec.Size
	}
}

func (b *Buffer) Output(input Signal) Signal {
	if b.spec.Size == 0 {
		return input
	}
	if len(b.queue) == 0 {
		return Signal{}
	}
	return Signal{Valid: true, Token: b.queue[0]}
}

func (b *Buffer) Evaluate(input Signal, downstream bool) Transition {
	t := transfer(input, b.Output(input), b.Ready(downstream), downstream)
	next := b.Contents()
	if b.spec.Size == 0 {
		next = nil
	} else {
		if t.Completed {
			next = next[1:]
		}
		if t.Accepted {
			next = append(next, input.Token)
		}
	}
	t.edits = []mutation{b.rev.propose(func() { b.queue = next })}
	return t
}

// Flush proposes discarding transient contents, with no architectural rollback.
// Callers must separately cancel external requests before reusing their epoch.
func (b *Buffer) Flush() Transition {
	return Transition{edits: []mutation{b.rev.propose(func() { b.queue = nil })}}
}
