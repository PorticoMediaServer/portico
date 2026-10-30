package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// The authorizer re-admits the original principal inside this exact snapshot or
// writer. A client projection is a CAS witness, never mutation authority.
type ReadingMarkAllAuthorizer func(context.Context, *sql.Tx) (Viewer, error)
type ReadingMarkAllScope struct {
	ServerID    string          `json:"serverId"`
	LibraryID   string          `json:"libraryId"`
	ViewerFence string          `json:"viewerFence"`
	Revision    ContentRevision `json:"revision"`
	Count       int64           `json:"count"`
}
type ReadingMarkAllCommand struct {
	OperationID      string          `json:"operationId"`
	ViewerFence      string          `json:"viewerFence"`
	ExpectedRevision ContentRevision `json:"expectedRevision"`
}
type ReadingMarkAllReceipt struct {
	ReadingMarkAllScope
	OperationID      string          `json:"operationId"`
	ExpectedRevision ContentRevision `json:"expectedRevision"`
	Changed          int64           `json:"changed"`
}

func validReadingMarkAllRevision(r ContentRevision) bool {
	return r.Catalog >= 0 && r.Viewer >= 0 && r.Catalog <= 9007199254740991 && r.Viewer <= 9007199254740991
}

func (s *Service) readingMarkAllTargets(ctx context.Context, tx *sql.Tx, library string, authorize ReadingMarkAllAuthorizer) (Viewer, string, []any, error) {
	if authorize == nil {
		return Viewer{}, "", nil, identity.ErrForbidden
	}
	viewer, err := authorize(ctx, tx)
	if err != nil {
		return viewer, "", nil, err
	}
	if viewer.Profile == "" || viewer.Fence == "" || !viewer.AllowsLibrary(library) {
		return viewer, "", nil, identity.ErrForbidden
	}
	scope, err := s.readingBrowseScope(library)
	if err != nil {
		return viewer, "", nil, err
	}
	if scope == nil {
		return viewer, "", nil, sql.ErrNoRows
	}
	restriction, args := viewer.itemVisibilitySQL("m.item_id")
	targets := `SELECT DISTINCT m.item_id FROM reading_attribution_members m WHERE ` + scope.memberScope() + ` AND ` + scope.eligibleSQL("m.item_id") + ` AND ` + restriction
	return viewer, targets, args, nil
}

func (s *Service) ReadingMarkAllProjection(server, library string, authorize ReadingMarkAllAuthorizer) (ReadingMarkAllScope, error) {
	var out ReadingMarkAllScope
	err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
		scoped := s.WithContext(ctx)
		viewer, targets, args, err := scoped.readingMarkAllTargets(ctx, dbwork.Snapshot(ctx), library, authorize)
		if err != nil {
			return err
		}
		out = ReadingMarkAllScope{ServerID: server, LibraryID: library, ViewerFence: viewer.Fence}
		out.Revision, err = scoped.ContentRevision(library, viewer.Profile)
		if err != nil {
			return err
		}
		if !validReadingMarkAllRevision(out.Revision) {
			return ErrReadingProjectionChanged
		}
		if err = scoped.read().QueryRowContext(ctx, `SELECT count(*) FROM (`+targets+`)`, args...).Scan(&out.Count); err != nil {
			return err
		}
		if out.Count < 0 || out.Count > 9007199254740991 {
			return ErrReadingProjectionChanged
		}
		return nil
	})
	return out, err
}

