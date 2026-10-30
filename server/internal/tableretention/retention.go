// Package tableretention bounds disposable command and observation records.
// Physical media cleanup remains with the subsystem that owns its leases.
package tableretention

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync"
	"time"
)

type Policy struct {
	Table     string
	Days      int
	Predicate string
}

// Each row is timestamped on insert by the baseline's retention triggers. The
// predicate is rechecked under the writer gate, so an active resource is never
// removed merely because it was created long ago.
var Policies = []Policy{
	{"hosted_membership_endings", 30, `expires_ms<=CAST(unixepoch('subsec')*1000 AS INTEGER)`},
	{"metadata_owner_history", 30, `(kind='person' AND NOT EXISTS(SELECT 1 FROM catalog_people p WHERE p.id=d.entity_id)) OR (kind<>'person' AND NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=d.entity_id))`},
	{"analysis_probe_bindings", 1, `NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=d.object_id AND o.revision=d.source_revision)`},
	{"analysis_marker_sets", 30, `NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=d.object_id AND o.revision=d.source_revision) AND NOT EXISTS(SELECT 1 FROM analysis_markers m WHERE m.object_id=d.object_id AND m.source_revision=d.source_revision)`},
	{"analysis_markers", 30, `NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=d.object_id AND o.revision=d.source_revision)`},
	{"metadata_relationship_decisions", 30, `NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=d.entity_id)`},
	{"artwork_jobs", 30, `status NOT IN('running','pending','retry') AND ((kind='person' AND NOT EXISTS(SELECT 1 FROM catalog_people p WHERE p.id=d.entity_id)) OR (kind<>'person' AND NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=d.entity_id)))`},
	{"subtitle_sidecars", 30, `NOT EXISTS(SELECT 1 FROM asset_subtitles t WHERE t.sidecar_id=d.id)`},
	{"personal_device_sequences", 30, `NOT EXISTS(SELECT 1 FROM identity_devices dev WHERE dev.id=d.device_id)`},
	{"remote_analysis_status", 30, `NOT EXISTS(SELECT 1 FROM catalog_assets a WHERE a.path=d.path)`},
	{"remote_object_refs", 30, `NOT EXISTS(SELECT 1 FROM catalog_assets a WHERE a.path=d.path)`},

	{"playback_requests", 30, `NOT EXISTS(SELECT 1 FROM playback_sessions s WHERE s.item_id=d.item_id AND s.account_id=d.account_id AND s.profile_id=d.profile_id AND s.state NOT IN('stopped','ended','failed'))`},
	{"playback_legacy_replacement_requests", 30, `NOT EXISTS(SELECT 1 FROM playback_sessions s WHERE s.id=d.old_session_id AND s.state NOT IN('stopped','ended','failed'))`},
	{"subtitle_operations", 30, `1`},
	{"playback_marker_skips", 30, `NOT EXISTS(SELECT 1 FROM playback_v1_sessions s WHERE s.id=d.playback_id AND s.ended_ms=0)`},
	{"analysis_marker_history", 90, `1`},
	{"admin_receipts", 30, `1`},
	{"queue_v1_container_counts", 7, `1`},
	{"social_rate", 1, `window_start_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)-86400000`},
	{"social_cast_bootstraps", 7, `expires_at_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)`},
	{"social_cast_devices", 30, `state IN('stopped','expired','revoked') AND expires_at_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)`},
	{"social_receivers", 30, `state='retired' AND NOT EXISTS(SELECT 1 FROM social_cast_devices c WHERE c.receiver_id=d.id AND c.state IN('active','pending'))`},
	{"social_groups", 30, `state IN('ended','failed') AND updated_at_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)-2592000000 AND NOT EXISTS(SELECT 1 FROM queues_v1 q WHERE q.owner_kind='group' AND q.owner_id=d.id)`},
	{"access_invitations", 30, `expires_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)`},
	{"prepared_media_jobs", 30, `state IN('cancelled','failed','succeeded') AND updated_ms<CAST(unixepoch('subsec')*1000 AS INTEGER)-2592000000 AND generation<=cleaned_generation AND NOT EXISTS(SELECT 1 FROM prepared_media_versions v WHERE v.job_id=d.id)`},
	{"prepared_media_versions", 30, `state='deleted' AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins p WHERE p.version_id=d.id)`},
	{"lc_playback_refs", 1, `lease_until_ms<CAST(unixepoch('subsec')*1000 AS INTEGER) AND NOT EXISTS(SELECT 1 FROM playback_channel_sessions s JOIN playback_v1_sessions v ON v.id=s.session_id WHERE s.session_id=d.playback_id AND v.ended_ms=0)`},
	{"live_allocations", 7, `state='released'`},
	{"live_reservations", 7, `end_ms<CAST(unixepoch('subsec')*1000 AS INTEGER) AND NOT EXISTS(SELECT 1 FROM dvr_recordings r WHERE r.id=d.id AND r.state NOT IN('deleted','failed','cancelled'))`},
}

