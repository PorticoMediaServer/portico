package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func listeningFixture(t *testing.T) (*Service, *sql.DB, catalogtest.Names) {
	t.Helper()
	s, c, names := phase34ListeningFixture(t)
	return s, c.DB, names
}
func listeningRequest(library, view, entity string) ContentRequest {
	return ContentRequest{Viewer: Viewer{Profile: "local:account:profile", Fence: "viewer", Libraries: []string{library}}, ServerID: "server", Library: library, Profile: "local:account:profile", ViewerFence: "viewer", View: view, EntityID: entity, Limit: 100}
}

func TestListeningMembershipVisibilityUsesIndexedPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, db, names := listeningFixture(t)
	for _, kind := range []string{"artist", "disc"} {
		predicate, args := listeningSectionVisibility(kind, Viewer{Profile: "local:account:profile", Libraries: []string{"music"}})
		base := `SELECT projected.entity_id FROM (SELECT id entity_id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5) projected WHERE `
		baseArgs := []any{names["artist"].Public}
		if kind == "disc" {
			base = `SELECT projected.entity_id FROM (SELECT ? entity_id,1 disc_number) projected WHERE `
			baseArgs = []any{names["album"].ID}
		}
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+base+predicate, append(baseArgs, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				break
			}
			plan.WriteString(detail)
			plan.WriteByte('\n')
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		got := plan.String()
		if strings.Contains(got, "SCAN member") || strings.Contains(got, "SCAN i") {
			t.Fatalf("%s membership scanned songs:\n%s", kind, got)
		}
		if kind == "artist" && (!strings.Contains(got, "catalog_albums_artist") || !strings.Contains(got, "catalog_songs_album_order") || !strings.Contains(got, "catalog_song_artists_artist")) {
			t.Fatalf("artist paths lost indexes:\n%s", got)
		}
		if kind == "disc" && !strings.Contains(got, "catalog_songs_listening_order") {
			t.Fatalf("disc path lost index:\n%s", got)
		}
	}
}

func TestOrderedMusicSelectionWalksOrderIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	request := listeningRequest("music", "browse", "")
	page, err := s.ListeningSelection(request, ListeningTarget{"music", "library", "music"}, "ordered", "", false, "")
	if err != nil || len(page.Entries) != 100 || page.Entries[0].ItemID != names["song-001"].Public || page.NextCursor == "" || page.TotalCount != 204 || page.UnavailableCount != 1 {
		t.Fatalf("library order: %+v %v", page, err)
	}
	request.Cursor = page.NextCursor
	page, err = s.ListeningSelection(request, ListeningTarget{"music", "library", "music"}, "ordered", "", false, "")
	if err != nil || len(page.Entries) != 100 || page.Entries[0].ItemID != names["song-101"].Public {
		t.Fatalf("library continuation: %+v %v", page, err)
	}
	request.Cursor = ""
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, names["song-205"].Asset, true)
	})
	c.Drain()
	page, err = s.ListeningSelection(request, ListeningTarget{"music", "library", "music"}, "ordered", "", false, "")
	if err != nil || page.TotalCount != 205 || page.UnavailableCount != 0 {
		t.Fatalf("availability bucket did not update listening counts: %+v %v", page, err)
	}
	queries := []struct {
		query string
		index string
		args  []any
	}{
		{`SELECT i.id FROM catalog_browse_rows ar INDEXED BY catalog_browse_title
			CROSS JOIN catalog_albums a ON a.entity_id=ar.entity_id
			CROSS JOIN catalog_songs s INDEXED BY catalog_songs_listening_order ON s.album_id=a.entity_id
			CROSS JOIN catalog_entities i ON i.id=s.entity_id
			WHERE ar.library_id=(SELECT id FROM catalog_libraries WHERE library_id='music') AND ar.kind=6 AND i.library_id=ar.library_id AND i.kind=7
			ORDER BY ar.sort_key COLLATE NOCASE,ar.entity_id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id LIMIT 100`, "catalog_browse_title", nil},
		{`SELECT i.id FROM catalog_songs s INDEXED BY catalog_songs_listening_order
			CROSS JOIN catalog_entities i ON i.id=s.entity_id
			WHERE s.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=6) AND i.library_id=(SELECT id FROM catalog_libraries WHERE library_id='music') AND i.kind=7
			ORDER BY COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id LIMIT 100`, "catalog_songs_listening_order", []any{names["album"].Public}},
		{`SELECT i.id FROM catalog_book_files f INDEXED BY catalog_book_files_listening_order
			CROSS JOIN catalog_entities i ON i.id=f.entity_id
			WHERE f.book_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=8) AND i.library_id=(SELECT id FROM catalog_libraries WHERE library_id='books') AND i.kind=9
			ORDER BY COALESCE(f.disc_number,1),COALESCE(f.part_number,0),f.entity_id LIMIT 100`, "catalog_book_files_listening_order", []any{names["book-a"].Public}},
	}
	for _, tc := range queries {
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				break
			}
			plan.WriteString(detail)
			plan.WriteByte('\n')
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got := plan.String(); !strings.Contains(got, tc.index) || strings.Contains(got, "USE TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("ordered page lost its indexed walk:\n%s", got)
		}
	}
}
func TestOrderedBookSelectionContinuesByPartKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	c := catalogtest.New(t, db)
	for part := 3; part <= 103; part++ {
		id := fmt.Sprintf("part-a%03d", part)
		names[id] = c.BookFile(names["book-a"], part, "/books/"+id+".m4b")
	}
	c.Drain()
	r := listeningRequest("books", "book", names["book-a"].Public)
	target := ListeningTarget{"books", "book", names["book-a"].Public}
	first, err := s.ListeningSelection(r, target, "ordered", "", false, "")
	if err != nil || first.TotalCount != 103 || len(first.Entries) != 100 || first.Entries[0].ItemID != names["part-a1"].Public || first.NextCursor == "" {
		t.Fatalf("first book page: %+v %v", first, err)
	}
	r.Cursor = first.NextCursor
	second, err := s.ListeningSelection(r, target, "ordered", "", false, "")
	if err != nil || len(second.Entries) != 3 || second.Entries[0].ItemID != names["part-a101"].Public || second.NextCursor != "" {
		t.Fatalf("continued book page: %+v %v", second, err)
	}
}
func TestListeningNavigationDiscsAuthorsAndSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, names := listeningFixture(t)
	for _, view := range []string{"discover", "browse", "releases", "songs"} {
		p, e := s.Content(listeningRequest("music", view, ""))
		if e != nil || len(p.Sections) == 0 || p.Listening == nil {
			t.Fatalf("%s: %+v %v", view, p, e)
		}
	}
	r := listeningRequest("music", "album", names["album"].Public)
	r.Sort = "track"
	p, e := s.Content(r)
	if e != nil || p.Query.Sort != "track" || len(p.Sections) != 2 {
		t.Fatal(p, e)
	}
	if p.Sections[0].Entries[0].ID != names["album"].Public+":disc:1" || p.Sections[1].Entries[0].ID != names["song-001"].Public {
		t.Fatal("lost disc/track order", p.Sections)
	}
	p, e = s.Content(listeningRequest("music", "disc", names["album"].Public+":disc:2"))
	if e != nil || p.Sections[0].TotalCount != 100 || p.Sections[0].Entries[0].ID != names["song-101"].Public {
		t.Fatal(p, e)
	}
	for _, view := range []string{"authors", "series"} {
		p, e = s.Content(listeningRequest("books", view, ""))
		if e != nil || len(p.Sections) != 1 || len(p.Sections[0].Entries) != 1 {
			t.Fatal(view, p, e)
		}
		entry := p.Sections[0].Entries[0]
		child, e := s.Content(listeningRequest("books", entry.Navigation.View, entry.ID))
		if e != nil || child.Sections[0].TotalCount != 2 {
			t.Fatal(child, e)
		}
		if view == "series" && child.Sections[0].Entries[0].ID != names["book-b"].Public {
			t.Fatal("series is not numeric evidence order", child)
		}
	}
	r = listeningRequest("books", "book", names["book-a"].Public)
	r.Sort = "part"
	p, e = s.Content(r)
	if e != nil || p.Query.Sort != "part" || len(p.Sections) != 3 || p.Sections[0].Entries[0].Playback.ItemID != names["part-a1"].Public {
		t.Fatal(p, e)
	}
	if _, e = s.Content(listeningRequest("other", "album", names["album"].Public)); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("cross library parent accepted", e)
	}
}

