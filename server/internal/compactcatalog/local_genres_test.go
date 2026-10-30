package compactcatalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The bounded existing-library path for the local audio genre projection: the
// scan stored selected evidence before that projection existed, and an
// unchanged-file rescan never re-enters the analysis commit, so a catalog
// upgrade projects from the stored selected evidence in durable batches
// through the same shared projection the scanner uses.

// genreItem creates an audio item of kind with one linked file whose selected
// genre evidence is genre ("" for none).
func genreItem(t *testing.T, db *sql.DB, library int64, key, title string, kind Kind, genre string) int64 {
	t.Helper()
	ident := key
	if ident == "" {
		ident = title
	}
	id := cc1Entity(t, db, Entity{Library: library, Kind: kind, Key: "file:" + ident, Title: title}, nil)
	if key != "" {
		genreFile(t, db, id, key, genre)
	}
	return id
}

func genreFile(t *testing.T, db *sql.DB, item int64, key, genre string) {
	t.Helper()
	var token string
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		asset, tok, err := UpsertAssetTx(ctx, tx, Asset{Path: "/music/" + key + ".m4a", Size: 1, ModifiedNS: 1, Container: "m4a", VideoCodec: "mpeg4", AudioCodec: "aac", Duration: 12})
		if err != nil {
			return err
		}
		token = tok
		return LinkAssetTx(ctx, tx, item, asset, Link{})
	})
	if genre != "" {
		cc1Exec(t, db, `INSERT INTO audio_tag_evidence VALUES('music',?,'genre','portico-json',?)`, token, genre)
	}
}

func audioGenreBackfillFixture(t *testing.T) (context.Context, *sql.DB, map[string]int64) {
	t.Helper()
	db := cc1OpenDB(t, "audio-genres.sqlite")
	// A music library whose policy has already been revised three times, with
	// two items whose selected evidence carries genre spellings that must
	// normalize to one shared facet, and one audiobook item with no evidence.
	music := cc1Library(t, db, "music", "Music", "music", "/music")
	cc1Exec(t, db, `UPDATE audio_metadata_policies SET revision=3 WHERE library_id='music'`)
	ids := map[string]int64{
		"song-a": genreItem(t, db, music, "song-a", "Track song-a", Track, "Heist, Crime"),
		"song-b": genreItem(t, db, music, "song-b", "Track song-b", Track, "HEIST ; CRIME"),
		"book-a": genreItem(t, db, music, "book-a", "Part One", Part, ""),
	}
	return context.Background(), db, ids
}

func audioGenreProjectionRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT source_id||'='||source_name FROM catalog_term_sources WHERE provider='local' ORDER BY source_id,source_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var row string
		if err = rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAudioGenreCatchUpRespectsOwnerLock(t *testing.T) {
	ctx, db, ids := audioGenreBackfillFixture(t)
	// The owner locked one item's genre class before the catch-up ran: its
	// projected set must stay exactly what the owner left.
	cc1Exec(t, db, `INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at)
 VALUES('item',?,'genre','[]','owner_lock',1,'owner','2026-01-01T00:00:00Z')`, ids["song-a"])
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, ids["song-a"], VocabGenre, "local", []Term{{SourceID: "owned genre", Name: "Owned Genre", Key: "owned genre"}})
	})
	if _, err := ProjectAudioLocalGenresBatch(ctx, db, "music"); err != nil {
		t.Fatal(err)
	}
	genres := strings.Join(audioGenreProjectionRows(t, db), ",")
	// song-b (unlocked) carries its own uppercase spelling; song-a's own
	// mixed-case forms must be absent because its class was locked.
	if !strings.Contains(genres, "owned genre=Owned Genre") {
		t.Fatal("the owner set vanished")
	}
	if strings.Contains(genres, "crime=Crime,") || strings.Contains(genres, "heist=Heist,") || strings.Contains(genres, "heist=Heist") && !strings.Contains(genres, "heist=HEIST") {
		t.Fatalf("the locked class was rewritten by the catch-up: %v", genres)
	}
	// The unlocked item still projected, from its own selected evidence.
	if !strings.Contains(genres, "crime=CRIME") || !strings.Contains(genres, "heist=HEIST") {
		t.Fatalf("the unlocked item was skipped: %v", genres)
	}
}

