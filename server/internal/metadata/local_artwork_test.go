package metadata

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Spec — Page Content §0.5 item 7: an entity's scanned sidecars become
// provider='local' candidates through the ordinary dirty pass.
func TestSeedLocalArtworkCandidatesFromCarriers(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2020)
	season := c.Season(show, 1)
	names := catalogtest.Names{"show": show, "season1": season}
	c.Exec(`INSERT INTO local_artwork(library_id,kind,entity_id,role,art_key,observed_at) VALUES
	('tv','show',?,'poster','k-poster','now'),('tv','show',?,'backdrop','k-backdrop','now'),('tv','show',?,'logo','k-logo','now'),('tv','season',?,'poster','k-s1','now'),('tv','season',999999,'poster','k-gone','now')`,
		show.ID, show.ID, show.ID, season.ID)
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	now := "now"
	showFence, e := artworkFence(context.Background(), tx, RepairTarget{"show", show.Public})
	if e != nil {
		t.Fatal(e)
	}
	seasonFence, e := artworkFence(context.Background(), tx, RepairTarget{"season", season.Public})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = seedLocalArtwork(context.Background(), tx, RepairTarget{"show", show.Public}, showFence, now); e != nil {
		t.Fatal(e)
	}
	if _, e = seedLocalArtwork(context.Background(), tx, RepairTarget{"season", season.Public}, seasonFence, now); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	rows, e := db.Query(`SELECT kind,entity_id,role,provider,origin FROM artwork_candidates WHERE provider='local' ORDER BY kind,entity_id,role`)
	if e != nil {
		t.Fatal(e)
	}
	got := []string{}
	for rows.Next() {
		var kind, role, provider, origin string
		var entity int64
		if e = rows.Scan(&kind, &entity, &role, &provider, &origin); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		got = append(got, kind+"/"+names.Of(c.Public(entity))+"/"+role+"="+origin)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	want := []string{"season/season1/poster=local:k-s1", "show/show/backdrop=local:k-backdrop", "show/show/logo=local:k-logo", "show/show/poster=local:k-poster"}
	if len(got) != len(want) {
		t.Fatalf("candidates: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates: %v", got)
		}
	}
	// Jobs are queued for the seeded candidates.
	var jobs int
	if e = db.QueryRow(`SELECT count(*) FROM artwork_jobs WHERE provider='local'`).Scan(&jobs); e != nil || jobs != 4 {
		t.Fatal("jobs not queued", jobs, e)
	}
}
