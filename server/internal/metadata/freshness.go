package metadata

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Two things decided how often this server asked a metadata provider for a document it
// already had: nothing, and nothing. A work row woken for any reason called the provider, and
// nothing anywhere bounded how many such calls a library could make. On a two or three million
// item library behind a one-a-second pacing clock that is a month of continuous requests to
// one service, per pass, which is not an allowance anybody grants.
//
// So there are three rules, and they are all here.
//
//  1. Freshness. A fetched document is trusted for a period that depends on what it is. A
//     finished film's facts do not change; a series that is airing this week gains an episode
//     every week; a fingerprint-to-recording mapping barely moves at all. Inside that period a
//     refresh is not considered, and the stored evidence is republished instead — so local
//     changes still reach the catalogue while the provider is not asked.
//  2. Conditional requests. Outside that period the provider is asked, but with the validators
//     it gave last time, so an unchanged document is a 304 with no body.
//  3. A budget. Refreshes are bounded per provider per hour and per day, durably, so a
//     restart cannot reset the allowance and a library-wide wake cannot spend it in one pass.
//     A first fetch is never budgeted: a newly added item is what the person is waiting for.

// Freshness windows, per provider and resource kind. They are deliberately generous. A
// provider is a source of facts that were mostly settled before the file existed; the cost of
// being a week late with a changed overview is nothing, and the cost of asking is a request
// that a library of millions multiplies by millions.
const (
	freshnessMovie       = 30 * 24 * time.Hour
	freshnessShow        = 14 * 24 * time.Hour
	freshnessEpisode     = 30 * 24 * time.Hour
	freshnessAiringShow  = 36 * time.Hour
	freshnessRecent      = 24 * time.Hour
	freshnessRecording   = 90 * 24 * time.Hour
	freshnessReleaseData = 60 * 24 * time.Hour
	freshnessDefault     = 14 * 24 * time.Hour
)

// RefreshPriority says why a document is being looked at, which is the only thing that
// shortens a freshness window. Nothing lengthens one.
type RefreshPriority int

const (
	// RefreshOrdinary is a document nobody is waiting for.
	RefreshOrdinary RefreshPriority = iota
	// RefreshAiring is a series with an episode due: the one case where a fortnight is too
	// long, because the person watching expects next week's episode to appear.
	RefreshAiring
	// RefreshRecentlyAdded is something that arrived in the library in the last few days, and
	// is therefore the most likely thing anybody is about to open.
	RefreshRecentlyAdded
)

// documentFreshness is how long a fetched document is trusted before a refresh is considered.
func documentFreshness(provider, kind string, priority RefreshPriority) time.Duration {
	window := freshnessDefault
	switch provider {
	case "musicbrainz":
		window = freshnessReleaseData
	case "acoustid":
		window = freshnessRecording
	default:
		switch kind {
		case "movie":
			window = freshnessMovie
		case "show", "anime":
			window = freshnessShow
		case "episode":
			window = freshnessEpisode
		}
	}
	switch priority {
	case RefreshAiring:
		window = min(window, freshnessAiringShow)
	case RefreshRecentlyAdded:
		window = min(window, freshnessRecent)
	}
	return window
}

// Budget caps, per provider. They are what one server may spend on refreshing documents it
// already holds, and they are what stops a library-wide wake from becoming a stampede: at 240
// an hour a two-million-item library takes a year to cycle, which is the point — the items
// anybody is actually opening are the recently added ones, and those are never budgeted.
const (
	refreshBudgetHour = 240
	refreshBudgetDay  = 2000
)

// documentValidators is what was stored about one provider document.
type documentValidators struct {
	ETag         string
	LastModified string
	CheckedAt    time.Time
	Found        bool
}

func freshnessKey(provider, kind, resource string) (string, string, string, bool) {
	if provider == "" || kind == "" || resource == "" || len(provider)+len(kind)+len(resource) > 512 {
		return "", "", "", false
	}
	return provider, kind, resource, true
}

