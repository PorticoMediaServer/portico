package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Provider trend snapshots and the richer typed provider facts the metadata
// service publishes. The discovery tables are the provider lane's durable
// output: a bounded snapshot of documented provider trend feeds that the
// catalog consumes read-only, matching provider identities to locally
// available media before anything is shown. No title, artwork or rank text
// leaves a provider here that is not locally available; the catalog owns
// viewer, restriction and library-local presentation.
//
// metadata_discovery_items is the authoritative cross-lane contract:
//
//	metadata_discovery_items(provider TEXT, media_kind TEXT, provider_id TEXT,
//	  rank INTEGER, fetched_at TEXT, expires_at TEXT,
//	  PRIMARY KEY(provider, media_kind, provider_id))
//
// media_kind is movie/tv/anime; provider ids are provider-scoped strings;
// times are UTC RFC3339; rank starts at 1; expires_at is the last permissible
// serving time, at most fetched_at+48h. Additional refresh state belongs to
// the provider-owned table below and must stay out of the contract table.

// DiscoveryStaleLimit is the maximum time a snapshot may keep serving. A
// refresh that cannot complete within it loses the row, so a stale feed is
// never presented as current evidence for longer than the bounded window.
const DiscoveryStaleLimit = 48 * time.Hour

// DiscoveryRefreshInterval is the target refresh period for a documented
// trend feed. It is a target, not a floor on failures: Retry-After and
// bounded exponential backoff own the schedule when a provider refuses.
const DiscoveryRefreshInterval = 6 * time.Hour

// DiscoveryBackoff is the base delay a failed refresh waits before trying
// again; attempts multiply it, capped by DiscoveryBackoffLimit.
const (
	DiscoveryBackoff     = 30 * time.Second
	DiscoveryBackoffCap  = 24 * time.Hour
	DiscoveryMaxAttempts = 6
)

// DiscoveryEntry is one cached provider trend row.
type DiscoveryEntry struct {
	Provider   string
	MediaKind  string
	ProviderID string
	Rank       int
	FetchedAt  time.Time
	ExpiresAt  time.Time
}

