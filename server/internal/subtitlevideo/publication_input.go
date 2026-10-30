package subtitlevideo

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

// Existing retained input operations use exact-owned liveness, not new-start
// admission. A held prewriter claim is not revocation of this source; its
// unchanged selection/actor checks still apply. Concrete input types remain
// intact, including the remote companion and persistent-evidence assertions.
func validatePublication(ctx context.Context, validate func(context.Context) error) error {
	owned, err := childprocess.OwnedIOContext(ctx)
	if err != nil {
		return err
	}
	if err = validate(owned); err != nil {
		return err
	}
	return childprocess.CheckOwnedIO(ctx)
}
func readPublication(ctx context.Context, read func(context.Context) ([]byte, error)) ([]byte, error) {
	owned, err := childprocess.OwnedIOContext(ctx)
	if err != nil {
		return nil, err
	}
	bytes, err := read(owned)
	if err != nil {
		return nil, err
	}
	if err = childprocess.CheckOwnedIO(ctx); err != nil {
		return nil, err
	}
	return bytes, nil
}
func (p *localInput) Validate(ctx context.Context) error {
	return validatePublication(ctx, p.validateOwned)
}
func (p *localInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	return readPublication(ctx, func(owned context.Context) ([]byte, error) { return p.readOwned(owned, offset, length) })
}
func (p *remoteInput) Validate(ctx context.Context) error {
	return validatePublication(ctx, p.validateOwned)
}
func (p *remoteInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	return readPublication(ctx, func(owned context.Context) ([]byte, error) { return p.readOwned(owned, offset, length) })
}
func (p *selectedRemoteInput) Validate(ctx context.Context) error {
	return validatePublication(ctx, p.validateOwned)
}
func (p *selectedRemoteInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	return readPublication(ctx, func(owned context.Context) ([]byte, error) { return p.readOwned(owned, offset, length) })
}
