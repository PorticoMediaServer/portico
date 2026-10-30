package metadata

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Reuse existing provider review intents. These commands do not add matching
// algorithms, change a selected identity, or discard last published metadata.
func retryRepairProvider(ctx context.Context, tx *sql.Tx, t RepairTarget, current *RepairIdentity, alternatives bool) error {
	if current == nil {
		return ErrRepairInput
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	switch current.Provider {
	case "musicbrainz":
		kind := t.Kind
		if kind == "item" {
			kind = "song"
		}
		if kind != "song" && kind != "album" {
			return ErrRepairInput
		}
		_, err := tx.ExecContext(ctx, `UPDATE mb_jobs SET review_search=?,status='pending',attempts=0,next_attempt='',error='',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=? AND revision=?`, alternatives, kind, entity, current.Revision)
		return err
	case "tvdb":
		if t.Kind != "show" || alternatives {
			return ErrRepairInput
		}
		if current.Status != "unavailable" && current.Status != "unresolved" && current.Status != "needs_selection" {
			return ErrRepairInput
		}
		_, err := tx.ExecContext(ctx, `UPDATE tvdb_jobs SET status=CASE WHEN provider_id>0 THEN CASE WHEN retry_status='pending_apply' THEN 'pending_apply' ELSE 'pending_episodes' END ELSE 'pending_search' END,revision=revision+1,attempts=0,next_attempt='',error='',lease='',lease_until='' WHERE show_id=? AND revision=?`, entity, current.Revision)
		return err
	}
	return ErrRepairInput
}

// Undo to an unmatched state is an explicit locked local decision. Accepted
// provider evidence is detached, not reassigned to another catalog identity.
func clearRepairIdentity(ctx context.Context, tx *sql.Tx, t RepairTarget, previous *RepairIdentity) error {
	if previous == nil {
		return nil
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	switch previous.Provider {
	case "tmdb":
		for _, q := range []string{`DELETE FROM metadata_movie_selections WHERE item_id=?`, `DELETE FROM provider_evidence WHERE item_id=? AND provider='tmdb'`, `DELETE FROM metadata_details WHERE item_id=? AND provider='tmdb'`, `DELETE FROM metadata_ratings WHERE item_id=? AND provider='tmdb'`, `UPDATE metadata_publication_heads SET identity_revision=identity_revision+1 WHERE item_id=?`} {
			if _, err := tx.ExecContext(ctx, q, entity); err != nil {
				return err
			}
		}
	case "musicbrainz":
		kind := t.Kind
		table := "mb_album_links"
		column := "album_id"
		if kind == "item" {
			kind = "song"
			table = "mb_song_links"
			column = "item_id"
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+column+`=?`, entity); err != nil {
			return err
		}
		if err := compactcatalog.TouchEntityTx(ctx, tx, entity); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE mb_jobs SET selected_id='',manual=1,status='unresolved',review_search=0,error='owner_restored_unmatched',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=?`, kind, entity)
		return err
	case "tvdb":
		if _, err := tx.ExecContext(ctx, `UPDATE tvdb_jobs SET provider_id=0,status='unresolved',generation=generation+1,revision=revision+1,lease='',lease_until='',error='owner_restored_unmatched' WHERE show_id=?`, entity); err != nil {
			return err
		}
		// Canonical show/season/episode IDs and numbering are retained; the old
		// provider links may not remain accepted after an explicit unmatch.
		for _, q := range []string{`DELETE FROM tvdb_episode_links WHERE item_id IN(SELECT entity_id FROM catalog_episodes WHERE show_id=?)`, `DELETE FROM metadata_details WHERE provider='tvdb' AND item_id IN(SELECT entity_id FROM catalog_episodes WHERE show_id=?)`} {
			if _, err := tx.ExecContext(ctx, q, entity); err != nil {
				return err
			}
		}
	}
	return nil
}
