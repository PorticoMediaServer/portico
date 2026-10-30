package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A fresh catalog Service in a settled snapshot first resolves whether serving
// overlays must be retained. The other statement is the action batch, including
// viewer/library/restriction predicates and indexed availability EXISTS. BEGIN
// is a transaction/acquisition, not a statement; no per-card or per-member read
// is allowed. Further batches on the same scoped Service reuse that resolution.
func assertHomeActionSnapshotQueries(t *testing.T, cost dbwork.Cost, queries []string, batches int) {
	t.Helper()
	if cost.Statements != int64(1+batches) || cost.Transactions != 1 || cost.Acquisitions != 1 || len(queries) != 1+batches {
		t.Fatalf("one snapshot/generation read plus %d action batches: cost=%+v queries=%v", batches, cost, queries)
	}
	if queries[0] != "SELECT retaining FROM catalog_generations WHERE singleton=1" {
		t.Fatalf("unexpected snapshot serving-generation read: %s", queries[0])
	}
	for _, query := range queries[1:] {
		for _, constraint := range []string{
			"FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)",
			"home_member.entity_id=e.id AND available.available=1 AND available.retired=0",
			"visible_library.library_id IN(SELECT value FROM json_each(?))",
			"WHERE e.retired=0 AND l.retired=0 AND (e.kind NOT IN(20,21) OR EXISTS(SELECT 1 FROM libraries reading_library",
			"CASE WHEN k.playable=1 OR e.kind IN(20,21) THEN (EXISTS(SELECT 1 FROM catalog_entities visible_item",
		} {
			if !strings.Contains(query, constraint) {
				t.Fatalf("action batch lost its returned-ID/current-policy constraint %q: %s", constraint, query)
			}
		}
		if strings.Contains(query, "LIMIT 64") {
			t.Fatal("action availability regained a member correctness cap")
		}
	}
}

