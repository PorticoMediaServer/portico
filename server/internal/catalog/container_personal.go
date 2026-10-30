package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// ContainerPersonalMutation sets exactly one of watched, watchlisted or favorite.
// expectedRevision fences the watched default only; the two saved flags are plain
// values, so the last write wins.
type ContainerPersonalMutation struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	Watched          *bool `json:"watched,omitempty"`
	Watchlisted      *bool `json:"watchlisted,omitempty"`
	Favorite         *bool `json:"favorite,omitempty"`
}
type ContainerPersonal struct {
	Revision    int64  `json:"revision"`
	Watched     bool   `json:"watched"`
	Watermark   string `json:"watermark"`
	Watchlisted bool   `json:"watchlisted"`
	Favorite    bool   `json:"favorite"`
	UndoToken   string `json:"undoToken,omitempty"`
	UndoUntil   string `json:"undoUntil,omitempty"`
}
type ContainerCounts struct {
	WatchedCount   int64 `json:"watchedCount"`
	UnwatchedCount int64 `json:"unwatchedCount"`
}

// containerMembers returns the member entity ids of a container. The bound
// value is the container's integer entity id; callers resolve the public id
// once (entityid.Resolve) and bind the int64.
func containerMembers(kind string) (string, error) {
	switch kind {
	case "show":
		return `SELECT entity_id FROM catalog_episodes WHERE show_id=?`, nil
	case "season":
		return `SELECT entity_id FROM catalog_episodes WHERE season_id=?`, nil
	case "album":
		return `SELECT entity_id FROM catalog_songs WHERE album_id=?`, nil
	case "book":
		return `SELECT entity_id FROM catalog_book_files WHERE book_id=?`, nil
	}
	return "", jobInvalid("personal state supports show, season, album and book containers")
}

// containerSaves says which title-level saved flags a kind carries: a show or a book can be on
// My List (stored as `watchlisted`); a show, album, book or artist can be a Favorite. A season
// carries neither.
func containerSaves(kind, flag string) bool {
	switch flag {
	case "watchlisted":
		// My List: a show to watch, a book to listen to (Justin, 2 Oct 2026).
		return kind == "show" || kind == "book"
	case "favorite":
		return kind == "show" || kind == "album" || kind == "book" || kind == "artist"
	}
	return false
}

