package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// The claim order: a search the owner asked for, then shows whose episodes
// are being dispatched, then shows, then everything else. Within a class a
// title never tried comes before a retry. Titles (films, episodes) are taken
// most recently catalogued first, so what was just added, and is on Recently
// Added, is matched ahead of an old backlog. Shows are taken oldest first, so
// a show a scan is still filling is reached after the shows it found before.
// A new episode does not send a matched or claimed show back to be matched
// (the episode is fetched on its own row); a show not yet matched is woken.
//
// Each class, kind and order is its own indexed probe that reads one row, so
// a claim costs the same whatever the backlog's size; the claim runs in a
// write transaction, and a sort over every pending row there held the write
// gate for as long as the backlog was large.
var screenClaimSQL = screenProbes([]string{"searching", "pending_children", "pending"}, `next_attempt<=?1 AND lease_until<=?1`)

// A title parked by an outage, once its cool-off has passed: the oldest
// retry time first.
var screenRecoverSQL = screenProbes([]string{"unavailable"}, `next_attempt>'' AND next_attempt<=?1 AND lease_until<=?1 AND error IN('unavailable','provider_unavailable','provider_backoff')`)

func screenProbes(statuses []string, where string) string {
	var arms []string
	arm := func(rank int, status, kind, extra, order string) {
		arms = append(arms, fmt.Sprintf(`SELECT * FROM (SELECT %d AS rank,target_kind,target_id,next_attempt,%s AS turn FROM screen_metadata_work INDEXED BY screen_metadata_due WHERE status='%s' AND target_kind='%s' AND %s%s ORDER BY %s LIMIT 1)`,
			rank, map[string]string{"show": "target_id", "item": "-target_id"}[kind], status, kind, where, extra, order))
	}
	for class, status := range statuses {
		rank := class * 2
		arm(rank, status, "show", "", "next_attempt,target_id")
		if status == "pending" {
			rank++
		}
		arm(rank, status, "item", ` AND next_attempt=''`, "target_id DESC")
		arm(rank, status, "item", ` AND next_attempt>''`, "next_attempt,target_id")
	}
	return `SELECT target_kind,target_id FROM (` + strings.Join(arms, " UNION ALL ") + `) ORDER BY rank,next_attempt,turn LIMIT 1`
}