// retentionCursor is the last age-ledger entry a policy examined. The next
// sweep resumes just after it, so rows that stay protected by their predicate
// are walked once per pass instead of once per run. It lives only for the
// process lifetime and is scoped to the database handle it was measured on: a
// restart, or another database, simply begins again at the oldest row.
type retentionCursor struct {
	db      *sql.DB
	created int64
	key     string
	set     bool
}

var (
	cursorMu sync.Mutex
	cursors  = map[string]retentionCursor{}
)

// resetCursors makes the next sweep of every policy begin at the oldest row.
func resetCursors() {
	cursorMu.Lock()
	cursors = map[string]retentionCursor{}
	cursorMu.Unlock()
}

func loadCursor(db *sql.DB, table string) retentionCursor {
	cursorMu.Lock()
	defer cursorMu.Unlock()
	cursor := cursors[table]
	if !cursor.set || cursor.db != db {
		return retentionCursor{}
	}
	return cursor
}

func storeCursor(db *sql.DB, table string, cursor retentionCursor) {
	cursorMu.Lock()
	defer cursorMu.Unlock()
	if !cursor.set {
		delete(cursors, table)
		return
	}
	cursor.db = db
	cursors[table] = cursor
}

// retentionPage is the bounded candidate slice a sweep examined, in age order.
// Its length is the number of rows looked at, not the number deleted.
type retentionPage struct {
	length int
	last   retentionCursor
}

