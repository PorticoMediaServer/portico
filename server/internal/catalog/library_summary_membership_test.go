package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func summaryCount(summary LibrarySummary, kind string) int {
	for _, count := range summary.Counts {
		if count.Kind == kind {
			return count.Count
		}
	}
	return -1
}

// Move a primary source, reuse its old path, then use the metadata editor's
// Owner write boundary. No identity/root/entity-library/membership is patched.
func TestLibrarySummaryRestrictedAudiobookMembershipBypass(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB)
	ctx := context.Background()
	originalRoot, movedRoot := filepath.Join(t.TempDir(), "original"), filepath.Join(t.TempDir(), "moved")
	for _, path := range []string{originalRoot, movedRoot} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	aLibrary, err := s.CreateAuthorized(ctx, "A", "audiobook", originalRoot, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	a := c.Handle(aLibrary.ID)
	localBook := c.Book(a, "Local book", "Shared Author")
	localFile := c.BookFile(localBook, 1, filepath.Join(originalRoot, "local.m4b"))
	c.Attributes(localFile.ID, "label", "blocked")
	var configRevision int64
	if err := c.DB.QueryRow(`SELECT revision FROM library_configuration WHERE library_id=?`, aLibrary.ID).Scan(&configRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveSource(ctx, aLibrary.ID, aLibrary.ID, SourceSettings{Path: movedRoot, Classification: "local", ExpectedRevision: configRevision, AcceptReplacement: true}, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	bLibrary, err := s.CreateAuthorized(ctx, "B", "audiobook", originalRoot, func(*sql.Tx) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	b := c.Handle(bLibrary.ID)
	otherBook := c.Book(b, "Other book", "Other Author")
	otherFile := c.BookFile(otherBook, 1, filepath.Join(originalRoot, "other.m4b"))
	// Ordinary Ensure refuses cross-library publication, but Owner fields do not
	// publish a new author; the membership derivation resolves the old identity.
	err = dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, _, err := compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{Library: b, Kind: compactcatalog.Author, Key: compactcatalog.AuthorKey("Shared Author"), Title: "Shared Author"})
		return err
	})
	if !errors.Is(err, compactcatalog.ErrIdentityConflict) {
		t.Fatalf("expected publication conflict, got %v", err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, otherBook.ID, compactcatalog.Owner, map[string]any{"author": "Shared Author"})
	})
	c.Drain()
	var overlapping, crossEdge int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_libraries WHERE root=?`, originalRoot).Scan(&overlapping); err != nil || overlapping != 2 {
		t.Fatal("creation roots", overlapping, err)
	}
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_memberships m JOIN catalog_entities au ON au.id=m.entity_id JOIN catalog_entities file ON file.id=m.item_id WHERE m.source=7 AND file.id=? AND au.library_id=? AND file.library_id=?`, otherFile.ID, a, b).Scan(&crossEdge); err != nil || crossEdge != 1 {
		t.Fatal("natural cross-library author edge", crossEdge, err)
	}
	t.Log("primary-source move/reuse plus Owner author edit produced a cross-library source-7 edge; EnsureEntityTx alone rejected it")
	lib := aLibrary
	v := Viewer{Libraries: []string{aLibrary.ID}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}}
	// Seed an actual previously computed core under the complete pre-bypass key.
	// This models an existing cache entry, not a fabricated production topology.
	key := summaryKeyRead(t, s, Viewer{Libraries: []string{aLibrary.ID}}, lib)
	key.Policy = summaryPolicyKey(v.Restrictions)
	if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
		_, err := s.state.summaries.get(ctx, key, func() (librarySummaryCore, error) { return s.WithContext(ctx).computeLibrarySummaryCore(ctx, v, lib) })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	entries, bytes := len(s.state.summaries.entries), s.state.summaries.bytes
	check := func(wantAuthor int) {
		t.Helper()
		before := telemetry.LibrarySummaryCacheCounters()
		for i := 0; i < 2; i++ {
			got, scans := summaryRead(t, s, v, lib)
			if scans != 1 {
				t.Fatal("restricted audiobook reused", scans)
			}
			summaryParity(t, got, summaryOracle(t, s, v, lib))
			if summaryCount(got, "author") != wantAuthor {
				t.Fatal("author visibility", got, wantAuthor)
			}
		}
		after := telemetry.LibrarySummaryCacheCounters()
		if after[telemetry.SummaryBypassRestrictedMembership] != before[telemetry.SummaryBypassRestrictedMembership]+2 || after[telemetry.SummaryHitEntry] != before[telemetry.SummaryHitEntry] || after[telemetry.SummaryHitFlight] != before[telemetry.SummaryHitFlight] || after[telemetry.SummaryMissCold] != before[telemetry.SummaryMissCold] {
			t.Fatal("bypass telemetry", before, after)
		}
		if len(s.state.summaries.entries) != entries || s.state.summaries.bytes != bytes || len(s.state.summaries.flights) != 0 {
			t.Fatal("restricted audiobook stored/shared")
		}
	}
	check(1)
	for _, labels := range [][]string{{"blocked"}, nil, {"blocked"}, nil} {
		prior := summaryKeyRead(t, s, Viewer{Libraries: []string{aLibrary.ID}}, lib)
		c.Attributes(otherFile.ID, "label", labels...)
		want := 1
		if len(labels) > 0 {
			want = 0
		}
		check(want)
		c.Drain()
		if after := summaryKeyRead(t, s, Viewer{Libraries: []string{aLibrary.ID}}, lib); after != prior {
			t.Fatal("expected unchanged consuming-library identity after member-only edit")
		}
		check(want)
	}
	// Every Active form bypasses on a clean, fully drained catalog too.
	age := 13
	for _, policy := range []identity.ContentRestrictions{{MaximumAge: &age}, {BlockUnrated: true}, {BlockedLabels: []string{"blocked"}}, {MemberDeniedLabels: []string{"blocked"}}} {
		v.Restrictions = policy
		for i := 0; i < 2; i++ {
			got, scans := summaryRead(t, s, v, lib)
			if scans != 1 {
				t.Fatal("active policy reused", policy, scans)
			}
			summaryParity(t, got, summaryOracle(t, s, v, lib))
		}
	}
	if len(s.state.summaries.entries) != entries || s.state.summaries.bytes != bytes {
		t.Fatal("active policy inserted")
	}
	v.Restrictions = identity.ContentRestrictions{}
	got, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("unrestricted cold", scans)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
	got, scans = summaryRead(t, s, v, lib)
	if scans != 0 {
		t.Fatal("unrestricted hit", scans)
	}
	summaryParity(t, got, summaryOracle(t, s, v, lib))
}

