package isa

import (
	"encoding/binary"
	"errors"
	"fmt"
	"testing"

	memsupport "vortex.local/simulator/support/memory"
)

func decodedByName(t *testing.T, name string) Decoded {
	t.Helper()
	for _, entry := range catalog {
		if entry.Name == name {
			decoded, err := Decode(entry.Example)
			if err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			return decoded
		}
	}
	t.Fatalf("catalog instruction %q not found", name)
	return Decoded{}
}

func TestIntegerALUFunctionalCoverage(t *testing.T) {
	tests := []struct {
		name string
		pc   uint32
		a    uint32
		b    uint32
		imm  int32
		want uint32
	}{
		{"lui", 0, 0, 0, -2147483648, 0x80000000},
		{"auipc", 0xfffffff0, 0, 0, 0x20, 0x10},
		{"addi", 0, 0xffffffff, 0, 1, 0},
		{"slti", 0, 0xffffffff, 0, 1, 1},
		{"sltiu", 0, 0xffffffff, 0, 1, 0},
		{"xori", 0, 0xaa55aa55, 0, 0x7ff, 0xaa55adaa},
		{"ori", 0, 0x1000, 0, -2048, 0xfffff800},
		{"andi", 0, 0xabcdef01, 0, 0x7ff, 0x701},
		{"slli", 0, 1, 0, 31, 0x80000000},
		{"srli", 0, 0x80000000, 0, 31, 1},
		{"srai", 0, 0x80000000, 0, 31, 0xffffffff},
		{"add", 0, 0xffffffff, 1, 0, 0},
		{"sub", 0, 0, 1, 0, 0xffffffff},
		{"sll", 0, 1, 40, 0, 0x100},
		{"slt", 0, 0xffffffff, 0, 0, 1},
		{"sltu", 0, 0xffffffff, 0, 0, 0},
		{"xor", 0, 0xff00ff00, 0x0ff00ff0, 0, 0xf0f0f0f0},
		{"srl", 0, 0x80000000, 32, 0, 0x80000000},
		{"sra", 0, 0x80000000, 36, 0, 0xf8000000},
		{"or", 0, 0xf0000000, 0x0000000f, 0, 0xf000000f},
		{"and", 0, 0xf0f0f0f0, 0x0ff00ff0, 0, 0x00f000f0},
		{"mul", 0, 0xffffffff, 2, 0, 0xfffffffe},
		{"mulh", 0, 0xfffffffe, 3, 0, 0xffffffff},
		{"mulhsu", 0, 0xfffffffe, 0x80000000, 0, 0xffffffff},
		{"mulhu", 0, 0xffffffff, 0xffffffff, 0, 0xfffffffe},
		{"div", 0, 0xfffffff9, 3, 0, 0xfffffffe},
		{"divu", 0, 7, 3, 0, 2},
		{"rem", 0, 0xfffffff9, 3, 0, 0xffffffff},
		{"remu", 0, 7, 3, 0, 1},
		{"czero.eqz", 0, 0x12345678, 0, 0, 0},
		{"czero.nez", 0, 0x12345678, 1, 0, 0},
	}
	covered := make(map[string]bool, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			if decoded.HasImmediate {
				decoded.Immediate = test.imm
			}
			input := IntegerInput{PC: test.pc, ActiveMask: 1}
			input.RS1[0], input.RS2[0] = test.a, test.b
			effects, err := EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(effects.RegisterWrites) != 1 {
				t.Fatalf("register writes=%v", effects.RegisterWrites)
			}
			write := effects.RegisterWrites[0]
			if write.Mask != 1 || write.Values[0] != test.want {
				t.Fatalf("write=%+v, want lane0=%#x", write, test.want)
			}
			if effects.Control == nil || effects.Control.NextPC != test.pc+4 || effects.Control.Reason != PCSequential {
				t.Fatalf("sequential control=%+v", effects.Control)
			}
			covered[test.name] = true
		})
	}
	assertCatalogFunctionallyCovered(t, covered, CategoryRV32I, CategoryRV32M, CategoryZicond)
}

