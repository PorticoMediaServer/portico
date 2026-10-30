package mounts

import (
	"context"
	"database/sql"
)

// ConfigureReadCache is private server-domain composition, not a wire action.
// The continuation observes the exact prior and accepted child configuration
// after source publication and before the mount receipt/commit. It may perform
// DB-only validation/writes; a refusal rolls the entire mount transaction back.
// Exact receipt replay returns its stored outcome without rerunning the hook.
func (s *Service) ConfigureReadCache(ctx context.Context, actor, id, operation string, expectedRevision, expectedGeneration, bytes, floor int64,
	authorize func(*sql.Tx) error, accepted func(context.Context, *sql.Tx, Mount, Mount) error) (Receipt, error) {
	if s == nil || ctx == nil || id == "" || expectedRevision < 1 || expectedGeneration < 1 || authorize == nil || accepted == nil {
		return Receipt{}, ErrInvalid
	}
	if bytes < 0 || bytes > 64<<30 || bytes != 0 && bytes < 64<<20 || floor < 1<<30 || floor > 1<<40 {
		return Receipt{}, ErrInvalid
	}
	return s.command(ctx, actor, id, Command{
		OperationID: operation, Action: "configure", ExpectedRevision: expectedRevision,
		CacheBytes: &bytes, CacheFloor: &floor, readCacheGeneration: expectedGeneration,
	}, authorize, accepted)
}
