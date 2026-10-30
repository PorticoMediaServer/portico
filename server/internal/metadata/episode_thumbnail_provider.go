package metadata

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// EpisodeProviderStillAllowedTx checks automatic provider artwork policy, not
// source permission. The caller supplies the actual library/item kind from its
// current snapshot. A retained selection actor is not a new owner invocation:
// background size refresh must pass an empty actor here.
func EpisodeProviderStillAllowedTx(ctx context.Context, tx *sql.Tx, library, itemKind, role, actor, provider string) (bool, error) {
	if itemKind != "episode" || role != "still" && role != "thumbnail" || actor != "" || provider == "local" || provider == "upload" || provider == "generated" {
		return true, nil
	}
	policy, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, library)
	if err != nil {
		return false, err
	}
	return policy.Mode != "off", nil
}

func episodeProviderStillTargetTx(ctx context.Context, tx *sql.Tx, target RepairTarget) (bool, error) {
	if target.Kind != "item" {
		return true, nil
	}
	var library, kind string
	err := tx.QueryRowContext(ctx, `SELECT cl.library_id,k.name FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, target.ID).Scan(&library, &kind)
	if err != nil {
		return false, err
	}
	return EpisodeProviderStillAllowedTx(ctx, tx, library, kind, "still", "", "tmdb")
}