func TestAudioGenreCatchUpBatchCursorIsDurable(t *testing.T) {
	// The exact pre-upgrade shape the fixture hit: a catalog larger than one
	// batch whose projection state row does not exist at all, because the
	// policy was never revised after the library was created. The first pass
	// must advance its cursor through an upsert or it repeats the first
	// window for ever and starves the other libraries.
	path := filepath.Join(t.TempDir(), "boundary.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	music := cc1Library(t, db, "music", "Music", "music", "/music")
	// 60 items: many full batches plus a remainder.
	for i := 0; i < 60; i++ {
		id := "item-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		genreItem(t, db, music, id, "Track "+id, Track, "Jazz")
	}
	// The catalog carries no projection state yet.
	var states int
	if err = db.QueryRow(`SELECT count(*) FROM audio_genre_projection`).Scan(&states); err != nil || states != 0 {
		t.Fatalf("fixture must start without projection state: %d %v", states, err)
	}
	ctx := context.Background()
	due, err := AudioLocalGenreProjectionDue(ctx, db)
	if err != nil || len(due) != 1 || due[0] != "music" {
		t.Fatal(due, err)
	}
	more, err := ProjectAudioLocalGenresBatch(ctx, db, "music")
	if err != nil || !more {
		t.Fatalf("the first pass of a >batch catalog must report more work: %v %v", more, err)
	}
	// The cursor must be persisted through the upsert, or the first window
	// would repeat for ever.
	var cursor, completed string
	if err = db.QueryRow(`SELECT cursor,completed_at FROM audio_genre_projection WHERE library_id='music'`).Scan(&cursor, &completed); err != nil {
		t.Fatalf("the first pass did not create the projection state: %v", err)
	}
	if cursor == "" || completed != "" {
		t.Fatalf("persisted cursor wrong: cursor=%q completed=%q", cursor, completed)
	}
	if n := audioGenreScalarQuery(t, db); n != audioGenreBatch {
		t.Fatalf("the first pass projected %d rows, not exactly one batch", n)
	}
	// Reopen the same database (a restart during the catch-up) and resume.
	db.Close()
	db2, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	for passes := 1; more; passes++ {
		if passes > 60/audioGenreBatch+1 {
			t.Fatal("the catch-up did not finish in as many passes as it has batches")
		}
		if more, err = ProjectAudioLocalGenresBatch(ctx, db2, "music"); err != nil {
			t.Fatal(err)
		}
	}
	if n := audioGenreScalarQuery(t, db2); n != 60 {
		t.Fatalf("the catch-up did not project every item: %d rows", n)
	}
	if due, err = AudioLocalGenreProjectionDue(ctx, db2); err != nil || len(due) != 0 {
		t.Fatalf("the completed catch-up is still due: %v %v", due, err)
	}
	// An idle pass changes nothing at all.
	before := audioGenreScalarQuery(t, db2)
	if _, err = ProjectAudioLocalGenresBatch(ctx, db2, "music"); err != nil {
		t.Fatal(err)
	}
	if due, _ = AudioLocalGenreProjectionDue(ctx, db2); len(due) != 0 {
		t.Fatal("an idle pass re-opened the projection")
	}
	if after := audioGenreScalarQuery(t, db2); after != before {
		t.Fatalf("an idle pass wrote: %d -> %d", before, after)
	}
}

func audioGenreScalarQuery(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_term_sources WHERE provider='local'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAudioGenreConflictingLinkedAssetsStayEvidence(t *testing.T) {
	// Two linked sources that normalize to different facet sets are a
	// conflict: nothing may pick a winner by traversal order, so nothing is
	// published or erased — the previously published set keeps serving.
	ctx, db, _ := audioGenreBackfillFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		cc1Exec(t, db, query, args...)
	}
	var music int64
	if err := db.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id='music'`).Scan(&music); err != nil {
		t.Fatal(err)
	}
	book := genreItem(t, db, music, "", "Part", Part, "")
	genreFile(t, db, book, "b1", "Thriller")
	genreFile(t, db, book, "b2", "Jazz")
	if _, err := ProjectAudioLocalGenresBatch(ctx, db, "music"); err != nil {
		t.Fatal(err)
	}
	genres := strings.Join(audioGenreProjectionRows(t, db), ",")
	// song-a/song-b agree with themselves and project normally; book-b's two
	// linked sources disagree, so nothing is picked for it and nothing was
	// erased.
	if !strings.Contains(genres, "heist=") || strings.Contains(genres, "thriller=") {
		t.Fatalf("a conflicting item was resolved by traversal order: %v", genres)
	}
	// When the sources agree (same facet set), the projection publishes.
	exec(`UPDATE audio_tag_evidence SET value='Thriller' WHERE asset_id=(SELECT token FROM catalog_assets WHERE path='/music/b2.m4a') AND field='genre'`)
	exec(`DELETE FROM audio_genre_projection`)
	if _, err := ProjectAudioLocalGenresBatch(ctx, db, "music"); err != nil {
		t.Fatal(err)
	}
	genres = strings.Join(audioGenreProjectionRows(t, db), ",")
	// book-b's own set now agrees per source ("thriller"+"heist" from both
	// distinct sources still differs from... it projects whatever is unanimous.
	if !strings.Contains(genres, "thriller=Thriller") {
		t.Fatalf("agreeing sources did not project: %v", genres)
	}
}

func TestStructuredLocalGenresPreserveNames(t *testing.T) {
	got := normalizeAudioGenres(`["Singer/Songwriter","Earth, Wind & Fire"]`)
	if len(got) != 2 || got[0] != "Singer/Songwriter" || got[1] != "Earth, Wind & Fire" {
		t.Fatal(got)
	}
}