func TestAudiobookBrowseDoesNotBuildNavigationGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _ := listeningFixture(t)
	c := catalogtest.New(t, db)
	for i := 0; i < 70; i++ {
		c.Book(c.Handle("books"), fmt.Sprintf("Fresh %02d", i), fmt.Sprintf("Author %02d", i))
	}
	before := dbwork.Publications()
	for _, view := range []string{"browse", "authors", "series"} {
		if _, err := s.Content(listeningRequest("books", view, "")); err != nil {
			t.Fatal(view, err)
		}
	}
	if dbwork.Publications() != before {
		t.Fatal("audiobook browse wrote a group on the request path")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM listening_book_groups WHERE library_id='books' AND kind='author'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("request materialized new authors", count, err)
	}
	beforeRevision, err := s.ContentRevision("books", "local:account:profile")
	if err != nil {
		t.Fatal(err)
	}
	defer func(size int) { listeningGroupBatch = size }(listeningGroupBatch)
	listeningGroupBatch = 64
	more, err := s.RefreshListeningGroups(context.Background())
	if err != nil || !more {
		t.Fatal("first background batch was not bounded", more, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM listening_book_groups WHERE library_id='books' AND kind='author'`).Scan(&count); err != nil || count <= 1 || count >= 71 {
		t.Fatal("first batch count", count, err)
	}
	afterRevision, err := s.ContentRevision("books", "local:account:profile")
	if err != nil || afterRevision.Catalog <= beforeRevision.Catalog {
		t.Fatal("group publication did not invalidate content cursors", beforeRevision, afterRevision, err)
	}
	more, err = s.RefreshListeningGroups(context.Background())
	if err != nil || more {
		t.Fatal("second background batch did not drain", more, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM listening_book_groups WHERE library_id='books' AND kind='author'`).Scan(&count); err != nil || count != 71 {
		t.Fatal("drained count", count, err)
	}
}
func TestListeningWholeSelectionMixAndContinuationFence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	r := listeningRequest("music", "browse", "")
	target := ListeningTarget{"music", "album", names["album"].Public}
	beforeFirst := dbwork.Reads().Statements
	first, e := s.ListeningSelection(r, target, "ordered", "", false, "")
	firstStatements := dbwork.Reads().Statements - beforeFirst
	if e != nil || len(first.Entries) != 100 || first.TotalCount != 204 || first.UnavailableCount != 1 {
		t.Fatal(first, e)
	}
	seen := []string{}
	page := first
	measuredNext := false
	for {
		for _, entry := range page.Entries {
			seen = append(seen, entry.ItemID)
		}
		if page.NextCursor == "" {
			break
		}
		r.Cursor = page.NextCursor
		beforeNext := dbwork.Reads().Statements
		page, e = s.ListeningSelection(r, target, "ordered", "", false, "")
		if e != nil {
			t.Fatal(e)
		}
		if !measuredNext {
			measuredNext = true
			if nextStatements := dbwork.Reads().Statements - beforeNext; nextStatements >= firstStatements {
				t.Fatalf("keyset continuation repeated the first page's full count: first %d, next %d statements", firstStatements, nextStatements)
			}
		}
	}
	if len(seen) != 204 || seen[0] != names["song-001"].Public || seen[203] != names["song-204"].Public {
		t.Fatal("partial or reordered album", seen)
	}
	r.Cursor = ""
	a, e := s.ListeningSelection(r, target, "mix", "known-seed", false, "")
	if e != nil {
		t.Fatal(e)
	}
	b, e := New(db).ListeningSelection(r, target, "mix", "known-seed", false, "")
	if e != nil || !reflect.DeepEqual(a.Entries, b.Entries) {
		t.Fatal("mix not reproducible", e)
	}
	for _, entry := range a.Entries {
		if entry.ItemID == names["outside"].Public {
			t.Fatal("mix escaped named library")
		}
	}
	r.Cursor = first.NextCursor
	r.Profile = "other-profile"
	if _, e = s.ListeningSelection(r, target, "ordered", "", false, ""); !errors.Is(e, ErrCursor) {
		t.Fatal("cross viewer continuation", e)
	}
	r.Profile = "local:account:profile"
	if _, e = db.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('music',?,'genre','embedded','Jazz')`, names["song-001"].Token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ListeningSelection(r, target, "ordered", "", false, ""); e != nil {
		t.Fatal("local evidence prevented continuation", e)
	}
}
func TestListeningBookResumeUnavailableAndExplicitPartRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	c := catalogtest.New(t, db)
	r := listeningRequest("books", "book", names["book-a"].Public)
	target := ListeningTarget{"books", "book", names["book-a"].Public}
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES(?,?,?,'old-session',125000,0,0)`, r.Profile, names["book-a"].ID, names["part-a2"].ID)
	p, e := s.ListeningSelection(r, target, "ordered", "", true, "")
	if e != nil || p.Resume == nil || p.Resume.PositionSeconds != 125 || len(p.Entries) != 1 || p.Entries[0].ItemID != names["part-a2"].Public {
		t.Fatal(p, e)
	}
	other := r
	other.Profile = "another-profile"
	other.Viewer.Profile = "another-profile"
	p, e = s.ListeningSelection(other, target, "ordered", "", true, "")
	if e != nil || p.Resume != nil || len(p.Entries) != 2 {
		t.Fatal("resume scope leaked", p, e)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, names["part-a2"].Asset, false)
	})
	c.Drain()
	if _, e = s.ListeningSelection(r, target, "ordered", "", true, ""); !errors.Is(e, ErrListeningResumeUnavailable) {
		t.Fatal(e)
	}
	c.Drain()
	content, e := s.Content(r)
	if e != nil || !content.Listening.ResumeUnavailable {
		t.Fatal(content, e)
	}
	p, e = s.ListeningSelection(r, target, "ordered", "", false, names["part-a1"].Public)
	if e != nil || len(p.Entries) != 1 || p.UnavailableCount != 1 {
		t.Fatal(p, e)
	}
	info, e := s.ListeningItem(Viewer{Libraries: []string{"books"}}, names["part-a1"].Public)
	if e != nil || info.LastBookItemID != names["part-a2"].Public || info.BookEndAvailable == nil || *info.BookEndAvailable {
		t.Fatal(info, e)
	}
	song, e := s.ListeningItem(Viewer{Libraries: []string{"music"}}, names["song-001"].Public)
	raw, _ := json.Marshal(song)
	var wire map[string]any
	json.Unmarshal(raw, &wire)
	if e != nil || wire["bookId"] != nil || wire["artistId"] != names["artist"].Public {
		t.Fatal(string(raw), e)
	}
}
func TestListeningPreferencesCASAndProfileIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _ := listeningFixture(t)
	allow := func(context.Context, *sql.Tx, string) error { return nil }
	p, e := s.ListeningPreferences(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p"}, allow)
	if e != nil || p.BookRate != 1 || p.Revision != 1 {
		t.Fatal(p, e)
	}
	old := p
	p.BookRate = 1.5
	p.PassoutMinutes = 30
	saved, e := s.SaveListeningPreferences(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p"}, p, allow)
	if e != nil || saved.Revision != 2 {
		t.Fatal(saved, e)
	}
	if _, e = s.SaveListeningPreferences(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p"}, old, allow); !errors.Is(e, ErrListeningPreferencesConflict) {
		t.Fatal("stale save accepted", e)
	}
	other, e := s.ListeningPreferences(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "other"}, allow)
	if e != nil || other.BookRate != 1 || other.PassoutMinutes != 0 {
		t.Fatal("preferences escaped profile", other, e)
	}
	values, _, _, e := operations.EffectivePreferences(db, identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p"}, "")
	if e != nil || values.Float("audiobooks.defaultSpeed") != 1.5 || values.Int("playback.sleepTimerMinutes") != 30 {
		t.Fatal("registry and listening disagree", values, e)
	}
	invalid := saved
	invalid.BookRate = 9
	if _, e = s.SaveListeningPreferences(identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "p"}, invalid, allow); e == nil {
		t.Fatal("unsupported speed saved")
	}
}