// documentState reads when a document was last checked and with what validators. A row that is
// not there is a document never fetched, which is never fresh and never budgeted.
func (s *Service) documentState(ctx context.Context, provider, kind, resource string) (documentValidators, error) {
	var out documentValidators
	p, k, r, ok := freshnessKey(provider, kind, resource)
	if !ok {
		return out, nil
	}
	var checked int64
	e := dbwork.QueryRow(ctx, s.db, `SELECT checked_at,etag,last_modified FROM metadata_document_freshness WHERE provider=? AND kind=? AND resource=?`, p, k, r).Scan(&checked, &out.ETag, &out.LastModified)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Found = true
	out.CheckedAt = time.Unix(checked, 0).UTC()
	return out, nil
}

// documentFresh answers the only question the worker needs: may this document be left alone?
//
// The owner's Check for new details narrows which held documents may be asked
// for again: "regular" uses the windows above, "recent" only recently added and
// airing titles, "never" none. Refresh all overrides it for every document
// checked before it was asked for, and still spends the refresh allowance. A
// document never fetched is never fresh, and an owner's refresh of one title
// never reaches here.
func (s *Service) documentFresh(ctx context.Context, provider, kind, resource string, priority RefreshPriority, now time.Time) (bool, documentValidators, error) {
	state, e := s.documentState(ctx, provider, kind, resource)
	if e != nil || !state.Found {
		return false, state, e
	}
	var epoch int64
	if e = dbwork.QueryRow(ctx, s.db, `SELECT refresh_all_epoch FROM metadata_runtime_settings WHERE singleton=1`).Scan(&epoch); e != nil {
		return false, state, e
	}
	// The epoch is whole seconds, so a document checked in the second Refresh all
	// was asked for counts as checked before it.
	if epoch > 0 && state.CheckedAt.Unix() <= epoch {
		return false, state, nil
	}
	switch policy, _ := s.refreshPolicy.Load().(string); policy {
	case "never":
		return true, state, nil
	case "recent":
		if priority == RefreshOrdinary {
			return true, state, nil
		}
	}
	return now.Sub(state.CheckedAt) < documentFreshness(provider, kind, priority), state, nil
}