func assertCatalogFunctionallyCovered(t *testing.T, covered map[string]bool, categories ...Category) {
	t.Helper()
	selected := make(map[Category]bool, len(categories))
	for _, category := range categories {
		selected[category] = true
	}
	for _, entry := range catalog {
		if !selected[entry.Category] || entry.Memory.Kind != MemoryNone || entry.Control != ControlNone {
			continue
		}
		if !covered[entry.Name] {
			t.Errorf("catalog instruction %s has no functional case", entry.Name)
		}
	}
}

func TestMAndZicondBoundaries(t *testing.T) {
	tests := []struct {
		name string
		a    uint32
		b    uint32
		want uint32
	}{
		{"div", 123, 0, 0xffffffff},
		{"divu", 123, 0, 0xffffffff},
		{"rem", 0x81234567, 0, 0x81234567},
		{"remu", 0x81234567, 0, 0x81234567},
		{"div", 0x80000000, 0xffffffff, 0x80000000},
		{"rem", 0x80000000, 0xffffffff, 0},
		{"mulh", 0x80000000, 2, 0xffffffff},
		{"mulhsu", 0x80000000, 0xffffffff, 0x80000000},
		{"mulhu", 0x80000000, 2, 1},
		{"czero.eqz", 0x89abcdef, 1, 0x89abcdef},
		{"czero.nez", 0x89abcdef, 0, 0x89abcdef},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("%02d_%s", i, test.name), func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			input := IntegerInput{ActiveMask: 1}
			input.RS1[0], input.RS2[0] = test.a, test.b
			effects, err := EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if got := effects.RegisterWrites[0].Values[0]; got != test.want {
				t.Fatalf("got %#x, want %#x", got, test.want)
			}
		})
	}
}

func TestRegisterWriteMaskInactiveLanesAndX0(t *testing.T) {
	decoded := decodedByName(t, "add")
	input := IntegerInput{PC: 0x100, ActiveMask: 0b0101}
	input.RS1 = LaneValues{1, 100, 3, 100}
	input.RS2 = LaneValues{10, 100, 30, 100}
	effects, err := EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	write := effects.RegisterWrites[0]
	if write.Mask != 0b0101 || write.Values != (LaneValues{11, 0, 33, 0}) {
		t.Fatalf("inactive lanes affected: %+v", write)
	}

	decoded.Destinations[0].Index = 0
	effects, err = EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.RegisterWrites) != 0 {
		t.Fatalf("x0 write was not suppressed: %+v", effects.RegisterWrites)
	}

	load := decodedByName(t, "lw")
	load.Destinations[0].Index = 0
	completed, err := CompleteMemory(load, 0x100, 1, []MemoryResponse{{
		Request: MemoryRequest{Lane: 0, Kind: MemoryLoad, Width: 4, Signed: true},
		Data:    0xffffffff,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.RegisterWrites) != 0 || completed.Control == nil {
		t.Fatalf("load-to-x0 effects=%+v", completed)
	}
}

func TestImmediateAndPCWraparoundBoundaries(t *testing.T) {
	for _, test := range []struct {
		name string
		pc   uint32
		a    uint32
		imm  int32
		want uint32
	}{
		{"addi", 0, 0x800, -2048, 0},
		{"addi", 0, 0xfffff801, 2047, 0},
		{"auipc", 0xfffff000, 0, 0x2000, 0x1000},
	} {
		decoded := decodedByName(t, test.name)
		decoded.Immediate = test.imm
		input := IntegerInput{PC: test.pc, ActiveMask: 1}
		input.RS1[0] = test.a
		effects, err := EvaluateInteger(decoded, input)
		if err != nil {
			t.Fatal(err)
		}
		if got := effects.RegisterWrites[0].Values[0]; got != test.want {
			t.Errorf("%s result=%#x, want %#x", test.name, got, test.want)
		}
	}
}

func TestBranchTakenNotTakenAndLastActiveLane(t *testing.T) {
	tests := []struct {
		name  string
		a, b  uint32
		taken bool
	}{
		{"beq", 7, 7, true},
		{"bne", 7, 8, true},
		{"blt", 0xffffffff, 0, true},
		{"bge", 0, 0xffffffff, true},
		{"bltu", 0, 0xffffffff, true},
		{"bgeu", 0xffffffff, 0, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			decoded.Immediate = 8
			input := IntegerInput{PC: 0x100, ActiveMask: 0b0101}
			// Lane 0 says the opposite; highest active lane 2 controls the warp.
			setNotTakenBranchOperands(&input, 0, test.name)
			input.RS1[2], input.RS2[2] = test.a, test.b
			effects, err := EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			control := effects.Control
			if control == nil || control.DecisionLane != 2 || control.Taken != test.taken || control.NextPC != 0x108 {
				t.Fatalf("taken control=%+v", control)
			}

			setNotTakenBranchOperands(&input, 2, test.name)
			effects, err = EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if effects.Control.Taken || effects.Control.NextPC != 0x104 {
				t.Fatalf("not-taken control=%+v", effects.Control)
			}
		})
	}
}

