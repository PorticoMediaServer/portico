package metadataprovider

import "context"

type admissionKey struct{}

// NetworkAdmission captures the caller's current policy and returns its exact
// post-response fence. The ordinary provider route, credential and pacing rules remain.
type NetworkAdmission func(context.Context) (func() error, error)

// WithNetworkAdmission composes with a prior caller's fence rather than replacing it.
func WithNetworkAdmission(ctx context.Context, admission NetworkAdmission) context.Context {
	prior, _ := ctx.Value(admissionKey{}).(NetworkAdmission)
	if prior != nil {
		next := admission
		admission = func(ctx context.Context) (func() error, error) {
			first, err := prior(ctx)
			if err != nil {
				return nil, err
			}
			second, err := next(ctx)
			if err != nil {
				return nil, err
			}
			return func() error {
				if err := first(); err != nil {
					return err
				}
				return second()
			}, nil
		}
	}
	return context.WithValue(ctx, admissionKey{}, admission)
}
func networkAdmission(ctx context.Context) (func() error, error) {
	admission, _ := ctx.Value(admissionKey{}).(NetworkAdmission)
	if admission == nil {
		return func() error { return nil }, nil
	}
	return admission(ctx)
}

// BeginNetworkAdmission captures one policy epoch for the complete image fetch,
// including every explicitly validated redirect and body read.
func BeginNetworkAdmission(ctx context.Context) (func() error, error) {
	return networkAdmission(ctx)
}
