package metadata

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// queueAcceptedLocalArtworkDefaultTx selects only the ranked default already
// materialized by the bounded consumer. It inserts no candidate and therefore
// consumes none of that worker's eight-candidate budget. All owner and source
// decisions are rechecked in this publication transaction.
func queueAcceptedLocalArtworkDefaultTx(ctx context.Context, tx *sql.Tx, target RepairTarget, role, fence, now string) error {
	if tx == nil {
		return ErrRepairInput
	}
	if target.Kind != "item" && target.Kind != "show" && target.Kind != "season" {
		return nil
	}
	if role != "poster" && role != "backdrop" && role != "logo" {
		return nil
	}
	entity, err := resolveEntity(ctx, tx, target.ID)
	if err != nil {
		return err
	}
	preferred, err := catalog.ReadDefaultLocalArtworkCandidateTx(ctx, tx, target.Kind, entity, role)
	if err != nil || preferred == nil {
		return err
	}
	origin := "local:" + preferred.Key
	id := publicationDigest([]string{target.Kind, target.ID, role, "", "local", publicationDigest(origin)})
	var materialized bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_candidates WHERE id=? AND kind=? AND entity_id=? AND role=? AND subject='' AND provider='local' AND origin=? AND source_fence=?)`, id, target.Kind, entity, role, origin, fence).Scan(&materialized); err != nil || !materialized {
		return err
	}
	current, err := artworkFence(ctx, tx, target)
	if err != nil {
		return err
	}
	if current != fence {
		return ErrRepairConflict
	}
	var selected string
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT candidate_id,revision FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND subject=''`, target.Kind, entity, role).Scan(&selected, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var job, candidate, jobFence, provider, actor, status string
	var jobRevision int64
	var locked bool
	err = tx.QueryRowContext(ctx, `SELECT id,candidate_id,source_fence,provider,actor,status,selection_revision,locked FROM artwork_jobs WHERE kind=? AND entity_id=? AND role=? AND subject='' AND preview=0`, target.Kind, entity, role).Scan(&job, &candidate, &jobFence, &provider, &actor, &status, &jobRevision, &locked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	active := status == "pending" || status == "running" || status == "retry"
	if selected == id {
		if active && candidate != id && provider == "local" && actor == "" && !locked {
			_, err = tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',error='slot_held',lease='',lease_until='' WHERE id=? AND actor='' AND locked=0 AND status IN('pending','running','retry')`, job)
			return err
		}
		return nil
	}
	if active && candidate == id && jobFence == fence && jobRevision == revision {
		// A repeated dirty pass must not reset a valid running/retry lease.
		return nil
	}
	return queueArtworkCandidate(ctx, tx, target, id, "", now, false, false)
}