// readContainerSaved reads a container's own saved flags. They live in the personal_items row
// of the container entity, where Not interested already sits for these kinds, so Saved lists,
// Home rows and the recommender (a work's signal includes its own row) read one place.
func readContainerSaved(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, profile string, containerID int64) (watchlisted, favorite bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT watchlisted,favorite FROM personal_items WHERE profile_id=? AND item_id=?`, profile, containerID).Scan(&watchlisted, &favorite)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

// SetContainerSaved writes a title's Watchlist or Favorite flag (see containerSaves).
func (s *Service) SetContainerSaved(ctx context.Context, p identity.Principal, v Viewer, kind, id string, m ContainerPersonalMutation) (out ContainerPersonal, err error) {
	flag, value := "", false
	switch {
	case m.Watchlisted != nil && m.Favorite == nil && m.Watched == nil:
		flag, value = "watchlisted", *m.Watchlisted
	case m.Favorite != nil && m.Watchlisted == nil && m.Watched == nil:
		flag, value = "favorite", *m.Favorite
	default:
		return out, jobInvalid("set exactly one of watched, watchlisted or favorite")
	}
	if !containerSaves(kind, flag) {
		return out, jobInvalid("this kind does not carry that flag")
	}
	_, _, protocol := usermutation.RequestFrom(ctx)
	if !protocol {
		if err = s.WithContext(ctx).VisibleEntity(ctx, v, kind, id); err != nil {
			return out, err
		}
	}
	profile := identity.PersonalKey(p.Viewer)
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
		prior, e := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
		if e != nil {
			return e
		}
		if prior != nil {
			if _, e := personalID(tx, id); errors.Is(e, sql.ErrNoRows) {
				out = ContainerPersonal{Revision: prior.Revision}
				return nil
			} else if e != nil {
				return e
			}
			if e := s.WithContext(ctx).VisibleEntity(ctx, v, kind, id); e != nil {
				return e
			}
			out, e = readContainerMutation(ctx, tx, profile, kind, id, prior.Revision)
			return e
		}
		if e := s.WithContext(ctx).VisibleEntity(ctx, v, kind, id); e != nil {
			return e
		}

		containerID, e := entityid.Resolve(ctx, tx, id)
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		if e != nil {
			return e
		}
		// The row's triggers bump the viewer revision and queue the recommender, as an item's do.
		if _, e = tx.ExecContext(ctx, `INSERT INTO personal_items(profile_id,item_id,`+flag+`,revision) VALUES(?,?,?,1) ON CONFLICT(profile_id,item_id) DO UPDATE SET `+flag+`=excluded.`+flag+`,revision=personal_items.revision+1 WHERE personal_items.`+flag+` IS NOT excluded.`+flag, profile, containerID, value); e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, `SELECT revision,watched,watermark FROM container_personal_state WHERE profile_id=? AND kind=? AND container_id=?`, profile, kind, containerID).Scan(&out.Revision, &out.Watched, &out.Watermark)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out.Watchlisted, out.Favorite, e = readContainerSaved(ctx, tx, profile, containerID)
		if e != nil {
			return e
		}
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: id, Revision: out.Revision}, time.Now().UnixMilli())
	})
	return
}

func (s *Service) SetContainerPersonal(ctx context.Context, p identity.Principal, kind, id string, m ContainerPersonalMutation, _ *operations.Scheduler, access BulkAccess) (out ContainerPersonal, err error) {
	if m.Watched == nil || m.Watchlisted != nil || m.Favorite != nil || m.ExpectedRevision < 0 || m.ExpectedRevision > 9007199254740991 {
		return out, jobInvalid("invalid container mutation")
	}
	members, e := containerMembers(kind)
	if e != nil {
		return out, e
	}
	profile := identity.PersonalKey(p.Viewer)
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassInteractive, func(ctx context.Context, tx *sql.Tx) error {
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
		containerID, e := entityid.Resolve(ctx, tx, id)
		if errors.Is(e, entityid.ErrNotFound) {
			return sql.ErrNoRows
		}
		if e != nil {
			return e
		}
		var present int
		if e = tx.QueryRowContext(ctx, `SELECT 1 FROM (`+members+`) member JOIN catalog_entities i ON i.id=member.entity_id WHERE `+visible+` LIMIT 1`, append([]any{containerID}, bound...)...).Scan(&present); e != nil {
			return e
		}
		var previous ContainerPersonal
		var clear string
		e = tx.QueryRowContext(ctx, `SELECT revision,watched,watermark,cleared_through FROM container_personal_state WHERE profile_id=? AND kind=? AND container_id=?`, profile, kind, containerID).Scan(&previous.Revision, &previous.Watched, &previous.Watermark, &clear)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if previous.Revision != m.ExpectedRevision {
			return ErrPersonalConflict
		}
		ids, e := containerMemberIDs(ctx, tx, members, containerID)
		if e != nil {
			return e
		}
		out = ContainerPersonal{Revision: previous.Revision + 1, Watched: *m.Watched, Watermark: personalstate.Stamp(), UndoToken: identity.Token()}
		if e = captureContainerUndo(ctx, tx, profile, kind, containerID, previous, clear, &out, ids); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision,cleared_through) VALUES(?,?,?,?,?,?,?) ON CONFLICT(profile_id,kind,container_id) DO UPDATE SET watched=excluded.watched,watermark=excluded.watermark,revision=excluded.revision,cleared_through=excluded.cleared_through`, profile, kind, containerID, out.Watched, out.Watermark, out.Revision, out.Watermark); e != nil {
			return e
		}
		intentTime, e := time.Parse(time.RFC3339Nano, out.Watermark)
		if e != nil {
			return e
		}
		intentTime = intentTime.Add(time.Nanosecond)
		states, e := readPersonalBatch(ctx, tx, profile, ids)
		if e != nil {
			return e
		}
		if e = writeContainerMemberStates(ctx, tx, profile, ids, states, out.Watched, intentTime); e != nil {
			return e
		}

		if e = snapshotPersonalBatch(ctx, tx, profile, ids, states); e != nil {
			return e
		}
		if e = invalidateContainer(ctx, tx, profile, containerID); e != nil {
			return e
		}
		out.Watchlisted, out.Favorite, e = readContainerSaved(ctx, tx, profile, containerID)
		if e != nil {
			return e
		}
		until := time.Now().UTC().Add(containerUndoLifetime)
		out.UndoUntil = until.Format("2006-01-02T15:04:05.000Z")
		_, e = tx.ExecContext(ctx, `UPDATE container_personal_undo SET expires_ms=? WHERE token=?`, until.UnixMilli(), out.UndoToken)
		if e != nil {
			return e
		}
		return usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: id, Revision: out.Revision}, time.Now().UnixMilli())
	})
	return
}
func (s *Service) ContainerPersonalCounts(v Viewer, kind, id string) (out ContainerCounts, err error) {
	if dbwork.Snapshot(s.Context()) == nil {
		err = dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			out, readErr = s.WithContext(ctx).ContainerPersonalCounts(v, kind, id)
			return readErr
		})
		return
	}
	members, e := compactContainerMembers(kind)
	if e != nil {
		return out, e
	}
	visible, bound := v.itemVisibilitySQL("i.id")
	args := append([]any{v.Profile, id}, bound...)
	err = s.read().QueryRow(`SELECT COALESCE(sum(watched),0),count(*)-COALESCE(sum(watched),0) FROM (SELECT `+personalstate.CompactSQL("?", "i.id")+` AS watched FROM catalog_entities i WHERE i.id IN(`+members+`) AND `+visible+recordingsClause("i.id", v.EffectiveRestrictions())+`)`, args...).Scan(&out.WatchedCount, &out.UnwatchedCount)
	return
}