func TestLibrarySummaryMembershipDrainInvalidatesOrdinaryCachedKind(t *testing.T) {
	c := catalogtest.Open(t)
	handle := c.Library("tv", "TV", "tv", "/tv")
	first, second := c.Show(handle, "First", 2020), c.Show(handle, "Second", 2020)
	season := c.Season(first, 1)
	allowed := c.Episode(first, season, 1, "/tv/allowed.mkv")
	blocked := c.Episode(first, season, 2, "/tv/blocked.mkv")
	c.Attributes(blocked.ID, "label", "blocked")
	c.Drain()
	s := New(c.DB)
	lib := Library{ID: "tv", Kind: "tv"}
	v := Viewer{Libraries: []string{"tv"}, Restrictions: identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}}
	prior, scans := summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(prior, "show") != 2 {
		t.Fatal("cold shows", prior, scans)
	}
	_, scans = summaryRead(t, s, v, lib)
	if scans != 0 {
		t.Fatal("warm cache", scans)
	}
	key := summaryKeyRead(t, s, v, lib)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, allowed.ID, map[string]any{"show_id": second.ID, "season_id": nil})
	})
	var dirty int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty); err != nil || dirty == 0 {
		t.Fatal("no actual dirty work", dirty, err)
	}
	live, scans := summaryRead(t, s, v, lib)
	if scans != 1 {
		t.Fatal("pending reused", scans)
	}
	summaryParity(t, live, summaryOracle(t, s, v, lib))
	c.Drain()
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatal("worker did not acknowledge", dirty, err)
	}
	var edge int64
	if err := c.DB.QueryRow(`SELECT entity_id FROM catalog_browse_memberships WHERE item_id=? AND source=1`, allowed.ID).Scan(&edge); err != nil || edge != second.ID {
		t.Fatal("edge did not move", edge, err)
	}
	next := summaryKeyRead(t, s, v, lib)
	if next == key {
		t.Fatal("edge input did not invalidate key")
	}
	live, scans = summaryRead(t, s, v, lib)
	if scans != 1 || summaryCount(live, "show") != 1 {
		t.Fatal("drained stale core", live, scans)
	}
	summaryParity(t, live, summaryOracle(t, s, v, lib))
	if _, ok := s.state.summaries.entries[key]; !ok {
		t.Fatal("old entry was manually cleared")
	}
	live, scans = summaryRead(t, s, v, lib)
	if scans != 0 {
		t.Fatal("drained ordinary kind not reused", scans)
	}
	summaryParity(t, live, summaryOracle(t, s, v, lib))
}

