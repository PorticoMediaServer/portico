package metadata

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// downAfter is how many titles in a row the provider may fail on before a run stops: by then
// it is down, not refusing one title, and asking it about every other title would only fail too.
const downAfter = 5

// ErrProviderDown is a run stopped because the provider failed on several titles in a row, or
// on every title it was asked about. The titles not reached, and those it failed on, are as
// they were, and the next run asks about them again.
var ErrProviderDown = errors.New("the metadata provider did not answer")

// providerError is an error from the provider itself, as opposed to one of this server's.
type providerError struct{ err error }

func (e providerError) Error() string { return e.err.Error() }
func (e providerError) Unwrap() error { return e.err }

// failures is what one run (a lookup or a refresh of a library) does with the titles the
// provider fails on, so one title can never keep the rest from being reached. A title it failed
// on is passed over. Once the provider answers for a later title, those it failed on are
// recorded (db.ProviderFailed) and wait before they are asked again, an hour at first and longer
// each time; until then the provider may simply be down, and a title is not to wait for a fault
// that was not its own. Several failures in a row, or a run where every title failed, is a
// provider that is down: the run stops with ErrProviderDown.
type failures struct {
	s        *Service
	failed   []int64
	last     error
	answered bool
	count    int
}

// took is told what asking about one title gave. It reports handled for a provider's failure,
// which the run passes over, and an error when the run is to stop.
func (f *failures) took(ctx context.Context, itemID int64, err error) (handled bool, stop error) {
	if !errors.As(err, new(providerError)) {
		if err != nil {
			return false, err
		}
		f.answered = true
		return false, f.settle(ctx)
	}
	f.count++
	f.failed, f.last = append(f.failed, itemID), err
	if len(f.failed) >= downAfter {
		return true, fmt.Errorf("%w: %w", ErrProviderDown, err)
	}
	return true, nil
}

// end is called when a run has asked about every title it was going to.
func (f *failures) end(ctx context.Context) error {
	if len(f.failed) > 0 && !f.answered {
		return fmt.Errorf("%w: %w", ErrProviderDown, f.last)
	}
	return f.settle(ctx)
}

func (f *failures) settle(ctx context.Context) error {
	for _, id := range f.failed {
		if err := f.s.DB.ProviderFailed(ctx, id, time.Now()); err != nil {
			return err
		}
	}
	f.failed = f.failed[:0]
	return nil
}