func setNotTakenBranchOperands(input *IntegerInput, lane uint8, name string) {
	switch name {
	case "beq":
		input.RS1[lane], input.RS2[lane] = 1, 2
	case "bne":
		input.RS1[lane], input.RS2[lane] = 1, 1
	case "blt":
		input.RS1[lane], input.RS2[lane] = 0, 0xffffffff
	case "bge":
		input.RS1[lane], input.RS2[lane] = 0xffffffff, 0
	case "bltu":
		input.RS1[lane], input.RS2[lane] = 0xffffffff, 0
	case "bgeu":
		input.RS1[lane], input.RS2[lane] = 0, 0xffffffff
	}
}

func TestJumpsPCPlusFourAndAlignment(t *testing.T) {
	jal := decodedByName(t, "jal")
	jal.Immediate = 8
	input := IntegerInput{PC: 0xfffffffc, ActiveMask: 0b1011}
	effects, err := EvaluateInteger(jal, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Control.NextPC != 4 || effects.Control.Target != 4 || effects.RegisterWrites[0].Values != (LaneValues{}) || effects.RegisterWrites[0].Mask != 0b1011 {
		t.Fatalf("JAL wrap/link effects=%+v", effects)
	}

	jalr := decodedByName(t, "jalr")
	jalr.Immediate = 4
	input = IntegerInput{PC: 0x200, ActiveMask: 0b1011}
	input.RS1[0], input.RS1[1], input.RS1[3] = 0x800, 0x900, 0x101
	effects, err = EvaluateInteger(jalr, input)
	if err != nil {
		t.Fatal(err)
	}
	if effects.Control.DecisionLane != 3 || effects.Control.Target != 0x104 || effects.Control.NextPC != 0x104 {
		t.Fatalf("JALR did not use last active lane/clear bit zero: %+v", effects.Control)
	}
	if effects.RegisterWrites[0].Values != (LaneValues{0x204, 0x204, 0x204, 0x204}) || effects.RegisterWrites[0].Mask != 0b1011 {
		t.Fatalf("JALR link write=%+v", effects.RegisterWrites[0])
	}

	jalr.Immediate = 0
	input.RS1[3] = 0x102
	effects, err = EvaluateInteger(jalr, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 1 || effects.Faults[0].Kind != FaultInstructionAddressMisaligned || effects.Control != nil || len(effects.RegisterWrites) != 0 {
		t.Fatalf("misaligned JALR effects=%+v", effects)
	}
}

func TestBranchMisalignmentOnlyWhenTaken(t *testing.T) {
	decoded := decodedByName(t, "beq")
	decoded.Immediate = 2
	input := IntegerInput{PC: 0x100, ActiveMask: 1}
	input.RS1[0], input.RS2[0] = 1, 1
	effects, err := EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 1 || effects.Faults[0].Kind != FaultInstructionAddressMisaligned {
		t.Fatalf("taken misaligned branch=%+v", effects)
	}
	input.RS2[0] = 2
	effects, err = EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 0 || effects.Control == nil || effects.Control.NextPC != 0x104 {
		t.Fatalf("not-taken branch should ignore target alignment: %+v", effects)
	}
}

func TestMemoryInstructionFunctionalCoverage(t *testing.T) {
	loads := []struct {
		name string
		data uint32
		want uint32
	}{
		{"lb", 0x80, 0xffffff80},
		{"lbu", 0x80, 0x80},
		{"lh", 0x8001, 0xffff8001},
		{"lhu", 0x8001, 0x8001},
		{"lw", 0x89abcdef, 0x89abcdef},
	}
	covered := make(map[string]bool)
	for _, test := range loads {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			decoded.Immediate = 0
			input := IntegerInput{PC: 0x40, ActiveMask: 1}
			input.RS1[0] = 4
			issued, err := EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(issued.MemoryRequests) != 1 || issued.MemoryRequests[0].Kind != MemoryLoad || issued.Control != nil {
				t.Fatalf("load request=%+v", issued)
			}
			completed, err := CompleteMemory(decoded, input.PC, input.ActiveMask, []MemoryResponse{{Request: issued.MemoryRequests[0], Data: test.data}})
			if err != nil {
				t.Fatal(err)
			}
			if got := completed.RegisterWrites[0].Values[0]; got != test.want {
				t.Fatalf("load result=%#x, want %#x", got, test.want)
			}
			if completed.Control == nil || completed.Control.NextPC != input.PC+4 {
				t.Fatalf("successful load did not produce PC+4: %+v", completed.Control)
			}
			covered[test.name] = true
		})
	}

	stores := []struct {
		name    string
		address uint32
		value   uint32
		mask    uint8
		data    uint32
	}{
		{"sb", 3, 0x123456ab, 0x8, 0xab000000},
		{"sh", 2, 0x1234cdef, 0xc, 0xcdef0000},
		{"sw", 4, 0x89abcdef, 0xf, 0x89abcdef},
	}
	for _, test := range stores {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodedByName(t, test.name)
			decoded.Immediate = 0
			input := IntegerInput{PC: 0x40, ActiveMask: 1}
			input.RS1[0], input.RS2[0] = test.address, test.value
			effects, err := EvaluateInteger(decoded, input)
			if err != nil {
				t.Fatal(err)
			}
			if len(effects.MemoryRequests) != 1 || effects.Control != nil {
				t.Fatalf("store request=%+v", effects)
			}
			request := effects.MemoryRequests[0]
			if request.Address != test.address || request.AlignedAddress != test.address&^3 || request.ByteMask != test.mask || request.StoreData != test.data {
				t.Fatalf("store request=%+v", request)
			}
			covered[test.name] = true
		})
	}

	for _, entry := range catalog {
		if (entry.Category == CategoryRV32I) && (entry.Memory.Kind == MemoryLoad || entry.Memory.Kind == MemoryStore) && !covered[entry.Name] {
			t.Errorf("catalog memory instruction %s has no functional case", entry.Name)
		}
	}
}

