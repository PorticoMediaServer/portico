package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// ProgressEntry is one play of a downloaded copy, as the device last saw it.
//
// A device keeps one entry per play, not one per title: watching a film twice
// on a flight is two plays and is reported as two. playId names the play and
// stays the same while the device reports later positions of it. observedAt is
// the device's own clock at the moment of the observation, which is the only
// ordering authority available for something done offline. ended is true when
// the file played to its end; whether a play that stopped short counts as
// watched is decided here, by the viewer's own settings, exactly as it is for
// a play the server watched happen.
type ProgressEntry struct {
	PlayID          string  `json:"playId"`
	ItemID          string  `json:"itemId"`
	PositionSeconds float64 `json:"positionSeconds"`
	Ended           bool    `json:"ended,omitempty"`
	ObservedAt      string  `json:"observedAt"`
}

// Progress outcome codes, reported per entry. "applied" moved the title's
// resume point and watched state. "stale_observation" and "superseded_online"
// did not, because something newer is already known; the play is still in the
// history. "history_paused" kept the viewer's own history untouched because
// they have paused it.
const (
	ProgressApplied       = "applied"
	ProgressHistoryPaused = "history_paused"
	ProgressStale         = "stale_observation"
	ProgressSuperseded    = "superseded_online"
	ProgressMissing       = "item_deleted"
	ProgressInvalid       = "invalid_entry"
	ProgressFuture        = "observation_in_future"
)

// ProgressResult reports what happened to one entry, and what the server now
// holds, so a device can bring its own resume point up to date without a
// second read.
type ProgressResult struct {
	PlayID          string  `json:"playId"`
	ItemID          string  `json:"itemId"`
	Outcome         string  `json:"outcome"`
	PositionSeconds float64 `json:"positionSeconds"`
	Watched         bool    `json:"watched"`
	ObservedAt      string  `json:"observedAt,omitempty"`
}

// ProgressReceipt is the answer to one deferred batch.
type ProgressReceipt struct {
	Applied   int              `json:"applied"`
	Conflicts int              `json:"conflicts"`
	Entries   []ProgressResult `json:"entries"`
}

// clockSkew is how far into the future an offline observation may sit before
// the server refuses it. A client whose clock ran ahead would otherwise pin its
// item against every later observation from every other device.
const clockSkew = 24 * time.Hour

// ErrNoPlayWriter is a server composed without the personal-state writer.
var ErrNoPlayWriter = errors.New("offline plays cannot be recorded on this server")

