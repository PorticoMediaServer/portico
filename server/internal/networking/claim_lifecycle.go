package networking

import "context"

// LifecycleLease is implemented by the one root-owned operations AuthorityGate.
// Root injects its Acquire closure; networking creates no gate/incarnation state.
type LifecycleLease interface {
	Context() context.Context
	Incarnation() string
	Check() error
	Commit(func() error) error
	Close()
}
type AcquireClaimAuthority func(context.Context) (LifecycleLease, error)
type AuthorityRunner struct{ acquire AcquireClaimAuthority }
type claimAuthorityKey struct{}

func NewAuthorityRunner(acquire AcquireClaimAuthority) (*AuthorityRunner, error) {
	if acquire == nil {
		return nil, ErrInvalid
	}
	return &AuthorityRunner{acquire: acquire}, nil
}
func claimAuthority(ctx context.Context) (LifecycleLease, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	lease, ok := ctx.Value(claimAuthorityKey{}).(LifecycleLease)
	if !ok || lease == nil || lease.Incarnation() == "" {
		return nil, &authorityError{"missing", ErrUnavailable}
	}
	if e := lease.Check(); e != nil {
		return nil, &authorityError{"check", ErrUnavailable}
	}
	return lease, nil
}

// Do holds authority through the full bounded operation, including secret use,
// signing, HTTP and final publication. Cancellation never reports success.
func (r *AuthorityRunner) Do(ctx context.Context, fn func(context.Context) error) error {
	if r == nil || r.acquire == nil || fn == nil {
		return ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	lease, e := r.acquire(ctx)
	if e != nil {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return &authorityError{"acquire", ErrUnavailable}
	}
	if lease == nil {
		return &authorityError{"acquire", ErrUnavailable}
	}
	defer lease.Close()
	if lease.Context() == nil || lease.Incarnation() == "" || lease.Check() != nil {
		return &authorityError{"check", ErrUnavailable}
	}
	scoped := context.WithValue(lease.Context(), claimAuthorityKey{}, lease)
	if e = fn(scoped); e != nil {
		return e
	}
	_, e = claimAuthority(scoped)
	return e
}
func (r *AuthorityRunner) Step(ctx context.Context, c *Coordinator) (Intent, error) {
	if c == nil {
		return Intent{}, ErrInvalid
	}
	var v Intent
	e := r.Do(ctx, func(scoped context.Context) error { var e error; v, e = c.Step(scoped); return e })
	if e != nil {
		return Intent{}, e
	}
	return v, nil
}
func (r *AuthorityRunner) Cancel(ctx context.Context, c *Coordinator) (Intent, error) {
	if c == nil {
		return Intent{}, ErrInvalid
	}
	var v Intent
	e := r.Do(ctx, func(scoped context.Context) error { var e error; v, e = c.Cancel(scoped); return e })
	if e != nil {
		return Intent{}, e
	}
	return v, nil
}
func commitClaimTx(ctx context.Context, commit func() error) error {
	lease, e := claimAuthority(ctx)
	if e != nil {
		return e
	}
	admitted := false
	e = lease.Commit(func() error {
		admitted = true
		if e := ctx.Err(); e != nil {
			return e
		}
		return commit()
	})
	if e != nil && !admitted {
		// The gate refused the commit itself (fenced between check and commit).
		return &authorityError{"commit", e}
	}
	return e
}

type fencedClaimSigner struct{ inner Signer }

func NewFencedSigner(inner Signer) (Signer, error) {
	if inner == nil {
		return nil, ErrInvalid
	}
	return fencedClaimSigner{inner}, nil
}
func (s fencedClaimSigner) Sign(ctx context.Context, b Binding, payload []byte) ([]byte, error) {
	if _, e := claimAuthority(ctx); e != nil {
		return nil, e
	}
	if e := checkClaimRequest(ctx); e != nil {
		return nil, e
	}
	signature, e := s.inner.Sign(ctx, b, payload)
	if e != nil {
		clear(signature)
		return nil, e
	}
	if _, e = claimAuthority(ctx); e != nil {
		clear(signature)
		return nil, e
	}
	if e = checkClaimRequest(ctx); e != nil {
		clear(signature)
		return nil, e
	}
	return signature, nil
}