func TestHomeActionProjectionUsesDisplayedEntitiesAndBatchesReturnedItems(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	show, episode := homeItem(t, db, "show"), homeItem(t, db, "e101")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision) VALUES('p',?,1,1,NULL,7),('p',?,0,0,8,3)`, show.ID, episode.ID)
	c.Drain()
	movie := homeItem(t, db, "m1")
	makeRows := func() []HomeRow {
		return []HomeRow{{Entries: []ContentEntry{
			{ID: show.Public, LibraryID: "tv", Kind: "show", Playback: &ContentPlayback{ItemID: episode.Public}},
			{ID: movie.Public, LibraryID: "movies", Kind: "movie"},
			{ID: movie.Public, LibraryID: "movies", Kind: "movie"},
			{ID: homeItem(t, db, "album").Public, LibraryID: "music", Kind: "album"},
			{ID: homeItem(t, db, "book").Public, LibraryID: "books", Kind: "book"},
		}, Resumes: []HomeResume{{EntryID: show.Public, Target: ContentEntry{ID: episode.Public, LibraryID: "tv", Kind: "episode"}}}}}
	}
	rows := makeRows()
	duplicates := []HomeRow{{}}
	for range 40 {
		duplicates[0].Entries = append(duplicates[0].Entries, rows[0].Entries[1])
	}
	deletionReads := 0
	deletionLibraries := map[string]int{}
	authority := HomeActionAuthority{Personal: true, Organize: true, Bulk: true, Playback: true, Queue: true, Metadata: true, Refresh: true, Downloads: true, DeleteLibrary: func(library string) (bool, error) {
		deletionReads++
		deletionLibraries[library]++
		return library == "movies", nil
	}}
	ctx, cost := dbwork.Measure(context.Background())
	ctx, queries := dbwork.TraceQueryPlans(ctx)
	if err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
		scoped := s.WithContext(ctx)
		if err := scoped.ProjectHomeActions(homeRequestFixture().Viewer, authority, rows, nil); err != nil {
			return err
		}
		assertHomeActionSnapshotQueries(t, cost(), queries(), 1)
		// Forty copies still use one returned-key batch; no authority from the
		// first call is retained, nor is the generation queried a second time.
		return scoped.ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{}, duplicates, nil)
	}); err != nil {
		t.Fatal(err)
	}
	assertHomeActionSnapshotQueries(t, cost(), queries(), 2)
	if deletionReads != 4 || len(deletionLibraries) != 4 {
		t.Fatalf("deletion policies=%d libraries=%v", deletionReads, deletionLibraries)
	}
	for _, library := range []string{"movies", "tv", "music", "books"} {
		if deletionLibraries[library] != 1 {
			t.Fatalf("deletion policy not cached for returned library %q: %v", library, deletionLibraries)
		}
	}
	for _, entry := range duplicates[0].Entries {
		if a := entry.HomeActions; a == nil || a.Play || a.Delete || a.Selection != (HomeSelectionActions{}) {
			t.Fatalf("repeated batch borrowed previous authority: %+v", a)
		}
	}
	a := rows[0].Entries[0].HomeActions
	if a == nil || !a.MyList || !a.Favorite || !a.EditMetadata || a.Delete || !a.State.Watchlisted || !a.State.Favorite || a.State.Rating != nil || a.State.PersonalRevision != 7 {
		t.Fatalf("show offers/state: %+v", a)
	}
	if rows[0].Entries[0].ID != show.Public || rows[0].Entries[0].Playback.ItemID != episode.Public {
		t.Fatal("aggregate identity replaced by a child")
	}
	if child := rows[0].Resumes[0].Target.HomeActions; child == nil || child.State.Rating == nil || *child.State.Rating != 8 || child.State.PersonalRevision != 3 {
		t.Fatalf("resume state: %+v", child)
	}
	if a := rows[0].Entries[1].HomeActions; a == nil || !a.Delete || !a.Watched || a.Played {
		t.Fatalf("movie offers: %+v", a)
	}
	if a := rows[0].Entries[3].HomeActions; a == nil || a.MyList || a.Watched || !a.Played || a.Delete || a.RefreshMetadata {
		t.Fatalf("album offers: %+v", a)
	}
	if a := rows[0].Entries[3].HomeActions; !a.Selection.MyList {
		t.Fatal("album lost the genuine descendant My List command")
	}
	if a := rows[0].Entries[4].HomeActions; a == nil || !a.MyList || a.Watched || !a.Played || a.Delete {
		t.Fatalf("book offers: %+v", a)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"/movies/", "/tv/", "sourcePath", "filePath", "apiKey", "rootIdentity"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("private field leaked: %s", private)
		}
	}
}

func TestHomeActionProjectionFailsClosedForMissingAuthorityAndWrongIdentity(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	movie := homeItem(t, db, "m1")
	rows := []HomeRow{{Entries: []ContentEntry{
		{ID: movie.Public, LibraryID: "movies", Kind: "movie"},
		{ID: movie.Public, LibraryID: "private", Kind: "movie"},
		{ID: movie.Public, LibraryID: "movies", Kind: "show"},
		{ID: homeItem(t, db, "book").Public, LibraryID: "books", Kind: "book"},
	}}}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture("movies").Viewer, HomeActionAuthority{}, rows, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if a := rows[0].Entries[0].HomeActions; a == nil || a.Play || a.Queue || a.Playlist || a.Collection || a.MyList || a.Favorite || a.Watched || a.EditMetadata || a.RefreshMetadata || a.Delete || a.Download {
		t.Fatalf("ungranted offers: %+v", a)
	}
	if a := rows[0].Entries[0].HomeActions; a.Selection != (HomeSelectionActions{}) {
		t.Fatalf("missing authority supplied selection grants: %+v", a.Selection)
	}
	for _, entry := range rows[0].Entries[1:] {
		if entry.HomeActions != nil {
			t.Fatalf("wrong or inaccessible identity got offers: %+v", entry)
		}
	}
	if err := s.ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{}, rows, nil); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("projection outside snapshot: %v", err)
	}
}

func TestHomeActionProjectionUnavailableMediaAndCollectionAreIndependent(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	movie := homeItem(t, db, "m1")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, movie.Asset, false)
	})
	c.Drain()
	collection, err := s.CreateCollection("movies", "Actual series")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCollectionItem(collection.ID, homeItem(t, db, "m2").Public, true); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	rows := []HomeRow{{Entries: []ContentEntry{{ID: movie.Public, LibraryID: "movies", Kind: "movie"}}}, {Collection: &HomeCollection{MediaKind: "movie", Collection: collection, Members: []ContentEntry{{ID: homeItem(t, db, "m2").Public, LibraryID: "movies", Kind: "movie"}}}}}
	if err = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{Personal: true, Playback: true, Queue: true, Organize: true, Bulk: true, Metadata: true, Downloads: true, DeleteLibrary: func(string) (bool, error) { return true, nil }}, rows, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if a := rows[0].Entries[0].HomeActions; a == nil || a.Play || a.Queue || a.Download || !a.MyList {
		t.Fatalf("unavailable offers: %+v", a)
	}
	banner := rows[1].Collection
	if banner.Actions == nil || !banner.Actions.Details || !banner.Actions.Playlist || banner.Actions.Delete || banner.Actions.EditMetadata || banner.Actions.MyList || banner.Actions.Download {
		t.Fatalf("collection grants: %+v", banner.Actions)
	}
	if banner.Members[0].HomeActions == nil || !banner.Members[0].HomeActions.Delete || banner.Actions == banner.Members[0].HomeActions {
		t.Fatal("banner borrowed member grants")
	}
	if banner.Collection.ID != collection.ID || banner.MediaKind != "movie" {
		t.Fatal("canonical collection identity/semantics lost")
	}
}

func TestHomeActionProjectionRestrictedAvailabilityUsesVisibleMembers(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	for _, name := range []string{"e102", "e201", "e202"} {
		c.Attributes(homeItem(t, db, name).ID, "label", "adult")
	}
	visible := homeItem(t, db, "e101")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, visible.Asset, false)
	})
	c.Drain()
	v := homeRequestFixture("tv").Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	read := func() *HomeActions {
		t.Helper()
		rows := []HomeRow{{Entries: []ContentEntry{{ID: homeItem(t, db, "show").Public, LibraryID: "tv", Kind: "show"}}}}
		if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			return s.WithContext(ctx).ProjectHomeActions(v, HomeActionAuthority{Personal: true, Playback: true, Queue: true, Downloads: true}, rows, nil)
		}); err != nil {
			t.Fatal(err)
		}
		return rows[0].Entries[0].HomeActions
	}
	if a := read(); a == nil || a.Play || a.Queue || a.Download || !a.MyList {
		t.Fatalf("hidden available episodes supplied a false grant: %+v", a)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, visible.Asset, true)
	})
	c.Drain()
	if a := read(); a == nil || !a.Play || !a.Queue || !a.Download {
		t.Fatalf("visible available episode did not supply the action proof: %+v", a)
	}
}

func TestHomeActionSpotlightKeepsAggregateGrantsSeparateFromResume(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	show, episode := homeItem(t, db, "show"), homeItem(t, db, "e101")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,1,5),('p',?,0,0,2)`, show.ID, episode.ID)
	c.Drain()
	spotlight := []HomeSpotlight{{Entry: ContentEntry{ID: show.Public, LibraryID: "tv", Kind: "show"}, ResumeTarget: &ContentEntry{ID: episode.Public, LibraryID: "tv", Kind: "episode", Playback: &ContentPlayback{ItemID: episode.Public}}}}
	ctx, cost := dbwork.Measure(context.Background())
	ctx, queries := dbwork.TraceQueryPlans(ctx)
	if err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
		return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture("tv").Viewer, HomeActionAuthority{Personal: true, Playback: true, Queue: true, Metadata: true}, nil, spotlight)
	}); err != nil {
		t.Fatal(err)
	}
	assertHomeActionSnapshotQueries(t, cost(), queries(), 1)
	if a := spotlight[0].Entry.HomeActions; a == nil || !a.MyList || !a.State.Watchlisted || a.State.PersonalRevision != 5 || !a.EditMetadata {
		t.Fatalf("displayed spotlight offers: %+v", a)
	}
	if a := spotlight[0].ResumeTarget.HomeActions; a == nil || a.State.Watchlisted || a.State.PersonalRevision != 2 {
		t.Fatalf("resume offers borrowed aggregate state: %+v", a)
	}
	if spotlight[0].Entry.ID != show.Public || spotlight[0].ResumeTarget.Playback.ItemID != episode.Public {
		t.Fatal("spotlight identity changed")
	}
}

