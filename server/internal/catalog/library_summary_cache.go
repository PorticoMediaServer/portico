package catalog

import (
	"container/list"
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

const (
	summaryCacheEntries    = 512
	summaryCacheBytes      = 8 << 20
	summaryCacheValueBytes = 64 << 10
	summaryCacheFlights    = 32
	summaryAlgorithm       = 1
)

// Only semantic inputs belong here. User/profile IDs and revisions, credentials,
// progress, sessions and process publication counters are deliberately absent.
// JSON encodes a typed canonical policy, not a delimiter-joined label fence.
type summaryPolicy struct {
	HasCeiling                        bool
	Ceiling                           int
	BlockUnrated, BlockRecordings     bool
	BlockedLabels, MemberDeniedLabels []string
	RatingTable                       string
	Rule                              int
}

func summaryPolicyKey(r identity.ContentRestrictions) string {
	p := summaryPolicy{BlockUnrated: r.BlockUnrated, BlockRecordings: r.BlockRecordings, RatingTable: identity.RatingTableVersion, Rule: 1}
	if r.MaximumAge != nil {
		p.HasCeiling = true
		p.Ceiling = *r.MaximumAge
	}
	canonical := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, v := range values {
			out = append(out, strings.ToLower(v))
		}
		sort.Strings(out)
		return compactStrings(out)
	}
	p.BlockedLabels = canonical(r.BlockedLabels)
	p.MemberDeniedLabels = canonical(r.MemberDeniedLabels)
	raw, _ := json.Marshal(p)
	return string(raw)
}
func compactStrings(v []string) []string {
	if len(v) == 0 {
		return v
	}
	n := 1
	for _, s := range v[1:] {
		if s != v[n-1] {
			v[n] = s
			n++
		}
	}
	return v[:n]
}

type summaryDomain struct {
	Version int64
	Slot    int
	Present bool
}
type summaryReading struct {
	Library, Namespace, Input, Ready int64
	Rule                             int
	Kind                             string
	Mobi, CB7, CBR                   bool
}
type summaryKey struct {
	DB                                                  *sql.DB
	Namespace                                           *serviceState
	Library                                             string
	Handle                                              int64
	Kind, Policy                                        string
	Algorithm, Cap                                      int
	Kinds                                               [3]string
	Revision, OrderRevision, Generation, Classification int64
	PolicyEpoch                                         int64
	Domains                                             [100]summaryDomain
	Reading                                             summaryReading
}
type librarySummaryCore struct {
	Counts, PublicationCounts []LibraryKindCount
	Targets                   []artworkTarget
	Empty, EmptyKnown         bool
}

func cloneSummaryCounts(v []LibraryKindCount) []LibraryKindCount {
	if v == nil {
		return nil
	}
	return append([]LibraryKindCount{}, v...)
}
func (v librarySummaryCore) clone() librarySummaryCore {
	v.Counts = cloneSummaryCounts(v.Counts)
	v.PublicationCounts = cloneSummaryCounts(v.PublicationCounts)
	v.Targets = append([]artworkTarget(nil), v.Targets...)
	return v
}
func (v librarySummaryCore) size() int {
	n := int(unsafe.Sizeof(v)) + cap(v.Counts)*int(unsafe.Sizeof(LibraryKindCount{})) + cap(v.PublicationCounts)*int(unsafe.Sizeof(LibraryKindCount{})) + cap(v.Targets)*int(unsafe.Sizeof(artworkTarget{}))
	for _, c := range v.Counts {
		n += len(c.Kind)
	}
	for _, c := range v.PublicationCounts {
		n += len(c.Kind)
	}
	for _, t := range v.Targets {
		n += len(t.Kind) + len(t.ID)
	}
	return n
}
func (k summaryKey) size() int {
	return 2*int(unsafe.Sizeof(k)) + len(k.Library) + len(k.Kind) + len(k.Policy) + len(k.Reading.Kind) + 256
}

type summaryEntry struct {
	key   summaryKey
	core  librarySummaryCore
	bytes int
}
type summaryFlight struct {
	done    chan struct{}
	core    librarySummaryCore
	ok      bool
	waiters int
}

// All work and leases belong to admitted caller snapshots. This cache starts no
// goroutines, has no timer, and owns no database reader or shutdown work. It dies
// with its Service; WithContext copies share that lifetime. Failed/cancelled
// leaders publish no error/value, allowing a surviving waiter to retry in its
// OWN snapshot. No mutex is held over SQL, copying results or waiting.
type librarySummaryCache struct {
	mu      sync.Mutex
	entries map[summaryKey]*list.Element
	lru     list.List
	bytes   int
	flights map[summaryKey]*summaryFlight
}

