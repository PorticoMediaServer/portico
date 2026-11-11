//go:build !linux

package console

import "time"

// machine measures nothing yet on this system: the processor and memory series are left out,
// and the Dashboard hides their charts.
type machine struct{}

func (*machine) sample(time.Time, map[string]float64) {}
