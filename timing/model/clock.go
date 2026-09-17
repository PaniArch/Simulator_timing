package model

import (
	"fmt"
	"math"

	akita "github.com/sarchlab/akita/v5/timing"
)

// Clock schedules one Akita event per common core edge. The period is an
// explicit simulation time scale, not an inferred external RTL frequency.
// A Clock owns its engine and must not be copied or run concurrently/reentrantly.
type Clock struct {
	period akita.VTimeInPicoSec
	cycle  uint64
	driver edgeDriver
}

func NewClock(period akita.VTimeInPicoSec) (*Clock, error) {
	if period == 0 {
		return nil, fmt.Errorf("zero clock period")
	}
	c := &Clock{period: period}
	c.driver.clock = c
	c.driver.engine = akita.NewSerialEngine()
	c.driver.engine.RegisterHandler("core-edge", &c.driver)
	return c, nil
}

func (c *Clock) Cycle() uint64 { return c.cycle }

// Run preserves edge numbering across budgets. step computes and commits one
// common edge, then returns whether execution should stop. Errors stop further
// events and are returned explicitly (this Akita version discards Handle errors).
func (c *Clock) Run(budget uint64, step func(uint64) (bool, error)) error {
	if step == nil {
		return fmt.Errorf("nil edge callback")
	}
	if budget == 0 {
		return nil
	}
	if budget > math.MaxUint64-c.cycle || c.cycle+budget-1 > math.MaxUint64/uint64(c.period) {
		return fmt.Errorf("clock time overflow")
	}
	d := &c.driver
	d.remaining, d.step = budget, step
	defer func() {
		// The engine drains: Handle schedules a successor only after a successful
		// non-terminal edge. Do not retain the caller or an error between runs.
		d.remaining, d.step, d.err = 0, nil, nil
	}()
	d.schedule()
	if err := d.engine.Run(); err != nil {
		return err
	}
	return d.err
}

type edgeDriver struct {
	clock     *Clock
	remaining uint64
	step      func(uint64) (bool, error)
	engine    *akita.SerialEngine
	err       error
}

func (d *edgeDriver) schedule() {
	d.engine.Schedule(akita.MakeEventBase(akita.VTimeInPicoSec(d.clock.cycle)*d.clock.period, "core-edge"))
}

func (d *edgeDriver) Handle(_ akita.Event) error {
	stop, err := d.step(d.clock.cycle)
	d.err = err
	if err != nil {
		return nil
	}
	d.clock.cycle++
	d.remaining--
	if !stop && d.remaining > 0 {
		d.schedule()
	}
	return nil
}
