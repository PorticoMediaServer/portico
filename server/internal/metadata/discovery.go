package metadata

// Provider Trending Now. This is the background refresh of documented provider
// trend feeds into the shared metadata_discovery_items table: TMDB's daily
// movie/TV trending collection and AniList's trend-sorted Page query. It runs
// only inside the metadata worker, never inside an interactive request, and it
// sends no viewer history or library inventory anywhere — a feed request is
// the provider's public list and nothing else.
//
// Guarantees, per the discovery plan and PC-METADATA:
//   - global provider consent and the per-library provider policy are checked
//     before any network I/O and re-checked before a snapshot commits;
//   - every fetch reuses the shared paced transports, so trending requests
//     share the one pacing clock, in-flight gate and Retry-After deferral that
//     item matching uses;
//   - snapshots are stored atomically: a reader sees the previous snapshot or
//     the new one, and a failure keeps the last good one until its 48-hour
//     expiry;
//   - refresh scheduling is bounded backoff in a provider-owned table, so a
//     server with nothing due never writes, never wakes itself, and a rate
//     limited provider is honoured through the shared cooldown ledger.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// discoveryRowBudget is the most provider ids one snapshot may hold per
// (provider, media_kind). It bounds both the persisted feed and the over-fetch
// the catalog needs to find locally available entries near the top.
const discoveryRowBudget = 40

// trendingSource is one provider's trend fetch. Production wires the same
// paced adapters the screen matcher uses; tests substitute a bounded fake.
type trendingSource interface {
	Trending(ctx context.Context, language string, pages int) ([]metadataprovider.TrendEntry, error)
}

type discoveryFeed struct {
	Provider  string
	MediaKind string
	Language  string
}

// discoveryConsent reports whether the owner's provider disclosure has been
// confirmed. No confirmation, no provider request and no cached trends.
func (s *Service) discoveryConsent(ctx context.Context) (bool, error) {
	var confirmed int
	err := dbwork.QueryRow(ctx, s.db, `SELECT confirmed FROM screen_metadata_consent WHERE singleton=1`).Scan(&confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return confirmed == 1, nil
}

// discoveryFeeds builds the feeds worth refreshing from each library's own
// provider list. A library of kind movie/tv contributes a TMDB feed only when
// its own policy names tmdb; an anime library contributes TMDB's feeds when its
// policy names tmdb and the AniList feed when it names anilist. TVDB, MusicBrainz and anything else in a
// provider list have no documented trend endpoint, so they contribute nothing
// — a feed is never inferred from the library kind alone, and a library that
// enabled TMDB but disabled AniList never sends an AniList request. Library
// language is the metadata language policy, not library inventory, and is the
// only per-library value a feed request carries.
func (s *Service) discoveryFeeds(ctx context.Context) ([]discoveryFeed, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.kind,p.providers,p.language FROM screen_metadata_policies p
 JOIN libraries l ON l.id=p.library_id
 WHERE p.enabled=1 AND l.kind IN('movie','tv','anime')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]discoveryFeed{}
	for rows.Next() {
		var kind, providersJSON, language string
		if err = rows.Scan(&kind, &providersJSON, &language); err != nil {
			rows.Close()
			return nil, err
		}
		var providers []string
		if err = json.Unmarshal([]byte(providersJSON), &providers); err != nil {
			continue // an unparsable policy is a broken owner row, not an AniList request
		}
		for _, provider := range providers {
			// An anime library on TMDB (its default) holds TMDB shows and films,
			// so TMDB's own two feeds are the ones its titles can appear in.
			mediaKinds := map[string][]string{"movie/tmdb": {"movie"}, "tv/tmdb": {"tv"}, "anime/anilist": {"anime"}, "anime/tmdb": {"movie", "tv"}}[kind+"/"+provider]
			for _, mediaKind := range mediaKinds {
				key := provider + "/" + mediaKind
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = discoveryFeed{Provider: provider, MediaKind: mediaKind, Language: language}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]discoveryFeed, 0, len(seen))
	for _, f := range seen {
		out = append(out, f)
	}
	// One deterministic pass order: a server with several enabled feeds visits
	// them the same way every time, so per-feed permission checks and logs are
	// reproducible.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Provider+"/"+out[i].MediaKind < out[j].Provider+"/"+out[j].MediaKind
	})
	return out, nil
}

