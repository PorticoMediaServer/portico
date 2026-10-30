package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// These are actual local identity collisions in one admitted file publication,
// held only in its short-lived memory. Never persist a query/member list.
type audioMergePair struct {
	From, Into int64
	Key        string
}
type audioMergeAdmissions struct{ Pairs []audioMergePair }
type audioMergeKey struct{}

func withAudioMergeAdmissions(ctx context.Context) (context.Context, *audioMergeAdmissions) {
	if a, ok := ctx.Value(audioMergeKey{}).(*audioMergeAdmissions); ok {
		return ctx, a
	}
	a := &audioMergeAdmissions{}
	return context.WithValue(ctx, audioMergeKey{}, a), a
}
func enqueueAudioMergeTx(ctx context.Context, tx *sql.Tx, pair audioMergePair) error {
	var public string
	if err := tx.QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, pair.From).Scan(&public); err != nil {
		return err
	}
	ref, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{Entity: pair.From, Target: pair.Into, OwnerKind: "identity", OwnerID: "audio:" + public, OwnerGeneration: 1, Mode: "merge", Provider: "local", ProviderKind: "audio-tag", ProviderID: pair.Key})
	if err != nil {
		return err
	}
	return enqueueCatalogMutationTx(ctx, tx, ref)
}
func finishAudioMergeAdmissionsTx(ctx context.Context, tx *sql.Tx, a *audioMergeAdmissions) error {
	for _, pair := range a.Pairs {
		if err := enqueueAudioMergeTx(ctx, tx, pair); err != nil {
			return err
		}
	}
	return nil
}
func deferAudioMergeTx(ctx context.Context, tx *sql.Tx, from, into int64, key string) (bool, error) {
	if err := requireCompleteMergeTx(ctx, tx, from, into); err != nil {
		if !errors.Is(err, compactcatalog.ErrEntityPinned) {
			return false, err
		}
		if a, ok := ctx.Value(audioMergeKey{}).(*audioMergeAdmissions); ok {
			if len(a.Pairs) >= 128 {
				return false, ErrMergeTooLarge
			}
			for _, pair := range a.Pairs {
				if pair.From == from {
					if pair.Into != into {
						return false, compactcatalog.ErrIdentityConflict
					}
					return true, nil
				}
			}
			a.Pairs = append(a.Pairs, audioMergePair{from, into, key})
			return true, nil
		}
		return true, enqueueAudioMergeTx(ctx, tx, audioMergePair{from, into, key})
	}
	return false, nil
}
