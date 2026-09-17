// Package vortexruntime adapts the native Vortex command-processor launch ABI
// to the runtime-independent Simulator device layer.
package vortexruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"vortex.local/simulator/emu/device"
	"vortex.local/simulator/isa"
	"vortex.local/simulator/support/memory"
	"vortex.local/simulator/timing/memsys"
	"vortex.local/simulator/timing/runner"
)

const (
	dcrCacheFlush = 0x000
	dcrMPMValue   = 0x001

	dcrStartupAddr0 = 0x010
	dcrStartupAddr1 = 0x011
	dcrKernelEntry0 = 0x012
	dcrKernelEntry1 = 0x013
	dcrStartupArg0  = 0x014
	dcrStartupArg1  = 0x015
	dcrBlockDimX    = 0x016
	dcrBlockDimY    = 0x017
	dcrBlockDimZ    = 0x018
	dcrGridDimX     = 0x019
	dcrGridDimY     = 0x01a
	dcrGridDimZ     = 0x01b
	dcrLocalMemSize = 0x01c
	dcrBlockSize    = 0x01d
	dcrWarpStepX    = 0x01e
	dcrWarpStepY    = 0x01f
	dcrWarpStepZ    = 0x020
	dcrClusterDimX  = 0x021
	dcrClusterDimY  = 0x022
	dcrClusterDimZ  = 0x023

	defaultStepChunk = uint64(1_000_000)
	devicePageSize   = uint32(4096)
	unwrittenWord    = uint32(0xbaadf00d)
)

// ErrorOrigin identifies whether a failure was produced by the simulator's
// architectural execution or by the native-runtime connection around it.
type ErrorOrigin string

const (
	OriginSimulator  ErrorOrigin = "simulator-internal"
	OriginConnection ErrorOrigin = "external-connection"
)

// ClassifiedError is stable across the C ABI and benchmark audit records.
type ClassifiedError struct {
	Origin ErrorOrigin
	Op     string
	Err    error
}

func (e *ClassifiedError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("origin=%s op=%s: %v", e.Origin, e.Op, e.Err)
}

