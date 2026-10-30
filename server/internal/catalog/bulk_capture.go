package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"time"
)

func bulkSelectionRevisionFence(q interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}, v Viewer, selector JobSelector) (string, error) {
	rows, e := q.Query(`SELECT l.library_id,l.revision,COALESCE(v.revision,0) FROM library_revisions l LEFT JOIN viewer_revisions v ON v.library_id=l.library_id AND v.profile_id=? WHERE l.library_id IN(SELECT value FROM json_each(?)) ORDER BY l.library_id`, v.Profile, v.librariesJSON())
	if e != nil {
		return "", e
	}
	values := []any{}
	for rows.Next() {
		var id string
		var cat, personal int64
		if e = rows.Scan(&id, &cat, &personal); e != nil {
			rows.Close()
			return "", e
		}
		values = append(values, []any{id, cat, personal})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", e
	}
	if c := selector.Container; c != nil && c.Kind == "playlist" {
		var revision int64
		var deleted bool
		if e = q.QueryRow(`SELECT revision,deleted FROM catalog_playlists WHERE token=?`, c.ID).Scan(&revision, &deleted); e != nil {
			return "", e
		}
		values = append(values, []any{c.ID, revision, deleted})
	}
	return operationHash(values), nil
}

// Capture is keyset-paged and restartable. The source catalogue and personal
// revision fence must remain the accepted one until the last key is copied.
// A changed source fails without applying anything, rather than silently growing
// the confirmed set. No HTTP request enumerates a library or holds this snapshot.
func (s *Service) bulkCaptureStep(ctx context.Context, id string, access BulkAccess) (out BulkJob, err error) {
	snap, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer snap.Rollback()
	var query, rawArgs, rawScope, rawSelector, rawP, rawA, command, fence, cursor string
	var total int64
	e = snap.Tx().QueryRowContext(ctx, `SELECT selection_sql,selection_args,selection_scope,selector,principal,args,command,selection_fence,selection_cursor,total FROM personal_jobs WHERE id=? AND state='building'`, id).Scan(&query, &rawArgs, &rawScope, &rawSelector, &rawP, &rawA, &command, &fence, &cursor, &total)
	if e != nil {
		return out, e
	}
	var args []any
	var v Viewer
	var selector JobSelector
	var p identity.Principal
	var arguments JobPersonalArgs
	for _, entry := range []struct {
		raw string
		to  any
	}{{rawArgs, &args}, {rawScope, &v}, {rawSelector, &selector}, {rawP, &p}, {rawA, &arguments}} {
		if e = json.Unmarshal([]byte(entry.raw), entry.to); e != nil {
			return out, e
		}
	}
	current, e := s.WithContext(snap.Context()).bulkSelectionFence(snap.Tx(), v, selector)
	if errors.Is(e, ErrSelectionBatchStale) {
		current = ""
		e = nil
	}
	if e != nil {
		return out, e
	}
	keys := []int64{}
	replay := query == bulkReplaySelection
	eof := false
	var page BrowseSelectionReplayPage
	defer func() { page.Release() }()
	if current == fence {
		// Captured keys are integer entity ids; the stored cursor is their
		// decimal form ("" before the first batch).
		cursorID := int64(0)
		if cursor != "" {
			if n, e := strconv.ParseInt(cursor, 10, 64); e == nil && n > 0 {
				cursorID = n
			}
		}
		if replay {
			page, e = s.WithContext(snap.Context()).bulkReplayPage(id, cursor, v, selector, p, command, arguments, access)
			if e != nil {
				return out, e
			}
			if page.Preparing {
				return scanBulk(snap.Tx().QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
			}
			keys, eof = page.IDs, page.EOF
			// Retain the existing 20-target accepting writer bound. A prepared
			// page can be larger; its suffix is replayed from the last accepted
			// ID, never persisted as query progress or mistaken for EOF.
			if len(keys) > bulkBatchSize {
				keys, eof = keys[:bulkBatchSize], false
			}
		} else {
			rows, e := snap.Tx().QueryContext(ctx, `SELECT id FROM (`+query+`) selected WHERE id>? ORDER BY id LIMIT ?`, append(args, cursorID, bulkBatchSize)...)
			if e != nil {
				return out, e
			}
			for rows.Next() {
				var key int64
				if e = rows.Scan(&key); e != nil {
					rows.Close()
					return out, e
				}
				keys = append(keys, key)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return out, e
			}
			eof = len(keys) < bulkBatchSize
		}
	}
	expected, sortKeys := map[int64]string{}, map[int64]string{}
	for _, key := range keys {
		if replay {
			if e = s.WithContext(snap.Context()).bulkReplayTarget(snap.Tx(), v, selector, command, key); e != nil {
				return out, e
			}
		}
		if command == "playlist-add" || command == "collection-add" {
			// This stable identity also fences apply against integer row-id reuse.
			var publicID string
			if e = snap.Tx().QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, key).Scan(&publicID); e != nil {
				return out, e
			}
			expected[key] = publicID
		}
		if command == "playlist-add" {
			if replay {
				if len(page.SortKeys) > 0 {
					var ok bool
					sortKeys[key], ok = page.SortKeys[key]
					if !ok || sortKeys[key] == "" {
						return out, ErrSelectionBatchStale
					}
				} else {
					sortKeys[key], e = s.bulkSortKeyReserved(ctx, snap.Tx(), v, selector, key, page.lease.Reserve)
				}
			} else {
				sortKeys[key], e = s.bulkSortKey(ctx, snap.Tx(), v, selector, key)
			}
			if e != nil {
				return out, e
			}
		}
		if jobOwnerCommand(command) && s.BulkCommands.Capture != nil {
			var publicID string
			if e = snap.Tx().QueryRowContext(ctx, `SELECT pid(public_id) FROM catalog_entities WHERE id=?`, key).Scan(&publicID); e != nil {
				return out, e
			}
			expected[key], e = s.BulkCommands.Capture(ctx, snap.Tx(), p, command, publicID, arguments)
			if e != nil {
				return out, e
			}
		}
	}
	snap.Rollback()
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		defer observeBulkTiming(ctx, "capture")()
		var liveCursor, state string
		if e := tx.QueryRowContext(ctx, `SELECT selection_cursor,state FROM personal_jobs WHERE id=?`, id).Scan(&liveCursor, &state); e != nil {
			return e
		}
		if state != "building" || liveCursor != cursor {
			var e error
			out, e = scanBulk(tx.QueryRow(`SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
			return e
		}
		if replay {
			if access == nil {
				return identity.ErrUnauthorized
			}
			if _, _, e := access(ctx, tx, p, "i.id"); e != nil {
				return e
			}
			if e := s.authorizeBulkCommand(ctx, tx, p, command, arguments, false); e != nil {
				return e
			}
		}
		now, e := s.WithContext(ctx).bulkSelectionFence(tx, v, selector)
		if errors.Is(e, ErrSelectionBatchStale) {
			now = ""
			e = nil
		}
		if e != nil {
			return e
		}
		code := ""
		if current != fence || now != fence {
			code = "selection_changed"
		}
		if total+int64(len(keys)) > 10000000 {
			code = "selection_too_large"
		}
		if code != "" {
			if _, e = tx.ExecContext(ctx, `UPDATE personal_jobs SET revision=revision+1,state='failed',total=0,error_code=?,updated_ms=? WHERE id=?`, code, time.Now().UnixMilli(), id); e != nil {
				return e
			}
		} else {
			observeBulkBatch(ctx, "capture", len(keys))
			for index, key := range keys {
				if _, e = tx.ExecContext(ctx, `INSERT INTO personal_job_items VALUES(?,?,?)`, id, total+int64(index)+1, key); e != nil {
					return e
				}
				if command != "personal-state" {
					if _, e = tx.ExecContext(ctx, `INSERT INTO personal_job_details(job_id,item_id,expected,sort_key) VALUES(?,?,?,?)`, id, key, expected[key], sortKeys[key]); e != nil {
						return e
					}
				}
				if command == "playlist-add" || command == "collection-add" {
					if _, e = tx.ExecContext(ctx, `INSERT INTO personal_job_membership_outcomes(job_id,item_public_id,outcome) VALUES(?,pid_blob(?),'pending')`, id, expected[key]); e != nil {
						return e
					}
				}
				liveCursor = strconv.FormatInt(key, 10)
			}
			if eof {
				state = "queued"
			}
			if _, e = tx.ExecContext(ctx, `UPDATE personal_jobs SET revision=revision+1,total=?,selection_cursor=?,state=?,selection_complete=?,updated_ms=? WHERE id=?`, total+int64(len(keys)), liveCursor, state, state == "queued", time.Now().UnixMilli(), id); e != nil {
				return e
			}
		}
		out, e = scanBulk(tx.QueryRowContext(ctx, `SELECT `+bulkColumns+` FROM personal_jobs WHERE id=?`, id))
		if e != nil {
			return e
		}
		return bulkEvent(tx, p, id)
	})
	if err == nil && replay && s.ReadPreparations() != nil {
		_ = s.ReadPreparations().Cancel(context.WithoutCancel(ctx), bulkReplayCaller(id), page.handle)
	}
	return
}
