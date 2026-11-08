package writer

import (
	"context"
	"errors"
)

// Domain adapter over W's ONE source-preparation primitive (#1583). The driver
// supplies the original identity; this function mints no identity, disclosure,
// lease or grant and performs no native observation. The returned holding must
// cover all later payload/probe ownership until physical and consumer release.
func prepareBasicCommandOriginal(ctx context.Context, driver *ScanDriver, lease *ScanRunLease, last ScanRunReference, purpose BasicPurpose, identity CommandIdentity) (preparation *scanPreparation, original basicOriginalScope, err error) {
	lifetime := ctx
	locked := false
	defer func() {
		if locked {
			err = errors.Join(err, driver.current())
		}
		value, e := finishCommand(ctx, lifetime, struct {
			Preparation *scanPreparation
			Original    basicOriginalScope
		}{preparation, original}, err)
		preparation, original, err = value.Preparation, value.Original, e
		if locked {
			driver.mu.Unlock()
		}
	}()
	if ctx == nil {
		return nil, original, ErrInvalid
	}
	kind, err := basicResourceCommand(purpose)
	if err != nil {
		return nil, original, err
	}
	var snapshot scanRunSnapshot
	var source scanSourceFacts
	preparation, snapshot, source, err = prepareBasicSource(ctx, driver, lease, last, kind)
	if err != nil {
		return nil, original, err
	}
	if preparation == nil {
		return nil, original, ErrInvalid
	}
	lifetime = preparation.lifetime
	// W returned the funded holding before this projection. Serialize the
	// consumer handoff with the SAME driver's Close/ReleasePending so neither
	// can refund its capture while these owned values are being constructed.
	if !driver.mu.TryLock() {
		return nil, original, ErrBusy
	}
	locked = true
	if driver.preparation != preparation || preparation.mode != scanBasicPreparation || preparation.expected.Key.Domain != uint16(kind) {
		return nil, original, ErrInvalid
	}
	if err = errors.Join(driver.current(), preparation.current()); err != nil {
		return nil, original, err
	}
	original, err = basicOriginalFromSnapshot(identity, snapshot, source)
	if err != nil {
		// W retains an unconsumed original on the driver for its existing
		// ReleasePending path. A mapping error cannot silently refund that hold.
		return nil, original, err
	}
	err = preparation.current()
	return preparation, original, err
}