func (s *Service) claimScreen(ctx context.Context) (*screenClaim, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return nil, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if ready, e := publicationSourcesReady(ctx, tx); e != nil || !ready {
		return nil, e
	}
	var kind string
	var target int64
	stamp := tvdbStamp(s.publicationTime())
	// A search the owner asked for goes first: it is one deliberate request with
	// somebody waiting on it, and a search that fails rejoins the queue as an
	// ordinary pending row, so it cannot hold the front.
	err = tx.QueryRowContext(ctx, screenClaimSQL, stamp).Scan(&kind, &target)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing else is due, so a title parked by an outage whose cool-off
		// has passed gets another try. Both marks are required: a retry time,
		// and one of the reasons only an outage records (screenRecoverable). A
		// retry time alone proves nothing, because rows parked before this rule
		// were all given one, whatever had parked them.
		err = tx.QueryRowContext(ctx, screenRecoverSQL, stamp).Scan(&kind, &target)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gated.Commit()
	}
	if err != nil {
		return nil, err
	}
	id, err := entityid.Public(ctx, tx, target)
	if errors.Is(err, entityid.ErrNotFound) {
		// The entity is gone; park the row instead of claiming it forever.
		if _, writeErr := tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='unavailable',error='metadata_scope_unavailable',next_attempt='',revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=?`, kind, target); writeErr != nil {
			return nil, writeErr
		}
		return nil, gated.Commit()
	}
	if err != nil {
		return nil, err
	}
	b, err := readScreenBase(ctx, tx, kind, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || err.Error() == "conflicting_local_metadata_budget" {
			status := "unavailable"
			reason := "metadata_scope_unavailable"
			if err.Error() == "conflicting_local_metadata_budget" {
				status = "needs_selection"
				reason = "conflicting_local_metadata_budget"
			}
			if _, writeErr := tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,error=?,next_attempt='',revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=?`, status, reason, kind, target); writeErr != nil {
				return nil, writeErr
			}
			return nil, gated.Commit()
		}
		return nil, err
	}
	if b.Available {
		if err = s.applyScreenLocal(ctx, tx, b); err != nil {
			return nil, err
		}
		b, err = readScreenBase(ctx, tx, kind, id)
		if err != nil {
			return nil, err
		}
	}
	// Adopt an already accepted legacy identity, rather than initiating a new
	// name search against titles that a prior provider or owner may have changed.
	// Only an identity with the library's own provider: one from a provider the
	// library has left is what sent this title back to be matched again.
	if !b.IdentityLocked && b.ProviderID == "" && b.Requested == "" && b.ItemKind != "episode" {
		for _, v := range b.Existing {
			if !screenProviderEnabled(b, v.Provider) {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET provider=?,provider_type=?,provider_id=?,selection_mode='legacy',selection_revision=selection_revision+1 WHERE target_kind=? AND target_id=?`, v.Provider, v.Type, v.ID, kind, target); err != nil {
				return nil, err
			}
			b, err = readScreenBase(ctx, tx, kind, id)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	local, err := LibraryLocalOnlyTx(ctx, tx, b.Library)
	if err != nil {
		return nil, err
	}
	blocked := ""
	if !b.Available {
		blocked = "source_unavailable"
	} else if local {
		blocked = "provider_disabled"
	} else if !b.Confirmed {
		blocked = "needs_consent"
	} else if !b.Enabled {
		blocked = "provider_disabled"
	} else if b.IdentityLocked && b.ProviderID == "" && b.Requested == "" {
		blocked = "manual_preserved"
	}
	if blocked != "" {
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,error='',revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=?`, blocked, kind, target)
		if err != nil {
			return nil, err
		}
		return nil, gated.Commit()
	}
	p := &screenClaim{Base: b, Token: identity.Token(), Until: tvdbStamp(s.publicationTime().Add(240 * time.Second))}
	if err = tx.QueryRowContext(ctx, `SELECT attempts FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, kind, target).Scan(&p.Attempts); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET lease=?,lease_until=?,revision=revision+1 WHERE target_kind=? AND target_id=?`, p.Token, p.Until, kind, target)
	if err != nil {
		return nil, err
	}
	p.Digest = screenInputDigest(b)
	p.Bound = screenUnbounded
	if kind == "show" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM catalog_assets`).Scan(&p.Bound); err != nil {
			return nil, err
		}
	}
	p.Fence = screenFence(b)
	return p, gated.Commit()
}
func (s *Service) screenCurrent(ctx context.Context, tx *sql.Tx, p *screenClaim) (bool, error) {
	if ready, e := publicationSourcesReady(ctx, tx); e != nil || !ready {
		return false, e
	}
	var token, until string
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT lease,lease_until,generation FROM screen_metadata_work WHERE target_kind=? AND target_id=?`, p.Base.TargetKind, p.Base.Entity).Scan(&token, &until, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if token != p.Token || until <= tvdbStamp(s.publicationTime()) || generation != p.Base.Generation {
		return false, nil
	}
	b, err := readScreenBaseWithin(ctx, tx, p.Base.TargetKind, p.Base.TargetID, p.Bound)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return screenFence(b) == p.Fence, nil
}
func (s *Service) screenStale(ctx context.Context, tx *sql.Tx, p *screenClaim) error {
	_, err := tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt=?,error='stale_result_discarded' WHERE target_kind=? AND target_id=? AND lease=?`, tvdbStamp(s.publicationTime().Add(time.Second)), p.Base.TargetKind, p.Base.Entity, p.Token)
	return err
}
func (s *Service) finishScreen(ctx context.Context, p *screenClaim, status, reason string) error {
	gated2, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	ok, err := s.screenCurrent(ctx, tx, p)
	if err != nil {
		return err
	}
	if !ok {
		if err = s.screenStale(ctx, tx, p); err != nil {
			return err
		}
		return gated2.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,error=?,revision=revision+1,lease='',lease_until='',attempts=0,next_attempt='' WHERE target_kind=? AND target_id=? AND lease=?`, status, reason, p.Base.TargetKind, p.Base.Entity, p.Token)
	if err != nil {
		return err
	}
	return gated2.Commit()
}
func (s *Service) failScreen(ctx context.Context, p *screenClaim, problem error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	gated3, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	ok, err := s.screenCurrent(ctx, tx, p)
	if err != nil {
		return err
	}
	if !ok || errors.Is(problem, errScreenStale) {
		if err = s.screenStale(ctx, tx, p); err != nil {
			return err
		}
		return gated3.Commit()
	}
	delay := min(6*time.Hour, time.Duration(1<<min(p.Attempts, 10))*30*time.Second)
	status, reason := "pending", "provider_unavailable"
	var pe *metadataprovider.Error
	if errors.Is(problem, context.DeadlineExceeded) {
		problem = &metadataprovider.Error{Code: "unavailable"}
	}
	if !errors.As(problem, &pe) {
		// Not the provider's fault: a local publication error. Say so.
		reason = "publication_failed"
	}
	if errors.As(problem, &pe) {
		reason = pe.Code
		if len(reason) > 128 {
			reason = "provider_unavailable"
		}
		if pe.RetryAfter > delay {
			delay = pe.RetryAfter
		}
		if !pe.Retryable() {
			status = "unavailable"
		}
		if pe.RetryAfter > 0 {
			_, err = tx.ExecContext(ctx, `INSERT INTO metadata_provider_cooldowns(provider,next_attempt) VALUES(?,?) ON CONFLICT(provider) DO UPDATE SET next_attempt=MAX(next_attempt,excluded.next_attempt)`, pe.Provider, tvdbStamp(s.publicationTime().Add(pe.RetryAfter)))
			if err != nil {
				return err
			}
		}
	}
	if p.Attempts >= 5 {
		status = "unavailable"
	}
	if delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	// Waiting out a provider's cooldown is not a failure of this title: it is
	// put off until the cooldown ends and none of its retries is used.
	counted := 1
	if pe != nil && pe.Code == "provider_backoff" {
		status, counted = "pending", 0
		delay = pe.RetryAfter
	}
	next := tvdbStamp(s.publicationTime().Add(delay))
	if status == "unavailable" {
		// A parked title is retried only if waiting can help: an outage or a
		// provider's own refusal to serve for now. It comes back after a
		// cool-off that keeps doubling, and claimScreen takes it only when
		// nothing else is due. A refusal that waiting cannot change, and a
		// local publication failure that has used its five tries, stay parked
		// with no retry time until the owner or a change to the title wakes them.
		next = ""
		if pe != nil && pe.Retryable() {
			// A refused request is recorded as request_rejected whatever its
			// status, so the reason is restated as one that says the provider,
			// not the request, was at fault; claimScreen recovers on that.
			if !screenRecoverable[reason] {
				reason = "provider_unavailable"
			}
			next = tvdbStamp(s.publicationTime().Add(max(delay, screenRecoveryCoolOff) + time.Duration(p.Base.Entity%31)*time.Second))
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,error=?,attempts=attempts+?,next_attempt=?,revision=revision+1,lease='',lease_until='' WHERE target_kind=? AND target_id=? AND lease=?`, status, reason, counted, next, p.Base.TargetKind, p.Base.Entity, p.Token)
	if err != nil {
		return err
	}
	return gated3.Commit()
}

// screenRecoveryCoolOff is the least a parked title waits before it is tried
// again: long enough that a provider which is down is asked about each title
// about once an hour at first, and far less often after that.
const screenRecoveryCoolOff = 15 * time.Minute

// screenRecoverable are the recorded reasons that can only mean the provider
// could not be reached or would not serve for now: a network failure, a rate
// limit or provider-side error, and a wait on the provider's own cooldown.
// claimScreen's recovery query lists the same three.
var screenRecoverable = map[string]bool{"unavailable": true, "provider_unavailable": true, "provider_backoff": true}

func screenProviderEnabled(b screenBase, provider string) bool {
	for _, p := range b.Providers {
		if p == provider {
			return true
		}
	}
	return false
}
func (s *Service) screenBeforeRequest(ctx context.Context, p *screenClaim, provider string) error {
	if !screenProviderEnabled(p.Base, provider) {
		return &metadataprovider.Error{Provider: provider, Code: "provider_disabled"}
	}
	gated4, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	ok, err := s.screenCurrent(ctx, tx, p)
	if err != nil {
		return err
	}
	if !ok {
		return errScreenStale
	}
	var until string
	err = tx.QueryRowContext(ctx, `SELECT next_attempt FROM metadata_provider_cooldowns WHERE provider=?`, provider).Scan(&until)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if until > tvdbStamp(s.publicationTime()) {
		t, _ := time.Parse(time.RFC3339, until)
		return &metadataprovider.Error{Provider: provider, Status: 429, Code: "provider_backoff", RetryAfter: t.Sub(s.publicationTime())}
	}
	return nil
}
func (s *Service) screenRequestContext(parent context.Context, p *screenClaim) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	// Revocation/configuration and superseding owner selections cancel in-flight
	// I/O, not merely publication. All source observations are rechecked at commit.
	supervise.Go("metadata.screen-worker.watch", func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				var current bool
				err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM screen_metadata_work w JOIN screen_metadata_policies p ON p.library_id=w.library_id CROSS JOIN screen_metadata_consent c LEFT JOIN library_configuration lc ON lc.library_id=w.library_id WHERE w.target_kind=? AND w.target_id=? AND w.lease=? AND w.generation=? AND w.selection_revision=? AND p.revision=? AND p.enabled=1 AND c.singleton=1 AND c.confirmed=1 AND c.revision=? AND COALESCE(lc.revision,0)=?)`, p.Base.TargetKind, p.Base.Entity, p.Token, p.Base.Generation, p.Base.Selection, p.Base.PolicyRevision, p.Base.ConsentRevision, p.Base.Configuration).Scan(&current)
				if err != nil || !current {
					cancel()
					return
				}
			}
		}
	})
	return ctx, cancel
}