func TestMemoryInactiveAlignmentBoundsAndAddressWrap(t *testing.T) {
	decoded := decodedByName(t, "lh")
	decoded.Immediate = 1
	bounds := AddressBounds{Base: 0, Size: 16}
	input := IntegerInput{PC: 0, ActiveMask: 0b0101, Bounds: &bounds}
	input.RS1 = LaneValues{0, 0, 14, 0}
	effects, err := EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 2 || effects.Faults[0].Kind != FaultLoadAddressMisaligned || effects.Faults[1].Kind != FaultLoadAddressMisaligned {
		t.Fatalf("alignment faults=%+v", effects)
	}

	decoded.Immediate = 0
	input.ActiveMask = 0b0100 // misaligned inactive lane 0 must be ignored
	input.RS1[2] = 16
	effects, err = EvaluateInteger(decoded, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 1 || effects.Faults[0].Kind != FaultLoadAccess || effects.Faults[0].Reason != FaultReasonBounds || effects.Faults[0].Lane != 2 {
		t.Fatalf("bounds fault=%+v", effects)
	}

	byteLoad := decodedByName(t, "lbu")
	byteLoad.Immediate = 1
	input = IntegerInput{ActiveMask: 1}
	input.RS1[0] = 0xffffffff
	effects, err = EvaluateInteger(byteLoad, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.MemoryRequests) != 1 || effects.MemoryRequests[0].Address != 0 {
		t.Fatalf("32-bit address did not wrap: %+v", effects)
	}

	store := decodedByName(t, "sh")
	store.Immediate = 0
	input = IntegerInput{ActiveMask: 1, Bounds: &bounds}
	input.RS1[0] = 1
	effects, err = EvaluateInteger(store, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 1 || effects.Faults[0].Kind != FaultStoreAddressMisaligned {
		t.Fatalf("store alignment fault=%+v", effects)
	}

	store = decodedByName(t, "sw")
	store.Immediate = 0
	input.RS1[0] = 16
	effects, err = EvaluateInteger(store, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(effects.Faults) != 1 || effects.Faults[0].Kind != FaultStoreAccess || effects.Faults[0].Reason != FaultReasonBounds {
		t.Fatalf("store bounds fault=%+v", effects)
	}
}

func TestMemoryOwnerIntegrationAndNoISAMutation(t *testing.T) {
	memory, err := memsupport.New(16)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Store32(4, 0x80ff7f01); err != nil {
		t.Fatal(err)
	}

	load := decodedByName(t, "lb")
	load.Immediate = 0
	input := IntegerInput{PC: 0x20, ActiveMask: 1}
	input.RS1[0] = 6
	issued, err := EvaluateInteger(load, input)
	if err != nil {
		t.Fatal(err)
	}
	response := serveMemoryRequest(memory, issued.MemoryRequests[0])
	completed, err := CompleteMemory(load, input.PC, input.ActiveMask, []MemoryResponse{response})
	if err != nil {
		t.Fatal(err)
	}
	if completed.RegisterWrites[0].Values[0] != 0xffffffff {
		t.Fatalf("external load result=%#x", completed.RegisterWrites[0].Values[0])
	}

	store := decodedByName(t, "sh")
	store.Immediate = 0
	input.RS1[0], input.RS2[0] = 2, 0xabcd
	before := memory.Snapshot()
	issued, err = EvaluateInteger(store, input)
	if err != nil {
		t.Fatal(err)
	}
	if got := memory.Snapshot(); string(got) != string(before) {
		t.Fatal("ISA evaluator mutated canonical memory")
	}
	response = serveMemoryRequest(memory, issued.MemoryRequests[0])
	if response.Fault != FaultNone {
		t.Fatalf("external store failed: %+v", response)
	}
	storeCompleted, err := CompleteMemory(store, input.PC, input.ActiveMask, []MemoryResponse{response})
	if err != nil {
		t.Fatal(err)
	}
	if len(storeCompleted.RegisterWrites) != 0 || len(storeCompleted.Faults) != 0 || storeCompleted.Control == nil || storeCompleted.Control.NextPC != input.PC+4 {
		t.Fatalf("successful store completion has extra effects: %+v", storeCompleted)
	}
	after := memory.Snapshot()
	if after[2] != 0xcd || after[3] != 0xab {
		t.Fatalf("external owner store bytes=%x", after[:4])
	}

	wordLoad := decodedByName(t, "lw")
	wordLoad.Immediate = 0
	input.RS1[0] = 16
	issued, err = EvaluateInteger(wordLoad, input)
	if err != nil {
		t.Fatal(err)
	}
	response = serveMemoryRequest(memory, issued.MemoryRequests[0])
	completed, err = CompleteMemory(wordLoad, input.PC, input.ActiveMask, []MemoryResponse{response})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Faults) != 1 || completed.Faults[0].Kind != FaultLoadAccess || completed.Faults[0].Reason != FaultReasonBounds || len(completed.RegisterWrites) != 0 {
		t.Fatalf("owner bounds fault=%+v", completed)
	}
}

func serveMemoryRequest(memory *memsupport.Memory, request MemoryRequest) MemoryResponse {
	response := MemoryResponse{Request: request}
	if request.Kind == MemoryLoad {
		data, err := memory.ReadBytes(request.Address, uint64(request.Width))
		if err != nil {
			response.Fault = FaultLoadAccess
			response.Reason = FaultReasonBounds
			return response
		}
		var word [4]byte
		copy(word[:], data)
		response.Data = binary.LittleEndian.Uint32(word[:])
		return response
	}
	word, err := memory.Load32(request.AlignedAddress)
	if err != nil {
		response.Fault = FaultStoreAccess
		response.Reason = FaultReasonBounds
		return response
	}
	for i := uint8(0); i < 4; i++ {
		if request.ByteMask&(1<<i) != 0 {
			mask := uint32(0xff) << (8 * i)
			word = (word &^ mask) | (request.StoreData & mask)
		}
	}
	if err := memory.Store32(request.AlignedAddress, word); err != nil {
		response.Fault = FaultStoreAccess
		response.Reason = FaultReasonBounds
	}
	return response
}

func TestMemoryCompletionSuppressesPartialWriteOnFault(t *testing.T) {
	decoded := decodedByName(t, "lw")
	requests := []MemoryRequest{
		{Lane: 0, Kind: MemoryLoad, Address: 0, Width: 4, Signed: true},
		{Lane: 1, Kind: MemoryLoad, Address: 16, Width: 4, Signed: true},
	}
	completed, err := CompleteMemory(decoded, 0x100, 0b0011, []MemoryResponse{
		{Request: requests[0], Data: 0x12345678},
		{Request: requests[1], Fault: FaultLoadAccess, Reason: FaultReasonMemoryService},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Faults) != 1 || len(completed.RegisterWrites) != 0 {
		t.Fatalf("partial completion leaked: %+v", completed)
	}
}

func TestFenceOrderingEffect(t *testing.T) {
	word := uint32(0xa5)<<20 | opFence
	decoded, err := Decode(word)
	if err != nil {
		t.Fatal(err)
	}
	effects, err := EvaluateInteger(decoded, IntegerInput{PC: 0xfffffffc, ActiveMask: AllLanes})
	if err != nil {
		t.Fatal(err)
	}
	if effects.Ordering == nil || effects.Ordering.Predecessor != 0xa || effects.Ordering.Successor != 5 || effects.Control == nil || effects.Control.NextPC != 0 {
		t.Fatalf("fence effects=%+v", effects)
	}
	if len(effects.MemoryRequests) != 0 || len(effects.RegisterWrites) != 0 {
		t.Fatalf("fence introduced non-ordering effects: %+v", effects)
	}
}

func TestEvaluationInputErrorsAreNotArchitecturalFaults(t *testing.T) {
	decoded := decodedByName(t, "beq")
	_, err := EvaluateInteger(decoded, IntegerInput{ActiveMask: 0})
	var evalErr *EvaluationError
	if !errors.As(err, &evalErr) {
		t.Fatalf("empty control mask error=%T %v", err, err)
	}

	decoded = decodedByName(t, "add")
	_, err = EvaluateInteger(decoded, IntegerInput{ActiveMask: 0x80})
	if !errors.As(err, &evalErr) {
		t.Fatalf("invalid lane mask error=%T %v", err, err)
	}

	badBounds := AddressBounds{Base: 0xfffffff0, Size: 32}
	_, err = EvaluateInteger(decoded, IntegerInput{ActiveMask: 1, Bounds: &badBounds})
	if !errors.As(err, &evalErr) {
		t.Fatalf("invalid bounds error=%T %v", err, err)
	}

	floatDecoded := decodedByName(t, "fadd.s")
	_, err = EvaluateInteger(floatDecoded, IntegerInput{ActiveMask: 1})
	if !errors.As(err, &evalErr) {
		t.Fatalf("out-of-scope instruction error=%T %v", err, err)
	}
}
