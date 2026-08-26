package warp_test

import (
	"bytes"
	"errors"
	"testing"

	"vortex.local/simulator/isa"
	"vortex.local/simulator/state"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/warp"
)

func memoryExecutor(t *testing.T, owner *state.WarpState, service warp.MemoryService) *warp.Warp {
	t.Helper()
	executor, err := warp.NewWithMemory(owner, service)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func loadWord(immediate, rs1, funct3, rd uint32) uint32 {
	return (immediate&0xfff)<<20 | rs1<<15 | funct3<<12 | rd<<7 | 0x03
}

func storeWord(immediate, rs2, rs1, funct3 uint32) uint32 {
	return (immediate>>5)<<25 | rs2<<20 | rs1<<15 | funct3<<12 | (immediate&0x1f)<<7 | 0x23
}

func TestMemoryStepStoreLoadAndALUDependencies(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[1] = 0x200
	initial.Lanes[0].GPR[2] = 0x80ff7f01
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, storeWord(0, 2, 1, 2)) // sw x2,0(x1)
	putWord(t, m, 0x104, loadWord(0, 1, 2, 3))  // lw x3,0(x1)
	putWord(t, m, 0x108, 0x00118213)            // addi x4,x3,1
	w := memoryExecutor(t, owner, m)
	for range 3 {
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	}
	data, err := m.Load32(0x200)
	if err != nil {
		t.Fatal(err)
	}
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	x4, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 4})
	if data != 0x80ff7f01 || x3[0] != data || x4[0] != data+1 {
		t.Fatalf("memory/dependency data=%#x x3=%#x x4=%#x", data, x3[0], x4[0])
	}
}

func TestMemoryStepAllIntegerWidthsAndSignedLoads(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[1] = 0x240
	initial.Lanes[0].GPR[2] = 0x8001ff80
	owner := newState(t, initial)
	m := newMemory(t)
	program := []uint32{
		storeWord(0, 2, 1, 0), // sb x2,0(x1)
		storeWord(2, 2, 1, 1), // sh x2,2(x1)
		storeWord(4, 2, 1, 2), // sw x2,4(x1)
		loadWord(0, 1, 0, 3),  // lb x3,0(x1)
		loadWord(0, 1, 4, 4),  // lbu x4,0(x1)
		loadWord(2, 1, 1, 5),  // lh x5,2(x1)
		loadWord(2, 1, 5, 6),  // lhu x6,2(x1)
		loadWord(4, 1, 2, 7),  // lw x7,4(x1)
	}
	for index, word := range program {
		putWord(t, m, 0x100+uint32(4*index), word)
	}
	w := memoryExecutor(t, owner, m)
	for range len(program) {
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	}
	want := map[uint8]uint32{3: 0xffffff80, 4: 0x80, 5: 0xffffff80, 6: 0xff80, 7: 0x8001ff80}
	for register, expected := range want {
		values, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: register})
		if values[0] != expected {
			t.Fatalf("x%d=%#x want=%#x", register, values[0], expected)
		}
	}
	got, _ := m.ReadBytes(0x240, 8)
	if !bytes.Equal(got, []byte{0x80, 0, 0x80, 0xff, 0x80, 0xff, 0x01, 0x80}) {
		t.Fatalf("stored bytes=%x", got)
	}
}

func TestMemoryStepFLWFPFSWChain(t *testing.T) {
	initial := initialWarp()
	initial.Lanes[0].GPR[1] = 0x280
	owner := newState(t, initial)
	m := newMemory(t)
	if err := m.Store32(0x280, 0x3f800000); err != nil { // 1.0
		t.Fatal(err)
	}
	putWord(t, m, 0x100, 0x0000a087) // flw f1,0(x1)
	putWord(t, m, 0x104, 0x00108153) // fadd.s f2,f1,f1
	putWord(t, m, 0x108, 0x0020a227) // fsw f2,4(x1)
	w := memoryExecutor(t, owner, m)
	for range 3 {
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	}
	f1, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 1})
	f2, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 2})
	stored, _ := m.Load32(0x284)
	if f1[0] != 0x3f800000 || f2[0] != 0x40000000 || stored != f2[0] {
		t.Fatalf("FP chain f1=%#x f2=%#x stored=%#x", f1[0], f2[0], stored)
	}
}

