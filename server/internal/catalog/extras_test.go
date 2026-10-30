package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Classification is a pure function of the path. Folder evidence outranks a
// filename suffix, and the owning media folder is the folder above the extras
// folder however deeply the extra is nested.
func TestExtraClassificationFromPaths(t *testing.T) {
	root := "/library"
	for _, row := range []struct {
		path, kind, parent, title string
		ok                        bool
	}{
		{"/library/Film (2001)/Extras/Making Of.mkv", "other", "/library/Film (2001)", "Making Of", true},
		{"/library/Film (2001)/Featurettes/Design.mkv", "featurette", "/library/Film (2001)", "Design", true},
		{"/library/Film (2001)/Trailers/Teaser.mp4", "trailer", "/library/Film (2001)", "Teaser", true},
		{"/library/Film (2001)/Deleted Scenes/Cut.mkv", "deleted_scene", "/library/Film (2001)", "Cut", true},
		{"/library/Film (2001)/Behind The Scenes/Set.mkv", "behind_the_scenes", "/library/Film (2001)", "Set", true},
		{"/library/Film (2001)/Interviews/Cast.mkv", "interview", "/library/Film (2001)", "Cast", true},
		{"/library/Film (2001)/Scenes/Opening.mkv", "scene", "/library/Film (2001)", "Opening", true},
		{"/library/Film (2001)/Shorts/Prequel.mkv", "short", "/library/Film (2001)", "Prequel", true},
		{"/library/Film (2001)/Extras/Featurettes/Deep.mkv", "featurette", "/library/Film (2001)", "Deep", true},
		{"/library/Show/Season 01/Extras/Recap.mkv", "other", "/library/Show/Season 01", "Recap", true},
		{"/library/Film (2001)/Film-trailer.mkv", "trailer", "/library/Film (2001)", "Film", true},
		{"/library/Film (2001)/Film-featurette.mkv", "featurette", "/library/Film (2001)", "Film", true},
		{"/library/Film (2001)/Film.mkv", "", "", "", false},
		{"/library/Extraordinary Film/Film.mkv", "", "", "", false},
		{"/elsewhere/Extras/Thing.mkv", "", "", "", false},
	} {
		extra, ok := ClassifyExtra(root, row.path)
		if ok != row.ok {
			t.Fatal("classification", row.path, ok)
		}
		if !ok {
			continue
		}
		if extra.Kind != row.kind || extra.ParentPath != row.parent || extra.Title != row.title {
			t.Fatalf("path %s classified as %+v", row.path, extra)
		}
	}
	for _, kind := range ExtraKinds() {
		if extraKindLabel(kind) == "" {
			t.Fatal("unlabelled extra kind", kind)
		}
	}
}

// Extras are ordinary items with a relationship row; detail groups them by kind
// and each entry stays playable through the normal item path.
func TestDetailExtrasGroupedAndPlayable(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := New(db)
	c := catalogtest.New(t, db)
	library := c.Library("movies", "Movies", "movie", "/movies")
	parent := c.Movie(library, "/movies/Film (2001)/Film.mkv", "Film", 2001)
	sources, err := service.LibrarySources(context.Background(), "movies")
	if err != nil || len(sources) != 1 {
		t.Fatalf("movie sources: %+v %v", sources, err)
	}
	source := sources[0]
	rows := []struct{ token, folder, name string }{
		{"asset-x1", "Trailers", "Teaser.mkv"},
		{"asset-x2", "Featurettes", "Design.mkv"},
		{"asset-x3", "Trailers", "Full Trailer.mkv"},
	}
	for _, row := range rows {
		path := filepath.Join(source.ResolvedPath, "Film (2001)", row.folder, row.name)
		extra, ok := ClassifyExtra(source.ResolvedPath, path)
		if !ok {
			t.Fatalf("extra path was not classified: %s", path)
		}
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			if _, err := compactcatalog.CreateAssetTx(ctx, tx, row.token, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", Width: 1, Height: 1, Duration: 10}, true); err != nil {
				return err
			}
			return service.publishExtraTx(ctx, tx, source, row.token, path, extra)
		})
	}
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "fence", Libraries: []string{"movies"}}
	detail, err := service.Detail(viewer, "server", parent.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Extras) != 2 || detail.Extras[0].Type != "trailer" || detail.Extras[1].Type != "featurette" {
		t.Fatalf("extras not grouped in published order: %+v", detail.Extras)
	}
	if detail.Extras[0].Label != "Trailers" || len(detail.Extras[0].Items) != 2 {
		t.Fatalf("trailer group: %+v", detail.Extras[0])
	}
	for _, entry := range detail.Extras[0].Items {
		if entry.Kind != "extra" || entry.Playback == nil || entry.Playback.ItemID != entry.ID {
			t.Fatalf("extra is not playable through the item path: %+v", entry)
		}
	}
	// An extra's own detail carries no further extras and stays out of home rows.
	childID := detail.Extras[0].Items[0].ID
	child, err := service.Detail(viewer, "server", childID, false)
	if err != nil || len(child.Extras) != 0 {
		t.Fatal("extras recursed", child.Extras, err)
	}
	request := HomeRequest{Viewer: viewer, ServerID: "server", Profile: viewer.Profile, ViewerFence: viewer.Fence, Libraries: viewer.Libraries}
	row, err := service.HomeSingleRow(request, "new_on_server", HomeRowPage{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range row.Entries {
		if entry.Kind == "extra" {
			t.Fatal("an extra reached the recently added row", entry)
		}
	}
}