// readPage reads at most limit age-ledger entries after the cursor. It names
// maintenance_retention_age so SQLite walks the age order without a sort.
func readPage(ctx context.Context, tx *sql.Tx, table string, cursor retentionCursor, cutoff int64, limit int) (retentionPage, error) {
	query := `SELECT created_ms,record_key FROM maintenance_retention_rows INDEXED BY maintenance_retention_age
 WHERE table_name=? AND created_ms<?`
	args := []any{table, cutoff}
	if cursor.set {
		// The plain range test is redundant with the OR form, but it gives
		// SQLite an index lower bound instead of a start-of-range scan.
		query += ` AND created_ms>=? AND (created_ms>? OR (created_ms=? AND record_key>?))`
		args = append(args, cursor.created, cursor.created, cursor.created, cursor.key)
	}
	query += ` ORDER BY created_ms,record_key LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return retentionPage{}, err
	}
	defer rows.Close()
	page := retentionPage{}
	for rows.Next() {
		var created int64
		var key string
		if err = rows.Scan(&created, &key); err != nil {
			return retentionPage{}, err
		}
		page.length++
		page.last = retentionCursor{created: created, key: key, set: true}
	}
	return page, rows.Err()
}

// candidatePage appends the "same bounded page" restriction, from the cursor
// up to and including last, to a query that already selected from
// maintenance_retention_rows. The redundant created_ms range tests alongside
// the OR form let the age index resolve both ends of the page, so the OR only
// disambiguates keys that share a millisecond.
func candidatePage(cursor retentionCursor, last retentionCursor, args []any) (string, []any) {
	query := ``
	if cursor.set {
		query += ` AND created_ms>=? AND (created_ms>? OR (created_ms=? AND record_key>?))`
		args = append(args, cursor.created, cursor.created, cursor.created, cursor.key)
	}
	query += ` AND created_ms<=? AND (created_ms<? OR (created_ms=? AND record_key<=?))`
	args = append(args, last.created, last.created, last.created, last.key)
	return query, args
}

// Sweep removes at most limit records per table, each in its own short
// maintenance transaction. It bounds the rows examined, not only the rows
// deleted: each policy walks one candidate page of its age ledger per call, so
// rows that stay protected by their predicate are not rescanned from the start
// on every run. It returns true while a full page needs another turn. Calls do
// not hold a reader or writer while pacing background work.
func Sweep(ctx context.Context, db *sql.DB, now time.Time, limit int) (bool, error) {
	if limit < 1 {
		limit = 128
	}
	if limit > 256 {
		limit = 256
	}
	more := false
	for _, p := range Policies {
		if !dbwork.WaitBackground(ctx) {
			return more, ctx.Err()
		}
		ctx = dbwork.WithBackgroundSpeed(ctx, dbwork.ReadBackgroundSpeed(ctx, db))
		started := time.Now()
		batchLimit := min(limit, 32*dbwork.BackgroundConcurrency(ctx))
		cutoff := now.Add(-time.Duration(p.Days) * 24 * time.Hour).UnixMilli()
		if p.Table == "social_groups" || p.Table == "social_receivers" {
			backlog, err := pruneParent(ctx, db, p, cutoff, batchLimit)
			if err != nil {
				return more, err
			}
			more = more || backlog
			if !dbwork.PaceBackground(ctx, started) {
				return more, ctx.Err()
			}
			continue
		}
		key := "rowid"
		if p.Table == "queue_v1_container_counts" {
			key = "selector"
		}
		cursor := loadCursor(db, p.Table)
		var page retentionPage
		err := dbwork.WithWriteTxContext(ctx, db, dbwork.ClassMaintenance, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			page, err = readPage(ctx, tx, p.Table, cursor, cutoff, batchLimit)
			if err != nil || page.length == 0 {
				return err
			}
			query := `DELETE FROM ` + p.Table + ` WHERE ` + key + ` IN (
 SELECT d.` + key + ` FROM (SELECT record_key,created_ms FROM maintenance_retention_rows INDEXED BY maintenance_retention_age
  WHERE table_name=? AND created_ms<?`
			args := []any{p.Table, cutoff}
			var rangeClause string
			rangeClause, args = candidatePage(cursor, page.last, args)
			query += rangeClause + ` ORDER BY created_ms,record_key LIMIT ?) age
 JOIN ` + p.Table + ` d ON d.` + key + `=age.record_key WHERE (` + p.Predicate + `))`
			args = append(args, batchLimit)
			_, err = tx.ExecContext(ctx, query, args...)
			return err
		})
		if err != nil {
			return more, err
		}
		full := page.length == batchLimit
		if full {
			storeCursor(db, p.Table, page.last)
			more = true
		} else {
			storeCursor(db, p.Table, retentionCursor{})
		}
		if !dbwork.PaceBackground(ctx, started) {
			return more, ctx.Err()
		}
	}
	return more, nil
}

// Dismantle an expired group's or receiver's child history before its parent. A cascade can
// otherwise turn one nominal parent batch into hundreds of thousands of writes.
func pruneParent(ctx context.Context, db *sql.DB, policy Policy, cutoff int64, limit int) (bool, error) {
	more := false
	children := []string{"social_group_queue", "social_group_invites", "social_group_receipts", "social_group_events", "social_group_members"}
	foreignKey := "group_id"
	if policy.Table == "social_receivers" {
		children = []string{"social_handoffs", "social_receiver_grants"}
		foreignKey = "receiver_id"
	}
	cursor := loadCursor(db, policy.Table)
	var next retentionCursor
	advance := false
	err := dbwork.WithWriteTxContext(ctx, db, dbwork.ClassMaintenance, func(ctx context.Context, tx *sql.Tx) error {
		page, err := readPage(ctx, tx, policy.Table, cursor, cutoff, limit)
		if err != nil {
			return err
		}
		if page.length == 0 {
			advance = true
			return nil
		}
		full := page.length == limit
		var id string
		var found retentionCursor
		eligible := `SELECT age.created_ms,age.record_key,d.id FROM (SELECT record_key,created_ms FROM maintenance_retention_rows INDEXED BY maintenance_retention_age
 WHERE table_name=? AND created_ms<?`
		args := []any{policy.Table, cutoff}
		var rangeClause string
		rangeClause, args = candidatePage(cursor, page.last, args)
		eligible += rangeClause + ` ORDER BY created_ms,record_key LIMIT ?) age
 JOIN ` + policy.Table + ` d ON d.rowid=age.record_key WHERE (` + policy.Predicate + `) ORDER BY age.created_ms,age.record_key LIMIT 1`
		args = append(args, limit)
		err = tx.QueryRowContext(ctx, eligible, args...).Scan(&found.created, &found.key, &id)
		if errors.Is(err, sql.ErrNoRows) {
			advance = true
			if full {
				next = page.last
				more = true
			}
			return nil
		}
		if err != nil {
			return err
		}
		found.set = true
		for _, table := range children {
			result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE `+foreignKey+`=? LIMIT ?)`, id, limit)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count > 0 {
				more = true
				return nil
			}
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM `+policy.Table+` WHERE id=?`, id); err != nil {
			return err
		}
		advance = true
		next = found
		more = true
		return nil
	})
	if err != nil {
		return more, err
	}
	if advance {
		storeCursor(db, policy.Table, next)
	}
	return more, err
}

// Run owns its own timer so an explicit background pause cannot stall WAL
// checkpoint monitoring or the owner's control-plane housekeeping.
func Run(ctx context.Context, db *sql.DB) {
	delay := 15 * time.Minute
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		more, err := Sweep(ctx, db, time.Now(), 256)
		delay = 15 * time.Minute
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("Record retention could not finish: %v", err)
		} else if more {
			delay = time.Second
		}
	}
}
