package catalog

import (
	"context"
	"database/sql"

	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The bounded audio genre projection: effective, policy-applied local genre
// tags a scan observed become typed genre relations, with local source
// ownership, owner-lock survival, no accidental erasure and idempotent
// repeated scans. Everything here runs with no provider and no consent.
func audioGenreFixture(t *testing.T) (*catalogtest.Catalog, *Service, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "audio-genres.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	c.Library("music", "Music", "music", "/music")
	return c, New(db), db
}

func audioGenreCommit(t *testing.T, c *catalogtest.Catalog, s *Service, db *sql.DB, path string, tags map[string]string, issue string) error {
	t.Helper()
	var assetToken string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "flac", VideoCodec: "mpeg4", AudioCodec: "aac", Duration: 10})
		assetToken = token
		return err
	})
	gated, err := dbwork.Begin(context.Background(), db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	if err = s.commitAudio(context.Background(), tx, "music", "music", "/music", assetToken, path, assets.Facts{AudioCodec: "aac", Tags: tags, LocalMetadataIssue: issue}); err != nil {
		gated.Rollback()
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	c.Drain()
	return nil
}

func audioGenreExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(query, err)
	}
}

func audioGenreRows(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT s.source_id||'='||COALESCE(s.label_override,t.label) FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE t.vocab=1 ORDER BY s.source_id,COALESCE(s.label_override,t.label)`)
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

func audioGenreItemID(t *testing.T, db *sql.DB, title string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE kind=7 AND title=?`, title).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func audioGenreSetTerms(c *catalogtest.Catalog, item int64, provider string, terms ...compactcatalog.Term) {
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, item, compactcatalog.VocabGenre, provider, terms)
	})
	c.Drain()
}

func audioGenreLock(t *testing.T, c *catalogtest.Catalog, item int64) {
	t.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO metadata_relationship_decisions(kind,entity_id,relationship,value_json,source,locked,actor,observed_at) VALUES('item',?,'genre','[]','owner_lock',1,'owner','2026-01-01T00:00:00Z')`, item)
		return err
	})
	c.Drain()
}

func audioGenreCatchUp(t *testing.T, db *sql.DB, library string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		more, err := compactcatalog.ProjectAudioLocalGenresBatch(context.Background(), db, library)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			return
		}
	}
	t.Fatal("audio genre projection did not finish")
}

func TestAudioGenresPublishNormalizeAndReplaceOnRescan(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist, crime"}, ""); err != nil {
		t.Fatal(err)
	}
	genres := strings.Join(audioGenreRows(t, db), ",")
	if !strings.Contains(genres, "crime=") || !strings.Contains(genres, "heist=") {
		t.Fatalf("normalized genre relations not published: %v", genres)
	}
	// The projection is per item and idempotent: a repeated scan of unchanged
	// evidence (any spelling of the same names) neither accumulates nor loses rows.
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "heist; crime"}, ""); err != nil {
		t.Fatal(err)
	}
	if again := audioGenreRows(t, db); len(again) != 2 {
		t.Fatalf("repeated scan accumulated or lost rows: %v", again)
	}
	// A changed set replaces exactly the rows this source published; manual and
	// remote families are never touched by a rescan.
	audioGenreSetTerms(c, audioGenreItemID(t, db, "One"), "tmdb", compactcatalog.Term{SourceID: "16", Name: "Animation"})
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Crime"}, ""); err != nil {
		t.Fatal(err)
	}
	remaining := strings.Join(audioGenreRows(t, db), ",")
	if len(audioGenreRows(t, db)) != 2 || !strings.Contains(remaining, "16=Animation") || !strings.Contains(remaining, "crime=Crime") || strings.Contains(remaining, "heist=") {
		t.Fatalf("rescan disturbed other genre families: %v", remaining)
	}
}

func TestAudioGenrePolicyOffRemovesLocalRowsOnly(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(audioGenreRows(t, db)) != 1 {
		t.Fatal("local genre not published under default policy")
	}
	audioGenreExec(t, db, `UPDATE audio_metadata_policies SET local_mode='off' WHERE library_id='music'`)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); len(got) != 0 {
		t.Fatalf("policy off must not serve descriptive local genres: %v", got)
	}
	// Re-enable and rescan: the facet returns without stale accumulation.
	audioGenreExec(t, db, `UPDATE audio_metadata_policies SET local_mode='prefer' WHERE library_id='music'`)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist"}, ""); err != nil {
		t.Fatal(err)
	}
	if len(audioGenreRows(t, db)) != 1 {
		t.Fatal("re-enabled policy did not republish the local facet")
	}
}

func TestAudioGenreLockedClassAndInvalidSidecarSurviveRescan(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist, Crime"}, ""); err != nil {
		t.Fatal(err)
	}
	// An owner locks the genre class: later rescans must not rewrite it.
	audioGenreLock(t, c, audioGenreItemID(t, db, "One"))
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Drama"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); len(got) != 2 || strings.Contains(strings.Join(got, ","), "drama") {
		t.Fatalf("a locked genre class was rewritten by a rescan: %v", got)
	}
	// A later scan without any genre keeps the last valid rows, too.
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); len(got) != 2 {
		t.Fatalf("a genre-less rescan erased valid local facts: %v", got)
	}
}

func TestAudioGenreNormalizationSharesOneFacet(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	for i, spelling := range []string{"Heist, Crime", "HEIST ; CRIME", " crime / heist "} {
		asset := fmt.Sprintf("a%d", i)
		if err := audioGenreCommit(t, c, s, db, "/music/"+asset+".flac", map[string]string{"title": "T", "album": "A", "artist": "X", "genre": spelling}, ""); err != nil {
			t.Fatalf("asset %d import: %v", i, err)
		}
	}
	// One shared facet id across independent items with differently-spelled
	// equal genres, capped at the bounded length with control noise dropped.
	rows, err := db.Query(`SELECT s.source_id,COALESCE(s.label_override,t.label) FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id WHERE s.provider='local' AND t.vocab=1 ORDER BY s.source_id,COALESCE(s.label_override,t.label)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		seen[id] = name
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("normalized genres did not collapse to one facet: %v", seen)
	}
}

