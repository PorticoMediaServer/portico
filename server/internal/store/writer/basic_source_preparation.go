package writer

import "context"

// Domain adapter over W's ONE source-preparation primitive (#1583). The driver
// supplies the original identity; this function mints no identity, disclosure,
// lease or grant and performs no native observation. The returned holding must
// cover all later payload/probe ownership until physical and consumer release.
func prepareBasicCommandOriginal(ctx context.Context, driver *ScanDriver, lease *ScanRunLease, last ScanRunReference, purpose BasicPurpose, identity CommandIdentity) (preparation *scanPreparation, original basicOriginalScope, err error) {
	lifetime := ctx
	defer func() {
		value, e := finishCommand(ctx, lifetime, struct {
			Preparation *scanPreparation
			Original    basicOriginalScope
		}{preparation, original}, err)
		preparation, original, err = value.Preparation, value.Original, e
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
	original, err = basicOriginalFromSnapshot(identity, snapshot, source)
	if err != nil {
		// W retains an unconsumed original on the driver for its existing
		// ReleasePending path. A mapping error cannot silently refund that hold.
		return nil, original, err
	}
	err = preparation.current()
	return preparation, original, err
}
