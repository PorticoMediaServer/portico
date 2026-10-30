package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func sidecarDB(t *testing.T) (*Service, *catalogtest.Catalog, catalogtest.Item, catalogtest.Item) {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2020)
	season := c.Season(show, 1)
	c.Season(show, 0)
	c.Episode(show, season, 1, "/ep1")
	c.Episode(show, season, 2, "/ep2")
	c.Drain()
	return New(db), c, show, season
}

// Spec — Page Content §0.5 item 7: sidecar keys resolve to show, season and
// item carriers; an unmatched season file seeds nothing.
func TestCommitAssetSidecarsShowSeasonLogo(t *testing.T) {
	s, c, show, season := sidecarDB(t)
	db := s.db
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	extra := map[string]string{
		"show/poster": "k-poster", "show/backdrop": "k-backdrop", "show/logo": "k-logo",
		"season/1/poster": "k-s1", "season/9/poster": "k-s9",
	}
	if e = s.commitAssetSidecars(context.Background(), tx, "tv", "tv", sidecarItemToken(t, c, "/ep1"), extra); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	rows, e := db.Query(`SELECT kind,entity_id,role,art_key FROM local_artwork ORDER BY kind,entity_id,role`)
	if e != nil {
		t.Fatal(e)
	}
	got := []string{}
	for rows.Next() {
		var kind, entity, role, key string
		if e = rows.Scan(&kind, &entity, &role, &key); e != nil {
			rows.Close()
			t.Fatal(e)
		}
		got = append(got, kind+"/"+entity+"/"+role+"="+key)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	want := []string{fmt.Sprintf("season/%d/poster=k-s1", season.ID), fmt.Sprintf("show/%d/backdrop=k-backdrop", show.ID), fmt.Sprintf("show/%d/logo=k-logo", show.ID), fmt.Sprintf("show/%d/poster=k-poster", show.ID)}
	if len(got) != len(want) {
		t.Fatalf("carriers: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("carriers: %v", got)
		}
	}
	// The carrier insert marks artwork dirty for the touched entities.
	var dirty int
	if e = db.QueryRow(`SELECT count(*) FROM artwork_dirty WHERE (kind='show' AND entity_id=?) OR (kind='season' AND entity_id=?)`, show.ID, season.ID).Scan(&dirty); e != nil || dirty != 2 {
		t.Fatal("dirty not marked", dirty, e)
	}
}

func sidecarItemToken(t *testing.T, c *catalogtest.Catalog, path string) string {
	t.Helper()
	var token string
	if err := c.DB.QueryRow(`SELECT a.token FROM catalog_assets a WHERE a.path=?`, path).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestArtistSidecarCarriersSkipPlaceholders(t *testing.T) {
	full := map[string]string{"artist/portrait": "k", "artist/backdrop": "k2"}
	if carriers := artistSidecarCarriers(1, "Artist", full); len(carriers) != 2 {
		t.Fatalf("artist carriers: %v", carriers)
	}
	for _, artist := range []string{"", "Various Artists", "Unknown artist"} {
		if carriers := artistSidecarCarriers(1, artist, full); len(carriers) != 0 {
			t.Fatalf("placeholder %q tattooed: %v", artist, carriers)
		}
	}
	if carriers := artistSidecarCarriers(1, "Artist", nil); len(carriers) != 0 {
		t.Fatalf("empty extra: %v", carriers)
	}
}
