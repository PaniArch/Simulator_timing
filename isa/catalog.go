package isa

const (
	opLUI    = 0x37
	opAUIPC  = 0x17
	opJAL    = 0x6f
	opJALR   = 0x67
	opBranch = 0x63
	opLoad   = 0x03
	opStore  = 0x23
	opImm    = 0x13
	opReg    = 0x33
	opFence  = 0x0f
	opSystem = 0x73
	opFLoad  = 0x07
	opFStore = 0x27
	opFMAdd  = 0x43
	opFMSub  = 0x47
	opFNMSub = 0x4b
	opFNMAdd = 0x4f
	opFloat  = 0x53
	opCustom = 0x0b
	opGather = 0x2b

	maskOpcode = 0x0000007f
	maskF3     = 0x0000707f
	maskF7     = 0xfe00707f
	maskF7NoF3 = 0xfe00007f
	maskRS2    = 0x01f00000
	maskRS1    = 0x000f8000
	maskRD     = 0x00000f80
	maskRM     = 0x00007000
)

var (
	integerEvidence = []string{"hw/rtl/core/VX_decode.sv:208-339", "hw/rtl/VX_gpu_pkg.sv:249-283"}
	mEvidence       = []string{"hw/rtl/core/VX_decode.sv:143-157,227-246", "hw/rtl/core/VX_alu_muldiv.sv"}
	fEvidence       = []string{"hw/rtl/core/VX_decode.sv:398-558", "hw/rtl/VX_gpu_pkg.sv:470-490"}
	systemEvidence  = []string{"hw/rtl/core/VX_decode.sv:131-140,373-397", "hw/rtl/core/VX_csr_unit.sv"}
	memoryEvidence  = []string{"hw/rtl/core/VX_decode.sv:340-347,398-431", "hw/rtl/core/VX_lsu_slice.sv:149-164"}
	customEvidence  = []string{"hw/rtl/core/VX_decode.sv:560-751", "hw/rtl/core/VX_wctl_unit.sv:74-192"}
	laneEvidence    = []string{"hw/rtl/core/VX_decode.sv:613-623,739-751", "hw/rtl/core/VX_alu_int.sv:136-223"}
	packEvidence    = []string{"hw/rtl/core/VX_decode.sv:709-732", "hw/rtl/core/VX_uop_packld.sv"}
	rmConstraint    = []ValueConstraint{{Name: "rm", Mask: maskRM, Shift: 12, Values: []uint32{0, 1, 2, 3, 4, 7}}}
)

func field(file RegisterFile, field RegisterField, access OperandAccess) OperandSpec {
	return OperandSpec{Field: field, File: file, Access: access}
}

var (
	xd = field(Integer, FieldRD, Write)
	x1 = field(Integer, FieldRS1, Read)
	x2 = field(Integer, FieldRS2, Read)
	x3 = field(Integer, FieldRS3, Read)
	fd = field(Float, FieldRD, Write)
	f1 = field(Float, FieldRS1, Read)
	f2 = field(Float, FieldRS2, Read)
	f3 = field(Float, FieldRS3, Read)
)

func bitsF3(f3 uint32, opcode uint32) uint32 { return f3<<12 | opcode }
func bitsF7(f7, f3, opcode uint32) uint32    { return f7<<25 | bitsF3(f3, opcode) }
func bitsRS2(rs2, f7, f3, opcode uint32) uint32 {
	return rs2<<20 | bitsF7(f7, f3, opcode)
}
func rExample(match, mask uint32) uint32 {
	const ordinaryFields = uint32(3<<7 | 1<<15 | 2<<20 | 3<<27)
	return match | (ordinaryFields &^ mask)
}

func base(name string, category Category, match, mask uint32, operands []OperandSpec, imm ImmediateKind, result ResultKind, effects EffectKind, evidence []string) Entry {
	e := Entry{Name: name, Category: category, Match: match, Mask: mask, Operands: operands, Immediate: imm, Result: result, Effects: effects, RequiresActiveMask: true, RTLEvidence: evidence}
	if result != ResultNone {
		e.WriteMask = WriteMaskActive
	}
	e.Example = rExample(match, mask)
	return e
}