func (e *ClassifiedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// RunSummary is the detached result of the most recent native launch.
type RunSummary struct {
	DeviceID         uint64           `json:"trace_device_id,omitempty"`
	LaunchID         uint64           `json:"trace_launch_id,omitempty"`
	HardwareCounters *isa.CounterView `json:"hardware_counters"` // nil means unavailable; device cumulative

	Mode           Mode                 `json:"mode"`
	Cycles         uint64               `json:"execution_cycles"`
	FlushCycles    uint64               `json:"flush_cycles"`
	BackingVisible bool                 `json:"backing_visible"`
	Sequence       uint64               `json:"sequence"`
	Launch         device.LaunchState   `json:"launch"`
	Outcome        device.KernelOutcome `json:"outcome"`
	Attempts       uint64               `json:"attempts"`
	Retired        uint64               `json:"retired"`
	Generated      uint32               `json:"generated"`
	Admitted       uint32               `json:"admitted"`
	Completed      uint32               `json:"completed"`
	Error          string               `json:"error,omitempty"`
	Origin         ErrorOrigin          `json:"error_origin,omitempty"`
}

type auditRecord struct {
	Time    string      `json:"time"`
	Event   string      `json:"event"`
	Summary *RunSummary `json:"summary,omitempty"`
	Error   string      `json:"error,omitempty"`
	Origin  ErrorOrigin `json:"error_origin,omitempty"`
}

// Device owns the one canonical sparse device-memory image and the native
// launch adapter. Existing ISA/Warp/Core/Device owners remain unchanged.
type Device struct {
	mu     sync.Mutex
	mode   Mode
	kernel *runner.Kernel // retained until native CP explicitly flushes D and I

	memory *memory.Sparse
	dcr    [0x1000]uint32

	stepChunk uint64
	sequence  uint64
	busy      bool
	busyLatch bool
	closed    bool
	lastError *ClassifiedError
	lastRun   RunSummary
	auditPath string
	auditMu   sync.Mutex                           // serializes whole records, independent of the state lock
	auditOpen func(string) (io.WriteCloser, error) // optional per-device test seam
}

// NewDevice selects timing by default; functional is an explicit alternative.
func NewDevice() (*Device, error) {
	mode := Mode(os.Getenv("SIMTIMING_MODE"))
	if mode == "" {
		mode = Timing
	}
	return NewDeviceWithMode(mode)
}

type Mode string

const (
	Timing     Mode = "timing"
	Functional Mode = "functional"
)

func NewDeviceWithMode(mode Mode) (*Device, error) {
	if mode != Timing && mode != Functional {
		return nil, fmt.Errorf("invalid SIMTIMING_MODE %q", mode)
	}
	backing, err := memory.NewSparse(uint64(1)<<32, devicePageSize, unwrittenWord)
	if err != nil {
		return nil, err
	}
	return &Device{
		mode:      mode,
		memory:    backing,
		stepChunk: defaultStepChunk,
		auditPath: os.Getenv("SIMTIMING_EVENT_LOG"),
	}, nil
}

// Close prevents new launches. A caller must wait for Busy to become false.
func (d *Device) Close() error {
	if d == nil {
		return fmt.Errorf("nil runtime device")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busy {
		return fmt.Errorf("cannot close while a kernel is running")
	}
	d.closed = true
	return nil
}

// Memory returns the canonical sparse backing owner.
func (d *Device) Memory() *memory.Sparse {
	if d == nil {
		return nil
	}
	return d.memory
}

func (d *Device) connectionError(op string, err error) error {
	classified := &ClassifiedError{Origin: OriginConnection, Op: op, Err: err}
	d.mu.Lock()
	d.lastError = combineErrors(d.lastError, classified)
	d.mu.Unlock()
	auditErr := d.audit(auditRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "error", Error: classified.Error(), Origin: classified.Origin})
	d.mu.Lock()
	d.lastError = combineErrors(d.lastError, auditFailure(auditErr))
	d.mu.Unlock()
	return combineErrors(classified, auditFailure(auditErr))
}

// RecordConnectionError exposes C-ABI and backend transport failures through
// the same stable error channel as launch failures.
func (d *Device) RecordConnectionError(op string, err error) error {
	if d == nil {
		return err
	}
	return d.connectionError(op, err)
}

// WriteDCR records one native command-processor DCR write.
func (d *Device) WriteDCR(address, value uint32) error {
	if d == nil {
		return fmt.Errorf("nil runtime device")
	}
	if address >= uint32(len(d.dcr)) {
		return d.connectionError("dcr-write", fmt.Errorf("address %#x is outside the 12-bit DCR space", address))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return fmt.Errorf("runtime device is closed")
	}
	d.dcr[address] = value
	return nil
}

// ReadDCR connects cache control, hardware MPM snapshots and stored DCRs.
func (d *Device) ReadDCR(address, tag uint32) (uint32, error) {
	if d == nil {
		return 0, fmt.Errorf("nil runtime device")
	}
	if address >= uint32(len(d.dcr)) {
		return 0, d.connectionError("dcr-read", fmt.Errorf("address %#x is outside the 12-bit DCR space", address))
	}
	if address == dcrCacheFlush {
		return d.flush(tag)
	}
	if address == dcrMPMValue {
		return d.readMPM(tag)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dcr[address], nil
}

func (d *Device) launchLocked() (device.LaunchState, error) {
	if d.dcr[dcrStartupAddr1] != 0 || d.dcr[dcrKernelEntry1] != 0 || d.dcr[dcrStartupArg1] != 0 {
		return device.LaunchState{}, fmt.Errorf("RV32 backend received non-zero high address words startup=%#x entry=%#x args=%#x", d.dcr[dcrStartupAddr1], d.dcr[dcrKernelEntry1], d.dcr[dcrStartupArg1])
	}
	return device.LaunchState{
		StartupPC:         d.dcr[dcrStartupAddr0],
		KernelEntryPC:     d.dcr[dcrKernelEntry0],
		ParameterAddress:  d.dcr[dcrStartupArg0],
		BlockDimensions:   [3]uint32{d.dcr[dcrBlockDimX], d.dcr[dcrBlockDimY], d.dcr[dcrBlockDimZ]},
		GridDimensions:    [3]uint32{d.dcr[dcrGridDimX], d.dcr[dcrGridDimY], d.dcr[dcrGridDimZ]},
		LocalMemorySize:   d.dcr[dcrLocalMemSize],
		BlockSize:         d.dcr[dcrBlockSize],
		WarpStep:          [3]uint32{d.dcr[dcrWarpStepX], d.dcr[dcrWarpStepY], d.dcr[dcrWarpStepZ]},
		ClusterDimensions: [3]uint32{d.dcr[dcrClusterDimX], d.dcr[dcrClusterDimY], d.dcr[dcrClusterDimZ]},
	}, nil
}

// Start snapshots the complete launch DCR bank and begins asynchronous kernel
// execution. Busy is guaranteed to be observed true at least once by the CP.
func (d *Device) Start() error {
	if d == nil {
		return fmt.Errorf("nil runtime device")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return d.connectionError("launch-start", fmt.Errorf("runtime device is closed"))
	}
	if d.busy || d.busyLatch {
		d.mu.Unlock()
		return d.connectionError("launch-start", fmt.Errorf("a kernel is already active"))
	}
	if d.lastError != nil || (d.kernel != nil && !d.lastRun.BackingVisible) {
		d.mu.Unlock()
		return d.connectionError("launch-start", fmt.Errorf("previous execution failed or requires native CACHE_FLUSH"))
	}
	launch, err := d.launchLocked()
	if err != nil {
		d.mu.Unlock()
		return d.connectionError("launch-descriptor", err)
	}
	normalized, err := device.ValidateLaunch(launch)
	if err != nil {
		d.mu.Unlock()
		return d.connectionError("launch-descriptor", err)
	}
	d.sequence++
	sequence := d.sequence
	d.lastError = nil
	d.busy = true
	d.busyLatch = true
	d.mu.Unlock()

	if err := d.audit(auditRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "launch-start", Summary: &RunSummary{Sequence: sequence, Launch: normalized, Mode: d.mode}}); err != nil {
		// No execution is started when its launch cannot be recorded.
		return d.publish(RunSummary{Sequence: sequence, Launch: normalized, Mode: d.mode, Outcome: device.KernelFault}, auditFailure(err))
	}
	go d.run(sequence, normalized)
	return nil
}

func (d *Device) run(sequence uint64, launch device.LaunchState) {
	summary := RunSummary{Sequence: sequence, Launch: launch, Mode: d.mode}
	var err error
	if d.mode == Timing {
		err = d.runTiming(launch, &summary)
	} else {
		err = d.runFunctional(launch, &summary)
		summary.BackingVisible = err == nil
	}
	var classified *ClassifiedError
	if err != nil {
		classified = &ClassifiedError{Origin: OriginSimulator, Op: "kernel-execution", Err: err}
		summary.Error = classified.Error()
		summary.Origin = classified.Origin
	}
	auditErr := d.audit(auditRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "launch-finish", Summary: &summary, Error: summary.Error, Origin: summary.Origin})
	d.publish(summary, combineErrors(classified, auditFailure(auditErr)))
}

