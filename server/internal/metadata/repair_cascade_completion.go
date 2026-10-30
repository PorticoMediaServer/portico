package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Receipts follow the concrete admitted worker generation/job IDs, not whichever
// job happens to have completed most recently for an item.
type repairWorkReceipt struct {
	Worker     string   `json:"worker"`
	Generation int64    `json:"generation,omitempty"`
	Selected   string   `json:"selected,omitempty"`
	Fence      string   `json:"fence,omitempty"`
	Discover   bool     `json:"discover,omitempty"`
	Jobs       []string `json:"jobs,omitempty"`
}

func (s *Service) admitRepairCascade(ctx context.Context, tx *sql.Tx, t RepairTarget, snap RepairSnapshot, intent, actor string) (repairWorkReceipt, string, string, error) {
	r := repairWorkReceipt{}
	if intent == "repair_assets" {
		r.Worker = "artwork"
		r.Discover = len(snap.Artwork) == 0
		if err := s.repairArtworkTx(ctx, tx, t, RepairCommand{Action: "repair_assets"}, actor, s.publicationTime().Format(time.RFC3339Nano)); err != nil {
			return r, "", "", err
		}
		var err error
		r.Fence, err = artworkFence(ctx, tx, t)
		if err != nil {
			return r, "", "", err
		}
		entity, err := resolveEntity(ctx, tx, t.ID)
		if err != nil {
			return r, "", "", err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM artwork_jobs WHERE kind=? AND entity_id=? AND actor=? AND preview=0 ORDER BY id`, t.Kind, entity, actor)
		if err != nil {
			return r, "", "", err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return r, "", "", err
			}
			r.Jobs = append(r.Jobs, id)
		}
		err = rows.Err()
		rows.Close()
		return r, "queued", "", err
	}
	if snap.Identity == nil || snap.Identity.ID == "" || snap.Identity.ID == "0" {
		return r, "skipped", "no_accepted_provider_identity", nil
	}
	if snap.Identity.Engine == "screen" {
		b, err := readScreenBase(ctx, tx, t.Kind, t.ID)
		if err != nil {
			return r, "", "", err
		}
		if b.Accepted == "" {
			return r, "skipped", "identity_not_yet_published", nil
		}
		if b.ItemKind == "episode" && b.ParentProvider == "tvdb" {
			return r, "skipped", "tvdb_episode_requires_parent_refresh", nil
		}
		var published string
		if err = tx.QueryRowContext(ctx, `SELECT provider||':'||provider_type||':'||provider_id FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&published); err != nil {
			return r, "", "", err
		}
		if published != b.Provider+":"+b.ProviderType+":"+b.ProviderID {
			return r, "skipped", "replacement_identity_pending", nil
		}
		if err = queueScreenRepair(ctx, tx, t, RepairCommand{Action: "retry"}); err != nil {
			return r, "", "", err
		}
		r.Worker = "screen"
		err = tx.QueryRowContext(ctx, `SELECT generation,provider||':'||provider_type||':'||provider_id FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, t.Kind, b.Entity).Scan(&r.Generation, &r.Selected)
		return r, "queued", "", err
	}
	if snap.Identity.Provider == "musicbrainz" {
		entity, err := resolveEntity(ctx, tx, t.ID)
		if err != nil {
			return r, "", "", err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mb_jobs SET status='pending',attempts=0,next_attempt='',error='',generation=generation+1,revision=revision+1,lease='',lease_until='' WHERE kind='song' AND entity_id=? AND selected_id<>''`, entity); err != nil {
			return r, "", "", err
		}
		r.Worker = "musicbrainz"
		err = tx.QueryRowContext(ctx, `SELECT generation,selected_id FROM mb_jobs WHERE kind='song' AND entity_id=?`, entity).Scan(&r.Generation, &r.Selected)
		return r, "queued", "", err
	}
	return r, "skipped", "no_independent_refresh_worker", nil
}