func TestHomeActionAvailabilityFindsPermittedMemberBeyond64AndRechecksCurrentPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	c := catalogtest.New(t, db)
	s := New(db)
	show := c.Show(c.Handle("tv"), "Long series", 2026)
	season := c.Season(show, 1)
	unavailable := c.Episode(show, season, 1, "/tv/permitted-unavailable.mkv")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, unavailable.Asset, false)
	})
	c.Drain()
	var hidden, permitted catalogtest.Item
	v := homeRequestFixture("tv").Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	read := func() []HomeRow {
		t.Helper()
		// A copied global/class availability flag is not the command proof.
		stale := false
		rows := []HomeRow{{Entries: []ContentEntry{
			{ID: show.Public, LibraryID: "tv", Kind: "show", Available: &stale},
			{ID: season.Public, LibraryID: "tv", Kind: "season"},
		}}}
		ctx, measured := dbwork.Measure(context.Background())
		ctx, queries := dbwork.TraceQueryPlans(ctx)
		if err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
			return s.WithContext(ctx).ProjectHomeActions(v, HomeActionAuthority{Personal: true, Bulk: true, Playback: true, Queue: true, Downloads: true}, rows, nil)
		}); err != nil {
			t.Fatal(err)
		}
		assertHomeActionSnapshotQueries(t, measured(), queries(), 1)
		return rows
	}
	check := func(want bool) {
		t.Helper()
		rows := read()
		for _, entry := range rows[0].Entries {
			a := entry.HomeActions
			if a == nil || a.Play != want || a.Queue != want || a.Download != want || a.Selection.Play != want || !a.Selection.MyList {
				t.Fatalf("exact permitted availability=%v: %+v", want, entry)
			}
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{hidden.Public, permitted.Public, "/tv/private-episode", "sourcePath", "filePath"} {
			if secret != "" && strings.Contains(string(raw), secret) {
				t.Fatalf("member identity/path leaked into offers: %s", secret)
			}
		}
	}
	check(false) // One unavailable member: the same two-statement snapshot cost.
	for number := 2; number <= 81; number++ {
		hidden = c.Episode(show, season, number, fmt.Sprintf("/tv/private-episode-%d.mkv", number))
		c.Attributes(hidden.ID, "label", "adult")
	}
	permitted = c.Episode(show, season, 82, "/tv/permitted-available.mkv")
	c.Drain()
	var before int
	if err := db.QueryRow(`SELECT count(DISTINCT item_id) FROM catalog_browse_memberships WHERE entity_id=? AND item_id<?`, show.ID, permitted.ID).Scan(&before); err != nil || before != 81 {
		t.Fatalf("fixture did not place permitted availability behind 81 members: %d %v", before, err)
	}
	check(true)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, permitted.Asset, false)
	})
	c.Drain()
	check(false) // Hidden available members cannot replace the permitted member.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, permitted.Asset, true)
	})
	c.Attributes(permitted.ID, "label", "adult")
	c.Drain()
	v.Fence = "restriction-revoked"
	v.Restrictions.Revision++
	check(false) // Permission revocation wins over current physical availability.
	v.Restrictions = identity.ContentRestrictions{}
	v.Fence = "unrestricted-current"
	check(true)
}

