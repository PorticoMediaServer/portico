package decoder

import (
	"context"
	"errors"
)

// One in-flight observation, not a positive file-stat cache. Non-overlapping calls always
// re-read the executable/library bytes. No mutex is held during file IO or while waiting.
type hardwareIdentityFlight struct {
	executable, identity string
	err                  error
	done                 chan struct{}
}

func (d *HardwareDetector) runtimeIdentity(ctx context.Context, executable string) (string, error) {
	if ctx == nil {
		return "", ErrInvalidConfiguration
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		d.identityMu.Lock()
		if run := d.identityFlight; run != nil {
			d.identityMu.Unlock()
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-run.done:
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if run.executable == executable {
				if run.err == nil || !errors.Is(run.err, context.Canceled) && !errors.Is(run.err, context.DeadlineExceeded) {
					return run.identity, run.err
				}
			}
			// A cancelled leader cannot renew a follower. One retry uses only that
			// follower's still-current original context and original deadline.
			continue
		}
		run := &hardwareIdentityFlight{executable: executable, done: make(chan struct{})}
		d.identityFlight = run
		d.identityMu.Unlock()
		lookup := d.RuntimeIdentity
		if lookup == nil {
			lookup = hardwareRuntimeIdentity
		}
		bounded, cancel := context.WithTimeout(ctx, defaultProbeTimeout)
		run.identity, run.err = lookup(bounded, executable)
		cancel()
		d.identityMu.Lock()
		if d.identityFlight == run {
			d.identityFlight = nil
		}
		close(run.done)
		d.identityMu.Unlock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return run.identity, run.err
	}
	return "", ErrInvalidConfiguration
}