// DiscoveryItems returns the unexpired rows of one snapshot in rank order.
// Serving remains the catalog's decision; this helper only applies the cached
// expiry, so a disabled provider, revoked consent or viewer restriction still
// filters at the consumer.
func DiscoveryItems(ctx context.Context, db *sql.DB, provider, mediaKind string, now time.Time) ([]DiscoveryEntry, error) {
	rows, err := db.QueryContext(ctx, `SELECT provider_id,rank,fetched_at,expires_at FROM metadata_discovery_items
 WHERE provider=? AND media_kind=? AND expires_at>? ORDER BY rank,provider_id`, provider, mediaKind, rfc3339(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DiscoveryEntry{}
	for rows.Next() {
		var e DiscoveryEntry
		var fetched, expires string
		e.Provider, e.MediaKind = provider, mediaKind
		if err = rows.Scan(&e.ProviderID, &e.Rank, &fetched, &expires); err != nil {
			return nil, err
		}
		e.FetchedAt = parseRFC3339(fetched)
		e.ExpiresAt = parseRFC3339(expires)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DiscoveryProviderIDs is the lean form of DiscoveryItems: the ids a local
// match needs, in rank order.
func DiscoveryProviderIDs(ctx context.Context, db *sql.DB, provider, mediaKind string, now time.Time) ([]string, error) {
	items, err := DiscoveryItems(ctx, db, provider, mediaKind, now)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.ProviderID)
	}
	return out, nil
}

// DiscoverySnapshot reports when a snapshot was last fetched and when it stops
// serving, even if it has expired.
func DiscoverySnapshot(ctx context.Context, db *sql.DB, provider, mediaKind string) (fetched, expires time.Time, ok bool, err error) {
	var fetchedAt, expiresAt string
	err = db.QueryRowContext(ctx, `SELECT fetched_at,expires_at FROM metadata_discovery_items
 WHERE provider=? AND media_kind=? ORDER BY rank,provider_id LIMIT 1`, provider, mediaKind).Scan(&fetchedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	return parseRFC3339(fetchedAt), parseRFC3339(expiresAt), true, nil
}

// DiscoveryRefreshState is one feed's provider-owned scheduling state.
// Attempts counts consecutive failed refreshes and drives bounded backoff.
type DiscoveryRefreshState struct {
	Attempts  int
	NextDue   time.Time
	FetchedAt time.Time
	Status    string
}

// DiscoveryRefreshState reads the scheduling row for one feed. ok is false
// when the feed has never been scheduled.
func LoadDiscoveryRefresh(ctx context.Context, db *sql.DB, provider, mediaKind string) (DiscoveryRefreshState, bool, error) {
	var state DiscoveryRefreshState
	var next, fetched string
	err := db.QueryRowContext(ctx, `SELECT next_attempt,attempts,status,fetched_at FROM metadata_discovery_refresh WHERE provider=? AND media_kind=?`, provider, mediaKind).Scan(&next, &state.Attempts, &state.Status, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscoveryRefreshState{}, false, nil
	}
	if err != nil {
		return DiscoveryRefreshState{}, false, err
	}
	state.NextDue = parseRFC3339(next)
	state.FetchedAt = parseRFC3339(fetched)
	return state, true, nil
}

// DiscoveryRefreshDue reports whether a (provider, media_kind) refresh may run
// now: it is due when nothing has ever been scheduled for it, or its next
// scheduled attempt is not in the future. Refresh state is the only thing
// consulted, so an idle server's step never writes or wakes.
func DiscoveryRefreshDue(ctx context.Context, db *sql.DB, provider, mediaKind string, now time.Time) (bool, error) {
	state, ok, err := LoadDiscoveryRefresh(ctx, db, provider, mediaKind)
	if err != nil || !ok {
		return !ok && err == nil, err
	}
	return !state.NextDue.After(now), nil
}

// ClearDiscoverySnapshots removes every provider trend row and its scheduling
// state. Revoking the global provider consent must stop serving every cached
// trend immediately: the table is the provider lane's output, so the provider
// lane clears it rather than trusting every future consumer to remember. A
// later consent confirmation refetches from scratch. The caller checks first
// whether anything exists, so a revoked server performs no writes at all.
func ClearDiscoverySnapshots(ctx context.Context, db *sql.DB) error {
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_discovery_items`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_discovery_refresh`); err != nil {
		return err
	}
	return gated.Commit()
}

// DiscoverySnapshotStored reports whether any provider trend row is cached, so
// a consent check on an idle server never opens a write transaction.
func DiscoverySnapshotStored(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_discovery_items)`).Scan(&count); err != nil {
		return false, err
	}
	return count == 1, nil
}

// StoreDiscoverySnapshot atomically replaces one (provider, media_kind)
// snapshot with the given provider ids, ranked in list order. Readers see the
// old snapshot or the new one; a crash leaves the previous one intact. The
// refresh state advances, and the library revisions whose policies enable the
// provider move so Home/Discover projections re-read the feed.
// SnapshotPermission reports, inside the transaction that would replace a
// snapshot, whether that replacement may commit. The metadata service closes
// this over its consent and per-library provider policy reads; persistence
// never imports a caller, so the fence is one callback shape both sides know.
type SnapshotPermission func(ctx context.Context, tx *sql.Tx, provider, mediaKind string) (bool, error)

// ErrDiscoveryFence is returned by StoreDiscoverySnapshot when the permission
// check refused the commit. Nothing was written: no snapshot row, no scheduling
// row, no revision. The caller treats it as "nothing to do this pass", not as a
// provider failure.
var ErrDiscoveryFence = errors.New("provider discovery snapshot refused by policy fence")

// StoreDiscoverySnapshot atomically replaces one (provider, media_kind)
// snapshot with the given provider ids, ranked in list order. Readers see the
// old snapshot or the new one; a crash leaves the previous one intact. The
// permission fence runs inside this write transaction — the owner consent and
// provider policy answer observed here is the one the commit answers to — and
// the refresh state advances only when it passes. Committing also moves the
// library revisions whose policies enable the provider, so Home/Discover
// projections re-read the feed.
func StoreDiscoverySnapshot(ctx context.Context, db *sql.DB, provider, mediaKind string, providerIDs []string, now time.Time, permitted SnapshotPermission) error {
	if err := validDiscoveryKey(provider, mediaKind); err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if permitted != nil {
		allowed, err := permitted(ctx, tx, provider, mediaKind)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrDiscoveryFence
		}
	}
	fetched := rfc3339(now)
	expires := rfc3339(now.Add(DiscoveryStaleLimit))
	if _, err = tx.ExecContext(ctx, `DELETE FROM metadata_discovery_items WHERE provider=? AND media_kind=?`, provider, mediaKind); err != nil {
		return err
	}
	for i, id := range providerIDs {
		if i >= 75 {
			break // bounded snapshot, matching the row budget of one shelf family
		}
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 256 {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_discovery_items(provider,media_kind,provider_id,rank,fetched_at,expires_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(provider,media_kind,provider_id) DO UPDATE SET rank=excluded.rank,fetched_at=excluded.fetched_at,expires_at=excluded.expires_at`,
			provider, mediaKind, id, i+1, fetched, expires); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_discovery_refresh(provider,media_kind,next_attempt,attempts,status,error,fetched_at,revision)
 VALUES(?,?,?,0,'fresh','',?,1) ON CONFLICT(provider,media_kind) DO UPDATE SET
 next_attempt=excluded.next_attempt,attempts=0,status='fresh',error='',fetched_at=excluded.fetched_at,revision=metadata_discovery_refresh.revision+1`,
		provider, mediaKind, rfc3339(now.Add(DiscoveryRefreshInterval)), fetched); err != nil {
		return err
	}
	// Feed commits invalidate the catalog/Home projections that read them, once
	// per snapshot rather than per item, and only for libraries that have the
	// provider enabled: a disabled library's caches are already serving the
	// policy answer, not this feed.
	if _, err = tx.ExecContext(ctx, `UPDATE library_revisions SET revision=revision+1 WHERE library_id IN
 (SELECT library_id FROM screen_metadata_policies WHERE enabled=1 AND EXISTS(SELECT 1 FROM json_each(providers) WHERE value=?))`, provider); err != nil {
		return err
	}
	return gated.Commit()
}

// NoteDiscoveryRefreshFailure records one failed attempt with bounded
// exponential backoff, honouring a provider Retry-After when it is longer.
// The previous snapshot is untouched: refresh failures retain bounded stale
// data, and only the 48-hour expiry omits the row.
func NoteDiscoveryRefreshFailure(ctx context.Context, db *sql.DB, provider, mediaKind string, now time.Time, attempt int, retryAfter time.Duration, code string) error {
	if err := validDiscoveryKey(provider, mediaKind); err != nil {
		return err
	}
	if code == "" {
		code = "provider_unavailable"
	}
	if len(code) > 128 || strings.ContainsAny(code, "/\\\r\n") {
		code = "provider_unavailable"
	}
	delay := DiscoveryBackoff << min(max(attempt, 0), 10)
	if attempt+1 >= DiscoveryMaxAttempts {
		// A sustained failure parks the feed for a day: the provider is still
		// probed eventually — outages do end — but the schedule is bounded and
		// never a wake loop, and the stale snapshot, if any, is what serves
		// until its own 48-hour expiry omits it.
		delay = DiscoveryBackoffCap
	}
	if delay > DiscoveryBackoffCap {
		delay = DiscoveryBackoffCap
	}
	if retryAfter > delay {
		delay = min(retryAfter, DiscoveryBackoffCap)
	}
	nextAttempt := rfc3339(now.Add(delay))
	status, reason := "backoff", code
	if attempt+1 >= DiscoveryMaxAttempts {
		// Park the feed until an owner change or a much later pass; the stale
		// snapshot, if any, is still what serves, until it expires.
		status, reason = "unavailable", code
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_discovery_refresh(provider,media_kind,next_attempt,attempts,status,error,revision)
 VALUES(?,?,?,?,?,?,1) ON CONFLICT(provider,media_kind) DO UPDATE SET
 next_attempt=MAX(next_attempt,excluded.next_attempt),attempts=excluded.attempts,status=excluded.status,error=excluded.error,revision=metadata_discovery_refresh.revision+1`,
		provider, mediaKind, nextAttempt, attempt+1, status, reason); err != nil {
		return err
	}
	// The shared provider cooldown ledger feeds every metadata worker, so a
	// trending 429 also delays item matching, and vice versa.
	if retryAfter > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO metadata_provider_cooldowns(provider,next_attempt) VALUES(?,?)
 ON CONFLICT(provider) DO UPDATE SET next_attempt=MAX(next_attempt,excluded.next_attempt)`, provider, rfc3339(now.Add(min(max(retryAfter, 0), DiscoveryBackoffCap)))); err != nil {
			return err
		}
	}
	return gated.Commit()
}

func validDiscoveryKey(provider, mediaKind string) error {
	if strings.TrimSpace(provider) == "" || len(provider) > 64 || strings.ContainsAny(provider, "/\\\r\n") {
		return fmt.Errorf("invalid discovery provider %q", provider)
	}
	switch mediaKind {
	case "movie", "tv", "anime":
		return nil
	}
	return fmt.Errorf("invalid discovery media_kind %q", mediaKind)
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseRFC3339(value string) time.Time {
	t, _ := time.Parse(time.RFC3339, value)
	return t
}
