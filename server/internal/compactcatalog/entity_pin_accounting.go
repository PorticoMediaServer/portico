package compactcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
)

// pinAccountingStepTx retains the real mutation and old pin during private
// construction. Its caller has already checked the mutation's live lifetime,
// generation, source custody and family. A sealed flip is finished even if its
// original caller disappears; private preparation is never an effect.
func pinAccountingStepTx(ctx context.Context, tx *sql.Tx, m EntityMutation) (MutationStep, error) {
	owner := mbaccounting.Owner{Kind: "entity-mutation", Operation: m.Ref.ID, Generation: m.Ref.Generation}
	state, e := mbaccounting.ReadTx(ctx, tx)
	if e != nil {
		return MutationStep{}, e
	}
	if state.Phase != "idle" {
		// Any worker may finish accepted accounting, and inactivity alone may
		// abort only a private build. Neither case spends this title's authority.
		// Return after this turn: folding and a pin mutation never share a budget.
		worked, e := mbaccounting.MaintainTx(ctx, tx, func(held mbaccounting.Owner) (bool, error) {
			if held.Kind != "entity-mutation" {
				// Publication owners retain their own lease/domain validation.
				return true, nil
			}
			var active bool
			e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entity_mutations WHERE mutation_id=? AND generation=? AND status='active' AND entity_id IS NOT NULL)`, held.Operation, held.Generation).Scan(&active)
			return active, e
		})
		if e != nil || worked {
			return MutationStep{More: true, Progressed: e == nil && worked, Outcome: "pending"}, e
		}
	}
	if state.Phase != "idle" && state.Owner != owner {
		return PendingMutationStep(), nil
	}
	c, found, e := readPinAccountingChangeTx(ctx, tx, m, owner)
	if e != nil {
		return MutationStep{}, e
	}
	if !found {
		if state.Phase != "idle" {
			return MutationStep{}, mbaccounting.ErrStale
		}
		return PendingMutationStep(), SetMutationPhaseTx(ctx, tx, m.Ref, "finalize")
	}
	if state.Phase == "idle" {
		_, e = mbaccounting.BeginTx(ctx, tx, c)
		return MutationStep{More: true, Progressed: e == nil, Outcome: "pending"}, e
	}
	if state.Phase == "prepare" {
		_, e = mbaccounting.PrepareTx(ctx, tx, c)
		return MutationStep{More: true, Progressed: e == nil, Outcome: "pending"}, e
	}
	if state.Phase != "ready" {
		return MutationStep{}, mbaccounting.ErrInvariant
	}
	e = mbaccounting.FlipTx(ctx, tx, c, func() error {
		// Recheck at the effect boundary, not just before the read-only stage.
		current, e := CheckEntityMutationTx(ctx, tx, m.Ref)
		if e != nil {
			return e
		}
		again, found, e := readPinAccountingChangeTx(ctx, tx, current, owner)
		if e != nil {
			return e
		}
		if !found || again.Digest != c.Digest {
			return mbaccounting.ErrStale
		}
		query := `DELETE FROM mb_song_links WHERE item_id=?`
		if c.Link == "album" {
			query = `DELETE FROM mb_album_links WHERE album_id=?`
		}
		r, e := tx.ExecContext(ctx, query, m.Entity)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return mbaccounting.ErrStale
		}
		return nil
	}, func() error {
		// Preserve existing removal policy: unlike a new publication, removing an
		// accepted pin must not be refused because its old evidence becomes cache.
		// Exact totals remain visible and existing bounded retirement reclaims it.
		return nil
	})
	return MutationStep{More: true, PinDrained: e == nil, Progressed: e == nil, Outcome: "pending"}, e
}

func readPinAccountingChangeTx(ctx context.Context, tx *sql.Tx, m EntityMutation, owner mbaccounting.Owner) (mbaccounting.Change, bool, error) {
	c := mbaccounting.Change{Owner: owner, Entity: m.Entity}
	var recording, release, recordingID, releaseID, trackID, status, observed string
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(recording_revision,''),COALESCE(release_revision,''),COALESCE(recording_id,''),COALESCE(release_id,''),COALESCE(track_id,''),release_status,observed_at FROM mb_song_links WHERE item_id=?`, m.Entity).Scan(&recording, &release, &recordingID, &releaseID, &trackID, &status, &observed)
	c.Link = "song"
	if errors.Is(e, sql.ErrNoRows) {
		c.Link = "album"
		e = tx.QueryRowContext(ctx, `SELECT release_revision,release_id,requested_id,observed_at FROM mb_album_links WHERE album_id=?`, m.Entity).Scan(&release, &releaseID, &trackID, &observed)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return c, false, nil
	}
	if e != nil {
		return c, false, e
	}
	c.OldRelease, c.OldRecording = release, recording
	// The fingerprint covers the actual row and admitted entity lifetime. It is
	// only a drift witness; CheckEntityMutationTx remains execution authority.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%q/%d/%q/%q/%q/%q/%q/%q/%q/%q/%q", m.Ref.ID, m.Ref.Generation, m.Public, m.Incarnation, c.Link, recording, release, recordingID, releaseID, trackID, status+"\x00"+observed)))
	c.Digest = fmt.Sprintf("%x", digest)
	c.ID = fmt.Sprintf("%s:%d:%s", m.Ref.ID, m.Ref.Generation, c.Link)
	return c, true, nil
}
