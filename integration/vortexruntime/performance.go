package vortexruntime

import "fmt"

// readMPM decodes VX_dcr_data.mpm_{target_cid,tag_idx,class}. It reads the
// published execution snapshot, never an asynchronously mutating Kernel.
// Unavailable statistics return errors without changing execution/audit state.
func (d *Device) readMPM(tag uint32) (uint32, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.busy || d.busyLatch {
		return 0, fmt.Errorf("MPM requires an idle open device")
	}
	if tag&0xffff != 0 || tag>>30 != 0 {
		return 0, fmt.Errorf("unsupported MPM target/tag %#x", tag)
	}
	if d.mode != Timing || d.lastError != nil {
		return 0, fmt.Errorf("hardware MPM unavailable for functional or failed execution")
	}
	index, class := (tag>>16)&63, (tag>>22)&255
	slot := index & 31
	var value uint64
	switch slot {
	case 0, 2:
		// The two base CSRs are independent of mpm_class in VX_csr_data.
		if c := d.lastRun.HardwareCounters; c != nil {
			if slot == 0 {
				value = c.Cycle
			} else {
				value = c.Instret
			}
		} else if d.sequence != 0 {
			return 0, fmt.Errorf("hardware counter snapshot unavailable")
		} // A newly reset timing device has valid zero base counters.
	default:
		if slot == 1 || class != 0 {
			return 0, fmt.Errorf("unsupported MPM class %d slot %d", class, slot)
		}
		// BASE class user window is explicitly zero in VX_csr_data, not unknown.
	}
	if index&32 != 0 {
		return uint32(value>>32) & 0xfff, nil
	}
	return uint32(value), nil
}
