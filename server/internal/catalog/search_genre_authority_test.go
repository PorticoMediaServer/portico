package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func genreAuthorityInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Actual production baseline, source capture, coherent publication and private
// handles: newly imported IDs cannot revoke a previously accepted content B.
func TestGenreAuthorityNewMovieAndShowPreserveActiveReadyBasis(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	movie := c.Movie(movies, "/movies/old.mkv", "Old", 2000)
	c.Genres(movie.ID, "fixture", "Alpha", "Beta")
	empty := c.Show(tv, "Old empty", 2000)
	c.Genres(empty.ID, "fixture", "Empty")
	hidden := c.Movie(movies, "/movies/hidden.mkv", "Hidden", 2000)
	c.Attributes(hidden.ID, "label", "hidden")
	c.Drain()
	v := Viewer{Libraries: []string{"movies", "tv"}, Restrictions: identity.ContentRestrictions{MemberDeniedLabels: []string{"hidden"}}}
	r, store := genreAuthorityRuntime(t, c, v)
	first := genreReady(t, r, v, SearchGenreRequest{Limit: 1})
	original, err := r.decode(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := genreHandle(first.QueryID)
	pinned, err := store.Poll(context.Background(), "caller", h)
	if err != nil {
		t.Fatal(err)
	}
	var summary genreSummary
	if err = json.Unmarshal(pinned.Payload, &summary); err != nil {
		t.Fatal(err)
	}
	pinned.Release()
	if len(summary.Bases) != 2 {
		t.Fatal("fixture has no accepted two-library B", summary.Bases)
	}
	for _, b := range summary.Bases {
		if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, b.Scope.LibraryID) != genreAuthorityInt(t, c.DB, `SELECT max(id) FROM catalog_entities WHERE library_id=?`, b.Scope.LibraryID) {
			t.Fatal("missing accepted high-water")
		}
	}
	genreBefore := genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`)
	newMovie := c.Movie(movies, "/movies/new.mkv", "New", 2001)
	c.Genres(newMovie.ID, "fixture", "New movie genre")
	newShow := c.Show(tv, "New show", 2001)
	c.Genres(newShow.ID, "fixture", "New show genre")
	c.Drain()
	for _, item := range []struct{ item, library int64 }{{newMovie.ID, movies}, {newShow.ID, tv}} {
		if item.item <= genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, item.library) {
			t.Fatal("new target did not exceed accepted B")
		}
	}
	c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, newShow.ID, hidden.ID)
	if genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`) != genreBefore {
		t.Fatal("new target first member revoked B")
	}
	polled, err := r.Poll(context.Background(), "caller", first.QueryID)
	if err != nil {
		t.Fatal("new-title churn staled ready handle", err)
	}
	if polled.Page == nil || !reflect.DeepEqual(polled.Page.Genres, first.Genres) {
		t.Fatal("old handle changed contents", polled.Page)
	}
	polled.Release()
	out, err := r.Continue(context.Background(), "caller", v, SearchGenreRequest{}, first.NextCursor)
	if err != nil {
		t.Fatal("new-title churn staled B continuation", err)
	}
	out = genreAwait(t, r, "caller", out)
	defer out.Release()
	continued, err := r.decode(out.Page.ReloadCursor)
	if err != nil || continued.expires != original.expires {
		t.Fatal("continuation changed original expiry", err)
	}
	for _, b := range summary.Bases {
		snap, err := facetBaseSnapshot(context.Background(), c.DB, b.ID, true)
		if err != nil || snap != b {
			t.Fatal("accepted immutable B disappeared/changed", b, err)
		}
	}
}

