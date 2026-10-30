package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrPlaylistConflict = errors.New("playlist changed; refresh before applying a new intent")
var ErrPlaylistCapacity = errors.New("playlist resource limit reached")

// MaxPlaylistEntries bounds a single synchronous write, not stored playlist size.
const MaxPlaylistEntries = 200
const MaxPlaylistShares = 100

func playlistRole(q personalReader, id string, a ResourceActor) (string, int64, bool, error) {
	var owner ResourceActor
	var rev int64
	var deleted bool
	e := q.QueryRow(`SELECT owner_authority,owner_account,owner_profile,revision,deleted FROM catalog_playlists WHERE token=?`, id).Scan(&owner.Authority, &owner.AccountID, &owner.ProfileID, &rev, &deleted)
	if e != nil {
		return "", 0, false, e
	}
	if a == owner {
		return "owner", rev, deleted, nil
	}
	var role string
	e = q.QueryRow(`SELECT role FROM playlist_shares WHERE playlist_id=? AND authority=? AND account_id=? AND profile_id=?`, id, a.Authority, a.AccountID, a.ProfileID).Scan(&role)
	if e != nil {
		return "", 0, false, sql.ErrNoRows
	}
	return role, rev, deleted, nil
}

// playlistItemAllowed accepts playable media leaves (not extras).
func playlistItemAllowed(q personalReader, item string) error {
	var ok bool
	if e := q.QueryRow(`SELECT k.playable=1 AND e.kind<>11 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?)`, item).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return requestinput.New("only playable media leaves can be added")
	}
	return nil
}

