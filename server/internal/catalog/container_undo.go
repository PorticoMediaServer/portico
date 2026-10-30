package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// A container action is atomic and bounded; an oversized selector must be split
// into seasons. The journal expires quickly and holds only watched/resume state.
const containerActionMaximum = 5000
const containerUndoLifetime = 30 * time.Second

type ContainerUndoMutation struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	UndoToken        string `json:"undoToken"`
}

func containerMemberIDs(ctx context.Context, tx *sql.Tx, members string, container int64) ([]int64, error) {
	rows, e := tx.QueryContext(ctx, `SELECT entity_id FROM (`+members+`) ORDER BY entity_id LIMIT ?`, container, containerActionMaximum+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(ids) > containerActionMaximum {
		return nil, jobInvalid("mark smaller containers separately")
	}
	return ids, nil
}

func captureContainerUndo(ctx context.Context, tx *sql.Tx, profile, kind string, container int64, previous ContainerPersonal, clear string, out *ContainerPersonal, ids []int64) error {
	// One live receipt per container; expired snapshots are removed on subsequent
	// actions. Bound each cleanup so a busy household cannot monopolize a write.
	if _, e := tx.ExecContext(ctx, `DELETE FROM container_personal_undo WHERE token IN(SELECT token FROM container_personal_undo WHERE expires_ms<? LIMIT 1) OR (profile_id=? AND kind=? AND container_id=?)`, time.Now().UnixMilli(), profile, kind, container); e != nil {
		return e
	}
	until := time.Now().UTC().Add(containerUndoLifetime)
	out.UndoUntil = until.Format("2006-01-02T15:04:05.000Z")
	if _, e := tx.ExecContext(ctx, `INSERT INTO container_personal_undo(token,profile_id,kind,container_id,revision,previous_watched,previous_watermark,previous_clear,watermark,expires_ms,member_count) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, out.UndoToken, profile, kind, container, out.Revision, previous.Watched, previous.Watermark, clear, out.Watermark, until.UnixMilli(), len(ids)); e != nil {
		return e
	}
	intentTime, e := time.Parse(time.RFC3339Nano, out.Watermark)
	if e != nil {
		return e
	}
	intentAt := intentTime.Add(time.Nanosecond).Format("2006-01-02T15:04:05.000000000Z")
	states, err := readPersonalBatch(ctx, tx, profile, ids)
	if err != nil {
		return err
	}
	if err = snapshotPersonalBatch(ctx, tx, profile, ids, states); err != nil {
		return err
	}
	raw, _ := json.Marshal(ids)
	_, err = tx.ExecContext(ctx, `INSERT INTO container_personal_undo_items(token,item_id,previous_watched,after_revision,position,unit,playback_id,activity_at,activity_state,book_position,previous_intent,previous_authored_at,intent_at,added_ms,show_id,season_id,album_id,book_id)
 SELECT ?,e.id,COALESCE(`+personalstate.SQL("scope.profile", "e.id")+`,0),COALESCE(pi.revision,0)+1,p.position,p.unit,p.playback_id,a.updated_at,a.state,
 (SELECT b.position FROM book_resume b WHERE b.profile_id=scope.profile AND b.item_id=e.id),wi.watched,wi.authored_at,?,e.added_ms,
 (SELECT show_id FROM catalog_episodes WHERE entity_id=e.id),(SELECT season_id FROM catalog_episodes WHERE entity_id=e.id),(SELECT album_id FROM catalog_songs WHERE entity_id=e.id),(SELECT book_id FROM catalog_book_files WHERE entity_id=e.id)
 FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.id=CAST(requested.value AS INTEGER) CROSS JOIN (SELECT ? profile) scope
 LEFT JOIN personal_items pi ON pi.profile_id=scope.profile AND pi.item_id=e.id LEFT JOIN progress p ON p.profile_id=scope.profile AND p.item_id=e.id
 LEFT JOIN progress_activity a ON a.profile_id=scope.profile AND a.item_id=e.id LEFT JOIN personal_watched_intents wi ON wi.profile_id=scope.profile AND wi.item_id=e.id`, out.UndoToken, intentAt, string(raw), profile)
	if err != nil {
		return err
	}
	return nil
}

func invalidateContainer(ctx context.Context, tx *sql.Tx, profile string, container int64) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO viewer_revisions(profile_id,library_id,revision,title_path) SELECT ?,cl.library_id,1,'' FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? ON CONFLICT(profile_id,library_id) DO UPDATE SET revision=revision+1,title_path=excluded.title_path`, profile, container)
	return e
}

func (s *Service) UndoContainerPersonal(ctx context.Context, p identity.Principal, kind, id string, m ContainerUndoMutation, access BulkAccess) (out ContainerPersonal, err error) {
	if !personalOperation.MatchString(m.UndoToken) || m.ExpectedRevision < 1 || m.ExpectedRevision > 9007199254740991 {
		return out, jobInvalid("invalid container undo")
	}
	members, e := containerMembers(kind)
	if e != nil {
		return out, e
	}
	profile := identity.PersonalKey(p.Viewer)
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		visible, bound, e := access(ctx, tx, p, "i.id")
		if e != nil {
			return e
		}
		prior, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
		if e != nil {
			return e
		}
		if prior != nil {
			if e = checkContainerReplayAccess(ctx, tx, id, members, visible, bound); e != nil {
				return e
			}
			out, e = readContainerMutation(ctx, tx, profile, kind, id, prior.Revision)
			return e
		}
		container, e := entityid.Resolve(ctx, tx, id)
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		if e != nil {
			return e
		}
		var present int
		if e = tx.QueryRowContext(ctx, `SELECT 1 FROM (`+members+`) m JOIN catalog_entities i ON i.id=m.entity_id WHERE `+visible+` LIMIT 1`, append([]any{container}, bound...)...).Scan(&present); e != nil {
			return e
		}
		var previousWatermark, clear, mark string
		var expires, revision int64
		var memberCount int
		e = tx.QueryRowContext(ctx, `SELECT u.previous_watched,u.previous_watermark,u.previous_clear,u.watermark,u.expires_ms,c.revision,u.member_count FROM container_personal_undo u JOIN container_personal_state c ON c.profile_id=u.profile_id AND c.kind=u.kind AND c.container_id=u.container_id AND c.revision=u.revision WHERE u.token=? AND u.profile_id=? AND u.kind=? AND u.container_id=?`, m.UndoToken, profile, kind, container).Scan(&out.Watched, &previousWatermark, &clear, &mark, &expires, &revision, &memberCount)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrPersonalConflict
		}
		if e != nil {
			return e
		}
		if expires < time.Now().UnixMilli() {
			return ErrOperationExpired
		}
		if revision != m.ExpectedRevision {
			return ErrPersonalConflict
		}
		ids, e := containerMemberIDs(ctx, tx, members, container)
		if e != nil {
			return e
		}
		// Refuse to overwrite later item actions, playback, nested container marks,
		// or changed membership. Restoring only some members would not be a true Undo.
		var changed int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM container_personal_undo_items u LEFT JOIN personal_items pi ON pi.profile_id=? AND pi.item_id=u.item_id LEFT JOIN progress p ON p.profile_id=? AND p.item_id=u.item_id LEFT JOIN progress_activity a ON a.profile_id=? AND a.item_id=u.item_id LEFT JOIN personal_watched_intents wi ON wi.profile_id=? AND wi.item_id=u.item_id JOIN catalog_entities current ON current.id=u.item_id WHERE u.token=? AND (pi.revision IS NOT u.after_revision OR p.position IS NOT CASE WHEN u.position IS NULL THEN NULL ELSE 0 END OR p.unit IS NOT u.unit OR p.playback_id IS NOT u.playback_id OR a.updated_at IS NOT u.activity_at OR a.state IS NOT CASE WHEN u.activity_state IS NULL THEN NULL ELSE 'ended' END OR wi.authored_at IS NOT u.intent_at OR current.added_ms IS NOT u.added_ms
 OR (SELECT show_id FROM catalog_episodes WHERE entity_id=u.item_id) IS NOT u.show_id
 OR (SELECT season_id FROM catalog_episodes WHERE entity_id=u.item_id) IS NOT u.season_id
 OR (SELECT album_id FROM catalog_songs WHERE entity_id=u.item_id) IS NOT u.album_id
 OR (SELECT book_id FROM catalog_book_files WHERE entity_id=u.item_id) IS NOT u.book_id)`, profile, profile, profile, profile, m.UndoToken).Scan(&changed); e != nil {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM container_personal_undo_items WHERE token=?`, m.UndoToken).Scan(&count); e != nil {
			return e
		}
		if changed != 0 || count != len(ids) || count != memberCount {
			return ErrPersonalConflict
		}
		rawIDs, _ := json.Marshal(ids)
		var invalid bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM json_each(?) requested WHERE NOT EXISTS(SELECT 1 FROM container_personal_undo_items u WHERE u.token=? AND u.item_id=CAST(requested.value AS INTEGER)))`, string(rawIDs), m.UndoToken).Scan(&invalid); e != nil {
			return e
		}
		if invalid {
			return ErrPersonalConflict
		}
		// Every overlapping default is a full primary-key probe from the captured
		// member, rather than a scan of all defaults marked by this profile.
		parts := []string{}
		args := []any{string(rawIDs)}
		for _, scope := range []struct{ kind, table, column string }{{"show", "catalog_episodes", "show_id"}, {"season", "catalog_episodes", "season_id"}, {"album", "catalog_songs", "album_id"}, {"book", "catalog_book_files", "book_id"}} {
			parts = append(parts, `EXISTS(SELECT 1 FROM container_personal_state c WHERE c.profile_id=? AND c.kind='`+scope.kind+`' AND c.container_id=(SELECT `+scope.column+` FROM `+scope.table+` WHERE entity_id=CAST(requested.value AS INTEGER)) AND c.watermark>?)`)
			args = append(args, profile, mark)
		}
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM json_each(?) requested WHERE `+strings.Join(parts, " OR ")+`)`, args...).Scan(&invalid); e != nil {
			return e
		}
		if invalid {
			return ErrPersonalConflict
		}

		beforeStates, e := readPersonalBatch(ctx, tx, profile, ids)
		if e != nil {
			return e
		}
		if e = snapshotPersonalBatch(ctx, tx, profile, ids, beforeStates); e != nil {
			return e
		}
		out.Revision = revision + 1
		out.Watermark = previousWatermark
		if _, e = tx.ExecContext(ctx, `UPDATE container_personal_state SET watched=?,watermark=?,cleared_through=?,revision=? WHERE profile_id=? AND kind=? AND container_id=?`, out.Watched, previousWatermark, clear, out.Revision, profile, kind, container); e != nil {
			return e
		}
		if e = restoreContainerMemberStates(ctx, tx, profile, m.UndoToken); e != nil {
			return e
		}

		restored, e := readPersonalBatch(ctx, tx, profile, ids)
		if e != nil {
			return e
		}
		if e = snapshotPersonalBatch(ctx, tx, profile, ids, restored); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM container_personal_undo WHERE token=?`, m.UndoToken); e != nil {
			return e
		}
		if e = invalidateContainer(ctx, tx, profile, container); e != nil {
			return e
		}
		out.Watchlisted, out.Favorite, e = readContainerSaved(ctx, tx, profile, container)
		if e != nil {
			return e
		}
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: id, Revision: out.Revision}, time.Now().UnixMilli())
	})
	return
}