func TestListeningSelectionBoundsMixBeforeScoring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	c := catalogtest.New(t, db)
	music := c.Handle("music")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 206; i <= 650; i++ {
			id := fmt.Sprintf("song-%04d", i)
			path := "/music/" + id + ".mp3"
			itemID, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: music, Kind: compactcatalog.Track, Parent: names["album"].ID,
				Key: compactcatalog.ItemKey("/music", path, 0), Title: id,
			})
			if err != nil {
				return err
			}
			if err := compactcatalog.SetFactsTx(ctx, tx, itemID, map[string]any{
				"album_id": names["album"].ID, "disc_number": 1, "track_number": i,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	r := listeningRequest("music", "browse", "")
	target := ListeningTarget{"music", "library", "music"}
	out, e := s.ListeningSelection(r, target, "mix", "bounded", false, "")
	if e != nil {
		t.Fatal(e)
	}
	if out.TotalCount+out.UnavailableCount > 640 {
		t.Fatal("unbounded candidate pool", out.TotalCount, out.UnavailableCount)
	}
	// Ordered playback still represents the complete library, including missing
	// tracks in its unavailable count, while returning only a requested page.
	out, e = s.ListeningSelection(r, target, "ordered", "", false, "")
	if e != nil || out.TotalCount != 204 || out.UnavailableCount != 446 || len(out.Entries) != 100 {
		t.Fatal(out, e)
	}
}

// B85: the background step reads nothing while no audiobook change arrived,
// runs a complete pass after one, and a pass reads each book once.
func TestListeningGroupStepRunsOnlyAfterAChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _ := listeningFixture(t)
	c := catalogtest.New(t, db)
	defer func(size int, gap time.Duration) { listeningGroupBatch, listeningGroupPassGap = size, gap }(listeningGroupBatch, listeningGroupPassGap)
	listeningGroupBatch, listeningGroupPassGap = 16, 0
	changed := worker.NewSignal()
	step := s.ListeningGroupStep(changed)
	ctx := context.Background()
	drain := func() {
		t.Helper()
		for n := 0; ; n++ {
			if n > 100 {
				t.Fatal("pass did not finish")
			}
			if wait := step(ctx); wait != time.Millisecond {
				return
			}
		}
	}
	drain() // the startup pass
	before := dbwork.DatabaseCalls()
	if wait := step(ctx); wait != 0 || dbwork.DatabaseCalls() != before {
		t.Fatal("an idle step read the database", wait, dbwork.DatabaseCalls()-before)
	}
	for i := 0; i < 40; i++ {
		c.Book(c.Handle("books"), fmt.Sprintf("Late %02d", i), fmt.Sprintf("Writer %02d", i%7))
	}
	changed.Wake()
	drain()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM listening_book_groups WHERE library_id='books' AND kind='author' AND name LIKE 'Writer %'`).Scan(&count); err != nil || count != 7 {
		t.Fatal("pass after a change", count, err)
	}
}

// B7: an artist selection starts from the artist's own songs (album-artist
// albums and credited songs) and pages them in the same order as the album,
// never testing every song of the library.
func TestListeningAlbumEntityCarriesYearDurationAndArtist(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, names := listeningFixture(t)
	r := listeningRequest("music", "album", names["album"].Public)
	r.Sort = "track"
	p, e := s.Content(r)
	if e != nil || p.Entity == nil {
		t.Fatal(p, e)
	}
	entity := p.Entity
	if entity.Subtitle != "Artist" {
		t.Fatalf("subtitle changed: %q", entity.Subtitle)
	}
	if entity.Year == nil || *entity.Year != 2020 {
		t.Fatalf("year missing: %+v", entity)
	}
	if entity.Artist == nil || entity.Artist.ID != names["artist"].Public || entity.Artist.Name != "Artist" {
		t.Fatalf("artist link missing: %+v", entity)
	}
	if entity.Duration == nil || *entity.Duration != 205*300 {
		t.Fatalf("duration is not the track total: %+v", entity)
	}
	disc, e := s.Content(listeningRequest("music", "disc", names["album"].Public+":disc:2"))
	if e != nil || disc.Entity == nil {
		t.Fatal(disc, e)
	}
	if disc.Entity.Year == nil || *disc.Entity.Year != 2020 || disc.Entity.Artist == nil || disc.Entity.Artist.ID != names["artist"].Public {
		t.Fatalf("disc lost the album facts: %+v", disc.Entity)
	}
	if disc.Entity.Duration == nil || *disc.Entity.Duration != 100*300 {
		t.Fatalf("disc duration is not its own tracks: %+v", disc.Entity)
	}
}

func TestListeningArtistMostPlayedRanksTheViewersPlays(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	// The list reads the maintained counts (personal_play_counts), not history rows.
	plays := func(id string, n int) {
		t.Helper()
		if _, e := db.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES(?,?,?)`, "local:account:profile", names[id].ID, n); e != nil {
			t.Fatal(e)
		}
	}
	plays("song-002", 3)
	plays("song-001", 5)
	plays("song-003", 1)
	plays("outside", 10)
	p, e := s.Content(listeningRequest("music", "artist", names["artist"].Public))
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Sections) != 3 || p.Sections[0].ID != "songs" || p.Sections[1].ID != "popularTracks" || p.Sections[2].ID != "releases" {
		t.Fatalf("popular tracks misplaced: %+v", p.Sections)
	}
	popular := p.Sections[1]
	if popular.TotalCount != 3 || len(popular.Entries) != 3 || popular.Entries[0].ID != names["song-001"].Public || popular.Entries[1].ID != names["song-002"].Public || popular.Entries[2].ID != names["song-003"].Public {
		t.Fatalf("not the viewer's top songs: %+v", popular)
	}
}