func TestHomeActionAggregateDeleteUsesDisplayedTargetAndExactLibraryAuthority(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	makeRows := func() []HomeRow {
		rows := []HomeRow{{}}
		for _, target := range []struct{ name, kind, library string }{
			{"show", "show", "tv"}, {"s1", "season", "tv"}, {"album", "album", "music"}, {"artist", "artist", "music"}, {"book", "book", "books"},
		} {
			rows[0].Entries = append(rows[0].Entries, ContentEntry{ID: homeItem(t, db, target.name).Public, Kind: target.kind, LibraryID: target.library})
		}
		return rows
	}
	for _, allowed := range []bool{true, false} {
		rows := makeRows()
		policies := map[string]int{}
		if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{DeleteLibrary: func(library string) (bool, error) {
				policies[library]++
				return allowed, nil
			}}, rows, nil)
		}); err != nil {
			t.Fatal(err)
		}
		for index, entry := range rows[0].Entries {
			if a := entry.HomeActions; a == nil || a.Delete != allowed || a.Selection.Delete != allowed {
				t.Fatalf("aggregate delete authority=%v: %+v", allowed, entry)
			}
			if entry.ID != makeRows()[0].Entries[index].ID || entry.Playback != nil {
				t.Fatal("aggregate delete substituted a child")
			}
		}
		if len(policies) != 3 || policies["tv"] != 1 || policies["music"] != 1 || policies["books"] != 1 {
			t.Fatalf("library policies were not cached per returned library: %+v", policies)
		}
	}
	// A fabricated entry that is no longer visible gets no offers at all.
	c := catalogtest.New(t, db)
	for _, name := range []string{"e101", "e102", "e201", "e202"} {
		c.Attributes(homeItem(t, db, name).ID, "label", "adult")
	}
	c.Drain()
	rows := makeRows()
	v := homeRequestFixture("tv").Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	policyReads := 0
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		return s.WithContext(ctx).ProjectHomeActions(v, HomeActionAuthority{DeleteLibrary: func(string) (bool, error) { policyReads++; return true, nil }}, rows, nil)
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range rows[0].Entries {
		if entry.HomeActions != nil {
			t.Fatalf("hidden or foreign library target retained delete authority: %+v", entry)
		}
	}
	if policyReads != 0 {
		t.Fatal("hidden targets probed owner library policy")
	}
}