func TestMemoryStepMultiLaneIntegerStoreAndLoad(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[1] = 0x200 + uint32(lane*8)
		initial.Lanes[lane].GPR[2] = 0x11110000 + uint32(lane)
	}
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, storeWord(0, 2, 1, 2)) // sw x2,0(x1)
	putWord(t, m, 0x104, loadWord(0, 1, 2, 3))  // lw x3,0(x1)
	w := memoryExecutor(t, owner, m)
	store := w.Step(state.ReadContext{})
	requireOutcome(t, store, warp.OutcomeRetired)
	load := w.Step(state.ReadContext{})
	requireOutcome(t, load, warp.OutcomeRetired)
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		want := uint32(0x11110000) + uint32(lane)
		stored, err := m.Load32(0x200 + uint32(lane)*8)
		if err != nil || stored != want || x3[lane] != want {
			t.Fatalf("lane %d stored=%#x x3=%#x err=%v", lane, stored, x3[lane], err)
		}
	}
	if len(store.IssuedEffects.MemoryRequests) != isa.FrozenLaneCount ||
		len(load.IssuedEffects.MemoryRequests) != isa.FrozenLaneCount || load.Effects.RegisterWrites[0].Mask != isa.AllLanes {
		t.Fatalf("multi-lane request coverage store=%+v load=%+v", store, load)
	}
}

func TestMemoryStepPartialMaskSkipsInactiveAddressesAndWriteback(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = 0b1010
	initial.Lanes[0].GPR[1], initial.Lanes[2].GPR[1] = 0x3ff, 0x3ff
	initial.Lanes[1].GPR[1], initial.Lanes[3].GPR[1] = 0x240, 0x248
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[3] = uint32(0xa0 + lane)
	}
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, loadWord(0, 1, 2, 3))
	if err := m.Store32(0x240, 0x11223344); err != nil {
		t.Fatal(err)
	}
	if err := m.Store32(0x248, 0x55667788); err != nil {
		t.Fatal(err)
	}
	result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeRetired)
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if x3 != (isa.LaneValues{0xa0, 0x11223344, 0xa2, 0x55667788}) ||
		len(result.IssuedEffects.MemoryRequests) != 2 || result.Effects.RegisterWrites[0].Mask != 0b1010 {
		t.Fatalf("partial load result=%+v x3=%#x", result, x3)
	}
}

func TestMemoryStepMultiLaneFLWAndFSW(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[1] = 0x280 + uint32(lane*8)
		initial.Lanes[lane].FPR[2] = 0x3f800000 + uint32(lane)<<23
	}
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, 0x0020a027) // fsw f2,0(x1)
	putWord(t, m, 0x104, 0x0000a187) // flw f3,0(x1)
	w := memoryExecutor(t, owner, m)
	requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	f2, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 2})
	f3, _ := owner.ReadRegister(isa.Register{File: isa.Float, Index: 3})
	if f3 != f2 {
		t.Fatalf("multi-lane FLW/FSW f2=%#x f3=%#x", f2, f3)
	}
}

func catalogDecoded(t *testing.T, name string) isa.Decoded {
	t.Helper()
	word := catalogWord(t, name)
	decoded, err := isa.Decode(word)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestMemoryStepCompletesSingleLanePackedLoad(t *testing.T) {
	for _, test := range []struct {
		name   string
		stride uint32
	}{
		{name: "vx_packlb_f", stride: 1},
		{name: "vx_packlh_f", stride: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded := catalogDecoded(t, test.name)
			initial := initialWarp()
			initial.Lanes[0].GPR[decoded.Sources[0].Index] = 0x2a0
			initial.Lanes[0].GPR[decoded.Sources[1].Index] = test.stride
			owner := newState(t, initial)
			m := newMemory(t)
			putWord(t, m, 0x100, decoded.Word)
			if err := m.Write(0x2a0, []byte{0x11, 0x22, 0x33, 0x44}); err != nil {
				t.Fatal(err)
			}
			result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
			requireOutcome(t, result, warp.OutcomeRetired)
			values, _ := owner.ReadRegister(decoded.Destinations[0])
			if values[0] != 0x44332211 || result.NextPC != 0x104 {
				t.Fatalf("packed result=%+v FPR=%#x", result, values[0])
			}
		})
	}
}

