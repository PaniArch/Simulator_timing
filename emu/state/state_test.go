package state_test

import (
	"strings"
	"testing"

	"vortex.local/simulator/emu/state"
	"vortex.local/simulator/isa"
)

func baseInitial() state.WarpInitial {
	lanes := make([]state.LaneInitial, isa.FrozenLaneCount)
	for lane := range lanes {
		lanes[lane].ID = uint8(lane)
		for register := 1; register < state.FrozenRegisterCount; register++ {
			lanes[lane].GPR[register] = uint32(0x10000*lane + register)
		}
		for register := 0; register < state.FrozenRegisterCount; register++ {
			lanes[lane].FPR[register] = uint32(0xf0000000 | 0x10000*lane | register)
		}
	}
	return state.WarpInitial{
		Topology: state.FrozenTopology(), WarpID: 2, PC: 0x100,
		ActiveMask: 0b0101, Lifecycle: state.WarpRunning,
		SavedThreadMask: 0b1011, FCSR: 2<<5 | 0x11,
		TrapCSRs: state.TrapCSRState{
			MStatus: 1, MTVec: 0x200, MScratch: 3,
			MEPC: 0x300, MCause: 5, MTVal: 6,
		},
		Divergence: state.DivergenceInitial{
			WritePointer: 2,
			Records: []state.DivergenceRecordInitial{
				{Pointer: 0, OriginalMask: 0b0011, NextPC: 0x180},
				{Pointer: 1, OriginalMask: 0b1111, NextPC: 0x1c0, ElseVisited: true},
			},
		},
		Lanes: lanes,
	}
}

func newWarp(t *testing.T) *state.WarpState {
	t.Helper()
	w, err := state.NewWarp(baseInitial())
	if err != nil {
		t.Fatalf("NewWarp: %v", err)
	}
	return w
}

func decoded(t *testing.T, name string) isa.Decoded {
	t.Helper()
	for _, entry := range isa.Catalog() {
		if entry.Name != name {
			continue
		}
		result, err := isa.Decode(entry.Example)
		if err != nil {
			t.Fatalf("Decode(%s): %v", name, err)
		}
		return result
	}
	t.Fatalf("catalog has no %s", name)
	return isa.Decoded{}
}