func TestHomeActionSelectionMyListUsesRealPermittedContainerMembers(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	collection, err := s.CreateCollection("movies", "Actual series")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCollectionItem(collection.ID, homeItem(t, db, "m1").Public, true); err != nil {
		t.Fatal(err)
	}
	c.Attributes(homeItem(t, db, "t2").ID, "label", "adult")
	c.Drain()
	v := homeRequestFixture().Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	for _, bulk := range []bool{false, true} {
		for _, target := range []struct{ kind, id, library string }{
			{"show", homeItem(t, db, "show").Public, "tv"}, {"season", homeItem(t, db, "s1").Public, "tv"},
			{"album", homeItem(t, db, "album").Public, "music"}, {"artist", homeItem(t, db, "artist").Public, "music"},
			{"book", homeItem(t, db, "book").Public, "books"}, {"collection", collection.ID, "movies"},
		} {
			rows := []HomeRow{{Entries: []ContentEntry{{ID: target.id, Kind: target.kind, LibraryID: target.library}}}}
			if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
				return s.WithContext(ctx).ProjectHomeActions(v, HomeActionAuthority{Personal: true, Bulk: bulk}, rows, nil)
			}); err != nil {
				t.Fatal(err)
			}
			a := rows[0].Entries[0].HomeActions
			if a == nil || a.Selection.MyList != bulk || a.MyList != (target.kind == "show" || target.kind == "book") || a.State.Watchlisted {
				t.Fatalf("single/selection My List semantics for %s scheduler=%v: %+v", target.kind, bulk, a)
			}
			if !bulk {
				continue
			}
			query, args, err := s.bulkSelection(identity.Principal{}, v, JobRequest{Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: target.kind, ID: target.id}}})
			if err != nil {
				t.Fatal(err)
			}
			members, err := scanInt64s(db.Query(query, args...))
			if err != nil || len(members) == 0 {
				t.Fatalf("offered container command has no genuine selector: %s %v %v", target.kind, members, err)
			}
			for _, member := range members {
				if member == c.ID(target.id) || member == homeItem(t, db, "t2").ID {
					t.Fatalf("bulk selector selected aggregate or hidden descendant: %s %d", target.kind, member)
				}
			}
		}
	}
	// Exercise the command's actual write primitive: the album's title flag
	// stays absent while its permitted descendants join My List.
	query, args, err := s.bulkSelection(identity.Principal{}, v, JobRequest{Command: "personal-state", Selector: JobSelector{Container: &JobContainer{Kind: "album", ID: homeItem(t, db, "album").Public}}})
	if err != nil {
		t.Fatal(err)
	}
	members, err := scanInt64s(db.Query(query, args...))
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, member := range members {
			var id string
			if err := tx.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, member).Scan(&id); err != nil {
				return err
			}
			if err := s.applyJobPersonal(tx, "p", id, JobPersonalArgs{Watchlist: &yes}); err != nil {
				return err
			}
		}
		return nil
	})
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM personal_items WHERE profile_id='p' AND watchlisted=1`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("permitted leaf My List state: %d %v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM personal_items WHERE profile_id='p' AND item_id IN(?,?)`, homeItem(t, db, "album").ID, homeItem(t, db, "t2").ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bulk state fabricated a title flag or mutated a hidden child: %d %v", count, err)
	}
}