// readingMarkAllStateSQL is the same effective personal state read by
// readPersonalStateByID, expressed per normalized target for version snapshots.
// Snapshots remain history; no member list or primary state is serialized.
func readingMarkAllStateSQL() string {
	conflicts := `(SELECT COALESCE(json_group_array(json_object('id',c.id,'field',c.field,'baseRevision',c.base_revision,'baseValue',json(c.base_value),'choices',json((SELECT COALESCE(json_group_array(json_object('operationId',v.operation_id,'deviceId',v.device_id,'authoredAt',v.authored_at,'value',json(v.value))),'[]') FROM personal_explicit_events v WHERE v.profile_id=c.profile_id AND v.item_id=c.item_id AND v.field=c.field AND v.base_revision=c.base_revision)))),'[]') FROM personal_conflicts c WHERE c.profile_id=actor.profile AND c.item_id=t.item_id AND c.resolved=0)`
	boolean := func(expression string) string {
		return `json(CASE WHEN ` + expression + ` THEN 'true' ELSE 'false' END)`
	}
	return `json_object('watched',` + boolean(personalstate.SQL("actor.profile", "t.item_id")) + `,
 'progressSeconds',COALESCE((SELECT position FROM progress WHERE profile_id=actor.profile AND item_id=t.item_id),0)/1000.0,
 'continueDismissed',` + boolean(`EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=actor.profile AND d.item_id=t.item_id AND d.playback_id=COALESCE((SELECT playback_id FROM progress WHERE profile_id=d.profile_id AND item_id=d.item_id),''))`) + `,
 'lastPlayedAt',COALESCE(p.last_played_at,''),'status',CASE WHEN EXISTS(SELECT 1 FROM personal_conflicts c WHERE c.profile_id=actor.profile AND c.item_id=t.item_id AND c.resolved=0) THEN 'needs-resolution' ELSE 'current' END,
 'conflicts',json(` + conflicts + `),'watchlisted',` + boolean("COALESCE(p.watchlisted,0)") + `,'favorite',` + boolean("COALESCE(p.favorite,0)") + `,'notInterested',` + boolean("COALESCE(p.not_interested,0)") + `, 'rating',p.rating,'revision',COALESCE(p.revision,0))`
}

