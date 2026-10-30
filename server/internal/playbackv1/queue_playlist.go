package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// SaveAsPlaylistRequest is POST /v1/queues/{id}:save-as-playlist (spec §8).
type SaveAsPlaylistRequest struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
}

// SavedPlaylist is the playlist a queue was saved as. A queue that fits one
// playlist write is saved at once ("saved"). A longer one is copied in the
// background ("saving"): entries is how many are in the playlist so far and
// total how many the queue had; replaying the same Idempotency-Key reports
// progress until "saved", or "failed" with errorCode (queue_changed,
// playlist_deleted, authority_revoked, interrupted) and the entries copied
// so far kept.
type SavedPlaylist struct {
	PlaylistID string `json:"playlistId"`
	Revision   string `json:"revision"`
	Entries    int    `json:"entries"`
	State      string `json:"state"`
	Total      int64  `json:"total,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
}

// SaveQueueAsPlaylist copies the queue, in its play order, into a new playlist
// of the caller's (spec §8; INT P21), through the catalog's playlist create
// (review P32). A queue that fits one playlist write (catalog.MaxPlaylistEntries)
// is saved in the request; a longer one, of any size, is a durable background
// copy (NEW-37, queue_save.go) that the key's replay reports on.
// An entry the caller can no longer play is left out. The Idempotency-Key is
// the playlist's creation operation, so a retry answers with the same
// playlist; If-Match, when sent, saves only that revision of the queue.
func (s *Service) SaveQueueAsPlaylist(ctx context.Context, c Caller, id, ifMatch, key string, req SaveAsPlaylistRequest) (SavedPlaylist, error) {
	if key == "" {
		return SavedPlaylist{}, ErrKeyRequired
	}
	if !keyPattern.MatchString(key) {
		return SavedPlaylist{}, &FieldError{Path: "Idempotency-Key"}
	}
	if req.Name == "" || strings.TrimSpace(req.Name) != req.Name || !utf8.ValidString(req.Name) || utf8.RuneCountInString(req.Name) > 200 || strings.ContainsAny(req.Name, "\x00\r\n") {
		return SavedPlaylist{}, &FieldError{Path: "name"}
	}
	if !utf8.ValidString(req.Summary) || utf8.RuneCountInString(req.Summary) > 4000 || strings.ContainsRune(req.Summary, 0) {
		return SavedPlaylist{}, &FieldError{Path: "summary"}
	}
	q, err := s.queueFor(ctx, c, id, ifMatch, ifMatch != "")
	if err != nil {
		return SavedPlaylist{}, err
	}
	p := c.Principal
	if out, ok, err := s.savedPlaylist(ctx, p, key, req); ok || err != nil {
		return out, err
	}
	// The queue's entries in play order, through the caller's fence, a window
	// at a time. An entry the caller can no longer play is left out: it is
	// absent for them (SEC-02), and a playlist entry would say it exists.
	items := []string{}
	large, order, size := false, "", int64(0)
	err = s.withMusicLayout(ctx, c, q.id, func(l *layout) ([]int64, error) {
		if ifMatch != "" && l.q.revision != q.revision {
			return nil, ErrRevision
		}
		if l.total() > catalog.MaxPlaylistEntries {
			return nil, nil
		}
		return l.sonicWindowsForRange(0, l.total())
	}, func(l *layout) error {
		if l.waiting() {
			return ErrQueueBuilding
		}
		total := l.total()
		if total > catalog.MaxPlaylistEntries {
			// Copied in the background from a settled order: a segment
			// still building would still be growing.
			if len(l.building) > 0 {
				return ErrQueueBuilding
			}
			large, order, size = true, l.orderDigest(), total
			return nil
		}
		for from := int64(0); from < total; from += WindowMax {
			entries, err := s.window(ctx, l, p, from, WindowMax)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if e.Kind == "pending" {
					return ErrQueueBuilding
				}
				if e.Available && e.ItemID != "" {
					items = append(items, e.ItemID)
				}
			}
		}
		return nil
	})
	if err != nil {
		return SavedPlaylist{}, err
	}
	if large {
		return s.startLargeSave(ctx, p, q.id, order, size, key, req)
	}
	if s.CreatePlaylist == nil {
		return SavedPlaylist{}, ErrUnsupportedSelector
	}
	// The catalog's own create (review P32): one transaction with its checks
	// (playable leaves, library access), its receipt budget and its positions.
	id, created, err := s.CreatePlaylist(ctx, p, key, req.Name, req.Summary, items)
	if err != nil {
		return SavedPlaylist{}, err
	}
	return SavedPlaylist{PlaylistID: id, Revision: strconv.FormatInt(created, 10), Entries: len(items), State: SaveStateSaved}, nil
}

// savedPlaylist answers a save whose key the caller has used: the playlist it
// made, or a mismatch for another request under the same key.
func (s *Service) savedPlaylist(ctx context.Context, p identity.Principal, key string, req SaveAsPlaylistRequest) (SavedPlaylist, bool, error) {
	// A large save under this key: its progress. One whose request died before
	// it made the playlist makes (or finds) the playlist now.
	if r, err := s.loadSave(ctx, s.DB, identity.PersonalKey(p.Viewer), key); err == nil {
		if r.playlist == "" && r.state == "building" {
			out, err := s.linkSave(ctx, p, key, req)
			return out, true, err
		}
		out := s.saveView(ctx, r)
		var name, summary string
		if r.playlist != "" && s.DB.QueryRowContext(ctx, `SELECT name,summary FROM catalog_playlists WHERE token=?`, r.playlist).Scan(&name, &summary) == nil && (name != req.Name || summary != req.Summary) {
			return SavedPlaylist{}, true, ErrIdempotencyMismatch
		}
		return out, true, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SavedPlaylist{}, true, err
	}
	out := SavedPlaylist{State: SaveStateSaved}
	var name, summary string
	var revision int64
	err := s.DB.QueryRowContext(ctx, `SELECT pl.token,pl.name,pl.summary,pl.revision,(SELECT count(*) FROM catalog_playlist_entries e WHERE e.playlist_id=pl.id) FROM catalog_playlists pl WHERE pl.owner_authority=? AND pl.owner_account=? AND pl.owner_profile=? AND pl.creation_operation=?`, p.Authority, p.AccountID, p.ProfileID, key).Scan(&out.PlaylistID, &name, &summary, &revision, &out.Entries)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, true, err
	}
	// The key made a playlist already: the same request is its replay (answered
	// before the queue is read again, so a queue changed since doesn't matter);
	// another name or summary under the key is a reuse.
	if name != req.Name || summary != req.Summary {
		return SavedPlaylist{}, true, ErrIdempotencyMismatch
	}
	out.Revision = strconv.FormatInt(revision, 10)
	return out, true, nil
}
