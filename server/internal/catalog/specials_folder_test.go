package catalog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// A "Specials" folder is Season 0 in a TV or anime library (Spec — Page
// Content §2), and an extras folder everywhere else.
func TestSpecialsFolderIsSeasonZeroInEpisodicLibraries(t *testing.T) {
	root := "/media"
	path := "/media/Harbor/Specials/Harbor S00E01.mkv"
	for _, kind := range []string{"tv", "anime"} {
		if _, ok := ClassifyExtraFor(kind, root, path); ok {
			t.Fatalf("%s: Specials classified as an extra", kind)
		}
	}
	if extra, ok := ClassifyExtraFor("movie", root, "/media/Film (2020)/Specials/Making of.mkv"); !ok || extra.Kind != "other" {
		t.Fatal("a movie's Specials folder is still an extras folder", extra, ok)
	}
	// Other extras folders inside a show still are extras.
	if extra, ok := ClassifyExtraFor("tv", root, "/media/Harbor/Featurettes/Cast.mkv"); !ok || extra.Kind != "featurette" {
		t.Fatal("show featurettes", extra, ok)
	}
	if naming := ParseEpisode("Harbor/Specials/Harbor S00E01.mkv", "tv"); naming.Season != 0 || len(naming.Numbers) != 1 || naming.Numbers[0] != 1 || naming.Issue != "" {
		t.Fatalf("Specials episode naming: %+v", naming)
	}

	// End to end through inventory association: an episode in Season 0.
	dir := t.TempDir()
	db, err := persistence.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	sourceRoot := filepath.Join(dir, "tv")
	if err = os.MkdirAll(filepath.Join(sourceRoot, "Harbor", "Specials"), 0700); err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create("TV", "tv", sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := s.LibrarySources(context.Background(), lib.ID)
	if err != nil || len(sources) != 1 {
		t.Fatal(sources, err)
	}
	file := filepath.Join(sources[0].ResolvedPath, "Harbor", "Specials", "Harbor S00E01.mkv")
	c := catalogtest.New(t, db)
	const assetToken = "special-asset"
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := compactcatalog.CreateAssetTx(ctx, tx, assetToken, compactcatalog.Asset{Path: file, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "", AudioCodec: "", Duration: 0}, true)
		return err
	})
	c.Drain()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = s.associateInventoryTx(context.Background(), tx, sources[0], assetToken, file); err != nil {
		t.Fatal(err)
	}
	var season, extras int
	if err = tx.QueryRow(`SELECT se.number FROM catalog_episodes e JOIN catalog_seasons se ON se.entity_id=e.season_id JOIN catalog_asset_links ia ON ia.entity_id=e.entity_id JOIN catalog_assets a ON a.id=ia.asset_id WHERE a.token=?`, assetToken).Scan(&season); err != nil || season != 0 {
		t.Fatal("Specials episode not in Season 0", season, err)
	}
	if err = tx.QueryRow(`SELECT count(*) FROM item_extras`).Scan(&extras); err != nil || extras != 0 {
		t.Fatal("Specials episode became an extra", extras, err)
	}
}