func (s *Service) SetReadingMarkAll(server, library string, in ReadingMarkAllCommand, authorize ReadingMarkAllAuthorizer) (ReadingMarkAllReceipt, error) {
	if err := requireCatalogMutationContext(s.Context(), in.OperationID); err != nil {
		return ReadingMarkAllReceipt{}, err
	}
	var out ReadingMarkAllReceipt
	if !personalOperation.MatchString(in.OperationID) || !personalOperation.MatchString(library) || len(in.ViewerFence) != 64 || !validReadingMarkAllRevision(in.ExpectedRevision) {
		return out, requestinput.New("invalid Reading mark-all intent")
	}
	hash := operationHash(struct {
		Server, Library string
		Command         ReadingMarkAllCommand
	}{server, library, in})
	gated, err := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	ctx, tx := gated.Context(), gated.Tx()
	scoped := s.WithContext(ctx)
	_, _, protocol := usermutation.RequestFrom(ctx)
	if protocol {
		if authorize == nil {
			return out, identity.ErrForbidden
		}
		priorReceipt, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
		if e != nil {
			return out, e
		}
		if priorReceipt != nil {
			current, e := scoped.ReadingMarkAllProjection(server, library, authorize)
			out.ReadingMarkAllScope = current
			out.OperationID, out.ExpectedRevision = in.OperationID, in.ExpectedRevision
			if errors.Is(e, sql.ErrNoRows) || errors.Is(e, ErrReadingProjectionChanged) {
				out.ServerID, out.LibraryID = server, library
				e = nil
			}
			return out, e
		}
	}
	viewer, targets, targetArgs, err := scoped.readingMarkAllTargets(ctx, tx, library, authorize)
	if err != nil {
		return out, err
	}
	if viewer.Fence != in.ViewerFence {
		return out, ErrStaleContinuation
	}
	// Colon is prohibited in generic batch operation IDs. This reserved storage
	// namespace cannot collide with or be overwritten by SetPersonalBatch. Its
	// durable retired domain also prevents a cleaned/expired operation becoming
	// a fresh action. Fresh actor/visibility admission still precedes replay.
	key := "reading-mark-all:" + in.OperationID
	if !protocol {
		var prior, raw string
		err = tx.QueryRowContext(ctx, `SELECT request_hash,response FROM personal_batch_receipts WHERE profile_id=? AND operation_id=? AND created_at>=?`, viewer.Profile, key, time.Now().UTC().Add(-30*24*time.Hour).Format(time.RFC3339)).Scan(&prior, &raw)
		if err == nil {
			if prior != hash {
				return out, ErrOperationConflict
			}
			err = json.Unmarshal([]byte(raw), &out)
			return out, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err = checkRetired(tx, viewer.Profile, "reading-mark-all", in.OperationID, hash); err != nil {
			return out, err
		}
	}
	revision, err := scoped.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	if revision != in.ExpectedRevision {
		return out, ErrPersonalConflict
	}
	cte := `WITH actor(profile) AS (VALUES (?)), targets AS (` + targets + `) `
	args := append([]any{viewer.Profile}, targetArgs...)
	var conflicts, overflow int64
	// Reading's existing setter compares its watched value (including the
	// authored zero resume value) before rereading untouched location/progress.
	// Preserve that revision rule without writing any reading or AV location.
	changed := `NOT COALESCE(` + personalstate.SQL("actor.profile", "t.item_id") + `,0) OR COALESCE((SELECT position FROM progress WHERE profile_id=actor.profile AND item_id=t.item_id),0)<>0`
	err = tx.QueryRowContext(ctx, cte+`SELECT count(*),COALESCE(sum(CASE WHEN `+changed+` THEN 1 ELSE 0 END),0),
 COALESCE(sum(EXISTS(SELECT 1 FROM personal_conflicts c WHERE c.profile_id=actor.profile AND c.item_id=t.item_id AND c.field='watched' AND c.resolved=0)),0),
 COALESCE(sum(CASE WHEN (`+changed+`) AND COALESCE(p.revision,0)>=9007199254740991 THEN 1 ELSE 0 END),0)
 FROM targets t CROSS JOIN actor LEFT JOIN personal_items p ON p.profile_id=actor.profile AND p.item_id=t.item_id`, args...).Scan(&out.Count, &out.Changed, &conflicts, &overflow)
	if err != nil {
		return out, err
	}
	if out.Count < 0 || out.Count > 9007199254740991 {
		return out, ErrReadingProjectionChanged
	}
	if conflicts != 0 {
		return out, ErrPersonalResolution
	}
	if overflow != 0 {
		return out, ErrPersonalConflict
	}
	snapshot := cte + `INSERT OR IGNORE INTO personal_snapshots(profile_id,item_id,revision,state,created_at)
 SELECT actor.profile,t.item_id,COALESCE(p.revision,0),` + readingMarkAllStateSQL() + `,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM targets t CROSS JOIN actor LEFT JOIN personal_items p ON p.profile_id=actor.profile AND p.item_id=t.item_id`
	if _, err = tx.ExecContext(ctx, snapshot, args...); err != nil {
		return out, err
	}
	// Materialize before writing so revision decisions see every old effective
	// intent. Existing personal/viewer invalidation triggers remain authoritative.
	_, err = tx.ExecContext(ctx, cte+`, prior AS MATERIALIZED (SELECT actor.profile,t.item_id,COALESCE(p.watchlisted,0) AS watchlisted,COALESCE(p.favorite,0) AS favorite,COALESCE(p.not_interested,0) AS not_interested,p.rating,COALESCE(p.revision,0)+CASE WHEN `+changed+` THEN 1 ELSE 0 END AS revision,COALESCE(p.last_played_at,'') AS last_played_at FROM targets t CROSS JOIN actor LEFT JOIN personal_items p ON p.profile_id=actor.profile AND p.item_id=t.item_id)
 INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at)
 SELECT profile,item_id,watchlisted,favorite,not_interested,rating,revision,1,last_played_at FROM prior WHERE true
 ON CONFLICT(profile_id,item_id) DO UPDATE SET revision=excluded.revision,watched=excluded.watched`, args...)
	if err != nil {
		return out, err
	}
	// Set-based equivalent of personalstate.Write: one server-authored explicit
	// intent per actual target, without touching progress, conflicts or events.
	intentArgs := append(append([]any{}, args...), personalstate.Stamp())
	_, err = tx.ExecContext(ctx, cte+`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) SELECT actor.profile,t.item_id,1,? FROM targets t CROSS JOIN actor WHERE true ON CONFLICT(profile_id,item_id) DO UPDATE SET watched=excluded.watched,authored_at=excluded.authored_at`, intentArgs...)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, snapshot, args...); err != nil {
		return out, err
	}
	out.ServerID, out.LibraryID, out.ViewerFence = server, library, viewer.Fence
	out.OperationID, out.ExpectedRevision = in.OperationID, in.ExpectedRevision
	out.Revision, err = scoped.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	if !validReadingMarkAllRevision(out.Revision) {
		return out, ErrPersonalConflict
	}
	if protocol {
		if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: out.Revision.Viewer}, time.Now().UnixMilli()); err != nil {
			return out, err
		}
		return out, gated.Commit()
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = fenceOperation(tx, viewer.Profile, "reading-mark-all", in.OperationID, hash); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_batch_receipts(profile_id,operation_id,request_hash,response,created_at) VALUES(?,?,?,?,?)`, viewer.Profile, key, hash, string(encoded), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return out, err
	}
	return out, gated.Commit()
}