// ScreenStep is the only movie/TV/anime name-matching writer scheduled by Run.
// Existing TVDB episode acquisition/projection and its durable pages are reused.
func (s *Service) ScreenStep(ctx context.Context) error {
	p, err := s.claimScreen(ctx)
	if err != nil || p == nil {
		return err
	}
	if p.Base.Status == "pending_children" {
		return s.settleScreen(ctx, p, s.dispatchScreenChildren(ctx, p))
	}
	if p.Base.ItemKind == "episode" {
		return s.settleScreen(ctx, p, s.screenEpisodeStep(ctx, p))
	}
	requestCtx, cancel := s.screenRequestContext(ctx, p)
	defer cancel()
	cs, winner, status, err := s.acquireScreen(requestCtx, p)
	if err != nil {
		return s.failScreen(ctx, p, err)
	}
	return s.settleScreen(ctx, p, s.publishScreen(ctx, p, cs, winner, status))
}

// settleScreen makes a failure after the claim cost something. A step that
// returns an error has already rolled its transaction back, which leaves the
// row pending under its lease with no attempt counted and no error recorded;
// the worker loop discards step errors, so the row was re-claimed every time
// the lease expired, forever: a provider request and seconds of CPU every two
// minutes per stuck row, on a server with nothing to do. Routing the error
// through failScreen counts the attempt, backs off exponentially and parks the
// row as unavailable after five tries, and the cause is logged once per
// distinct message so an owner can see why.
func (s *Service) settleScreen(ctx context.Context, p *screenClaim, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	noteScreenFailure(p.Base.TargetKind, p.Base.TargetID, err)
	return s.failScreen(ctx, p, err)
}