// discoverySource resolves one feed's paced provider adapter, or nil when
// this build does not configure a trend source for the (provider, media_kind)
// pair.
func (s *Service) discoverySource(feed discoveryFeed) trendingSource {
	return s.trendingSources[feed.Provider+"/"+feed.MediaKind]
}

type tmdbTrendingSource struct {
	tmdb *metadataprovider.TMDB
	kind string
}

func (t tmdbTrendingSource) Trending(ctx context.Context, language string, pages int) ([]metadataprovider.TrendEntry, error) {
	return t.tmdb.Trending(ctx, t.kind, language, pages)
}

// discoveryProviderType maps a persisted discovery media_kind onto the entity
// type the provider's feed results carry. TMDB types its television entities
// "show" while the discovery contract persists kind "tv"; AniList types its
// works "anime". Trend rows must match this type exactly before they are
// cached, so a provider payload that names the wrong entity kind is dropped
// rather than stored as another kind's trend.
func discoveryProviderType(feed discoveryFeed) string {
	if kind, ok := metadataprovider.TrendingType(feed.MediaKind); ok {
		return kind
	}
	return ""
}

// discoveryPermission closes the owner consent and the feed's per-library
// provider policy over the transaction the caller is about to commit or read
// in. The snapshot store calls it inside its write gate; the pre-request
// check calls it inside a read snapshot. One rule, one observation scope.
func (s *Service) discoveryPermission(feed discoveryFeed) persistence.SnapshotPermission {
	return func(ctx context.Context, tx *sql.Tx, provider, mediaKind string) (bool, error) {
		if err := checkLookupContextTx(ctx, tx); err != nil {
			return false, err
		}
		online, _, err := onlineLookupPolicyTx(ctx, tx)
		if err != nil || !online {
			return false, err
		}
		var confirmed int
		if err := tx.QueryRowContext(ctx, `SELECT confirmed FROM screen_metadata_consent WHERE singleton=1`).Scan(&confirmed); err != nil {
			return false, err
		}
		if confirmed != 1 {
			return false, nil
		}
		var enabled int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM screen_metadata_policies p
 JOIN libraries l ON l.id=p.library_id WHERE p.enabled=1 AND l.kind=?
 AND EXISTS(SELECT 1 FROM json_each(p.providers) WHERE value=?))`, feed.MediaKind, provider).Scan(&enabled); err != nil {
			return false, err
		}
		return enabled == 1, nil
	}
}

// discoveryStillPermitted reads the same fence rule inside a read snapshot:
// immediately before a feed's request, with the exact provider/kind pair.
func (s *Service) discoveryStillPermitted(ctx context.Context, feed discoveryFeed) (bool, error) {
	gated, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	return s.discoveryPermission(feed)(ctx, gated.Tx(), feed.Provider, feed.MediaKind)
}

// DiscoveryStep is one bounded discovery pass. It never opens a network
// request unless a feed is actually due, and on a server with nothing due it
// reads a handful of rows and writes nothing.
func (s *Service) DiscoveryStep(ctx context.Context) error {
	ctx = s.lookupContext(ctx)
	confirmed, err := s.discoveryConsent(ctx)
	if err != nil {
		return err
	}
	if !confirmed {
		// Revoked consent must stop serving cached trends immediately. The
		// clear only runs while rows exist, so a revoked idle server performs
		// no writes at all.
		stored, err := persistence.DiscoverySnapshotStored(ctx, s.db)
		if err != nil {
			return err
		}
		if !stored {
			return nil
		}
		return persistence.ClearDiscoverySnapshots(ctx, s.db)
	}
	now := s.publicationTime()
	feeds, err := s.discoveryFeeds(ctx)
	if err != nil {
		return err
	}
	for _, feed := range feeds {
		// A feed that has never been scheduled is due by definition; the
		// refresh state owns everything else. This is two small reads per
		// enabled feed, nothing written when nothing is due.
		state, ok, err := persistence.LoadDiscoveryRefresh(ctx, s.db, feed.Provider, feed.MediaKind)
		if err != nil {
			return err
		}
		if ok && state.NextDue.After(now) {
			continue
		}
		if err = s.refreshDiscoveryFeed(ctx, persistence.DiscoveryEntry{Provider: feed.Provider, MediaKind: feed.MediaKind}, feed.Language); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) refreshDiscoveryFeed(ctx context.Context, feed persistence.DiscoveryEntry, language string) error {
	ctx = s.lookupContext(ctx)
	current := discoveryFeed{Provider: feed.Provider, MediaKind: feed.MediaKind, Language: language}
	// The permission gate is per feed and immediately before its request: a
	// consent or policy revocation during another feed's request must prevent
	// this feed's network I/O, not merely its publication.
	permitted, err := s.discoveryStillPermitted(ctx, current)
	if err != nil || !permitted {
		return err
	}
	state, ok, err := persistence.LoadDiscoveryRefresh(ctx, s.db, feed.Provider, feed.MediaKind)
	if err != nil {
		return err
	}
	attempts := 0
	if ok {
		attempts = state.Attempts
	}
	source := s.discoverySource(current)
	if source == nil {
		return persistence.NoteDiscoveryRefreshFailure(ctx, s.db, feed.Provider, feed.MediaKind, s.publicationTime(), attempts, 0, "provider_not_configured")
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	entries, fetchErr := source.Trending(requestCtx, language, metadataprovider.TrendingPages(discoveryRowBudget))
	if fetchErr != nil {
		return s.noteDiscoveryFailure(ctx, feed, attempts, fetchErr)
	}
	// Rank order is the provider's trend order, minus adult evidence and any
	// entry typed for the wrong entity kind or failing this server's identity
	// validation: one malformed feed row must not cost the snapshot, and no
	// unavailable external title is ever stored.
	providerType := discoveryProviderType(current)
	ids := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Adult || entry.Provider != feed.Provider || entry.Type != providerType {
			continue
		}
		if !metadataprovider.ValidScreenID(metadataprovider.ScreenID{Provider: entry.Provider, Type: entry.Type, ID: entry.ID}) || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		ids = append(ids, entry.ID)
	}
	if len(ids) == 0 {
		// A successful response that yields nothing usable is a malformed feed,
		// not an empty shelf: the last good snapshot stays.
		return s.noteDiscoveryFailure(ctx, feed, attempts, &metadataprovider.Error{Provider: feed.Provider, Code: "malformed"})
	}
	// The store itself re-checks permission inside its own write transaction,
	// so a revocation between this recheck and the commit publishes nothing.
	if err = persistence.StoreDiscoverySnapshot(ctx, s.db, feed.Provider, feed.MediaKind, ids, s.publicationTime(), s.discoveryPermission(current)); err != nil {
		if errors.Is(err, persistence.ErrDiscoveryFence) {
			return nil
		}
		return err
	}
	return nil
}

// noteDiscoveryFailure classifies one failed refresh: provider codes and
// Retry-After reach the bounded backoff and the shared cooldown ledger, local
// publication errors are counted without treating the provider as offline.
func (s *Service) noteDiscoveryFailure(ctx context.Context, feed persistence.DiscoveryEntry, attempts int, problem error) error {
	code := "provider_unavailable"
	var retryAfter time.Duration
	var pe *metadataprovider.Error
	if errors.As(problem, &pe) {
		if pe.Provider != "" {
			feed.Provider = pe.Provider
		}
		code = pe.Code
		retryAfter = pe.RetryAfter
		if !pe.Retryable() && pe.Code != "" {
			code = pe.Code
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return persistence.NoteDiscoveryRefreshFailure(ctx, s.db, feed.Provider, feed.MediaKind, s.publicationTime(), attempts, retryAfter, code)
}
