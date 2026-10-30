package childprocess

import "context"

type startedKey struct{}

// WithStarted observes successful process Starts after the publication fence
// is released. Observers must not alter process policy or retain command data.
func WithStarted(ctx context.Context, observe func()) context.Context {
	return context.WithValue(ctx, startedKey{}, observe)
}

func observeStarted(ctx context.Context, err error) error {
	if err == nil {
		if observe, ok := ctx.Value(startedKey{}).(func()); ok && observe != nil {
			observe()
		}
	}
	return err
}