func TestMemoryStepCompletesPartialMaskPackedLoad(t *testing.T) {
	decoded := catalogDecoded(t, "vx_packlb_f")
	initial := initialWarp()
	initial.ActiveMask = 0b0101
	for lane := range initial.Lanes {
		initial.Lanes[lane].FPR[decoded.Destinations[0].Index] = uint32(0xdead0000 + lane)
		initial.Lanes[lane].GPR[decoded.Sources[1].Index] = 1
	}
	initial.Lanes[0].GPR[decoded.Sources[0].Index] = 0x2c0
	initial.Lanes[2].GPR[decoded.Sources[0].Index] = 0x2d0
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, decoded.Word)
	if err := m.Write(0x2c0, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(0x2d0, []byte{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeRetired)
	values, _ := owner.ReadRegister(decoded.Destinations[0])
	if values != (isa.LaneValues{0x04030201, 0xdead0001, 0x08070605, 0xdead0003}) ||
		len(result.IssuedEffects.PackedLoads) != 8 || result.Effects.RegisterWrites[0].Mask != 0b0101 {
		t.Fatalf("partial packed result=%+v values=%#x", result, values)
	}
}

func TestMemoryStepCompletesMultiLanePackedHalfLoad(t *testing.T) {
	decoded := catalogDecoded(t, "vx_packlh_f")
	initial := initialWarp()
	initial.ActiveMask = 0b1010
	for lane := range initial.Lanes {
		initial.Lanes[lane].FPR[decoded.Destinations[0].Index] = uint32(0xbeef0000 + lane)
		initial.Lanes[lane].GPR[decoded.Sources[1].Index] = 2
	}
	initial.Lanes[1].GPR[decoded.Sources[0].Index] = 0x300
	initial.Lanes[3].GPR[decoded.Sources[0].Index] = 0x320
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, decoded.Word)
	if err := m.Write(0x300, []byte{0x11, 0x22, 0x33, 0x44}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(0x320, []byte{0x55, 0x66, 0x77, 0x88}); err != nil {
		t.Fatal(err)
	}
	result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeRetired)
	values, _ := owner.ReadRegister(decoded.Destinations[0])
	if values != (isa.LaneValues{0xbeef0000, 0x44332211, 0xbeef0002, 0x88776655}) ||
		len(result.IssuedEffects.PackedLoads) != 4 || result.Effects.RegisterWrites[0].Mask != 0b1010 {
		t.Fatalf("packed half result=%+v values=%#x", result, values)
	}
}