func TestLibrarySummaryMovieDetailsAtTargetBoundary(t *testing.T) {
	c := catalogtest.Open(t)
	handle := c.Library("movies", "Movies", "movie", "/movies")
	items := make([]catalogtest.Item, 65)
	for i := range items {
		items[i] = c.Movie(handle, fmt.Sprintf("/movies/%d.mkv", i), fmt.Sprintf("Movie %d", i), 2026)
	}
	// Descending id order: items[1] is target 64, items[0] is outside.
	for _, i := range []int{0, 1, 2, 3} {
		c.Exec(`UPDATE catalog_item_details SET poster_url='fallback',backdrop_url=CASE WHEN ?<=1 THEN 'backdrop' ELSE '' END WHERE entity_id=?`, i, items[i].ID)
	}
	s := New(c.DB)
	lib := Library{ID: "movies", Kind: "movie"}
	v := Viewer{Libraries: []string{"movies"}}
	key := summaryKeyRead(t, s, v, lib)
	check := func(scansWant, covers int, backdrop string) {
		t.Helper()
		got, scans := summaryRead(t, s, v, lib)
		if scans != scansWant || len(got.CoverURLs) != covers || got.LatestArtwork != backdrop {
			t.Fatal("boundary details", got, scans)
		}
		summaryParity(t, got, summaryOracle(t, s, v, lib))
	}
	boundaryBackdrop := "/v1/items/" + items[1].Public + "/art/backdrop"
	check(1, 3, boundaryBackdrop)
	check(0, 3, boundaryBackdrop)
	core := s.state.summaries.entries[key].Value.(summaryEntry).core
	if len(core.Targets) != 64 || core.Targets[63].ID != items[1].Public {
		t.Fatal("64 target selection", core.Targets)
	}
	c.Exec(`DELETE FROM catalog_item_details WHERE entity_id=?`, items[1].ID)
	if summaryKeyRead(t, s, v, lib) != key {
		t.Fatal("details DELETE unexpectedly changed core key")
	}
	check(0, 2, "")
	c.Exec(`INSERT INTO catalog_item_details(entity_id,poster_url,backdrop_url) VALUES(?,'replacement','replacement')`, items[1].ID)
	if summaryKeyRead(t, s, v, lib) != key {
		t.Fatal("details INSERT unexpectedly changed core key")
	}
	check(0, 3, boundaryBackdrop)
}
