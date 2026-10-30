package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A personal Saved collection can contain a Reading title without granting
// playback or opening its source. Keep the existing timed-media path; only its
// concealed absence can fall back to the Reading personal-state authority.
func (d Dependencies) savedResourceItemAccess(ctx context.Context, p identity.Principal, item string) error {
	err := d.itemAccess(ctx, p, item)
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	read, err := dbwork.BeginSnapshot(ctx, d.DB)
	if err != nil {
		return err
	}
	defer read.Rollback()
	return d.savedReadingItemAuthorityTx(ctx, read.Tx(), p, item)
}

// Repeat current Reading admission in the transaction accepting membership.
// Non-Reading items retain the existing authority callback unchanged.
func (d Dependencies) savedResourceItemAuthorityTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) error {
	if _, valid := entityid.Decode(item); !valid {
		return sql.ErrNoRows
	}
	var kind int
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&kind); err != nil {
		return err
	}
	if kind == 20 || kind == 21 {
		return d.savedReadingItemAuthorityTx(ctx, tx, p, item)
	}
	_, err := d.playbackAuthorityTx(ctx, tx, p, item)
	return err
}

func (d Dependencies) savedReadingItemAuthorityTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) error {
	if !identity.CanUsePersonalState(p) {
		return identity.ErrForbidden
	}
	if _, valid := entityid.Decode(item); !valid {
		return sql.ErrNoRows
	}
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries lib ON lib.id=cl.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0 AND ((e.kind=20 AND lib.kind='book') OR (e.kind=21 AND lib.kind='comic'))`, item).Scan(&present); err != nil {
		return err
	}
	var fence string
	return d.readingMarkAuthorize(ctx, p, item, &fence)(tx)
}