func TestGenreAuthorityAcceptedEmptyShowFirstHiddenMemberRevokes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, restricted := range []bool{true, false} {
		t.Run(fmt.Sprintf("active-policy-%v", restricted), func(t *testing.T) {
			c := catalogtest.Open(t)
			tv := c.Library("a", "A", "tv", "/a")
			other := c.Library("b", "B", "movie", "/b")
			show := c.Show(tv, "Accepted empty", 2000)
			c.Genres(show.ID, "fixture", "Empty fallback")
			child := c.Movie(other, "/b/hidden.mkv", "Hidden", 2000)
			c.Attributes(child.ID, "label", "hidden")
			c.Drain()
			v := Viewer{Libraries: []string{"a"}}
			if restricted {
				v.Libraries = append(v.Libraries, "b")
				v.Restrictions = identity.ContentRestrictions{MemberDeniedLabels: []string{"hidden"}}
			}
			r, _ := genreAuthorityRuntime(t, c, v)
			first := genreReady(t, r, v, SearchGenreRequest{})
			if len(first.Genres) != 1 || first.Genres[0].Value != "Empty fallback" {
				t.Fatal(first)
			}
			if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, tv) < show.ID {
				t.Fatal("empty show not accepted")
			}
			before := genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`)
			// Child policy/grants were already hidden before B; only raw membership
			// changes here. Source 8 intentionally proves all-source first-member logic.
			c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, show.ID, child.ID)
			if genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`) != before+1 {
				t.Fatal("accepted empty target was not fenced exactly once")
			}
			got, err := r.Poll(context.Background(), "caller", first.QueryID)
			got.Release()
			if !errors.Is(err, readprep.ErrStale) {
				t.Fatal("hidden first member left old fallback usable", err)
			}
			got, err = r.Continue(context.Background(), "caller", v, SearchGenreRequest{}, first.ReloadCursor)
			got.Release()
			if !errors.Is(err, readprep.ErrStale) {
				t.Fatal("old B anchor survived revocation", err)
			}
		})
	}
}

func TestGenreAuthorityAllLibraryInactiveRetainsContentBasis(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("a", "A", "tv", "/a")
	other := c.Library("b", "B", "movie", "/b")
	show := c.Show(tv, "Accepted empty", 2000)
	c.Genres(show.ID, "fixture", "Empty fallback")
	child := c.Movie(other, "/b/member.mkv", "Member", 2000)
	c.Drain()
	v := Viewer{Libraries: []string{"a", "b"}}
	r, _ := genreAuthorityRuntime(t, c, v)
	first := genreReady(t, r, v, SearchGenreRequest{})
	c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, show.ID, child.ID)
	c.Attributes(child.ID, "label", "new policy input")
	out, err := r.Poll(context.Background(), "caller", first.QueryID)
	if err != nil {
		t.Fatal("all-library inactive content B was fenced", err)
	}
	defer out.Release()
	if out.Page == nil || !reflect.DeepEqual(out.Page.Genres, first.Genres) {
		t.Fatal("retained content B changed", out.Page)
	}
}

// Header-only unit fixture: exercise canonical grant size/lifetime matching;
// production acceptance/publication tests above never install a shadow schema.
func TestGenreAuthorityGrantCoverageUsesCanonicalLifetimeAndPublicID(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "grant-headers.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE libraries(id TEXT PRIMARY KEY,kind TEXT); CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,library_id TEXT,retired INTEGER); CREATE TABLE catalog_facet_source_state(singleton INTEGER PRIMARY KEY,policy_epoch INTEGER,genre_epoch INTEGER); INSERT INTO catalog_facet_source_state VALUES(1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	grants := make([]genreGrant, 1100)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := range grants {
		grants[i] = genreGrant{ID: int64(i + 1), Library: fmt.Sprintf("lib-%04d", i)}
		if _, err = tx.Exec(`INSERT INTO catalog_libraries VALUES(?,?,0)`, grants[i].ID, grants[i].Library); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO libraries VALUES('lib-0000','tv')`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	source := func(gs []genreGrant) string {
		raw, _ := json.Marshal(gs)
		return SearchVideoGenreSource + ":" + string(raw)
	}
	scope := FacetBaseScope{LibraryID: 1, Library: "lib-0000", Field: "genre", Source: source(grants), Policy: FacetBasePolicy(identity.ContentRestrictions{})}
	capture := func(s FacetBaseScope) string {
		t.Helper()
		value, err := CaptureFacetAuthority(context.Background(), db, s)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	all := capture(scope)
	if strings.Contains(all, "genreEpoch") {
		t.Fatal("complete grants treated as limited")
	}
	for _, mutate := range []func([]genreGrant) []genreGrant{
		func(g []genreGrant) []genreGrant { return g[:len(g)-1] },
		func(g []genreGrant) []genreGrant { g[len(g)-1].ID += 10000; return g },
		func(g []genreGrant) []genreGrant { g[len(g)-1].Library += "-old"; return g },
	} {
		limited := scope
		limited.Source = source(mutate(append([]genreGrant{}, grants...)))
		if !strings.Contains(capture(limited), "genreEpoch") {
			t.Fatal("incomplete lifetime/public grant set was treated as all-library")
		}
	}
	bare := scope
	bare.Source = SearchVideoGenreSource
	if !strings.Contains(capture(bare), "genreEpoch") {
		t.Fatal("bare legacy source assumed complete grants")
	}
	if _, err = db.Exec(`UPDATE catalog_facet_source_state SET genre_epoch=1`); err != nil {
		t.Fatal(err)
	}
	if capture(scope) != all {
		t.Fatal("all-library inactive authority bound extra epoch")
	}
	limited := scope
	limited.Source = source(grants[:len(grants)-1])
	if _, err = db.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=1100`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capture(limited), "genreEpoch") {
		t.Fatal("retired library incorrectly required a grant")
	}
}

