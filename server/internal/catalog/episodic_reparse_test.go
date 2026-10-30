package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// NEW-26 on existing libraries: sources the old reader left with a naming
// issue are read once more by the current ParseEpisode after an upgrade. A
// title with "(Part 1)" becomes its episode; a genuine stacked part is now
// attached to its episode as that part (reader version 4: stacked parts can be
// attached, where the older catalogue left them for the owner to assign); a
// manual assignment is never touched; the second run does nothing.
func TestReparseEpisodicIssuesOnceAfterAParserChange(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "reparse.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	c.Library("tv", "TV", "tv", "/tv")
	sources := []struct{ asset, path string }{
		{"jazz", "/tv/Cowboy Bebop/Season 01/Cowboy Bebop - S01E12 - Jupiter Jazz (Part 1).mkv"},
		{"stack", "/tv/Show/Season 01/Show - S01E05 - pt1.mkv"},
		{"manual", "/tv/Other/Season 01/Other - S01E02 - The Part 2 Problem.mkv"},
	}
	tokens := map[string]string{}
	for _, src := range sources {
		var token string
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			_, value, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: src.path, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
			token = value
			return err
		})
		tokens[src.asset] = token
		// As the old reader left them: every one a multipart issue, no episode.
		if _, err := db.Exec(`INSERT INTO episodic_sources(library_id,asset_id,manual,issue,source_name) VALUES('tv',?,?,'multipart_episode_requires_assignment',?)`, token, src.asset == "manual", filepath.Base(src.path)); err != nil {
			t.Fatal(err)
		}
	}
	c.Drain()
	dbwork.ResetPlans()
	if err = s.ReparseEpisodicIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := dbwork.Plans()
	t.Logf("trigger-inclusive reparse maximum: %+v", stats.Largest)
	plan := dbwork.DefaultPlan(dbwork.ClassMaintenance)
	if stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps || stats.Overruns != 0 {
		t.Fatalf("reparse exceeds the unchanged maintenance plan: %+v", stats)
	}
	// Newly created episode/show rows are queued by their first browse
	// publication, even when ingress skipped a holder that did not yet exist.
	c.Drain()
	for _, key := range []string{"jazz", "stack"} {
		var present bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_show_facet_episodes f JOIN catalog_asset_links l ON l.entity_id=f.episode_id JOIN catalog_assets a ON a.id=l.asset_id WHERE a.token=? AND f.field='resolution' AND f.value='1080p')`, tokens[key]).Scan(&present); err != nil || !present {
			t.Fatalf("new episode lost maintained facets: %s %v", key, err)
		}
	}
	issue := func(asset string) string {
		var v string
		if err := db.QueryRow(`SELECT issue FROM episodic_sources WHERE library_id='tv' AND asset_id=?`, tokens[asset]).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	var number int
	var show string
	if err = db.QueryRow(`SELECT e.number,sh.title FROM catalog_asset_links ia JOIN catalog_assets a ON a.id=ia.asset_id JOIN catalog_episodes e ON e.entity_id=ia.entity_id JOIN catalog_entities sh ON sh.id=e.show_id WHERE a.token=?`, tokens["jazz"]).Scan(&number, &show); err != nil || number != 12 || show != "Cowboy Bebop" || issue("jazz") != "" {
		t.Fatalf("the (Part 1) title didn't become S01E12: %d %q %q (%v)", number, show, issue("jazz"), err)
	}
	var part int
	if err = db.QueryRow(`SELECT e.number,ia.part_index FROM catalog_asset_links ia JOIN catalog_assets a ON a.id=ia.asset_id JOIN catalog_episodes e ON e.entity_id=ia.entity_id WHERE a.token=?`, tokens["stack"]).Scan(&number, &part); err != nil || number != 5 || part != 1 || issue("stack") != "" {
		t.Fatalf("a stacked part was not attached as part 1 of S01E05: episode %d part %d issue %q (%v)", number, part, issue("stack"), err)
	}
	if issue("manual") != "multipart_episode_requires_assignment" {
		t.Fatal("a manual source was read again")
	}
	var version string
	if err = db.QueryRow(`SELECT value FROM configuration WHERE key=?`, episodeParserVersionKey).Scan(&version); err != nil || version != strconv.Itoa(EpisodeParserVersion) {
		t.Fatalf("version %q (%v)", version, err)
	}
	// Once per version: a source given an issue afterwards is left for scans.
	if _, err = db.Exec(`UPDATE episodic_sources SET issue='multipart_episode_requires_assignment' WHERE asset_id=?`, tokens["jazz"]); err != nil {
		t.Fatal(err)
	}
	if err = s.ReparseEpisodicIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	if issue("jazz") == "" {
		t.Fatal("the reparse ran twice for one version")
	}
}