func finish(e Entry) Entry {
	e.Example = rExample(e.Match, e.Mask)
	return e
}

func aluI(name string, f3 uint32) Entry {
	return base(name, CategoryRV32I, bitsF3(f3, opImm), maskF3, []OperandSpec{xd, x1}, ImmediateI, ResultInteger, EffectRegister|EffectLane, integerEvidence)
}
func shiftI(name string, f7, f3 uint32) Entry {
	return base(name, CategoryRV32I, bitsF7(f7, f3, opImm), maskF7, []OperandSpec{xd, x1}, ImmediateShift, ResultInteger, EffectRegister|EffectLane, integerEvidence)
}
func aluR(name string, category Category, f7, f3 uint32, evidence []string) Entry {
	return base(name, category, bitsF7(f7, f3, opReg), maskF7, []OperandSpec{xd, x1, x2}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, evidence)
}
func branch(name string, f3 uint32) Entry {
	e := base(name, CategoryRV32I, bitsF3(f3, opBranch), maskF3, []OperandSpec{x1, x2}, ImmediateB, ResultNone, EffectControl|EffectLane, integerEvidence)
	e.Control = ControlBranch
	e.RequiresPC = true
	return e
}
func load(name string, f3 uint32, bytes uint8, signed bool) Entry {
	e := base(name, CategoryRV32I, bitsF3(f3, opLoad), maskF3, []OperandSpec{xd, x1}, ImmediateI, ResultMemoryValue, EffectRegister|EffectMemory|EffectLane, memoryEvidence)
	e.Memory = MemorySpec{Kind: MemoryLoad, Bytes: bytes, Signed: signed}
	return e
}
func store(name string, f3 uint32, bytes uint8) Entry {
	e := base(name, CategoryRV32I, bitsF3(f3, opStore), maskF3, []OperandSpec{x1, x2}, ImmediateS, ResultNone, EffectMemory|EffectLane, memoryEvidence)
	e.Memory = MemorySpec{Kind: MemoryStore, Bytes: bytes}
	return e
}
func fpRM(name string, match, mask uint32, operands []OperandSpec) Entry {
	e := base(name, CategoryRV32F, match, mask, operands, ImmediateNone, ResultFloat, EffectRegister|EffectFFlags|EffectLane, fEvidence)
	e.Constraints = rmConstraint
	e.Rounding = true
	e.Format = FormatSingle
	e.Example = rExample(match, mask) // RNE
	return e
}
func fpFixed(name string, f7, f3 uint32, operands []OperandSpec, result ResultKind, flags bool) Entry {
	effects := EffectRegister | EffectLane
	if flags {
		effects |= EffectFFlags
	}
	e := base(name, CategoryRV32F, bitsF7(f7, f3, opFloat), maskF7, operands, ImmediateNone, result, effects, fEvidence)
	e.Format = FormatSingle
	return e
}
func fpRS2RM(name string, f7, rs2 uint32, operands []OperandSpec, result ResultKind) Entry {
	e := fpRM(name, bitsRS2(rs2, f7, 0, opFloat), maskF7NoF3|maskRS2, operands)
	e.Result = result
	return e
}
func csr(name string, f3 uint32, kind CSRKind, immediate bool) Entry {
	ops := []OperandSpec{xd}
	if !immediate {
		ops = append(ops, x1)
	}
	e := base(name, CategorySystem, bitsF3(f3, opSystem), maskF3, ops, ImmediateCSR, ResultCSRValue, EffectRegister|EffectCSR|EffectLane, systemEvidence)
	e.CSR = kind
	return e
}
func custom(name string, f3 uint32, operands []OperandSpec, result ResultKind, effects EffectKind, control ControlKind) Entry {
	e := base(name, CategoryCustom, bitsF7(0, f3, opCustom), maskF7, operands, ImmediateNone, result, effects, customEvidence)
	e.Control = control
	return e
}

