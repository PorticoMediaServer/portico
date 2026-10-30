package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type HomeDestinationPickerRequest struct {
	Kind    string   `json:"kind"`
	Query   string   `json:"query"`
	Cursor  string   `json:"cursor,omitempty"`
	Limit   int      `json:"limit"`
	ItemIDs []string `json:"itemIds,omitempty"`
}
type HomeDestinationPickerRow struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Count     int            `json:"count"`
	Revision  int64          `json:"revision"`
	CanAdd    bool           `json:"canAdd"`
	Actions   []string       `json:"actions"`
	Artwork   []ContentEntry `json:"artwork"`
	AlreadyIn *int           `json:"alreadyIn,omitempty"`
}
type HomeDestinationPickerPage struct {
	ServerID    string                     `json:"serverId"`
	ViewerFence string                     `json:"viewerFence"`
	Kind        string                     `json:"kind"`
	Query       string                     `json:"query"`
	Revision    ContentRevision            `json:"revision"`
	Rows        []HomeDestinationPickerRow `json:"rows"`
	NextCursor  string                     `json:"nextCursor"`
}

// HomeDestinationPickerTx reads exactly one ACL-filtered keyset window. The
// canonical resource role functions still decide each returned destination.
func (s *Service) HomeDestinationPickerTx(ctx context.Context, tx *sql.Tx, server string, v Viewer, a ResourceActor, in HomeDestinationPickerRequest) (HomeDestinationPickerPage, error) {
	out := HomeDestinationPickerPage{ServerID: server, ViewerFence: v.Fence, Kind: in.Kind, Query: in.Query, Rows: []HomeDestinationPickerRow{}}
	if tx == nil {
		return out, jobInvalid("picker requires a catalogue snapshot")
	}
	// A malformed request is not a stale page: neither the destination window
	// nor the ids the caller chose are a cursor. Only a cursor that cannot be
	// decoded below is one, which keeps this read in step with the receipt read's
	// own request validation.
	if (in.Kind != "playlist" && in.Kind != "collection") || in.Limit < 1 || in.Limit > 40 || !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 100 || strings.IndexFunc(in.Query, unicode.IsControl) >= 0 || len(in.ItemIDs) > 200 {
		return out, jobInvalid("picker takes a playlist or collection kind, 1 to 40 rows, a name search of up to 100 characters, and at most 200 item ids")
	}
	seen := map[string]bool{}
	for _, id := range in.ItemIDs {
		if id == "" || len(id) > 256 || seen[id] {
			return out, jobInvalid("picker item ids must be distinct non-empty references")
		}
		seen[id] = true
	}
	clone := *s.WithContext(ctx)
	clone.readTx = tx
	s = &clone
	revision, err := directoryRevision(s.read(), a)
	if err != nil {
		return out, err
	}
	var saved int64
	if err = s.read().QueryRow(`SELECT revision FROM saved_resource_generation WHERE id=1`).Scan(&saved); err != nil {
		return out, err
	}
	content, err := s.homeRevision(v.Libraries, v.Profile)
	if err != nil {
		return out, err
	}
	revision.Catalog += content.Catalog
	revision.Viewer += content.Viewer + saved
	out.Revision = revision
	selected, _ := json.Marshal(in.ItemIDs)
	scope := cursorScope{Profile: actorKey(a), View: "home-destination-picker:" + in.Kind, Viewer: v.Fence, Search: in.Query, Category: fmt.Sprintf("%x", sha256.Sum256(selected)), Limit: in.Limit}
	after, key := "", ""
	if in.Cursor != "" {
		cursor, err := s.decodeRevisionCursor(in.Cursor, scope, revision)
		if err != nil {
			return out, err
		}
		after = cursor.ID
		key = cursor.Value
	}
	from := `catalog_playlists r`
	id := `r.token`
	predicate := `r.deleted=0 AND (r.owner_authority=? AND r.owner_account=? AND r.owner_profile=? OR EXISTS(SELECT 1 FROM playlist_shares share WHERE share.playlist_id=r.token AND share.authority=? AND share.account_id=? AND share.profile_id=?))`
	args := []any{a.Authority, a.AccountID, a.ProfileID, a.Authority, a.AccountID, a.ProfileID}
	if in.Kind == "collection" {
		from = `saved_resources r`
		id = `r.id`
		predicate = `r.deleted=0 AND r.kind='collection' AND (r.owner_key=? OR r.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares share WHERE share.resource_id=r.id AND share.authority=? AND share.account_id=? AND share.profile_id=?))`
		args = []any{actorKey(a), a.Authority, a.AccountID, a.ProfileID}
	}
	args = append(args, in.Query, key, key, after, in.Limit+1)
	found, err := s.read().Query(`SELECT `+id+`,lower(r.name) FROM `+from+` WHERE `+predicate+` AND instr(portico_search_fold(r.name),portico_search_fold(?))>0 AND (lower(r.name)>? OR (lower(r.name)=? AND `+id+`>?)) ORDER BY lower(r.name),`+id+` LIMIT ?`, args...)
	if err != nil {
		return out, err
	}
	ids, keys := []string{}, []string{}
	for found.Next() {
		var id, key string
		if err = found.Scan(&id, &key); err != nil {
			break
		}
		ids = append(ids, id)
		keys = append(keys, key)
	}
	if err == nil {
		err = found.Err()
	}
	found.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > in.Limit {
		ids = ids[:in.Limit]
		out.NextCursor, err = s.encodeRevisionCursor(cursorValue{Scope: scope, ID: ids[in.Limit-1], Value: keys[in.Limit-1], Expires: time.Now().Add(30 * time.Minute).Unix()}, revision)
		if err != nil {
			return out, err
		}
	}
	for _, id := range ids {
		row := HomeDestinationPickerRow{ID: id, Actions: []string{}, Artwork: []ContentEntry{}}
		var role string
		if in.Kind == "playlist" {
			var deleted bool
			role, row.Revision, deleted, err = catalogPlaylistRole(s.read(), id, a)
			if err != nil {
				return out, err
			}
			if deleted {
				return out, sql.ErrNoRows
			}
			resource, err := s.Playlist(server, v.Fence, id, a)
			if err != nil {
				return out, err
			}
			row.Name, row.Count, row.Actions = resource.Name, resource.EntryCount, resource.Actions
			for _, action := range resource.Actions {
				if action == "add" {
					row.CanAdd = true
				}
			}
		} else {
			var kind string
			var deleted bool
			role, kind, row.Revision, deleted, err = savedResourceRole(s.read(), id, a)
			if err != nil {
				return out, err
			}
			if deleted || kind != "collection" {
				return out, sql.ErrNoRows
			}
			resource, err := s.SavedResource(server, v.Fence, id, a)
			if err != nil {
				return out, err
			}
			row.Name, row.Count, row.Actions = resource.Name, resource.EntryCount, resource.Actions
			for _, action := range resource.Actions {
				if action == "entries" {
					row.CanAdd = true
				}
			}
		}
		if role == "" {
			return out, sql.ErrNoRows
		}
		visible, bound := v.itemVisibilitySQL("item.id")
		membership := `catalog_playlist_entries member JOIN catalog_playlists playlist ON playlist.id=member.playlist_id JOIN catalog_entities item ON item.id=member.item_id`
		resource := `playlist.token=? AND playlist.deleted=0`
		order := `member.order_key,member.id`
		if in.Kind == "collection" {
			membership = `saved_resource_entries member JOIN catalog_entities item ON item.id=member.item_id`
			resource = `member.resource_id=?`
			order = `member.order_key,member.id`
		}
		artArgs := append([]any{id}, bound...)
		artwork, err := s.read().Query(`SELECT pid(item.public_id) FROM `+membership+` WHERE `+resource+` AND `+visible+recordingsClause("item.id", v.EffectiveRestrictions())+` ORDER BY `+order+` LIMIT 4`, artArgs...)
		if err != nil {
			return out, err
		}
		media := []string{}
		for artwork.Next() {
			var item string
			if err = artwork.Scan(&item); err != nil {
				break
			}
			media = append(media, item)
		}
		if err == nil {
			err = artwork.Err()
		}
		artwork.Close()
		if err != nil {
			return out, err
		}
		reading, err := s.homeReadingPage(v, media, nil)
		if err != nil {
			return out, err
		}
		ordinary := make([]string, 0, len(media))
		for _, id := range media {
			if _, ok := reading[id]; !ok {
				ordinary = append(ordinary, id)
			}
		}
		projected, err := s.homeEntries(v.Profile, ordinary)
		if err != nil {
			return out, err
		}
		ordinaryByID := make(map[string]ContentEntry, len(projected))
		for _, entry := range projected {
			ordinaryByID[entry.ID] = entry
		}
		for _, id := range media {
			if entry, ok := reading[id]; ok {
				row.Artwork = append(row.Artwork, entry)
			} else if entry, ok := ordinaryByID[id]; ok {
				row.Artwork = append(row.Artwork, entry)
			} else {
				return out, ErrStaleContinuation
			}
		}
		if len(in.ItemIDs) > 0 {
			count := 0
			hintArgs := append(append([]any{id}, bound...), string(selected))
			err = s.read().QueryRow(`SELECT count(DISTINCT item.id) FROM `+membership+` WHERE `+resource+` AND `+visible+recordingsClause("item.id", v.EffectiveRestrictions())+` AND item.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, hintArgs...).Scan(&count)
			if err != nil {
				return out, err
			}
			row.AlreadyIn = &count
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}