func TestMemoryStepPackedElementFaultSuppressesAllLaneWriteback(t *testing.T) {
	decoded := catalogDecoded(t, "vx_packlh_f")
	initial := initialWarp()
	initial.ActiveMask = 0b0101
	for lane := range initial.Lanes {
		initial.Lanes[lane].FPR[decoded.Destinations[0].Index] = uint32(0xcafe0000 + lane)
		initial.Lanes[lane].GPR[decoded.Sources[1].Index] = 2
	}
	initial.Lanes[0].GPR[decoded.Sources[0].Index] = 0x2a0
	initial.Lanes[2].GPR[decoded.Sources[0].Index] = 0x2c0
	owner := newState(t, initial)
	backing := newMemory(t)
	putWord(t, backing, 0x100, decoded.Word)
	if err := backing.Write(0x2a0, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := backing.Write(0x2c0, []byte{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	before, _ := owner.Snapshot()
	result := memoryExecutor(t, owner, &faultMemory{backing: backing, failRead: 0x2c2}).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	after, _ := owner.Snapshot()
	values, _ := owner.ReadRegister(decoded.Destinations[0])
	if before != after || values != (isa.LaneValues{0xcafe0000, 0xcafe0001, 0xcafe0002, 0xcafe0003}) ||
		result.Fault.Kind != warp.FaultArchitectural || len(result.Fault.Architectural) != 1 ||
		result.Fault.Architectural[0].Lane != 2 || result.NextPC != 0x100 {
		t.Fatalf("packed fault result=%+v values=%#x changed=%t", result, values, before != after)
	}
}

func TestMemoryStepFenceResolvesOnlyWithSynchronousOwner(t *testing.T) {
	t.Run("resolved", func(t *testing.T) {
		owner := newState(t, initialWarp())
		m := newMemory(t)
		putWord(t, m, 0x100, catalogWord(t, "fence"))
		result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		if result.Effects == nil || result.Effects.Ordering == nil || result.NextPC != 0x104 {
			t.Fatalf("FENCE result=%+v", result)
		}
	})
	t.Run("deferred-without-owner", func(t *testing.T) {
		owner := newState(t, initialWarp())
		result := executor(t, owner, &recordingSource{word: catalogWord(t, "fence")}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeDeferred)
		if result.NextPC != 0x100 {
			t.Fatalf("deferred FENCE result=%+v", result)
		}
	})
}

func TestFetchAndDataAccessShareCanonicalBytes(t *testing.T) {
	const replacement = uint32(0x00900313) // addi x6,x0,9
	initial := initialWarp()
	initial.Lanes[0].GPR[1] = 0x108
	initial.Lanes[0].GPR[2] = replacement
	owner := newState(t, initial)
	m := newMemory(t)
	putWord(t, m, 0x100, storeWord(0, 2, 1, 2)) // overwrite PC 0x108
	putWord(t, m, 0x104, 0x0040006f)            // jal x0,+4
	putWord(t, m, 0x108, 0xffffffff)
	w := memoryExecutor(t, owner, m)
	for range 3 {
		requireOutcome(t, w.Step(state.ReadContext{}), warp.OutcomeRetired)
	}
	x6, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 6})
	word, _ := m.Load32(0x108)
	if word != replacement || x6[0] != 9 {
		t.Fatalf("shared bytes word=%#x x6=%#x", word, x6[0])
	}
}

type faultMemory struct {
	backing   *memory.Memory
	failRead  uint32
	failWrite uint32
}

type batchFaultMemory struct {
	backing    *memory.Memory
	failBatch  bool
	batchCalls int
}

func (m *batchFaultMemory) Read(address uint32, dst []byte) error {
	return m.backing.Read(address, dst)
}

func (m *batchFaultMemory) Write(address uint32, src []byte) error {
	return m.backing.Write(address, src)
}

func (m *batchFaultMemory) WriteBatch(addresses []uint32, sources [][]byte) error {
	m.batchCalls++
	if m.failBatch {
		return errors.New("injected atomic batch failure")
	}
	return m.backing.WriteBatch(addresses, sources)
}

func TestMultiLaneStoreBatchFailureLeavesEveryByteAndStateUnchanged(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[1] = 0x300 + uint32(lane*8)
		initial.Lanes[lane].GPR[2] = 0xabcdef00 + uint32(lane)
	}
	owner := newState(t, initial)
	backing := newMemory(t)
	putWord(t, backing, 0x100, storeWord(0, 2, 1, 2))
	service := &batchFaultMemory{backing: backing, failBatch: true}
	beforeState, _ := owner.Snapshot()
	beforeMemory := backing.Snapshot()
	result := memoryExecutor(t, owner, service).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	afterState, _ := owner.Snapshot()
	if result.Fault.Kind != warp.FaultArchitectural || len(result.Fault.Architectural) != isa.FrozenLaneCount ||
		service.batchCalls != 1 || beforeState != afterState || !bytes.Equal(beforeMemory, backing.Snapshot()) {
		t.Fatalf("atomic failure result=%+v calls=%d state=%t memory=%t", result, service.batchCalls,
			beforeState != afterState, !bytes.Equal(beforeMemory, backing.Snapshot()))
	}
}

func TestMultiLaneLoadServiceFaultSuppressesEveryLaneWriteback(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[1] = 0x200 + uint32(lane*8)
		initial.Lanes[lane].GPR[3] = uint32(0x55 + lane)
	}
	owner := newState(t, initial)
	backing := newMemory(t)
	putWord(t, backing, 0x100, loadWord(0, 1, 2, 3))
	for lane := uint32(0); lane < isa.FrozenLaneCount; lane++ {
		if err := backing.Store32(0x200+lane*8, 0x100+lane); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := owner.Snapshot()
	result := memoryExecutor(t, owner, &faultMemory{backing: backing, failRead: 0x210}).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	after, _ := owner.Snapshot()
	x3, _ := owner.ReadRegister(isa.Register{File: isa.Integer, Index: 3})
	if before != after || x3 != (isa.LaneValues{0x55, 0x56, 0x57, 0x58}) || len(result.Fault.Architectural) != 1 || result.Fault.Architectural[0].Lane != 2 {
		t.Fatalf("load rollback result=%+v x3=%#x changed=%t", result, x3, before != after)
	}
}

