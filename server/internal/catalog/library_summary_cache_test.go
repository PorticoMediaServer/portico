package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func summaryRead(t *testing.T, s *Service, v Viewer, l Library) (LibrarySummary, int) {
	t.Helper()
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	got, err := s.SummarizeLibraries(ctx, v, []Library{l})
	if err != nil {
		t.Fatal(err)
	}
	scans := 0
	for _, q := range trace() {
		if strings.Contains(q, "WITH candidates AS") {
			scans++
		}
	}
	return got[l.ID], scans
}
func summaryOracle(t *testing.T, s *Service, v Viewer, l Library) LibrarySummary {
	t.Helper()
	var want LibrarySummary
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var e error
		want, e = s.WithContext(ctx).originalLibrarySummary(ctx, v, l)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return want
}
func summaryParity(t *testing.T, got, want LibrarySummary) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("DTO parity\ngot %s\nwant%s", a, b)
	}
}
func summaryKeyRead(t *testing.T, s *Service, v Viewer, l Library) summaryKey {
	t.Helper()
	var key summaryKey
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var reason telemetry.SummaryCacheEvent
		key, reason = s.WithContext(ctx).librarySummaryKey(ctx, v, l)
		if reason != "" {
			return fmt.Errorf("ineligible: %s", reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestLibrarySummaryCoreParityPoliciesAndLiveDecoration(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	lib := Library{ID: "a", Kind: "movie"}
	viewer := Viewer{Profile: "one", Fence: "first-session", Libraries: []string{"a"}}
	c.Exec(`UPDATE catalog_item_details SET poster_url='legacy',backdrop_url='legacy' WHERE entity_id=?`, names["m1"].ID)
	c.Drain()
	got, scans := summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal("cold scans", scans)
	}
	summaryParity(t, got, summaryOracle(t, s, viewer, lib))
	// Equal policy shares across actual different profile identities/fences/revs.
	viewer.Profile = "two"
	viewer.Fence = "rotated-token"
	viewer.Restrictions.ProfileID = "two"
	viewer.Restrictions.Revision = 932
	viewer.Restrictions.MemberRevision = 21
	before := telemetry.LibrarySummaryCacheCounters()
	hit, scans := summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal("equal policy scanned", scans)
	}
	summaryParity(t, hit, got)
	if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryHitEntry] != before[telemetry.SummaryHitEntry]+1 {
		t.Fatal("hit counter")
	}
	// Caller mutation cannot corrupt immutable cached counts or target lists.
	hit.Counts[0].Count = 90909
	hit.CoverURLs[0] = "mutated"
	again, scans := summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal(scans)
	}
	summaryParity(t, again, got)
	key := summaryKeyRead(t, s, viewer, lib)
	c.Exec(`UPDATE progress SET position=position+1 WHERE profile_id='p'`)
	c.Exec(`UPDATE personal_items SET revision=revision+1,watched=1-watched WHERE profile_id='p'`)
	viewer.Fence = "another-session"
	viewer.Restrictions.Revision++
	if key != summaryKeyRead(t, s, viewer, lib) {
		t.Fatal("progress/personal/session changed core key")
	}
	_, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal("progress invalidated core")
	}
	// Job lifecycle is live even with an identical core key.
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('summary-job','a','running','now')`)
	live, scans := summaryRead(t, s, viewer, lib)
	if scans != 0 || !live.FirstScan {
		t.Fatal("FirstScan stale", scans, live)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	c.Exec(`UPDATE jobs SET status='complete' WHERE id='summary-job'`)
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 || live.FirstScan {
		t.Fatal(scans, live)
	}
	// A selection published with its source is live decoration: the core key
	// does not move, and the cover appears without a rescan.
	digest := strings.Repeat("c", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/png',400,600,100,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('summary-art','item',?,'poster','','fixture','poster','local','fixture','fence','now')`, names["m2"].ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('item',?,'poster','','summary-art',?,?,0,1,'fixture','now'))`, names["m2"].ID, digest, digest)
	c.Drain()
	summaryRead(t, s, viewer, lib)
	key = summaryKeyRead(t, s, viewer, lib)
	if key != summaryKeyRead(t, s, viewer, lib) {
		t.Fatal("decoration changed core key")
	}
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	if !strings.Contains(strings.Join(live.CoverURLs, " "), digest) {
		t.Fatal("ready artwork not live", live)
	}
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	// Details deletion/insert have no UPDATE revision; fallback must stay live.
	c.Exec(`DELETE FROM catalog_item_details WHERE entity_id=?`, names["m1"].ID)
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	c.Exec(`INSERT INTO catalog_item_details(entity_id,poster_url) VALUES(?,'replacement')`, names["m1"].ID)
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	c.Drain()
	// Real policy changes never share. Typed sets cannot alias comma fences.
	age := 13
	viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true, BlockedLabels: []string{"X,Y"}}
	restricted, scans := summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, restricted, summaryOracle(t, s, viewer, lib))
	viewer.Profile = "third"
	viewer.Restrictions.BlockedLabels = []string{"x,y", "X,Y"}
	_, scans = summaryRead(t, s, viewer, lib)
	if scans != 0 {
		t.Fatal("canonical equivalent policy missed")
	}
	viewer.Restrictions.BlockedLabels = []string{"x", "y"}
	_, scans = summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal("ambiguous label key shared")
	}
	viewer.Libraries = nil
	result, err := s.SummarizeLibraries(context.Background(), viewer, []Library{lib})
	if err != nil || len(result) != 0 {
		t.Fatal("hidden library escaped", result, err)
	}
	// Candidate add/retire/order mutations remain revision-fenced.
	viewer.Restrictions = identity.ContentRestrictions{}
	viewer.Libraries = []string{"a"}
	c.Drain()
	key = summaryKeyRead(t, s, viewer, lib)
	c.Exec(`UPDATE catalog_browse_rows SET sort_key=sort_key||'z' WHERE entity_id=?`, names["m2"].ID)
	next := summaryKeyRead(t, s, viewer, lib)
	if key.Revision != next.Revision || key.OrderRevision == next.OrderRevision {
		t.Fatal("order_revision coverage", key.Revision, next.Revision, key.OrderRevision, next.OrderRevision)
	}
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	c.Exec(`UPDATE catalog_entities SET retired=1 WHERE id=?`, names["m2"].ID)
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
	c.Movie(c.Handle("a"), "/a/new.mkv", "New", 2026)
	live, scans = summaryRead(t, s, viewer, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, viewer, lib))
}

func TestLibrarySummaryCoreClassificationAndConservativeBypass(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	v := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{BlockUnrated: true}}
	lib := Library{ID: "a", Kind: "movie"}
	c.Attributes(names["m5"].ID, "contentRating", "SUMMARY-UNKNOWN")
	c.Drain()
	for _, step := range []struct {
		query string
		delta int
	}{{`INSERT INTO content_rating_ages VALUES('summary-unknown',7)`, 1}, {`UPDATE content_rating_ages SET minimum_age=99 WHERE value_key='summary-unknown'`, -1}, {`DELETE FROM content_rating_ages WHERE value_key='summary-unknown'`, 0}} {
		// With a ceiling, known-age edits and deletion have distinct semantics.
		age := 13
		v.Restrictions.MaximumAge = &age
		prior, _ := summaryRead(t, s, v, lib)
		priorKey := summaryKeyRead(t, s, v, lib)
		c.Exec(step.query)
		nextKey := summaryKeyRead(t, s, v, lib)
		// Rating publication advances the global classification epoch without
		// walking matching libraries. That epoch still invalidates the summary
		// synchronously, before its deferred visibility fan-out is processed.
		if nextKey.Revision != priorKey.Revision || priorKey.OrderRevision != nextKey.OrderRevision || nextKey.Classification != priorKey.Classification+1 {
			t.Fatalf("classification revisions lib %d -> %d order %d -> %d class %d -> %d", priorKey.Revision, nextKey.Revision, priorKey.OrderRevision, nextKey.OrderRevision, priorKey.Classification, nextKey.Classification)
		}
		got, scans := summaryRead(t, s, v, lib)
		if scans != 1 {
			t.Fatal("classification missed", scans)
		}
		summaryParity(t, got, summaryOracle(t, s, v, lib))
		if got.Counts[0].Count != prior.Counts[0].Count+step.delta {
			t.Fatal("classification answer", prior, got, step.delta)
		}
	}

	// Both old and new spellings occur on existing titles. Renaming one age row
	// swaps which title is rated, without changing the item/library/profile/order
	// tokens: existing UPDATE invalidation only watches minimum_age.
	c.Exec(`INSERT INTO content_rating_ages VALUES('summary-alias',7)`)
	c.Attributes(names["m3"].ID, "contentRating", "SUMMARY-ALIAS")
	c.Exec(`UPDATE catalog_item_details SET poster_url='legacy' WHERE entity_id IN(?,?)`, names["m3"].ID, names["m5"].ID)
	c.Drain()
	prior, _ := summaryRead(t, s, v, lib)
	priorKey := summaryKeyRead(t, s, v, lib)
	profileFence := RestrictionFence(v.Restrictions)
	var viewerRevision int64
	if err := db.QueryRow(`SELECT COALESCE((SELECT revision FROM viewer_revisions WHERE profile_id='p' AND library_id='a'),0)`).Scan(&viewerRevision); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE content_rating_ages SET value_key='summary-unknown' WHERE value_key='summary-alias'`)
	nextKey := summaryKeyRead(t, s, v, lib)
	var afterViewerRevision int64
	if err := db.QueryRow(`SELECT COALESCE((SELECT revision FROM viewer_revisions WHERE profile_id='p' AND library_id='a'),0)`).Scan(&afterViewerRevision); err != nil {
		t.Fatal(err)
	}
	if priorKey.Revision != nextKey.Revision || priorKey.OrderRevision != nextKey.OrderRevision || nextKey.Classification != priorKey.Classification+1 || viewerRevision != afterViewerRevision || profileFence != RestrictionFence(v.Restrictions) {
		t.Fatal("rename classification identity")
	}
	renamed, scans := summaryRead(t, s, v, lib)
	oldCover := "/v1/items/" + names["m3"].Public + "/art/poster"
	newCover := "/v1/items/" + names["m5"].Public + "/art/poster"
	if scans != 1 || renamed.Counts[0].Count != prior.Counts[0].Count || !strings.Contains(strings.Join(prior.CoverURLs, " "), oldCover) || strings.Contains(strings.Join(prior.CoverURLs, " "), newCover) || !strings.Contains(strings.Join(renamed.CoverURLs, " "), newCover) || strings.Contains(strings.Join(renamed.CoverURLs, " "), oldCover) {
		t.Fatal("old/new rating visibility stale", prior, renamed, scans)
	}
	summaryParity(t, renamed, summaryOracle(t, s, v, lib))

	// Every unsafe state bypasses BOTH hits and insertion, preserving original DTO.
	summaryRead(t, s, v, lib)
	checks := []struct {
		reason         telemetry.SummaryCacheEvent
		setup, cleanup string
	}{
		{telemetry.SummaryBypassIdentity, `DELETE FROM catalog_summary_classification_revision`, fmt.Sprintf(`INSERT INTO catalog_summary_classification_revision VALUES(1,%d)`, nextKey.Classification)},
		{telemetry.SummaryBypassRetaining, `UPDATE catalog_generations SET retaining=1`, `UPDATE catalog_generations SET retaining=0`},
		{telemetry.SummaryBypassDirty, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(19,999999,1)`, `DELETE FROM catalog_dirty WHERE domain=19 AND entity_id=999999`},
		{telemetry.SummaryBypassFailure, `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(19,999999,'test',1)`, `DELETE FROM catalog_derivation_failures WHERE domain=19 AND entity_id=999999`},
		{telemetry.SummaryBypassRebuild, `UPDATE catalog_derivations SET rebuilding=1 WHERE domain=19`, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain=19`},
		{telemetry.SummaryBypassCleanup, `UPDATE catalog_derivations SET cleaning=1 WHERE domain=19`, `UPDATE catalog_derivations SET cleaning=0 WHERE domain=19`},
		{telemetry.SummaryBypassFirstBuild, `UPDATE catalog_derivations SET first_build=1 WHERE domain=19`, `UPDATE catalog_derivations SET first_build=0 WHERE domain=19`},
	}
	for _, check := range checks {
		t.Run(string(check.reason), func(t *testing.T) {
			before := telemetry.LibrarySummaryCacheCounters()
			keyBefore := summaryKeyRead(t, s, v, lib)
			entriesBefore := len(s.state.summaries.entries)
			c.Exec(check.setup)
			for i := 0; i < 2; i++ {
				got, scans := summaryRead(t, s, v, lib)
				if scans != 1 {
					t.Fatal("unsafe state hit", scans)
				}
				summaryParity(t, got, summaryOracle(t, s, v, lib))
			}
			if telemetry.LibrarySummaryCacheCounters()[check.reason] != before[check.reason]+2 {
				t.Fatal("bypass counter", check.reason)
			}
			if len(s.state.summaries.entries) != entriesBefore {
				t.Fatal("unsafe state inserted entry")
			}
			c.Exec(check.cleanup)
			afterKey := summaryKeyRead(t, s, v, lib)
			_, scans := summaryRead(t, s, v, lib)
			want := 0
			if keyBefore != afterKey {
				want = 1
			}
			if scans != want {
				t.Fatal("bypass polluted stable entry", scans, want)
			}
			_, scans = summaryRead(t, s, v, lib)
			if scans != 0 {
				t.Fatal("stable result did not reuse")
			}

		})
	}
	// Exercise an actual retained overlay. Removing the adult title's raw self
	// membership would make it an empty visible container without serving rewrite.
	c.Exec(`UPDATE catalog_generations SET retaining=1`)
	c.Exec(`DELETE FROM catalog_browse_memberships WHERE entity_id=?`, names["m2"].ID)
	retained, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("retained overlay hit")
	}
	summaryParity(t, retained, summaryOracle(t, s, v, lib))
	var previous int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_browse_memberships_previous WHERE entity_id=?`, names["m2"].ID).Scan(&previous); err != nil || previous == 0 {
		t.Fatal("retained overlay not exercised", previous, err)
	}
	c.Exec(`UPDATE catalog_generations SET retaining=0`)
	c.Drain()

	// Dirty membership with unchanged raw revisions cannot reuse an old core.
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(19,999999,1)`)
	c.Exec(`DELETE FROM catalog_browse_memberships WHERE entity_id=?`, names["m1"].ID)
	live, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, live, summaryOracle(t, s, v, lib))
	// Rollback invalidation cannot evict/reuse under an uncommitted token.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`UPDATE catalog_generations SET retaining=1`)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	c.Exec(`DELETE FROM catalog_dirty WHERE domain=19 AND entity_id=999999`)
	// Unrestricted core consumes no membership; dirty membership does not gate it.
	v.Restrictions = identity.ContentRestrictions{}
	summaryRead(t, s, v, lib)
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(19,999999,1)`)
	_, scans = summaryRead(t, s, v, lib)
	if scans != 0 {
		t.Fatal("unrestricted membership dependency")
	}
}

func TestLibrarySummaryCoreReadingReadyAndPublicationInvalidation(t *testing.T) {
	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			c, s, v, items := readingBrowseFixture(t, kind)
			lib := Library{ID: "reading", Kind: kind}
			before := telemetry.LibrarySummaryCacheCounters()
			if partial, err := s.SummarizeLibraries(context.Background(), v, []Library{lib}); err != nil {
				t.Fatal(err)
			} else {
				assertReadingSummaryPreparing(t, partial[lib.ID], 4)
			}
			if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryBypassReading] != before[telemetry.SummaryBypassReading]+1 {
				t.Fatal("reading bypass counter")
			}
			c.Drain()
			cold, scans := summaryRead(t, s, v, lib)
			if scans != 1 {
				t.Fatal(scans)
			}
			summaryParity(t, cold, summaryOracle(t, s, v, lib))
			v.Profile = "other"
			v.Fence = "new-session"
			hit, scans := summaryRead(t, s, v, lib)
			if scans != 0 {
				t.Fatal(scans)
			}
			summaryParity(t, hit, cold)
			key := summaryKeyRead(t, s, v, lib)
			c.Exec(`UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=?`, key.Handle)
			if partial, err := s.SummarizeLibraries(context.Background(), v, []Library{lib}); err != nil {
				t.Fatal(err)
			} else {
				assertReadingSummaryPreparing(t, partial[lib.ID], 4)
			}
			c.Drain()
			next := summaryKeyRead(t, s, v, lib)
			if key.Reading.Input == next.Reading.Input || next.Reading.Input != next.Reading.Ready {
				t.Fatal("ready token unchanged")
			}
			live, scans := summaryRead(t, s, v, lib)
			if scans != 1 {
				t.Fatal(scans)
			}
			summaryParity(t, live, summaryOracle(t, s, v, lib))
			if kind == "book" {
				before := summaryKeyRead(t, s, v, lib)
				s.ConfigureReadingMobi(true)
				after := summaryKeyRead(t, s, v, lib)
				if before.Revision != after.Revision || before.Reading == after.Reading {
					t.Fatal("runtime capability missing from key")
				}
				live, scans = summaryRead(t, s, v, lib)
				if scans != 1 {
					t.Fatal("capability did not invalidate")
				}
				summaryParity(t, live, summaryOracle(t, s, v, lib))
			}
			// DB001's agreed semantic publication changes library + Reading input once;
			// representation-only flatten/cleanup must not touch either token.
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				if _, e := tx.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='reading'`); e != nil {
					return e
				}
				_, e := tx.Exec(`UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=?`, key.Handle)
				return e
			})
			if partial, err := s.SummarizeLibraries(context.Background(), v, []Library{lib}); err != nil {
				t.Fatal(err)
			} else {
				assertReadingSummaryPreparing(t, partial[lib.ID], 4)
			}
			c.Drain()
			live, scans = summaryRead(t, s, v, lib)
			if scans != 1 {
				t.Fatal(scans)
			}
			summaryParity(t, live, summaryOracle(t, s, v, lib))
			if kind == "book" {
				c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'publicationType','magazine','',1,'owner','now')`, items[0].ID)
				c.Drain()
				live, scans = summaryRead(t, s, v, lib)
				if scans != 1 {
					t.Fatal(scans)
				}
				summaryParity(t, live, summaryOracle(t, s, v, lib))
			}
		})
	}
}

func TestLibrarySummaryCoreFlightsCancellationAndBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	t.Run("successful-flight-reasons", func(t *testing.T) {
		var cache librarySummaryCache
		key := summaryKey{Library: "success"}
		started := make(chan struct{})
		release := make(chan struct{})
		results := make(chan error, 3)
		before := telemetry.LibrarySummaryCacheCounters()
		go func() {
			_, err := cache.get(context.Background(), key, func() (librarySummaryCore, error) {
				// Taking the mutex here would deadlock if compute ran under it.
				cache.mu.Lock()
				cache.mu.Unlock()
				close(started)
				<-release
				return librarySummaryCore{Counts: []LibraryKindCount{{Kind: "movie", Count: 1}}}, nil
			})
			results <- err
		}()
		<-started
		for i := 0; i < 2; i++ {
			go func() {
				core, err := cache.get(context.Background(), key, func() (librarySummaryCore, error) { return librarySummaryCore{}, errors.New("flight repeated") })
				if err == nil && core.Counts[0].Count != 1 {
					err = errors.New("flight result")
				}
				results <- err
			}()
		}
		waitSummaryWaiters(t, &cache, key, 2)
		close(release)
		for i := 0; i < 3; i++ {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		after := telemetry.LibrarySummaryCacheCounters()
		if after[telemetry.SummaryHitFlight] != before[telemetry.SummaryHitFlight]+2 || after[telemetry.SummaryMissCold] != before[telemetry.SummaryMissCold]+1 {
			t.Fatal("flight reason counters", before, after)
		}
	})
	t.Run("equal-key-one-SQL", func(t *testing.T) {
		_, s, _ := browseFixture(t)
		v := Viewer{Libraries: []string{"a"}}
		lib := Library{ID: "a", Kind: "movie"}
		key := summaryKeyRead(t, s, v, lib)
		var cache librarySummaryCache
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		var queries atomic.Int32
		start := make(chan struct{})
		release := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
					_, err := cache.get(ctx, key, func() (librarySummaryCore, error) {
						queries.Add(1)
						<-release
						return s.WithContext(ctx).computeLibrarySummaryCore(ctx, v, lib)
					})
					return err
				})
				errs <- err
			}()
		}
		close(start)
		deadline := time.Now().Add(2 * time.Second)
		for queries.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		close(release)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if queries.Load() != 1 {
			t.Fatal("equal key repeated SQL", queries.Load())
		}
		scans := 0
		for _, q := range trace() {
			if strings.Contains(q, "WITH candidates AS") {
				scans++
			}
		}
		if scans != 1 {
			t.Fatal("actual equal-key SQL count", scans)
		}
	})
	t.Run("cancelled-leader-surviving-waiter", func(t *testing.T) {
		var cache librarySummaryCache
		key := summaryKey{Library: "cancel"}
		leaderCtx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		leaderDone := make(chan error, 1)
		waiterDone := make(chan error, 1)
		go func() {
			_, e := cache.get(leaderCtx, key, func() (librarySummaryCore, error) {
				close(started)
				<-leaderCtx.Done()
				return librarySummaryCore{}, leaderCtx.Err()
			})
			leaderDone <- e
		}()
		<-started
		go func() {
			core, e := cache.get(context.Background(), key, func() (librarySummaryCore, error) {
				return librarySummaryCore{Counts: []LibraryKindCount{{Kind: "movie", Count: 7}}}, nil
			})
			if e == nil && core.Counts[0].Count != 7 {
				e = errors.New("bad retry")
			}
			waiterDone <- e
		}()
		cancel()
		if !errors.Is(<-leaderDone, context.Canceled) {
			t.Fatal("leader cancellation")
		}
		select {
		case e := <-waiterDone:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter poisoned")
		}
		if len(cache.flights) != 0 {
			t.Fatal("flight leaked")
		}
	})
	t.Run("cancelled-waiter-error-not-cached", func(t *testing.T) {
		var cache librarySummaryCache
		key := summaryKey{Library: "waiter"}
		started := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, e := cache.get(context.Background(), key, func() (librarySummaryCore, error) {
				close(started)
				<-release
				return librarySummaryCore{}, errors.New("not-ready")
			})
			done <- e
		}()
		<-started
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, e := cache.get(ctx, key, func() (librarySummaryCore, error) {
			t.Error("cancelled waiter computed")
			return librarySummaryCore{}, nil
		})
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		close(release)
		if <-done == nil {
			t.Fatal("leader error lost")
		}
		if len(cache.entries) != 0 || len(cache.flights) != 0 {
			t.Fatal("error cached/flight leaked")
		}
	})
	t.Run("entry-byte-value-key-flight-bounds", func(t *testing.T) {
		core := librarySummaryCore{Counts: []LibraryKindCount{{Kind: "movie", Count: 1}}, Targets: []artworkTarget{{"item", "immutable"}}}
		var cache librarySummaryCache
		compute := func() (librarySummaryCore, error) { return core, nil }
		for i := 0; i < summaryCacheEntries+30; i++ {
			key := summaryKey{Library: fmt.Sprint(i)}
			v, e := cache.get(context.Background(), key, compute)
			if e != nil {
				t.Fatal(e)
			}
			v.Targets[0].ID = "changed"
		}
		if len(cache.entries) != summaryCacheEntries || cache.bytes > summaryCacheBytes {
			t.Fatal("entry bound", len(cache.entries), cache.bytes)
		}
		for _, e := range cache.entries {
			if e.Value.(summaryEntry).core.Targets[0].ID != "immutable" {
				t.Fatal("shared mutable target")
			}
		}
		for i := 0; i < 200; i++ {
			cache.get(context.Background(), summaryKey{Library: fmt.Sprint(i), Policy: strings.Repeat("x", summaryCacheValueBytes-(summaryKey{}).size()-256)}, compute)
		}
		if cache.bytes > summaryCacheBytes || len(cache.entries) >= summaryCacheEntries {
			t.Fatal("byte eviction", cache.bytes, len(cache.entries))
		}
		before := telemetry.LibrarySummaryCacheCounters()
		cache.get(context.Background(), summaryKey{Policy: strings.Repeat("x", summaryCacheValueBytes+1)}, compute)
		cache.get(context.Background(), summaryKey{Library: "huge-value"}, func() (librarySummaryCore, error) {
			return librarySummaryCore{Targets: []artworkTarget{{ID: strings.Repeat("y", summaryCacheValueBytes)}}}, nil
		})
		if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryBypassKeySize] != before[telemetry.SummaryBypassKeySize]+1 || telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryBypassValueSize] != before[telemetry.SummaryBypassValueSize]+1 {
			t.Fatal("size reason counters")
		}
		cache.flights = make(map[summaryKey]*summaryFlight)
		for i := 0; i < summaryCacheFlights; i++ {
			cache.flights[summaryKey{Library: fmt.Sprint(i)}] = &summaryFlight{done: make(chan struct{})}
		}
		before = telemetry.LibrarySummaryCacheCounters()
		cache.get(context.Background(), summaryKey{Library: "overflow-flight"}, compute)
		if len(cache.flights) != summaryCacheFlights || telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryBypassFlightLimit] != before[telemetry.SummaryBypassFlightLimit]+1 {
			t.Fatal("flight bound")
		}
	})
	// Distinguish null ceiling and zero, label vs member tag policy, and namespaces.
	z := 0
	if summaryPolicyKey(identity.ContentRestrictions{}) == summaryPolicyKey(identity.ContentRestrictions{MaximumAge: &z}) {
		t.Fatal("nil/zero ceiling alias")
	}
	if summaryPolicyKey(identity.ContentRestrictions{BlockedLabels: []string{"x"}}) == summaryPolicyKey(identity.ContentRestrictions{MemberDeniedLabels: []string{"x"}}) {
		t.Fatal("label/tag alias")
	}
	if (summaryKey{Namespace: &serviceState{}} == summaryKey{Namespace: &serviceState{}}) {
		t.Fatal("service namespace alias")
	}
}

func waitSummaryWaiters(t *testing.T, c *librarySummaryCache, key summaryKey, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		f := c.flights[key]
		ready := f != nil && f.waiters >= n
		c.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("waiter did not join flight")
}

func TestLibrarySummaryCoreCancellationReleasesRequestSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, _ := browseFixture(t)
	v := Viewer{Libraries: []string{"a"}}
	lib := Library{ID: "a", Kind: "movie"}
	key := summaryKeyRead(t, s, v, lib)
	baseline := s.db.Stats().InUse
	var cache librarySummaryCache
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()
	started := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		leaderDone <- dbwork.WithReadSnapshot(leaderCtx, s.db, func(ctx context.Context) error {
			_, err := cache.get(ctx, key, func() (librarySummaryCore, error) {
				close(started)
				<-ctx.Done()
				return librarySummaryCore{}, ctx.Err()
			})
			return err
		})
	}()
	<-started
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- dbwork.WithReadSnapshot(waiterCtx, s.db, func(ctx context.Context) error {
			_, err := cache.get(ctx, key, func() (librarySummaryCore, error) {
				return librarySummaryCore{}, errors.New("cancelled waiter became leader")
			})
			return err
		})
	}()
	waitSummaryWaiters(t, &cache, key, 1)
	before := telemetry.LibrarySummaryCacheCounters()
	cancelWaiter()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter pinned snapshot")
	}
	if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryBypassCancelled] != before[telemetry.SummaryBypassCancelled]+1 {
		t.Fatal("waiter cancellation reason")
	}
	survivorDone := make(chan error, 1)
	go func() {
		survivorDone <- dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
			_, err := cache.get(ctx, key, func() (librarySummaryCore, error) { return s.WithContext(ctx).computeLibrarySummaryCore(ctx, v, lib) })
			return err
		})
	}()
	waitSummaryWaiters(t, &cache, key, 1)
	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case err := <-survivorDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled leader poisoned survivor")
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.db.Stats().InUse != baseline && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.db.Stats().InUse != baseline || len(cache.flights) != 0 {
		t.Fatal("reader/flight leaked", s.db.Stats(), len(cache.flights))
	}
	// A recovered panic also releases the flight rather than stranding waiters.
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic missing")
			}
		}()
		cache.get(context.Background(), summaryKey{Library: "panic"}, func() (librarySummaryCore, error) { panic("request recovery") })
	}()
	if len(cache.flights) != 0 {
		t.Fatal("panic stranded flight")
	}
}

func TestLibrarySummaryCoreAllKindsAndCappedCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	s := New(c.DB)
	// A title-only unit setup, not a scale fixture/load run. The single movie
	// window exercises the exact 20,001 sentinel and 64-target artwork boundary.
	for _, kind := range []string{"movie", "tv", "anime", "music", "audiobook"} {
		handle := c.Library(kind, kind, kind, "/"+kind)
		v := Viewer{Libraries: []string{kind}}
		lib := Library{ID: kind, Kind: kind}
		empty, scans := summaryRead(t, s, v, lib)
		if scans != 1 {
			t.Fatal(scans)
		}
		summaryParity(t, empty, summaryOracle(t, s, v, lib))
		if !empty.Empty || !empty.EmptyKnown {
			t.Fatal(empty)
		}
		for i, k := range summaryKinds(kind) {
			code, err := compactcatalog.ParseKind(k)
			if err != nil {
				t.Fatal(err)
			}
			c.Entity(compactcatalog.Entity{Library: handle, Kind: code, Key: fmt.Sprint(i), Title: k}, nil)
		}
		got, scans := summaryRead(t, s, v, lib)
		if scans != 1 {
			t.Fatal(scans)
		}
		summaryParity(t, got, summaryOracle(t, s, v, lib))
		_, scans = summaryRead(t, s, v, lib)
		if scans != 0 {
			t.Fatal(scans)
		}
	}
	// Insert only synchronous candidate facts. Do not drain the unrelated derived
	// queue: unrestricted summary deliberately does not depend on it.
	handle := c.Handle("movie")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`WITH RECURSIVE seq(n) AS(VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<20001) INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head,added_ms) SELECT randomblob(16),?,1,'Movie '||n,'movie '||n,'M',n FROM seq`, handle)
		return err
	})
	v := Viewer{Libraries: []string{"movie"}}
	lib := Library{ID: "movie", Kind: "movie"}
	got, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
	if len(got.Counts) != 1 || got.Counts[0].Count != librarySummaryCap || !got.Counts[0].AtLeast || got.EmptyKnown || got.Empty {
		t.Fatal("capped semantics", got)
	}
	var core librarySummaryCore
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		core, err = s.WithContext(ctx).reusableLibrarySummaryCore(ctx, v, lib)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(core.Targets) != 64 {
		t.Fatal("target bound", len(core.Targets))
	}
	// Capped restricted emptiness remains unknown, never an invented exact zero.
	v.Restrictions.BlockUnrated = true
	got, scans = summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal(scans)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
	if !got.Empty || got.EmptyKnown || got.Counts[0].Count != 0 || !got.Counts[0].AtLeast {
		t.Fatal("restricted capped emptiness", got)
	}
}