func (s *Service) reconcileRepairCascades(ctx context.Context) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	// Old P08C admissions have no reliable generation receipt. Do not turn an
	// unrelated current completion into claimed historical success.
	if _, err = tx.ExecContext(ctx, `UPDATE metadata_repair_cascade_items SET status='failed',reason='legacy_admission_has_no_completion_receipt' WHERE status='queued' AND NOT EXISTS(SELECT 1 FROM metadata_repair_work_receipts r WHERE r.operation_id=metadata_repair_cascade_items.operation_id AND r.item_id=metadata_repair_cascade_items.item_id)`); err != nil {
		return err
	}
	now := s.publicationTime()
	rows, err := tx.QueryContext(ctx, `SELECT r.operation_id,r.item_id,r.receipt FROM metadata_repair_work_receipts r JOIN metadata_repair_cascade_items i ON i.operation_id=r.operation_id AND i.item_id=r.item_id WHERE i.status='queued' AND r.next_check<=? ORDER BY r.next_check,r.operation_id,r.item_id LIMIT 128`, now.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	type pending struct {
		op   string
		item int64
		raw  string
	}
	batch := []pending{}
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.op, &p.item, &p.raw); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range batch {
		var r repairWorkReceipt
		status, reason := "failed", "invalid_completion_receipt"
		if json.Unmarshal([]byte(p.raw), &r) == nil {
			target := RepairTarget{"item", entityPublic(ctx, tx, p.item)}
			status, reason, err = s.repairWorkStatus(ctx, tx, target, &r)
			if err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_repair_cascade_items SET status=?,reason=? WHERE operation_id=? AND item_id=? AND status='queued'`, status, reason, p.op, p.item); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE metadata_repair_work_receipts SET receipt=?,next_check=? WHERE operation_id=? AND item_id=?`, screenRaw(r), now.Add(2*time.Second).Format(time.RFC3339Nano), p.op, p.item); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE metadata_repair_cascades SET failed=(SELECT count(*) FROM metadata_repair_cascade_items i WHERE i.operation_id=metadata_repair_cascades.id AND i.status IN('failed','superseded')),status=CASE WHEN status='queued' AND NOT EXISTS(SELECT 1 FROM metadata_repair_cascade_items i WHERE i.operation_id=metadata_repair_cascades.id AND i.status='queued') THEN CASE WHEN EXISTS(SELECT 1 FROM metadata_repair_cascade_items i WHERE i.operation_id=metadata_repair_cascades.id AND i.status<>'complete') THEN 'partial' ELSE 'complete' END ELSE status END WHERE status IN('pending','queued','cancelled')`); err != nil {
		return err
	}
	return gated.Commit()
}
func (s *Service) repairWorkStatus(ctx context.Context, tx *sql.Tx, t RepairTarget, r *repairWorkReceipt) (string, string, error) {
	if _, err := readRepairEntity(ctx, tx, t); errors.Is(err, sql.ErrNoRows) {
		return "superseded", "entity_removed", nil
	} else if err != nil {
		return "", "", err
	}
	if r.Worker == "artwork" {
		return s.repairArtworkWorkStatus(ctx, tx, t, r)
	}
	var generation int64
	var selected, status string
	var err error
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return "", "", err
	}
	switch r.Worker {
	case "screen":
		err = tx.QueryRowContext(ctx, `SELECT generation,provider||':'||provider_type||':'||provider_id,status FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, t.Kind, entity).Scan(&generation, &selected, &status)
	case "musicbrainz":
		err = tx.QueryRowContext(ctx, `SELECT generation,selected_id,status FROM mb_jobs WHERE kind='song' AND entity_id=?`, entity).Scan(&generation, &selected, &status)
	default:
		return "failed", "unsupported_completion_receipt", nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "superseded", "worker_removed", nil
	}
	if err != nil {
		return "", "", err
	}
	if generation != r.Generation || selected != r.Selected {
		return "superseded", "newer_identity_or_work", nil
	}
	return repairTerminalStatus(status)
}
func repairTerminalStatus(status string) (string, string, error) {
	switch status {
	case "matched", "complete":
		return "complete", "", nil
	case "pending", "searching", "running", "retry", "pending_children", "pending_search", "pending_details", "pending_apply", "pending_episodes":
		return "queued", "", nil
	case "disabled", "provider_disabled", "consent_required", "needs_consent", "manual_preserved", "needs_parent_match", "delegated_tvdb":
		return "skipped", status, nil
	default:
		return "failed", status, nil
	}
}
func (s *Service) repairArtworkWorkStatus(ctx context.Context, tx *sql.Tx, t RepairTarget, r *repairWorkReceipt) (string, string, error) {
	current, err := artworkFence(ctx, tx, t)
	if err != nil {
		return "", "", err
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return "", "", err
	}
	if current != r.Fence {
		return "superseded", "source_identity_or_policy_changed", nil
	}
	if r.Discover {
		var dirty bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artwork_dirty WHERE kind=? AND entity_id=?)`, t.Kind, entity).Scan(&dirty); err != nil {
			return "", "", err
		}
		if dirty {
			return "queued", "", nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT status FROM artwork_discovery WHERE kind=? AND entity_id=? AND source_fence=?`, t.Kind, entity, r.Fence)
		if err != nil {
			return "", "", err
		}
		waiting, failed, skipped := false, "", ""
		for rows.Next() {
			var status string
			if err = rows.Scan(&status); err != nil {
				rows.Close()
				return "", "", err
			}
			st, why, _ := repairTerminalStatus(status)
			waiting = waiting || st == "queued"
			if st == "failed" {
				failed = why
			}
			if st == "skipped" {
				skipped = why
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", "", err
		}
		if waiting {
			return "queued", "", nil
		}
		if failed != "" {
			return "failed", failed, nil
		}
		if skipped != "" {
			return "skipped", skipped, nil
		}
		rows, err = tx.QueryContext(ctx, `SELECT id FROM artwork_jobs WHERE kind=? AND entity_id=? AND source_fence=? AND preview=0`, t.Kind, entity, r.Fence)
		if err != nil {
			return "", "", err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return "", "", err
			}
			r.Jobs = append(r.Jobs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", "", err
		}
		r.Discover = false
	}
	if len(r.Jobs) == 0 {
		return "skipped", "no_supported_artwork_candidates", nil
	}
	waiting := false
	terminal, why := "complete", ""
	for _, id := range r.Jobs {
		var status string
		err = tx.QueryRowContext(ctx, `SELECT status FROM artwork_jobs WHERE id=? AND kind=? AND entity_id=? AND preview=0`, id, t.Kind, entity).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return "superseded", "newer_artwork_choice_or_work", nil
		}
		if err != nil {
			return "", "", err
		}
		st, reason, _ := repairTerminalStatus(status)
		if st == "queued" {
			waiting = true
		} else if st != "complete" {
			terminal, why = st, reason
		}
	}
	if waiting {
		return "queued", "", nil
	}
	return terminal, why, nil
}