var screenFailures = struct {
	mu   sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

func noteScreenFailure(kind, id string, err error) {
	message := err.Error()
	if len(message) > 300 {
		message = message[:300]
	}
	screenFailures.mu.Lock()
	defer screenFailures.mu.Unlock()
	if last, ok := screenFailures.seen[message]; ok && time.Since(last) < time.Hour {
		return
	}
	if len(screenFailures.seen) > 256 {
		screenFailures.seen = map[string]time.Time{}
	}
	screenFailures.seen[message] = time.Now()
	log.Printf("metadata: %s %s could not be published and will back off: %s", kind, id, message)
}
func (s *Service) acquireScreen(ctx context.Context, p *screenClaim) ([]screenCandidate, int, string, error) {
	ctx = s.lookupContext(ctx)
	b := p.Base
	names, year, ids := screenQueries(b)
	providers := append([]string{}, b.Providers...)
	if b.Requested != "" {
		providers = []string{b.Requested}
	}
	// An accepted identity is a refresh target, never another title search. The
	// explicitly requested search is review-only until the owner selects a result.
	if b.ProviderID != "" && b.Requested == "" {
		id := metadataprovider.ScreenID{Provider: b.Provider, Type: b.ProviderType, ID: b.ProviderID}
		if err := s.screenBeforeRequest(ctx, p, id.Provider); err != nil {
			return nil, -1, "", err
		}
		provider := s.screenProviders[id.Provider]
		if provider == nil {
			return nil, -1, "", &metadataprovider.Error{Provider: id.Provider, Code: "provider_not_configured"}
		}
		// Freshness, budget and conditional requests all live in screen_freshness.go. Inside
		// the document's window this returns the remembered record and asks nothing.
		r, err := s.screenRefreshRecord(ctx, p, id, provider)
		if err != nil {
			return nil, -1, "", err
		}
		if r.Identity != id {
			return nil, -1, "", &metadataprovider.Error{Provider: id.Provider, Code: "identity_mismatch"}
		}
		if b.TargetKind == "show" && b.Mode == "owner" {
			valid := false
			valid = screenOrderSupported(r, b.Order)
			if !valid {
				return nil, -1, "", &metadataprovider.Error{Provider: id.Provider, Code: "order_unavailable"}
			}
		}
		c := scoreScreenEvidence(r, names, year, true, b)
		c.SourceKind = "accepted_identity"
		c.Reasons = []string{"accepted_identity_preserved"}
		// Reusing an automatic identity does not turn a tentative guess into certainty.
		if b.Mode != "owner" && b.Mode != "legacy" && b.Accepted != "" {
			var reasons string
			if e := dbwork.QueryRow(ctx, s.db, `SELECT confidence,reasons FROM screen_metadata_publications WHERE id=?`, b.Accepted).Scan(&c.Confidence, &reasons); e != nil {
				return nil, -1, "", e
			}
			if e := json.Unmarshal([]byte(reasons), &c.Reasons); e != nil {
				return nil, -1, "", e
			}
		}
		return []screenCandidate{c}, 0, "matched", nil
	}
	candidates := []screenCandidate{}
	seen := map[metadataprovider.ScreenID]bool{}
	incomplete := false
	var lastError error
	for _, name := range providers {
		if !screenProviderEnabled(b, name) {
			return nil, -1, "", &metadataprovider.Error{Provider: name, Code: "provider_disabled"}
		}
		provider := s.screenProviders[name]
		if provider == nil {
			lastError = &metadataprovider.Error{Provider: name, Code: "provider_not_configured"}
			continue
		}
		typ := b.ItemKind
		if name == "anilist" {
			if b.LibraryKind != "anime" {
				continue
			}
			typ = "anime"
		}
		exacts := []metadataprovider.ScreenID{}
		exactSeen := map[metadataprovider.ScreenID]bool{}
		for _, id := range ids {
			if id.Provider == "imdb" {
				if resolver, ok := provider.(interface {
					ResolveScreenID(context.Context, metadataprovider.ScreenID, string, string, string) (metadataprovider.ScreenID, error)
				}); ok {
					if e := s.screenBeforeRequest(ctx, p, name); e != nil {
						return nil, -1, "", e
					}
					resolved, e := resolver.ResolveScreenID(ctx, id, typ, b.Language, b.Region)
					if e != nil {
						return nil, -1, "", e
					}
					id = resolved
				}
			}
			if id.Provider == name && id.Type == typ && !exactSeen[id] {
				exacts = append(exacts, id)
				exactSeen[id] = true
			}
		}
		// Two different typed IDs for the same provider are a local conflict, not an
		// excuse to choose whichever remote response happened to arrive first.
		for index, id := range exacts {
			if index >= 4 {
				break
			}
			if err := s.screenBeforeRequest(ctx, p, name); err != nil {
				return nil, -1, "", err
			}
			r, err := provider.ScreenDetails(ctx, typ, id.ID, b.Language, b.Region)
			if err != nil {
				lastError = err
				if e := s.screenRememberCooldown(ctx, name, err); e != nil {
					return nil, -1, "", e
				}
				incomplete = true
				continue
			}
			if r.Identity != id {
				return nil, -1, "", &metadataprovider.Error{Provider: name, Code: "identity_mismatch"}
			}
			c := scoreScreenEvidence(r, names, year, true, b)
			if len(exacts) > 1 {
				c.Contradiction = true
				c.Reasons = append(c.Reasons, "local_identifiers_conflict")
			}
			candidates = append(candidates, c)
			seen[r.Identity] = true
		}
		if len(exacts) == 0 {
			for qi, q := range names {
				if qi >= 3 {
					break
				}
				if err := s.screenBeforeRequest(ctx, p, name); err != nil {
					return nil, -1, "", err
				}
				rows, err := provider.SearchScreen(ctx, typ, q, 0, b.Language, b.Region)
				if err != nil {
					lastError = err
					if e := s.screenRememberCooldown(ctx, name, err); e != nil {
						return nil, -1, "", e
					}
					incomplete = true
					break
				}
				if len(rows) > 25 {
					return nil, -1, "", &metadataprovider.Error{Provider: name, Code: "invalid_search"}
				}
				for _, r := range rows {
					if r.Identity.Provider != name || r.Identity.Type != typ {
						continue
					}
					r = metadataprovider.BoundScreenRecord(r)
					if err = metadataprovider.ValidateScreenRecord(r); err != nil {
						continue
					}
					if !seen[r.Identity] {
						c := scoreScreenEvidence(r, names, year, false, b)
						candidates = append(candidates, c)
						seen[r.Identity] = true
					}
				}
			}
		}
		// Fetch details for the leading provider candidates. This verifies exact
		// identity/type, supplies aliases/crosswalks and exposes numbering choices.
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Confidence > candidates[j].Confidence })
		fetched := 0
		for i := range candidates {
			c := &candidates[i]
			if c.Record.Identity.Provider != name || c.SourceKind == "typed_identifier" {
				continue
			}
			if fetched >= 4 {
				break
			}
			fetched++
			if err := s.screenBeforeRequest(ctx, p, name); err != nil {
				return nil, -1, "", err
			}
			id := c.Record.Identity
			r, err := provider.ScreenDetails(ctx, id.Type, id.ID, b.Language, b.Region)
			if err != nil {
				lastError = err
				if e := s.screenRememberCooldown(ctx, name, err); e != nil {
					return nil, -1, "", e
				}
				incomplete = true
				c.Contradiction = true
				c.Reasons = append(c.Reasons, "details_unavailable")
				continue
			}
			if r.Identity != id {
				return nil, -1, "", &metadataprovider.Error{Provider: name, Code: "identity_mismatch"}
			}
			*c = scoreScreenEvidence(r, names, year, false, b)
			c.DetailsVerified = true
		}
		if screenLocalYearConflict(b) {
			for i := range candidates {
				if candidates[i].SourceKind != "typed_identifier" {
					candidates[i].Confidence = max(0, candidates[i].Confidence-.03)
				}
				candidates[i].Reasons = append(candidates[i].Reasons, "local_release_years_conflict")
			}
		}
		winner, _ := screenWinner(candidates)
		if winner >= 0 && !incomplete {
			break
		} // provider order is authoritative; fallback is not a popularity vote.
	}
	if len(candidates) > 75 {
		candidates = candidates[:75]
	}
	winner, status := screenWinner(candidates)
	if b.Requested != "" || incomplete {
		winner = -1
		if len(candidates) > 0 {
			status = "needs_selection"
		}
	}
	if len(candidates) == 0 && lastError != nil {
		return nil, -1, "", lastError
	}
	// Do not infer a movie from a serial anime record just because its title agrees.
	if winner >= 0 && b.ItemKind == "movie" && candidates[winner].Record.Identity.Provider == "anilist" && candidates[winner].Record.Format != "MOVIE" {
		candidates[winner].Contradiction = true
		candidates[winner].Reasons = append(candidates[winner].Reasons, "anime_format_conflicts")
		winner = -1
		status = "needs_selection"
	}
	for i := range candidates {
		if candidates[i].Reasons == nil {
			candidates[i].Reasons = []string{}
		}
	}
	return candidates, winner, status, nil
}