func genreAuthorityBegin(t *testing.T, c *catalogtest.Catalog, viewer Viewer) (*FacetBases, int64) {
	t.Helper()
	var plan FacetBasePlan
	var authority string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		scopes, err := captureGenreScopes(ctx, tx, viewer)
		if err != nil {
			return err
		}
		if len(scopes) != 1 {
			return ErrFacetBaseInput
		}
		plan, err = CaptureSearchGenreBasePlan(ctx, tx, scopes[0])
		if err != nil {
			return err
		}
		authority, err = CaptureFacetAuthority(ctx, tx, plan.Scope)
		return err
	})
	b := &FacetBases{DB: c.DB, Producer: plan.Producer, MaxBytes: 128 << 20}
	var id int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = b.Begin(ctx, tx, plan.Scope, plan.Witness, authority)
		return err
	})
	return b, id
}

func TestGenreAuthorityWatermarkCapacityRollbackAndLifetimeCharge(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("empty", "Empty", "tv", "/empty")
	c.Drain()
	b, id := genreAuthorityBegin(t, c, Viewer{Libraries: []string{"empty"}})
	used := genreAuthorityInt(t, c.DB, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`)
	// Empty-source Done is the actual publication quantum. Only the new library
	// metadata charge crosses this limit; COMPLETE and its active pointer must
	// remain invisible together with the rolled-back watermark/ledger change.
	b.MaxBytes = used + 127
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { _, err := b.Step(context.Background(), tx, id); return err })
	if !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal("first watermark charge bypassed global cap", err)
	}
	if genreAuthorityInt(t, c.DB, `SELECT count(*) FROM catalog_search_genre_accepted_targets`) != 0 || genreAuthorityInt(t, c.DB, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`) != used || genreAuthorityInt(t, c.DB, `SELECT complete FROM catalog_facet_bases WHERE id=?`, id) != 0 || genreAuthorityInt(t, c.DB, `SELECT count(*) FROM catalog_facet_base_classes WHERE active_base IS NOT NULL`) != 0 {
		t.Fatal("publication capacity failure leaked metadata/COMPLETE")
	}
	b.MaxBytes = used + 128
	err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		done, err := b.Step(context.Background(), tx, id)
		if err == nil && !done {
			return errors.New("empty publication did not finish")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib) != 0 {
		t.Fatal("empty library high-water must be zero")
	}
	var physical, payload int64
	if err = c.DB.QueryRow(`SELECT sum(pgsize),sum(payload) FROM dbstat WHERE name='catalog_search_genre_accepted_targets'`).Scan(&physical, &payload); err != nil {
		t.Fatal(err)
	}
	t.Logf("one accepted library: logical=128B table=%dB payload=%dB (single root page, no secondary index)", physical, payload)
	// Deleting the BASE's work checkpoint reclaims its own charge separately.
	// Measure exactly the row's lifetime release through the real library FK.
	beforeDelete := genreAuthorityInt(t, c.DB, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`)
	c.Exec(`DELETE FROM catalog_libraries WHERE id=?`, lib)
	if genreAuthorityInt(t, c.DB, `SELECT count(*) FROM catalog_search_genre_accepted_targets`) != 0 || genreAuthorityInt(t, c.DB, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`) != beforeDelete-128 {
		t.Fatal("library cascade did not release exactly one row charge")
	}
}

