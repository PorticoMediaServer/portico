package httpapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// The new nonplayable Reading targets use the current library/family boundary,
// never LibraryForItem's playable-only lookup or an archive-access grant.
func (d Dependencies) readingMetadataTargetAuthorize(ctx context.Context, tx *sql.Tx, p identity.Principal, t metadata.RepairTarget) error {
	if t.Kind != "item" {
		return nil
	}
	entity, err := entityid.Resolve(ctx, tx, t.ID)
	if errors.Is(err, entityid.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var library string
	err = tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? AND e.kind IN(20,21) AND e.retired=0 AND cl.retired=0`, entity).Scan(&library)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return d.resourceAuthorize(tx, p, library)
}

func (d Dependencies) readingMetadataResponseAuthorize(ctx context.Context, p identity.Principal, out metadata.RepairState) error {
	if out.TargetItemKind != "ebook" && out.TargetItemKind != "comic" {
		return nil
	}
	read, err := dbwork.BeginSnapshot(ctx, d.DB)
	if err != nil {
		return err
	}
	defer read.Rollback()
	if err = d.manualMetadataAuthorize(ctx, read.Tx(), p); err != nil {
		return err
	}
	var library string
	err = read.Tx().QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.kind IN(20,21) AND e.retired=0 AND cl.retired=0`, out.Target.ID).Scan(&library)
	if err != nil {
		return err
	}
	return d.resourceAuthorize(read.Tx(), p, library)
}
