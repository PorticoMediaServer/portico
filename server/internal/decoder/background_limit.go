package decoder

import (
	"context"
	"sync"
)

var preparedAdmission = struct {
	sync.Mutex
	active, limit int
	changed       chan struct{}
}{changed: make(chan struct{})}

// SetBackgroundConversionLimit applies an owner cap to future background media
// processes. Active work completes; queued work remains cancellable. Zero adds
// no owner cap to the existing supervisor's resource admission.
func SetBackgroundConversionLimit(limit int) {
	if limit < 0 || limit > 1000 {
		return
	}
	preparedAdmission.Lock()
	defer preparedAdmission.Unlock()
	preparedAdmission.limit = limit
	close(preparedAdmission.changed)
	preparedAdmission.changed = make(chan struct{})
}
func admitPreparedConversion(ctx context.Context) (func(), error) {
	for {
		preparedAdmission.Lock()
		if preparedAdmission.limit == 0 || preparedAdmission.active < preparedAdmission.limit {
			preparedAdmission.active++
			preparedAdmission.Unlock()
			return func() {
				preparedAdmission.Lock()
				defer preparedAdmission.Unlock()
				preparedAdmission.active--
				close(preparedAdmission.changed)
				preparedAdmission.changed = make(chan struct{})
			}, nil
		}
		changed := preparedAdmission.changed
		preparedAdmission.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}