func TestGenreAuthorityWatermarkMonotonePublicationAndNoop(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	first := c.Movie(lib, "/movies/first.mkv", "First", 2000)
	c.Drain()
	b, id := genreAuthorityBegin(t, c, Viewer{Libraries: []string{"movies"}})
	finish := func() {
		t.Helper()
		for step := 0; step < 200; step++ {
			done := false
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				var err error
				done, err = b.Step(ctx, tx, id)
				return err
			})
			if done {
				return
			}
		}
		t.Fatal("genre publication did not finish")
	}
	finish()
	if got := genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib); got != first.ID {
		t.Fatal("incorrect accepted maximum", got, first.ID)
	}
	// A repeated coherent publication uses a distinct equivalent-policy class;
	// unchanged high-water must not execute a table UPDATE or another charge.
	c.Exec(`CREATE TABLE authority_hwm_updates(n INTEGER); CREATE TRIGGER authority_observe_hwm_update AFTER UPDATE ON catalog_search_genre_accepted_targets BEGIN INSERT INTO authority_hwm_updates VALUES(1); END;`)
	v := Viewer{Libraries: []string{"movies"}, Restrictions: identity.ContentRestrictions{MemberDeniedLabels: []string{"hidden"}}}
	b, id = genreAuthorityBegin(t, c, v)
	finish()
	if genreAuthorityInt(t, c.DB, `SELECT count(*) FROM authority_hwm_updates`) != 0 {
		t.Fatal("unchanged high-water executed a no-op write")
	}
	next := c.Movie(lib, "/movies/next.mkv", "Next", 2001)
	c.Drain()
	// Content/source drift alone cannot advance the accepted high-water.
	if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib) != first.ID {
		t.Fatal("source writer advanced accepted maximum before publication")
	}
	b, id = genreAuthorityBegin(t, c, v)
	finish()
	if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib) != next.ID || genreAuthorityInt(t, c.DB, `SELECT count(*) FROM authority_hwm_updates`) != 1 {
		t.Fatal("genuine successor publication did not advance monotonically")
	}
	// A conservatively higher retained watermark never moves backwards when a
	// later class sees a smaller maximum (including reused/lower target IDs).
	c.Exec(`UPDATE catalog_search_genre_accepted_targets SET max_entity_id=max_entity_id+1000 WHERE library_id=?`, lib)
	high := genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib)
	v.Restrictions.MemberDeniedLabels = []string{"another-hidden"}
	b, id = genreAuthorityBegin(t, c, v)
	finish()
	if genreAuthorityInt(t, c.DB, `SELECT max_entity_id FROM catalog_search_genre_accepted_targets WHERE library_id=?`, lib) != high || genreAuthorityInt(t, c.DB, `SELECT count(*) FROM authority_hwm_updates`) != 2 {
		t.Fatal("publication lowered/noop-updated retained maximum")
	}
}

