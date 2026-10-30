package ingestion

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

//go:embed testdata/synthetic_episode_inventory.json
var syntheticEpisodeInventory []byte

// The Python generator smoke pins this inventory to its actual emitted names.
// Scan equal bytes in independent files, then check physical attachment and
// browse membership as well as parsed episode identities, for every show.
func TestSyntheticFixtureEpisodeCountsSurviveInventoryAndBrowse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	var fixture struct {
		Shows []struct {
			Kind     string `json:"kind"`
			Folder   string `json:"folder"`
			Episodes int    `json:"episodes"`
		} `json:"shows"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(syntheticEpisodeInventory, &fixture); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, relative := range fixture.Files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("same procedural media bytes"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalog.New(db)
	s := New(db, c, assets.Probe{})
	ctx := context.Background()
	for _, source := range []struct{ folder, kind string }{{"TV", "tv"}, {"Anime", "anime"}} {
		library, err := c.Create(source.folder, source.kind, filepath.Join(root, source.folder))
		if err != nil {
			t.Fatal(err)
		}
		// Exercise inventory and filename interpretation; no decoder is asked
		// to pretend these small disposable bytes are a playable video.
		if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, library.ID); err != nil {
			t.Fatal(err)
		}
		job, err := s.QueueMode(ctx, library.ID, "", "full", nil)
		if err != nil {
			t.Fatal(err)
		}
		finished := false
		for turn := 0; turn < len(fixture.Files)*8+100; turn++ {
			s.process(ctx, job.ID, library.ID)
			state, err := s.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "failed" || state.Status == "cancelled" {
				t.Fatalf("%s scan refused: %+v", source.kind, state)
			}
			if state.Status == "complete" {
				finished = true
				break
			}
		}
		if !finished {
			t.Fatalf("%s inventory did not complete its file-derived turn guard", source.kind)
		}
	}
	catalogtest.New(t, db).Drain()
	total := 0
	for _, show := range fixture.Shows {
		title := show.Folder[:strings.LastIndex(show.Folder, " (")]
		var parsed, attached, browsable int
		err := db.QueryRow(`SELECT count(*),
 sum(EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=ep.entity_id)),
 sum(EXISTS(SELECT 1 FROM catalog_browse_rows b WHERE b.entity_id=ep.entity_id))
 FROM catalog_episodes ep JOIN catalog_entities sh ON sh.id=ep.show_id
 JOIN catalog_libraries cl ON cl.id=sh.library_id JOIN libraries lib ON lib.id=cl.library_id
 WHERE sh.title=? AND lib.kind=?`, title, show.Kind).Scan(&parsed, &attached, &browsable)
		if err != nil || parsed != show.Episodes || attached != show.Episodes || browsable != show.Episodes {
			t.Errorf("%s: parsed=%d attached=%d browse=%d want=%d (%v)", title, parsed, attached, browsable, show.Episodes, err)
		}
		t.Log(fmt.Sprintf("%s: %d parsed / %d attached / %d browse", title, parsed, attached, browsable))
		total += parsed
	}
	if total != 200 {
		t.Fatalf("fixture parsed %d episodes, want 200", total)
	}
}