// noteDocumentChecked records that the provider was asked. changed says whether it answered
// with a document rather than "not modified"; either way the document is now known current,
// which is what a freshness window is about, so both move checked_at.
func (s *Service) noteDocumentChecked(ctx context.Context, provider, kind, resource string, v documentValidators, changed bool, now time.Time) error {
	p, k, r, ok := freshnessKey(provider, kind, resource)
	if !ok {
		return nil
	}
	fetched := int64(0)
	if changed {
		fetched = now.Unix()
	}
	_, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO metadata_document_freshness(provider,kind,resource,fetched_at,checked_at,etag,last_modified) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(provider,kind,resource) DO UPDATE SET checked_at=excluded.checked_at,etag=excluded.etag,last_modified=excluded.last_modified,
  fetched_at=CASE WHEN excluded.fetched_at>0 THEN excluded.fetched_at ELSE metadata_document_freshness.fetched_at END`,
		p, k, r, fetched, now.Unix(), v.ETag, v.LastModified)
	return e
}

// ForgetDocumentFreshness makes one document stale again. It is what an owner asking for a
// refresh means: the rule exists to stop this server asking on its own, never to stop a person
// asking.
func (s *Service) ForgetDocumentFreshness(ctx context.Context, provider, kind, resource string) error {
	p, k, r, ok := freshnessKey(provider, kind, resource)
	if !ok {
		return nil
	}
	_, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `DELETE FROM metadata_document_freshness WHERE provider=? AND kind=? AND resource=?`, p, k, r)
	return e
}

type budgetSpan struct {
	name  string
	width time.Duration
	cap   int
}

var refreshBudgetSpans = []budgetSpan{{"hour", time.Hour, refreshBudgetHour}, {"day", 24 * time.Hour, refreshBudgetDay}}

// admitRefresh spends one unit of a provider's refresh allowance, or reports that there is
// none left. Both windows are checked and both are spent in one transaction, so two workers
// cannot each see room for the last call.
func (s *Service) admitRefresh(ctx context.Context, provider string, now time.Time) (bool, error) {
	if provider == "" {
		return false, nil
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return false, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, span := range refreshBudgetSpans {
		spent, err := refreshWindowTx(ctx, tx, provider, span, now)
		if err != nil {
			return false, err
		}
		if spent >= span.cap {
			return false, nil
		}
	}
	for _, span := range refreshBudgetSpans {
		if _, err := tx.ExecContext(ctx, `UPDATE metadata_refresh_budget SET spent=spent+1 WHERE provider=? AND span=?`, provider, span.name); err != nil {
			return false, err
		}
	}
	return true, gated.Commit()
}

// refreshWindowTx rolls the window forward if it has elapsed and returns what is spent in the
// current one. The window start is not aligned to the clock: aligning it would give every
// server in the world the same moment to start spending again.
func refreshWindowTx(ctx context.Context, tx *sql.Tx, provider string, span budgetSpan, now time.Time) (int, error) {
	var start int64
	var spent int
	e := tx.QueryRowContext(ctx, `SELECT window_start,spent FROM metadata_refresh_budget WHERE provider=? AND span=?`, provider, span.name).Scan(&start, &spent)
	if errors.Is(e, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_refresh_budget(provider,span,window_start,spent) VALUES(?,?,?,0)`, provider, span.name, now.Unix()); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	// A window that started in the future is a clock that moved backwards, which must not
	// freeze the allowance for ever; it is reopened like an elapsed one.
	if elapsed := now.Sub(time.Unix(start, 0).UTC()); elapsed >= span.width || elapsed < 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE metadata_refresh_budget SET window_start=?,spent=0 WHERE provider=? AND span=?`, now.Unix(), provider, span.name); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return spent, nil
}

// RefreshBudgetRemaining is what is left in each window, for the owner's diagnostics and for
// tests. It reads; it never rolls a window forward.
func (s *Service) RefreshBudgetRemaining(ctx context.Context, provider string, now time.Time) (map[string]int, error) {
	out := map[string]int{}
	for _, span := range refreshBudgetSpans {
		var start int64
		var spent int
		e := dbwork.QueryRow(ctx, s.db, `SELECT window_start,spent FROM metadata_refresh_budget WHERE provider=? AND span=?`, provider, span.name).Scan(&start, &spent)
		if errors.Is(e, sql.ErrNoRows) {
			out[span.name] = span.cap
			continue
		}
		if e != nil {
			return nil, e
		}
		// A window that started in the future is a clock that moved backwards, which must not
		// freeze the allowance for ever; it is reopened like an elapsed one.
		if elapsed := now.Sub(time.Unix(start, 0).UTC()); elapsed >= span.width || elapsed < 0 {
			spent = 0
		}
		out[span.name] = max(span.cap-spent, 0)
	}
	return out, nil
}

// providerRetryAt is the backoff a refused provider earns: exponential, capped, with jitter
// proportional to the delay so a library-wide failure does not come back as one wave. It is
// the rule networking.RetryAt applies to Hosted contact, and the reason it is spelled out here
// rather than imported is that this package must not depend on the claim stack. A provider's
// own Retry-After is a floor that is never undercut, only spread beyond.
func providerRetryAt(now time.Time, unit time.Duration, attempts, maxShift int, floor time.Time) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > maxShift {
		attempts = maxShift
	}
	delay := unit << attempts
	spread := time.Duration(rand.Int64N(int64(delay/2) + 1))
	if at := now.Add(delay); floor.After(at) {
		return floor.Add(spread)
	}
	return now.Add(delay + spread)
}