func TestAudioGenrePolicyFlipWithdrawsWithoutRescanAndKeepsLocks(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := audioGenreCommit(t, c, s, db, "/music/two.flac", map[string]string{"title": "Two", "album": "Other", "artist": "Other", "genre": "Drama"}, ""); err != nil {
		t.Fatal(err)
	}
	// The owner locks one item's genre class: that set survives the policy
	// flip; the unlocked item's source-owned rows are withdrawn immediately,
	// with no rescan, so no read ever serves a withdrawn local facet.
	audioGenreLock(t, c, audioGenreItemID(t, db, "Two"))
	audioGenreExec(t, db, `UPDATE audio_metadata_policies SET local_mode='off',revision=revision+1 WHERE library_id='music'`)
	audioGenreCatchUp(t, db, "music")
	remaining := strings.Join(audioGenreRows(t, db), ",")
	if len(audioGenreRows(t, db)) != 1 || !strings.Contains(remaining, "drama=Drama") {
		t.Fatalf("policy flip did not withdraw unlocked local genres: %v", remaining)
	}
	// The commit-time gate agrees: a rescan under off keeps the locked set and
	// removes nothing else.
	if err := audioGenreCommit(t, c, s, db, "/music/two.flac", map[string]string{"title": "Two", "album": "Other", "artist": "Other", "genre": "Drama"}, ""); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); len(got) != 1 {
		t.Fatalf("policy off rescan disturbed the locked class: %v", got)
	}
}

func TestAudioGenreValidTagsSurviveUnrelatedInvalidSidecar(t *testing.T) {
	c, s, db := audioGenreFixture(t)
	// A malformed unrelated sidecar marks the observation, but the embedded
	// tags the ingestion actually selected remain effective: the genre still
	// publishes, because this projection never consults the issue marker.
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist", "genre": "Heist"}, "invalid_sidecar_album"); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); len(got) != 1 || !strings.Contains(got[0], "heist=Heist") {
		t.Fatalf("an unrelated malformed sidecar blocked valid effective tags: %v", got)
	}
	// Genre evidence whose own source is invalid is simply absent from the
	// selected tags: nothing publishes, and prior valid rows are not erased.
	second := audioGenreRows(t, db)
	if err := audioGenreCommit(t, c, s, db, "/music/one.flac", map[string]string{"title": "One", "album": "Album", "artist": "Artist"}, "invalid_sidecar_genre"); err != nil {
		t.Fatal(err)
	}
	if got := audioGenreRows(t, db); strings.Join(got, ",") != strings.Join(second, ",") {
		t.Fatalf("an unavailable genre source erased valid facts: %v vs %v", got, second)
	}
}
