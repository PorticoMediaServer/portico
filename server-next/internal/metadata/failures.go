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

// ErrProviderDown is a run stopped because the provider plainly failed itself (no answer, 5xx,
// 429) on several titles in a row, or on every title it was asked about. The titles not reached, and those it failed on, are as
// they were, and the next run asks about them again.
var ErrProviderDown = errors.New("the metadata provider did not answer")

// providerError is an error from the provider itself, as opposed to one of this server's.
type providerError struct{ err error }

func (e providerError) Error() string { return e.err.Error() }
func (e providerError) Unwrap() error { return e.err }

// failures is what one run (a lookup or a refresh of a library) does with the titles the
// provider fails on, so one title can never keep the rest from being reached.
//
//   - A failure about one title (a refusal) is recorded at once (db.ProviderFailed): the title
//     waits before it is asked again, an hour at first and twice as long each time after, up to
//     a week, and the run goes on. It is not a sign that the provider is down.
//   - A failure that is plainly the provider's (providerOutage) is passed over and held. Once the
//     provider answers or refuses for a later title, it is up after all, and the held titles are
//     recorded too. Five such failures in a row, or a run that met nothing else, is a provider
//     that is down: the run stops with ErrProviderDown and those titles are as they were, with
//     no wait, so the next run asks about them again as soon as it is up.
type failures struct {
	s       *Service
	held    []int64
	last    error
	inARow  int
	upSince bool // the provider has answered or refused something in this run
	count   int
}

// took is told what asking about one title gave. It reports handled for a provider's failure,
// which the run passes over, and an error when the run is to stop.
func (f *failures) took(ctx context.Context, itemID int64, err error) (handled bool, stop error) {
	if !errors.As(err, new(providerError)) {
		if err != nil {
			return false, err
		}
		f.upSince, f.inARow = true, 0
		return false, f.settle(ctx)
	}
	f.count++
	if !providerOutage(err) {
		f.upSince, f.inARow = true, 0
		if err := f.settle(ctx); err != nil {
			return true, err
		}
		return true, f.s.DB.ProviderFailed(ctx, itemID, time.Now())
	}
	f.held, f.last = append(f.held, itemID), err
	if f.inARow++; f.inARow >= downAfter {
		return true, fmt.Errorf("%w: %w", ErrProviderDown, err)
	}
	return true, nil
}

// end is called when a run has asked about every title it was going to.
func (f *failures) end(ctx context.Context) error {
	if len(f.held) > 0 && !f.upSince {
		return fmt.Errorf("%w: %w", ErrProviderDown, f.last)
	}
	return f.settle(ctx)
}

// settle records the held titles: the provider is up, so their failures were their own.
func (f *failures) settle(ctx context.Context) error {
	for _, id := range f.held {
		if err := f.s.DB.ProviderFailed(ctx, id, time.Now()); err != nil {
			return err
		}
	}
	f.held = f.held[:0]
	return nil
}