func TestListeningArtistReleasesMarkAlbumAndAppearance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	c := catalogtest.New(t, db)
	guest := c.Artist(c.Handle("music"), "Guest")
	guestAlbum := c.Album(guest, "Guest Release", 2021)
	names["guest-album"] = guestAlbum
	guestSong := c.Entity(compactcatalog.Entity{Library: c.Handle("music"), Kind: compactcatalog.Track, Parent: guestAlbum.ID, Key: compactcatalog.ItemKey("/music", "/music/guest-song.mp3", 0), Title: "Guest Song"}, map[string]any{"album_id": guestAlbum.ID, "disc_number": 1, "track_number": 1})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, guestSong.ID, []int64{names["artist"].ID})
	})
	c.Drain()
	p, e := s.Content(listeningRequest("music", "artist", names["artist"].Public))
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Sections) != 2 {
		t.Fatalf("no-history artist gained a section: %+v", p.Sections)
	}
	releases := p.Sections[1]
	if releases.ID != "releases" || len(releases.Entries) != 2 {
		t.Fatalf("releases lost: %+v", p.Sections)
	}
	roles := map[string]string{}
	for _, entry := range releases.Entries {
		roles[names.Of(entry.ID)] = entry.Role
	}
	if roles["album"] != "album" || roles["guest-album"] != "appearance" {
		t.Fatalf("roles wrong: %+v", roles)
	}
}