func (c *librarySummaryCache) get(ctx context.Context, key summaryKey, compute func() (librarySummaryCore, error)) (librarySummaryCore, error) {
	for {
		if err := ctx.Err(); err != nil {
			telemetry.CountLibrarySummaryCache(telemetry.SummaryBypassCancelled)
			return librarySummaryCore{}, err
		}
		c.mu.Lock()
		if e := c.entries[key]; e != nil {
			c.lru.MoveToFront(e)
			core := e.Value.(summaryEntry).core
			c.mu.Unlock()
			telemetry.CountLibrarySummaryCache(telemetry.SummaryHitEntry)
			return core.clone(), nil
		}
		if f := c.flights[key]; f != nil {
			f.waiters++
			c.mu.Unlock()
			err := c.wait(ctx, f)
			if err != nil {
				telemetry.CountLibrarySummaryCache(telemetry.SummaryBypassCancelled)
				return librarySummaryCore{}, err
			}
			if f.ok {
				telemetry.CountLibrarySummaryCache(telemetry.SummaryHitFlight)
				return f.core.clone(), nil
			}
			continue
		}

		// Account keys too, so unusually large effective policies cannot make the
		// flight map or LRU unbounded. Bypass never changes the original answer.
		reason := telemetry.SummaryMissCold
		if key.size() > summaryCacheValueBytes {
			reason = telemetry.SummaryBypassKeySize
		} else if len(c.flights) >= summaryCacheFlights {
			reason = telemetry.SummaryBypassFlightLimit
		}
		if reason != telemetry.SummaryMissCold {
			c.mu.Unlock()
			telemetry.CountLibrarySummaryCache(reason)
			return compute()
		}
		if c.flights == nil {
			c.flights = make(map[summaryKey]*summaryFlight)
		}
		f := &summaryFlight{done: make(chan struct{})}
		c.flights[key] = f
		c.mu.Unlock()
		telemetry.CountLibrarySummaryCache(telemetry.SummaryMissCold)
		return c.build(ctx, key, f, compute)
	}
}

func (c *librarySummaryCache) wait(ctx context.Context, f *summaryFlight) error {
	defer func() { c.mu.Lock(); f.waiters--; c.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return ctx.Err()
	}
}

func (c *librarySummaryCache) build(ctx context.Context, key summaryKey, f *summaryFlight, compute func() (librarySummaryCore, error)) (librarySummaryCore, error) {
	// Always release the flight, even if a request-level recovery catches a panic.
	defer func() { c.mu.Lock(); delete(c.flights, key); close(f.done); c.mu.Unlock() }()
	core, err := compute()
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return librarySummaryCore{}, err
	}
	// Freeze before publication; only immutable slices are shared with waiters.
	core = core.clone()
	valueSize := core.size()
	size := valueSize + key.size()
	result := core.clone()
	c.mu.Lock()
	defer c.mu.Unlock()
	if valueSize > summaryCacheValueBytes || size > summaryCacheBytes {
		telemetry.CountLibrarySummaryCache(telemetry.SummaryBypassValueSize)
		return result, nil
	}
	f.core = core
	f.ok = true
	if c.entries == nil {
		c.entries = make(map[summaryKey]*list.Element)
	}
	for c.lru.Len() >= summaryCacheEntries || c.bytes+size > summaryCacheBytes {
		last := c.lru.Back()
		entry := last.Value.(summaryEntry)
		delete(c.entries, entry.key)
		c.bytes -= entry.bytes
		c.lru.Remove(last)
	}
	c.entries[key] = c.lru.PushFront(summaryEntry{key, core, size})
	c.bytes += size
	return result, nil
}

func (s *Service) reusableLibrarySummaryCore(ctx context.Context, viewer Viewer, library Library) (librarySummaryCore, error) {
	compute := func() (librarySummaryCore, error) { return s.computeLibrarySummaryCore(ctx, viewer, library) }
	key, reason := s.librarySummaryKey(ctx, viewer, library)
	if reason != "" {
		telemetry.CountLibrarySummaryCache(reason)
		return compute()
	}
	if isReadingLibrary(library.Kind) {
		r := &readingBrowseScope{library: key.Reading.Library, kind: key.Reading.Kind, mobi: key.Reading.Mobi, generation: compactcatalog.ReadingGeneration{Namespace: key.Reading.Namespace, Input: key.Reading.Input, Ready: key.Reading.Ready}}
		compute = func() (librarySummaryCore, error) {
			return s.computeLibrarySummaryCoreWithReading(ctx, viewer, library, r)
		}
	}
	return s.state.summaries.get(ctx, key, compute)
}

