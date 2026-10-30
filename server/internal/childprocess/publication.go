package childprocess

import (
	"context"
	"os/exec"
)

type publicationKey struct{}
type publicationGate func() (func(), error)

// WithPublication adds an internal lifetime fence. It only attenuates the
// caller's existing IO authority; without a fence process policy is unchanged.
// The release marks a successfully started child as owned by its original run,
// not completed. A stop claim can then cancel and join that run after commit.
func WithPublication(ctx context.Context, gate func() (func(), error)) context.Context {
	parent, _ := ctx.Value(publicationKey{}).(publicationGate)
	return context.WithValue(ctx, publicationKey{}, publicationGate(func() (func(), error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var releaseParent func()
		if parent != nil {
			var err error
			releaseParent, err = parent()
			if err != nil {
				return nil, err
			}
		}
		release, err := gate()
		if err != nil {
			if releaseParent != nil {
				releaseParent()
			}
			return nil, err
		}
		return func() {
			release()
			if releaseParent != nil {
				releaseParent()
			}
		}, nil
	}))
}

// AcquirePublication brackets provider acquisition or process Start, never the
// entire process lifetime. No database/provider work runs under its mutex.
func AcquirePublication(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if gate, ok := ctx.Value(publicationKey{}).(publicationGate); ok {
		return gate()
	}
	return func() {}, nil
}

func Start(ctx context.Context, cmd *exec.Cmd) error {
	if proof, owned := ctx.Value(ownedOperationKey{}).(ownedIOProof); owned {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := proof(); err != nil {
			return err
		}
		return observeStarted(ctx, cmd.Start())
	}

	release, err := AcquirePublication(ctx)
	if err != nil {
		return err
	}
	err = cmd.Start()
	release()
	return observeStarted(ctx, err)
}
func Run(ctx context.Context, cmd *exec.Cmd) error {
	if err := Start(ctx, cmd); err != nil {
		return err
	}
	return cmd.Wait()
}

type ownedIOKey struct{}
type ownedOperationKey struct{}
type ownedIOProof func() error

// WithOwnedIO supplies an exact installed resource's liveness proof. A held
// prewriter claim is not a revocation; committed or independently retired work
// must fail. This proof cannot authorize new registration/source acquisition.
func WithOwnedIO(ctx context.Context, proof func() error) context.Context {
	parent, _ := ctx.Value(ownedIOKey{}).(ownedIOProof)
	return context.WithValue(ctx, ownedIOKey{}, ownedIOProof(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if parent != nil {
			if err := parent(); err != nil {
				return err
			}
		}
		return proof()
	}))
}
func CheckOwnedIO(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if proof, ok := ctx.Value(ownedIOKey{}).(ownedIOProof); ok {
		return proof()
	}
	return nil
}

// OwnedIOContext scopes validation/reads of the already retained source. Its
// helper children inherit this exact repeated proof, not new-start authority.
func OwnedIOContext(ctx context.Context) (context.Context, error) {
	if err := CheckOwnedIO(ctx); err != nil {
		return nil, err
	}
	if proof, ok := ctx.Value(ownedIOKey{}).(ownedIOProof); ok {
		return context.WithValue(ctx, ownedOperationKey{}, proof), nil
	}
	return ctx, nil
}
