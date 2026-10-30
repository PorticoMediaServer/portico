package dbwork

import (
	"context"
	"sync"
	"time"
)

// The server has one active maintenance authority, like its foreground probes.
// Replacing this memory projection on save makes batch boundaries cheap and
// lets an owner resume work immediately, without a database read per item.
var backgroundPolicy = struct {
	sync.Mutex
	speed   string
	until   int64
	changed chan struct{}
}{speed: SpeedNormal, changed: make(chan struct{})}

func SetBackgroundPolicy(speed string, until int64) {
	if !ValidBackgroundSpeed(speed) {
		speed = SpeedNormal
	}
	backgroundPolicy.Lock()
	defer backgroundPolicy.Unlock()
	backgroundPolicy.speed, backgroundPolicy.until = speed, until
	close(backgroundPolicy.changed)
	backgroundPolicy.changed = make(chan struct{})
}

func BackgroundPriorityForSpeed(speed string) string {
	if speed == SpeedFast {
		return "normal"
	}
	return "lower"
}

func processBackgroundSpeed() string {
	backgroundPolicy.Lock()
	defer backgroundPolicy.Unlock()
	return backgroundPolicy.speed
}

// WaitBackground runs only between quanta. It never waits with a transaction
// carried by the context, and never affects playback, recording or owner HTTP
// admission. A running child finishes its quantum before observing the pause.
func WaitBackground(ctx context.Context) bool {
	if Snapshot(ctx) != nil {
		return ctx.Err() == nil
	}
	for ctx.Err() == nil {
		backgroundPolicy.Lock()
		until, changed := backgroundPolicy.until, backgroundPolicy.changed
		backgroundPolicy.Unlock()
		delay := time.Until(time.UnixMilli(until))
		if delay <= 0 {
			return true
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
	return false
}
