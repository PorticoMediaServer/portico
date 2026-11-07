package writer

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Private trusted-startup composition only. Explicit captured plans and a
// complete preparation forecast are required; no worker selects amounts or a
// command class, and no production defaults or qualification are inferred here.
func configureScanIssuer(ctx context.Context, d *ScanDriver, create, claim effectivePlan, ready scanPreparationPlan, lease time.Duration) (err error) {
	var installed bool
	defer func() {
		if installed && err != nil {
			// Composition may already have installed its exact captured registry.
			// A failed publication cannot leave a usable issuer behind it.
			d.closed.Store(true)
			d.cancel()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if d == nil || d.self != d {
		return ErrInvalid
	}
	if err = d.current(); err != nil {
		return err
	}
	if !d.mu.TryLock() {
		return ErrBusy
	}
	defer d.mu.Unlock()
	if d.preparation != nil || d.work != nil || d.operation != nil || d.leaseOwner != 0 {
		return ErrBusy
	}
	if ready.ready <= 0 || ready.need[governor.MemoryBytes] <= 0 || ready.budget.RawRows == 0 || lease < time.Millisecond {
		return ErrInvalid
	}
	var raw [8]byte
	for i := 0; i < 4; i++ {
		if _, err = rand.Read(raw[:]); err != nil {
			return err
		}
		if binary.BigEndian.Uint64(raw[:])&math.MaxInt64 != 0 {
			break
		}
	}
	owner := int64(binary.BigEndian.Uint64(raw[:]) & math.MaxInt64)
	if owner == 0 {
		return ErrInvalid
	}
	ready.io = slices.Clone(ready.io)
	for i := range ready.io {
		ready.io[i].DeviceID = strings.Clone(ready.io[i].DeviceID)
	}
	if err = composeScanLifecycleProfiles(ctx, d.writer.effective, create, claim, true); err != nil {
		return err
	}
	installed = true
	retention, err := scanOwnerRetentionFor(d.opening.executor, d.writer.effective.registry)
	if err != nil {
		return err
	}
	d.retention, d.readyPlan, d.leaseOwner, d.leaseDuration = retention, ready, owner, lease
	return nil
}