func TestListeningArtistSelectionSeeksTheArtistsSongs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	collect := func(target ListeningTarget) ([]string, int, int) {
		t.Helper()
		r := listeningRequest("music", "browse", "")
		ids, total, missing := []string{}, 0, 0
		for n := 0; ; n++ {
			page, err := s.ListeningSelection(r, target, "ordered", "", false, "")
			if err != nil || n > 10 {
				t.Fatal(target, err)
			}
			if n == 0 {
				total, missing = page.TotalCount, page.UnavailableCount
			}
			for _, entry := range page.Entries {
				ids = append(ids, entry.ItemID)
			}
			if page.NextCursor == "" {
				return ids, total, missing
			}
			r.Cursor = page.NextCursor
		}
	}
	albumIDs, albumTotal, albumMissing := collect(ListeningTarget{"music", "album", names["album"].Public})
	artistIDs, artistTotal, artistMissing := collect(ListeningTarget{"music", "artist", names["artist"].Public})
	if strings.Join(artistIDs, ",") != strings.Join(albumIDs, ",") || artistTotal != albumTotal || artistMissing != albumMissing {
		t.Fatalf("artist selection differs from its only album: %d/%d vs %d/%d, %d vs %d ids", artistTotal, artistMissing, albumTotal, albumMissing, len(artistIDs), len(albumIDs))
	}
	where, args, err := s.listeningWhere(ListeningTarget{"music", "artist", names["artist"].Public})
	if err != nil {
		t.Fatal(err)
	}
	query := `EXPLAIN QUERY PLAN SELECT i.id FROM (SELECT s2.entity_id FROM catalog_albums a2 INDEXED BY catalog_albums_artist CROSS JOIN catalog_songs s2 ON s2.album_id=a2.entity_id WHERE a2.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5)
			UNION SELECT sa2.song_id FROM catalog_song_artists sa2 INDEXED BY catalog_song_artists_artist WHERE sa2.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5)) member
			CROSS JOIN catalog_entities i ON i.id=member.entity_id WHERE i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where
	rows, err := db.Query(query, append([]any{names["artist"].Public, names["artist"].Public, "music"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if strings.Contains(plan, "SCAN i") {
		t.Fatalf("artist selection walks the library:\n%s", plan)
	}
}

func TestListeningBookResumeUsesOnlySelectedAmount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := listeningFixture(t)
	c := catalogtest.New(t, db)
	r := listeningRequest("books", "book", names["book-a"].Public)
	target := ListeningTarget{"books", "book", names["book-a"].Public}
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position,unit,completed) VALUES(?,?,?,'stored',2000,0,0)`, r.Profile, names["book-a"].ID, names["part-a2"].ID)
	for _, ms := range []int{0, 5000, 15000, 30000} {
		r.ResumeRewindMs = ms
		page, err := s.ListeningSelection(r, target, "ordered", "", true, "")
		want := 2.0
		if ms > 0 {
			want = 0
		}
		if err != nil || page.Resume == nil || page.Resume.PositionSeconds != want {
			t.Fatalf("choice %d: %+v %v", ms, page.Resume, err)
		}
	}
	r.ResumeRewindMs = 7000
	if _, err := s.ListeningSelection(r, target, "ordered", "", true, ""); err == nil {
		t.Fatal("unsupported internal amount admitted")
	}
}