func (d *Device) runFunctional(launch device.LaunchState, summary *RunSummary) error {
	executor, err := device.NewKernelExecutor(launch, d.memory)
	if err == nil {
		var execution *device.KernelExecution
		execution, err = executor.NewExecution()
		for err == nil {
			result := execution.Run(device.KernelRunOptions{StepBudget: d.stepChunk})
			summary.Outcome = result.Outcome
			summary.Attempts += result.Attempts
			summary.Retired += result.Retired
			summary.Generated += result.Generated
			summary.Admitted += result.Admitted
			summary.Completed += result.Completed
			if result.Outcome == device.KernelBudgetExceeded && errors.As(result.Err, new(*device.KernelBudgetExceededError)) {
				continue
			}
			if result.Outcome != device.KernelComplete {
				err = result.Err
				if err == nil {
					err = fmt.Errorf("kernel stopped with outcome %s", result.Outcome)
				}
			}
			break
		}
	}

	return err
}

func (d *Device) runTiming(launch device.LaunchState, summary *RunSummary) error {
	config, err := memsys.DefaultConfig()
	if err != nil {
		return err
	}
	// Only the external service latency is selected here, never cache geometry.
	config.Latency = 100
	var k *runner.Kernel
	if d.kernel == nil {
		k, err = runner.NewKernel(launch, d.memory, runner.Options{Backend: "std", PeriodPS: 1, MemoryConfig: &config})
	} else {
		k, err = d.kernel.NextLaunch(launch)
	}
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.kernel = k
	d.mu.Unlock()
	for {
		err = k.Run(d.stepChunk, func(r runner.MultiRecord) {
			summary.Retired = 0
			for _, n := range r.Retired {
				summary.Retired += n
			}
		})
		status := k.Status()
		summary.DeviceID, summary.LaunchID = status.DeviceID, status.LaunchID
		counters := k.Counters()
		summary.HardwareCounters = &counters
		summary.Cycles, summary.Generated, summary.Completed = status.LaunchCycles, status.Generated, status.Completed
		for _, e := range k.TakeEvents() {
			if e.Kind == "admitted" {
				summary.Admitted++
			}
		}
		if err != nil {
			summary.Outcome = device.KernelFault
			return err
		}
		if status.Complete {
			summary.Outcome = device.KernelComplete
			return nil
		}
		// Budget exhaustion preserves the exact Kernel and all memory state.
	}
}

