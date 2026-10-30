package catalog

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// PlaylistOrder is a bounded edit snapshot. IDs identify occurrences only;
// hidden media remains opaque and duplicate media references stay distinct.
type PlaylistOrder struct {
	ServerID    string   `json:"serverId"`
	ViewerFence string   `json:"viewerFence"`
	PlaylistID  string   `json:"playlistId"`
	Revision    int64    `json:"revision"`
	EntryIDs    []string `json:"entryIds"`
	NextCursor  string   `json:"nextCursor,omitempty"`
}

func catalogPlaylistRole(q personalReader, id string, a ResourceActor) (string, int64, bool, error) {
	var owner ResourceActor
	var revision int64
	var deleted bool
	if err := q.QueryRow(`SELECT owner_authority,owner_account,owner_profile,revision,deleted FROM catalog_playlists WHERE token=?`, id).Scan(&owner.Authority, &owner.AccountID, &owner.ProfileID, &revision, &deleted); err != nil {
		return "", 0, false, err
	}
	if a == owner {
		return "owner", revision, deleted, nil
	}
	var role string
	if err := q.QueryRow(`SELECT role FROM playlist_shares WHERE playlist_id=? AND authority=? AND account_id=? AND profile_id=?`, id, a.Authority, a.AccountID, a.ProfileID).Scan(&role); err != nil {
		return "", 0, false, err
	}
	return role, revision, deleted, nil
}

func orderAccess(q personalReader, id string, a ResourceActor) (int64, error) {
	role, revision, deleted, e := catalogPlaylistRole(q, id, a)
	if e != nil {
		return 0, e
	}
	if deleted {
		return 0, sql.ErrNoRows
	}
	if role != "owner" && role != "editor" {
		return 0, identity.ErrUnauthorized
	}
	return revision, nil
}

type PlaylistOrderWindow struct {
	Cursor string
	Limit  int
}

func (s *Service) PlaylistOrder(server, fence, id string, a ResourceActor, authorize func(*sql.Tx) error, windows ...PlaylistOrderWindow) (PlaylistOrder, error) {
	out := PlaylistOrder{ServerID: server, ViewerFence: fence, PlaylistID: id, EntryIDs: []string{}}
	gated, e := dbwork.BeginSnapshot(s.Context(), s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	out.Revision, e = orderAccess(reads{ctx: s.Context(), to: tx}, id, a)
	if e != nil {
		return out, e
	}
	window := PlaylistOrderWindow{Limit: 100}
	if len(windows) > 0 {
		window = windows[0]
		if window.Limit == 0 {
			window.Limit = 100
		}
	}
	if window.Limit < 1 || window.Limit > 100 {
		return out, ErrCursor
	}
	scope := cursorScope{Profile: actorKey(a), View: "playlist-order", Entity: id, Viewer: fence, Sort: "order-key", Direction: "asc", Limit: window.Limit}
	revision := ContentRevision{Catalog: out.Revision}
	after := ""
	var afterID int64
	if window.Cursor != "" {
		c, err := s.decodeRevisionCursor(window.Cursor, scope, revision)
		if err != nil {
			return out, err
		}
		after = c.Value
		afterID, err = strconv.ParseInt(c.ID, 10, 64)
		if err != nil || afterID < 1 {
			return out, ErrCursor
		}
	}
	rows, e := tx.QueryContext(s.Context(), `SELECT e.token,e.id,e.order_key FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE p.token=? AND p.deleted=0 AND (e.order_key>? OR (e.order_key=? AND e.id>?)) ORDER BY e.order_key,e.id LIMIT ?`, id, after, after, afterID, window.Limit+1)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var occurrence, key string
		var entryID int64
		if e = rows.Scan(&occurrence, &entryID, &key); e != nil {
			rows.Close()
			return out, e
		}
		if len(out.EntryIDs) == window.Limit {
			out.NextCursor, e = s.encodeRevisionCursor(cursorValue{Scope: scope, Value: after, ID: strconv.FormatInt(afterID, 10), Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
			if e != nil {
				rows.Close()
				return out, e
			}
			break
		}
		after, afterID = key, entryID
		out.EntryIDs = append(out.EntryIDs, occurrence)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// CheckPlaylistOrder rechecks current edit access and fences a response after
// its transaction has released the snapshot. CAS still protects later writes.
func (s *Service) CheckPlaylistOrder(id string, a ResourceActor, revision int64) error {
	current, e := orderAccess(s.read(), id, a)
	if e != nil {
		return e
	}
	if current != revision {
		return ErrStaleContinuation
	}
	return nil
}