func TestGenreAuthorityAcceptedTargetAndFirstMemberSeekPlans(t *testing.T) {
	c := catalogtest.Open(t)
	// Read the real baseline trigger's predicate, so the plan regression cannot
	// silently keep testing a separate copy after production SQL changes.
	var trigger string
	if err := c.DB.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='catalog_facet_policy_membership_insert'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	_, predicate, ok := strings.Cut(trigger, " WHEN ")
	if !ok {
		t.Fatal(trigger)
	}
	predicate, _, ok = strings.Cut(predicate, " BEGIN")
	if !ok {
		t.Fatal(trigger)
	}
	predicate = strings.NewReplacer("NEW.entity_id", "1", "NEW.item_id", "2", "NEW.source", "8").Replace(predicate)
	queries := []struct {
		sql  string
		want []string
	}{
		{`SELECT id FROM catalog_entities INDEXED BY catalog_entities_library_id WHERE library_id=1 ORDER BY id DESC LIMIT 1`, []string{"SEARCH catalog_entities USING COVERING INDEX catalog_entities_library_id (library_id=?)"}},
		{`SELECT ` + predicate, []string{"SEARCH b USING INTEGER PRIMARY KEY (rowid=?)", "SEARCH h USING INTEGER PRIMARY KEY (rowid=?)", "SEARCH catalog_browse_memberships USING PRIMARY KEY (entity_id=? AND (item_id,source)<(?,?))", "SEARCH catalog_browse_memberships USING PRIMARY KEY (entity_id=? AND (item_id,source)>(?,?))"}},
	}
	for _, query := range queries {
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN ` + query.sql)
		if err != nil {
			t.Fatal(err)
		}
		plan := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if strings.Contains(strings.ReplaceAll(plan, "SCAN CONSTANT ROW", ""), "SCAN ") || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatal("authority work lost indexed seek", plan)
		}
		for _, want := range query.want {
			if !strings.Contains(plan, want) {
				t.Fatal("missing full seek", want, plan)
			}
		}
		t.Log(plan)
	}
}

func TestGenreAuthorityWatermarkRejectsWrongFamilyAndSourceDrift(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("empty", "Empty", "tv", "/empty")
	c.Drain()
	_, id := genreAuthorityBegin(t, c, Viewer{Libraries: []string{"empty"}})
	snap, err := facetBaseSnapshot(context.Background(), c.DB, id, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*FacetBaseSnapshot){
		func(s *FacetBaseSnapshot) { s.Scope.Source = UnrestrictedFacetBaseSource },
		func(s *FacetBaseSnapshot) { s.Scope.Source = SearchVideoGenreSource },
		func(s *FacetBaseSnapshot) { s.Scope.Field = "studio" },
	} {
		invalid := snap
		mutate(&invalid)
		err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			return (searchGenreProducer{}).ValidatePublication(context.Background(), tx, invalid)
		})
		if !errors.Is(err, ErrFacetBaseInput) {
			t.Fatal("non-genre publication accepted high-water", err)
		}
	}
	c.Exec(`UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`)
	err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return (searchGenreProducer{}).ValidatePublication(context.Background(), tx, snap)
	})
	if !errors.Is(err, compactcatalog.ErrFacetProducerChanged) {
		t.Fatal("incoherent publication accepted high-water", err)
	}
	if genreAuthorityInt(t, c.DB, `SELECT count(*) FROM catalog_search_genre_accepted_targets`) != 0 {
		t.Fatal("failed validation wrote accepted-target metadata")
	}
}

func TestGenreAuthorityFirstMemberUsesOwnLibraryAndConservativeLowerIDs(t *testing.T) {
	c := catalogtest.Open(t)
	a := c.Library("a", "A", "tv", "/a")
	b := c.Library("b", "B", "tv", "/b")
	accepted := c.Show(a, "Accepted", 2000)
	unaccepted := c.Show(b, "Unaccepted", 2000)
	season := c.Season(accepted, 1)
	child := c.Show(b, "Member", 2000)
	c.Drain()
	// Isolate the actual accepting trigger with a conservative retained maximum;
	// normal production installation of this row is covered by the build tests.
	c.Exec(`INSERT INTO catalog_search_genre_accepted_targets(library_id,max_entity_id) VALUES(?,1000000)`, a)
	before := genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`)
	c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, unaccepted.ID, child.ID)
	c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, season.ID, child.ID)
	if genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`) != before {
		t.Fatal("foreign-library maximum or nonfallback kind stamped authority")
	}
	lower := c.Show(a, "Conservatively below retained maximum", 2001)
	c.Drain()
	c.Exec(`INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,8)`, lower.ID, child.ID)
	if genreAuthorityInt(t, c.DB, `SELECT genre_epoch FROM catalog_facet_source_state WHERE singleton=1`) != before+1 {
		t.Fatal("lower/reused target ID escaped conservative fence")
	}
}

// Synchronously publish the real producer's small BASEs before starting the
// real memory-only query store. This isolates authority semantics from writer
// scheduling/quiet-gap deadlines; no fixture facts or shadow DDL are installed.
func genreAuthorityRuntime(t *testing.T, c *catalogtest.Catalog, v Viewer) (*GenreRuntime, *readprep.Store) {
	t.Helper()
	scopes, err := captureGenreScopes(context.Background(), c.DB, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range scopes {
		var plan FacetBasePlan
		var authority string
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			plan, err = CaptureSearchGenreBasePlan(ctx, tx, scope)
			if err != nil {
				return err
			}
			authority, err = CaptureFacetAuthority(ctx, tx, scope)
			return err
		})
		b := &FacetBases{DB: c.DB, Producer: plan.Producer, MaxBytes: 128 << 20}
		var id int64
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			id, err = b.Begin(ctx, tx, scope, plan.Witness, authority)
			return err
		})
		done := false
		for step := 0; step < 300 && !done; step++ {
			err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { var err error; done, err = b.Step(context.Background(), tx, id); return err })
			if err != nil {
				t.Fatal(err)
			}
		}
		if !done {
			t.Fatal("real genre BASE failed to publish")
		}
	}
	store, err := readprep.New(readprep.Config{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	check := func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	bases, err := NewFacetRuntime(c.DB, store, check, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewGenreRuntime(c.DB, store, check, bases)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, store
}