// catalog is the single frozen-enabled ISA manifest. Decoder and coverage tests
// consume this exact slice; no second opcode table exists.
var catalog = []Entry{
	base("lui", CategoryRV32I, opLUI, maskOpcode, []OperandSpec{xd}, ImmediateU, ResultInteger, EffectRegister|EffectLane, integerEvidence),
	func() Entry {
		e := base("auipc", CategoryRV32I, opAUIPC, maskOpcode, []OperandSpec{xd}, ImmediateU, ResultInteger, EffectRegister|EffectLane, integerEvidence)
		e.RequiresPC = true
		return e
	}(),
	func() Entry {
		e := base("jal", CategoryRV32I, opJAL, maskOpcode, []OperandSpec{xd}, ImmediateJ, ResultInteger, EffectRegister|EffectControl|EffectLane, integerEvidence)
		e.Control = ControlJump
		e.RequiresPC = true
		return e
	}(),
	func() Entry {
		e := base("jalr", CategoryRV32I, bitsF3(0, opJALR), maskF3, []OperandSpec{xd, x1}, ImmediateI, ResultInteger, EffectRegister|EffectControl|EffectLane, integerEvidence)
		e.Control = ControlJump
		e.RequiresPC = true
		return e
	}(),
	branch("beq", 0), branch("bne", 1), branch("blt", 4), branch("bge", 5), branch("bltu", 6), branch("bgeu", 7),
	load("lb", 0, 1, true), load("lh", 1, 2, true), load("lw", 2, 4, true), load("lbu", 4, 1, false), load("lhu", 5, 2, false),
	store("sb", 0, 1), store("sh", 1, 2), store("sw", 2, 4),
	aluI("addi", 0), shiftI("slli", 0x00, 1), aluI("slti", 2), aluI("sltiu", 3), aluI("xori", 4), shiftI("srli", 0x00, 5), shiftI("srai", 0x20, 5), aluI("ori", 6), aluI("andi", 7),
	aluR("add", CategoryRV32I, 0x00, 0, integerEvidence), aluR("sub", CategoryRV32I, 0x20, 0, integerEvidence), aluR("sll", CategoryRV32I, 0x00, 1, integerEvidence), aluR("slt", CategoryRV32I, 0x00, 2, integerEvidence), aluR("sltu", CategoryRV32I, 0x00, 3, integerEvidence), aluR("xor", CategoryRV32I, 0x00, 4, integerEvidence), aluR("srl", CategoryRV32I, 0x00, 5, integerEvidence), aluR("sra", CategoryRV32I, 0x20, 5, integerEvidence), aluR("or", CategoryRV32I, 0x00, 6, integerEvidence), aluR("and", CategoryRV32I, 0x00, 7, integerEvidence),
	func() Entry {
		e := base("fence", CategoryFence, opFence, 0xf00fffff, nil, ImmediateNone, ResultNone, EffectMemory|EffectOrdering, memoryEvidence)
		e.Memory = MemorySpec{Kind: MemoryFence}
		e.Example = 0x0ff0000f
		return e
	}(),

	aluR("mul", CategoryRV32M, 0x01, 0, mEvidence), aluR("mulh", CategoryRV32M, 0x01, 1, mEvidence), aluR("mulhsu", CategoryRV32M, 0x01, 2, mEvidence), aluR("mulhu", CategoryRV32M, 0x01, 3, mEvidence), aluR("div", CategoryRV32M, 0x01, 4, mEvidence), aluR("divu", CategoryRV32M, 0x01, 5, mEvidence), aluR("rem", CategoryRV32M, 0x01, 6, mEvidence), aluR("remu", CategoryRV32M, 0x01, 7, mEvidence),
	aluR("czero.eqz", CategoryZicond, 0x07, 5, integerEvidence), aluR("czero.nez", CategoryZicond, 0x07, 7, integerEvidence),

	func() Entry {
		e := base("ecall", CategorySystem, 0x00000073, 0xffffffff, nil, ImmediateNone, ResultNone, EffectControl|EffectCSR|EffectWarp, systemEvidence)
		e.Control = ControlTrap
		e.RequiresPC = true
		e.Example = e.Match
		return e
	}(),
	func() Entry {
		e := base("ebreak", CategorySystem, 0x00100073, 0xffffffff, nil, ImmediateNone, ResultNone, EffectControl|EffectCSR|EffectWarp, systemEvidence)
		e.Control = ControlTrap
		e.RequiresPC = true
		e.Example = e.Match
		return e
	}(),
	func() Entry {
		e := base("uret", CategorySystem, 0x00200073, 0xffffffff, nil, ImmediateNone, ResultNone, EffectControl|EffectCSR|EffectWarp, systemEvidence)
		e.Control = ControlTrapReturn
		e.RequiresPC = true
		e.Example = e.Match
		return e
	}(),
	func() Entry {
		e := base("sret", CategorySystem, 0x10200073, 0xffffffff, nil, ImmediateNone, ResultNone, EffectControl|EffectCSR|EffectWarp, systemEvidence)
		e.Control = ControlTrapReturn
		e.RequiresPC = true
		e.Example = e.Match
		return e
	}(),
	func() Entry {
		e := base("mret", CategorySystem, 0x30200073, 0xffffffff, nil, ImmediateNone, ResultNone, EffectControl|EffectCSR|EffectWarp, systemEvidence)
		e.Control = ControlTrapReturn
		e.RequiresPC = true
		e.Example = e.Match
		return e
	}(),
	csr("csrrw", 1, CSRRW, false), csr("csrrs", 2, CSRRS, false), csr("csrrc", 3, CSRRC, false), csr("csrrwi", 5, CSRRW, true), csr("csrrsi", 6, CSRRS, true), csr("csrrci", 7, CSRRC, true),

	func() Entry {
		e := base("flw", CategoryRV32F, bitsF3(2, opFLoad), maskF3, []OperandSpec{fd, x1}, ImmediateI, ResultMemoryValue, EffectRegister|EffectMemory|EffectLane, memoryEvidence)
		e.Format = FormatSingle
		e.Memory = MemorySpec{Kind: MemoryLoad, Bytes: 4, Float: true}
		return e
	}(),
	func() Entry {
		e := base("fsw", CategoryRV32F, bitsF3(2, opFStore), maskF3, []OperandSpec{x1, f2}, ImmediateS, ResultNone, EffectMemory|EffectLane, memoryEvidence)
		e.Format = FormatSingle
		e.Memory = MemorySpec{Kind: MemoryStore, Bytes: 4, Float: true}
		return e
	}(),
	fpRM("fmadd.s", opFMAdd, 0x0600007f, []OperandSpec{fd, f1, f2, f3}), fpRM("fmsub.s", opFMSub, 0x0600007f, []OperandSpec{fd, f1, f2, f3}), fpRM("fnmsub.s", opFNMSub, 0x0600007f, []OperandSpec{fd, f1, f2, f3}), fpRM("fnmadd.s", opFNMAdd, 0x0600007f, []OperandSpec{fd, f1, f2, f3}),
	fpRM("fadd.s", bitsF7(0x00, 0, opFloat), maskF7NoF3, []OperandSpec{fd, f1, f2}), fpRM("fsub.s", bitsF7(0x04, 0, opFloat), maskF7NoF3, []OperandSpec{fd, f1, f2}), fpRM("fmul.s", bitsF7(0x08, 0, opFloat), maskF7NoF3, []OperandSpec{fd, f1, f2}), fpRM("fdiv.s", bitsF7(0x0c, 0, opFloat), maskF7NoF3, []OperandSpec{fd, f1, f2}), fpRS2RM("fsqrt.s", 0x2c, 0, []OperandSpec{fd, f1}, ResultFloat),
	fpFixed("fsgnj.s", 0x10, 0, []OperandSpec{fd, f1, f2}, ResultFloat, false), fpFixed("fsgnjn.s", 0x10, 1, []OperandSpec{fd, f1, f2}, ResultFloat, false), fpFixed("fsgnjx.s", 0x10, 2, []OperandSpec{fd, f1, f2}, ResultFloat, false), fpFixed("fmin.s", 0x14, 0, []OperandSpec{fd, f1, f2}, ResultFloat, true), fpFixed("fmax.s", 0x14, 1, []OperandSpec{fd, f1, f2}, ResultFloat, true),
	fpFixed("fle.s", 0x50, 0, []OperandSpec{xd, f1, f2}, ResultInteger, true), fpFixed("flt.s", 0x50, 1, []OperandSpec{xd, f1, f2}, ResultInteger, true), fpFixed("feq.s", 0x50, 2, []OperandSpec{xd, f1, f2}, ResultInteger, true),
	fpRS2RM("fcvt.w.s", 0x60, 0, []OperandSpec{xd, f1}, ResultInteger), fpRS2RM("fcvt.wu.s", 0x60, 1, []OperandSpec{xd, f1}, ResultInteger), fpRS2RM("fcvt.s.w", 0x68, 0, []OperandSpec{fd, x1}, ResultFloat), fpRS2RM("fcvt.s.wu", 0x68, 1, []OperandSpec{fd, x1}, ResultFloat),
	func() Entry {
		e := fpFixed("fmv.x.w", 0x70, 0, []OperandSpec{xd, f1}, ResultInteger, false)
		e.Mask |= maskRS2
		e.Match &^= maskRS2
		return finish(e)
	}(),
	func() Entry {
		e := fpFixed("fclass.s", 0x70, 1, []OperandSpec{xd, f1}, ResultInteger, false)
		e.Mask |= maskRS2
		e.Match &^= maskRS2
		return finish(e)
	}(),
	func() Entry {
		e := fpFixed("fmv.w.x", 0x78, 0, []OperandSpec{fd, x1}, ResultFloat, false)
		e.Mask |= maskRS2
		e.Match &^= maskRS2
		return finish(e)
	}(),

	func() Entry {
		e := custom("tmc", 0, []OperandSpec{x1}, ResultNone, EffectWarp|EffectLane|EffectControl, ControlThreadMask)
		e.Mask |= maskRD | maskRS2
		return finish(e)
	}(),
	func() Entry {
		e := custom("wspawn", 1, []OperandSpec{x1, x2}, ResultNone, EffectWarp|EffectCSR|EffectControl, ControlWarpSpawn)
		e.Mask |= maskRD
		return finish(e)
	}(),
	func() Entry {
		e := custom("split", 2, []OperandSpec{xd, x1}, ResultControlValue, EffectRegister|EffectWarp|EffectControl, ControlSplit)
		e.Mask |= 0x01e00000
		e.Modifier = ModifierSplitNegateRS2
		e.RequiresPC = true
		return finish(e)
	}(),
	func() Entry {
		e := custom("join", 3, []OperandSpec{x1}, ResultNone, EffectWarp|EffectControl, ControlJoin)
		e.Mask |= maskRD | maskRS2
		return finish(e)
	}(),
	func() Entry {
		e := custom("bar", 4, []OperandSpec{x1, x2}, ResultNone, EffectBarrier|EffectWarp|EffectOrdering, ControlNone)
		e.Mask |= maskRD
		e.Barrier = BarrierSync
		return finish(e)
	}(),
	func() Entry {
		e := custom("pred", 5, []OperandSpec{x1, x2}, ResultNone, EffectWarp|EffectLane|EffectControl, ControlPredicate)
		e.Mask |= 0x00000f00
		e.Modifier = ModifierPredicateNegateRD
		return finish(e)
	}(),
	func() Entry {
		e := custom("bar.wait", 6, []OperandSpec{x1, x2}, ResultNone, EffectBarrier|EffectWarp|EffectOrdering, ControlNone)
		e.Mask |= maskRD
		e.Barrier = BarrierWait
		return finish(e)
	}(),
	func() Entry {
		e := custom("bar.arrive", 6, []OperandSpec{xd, x1, x2}, ResultControlValue, EffectRegister|EffectBarrier|EffectWarp|EffectOrdering, ControlNone)
		e.Constraints = []ValueConstraint{{Name: "rd", Mask: maskRD, Shift: 7, NonZero: true}}
		e.Barrier = BarrierArrive
		return finish(e)
	}(),
	func() Entry {
		e := custom("wsync", 7, nil, ResultNone, EffectWarp|EffectOrdering, ControlWarpSync)
		e.Mask |= maskRD | maskRS1 | maskRS2
		return finish(e)
	}(),

	func() Entry {
		e := base("vote.all", CategoryCustom, bitsF7(1, 0, opCustom), maskF7|maskRS2, []OperandSpec{xd, x1}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence)
		return e
	}(),
	func() Entry {
		e := base("vote.any", CategoryCustom, bitsF7(1, 1, opCustom), maskF7|maskRS2, []OperandSpec{xd, x1}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence)
		return e
	}(),
	func() Entry {
		e := base("vote.uni", CategoryCustom, bitsF7(1, 2, opCustom), maskF7|maskRS2, []OperandSpec{xd, x1}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence)
		return e
	}(),
	func() Entry {
		e := base("vote.ballot", CategoryCustom, bitsF7(1, 3, opCustom), maskF7|maskRS2, []OperandSpec{xd, x1}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence)
		return e
	}(),
	base("shfl.up", CategoryCustom, bitsF7(1, 4, opCustom), maskF7, []OperandSpec{xd, x1, x2}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence), base("shfl.down", CategoryCustom, bitsF7(1, 5, opCustom), maskF7, []OperandSpec{xd, x1, x2}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence), base("shfl.bfly", CategoryCustom, bitsF7(1, 6, opCustom), maskF7, []OperandSpec{xd, x1, x2}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence), base("shfl.idx", CategoryCustom, bitsF7(1, 7, opCustom), maskF7, []OperandSpec{xd, x1, x2}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence),
	func() Entry {
		e := base("wgather", CategoryCustom, bitsF3(0, opGather), maskF3, []OperandSpec{xd, x1, x2, x3}, ImmediateNone, ResultInteger, EffectRegister|EffectLane, laneEvidence)
		e.WriteMask = WriteMaskGatherNonSource
		e.Modifier = ModifierGatherSourceLane
		return e
	}(),
	func() Entry {
		e := base("vx_packlb_f", CategoryCustom, bitsF7(4, 1, opCustom), maskF7, []OperandSpec{fd, x1, x2}, ImmediateNone, ResultMemoryValue, EffectRegister|EffectMemory|EffectLane, packEvidence)
		e.Format = FormatSingle
		e.Memory = MemorySpec{Kind: MemoryLoad, Bytes: 1, Float: true, Packed: 4}
		return e
	}(),
	func() Entry {
		e := base("vx_packlh_f", CategoryCustom, bitsF7(4, 2, opCustom), maskF7, []OperandSpec{fd, x1, x2}, ImmediateNone, ResultMemoryValue, EffectRegister|EffectMemory|EffectLane, packEvidence)
		e.Format = FormatSingle
		e.Memory = MemorySpec{Kind: MemoryLoad, Bytes: 2, Float: true, Packed: 2}
		return e
	}(),
}

// Catalog returns a defensive copy of the authoritative frozen manifest.
func Catalog() []Entry {
	result := make([]Entry, len(catalog))
	for i := range catalog {
		result[i] = catalog[i]
		result[i].Constraints = append([]ValueConstraint(nil), catalog[i].Constraints...)
		for j := range result[i].Constraints {
			result[i].Constraints[j].Values = append([]uint32(nil), catalog[i].Constraints[j].Values...)
		}
		result[i].Operands = append([]OperandSpec(nil), catalog[i].Operands...)
		result[i].RTLEvidence = append([]string(nil), catalog[i].RTLEvidence...)
	}
	return result
}