// SyncProgress records plays made offline.
//
// Every play that really started reaches the server's play history, the
// viewer's own history and the play counts, dated to when it happened. Whether
// it also moves the title's resume point and watched state depends on one
// question: is it the newest thing this server knows about the viewer and the
// title? It is not when a later offline play was already accepted, when the
// viewer played the title online afterwards, or when they marked it watched
// or unwatched (alone or as part of a show, season, album or book) or moved
// its resume point by hand afterwards. A play that is the newest is handed to
// the same writer online playback uses, so the viewer's own thresholds decide
// what it means; one that is not is recorded and changes nothing else.
func (s *Service) SyncProgress(ctx context.Context, p identity.Principal, operationID string, entries []ProgressEntry) (ProgressReceipt, error) {
	out := ProgressReceipt{Entries: []ProgressResult{}}
	if !validID.MatchString(operationID) || len(entries) == 0 || len(entries) > MaxBatchTargets {
		return out, ErrInput
	}
	if s.plays == nil {
		return out, ErrNoPlayWriter
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-progress:" + key
	raw, digest, e := operations.Receipt(tx, scope, operationID, entries, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		if e = json.Unmarshal([]byte(raw), &out); e != nil {
			return out, e
		}
		if e = operations.ReauthorizeReceipt(out.Entries, func(row ProgressResult) string {
			return row.ItemID
		}, func(item string) error {
			return s.visibleItem(ctx, tx, p, item)
		}, func(row *ProgressResult) {
			*row = ProgressResult{PlayID: row.PlayID, ItemID: row.ItemID, Outcome: ProgressMissing}
		}); e != nil {
			return ProgressReceipt{}, e
		}
		out.Applied, out.Conflicts = 0, 0
		for _, row := range out.Entries {
			switch row.Outcome {
			case ProgressApplied:
				out.Applied++
			case ProgressHistoryPaused:
			default:
				out.Conflicts++
			}
		}
		return out, nil
	}
	preferences, _, _, e := operations.EffectivePreferences(tx, p.Viewer, "")
	if e != nil {
		return out, e
	}
	paused := preferences.Bool("privacy.pauseWatchHistory")
	personal := identity.PersonalKey(p.Viewer)
	conflict := func(result ProgressResult, outcome string) {
		result.Outcome = outcome
		out.Entries, out.Conflicts = append(out.Entries, result), out.Conflicts+1
	}
	for _, entry := range entries {
		result := ProgressResult{PlayID: entry.PlayID, ItemID: entry.ItemID, ObservedAt: entry.ObservedAt}
		observed, err := time.Parse(time.RFC3339, entry.ObservedAt)
		if !validID.MatchString(entry.PlayID) || !validID.MatchString(entry.ItemID) || err != nil || entry.PositionSeconds < 0 || math.IsNaN(entry.PositionSeconds) || math.IsInf(entry.PositionSeconds, 0) {
			conflict(result, ProgressInvalid)
			continue
		}
		observedMS := observed.UnixMilli()
		if observedMS > now+int64(clockSkew/time.Millisecond) {
			conflict(result, ProgressFuture)
			continue
		}
		// Small clock skew is usable evidence, but must never reserve a
		// future position against newer online activity or explicit actions.
		if observedMS > now {
			observed = time.UnixMilli(now).UTC()
			observedMS = now
		}
		entity, err := entityid.Resolve(ctx, tx, entry.ItemID)
		if errors.Is(err, entityid.ErrNotFound) {
			conflict(result, ProgressMissing)
			continue
		}
		if err != nil {
			return out, err
		}
		// A title this viewer may not see answers exactly as one that does not
		// exist, and nothing is written about it.
		if err = s.visibleItem(ctx, tx, p, entry.ItemID); errors.Is(err, identity.ErrContentRestricted) {
			conflict(result, ProgressMissing)
			continue
		} else if err != nil {
			return out, err
		}
		var duration float64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT max(a.duration) FROM catalog_asset_links ia JOIN catalog_assets a ON a.id=ia.asset_id WHERE ia.entity_id=?),0)`, entity).Scan(&duration); err != nil {
			return out, err
		}
		if duration > 0 && entry.PositionSeconds > duration {
			entry.PositionSeconds = duration
		}
		outcome, keepBook, err := newest(ctx, tx, key, personal, entity, observed)
		if err != nil {
			return out, err
		}
		if paused {
			outcome = ProgressHistoryPaused
		}
		// The device chooses playId, so it is never used as it stands: bound to
		// the viewer, one viewer's play can neither collide with nor reach
		// another's, nor a real playback session's.
		play := playback.OfflinePlay{
			ID: "offline-" + identity.Digest(key + "\x00" + entry.PlayID)[:40], Item: entry.ItemID, At: observed,
			PositionSeconds: entry.PositionSeconds, DurationSeconds: duration, Ended: entry.Ended,
			Newest: outcome == ProgressApplied, KeepBookPointer: keepBook,
		}
		// This is the one place offline viewing enters personal state.
		if err = playback.AcceptOfflinePlay(tx, p, play, s.plays); err != nil {
			return out, err
		}
		if outcome == ProgressApplied {
			if _, err = tx.ExecContext(ctx, `INSERT INTO download_progress_marks(profile_key,item_id,observed_ms) VALUES(?,?,?) ON CONFLICT(profile_key,item_id) DO UPDATE SET observed_ms=excluded.observed_ms`, key, entity, observedMS); err != nil {
				return out, err
			}
		}
		current, watched, err := readCurrentProgress(ctx, tx, personal, entity)
		if err != nil {
			return out, err
		}
		result.Outcome, result.PositionSeconds, result.Watched = outcome, current, watched
		out.Entries = append(out.Entries, result)
		switch outcome {
		case ProgressApplied:
			out.Applied++
		case ProgressStale, ProgressSuperseded:
			out.Conflicts++
		}
	}
	if e = operations.SaveReceipt(tx, scope, operationID, digest, out, now); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

// newest decides whether an offline observation is the newest thing known
// about this viewer and title, and names what beat it when it is not.
//
// keepBook is for a part of a book. A part can be the newest thing known about
// itself while the book has moved on: the listener heard part three on a plane
// and has since been in part five at home. Part three's own place is then
// recorded, and the book stays where it is.
func newest(ctx context.Context, tx *sql.Tx, key, personal string, entity int64, observed time.Time) (outcome string, keepBook bool, err error) {
	outcome, keepBook, err = ProgressApplied, false, nil
	fail := func(e error) (string, bool, error) { return "", false, e }
	beaten := func(by string) (string, bool, error) { return by, false, nil }
	observedMS := observed.UnixMilli()
	// A later offline play of the same title, from this device or another.
	var mark int64
	if e := tx.QueryRowContext(ctx, `SELECT observed_ms FROM download_progress_marks WHERE profile_key=? AND item_id=?`, key, entity).Scan(&mark); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return fail(e)
	}
	if observedMS <= mark {
		return beaten(ProgressStale)
	}
	// Online playback: the last activity recorded for the title, and the
	// session that is its writer now. A session owns the title from the moment
	// it starts, before its first report, and a start records no activity, so
	// activity alone would let a play older than the start take the title from
	// a session that has only just begun.
	var activity, session string
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT a.updated_at FROM progress_activity a WHERE a.profile_id=? AND a.item_id=?),''),
 COALESCE((SELECT max(COALESCE(o.created_at,''),COALESCE(o.reported_at,'')) FROM progress w JOIN playback_observations o ON o.session_id=w.playback_id WHERE w.profile_id=? AND w.item_id=?),'')`, personal, entity, personal, entity).Scan(&activity, &session); e != nil {
		return fail(e)
	}
	if at, e := time.Parse(time.RFC3339, activity); e == nil && at.After(observed) {
		return beaten(ProgressSuperseded)
	}
	if at, e := time.Parse(time.RFC3339, session); e == nil && !observed.After(at) {
		return beaten(ProgressSuperseded)
	}
	// Something the viewer did on purpose afterwards: marked this title, or
	// the show, season, album or book it belongs to, or reset its resume point.
	stamp := observed.UTC().Format("2006-01-02T15:04:05.000000000Z")
	var later bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM personal_explicit_events x WHERE x.profile_id=? AND x.item_id=? AND x.field IN('watched','continueDismissed') AND x.created_at>?)
 OR EXISTS(SELECT 1 FROM container_personal_state c WHERE c.profile_id=? AND c.watermark>? AND (
  (c.kind='season' AND c.container_id=(SELECT season_id FROM catalog_episodes WHERE entity_id=?)) OR
  (c.kind='show' AND c.container_id=(SELECT show_id FROM catalog_episodes WHERE entity_id=?)) OR
  (c.kind='album' AND c.container_id=(SELECT album_id FROM catalog_songs WHERE entity_id=?)) OR
  (c.kind='book' AND c.container_id=(SELECT book_id FROM catalog_book_files WHERE entity_id=?))))`,
		personal, entity, stamp, personal, stamp, entity, entity, entity, entity).Scan(&later); e != nil {
		return fail(e)
	}
	if later {
		return beaten(ProgressSuperseded)
	}
	// A part of a book answers to the book's pointer as well. Listening that
	// is not newer than what the pointer knows does not move the book: in the
	// pointer's own part it is superseded outright, in another part it keeps
	// that part's place and leaves the book alone.
	book, e := readBookPointer(ctx, tx, key, personal, entity)
	if e != nil {
		return fail(e)
	}
	if book.book != 0 && book.current != 0 {
		older := book.datedAtOrAfter(observed)
		if book.current == entity && older {
			return beaten(ProgressSuperseded)
		}
		keepBook = book.current != entity && (older || observedMS <= book.mark)
	}
	return ProgressApplied, keepBook, nil
}

func readCurrentProgress(ctx context.Context, tx *sql.Tx, personal string, entity int64) (float64, bool, error) {
	var positionMS int64
	var watched bool
	e := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT position FROM progress WHERE profile_id=? AND item_id=?),0),`+personalstate.SQL("?", "?")+``, personal, entity, personal, entity).Scan(&positionMS, &watched)
	return float64(positionMS) / 1000, watched, e
}