// The native CP's DCR read is synchronous: acknowledge only after D writeback
// and I invalidation have completed. Execution completion alone is not visibility.
func (d *Device) flush(tag uint32) (uint32, error) {
	d.mu.Lock()
	if d.closed || d.busy || d.busyLatch || tag != 0 || d.lastError != nil {
		d.mu.Unlock()
		return 0, d.connectionError("cache-flush", fmt.Errorf("requires idle healthy single core (tag=%d)", tag))
	}
	k := d.kernel
	if d.mode == Functional || k == nil {
		d.mu.Unlock()
		return 0, nil
	}
	d.busy = true
	d.mu.Unlock()
	before := k.Status().Cycle
	var err error
	for {
		var done bool
		done, err = k.FlushCaches(d.stepChunk)
		if done || err != nil {
			break
		}
	}
	d.mu.Lock()
	summary := d.lastRun
	d.mu.Unlock()
	counters := k.Counters()
	summary.HardwareCounters = &counters
	summary.FlushCycles += k.Status().Cycle - before
	summary.BackingVisible = err == nil && k.Status().BackingVisible
	var classified *ClassifiedError
	if err != nil {
		classified = &ClassifiedError{Origin: OriginSimulator, Op: "cache-flush", Err: err}
		summary.Error, summary.Origin = classified.Error(), classified.Origin
	}
	auditErr := d.audit(auditRecord{Time: time.Now().UTC().Format(time.RFC3339Nano), Event: "cache-flush", Summary: &summary, Error: summary.Error, Origin: summary.Origin})
	return 0, d.publish(summary, combineErrors(classified, auditFailure(auditErr)))
}

// Busy supplies the CP launch handshake. The first observation after Start is
// true even if a tiny kernel completed before the CP's next tick.
func (d *Device) Busy() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.busyLatch {
		d.busyLatch = false
		return true
	}
	return d.busy
}

// LastError returns a detached stable error string.
func (d *Device) LastError() string {
	if d == nil {
		return "origin=external-connection op=device: nil runtime device"
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastError == nil {
		return ""
	}
	return d.lastError.Error()
}

// LastRun returns the detached most recent launch summary.
func (d *Device) LastRun() RunSummary {
	if d == nil {
		return RunSummary{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	summary := d.lastRun
	if summary.HardwareCounters != nil {
		counters := *summary.HardwareCounters
		summary.HardwareCounters = &counters
	}
	return summary
}

// publish is the only execution/flush completion boundary. Disk I/O must finish
// before entering it; Busy remains queryable and true throughout that I/O.
func (d *Device) publish(summary RunSummary, failure *ClassifiedError) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Prefer the execution failure's identity, retaining concurrent connection
	// failures and audit errors as causes rather than replacing either.
	d.lastError = combineErrors(failure, d.lastError)
	if d.lastError != nil {
		summary.Error, summary.Origin = d.lastError.Error(), d.lastError.Origin
	}
	d.lastRun = summary
	d.busy = false
	if d.lastError != nil {
		return d.lastError
	}
	return nil
}

func combineErrors(primary, secondary *ClassifiedError) *ClassifiedError {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return &ClassifiedError{Origin: primary.Origin, Op: primary.Op, Err: errors.Join(primary.Err, secondary)}
}

func auditFailure(err error) *ClassifiedError {
	if err == nil {
		return nil
	}
	return &ClassifiedError{Origin: OriginConnection, Op: "audit", Err: err}
}

func (d *Device) audit(record auditRecord) error {
	if d == nil || d.auditPath == "" {
		return nil // auditing explicitly disabled
	}
	d.auditMu.Lock()
	defer d.auditMu.Unlock()
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode audit: %w", err)
	}
	open := d.auditOpen
	if open == nil {
		open = func(path string) (io.WriteCloser, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		}
	}
	file, err := open(d.auditPath)
	if err != nil {
		return fmt.Errorf("open audit: %w", err)
	}
	data = append(data, '\n')
	n, writeErr := file.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		writeErr = fmt.Errorf("write audit: %w", writeErr)
	}
	closeErr := file.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close audit: %w", closeErr)
	}
	return errors.Join(writeErr, closeErr)
}