func (m *faultMemory) Read(address uint32, dst []byte) error {
	if address == m.failRead {
		return errors.New("injected memory read failure")
	}
	return m.backing.Read(address, dst)
}

func (m *faultMemory) Write(address uint32, src []byte) error {
	if address == m.failWrite {
		return errors.New("injected memory write failure")
	}
	return m.backing.Write(address, src)
}

func TestMemoryFaultsLeaveStateAndStoreBytesUnchanged(t *testing.T) {
	t.Run("misaligned", func(t *testing.T) {
		initial := initialWarp()
		initial.Lanes[0].GPR[1] = 0x201
		owner := newState(t, initial)
		m := newMemory(t)
		putWord(t, m, 0x100, loadWord(0, 1, 1, 3))
		before, _ := owner.Snapshot()
		result := memoryExecutor(t, owner, m).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Kind != warp.FaultArchitectural || result.Fault.Architectural[0].Kind != isa.FaultLoadAddressMisaligned || before != after {
			t.Fatalf("misaligned result=%+v changed=%t", result, before != after)
		}
	})
	t.Run("bounds", func(t *testing.T) {
		initial := initialWarp()
		initial.Lanes[0].GPR[1] = 0x300
		owner := newState(t, initial)
		m := newMemory(t)
		putWord(t, m, 0x100, loadWord(0, 1, 2, 3))
		before, _ := owner.Snapshot()
		result := memoryExecutor(t, owner, m).Step(state.ReadContext{Bounds: &isa.AddressBounds{Size: 0x300}})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Architectural[0].Reason != isa.FaultReasonBounds || before != after {
			t.Fatalf("bounds result=%+v changed=%t", result, before != after)
		}
	})
	t.Run("service-read", func(t *testing.T) {
		initial := initialWarp()
		initial.Lanes[0].GPR[1] = 0x200
		owner := newState(t, initial)
		backing := newMemory(t)
		putWord(t, backing, 0x100, loadWord(0, 1, 2, 3))
		before, _ := owner.Snapshot()
		result := memoryExecutor(t, owner, &faultMemory{backing: backing, failRead: 0x200}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		after, _ := owner.Snapshot()
		if result.Fault.Architectural[0].Kind != isa.FaultLoadAccess || result.Fault.Cause == nil || before != after {
			t.Fatalf("service result=%+v changed=%t", result, before != after)
		}
	})
	t.Run("service-store", func(t *testing.T) {
		initial := initialWarp()
		initial.Lanes[0].GPR[1], initial.Lanes[0].GPR[2] = 0x200, 0xdeadbeef
		owner := newState(t, initial)
		backing := newMemory(t)
		putWord(t, backing, 0x100, storeWord(0, 2, 1, 2))
		if err := backing.Store32(0x200, 0x12345678); err != nil {
			t.Fatal(err)
		}
		beforeState, _ := owner.Snapshot()
		beforeMemory := backing.Snapshot()
		result := memoryExecutor(t, owner, &faultMemory{backing: backing, failWrite: 0x200}).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		afterState, _ := owner.Snapshot()
		if result.Fault.Architectural[0].Kind != isa.FaultStoreAccess || beforeState != afterState || !bytes.Equal(beforeMemory, backing.Snapshot()) {
			t.Fatalf("store fault result=%+v state=%t memory=%t", result, beforeState != afterState, !bytes.Equal(beforeMemory, backing.Snapshot()))
		}
	})
}

type stateMutatingMemory struct {
	backing      *memory.Memory
	owner        *state.WarpState
	dataAddress  uint32
	mutateOnRead bool
	mutated      bool
	dataWrites   int
	failSecond   bool
}

func (m *stateMutatingMemory) Read(address uint32, dst []byte) error {
	if err := m.backing.Read(address, dst); err != nil {
		return err
	}
	if m.mutateOnRead && address == m.dataAddress && !m.mutated {
		m.mutated = true
		return m.owner.SetPC(0x180)
	}
	return nil
}

func (m *stateMutatingMemory) Write(address uint32, src []byte) error {
	if address == m.dataAddress {
		m.dataWrites++
		if m.failSecond && m.dataWrites > 1 {
			return errors.New("injected compensating write failure")
		}
	}
	if err := m.backing.Write(address, src); err != nil {
		return err
	}
	if !m.mutateOnRead && address == m.dataAddress && !m.mutated {
		m.mutated = true
		return m.owner.SetPC(0x180)
	}
	return nil
}

func TestStoreCoordinatedCommitRejectsStaleBeforeOneWrite(t *testing.T) {
	newFixture := func(t *testing.T) (*state.WarpState, *memory.Memory) {
		t.Helper()
		initial := initialWarp()
		initial.Lanes[0].GPR[1], initial.Lanes[0].GPR[2] = 0x200, 0xdeadbeef
		owner := newState(t, initial)
		backing := newMemory(t)
		putWord(t, backing, 0x100, storeWord(0, 2, 1, 2))
		if err := backing.Store32(0x200, 0x12345678); err != nil {
			t.Fatal(err)
		}
		return owner, backing
	}
	t.Run("stale-before-write", func(t *testing.T) {
		owner, backing := newFixture(t)
		service := &stateMutatingMemory{backing: backing, owner: owner, dataAddress: 0x200, mutateOnRead: true}
		beforeMemory := backing.Snapshot()
		result := memoryExecutor(t, owner, service).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeFault)
		if result.NextPC != 0x180 || service.dataWrites != 0 || !bytes.Equal(beforeMemory, backing.Snapshot()) {
			t.Fatalf("pre-write rejection result=%+v writes=%d memory changed=%t", result, service.dataWrites, !bytes.Equal(beforeMemory, backing.Snapshot()))
		}
	})
	t.Run("successful-write-makes-state-commit-infallible", func(t *testing.T) {
		owner, backing := newFixture(t)
		service := &stateMutatingMemory{backing: backing, owner: owner, dataAddress: 0x200, failSecond: true}
		result := memoryExecutor(t, owner, service).Step(state.ReadContext{})
		requireOutcome(t, result, warp.OutcomeRetired)
		stored, _ := backing.Load32(0x200)
		if result.NextPC != 0x104 || service.dataWrites != 1 || stored != 0xdeadbeef {
			t.Fatalf("coordinated commit result=%+v writes=%d stored=%#x", result, service.dataWrites, stored)
		}
	})
}