func (s *Service) dispatchScreenChildren(ctx context.Context, p *screenClaim) error {
	gated5, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	ok, err := s.screenCurrent(ctx, tx, p)
	if err != nil {
		return err
	}
	if !ok {
		if err = s.screenStale(ctx, tx, p); err != nil {
			return err
		}
		return gated5.Commit()
	}
	var cursor string
	if err = tx.QueryRowContext(ctx, `SELECT child_cursor FROM screen_metadata_work WHERE target_kind='show' AND target_id=?`, p.Base.Entity).Scan(&cursor); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT entity_id FROM catalog_episodes WHERE show_id=? AND entity_id>CAST(? AS INTEGER) ORDER BY entity_id LIMIT 64`, p.Base.Entity, cursor)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// An episode whose fetch is in flight is left to finish: its claim is
	// fenced by the show's publication it was claimed under, so a result for an
	// earlier one is discarded and fetched again when it publishes.
	for _, id := range ids {
		_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status='pending',generation=generation+1,revision=revision+1,lease='',lease_until='',next_attempt='',error='' WHERE target_kind='item' AND target_id=? AND selection_mode<>'owner' AND lease=''
 AND NOT (status='matched' AND force_refresh=0 AND EXISTS(
 SELECT 1 FROM screen_metadata_publications parent JOIN screen_metadata_publications episode ON episode.id=screen_metadata_work.accepted_publication
 JOIN metadata_document_freshness f ON f.provider=episode.provider AND f.kind='episode' AND f.resource=episode.provider_id||'@'||episode.language||':'||episode.region
 WHERE parent.id=screen_metadata_work.parent_publication AND parent.provider=? AND parent.provider_id=? AND parent.language=? AND parent.region=?
 AND episode.language=? AND episode.region=? AND f.checked_at>CASE WHEN EXISTS(
 SELECT 1 FROM catalog_item_details d WHERE d.entity_id=screen_metadata_work.target_id AND d.added_text>?) THEN ? ELSE ? END))`, id, p.Base.Provider, p.Base.ProviderID, p.Base.Language, p.Base.Region, p.Base.Language, p.Base.Region, tvdbStamp(s.publicationTime().Add(-recentlyAdded)), s.publicationTime().Add(-freshnessRecent).Unix(), s.publicationTime().Add(-freshnessEpisode).Unix())
		if err != nil {
			return err
		}
		cursor = strconv.FormatInt(id, 10)
	}
	status := "pending_children"
	if len(ids) < 64 {
		status = "matched"
	}
	_, err = tx.ExecContext(ctx, `UPDATE screen_metadata_work SET status=?,child_cursor=?,revision=revision+1,lease='',lease_until='' WHERE target_kind='show' AND target_id=? AND lease=?`, status, cursor, p.Base.Entity, p.Token)
	if err != nil {
		return err
	}
	return gated5.Commit()
}