// bookPointer is where a book's pointer stands when an observation of one of
// its parts arrives, read before that observation writes anything, with the
// evidence that dates it:
//
//   - the session the pointer is bound to: when it was created, and when it
//     last reported. A session owns the pointer from the moment it starts, before
//     its first report, and a start writes no activity, so activity alone would
//     let an observation older than the start rewind the book, or take it to
//     another part and unbind a session whose reports could then never move the
//     pointer again;
//   - the recorded activity of the part the pointer names, which is the time of
//     the stored position, whether a server session wrote it or an earlier
//     observation did;
//   - the last offline observation accepted for that part, which is kept to the
//     millisecond where activity is kept to the second, so two parts observed
//     within one second are still ordered by when each was observed.
//
// An observation changes the pointer, and the position of the part it names,
// only when it is strictly newer than all of that: a tie leaves what a server
// session wrote. A play that passes is activated like a session starting
// (playback.AcceptOfflinePlay), which is what moves the pointer to its part.
type bookPointer struct {
	// book is the book the observed item is a part of; zero when the item is
	// not a part of a book. current is the part the pointer names; zero when the
	// book has no pointer yet.
	book, current int64
	activity      string
	session       string
	mark          int64
}

func readBookPointer(ctx context.Context, tx *sql.Tx, key, personal string, entity int64) (bookPointer, error) {
	var b bookPointer
	e := tx.QueryRowContext(ctx, `SELECT f.book_id,COALESCE(r.item_id,0),
 COALESCE((SELECT a.updated_at FROM progress_activity a WHERE a.profile_id=r.profile_id AND a.item_id=r.item_id),''),
 COALESCE((SELECT max(COALESCE(o.created_at,''),COALESCE(o.reported_at,'')) FROM playback_observations o WHERE o.session_id=r.playback_id),''),
 COALESCE((SELECT m.observed_ms FROM download_progress_marks m WHERE m.profile_key=? AND m.item_id=r.item_id),0)
 FROM catalog_book_files f JOIN catalog_entities i ON i.id=f.entity_id AND i.kind=9 LEFT JOIN book_resume r ON r.profile_id=? AND r.book_id=f.book_id WHERE f.entity_id=?`, key, personal, entity).Scan(&b.book, &b.current, &b.activity, &b.session, &b.mark)
	if errors.Is(e, sql.ErrNoRows) {
		return bookPointer{}, nil
	}
	return b, e
}

// datedAtOrAfter reports whether the pointer's part has recorded activity, or
// its bound session a start or a report, at or after the observation.
func (b bookPointer) datedAtOrAfter(observed time.Time) bool {
	for _, evidence := range []string{b.activity, b.session} {
		if at, err := time.Parse(time.RFC3339, evidence); err == nil && !observed.After(at) {
			return true
		}
	}
	return false
}