func (s *Service) librarySummaryKey(ctx context.Context, viewer Viewer, library Library) (summaryKey, telemetry.SummaryCacheEvent) {
	key := summaryKey{DB: s.db, Namespace: s.state, Library: library.ID, Kind: library.Kind, Algorithm: summaryAlgorithm, Cap: librarySummaryCap, Policy: summaryPolicyKey(viewer.Restrictions)}
	copy(key.Kinds[:], summaryKinds(library.Kind))
	if dbwork.Snapshot(ctx) == nil {
		return key, telemetry.SummaryBypassSnapshot
	}
	// Author memberships resolve through the creation-root identity ledger and
	// can consume another library's item attributes after root move/reuse. This
	// library's revisions do not prove those inputs current, even after dirty
	// work drains. Keep the original same-snapshot computation for every active
	// audiobook policy until an exact membership dependency fence is proved.
	if library.Kind == "audiobook" && viewer.Restrictions.Active() {
		return key, telemetry.SummaryBypassRestrictedMembership
	}
	var retaining bool
	// All eligibility facts and tokens are read through the same serving reader
	// and admitted snapshot as candidates. No after-commit process epoch/TTL.
	err := s.read().QueryRowContext(ctx, `SELECT c.id,r.revision,r.order_revision,g.generation,g.retaining,v.revision FROM catalog_libraries c JOIN library_revisions r ON r.library_id=c.library_id CROSS JOIN catalog_generations g CROSS JOIN catalog_summary_classification_revision v WHERE c.library_id=? AND c.retired=0 AND g.singleton=1 AND v.singleton=1`, library.ID).Scan(&key.Handle, &key.Revision, &key.OrderRevision, &key.Generation, &retaining, &key.Classification)
	if err != nil || key.Revision < 1 || key.OrderRevision < 1 || key.Classification < 1 {
		return key, telemetry.SummaryBypassIdentity
	}
	if retaining {
		return key, telemetry.SummaryBypassRetaining
	}
	if viewer.Restrictions.Active() {
		// Cross-library members can change this library's visibility after all
		// work drains. Bind the existing authority epoch, not content churn.
		if err = s.read().QueryRowContext(ctx, `SELECT policy_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&key.PolicyEpoch); err != nil || key.PolicyEpoch < 1 {
			return key, telemetry.SummaryBypassIdentity
		}
		var dirty, failed bool
		err = s.read().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_dirty),EXISTS(SELECT 1 FROM catalog_derivation_failures)`).Scan(&dirty, &failed)
		if err != nil {
			return key, telemetry.SummaryBypassIdentity
		}
		if dirty {
			return key, telemetry.SummaryBypassDirty
		}
		if failed {
			return key, telemetry.SummaryBypassFailure
		}
	}
	// Only the domains the cached core actually reads belong in the key. A
	// non-restricted policy compiles the visibility clause to the literal "1",
	// so candidate counts read synchronous facts and the static kind table and
	// no derived row at all. An active policy additionally reads browse row
	// existence (domain 18) and membership edges (domain 19). The pending-work
	// probe above stays global: membership edges carry no revision wake, so no
	// per-library pending-work fence is proved yet. Domains outside this set are
	// explicitly not inputs; their rebuilds must not discard a correct entry.
	if viewer.Restrictions.Active() {
		rows, err := s.read().QueryContext(ctx, `SELECT domain,version,active_slot,rebuilding,cleaning,first_build FROM catalog_derivations WHERE domain IN(?,?) ORDER BY domain`, compactcatalog.DomainBrowseRows, compactcatalog.DomainBrowseEdges)
		if err != nil {
			return key, telemetry.SummaryBypassIdentity
		}
		reason := telemetry.SummaryCacheEvent("")
		for rows.Next() {
			var domain, slot int
			var version int64
			var rebuild, clean, first bool
			if err = rows.Scan(&domain, &version, &slot, &rebuild, &clean, &first); err != nil {
				break
			}
			if domain < 1 || domain >= len(key.Domains) || version < 1 {
				reason = telemetry.SummaryBypassIdentity
				break
			}
			key.Domains[domain] = summaryDomain{version, slot, true}
			if rebuild {
				reason = telemetry.SummaryBypassRebuild
			}
			if clean {
				reason = telemetry.SummaryBypassCleanup
			}
			if first {
				reason = telemetry.SummaryBypassFirstBuild
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return key, telemetry.SummaryBypassIdentity
		}
		if reason != "" {
			return key, reason
		}
	}
	if isReadingLibrary(library.Kind) {
		r, e := s.readingBrowseScope(library.ID)
		if e != nil || r == nil {
			return key, telemetry.SummaryBypassReading
		}
		key.Reading = summaryReading{Library: r.library, Namespace: r.generation.Namespace, Input: r.generation.Input, Ready: r.generation.Ready, Kind: r.kind, Rule: compactcatalog.ReadingProjectionRuleVersion, Mobi: r.mobi, CB7: readingcap.CB7(), CBR: readingcap.CBR()}
		// Authorization-derived scope is precisely this allowed library + effective
		// restrictions. Profile/Fence only authorize the request; neither changes
		// membership/eligibleSQL. Never include a token or viewer revision.
	}
	return key, ""
}