func TestHomeActionAvailabilityPredicateSeeksOnlyReturnedMembershipRanges(t *testing.T) {
	db := homeFixture(t)
	v := homeRequestFixture("tv").Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	predicate, args := homeActionAvailableSQL(v)
	args = append(args, homeItem(t, db, "show").Public)
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT `+predicate+` FROM catalog_entities e
 JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_item_availability a ON a.entity_id=e.id AND a.retired=0
 WHERE e.public_id=pid_blob(?)`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var membershipSeek, availabilitySeek bool
	for rows.Next() {
		var node, parent, unused int
		var detail string
		if err := rows.Scan(&node, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SEARCH home_member USING PRIMARY KEY (entity_id=?)") {
			membershipSeek = true
		}
		if strings.Contains(detail, "SEARCH available USING INTEGER PRIMARY KEY") {
			availabilitySeek = true
		}
		if strings.Contains(detail, "SCAN home_member") || strings.Contains(detail, "SCAN available") {
			t.Fatalf("availability predicate scans outside the returned membership range: %s", detail)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !membershipSeek || !availabilitySeek {
		t.Fatalf("indexed membership/availability probes absent: membership=%v availability=%v", membershipSeek, availabilitySeek)
	}
}

func TestHomeActionSelectionDoesNotOfferUnavailableJobServices(t *testing.T) {
	db := homeFixture(t)
	rows := []HomeRow{{Entries: []ContentEntry{
		{ID: homeItem(t, db, "m1").Public, Kind: "movie", LibraryID: "movies"},
		{ID: homeItem(t, db, "show").Public, Kind: "show", LibraryID: "tv"},
	}}}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		return New(db).WithContext(ctx).ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{Personal: true, Organize: true, Refresh: true}, rows, nil)
	}); err != nil {
		t.Fatal(err)
	}
	leaf, aggregate := rows[0].Entries[0].HomeActions, rows[0].Entries[1].HomeActions
	if leaf == nil || !leaf.MyList || !leaf.Favorite || !leaf.Watched || !leaf.Playlist || !leaf.RefreshMetadata {
		t.Fatalf("real single-title commands were lost with scheduler absent: %+v", leaf)
	}
	if leaf.Selection != (HomeSelectionActions{}) {
		t.Fatalf("leaf selection borrowed unavailable jobs: %+v", leaf.Selection)
	}
	if aggregate == nil || aggregate.Selection.MyList || aggregate.Selection.Playlist || aggregate.Selection.Collection || aggregate.Selection.Refresh || !aggregate.Selection.Favorite || !aggregate.Selection.Watched {
		t.Fatalf("aggregate selection did not distinguish direct personal commands from jobs: %+v", aggregate)
	}
}

func TestHomeNotInterestedOfferUsesPersonalWriterSupportAndVisibleIdentity(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	for _, personal := range []bool{true, false} {
		rows := []HomeRow{{Kind: "recent", Entries: []ContentEntry{}}}
		for _, key := range []string{"m1", "show", "e101", "s1", "album", "artist", "t1", "book", "p1"} {
			item := homeItem(t, db, key)
			var kind, library string
			if err := db.QueryRow(`SELECT k.name,l.library_id FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id WHERE e.id=?`, item.ID).Scan(&kind, &library); err != nil {
				t.Fatal(err)
			}
			rows[0].Entries = append(rows[0].Entries, ContentEntry{ID: item.Public, Kind: kind, LibraryID: library})
		}
		show := rows[0].Entries[1]
		show.Navigation = &ContentNavigation{View: "show", EntityID: homeItem(t, db, "m1").Public}
		rows[0].Entries = append(rows[0].Entries, show)
		if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			return s.WithContext(ctx).ProjectHomeActions(homeRequestFixture().Viewer, HomeActionAuthority{Personal: personal}, rows, nil)
		}); err != nil {
			t.Fatal(err)
		}
		for i, entry := range rows[0].Entries {
			expected := personal && i != 3 && i != 5 && i != 9
			if entry.HomeActions == nil || entry.HomeActions.NotInterested != expected {
				t.Fatalf("personal=%v kind=%s index=%d actions=%+v", personal, entry.Kind, i, entry.HomeActions)
			}
			if entry.HomeActions.Selection != selectionOffers(HomeActionAuthority{Personal: personal}, entry.Kind, false, false) {
				t.Fatal("Not interested changed bulk offers")
			}
		}
	}
}