func resourceName(name string) bool {
	return strings.TrimSpace(name) == name && name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 200 && !strings.ContainsAny(name, "\x00\r\n")
}
func profileLabel(id string) string {
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return "Profile …" + id
}
func (s *Service) Playlist(server, fence, id string, a ResourceActor) (Playlist, error) {
	out := Playlist{ServerID: server, ViewerFence: fence, ID: id, Actions: []string{}, Limits: PlaylistLimits{0, MaxPlaylistShares, MaxPlaylistEntries}}
	role, rev, deleted, e := playlistRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	out.Role = role
	if e = s.read().QueryRow(`SELECT COALESCE((SELECT pinned FROM saved_pins WHERE owner_key=? AND kind='playlist' AND resource_id=?),0),COALESCE((SELECT revision FROM saved_pins WHERE owner_key=? AND kind='playlist' AND resource_id=?),0)`, actorKey(a), id, actorKey(a), id).Scan(&out.Pinned, &out.PinRevision); e != nil {
		return out, e
	}
	out.Revision = rev
	e = s.read().QueryRow(`SELECT p.name,p.summary,p.entry_count FROM catalog_playlists p WHERE p.token=? AND `+playlistHeadFence, id).Scan(&out.Name, &out.Summary, &out.EntryCount)
	if e != nil {
		return out, e
	}
	if role == "owner" {
		out.Actions = []string{"rename", "summary", "delete", "share", "add", "remove", "reorder"}
		out.Shares = []PlaylistShare{}
		rows, err := s.read().Query(`SELECT authority,account_id,profile_id,role FROM playlist_shares WHERE playlist_id=? ORDER BY authority,account_id,profile_id`, id)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var v PlaylistShare
			if err = rows.Scan(&v.Authority, &v.AccountID, &v.ProfileID, &v.Role); err != nil {
				rows.Close()
				return out, err
			}
			v.DisplayName = profileLabel(v.ProfileID)
			out.Shares = append(out.Shares, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	} else if role == "editor" {
		out.Actions = []string{"add", "remove", "reorder"}
	}
	out.Actions = append(out.Actions, "pin")
	_, after, dead, e := playlistRole(s.read(), id, a)
	if e != nil {
		return out, e
	}
	if dead || after != rev {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// MutatePlaylist checks current principal authorization even on receipt replay.
// authorize also verifies media access for add and target identity for share inside the transaction.
func (s *Service) MutatePlaylist(a ResourceActor, id, action, target string, m PlaylistMutation, authorize func(*sql.Tx) error) (PlaylistReceipt, error) {
	return s.mutatePlaylistCaptured(a, id, action, target, m, authorize, nil)
}

// capture is internal domain composition. It runs in the existing writer,
// after receipt replay, with the original request hash already fixed. Ordinary
// public playlist mutations keep their existing hash and behavior unchanged.
func (s *Service) mutatePlaylistCaptured(a ResourceActor, id, action, target string, m PlaylistMutation, authorize func(*sql.Tx) error, capture func(*sql.Tx) ([]string, error)) (PlaylistReceipt, error) {
	if err := requireCatalogMutationContext(s.Context(), m.OperationID); err != nil {
		return PlaylistReceipt{}, err
	}
	out := PlaylistReceipt{OperationID: m.OperationID, PlaylistID: id}
	if a.Authority == "" || a.AccountID == "" || a.ProfileID == "" || !personalOperation.MatchString(m.OperationID) || m.ExpectedRevision < 0 {
		return out, requestinput.New("invalid playlist operation")
	}
	if m.Name != nil && !resourceName(*m.Name) {
		return out, requestinput.New("playlist name must contain 1–200 characters")
	}
	if m.Summary != nil && (!utf8.ValidString(*m.Summary) || utf8.RuneCountInString(*m.Summary) > 4000 || strings.ContainsRune(*m.Summary, 0)) {
		return out, requestinput.New("playlist summary exceeds 4000 characters")
	}
	if len(m.EntryIDs) > MaxPlaylistEntries || len(m.ItemIDs) > MaxPlaylistEntries {
		return out, ErrPlaylistCapacity
	}
	if m.UndoToken != "" && (action != "add" || m.ItemID != "" || !personalOperation.MatchString(m.UndoToken)) || action == "add" && (m.ItemID == "") == (m.UndoToken == "") {
		return out, requestinput.New("add an item, or undo a removal with its token")
	}
	if m.ItemIDs != nil && action != "create" {
		return out, requestinput.New("initial itemIds only apply to creation")
	}
	if (m.Name != nil || m.Summary != nil) && action != "create" && action != "update" || m.ItemID != "" && action != "add" || m.EntryIDs != nil && action != "reorder" || (m.Authority != "" || m.AccountID != "" || m.ProfileID != "") && action != "share" && action != "unshare" || m.Role != "" && action != "share" || m.AfterEntryID != nil && action != "reorder" {
		return out, requestinput.New("fields do not apply to this playlist operation")
	}
	if (action == "share" || action == "unshare") && (m.Authority == "" || m.AccountID == "" || m.ProfileID == "") {
		return out, requestinput.New("complete share target is required")
	}
	if (action == "share" || action == "unshare") && m.Authority != "local" {
		return out, requestinput.New("invalid share authority")
	}
	raw, _ := json.Marshal(struct {
		ID, Action, Target string
		Mutation           PlaylistMutation
	}{id, action, target, m})
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	gated, e := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	_, _, protocol := usermutation.RequestFrom(s.Context())
	prior, e := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if e != nil {
		return out, e
	}
	if prior != nil {
		out.PlaylistID, out.Revision = prior.TargetID, prior.Revision
		role, _, deleted, err := playlistRole(tx, out.PlaylistID, a)
		if err != nil {
			var exists bool
			if e = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_playlists WHERE token=?)`, out.PlaylistID).Scan(&exists); e != nil {
				return out, e
			}
			if exists || !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			deleted = true
		} else if deleted && role != "owner" {
			return out, sql.ErrNoRows
		} else if role != "owner" && (action != "add" && action != "remove" && action != "reorder" || role != "editor") {
			return out, identity.ErrUnauthorized
		}
		out.Deleted = deleted || prior.Status == "deleted"
		return out, nil
	}
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	role := "owner"
	rev := int64(0)
	deleted := false
	if action != "create" {
		role, rev, deleted, e = playlistRole(tx, id, a)
		if e != nil {
			return out, e
		}
		if role != "owner" && (action != "add" && action != "remove" && action != "reorder" || role != "editor") {
			return out, identity.ErrUnauthorized
		}
	}
	if !protocol {
		cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
		if _, e = tx.Exec(`DELETE FROM playlist_receipts WHERE rowid IN(SELECT rowid FROM playlist_receipts WHERE created_at<? LIMIT 1000)`, cutoff); e != nil {
			return out, e
		}
		var priorHash, receipt string
		e = tx.QueryRow(`SELECT request_hash,response FROM playlist_receipts WHERE authority=? AND account_id=? AND profile_id=? AND operation_id=? AND created_at>=?`, a.Authority, a.AccountID, a.ProfileID, m.OperationID, cutoff).Scan(&priorHash, &receipt)
		if e == nil {
			if hash != priorHash {
				return out, ErrOperationConflict
			}
			if e = json.Unmarshal([]byte(receipt), &out); e != nil {
				return out, e
			}
			_, _, out.Deleted, e = playlistRole(tx, out.PlaylistID, a)
			if e != nil {
				return out, e
			}
			return out, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if action == "create" {
			var originalHash string
			e = tx.QueryRow(`SELECT token,creation_hash,revision,deleted FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=? AND creation_operation=?`, a.Authority, a.AccountID, a.ProfileID, m.OperationID).Scan(&out.PlaylistID, &originalHash, &out.Revision, &out.Deleted)
			if e == nil {
				if originalHash != hash {
					return out, ErrOperationConflict
				}
				return out, nil
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
		}
		if e = checkRetired(tx, actorKey(a), "playlist", m.OperationID, hash); e != nil {
			return out, e
		}
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	if action != "create" && rev != m.ExpectedRevision {
		return out, ErrPlaylistConflict
	}
	// Idempotency receipts expire after 30 days; one profile's are bounded (an
	// abuse bound, not a list limit). There is no server-wide bound: one busy
	// profile must not stop everyone else's playlists.
	if !protocol {
		var receiptCount int
		if e = tx.QueryRow(`SELECT count(*) FROM playlist_receipts WHERE authority=? AND account_id=? AND profile_id=?`, a.Authority, a.AccountID, a.ProfileID).Scan(&receiptCount); e != nil {
			return out, e
		}
		if receiptCount >= 10000 {
			return out, ErrPlaylistCapacity
		}
	}
	touchedEntries := []string{}
	switch action {
	case "create":
		if m.Name == nil || m.ExpectedRevision != 0 {
			return out, requestinput.New("name is required for creation")
		}
		if capture != nil {
			m.ItemIDs, e = capture(tx)
			if e != nil {
				return out, e
			}
			if len(m.ItemIDs) == 0 || len(m.ItemIDs) > MaxPlaylistEntries {
				return out, ErrPlaylistCapacity
			}
		}
		var n int
		if e = tx.QueryRow(`SELECT count(*) FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=?`, a.Authority, a.AccountID, a.ProfileID).Scan(&n); e != nil {
			return out, e
		}
		if n >= 10000 {
			return out, ErrPlaylistCapacity
		}
		id = identity.Token()
		out.PlaylistID = id
		summary := ""
		if m.Summary != nil {
			summary = *m.Summary
		}
		_, e = tx.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,summary,creation_operation,creation_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, a.Authority, a.AccountID, a.ProfileID, *m.Name, summary, m.OperationID, hash, time.Now().UTC().Format(time.RFC3339))

		if e == nil {
			for position, item := range m.ItemIDs {
				if e = playlistItemAllowed(tx, item); e != nil {
					return out, e
				}
				entryID := identity.Token()
				// The same order keys an append writes (playlistEndKey): a later
				// insert "after" an entry needs keys to place itself between.
				if _, e = tx.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,p.id,i.id,?,? FROM catalog_playlists p,catalog_entities i WHERE p.token=? AND i.public_id=pid_blob(?)`, entryID, position+1, fmt.Sprintf("%020dV", position+1), id, item); e != nil {
					return out, e
				}
				touchedEntries = append(touchedEntries, entryID)
			}
		}
		rev = 1
	case "update":
		if m.Name == nil && m.Summary == nil {
			return out, requestinput.New("set name or summary")
		}
		_, e = tx.Exec(`UPDATE catalog_playlists SET name=COALESCE(?,name),summary=COALESCE(?,summary) WHERE token=?`, m.Name, m.Summary, id)
	case "delete":
		_, e = tx.Exec(`UPDATE catalog_playlists SET deleted=1 WHERE token=?`, id)
		out.Deleted = true
		if e == nil {
			_, e = tx.Exec(`DELETE FROM playlist_shares WHERE playlist_id=?`, id)
		}
	case "add":
		if m.UndoToken != "" {
			// The same occurrence returns: its entry id, its item and its order
			// key, so it sits where it was among the entries still there.
			var removed []removedEntry
			if removed, e = takeEntryUndo(tx, actorKey(a), "playlist", id, m.UndoToken); e != nil {
				return out, e
			}
			for _, r := range removed {
				if _, e = tx.Exec(`INSERT INTO catalog_playlist_entries(token,playlist_id,item_id,position,order_key) SELECT ?,p.id,?,(SELECT COALESCE(max(position),0)+1 FROM catalog_playlist_entries WHERE playlist_id=p.id),? FROM catalog_playlists p WHERE p.token=?`, r.entry, r.item, r.orderKey, id); e != nil {
					return out, e
				}
				out.EntryID = r.entry
				touchedEntries = append(touchedEntries, r.entry)
			}
			break
		}
		if e = playlistItemAllowed(tx, m.ItemID); e != nil {
			return out, e
		}
		out.EntryID = identity.Token()
		e = appendPlaylistEntry(tx, id, out.EntryID, m.ItemID)
		touchedEntries = append(touchedEntries, out.EntryID)
	case "remove":
		var r removedEntry
		if e = tx.QueryRow(`SELECT e.token,e.item_id,e.order_key FROM catalog_playlist_entries e JOIN catalog_playlists p ON p.id=e.playlist_id WHERE e.token=? AND p.token=?`, target, id).Scan(&r.entry, &r.item, &r.orderKey); e != nil {
			return out, e
		}
		var result sql.Result
		result, e = tx.Exec(`DELETE FROM catalog_playlist_entries WHERE token=? AND playlist_id=(SELECT id FROM catalog_playlists WHERE token=?)`, target, id)
		if e == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				return out, sql.ErrNoRows
			}
			touchedEntries = append(touchedEntries, target)
			out.UndoToken, out.UndoUntil, e = captureEntryUndo(tx, actorKey(a), "playlist", id, []removedEntry{r})
		}
	case "reorder":
		e = reorderPlaylistWindow(tx, id, m.EntryIDs, m.AfterEntryID)
		touchedEntries = append(touchedEntries, m.EntryIDs...)
	case "share":
		if m.Role != "viewer" && m.Role != "editor" {
			return out, requestinput.New("share role must be viewer or editor")
		}
		if (ResourceActor{m.Authority, m.AccountID, m.ProfileID}) == a {
			return out, requestinput.New("owner already has access")
		}
		var n, has int
		if e = tx.QueryRow(`SELECT count(*),COALESCE(sum(authority=? AND account_id=? AND profile_id=?),0) FROM playlist_shares WHERE playlist_id=?`, m.Authority, m.AccountID, m.ProfileID, id).Scan(&n, &has); e != nil {
			return out, e
		}
		if has == 0 {
			var visible int
			if e = tx.QueryRow(`SELECT count(*) FROM catalog_playlists WHERE deleted=0 AND token IN(SELECT token FROM catalog_playlists WHERE owner_authority=? AND owner_account=? AND owner_profile=? UNION SELECT playlist_id FROM playlist_shares WHERE authority=? AND account_id=? AND profile_id=?)`, m.Authority, m.AccountID, m.ProfileID, m.Authority, m.AccountID, m.ProfileID).Scan(&visible); e != nil {
				return out, e
			}
			if visible >= 10000 {
				return out, ErrPlaylistCapacity
			}
		}
		if has == 0 && n >= MaxPlaylistShares {
			return out, ErrPlaylistCapacity
		}
		_, e = tx.Exec(`INSERT INTO playlist_shares(playlist_id,authority,account_id,profile_id,role) VALUES(?,?,?,?,?) ON CONFLICT(playlist_id,authority,account_id,profile_id) DO UPDATE SET role=excluded.role`, id, m.Authority, m.AccountID, m.ProfileID, m.Role)
	case "unshare":
		_, e = tx.Exec(`DELETE FROM playlist_shares WHERE playlist_id=? AND authority=? AND account_id=? AND profile_id=?`, id, m.Authority, m.AccountID, m.ProfileID)
	default:
		return out, requestinput.New("unsupported playlist operation")
	}
	if e != nil {
		return out, e
	}
	if action != "create" {
		rev++
		// The playlist's own last change follows a real edit of it — a new name or summary,
		// an added, removed or reordered item — not a share change or a delete. The revision
		// and receipt stay the fence; updated_at only records when that fence moved.
		edited := action == "update" || action == "add" || action == "remove" || action == "reorder"
		if edited {
			_, e = tx.Exec(`UPDATE catalog_playlists SET revision=?,updated_at=? WHERE token=?`, rev, time.Now().UTC().Format(time.RFC3339), id)
		} else {
			_, e = tx.Exec(`UPDATE catalog_playlists SET revision=? WHERE token=?`, rev, id)
		}
		if e != nil {
			return out, e
		}
	}
	_ = touchedEntries
	out.Revision = rev
	if protocol {
		status := "applied"
		if out.Deleted {
			status = "deleted"
		}
		if e = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: status, TargetID: out.PlaylistID, Revision: out.Revision}, time.Now().UnixMilli()); e != nil {
			return out, e
		}
		return out, gated.Commit()
	}
	if e = fenceOperation(tx, actorKey(a), "playlist", m.OperationID, hash); e != nil {
		return out, e
	}
	raw, _ = json.Marshal(out)
	_, e = tx.Exec(`INSERT INTO playlist_receipts(authority,account_id,profile_id,operation_id,request_hash,response,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,operation_id) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,created_at=excluded.created_at`, a.Authority, a.AccountID, a.ProfileID, m.OperationID, hash, string(raw), time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// PlaylistReceiptStatus applies current sharing/deletion access after a commit;
// only the owner retains access to a deleted resource's recovery tombstone.
func (s *Service) PlaylistReceiptStatus(id string, a ResourceActor) (bool, error) {
	role, _, deleted, e := playlistRole(s.read(), id, a)
	if e != nil {
		return false, e
	}
	if deleted && role != "owner" {
		return false, sql.ErrNoRows
	}
	return deleted, nil
}
