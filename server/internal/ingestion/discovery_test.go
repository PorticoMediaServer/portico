package ingestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealMovieScanDiscoveryPaginationAndCollections(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.mp4")
	if output, err := exec.Command(binary, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:size=64x64:rate=1", "-t", "12", "-c:v", "libx264", "-pix_fmt", "yuv420p", fixture).CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, output)
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	for _, p := range []string{a, b} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for n := 0; n < 23; n++ {
		dir := a
		if n >= 21 {
			dir = b
		}
		if err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("Film %02d (%d).mp4", n, 1990+n)), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	fixtures := catalogtest.New(t, db)
	helper, _ := os.Executable()
	store := storage.New(helper)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), binary, store)
	if err != nil {
		t.Fatal(err)
	}
	var poster bytes.Buffer
	if err = png.Encode(&poster, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a, "Film 00 (1990)-poster.png"), poster.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	scan := func(name, path string) catalog.Library {
		t.Helper()
		lib, err := cat.Create(name, "movie", path)
		if err != nil {
			t.Fatal(err)
		}
		job, err := scanner.Queue(lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 20; n++ {
			scanner.process(context.Background(), job.ID, lib.ID)
			// The derived worker runs beside ingestion in production; analysis
			// waits for its pending catalogue information changes to publish.
			drainDerivedCatalogue(t, db)
			state, err := scanner.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" {
				drainDerivedCatalogue(t, db)
				return lib
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		t.Fatal("scan unfinished")
		return lib
	}
	la, lb := scan("A", a), scan("B", b)
	q := catalog.BrowseQuery{Library: la.ID, Viewer: "local:owner:profile", Profile: "profile", Limit: 5, Sort: "title"}
	first, cursor, err := cat.Browse(q)
	if err != nil || len(first) != 5 || cursor == "" {
		t.Fatal(first, cursor, err)
	}
	for _, item := range first {
		if item.AddedAt == nil || !item.Available {
			t.Fatal("real scan missing timestamp or source", item)
		}
	}
	if first[0].PosterURL != "/v1/items/"+first[0].ID+"/art/poster" {
		t.Fatal("local movie art projection absent", first[0])
	}
	projection, err := cat.Content(catalog.ContentRequest{Viewer: catalog.Viewer{Profile: "profile", Fence: "fixture", Libraries: []string{la.ID}}, ServerID: "fixture", Library: la.ID, Profile: "profile", ViewerFence: "fixture", View: "browse", Limit: 5})
	if err != nil || projection.Sections[0].TotalCount != 21 || projection.Sections[0].Entries[0].PosterURL == "" {
		t.Fatal("real scan semantic projection", projection, err)
	}
	seen := map[string]bool{}
	for {
		rows, next, err := cat.Browse(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if seen[row.ID] {
				t.Fatal("repeated keyset item")
			}
			seen[row.ID] = true
		}
		if next == "" {
			break
		}
		q.Cursor = next
	}
	if len(seen) != 21 {
		t.Fatal("pagination lost items", len(seen))
	}
	for _, change := range []func(*catalog.BrowseQuery){func(v *catalog.BrowseQuery) { v.Viewer = "other-profile" }, func(v *catalog.BrowseQuery) { v.Library = lb.ID }, func(v *catalog.BrowseQuery) { v.Sort = "year" }, func(v *catalog.BrowseQuery) { v.Category = "decade:2000" }} {
		bad := catalog.BrowseQuery{Library: la.ID, Viewer: "local:owner:profile", Profile: "profile", Limit: 5, Sort: "title", Cursor: cursor}
		change(&bad)
		if _, _, err = cat.Browse(bad); !errors.Is(err, catalog.ErrCursor) {
			t.Fatal("cursor escaped scope", err)
		}
	}
	tampered := cursor[:len(cursor)-3] + "xxx"
	if _, _, err = cat.Browse(catalog.BrowseQuery{Library: la.ID, Viewer: "local:owner:profile", Cursor: tampered}); !errors.Is(err, catalog.ErrCursor) {
		t.Fatal("tampered cursor accepted", err)
	}
	categories, err := cat.Categories(catalog.Viewer{Libraries: []string{la.ID}}, la.ID)
	if err != nil || len(categories) != 3 {
		t.Fatal(categories, err)
	}
	for _, c := range categories {
		if c.ID == "decade:2000" && c.Count != 10 {
			t.Fatal(c)
		}
	}
	filtered, _, err := cat.Browse(catalog.BrowseQuery{Library: la.ID, Viewer: "local:owner:profile", Category: "decade:2000", Sort: "year", Direction: "desc", Limit: 100})
	if err != nil || len(filtered) != 10 || filtered[0].Year != 2009 {
		t.Fatal(filtered, err)
	}
	// Playback admission reads the person's stream limits from their account.
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	player := playback.New(db)
	p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	session, err := player.Create(p, first[0].ID, "automatic", "watch")
	if err != nil {
		t.Fatal(err)
	}
	if err = player.Progress(p, session.ID, session.Generation, 1, 4, "paused"); err != nil {
		t.Fatal(err)
	}
	discover, err := cat.Discover(la.ID, identity.PersonalKey(p.Viewer))
	if err != nil || len(discover.Sections) < 2 || len(discover.Sections[0].Items) != 1 || len(discover.Sections[1].Items) != 12 {
		t.Fatal(discover, err)
	}
	other, err := cat.Discover(la.ID, "other")
	if err != nil || len(other.Sections) > 0 && other.Sections[0].ID == "continue_watching" {
		t.Fatal("continue watching leaked", other, err)
	}
	if err = player.Progress(p, session.ID, session.Generation, 2, 4, "ended"); err != nil {
		t.Fatal(err)
	}
	ended, err := cat.Discover(la.ID, identity.PersonalKey(p.Viewer))
	if err != nil || len(ended.Sections) > 0 && ended.Sections[0].ID == "continue_watching" {
		t.Fatal("ended media continued", ended, err)
	}
	collection, err := cat.CreateCollection(la.ID, "Weekend")
	if err != nil {
		t.Fatal(err)
	}
	if err = cat.SetCollectionItem(collection.ID, first[0].ID, true); err != nil {
		t.Fatal(err)
	}
	others, _, err := cat.Browse(catalog.BrowseQuery{Library: lb.ID, Viewer: "local:owner:profile"})
	if err != nil {
		t.Fatal(err)
	}
	if err = cat.SetCollectionItem(collection.ID, others[0].ID, true); err == nil {
		t.Fatal("cross-library collection link admitted")
	}
	collection, err = cat.RenameCollection(collection.ID, "Favourites")
	if err != nil || collection.Name != "Favourites" || collection.ItemCount != 1 {
		t.Fatal(collection, err)
	}
	if err = cat.DeleteCollection(collection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(a, "Film 00 (1990).mp4")); err != nil {
		t.Fatal("collection deleted media", err)
	}
	// Historical catalog rows have no trustworthy first-cataloged time.
	names := catalogtest.Names{}
	names["historical"] = fixtures.Entity(compactcatalog.Entity{Library: fixtures.Handle(la.ID), Kind: compactcatalog.Movie, Key: "historical", Title: "Historical", Year: 1980}, map[string]any{"added_text": nil})
	fixtures.File(names["historical"].ID, filepath.Join(a, "Historical (1980).mkv"), 12)
	fixtures.Drain()
	old, err := cat.Get("profile", names["historical"].Public)
	if err != nil || old.AddedAt != nil {
		t.Fatal("fabricated historical addedAt", old, err)
	}
	// Check that the hot ordered lookups have applicable indexes.
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT entity_id FROM catalog_browse_rows WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=1 AND item_id IS NOT NULL ORDER BY COALESCE(added_text,'') DESC,entity_id DESC LIMIT 12`, la.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var x, y, z int
		var detail string
		if err = rows.Scan(&x, &y, &z, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "catalog_browse_recent_kind") {
			indexed = true
		}
	}
	if !indexed {
		t.Fatal("recently added lookup lost its ordered index")
	}
}