type ContainerPersonalView struct {
	// LibraryID is where the title lives, so a link that carries only its id can open its page.
	LibraryID      string `json:"libraryId"`
	Revision       int64  `json:"revision"`
	Watched        bool   `json:"watched"`
	Watermark      string `json:"watermark"`
	Watchlisted    bool   `json:"watchlisted"`
	Favorite       bool   `json:"favorite"`
	WatchedCount   int64  `json:"watchedCount"`
	UnwatchedCount int64  `json:"unwatchedCount"`
}

func (s *Service) ReadContainerPersonal(v Viewer, kind, id string) (out ContainerPersonalView, err error) {
	// An artist has no watched default and no members to count: it carries Favorite only.
	if _, err = containerMembers(kind); err != nil && kind != "artist" {
		return out, err
	}
	if err = s.VisibleEntity(s.Context(), v, kind, id); err != nil {
		return out, err
	}
	if err = s.read().QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, id).Scan(&out.LibraryID); err != nil {
		return out, err
	}
	err = s.read().QueryRow(`SELECT watchlisted,favorite FROM personal_items WHERE profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, v.Profile, id).Scan(&out.Watchlisted, &out.Favorite)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if kind == "artist" {
		return out, nil
	}
	err = s.read().QueryRow(`SELECT revision,watched,watermark FROM container_personal_state WHERE profile_id=? AND kind=? AND container_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, v.Profile, kind, id).Scan(&out.Revision, &out.Watched, &out.Watermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	counts, err := s.ContainerPersonalCounts(v, kind, id)
	out.WatchedCount, out.UnwatchedCount = counts.WatchedCount, counts.UnwatchedCount
	return out, err
}

// compactContainerMembers is the read path. The bound value is the
// container's public id; members come back as integer entity ids.
func compactContainerMembers(kind string) (string, error) {
	table, column := "", ""
	switch kind {
	case "show":
		table, column = "catalog_episodes", "show_id"
	case "season":
		table, column = "catalog_episodes", "season_id"
	case "album":
		table, column = "catalog_songs", "album_id"
	case "book":
		table, column = "catalog_book_files", "book_id"
	default:
		return "", jobInvalid("personal state supports show, season, album and book containers")
	}
	return `SELECT m.entity_id item_id FROM ` + table + ` m WHERE m.` + column + `=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, nil
}