func TestMultiLaneStoreRejectsStaleStageBeforeBatchWrite(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = isa.AllLanes
	for lane := range initial.Lanes {
		initial.Lanes[lane].GPR[1] = 0x200 + uint32(lane*8)
		initial.Lanes[lane].GPR[2] = 0x12340000 + uint32(lane)
	}
	owner := newState(t, initial)
	backing := newMemory(t)
	putWord(t, backing, 0x100, storeWord(0, 2, 1, 2))
	service := &stateMutatingMemory{backing: backing, owner: owner, dataAddress: 0x200, mutateOnRead: true}
	beforeMemory := backing.Snapshot()
	result := memoryExecutor(t, owner, service).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	if result.Fault.Kind != warp.FaultEffectValidation || result.NextPC != 0x180 ||
		service.dataWrites != 0 || !bytes.Equal(beforeMemory, backing.Snapshot()) {
		t.Fatalf("multi stale result=%+v writes=%d memory changed=%t", result, service.dataWrites,
			!bytes.Equal(beforeMemory, backing.Snapshot()))
	}
}

func TestLegacyMemoryServiceRejectsMultiAddressStoreWithoutPartialWrite(t *testing.T) {
	initial := initialWarp()
	initial.ActiveMask = 0b0011
	initial.Lanes[0].GPR[1], initial.Lanes[1].GPR[1] = 0x200, 0x208
	initial.Lanes[0].GPR[2], initial.Lanes[1].GPR[2] = 0xaaaaaaaa, 0xbbbbbbbb
	owner := newState(t, initial)
	backing := newMemory(t)
	putWord(t, backing, 0x100, storeWord(0, 2, 1, 2))
	service := &faultMemory{backing: backing, failRead: ^uint32(0), failWrite: ^uint32(0)}
	beforeState, _ := owner.Snapshot()
	beforeMemory := backing.Snapshot()
	result := memoryExecutor(t, owner, service).Step(state.ReadContext{})
	requireOutcome(t, result, warp.OutcomeFault)
	afterState, _ := owner.Snapshot()
	if result.Fault.Kind != warp.FaultArchitectural || beforeState != afterState ||
		!bytes.Equal(beforeMemory, backing.Snapshot()) {
		t.Fatalf("legacy multi-store result=%+v state=%t memory=%t", result, beforeState != afterState,
			!bytes.Equal(beforeMemory, backing.Snapshot()))
	}
}