func TestNewWarpValidatesEveryFrozenInitializationBoundary(t *testing.T) {
	tests := []struct {
		name   string
		change func(*state.WarpInitial)
		want   string
	}{
		{"topology", func(i *state.WarpInitial) { i.Topology.LaneCount++ }, "topology"},
		{"warp-id", func(i *state.WarpInitial) { i.WarpID = isa.FrozenWarpCount }, "warp id"},
		{"pc", func(i *state.WarpInitial) { i.PC++ }, "aligned"},
		{"active-mask", func(i *state.WarpInitial) { i.ActiveMask = 0x80 }, "lane mask"},
		{"saved-mask", func(i *state.WarpInitial) { i.SavedThreadMask = 0x80 }, "lane mask"},
		{"lifecycle", func(i *state.WarpInitial) { i.Lifecycle = state.WarpInactive }, "inactive warp"},
		{"fcsr", func(i *state.WarpInitial) { i.FCSR = 0x100 }, "FCSR"},
		{"lane-count", func(i *state.WarpInitial) { i.Lanes = i.Lanes[:3] }, "lane initializers"},
		{"lane-id", func(i *state.WarpInitial) { i.Lanes[3].ID = 2 }, "lane id"},
		{"x0", func(i *state.WarpInitial) { i.Lanes[0].GPR[0] = 1 }, "x0"},
		{"stack-pointer", func(i *state.WarpInitial) { i.Divergence.WritePointer = 4 }, "write pointer"},
		{"stack-prefix", func(i *state.WarpInitial) { i.Divergence.Records = i.Divergence.Records[:1] }, "live records"},
		{"stack-record-mask", func(i *state.WarpInitial) { i.Divergence.Records[1].OriginalMask = 0 }, "original mask"},
		{"stack-record-pc", func(i *state.WarpInitial) { i.Divergence.Records[1].NextPC++ }, "aligned"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := baseInitial()
			test.change(&initial)
			if _, err := state.NewWarp(initial); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewWarp error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRegisterNamespacesX0MasksAndInactiveLanes(t *testing.T) {
	w := newWarp(t)
	gpr, err := w.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
	if err != nil {
		t.Fatal(err)
	}
	fpr, err := w.ReadRegister(isa.Register{File: isa.Float, Index: 7})
	if err != nil {
		t.Fatal(err)
	}
	for lane := uint8(0); lane < isa.FrozenLaneCount; lane++ {
		if gpr[lane] != uint32(lane)*0x10000+7 || fpr[lane] != 0xf0000000|uint32(lane)*0x10000|7 {
			t.Fatalf("lane %d namespaces: gpr=%#x fpr=%#x", lane, gpr[lane], fpr[lane])
		}
	}

	// Lane 1 is inactive, but the explicit effect mask is authoritative.
	values := isa.LaneValues{10, 11, 12, 13}
	if err := w.WriteRegister(isa.Register{File: isa.Integer, Index: 7}, 0b0010, values); err != nil {
		t.Fatal(err)
	}
	gpr, _ = w.ReadRegister(isa.Register{File: isa.Integer, Index: 7})
	if gpr != (isa.LaneValues{7, 11, 0x20007, 0x30007}) {
		t.Fatalf("explicit inactive-lane write produced %x", gpr)
	}

	if err := w.WriteRegister(isa.Register{File: isa.Integer, Index: 0}, isa.AllLanes, values); err != nil {
		t.Fatal(err)
	}
	x0, _ := w.ReadRegister(isa.Register{File: isa.Integer, Index: 0})
	if x0 != (isa.LaneValues{}) {
		t.Fatalf("x0 changed to %x", x0)
	}
	if err := w.WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 0x80, values); err == nil {
		t.Fatal("invalid lane mask was accepted")
	}
	if _, err := w.ReadRegister(isa.Register{File: isa.Integer, Index: 32}); err == nil {
		t.Fatal("invalid register index was accepted")
	}
	if _, err := w.ReadRegister(isa.Register{File: isa.RegisterFile(9), Index: 1}); err == nil {
		t.Fatal("invalid register namespace was accepted")
	}
}

func TestSnapshotAndInitializationInputsCannotModifyCanonicalState(t *testing.T) {
	initial := baseInitial()
	w, err := state.NewWarp(initial)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := w.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Mutating caller-owned initialization slices after construction is inert.
	initial.Lanes[0].GPR[1] = 0xdead
	initial.Divergence.Records[0].OriginalMask = 1
	current, _ := w.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	if current[0] != 1 {
		t.Fatalf("initializer alias changed state: %#x", current[0])
	}

	// A snapshot remains detached when canonical state later changes.
	if err := w.WriteRegister(isa.Register{File: isa.Integer, Index: 1}, 1, isa.LaneValues{0xbeef}); err != nil {
		t.Fatal(err)
	}
	old, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	old[0] = 0xcafe // this mutates only the returned array value
	stillOld, _ := snapshot.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	current, _ = w.ReadRegister(isa.Register{File: isa.Integer, Index: 1})
	if stillOld[0] != 1 || current[0] != 0xbeef {
		t.Fatalf("snapshot/canonical isolation failed: old=%#x current=%#x", stillOld[0], current[0])
	}
	record, err := snapshot.DivergenceRecord(0)
	if err != nil || !record.Valid || record.OriginalMask != 0b0011 {
		t.Fatalf("detached divergence record=%+v err=%v", record, err)
	}
}

func TestISAViewsReadDecodedNamespacesAndWarpFacts(t *testing.T) {
	w := newWarp(t)
	snapshot, _ := w.Snapshot()
	context := state.ReadContext{CoreID: 0, ActiveWarps: 0b1101}

	integerInput, err := snapshot.IntegerInput(decoded(t, "add"), context)
	if err != nil {
		t.Fatal(err)
	}
	if integerInput.PC != 0x100 || integerInput.ActiveMask != 0b0101 || integerInput.RS1[2] != 0x20001 || integerInput.RS2[2] != 0x20002 {
		t.Fatalf("integer input=%+v", integerInput)
	}

	// FSW has an integer RS1 and floating RS2; decoded namespaces, rather than
	// a single untyped register array, select each operand.
	floatInput, err := snapshot.FloatInput(decoded(t, "fsw"), context)
	if err != nil {
		t.Fatal(err)
	}
	if floatInput.RS1[2] != 0x20001 || floatInput.RS2[2] != 0xf0020002 || floatInput.FRM != isa.RDN {
		t.Fatalf("float input namespace/FCSR mapping=%+v", floatInput)
	}

	systemInput, err := snapshot.SystemInput(decoded(t, "csrrs"), context)
	if err != nil {
		t.Fatal(err)
	}
	if systemInput.CSR.WarpID != 2 || systemInput.CSR.ThreadMask != 0b0101 || systemInput.CSR.SavedThreadMask != 0b1011 ||
		systemInput.CSR.FCSR != 0x51 || systemInput.CSR.MTVec != 0x200 || systemInput.CSR.MScratch != 3 {
		t.Fatalf("system canonical view=%+v", systemInput.CSR)
	}
}

func TestCustomViewReadsAddressedDivergenceRecord(t *testing.T) {
	w := newWarp(t)
	join := decoded(t, "join")
	// Catalog examples use x1. The highest active lane (2) controls JOIN.
	values, _ := w.ReadRegister(join.Sources[0])
	values[2] = 1
	if err := w.WriteRegister(join.Sources[0], 1<<2, values); err != nil {
		t.Fatal(err)
	}
	input, err := w.CustomInput(join, state.ReadContext{})
	if err != nil {
		t.Fatal(err)
	}
	if input.Divergence.WritePointer != 2 || !input.Divergence.Record.Valid ||
		input.Divergence.Record.Pointer != 1 || input.Divergence.Record.OriginalMask != isa.AllLanes ||
		input.Divergence.Record.NextPC != 0x1c0 || !input.Divergence.Record.ElseVisited {
		t.Fatalf("JOIN divergence view=%+v", input.Divergence)
	}
}

func TestExternalContextIsCopiedAndNeverRetained(t *testing.T) {
	w := newWarp(t)
	bounds := isa.AddressBounds{Base: 0x1000, Size: 0x100}
	context := state.ReadContext{
		CoreID: 0, ActiveWarps: 0b0011, Bounds: &bounds,
		CTA:          isa.CTAView{ID: 7, ThreadCoordinates: [3]isa.LaneValues{{10, 11, 12, 13}}},
		Counters:     isa.CounterView{Cycle: 9, Instret: 8},
		BarrierPhase: true, PendingPriorWork: true, PendingLSU: true,
	}
	integerInput, err := w.IntegerInput(decoded(t, "lw"), context)
	if err != nil {
		t.Fatal(err)
	}
	systemInput, err := w.SystemInput(decoded(t, "csrrs"), context)
	if err != nil {
		t.Fatal(err)
	}
	customInput, err := w.CustomInput(decoded(t, "bar"), context)
	if err != nil {
		t.Fatal(err)
	}

	bounds.Base = 0x9000
	context.CTA.ID = 99
	context.CTA.ThreadCoordinates[0][0] = 99
	context.Counters.Cycle = 99
	context.BarrierPhase = false
	if integerInput.Bounds == &bounds || integerInput.Bounds.Base != 0x1000 ||
		systemInput.CSR.CTA.ID != 7 || systemInput.CSR.CTA.ThreadCoordinates[0][0] != 10 || systemInput.CSR.Counters.Cycle != 9 ||
		!customInput.BarrierPhase || !customInput.PendingPriorWork || !customInput.PendingLSU {
		t.Fatalf("external context leaked into snapshots: int=%+v system=%+v custom=%+v", integerInput, systemInput, customInput)
	}

	// A later call sees the newly supplied context value; the warp did not cache
	// either the old or new external owner fact.
	second, err := w.IntegerInput(decoded(t, "lw"), context)
	if err != nil || second.Bounds.Base != 0x9000 {
		t.Fatalf("second context snapshot=%+v err=%v", second, err)
	}
}

func TestCanonicalPCMaskFCSRAndDivergenceMutationValidation(t *testing.T) {
	w := newWarp(t)
	if err := w.SetPC(0x222); err == nil {
		t.Fatal("misaligned PC update accepted")
	}
	if err := w.SetActiveMask(0, state.WarpRunning); err == nil {
		t.Fatal("empty running mask accepted")
	}
	if err := w.SetFCSR(0x100); err == nil {
		t.Fatal("oversized FCSR accepted")
	}
	bad := state.DivergenceInitial{WritePointer: 1, Records: []state.DivergenceRecordInitial{{Pointer: 0, OriginalMask: 1, NextPC: 3}}}
	if err := w.SetDivergence(bad); err == nil {
		t.Fatal("misaligned divergence PC accepted")
	}

	before, _ := w.Snapshot()
	if before.PC() != 0x100 || before.ActiveMask() != 0b0101 || before.Lifecycle() != state.WarpRunning || before.FCSR() != 0x51 || before.DivergenceWritePointer() != 2 {
		t.Fatalf("unexpected snapshot facts")
	}
	if err := w.SetPC(0x400); err != nil {
		t.Fatal(err)
	}
	if err := w.SetActiveMask(0, state.WarpInactive); err != nil {
		t.Fatal(err)
	}
	after, _ := w.Snapshot()
	if after.PC() != 0x400 || after.ActiveMask() != 0 || after.Lifecycle() != state.WarpInactive || before.PC() != 0x100 {
		t.Fatalf("canonical/snapshot facts before=%#x after=%#x", before.PC(), after.PC())
	}
}