// A production-only restriction on the pre-existing TVDB worker. Its low-level
// methods remain independently testable; Run never schedules legacy name search.
type screenTVDBContextKey struct{}

func (s *Service) screenTVDBRun(ctx context.Context) error {
	return s.TVDBStep(context.WithValue(ctx, screenTVDBContextKey{}, true))
}
func screenTVDBRestricted(ctx context.Context) bool {
	v, _ := ctx.Value(screenTVDBContextKey{}).(bool)
	return v
}
func screenTVDBFilter(ctx context.Context) string {
	if !screenTVDBRestricted(ctx) {
		return ""
	}
	return ` AND EXISTS(SELECT 1 FROM screen_metadata_work sw JOIN screen_metadata_policies sp ON sp.library_id=sw.library_id CROSS JOIN screen_metadata_consent sc WHERE sw.target_kind='show' AND sw.target_id=j.show_id AND sw.provider='tvdb' AND sw.provider_id=CAST(j.provider_id AS TEXT) AND sw.accepted_publication<>'' AND sw.status IN('matched','pending_children','delegated_tvdb') AND sc.singleton=1 AND sc.confirmed=1 AND sp.enabled=1 AND EXISTS(SELECT 1 FROM json_each(sp.providers) WHERE value='tvdb')) AND j.status<>'pending_search'`
}

// Used in durable receipts, never exposed as a provider's raw query or credential.
func screenQueryReceipt(b screenBase) string {
	names, year, ids := screenQueries(b)
	return screenDigest(struct {
		Names                                 []string
		Year                                  int
		IDs                                   []metadataprovider.ScreenID
		Provider, Language, Region, Algorithm string
	}{names, year, ids, b.Requested, b.Language, b.Region, screenAlgorithm})
}
func screenRaw(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *Service) screenRememberCooldown(ctx context.Context, provider string, problem error) error {
	var pe *metadataprovider.Error
	if !errors.As(problem, &pe) || pe.RetryAfter <= 0 {
		return nil
	}
	delay := min(pe.RetryAfter, 24*time.Hour)
	_, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO metadata_provider_cooldowns(provider,next_attempt) VALUES(?,?) ON CONFLICT(provider) DO UPDATE SET next_attempt=MAX(next_attempt,excluded.next_attempt)`, provider, tvdbStamp(s.publicationTime().Add(delay)))
	return err
}
