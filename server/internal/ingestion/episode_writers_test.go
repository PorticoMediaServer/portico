package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The episode writers: a first import (seasonal, multi-episode, Specials and
// dated files) catalogues three shows and six episodes, an unchanged rescan
// changes nothing, a manual reassignment moves an episode to another show and
// season as a manual identity, and a deleted episode leaves the catalogue.
func TestEpisodeWritersImportReassignAndDelete(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, "tv")
	files := []string{
		"The Show/Season 01/The Show - S01E01.mp4",
		"The Show/Season 01/The Show - S01E02E03.mp4",
		"The Show/Specials/The Show - S00E01.mp4",
		"Other Show (2019)/Other Show (2019) - 1x04.mp4",
		"Daily News/Daily News - 2020-05-06.mp4",
	}
	for _, name := range files {
		path := filepath.Join(media, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	lib, err := c.Create("Television", "tv", media)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db, c, assets.Probe{})
	ctx := context.Background()
	run := func(mode string) {
		t.Helper()
		job, e := s.QueueMode(ctx, lib.ID, "", mode, nil)
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			s.process(ctx, job.ID, lib.ID)
			state, e := s.Get(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.Status == "complete" {
				return
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		t.Fatal("scan did not finish")
	}
	run("")
	var shows, episodes int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0),(SELECT count(*) FROM catalog_episodes ep JOIN catalog_entities e ON e.id=ep.entity_id WHERE e.retired=0)`).Scan(&shows, &episodes); err != nil || shows != 3 || episodes != 6 {
		t.Fatalf("first import published %d shows and %d episodes (%v)", shows, episodes, err)
	}
	run("full")
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM catalog_entities WHERE kind=2 AND retired=0),(SELECT count(*) FROM catalog_episodes ep JOIN catalog_entities e ON e.id=ep.entity_id WHERE e.retired=0)`).Scan(&shows, &episodes); err != nil || shows != 3 || episodes != 6 {
		t.Fatalf("unchanged rescan: %d shows and %d episodes (%v)", shows, episodes, err)
	}

	var asset string
	if err = db.QueryRow(`SELECT token FROM catalog_assets WHERE path=?`, filepath.Join(media, files[0])).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	season := 2
	if err = c.AssignEpisodes(ctx, lib.ID, catalog.EpisodeAssignment{AssetID: asset, ShowTitle: "Renamed Show", Year: 2021, Numbering: "seasonal", SeasonNumber: &season, EpisodeNumbers: []int{7}}); err != nil {
		t.Fatal(err)
	}
	var manual string
	if err = db.QueryRow(`SELECT c.local_identity_status FROM catalog_episodes c JOIN catalog_entities sh ON sh.id=c.show_id WHERE sh.title='Renamed Show' AND c.number=7`).Scan(&manual); err != nil || manual != "manual" {
		t.Fatalf("reassigned episode: %q %v", manual, err)
	}

	var episode string
	var entity int64
	if err = db.QueryRow(`SELECT pid(e.public_id),e.id FROM catalog_episodes ep JOIN catalog_seasons s ON s.entity_id=ep.season_id JOIN catalog_entities e ON e.id=ep.entity_id WHERE s.number=0`).Scan(&episode, &entity); err != nil {
		t.Fatal(err)
	}
	if err = c.RemoveTitle(ctx, episode); err != nil {
		t.Fatal(err)
	}
	var left int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_entities WHERE id=? AND retired=0`, entity).Scan(&left); err != nil || left != 0 {
		t.Fatalf("deleted episode still published: %d %v", left, err)
	}
}
