package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

func editValue(s string) MetadataEditValue { return MetadataEditValue{Present: true, Value: &s} }

func manualMetadataFixture(t *testing.T, kind, title, overview string) (*catalogtest.Catalog, *sql.DB, *Service, catalogtest.Item, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manual-metadata.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	libraryKind := "movie"
	if kind == "episode" {
		libraryKind = "tv"
	} else if kind == "song" {
		libraryKind = "music"
	} else if kind == "audiobook_file" {
		libraryKind = "audiobook"
	}
	library := c.Library("lib", "Library", libraryKind, "/owned")
	var item catalogtest.Item
	switch kind {
	case "movie":
		item = c.Movie(library, "/owned/source.mkv", title, 2020)
	case "episode":
		show := c.Show(library, "Show", 2020)
		season := c.Season(show, 1)
		item = c.Episode(show, season, 1, "/owned/Show/Season 1/Episode.mkv")
	case "song":
		artist := c.Artist(library, "Artist")
		album := c.Album(artist, "Album", 2020)
		item = c.Song(album, 1, "/owned/Album/Track.flac", title)
	case "audiobook_file":
		book := c.Book(library, "Book", "Author")
		item = c.BookFile(book, 1, "/owned/Book/Part.m4b")
	default:
		t.Fatalf("unsupported manual metadata fixture kind %q", kind)
	}
	c.Fields(item.ID, map[string]any{"title": title, "overview": overview})
	c.Drain()
	return c, db, New(db), item, path
}

func TestManualMetadataIndependentFieldsAutomaticRestoreAndIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, recursive := range []string{"OFF", "ON"} {
		for _, kind := range []string{"movie", "episode", "song", "audiobook_file"} {
			t.Run(kind+recursive, func(t *testing.T) {
				c, db, s, item, _ := manualMetadataFixture(t, kind, "Automatic title", "Automatic description")
				var e error
				if _, e := db.Exec(`PRAGMA recursive_triggers=` + recursive); e != nil {
					t.Fatal(e)
				}
				c.Exec(`INSERT INTO metadata_details(item_id,provider,provider_id,source_url,observed_at) VALUES(?,'provider','stable-provider-id','https://provider.invalid/item','observed')`, item.ID)
				ctx := context.Background()
				allow := func(*sql.Tx) error { return nil }
				read := func() ManualMetadata {
					v, e := s.ManualMetadata(ctx, "server", "fence", item.Public, allow)
					if e != nil {
						t.Fatal(e)
					}
					return v
				}
				save := func(m ManualMetadataMutation) ManualMetadata {
					v, e := s.SaveManualMetadata(ctx, "server", "fence", item.Public, "owner", m, allow)
					if e != nil {
						t.Fatal(e)
					}
					return v
				}
				v := read()
				v = save(ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue("Owner title")})
				if !v.Title.Manual || v.Description.Manual || v.Title.AutomaticValue != "Automatic title" || v.Description.Value != "Automatic description" {
					t.Fatal(v)
				}
				old := v.Revision
				c.Fields(item.ID, map[string]any{"title": "New automatic title", "overview": ""})
				c.Drain()
				v = read()
				if v.Title.Value != "Owner title" || v.Title.AutomaticValue != "New automatic title" || v.Description.Value != "" || v.Description.Manual {
					t.Fatal(v)
				}
				if _, e = s.SaveManualMetadata(ctx, "server", "fence", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: old, Description: editValue("Stale draft")}, allow); !errors.Is(e, ErrManualMetadataConflict) {
					t.Fatal("automatic backup change did not conflict", e)
				}
				v = save(ManualMetadataMutation{ExpectedRevision: v.Revision, Description: editValue("Owner description")})
				c.Fields(item.ID, map[string]any{"title": "Owner title", "overview": "New automatic description"})
				c.Drain()
				v = read()
				if v.Title.AutomaticValue != "Owner title" || v.Description.AutomaticValue != "New automatic description" || v.Description.Value != "Owner description" {
					t.Fatal("automatic value equal to manual or independent field capture failed", v)
				}
				v = save(ManualMetadataMutation{ExpectedRevision: v.Revision, Title: MetadataEditValue{Present: true}})
				if v.Title.Manual || !v.Description.Manual || v.Title.Value != "Owner title" {
					t.Fatal(v)
				}
				v = save(ManualMetadataMutation{ExpectedRevision: v.Revision, Description: MetadataEditValue{Present: true}})
				if v.Description.Manual || v.Description.Value != "New automatic description" {
					t.Fatal(v)
				}
				var entity int64
				var lib, asset, provider string
				var guard int
				if e = db.QueryRow(`SELECT i.id,cl.library_id,a.token,d.provider_id,(SELECT count(*) FROM metadata_owner_fields f WHERE f.kind='item' AND f.entity_id=i.id AND f.locked=1) FROM catalog_entities i JOIN catalog_libraries cl ON cl.id=i.library_id JOIN catalog_asset_links al ON al.entity_id=i.id JOIN catalog_assets a ON a.id=al.asset_id JOIN metadata_details d ON d.item_id=i.id WHERE i.id=?`, item.ID).Scan(&entity, &lib, &asset, &provider, &guard); e != nil || entity != item.ID || lib != "lib" || asset != item.Token || provider != "stable-provider-id" || guard != 0 {
					t.Fatal("identity/provenance/owner locks changed", e, entity, lib, asset, provider, guard)
				}
			})
		}
	}
}
func TestManualMetadataFailedSaveRollbackReopenAndAuthorization(t *testing.T) {
	c, db, s, item, path := manualMetadataFixture(t, "movie", "Automatic", "")
	if _, e := db.Exec(`CREATE TRIGGER fail_manual BEFORE INSERT ON metadata_owner_fields WHEN NEW.value='Reject' BEGIN SELECT RAISE(ABORT,'controlled owner-field failure');END`); e != nil {
		t.Fatal(e)
	}
	allow := func(*sql.Tx) error { return nil }
	ctx := context.Background()
	v, e := s.ManualMetadata(ctx, "server", "f", item.Public, allow)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue("Reject")}, allow); e == nil {
		t.Fatal("injected failure ignored")
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = New(db)
	c = catalogtest.New(t, db)
	var rows int
	if e = db.QueryRow(`SELECT count(*) FROM metadata_owner_fields WHERE kind='item' AND entity_id=?`, item.ID).Scan(&rows); e != nil || rows != 0 {
		t.Fatal("failed save persisted guard/override", rows, e)
	}
	c.Fields(item.ID, map[string]any{"title": "After reopen"})
	c.Drain()
	v, e = s.ManualMetadata(ctx, "server", "f", item.Public, allow)
	if e != nil || v.Title.Value != "After reopen" {
		t.Fatal(v, e)
	}
	denied := errors.New("revoked")
	if _, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue("Denied")}, func(*sql.Tx) error { return denied }); !errors.Is(e, denied) {
		t.Fatal(e)
	}
	if _, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue(" ")}, allow); !errors.Is(e, ErrManualMetadataInput) {
		t.Fatal("blank title accepted", e)
	}
}

func TestManualMetadataLongAutomaticValuesAndExistingRowRollback(t *testing.T) {
	c, db, s, item, path := manualMetadataFixture(t, "movie", strings.Repeat("T", 301), strings.Repeat("D", 20001))
	ctx := context.Background()
	allow := func(*sql.Tx) error { return nil }
	read := func() ManualMetadata {
		v, e := s.ManualMetadata(ctx, "server", "f", item.Public, allow)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := read()
	v, e := s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue("Owner")}, allow)
	if e != nil || v.Description.Manual || len(v.Description.Value) != 20001 {
		t.Fatal(v, e)
	}
	if _, e = db.Exec(`CREATE TRIGGER fail_existing BEFORE UPDATE OF value ON metadata_owner_fields WHEN NEW.value='Reject' BEGIN SELECT RAISE(ABORT,'controlled owner-field failure');END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: editValue("Reject")}, allow); e == nil {
		t.Fatal("failed projection committed")
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s = New(db)
	c = catalogtest.New(t, db)
	var guard int
	if e = db.QueryRow(`SELECT count(*) FROM metadata_owner_fields WHERE kind='item' AND entity_id=? AND locked=1`, item.ID).Scan(&guard); e != nil || guard != 1 {
		t.Fatal(guard, e)
	}
	old := read().Revision
	c.Fields(item.ID, map[string]any{"title": strings.Repeat("N", 302), "overview": strings.Repeat("E", 20002)})
	c.Drain()
	v = read()
	if v.Title.Value != "Owner" || len(v.Title.AutomaticValue) != 302 || v.Revision == old {
		t.Fatal(v)
	}
	if _, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: old, Title: MetadataEditValue{Present: true}}, allow); !errors.Is(e, ErrManualMetadataConflict) {
		t.Fatal("stale automatic release did not conflict", e)
	}
	v, e = s.SaveManualMetadata(ctx, "server", "f", item.Public, "owner", ManualMetadataMutation{ExpectedRevision: v.Revision, Title: MetadataEditValue{Present: true}}, allow)
	if e != nil || v.Title.Manual || len(v.Title.Value) != 302 || len(v.Description.Value) != 20002 {
		t.Fatal(v, e)
	}
	c.Delete(item.ID)
	c.Drain()
	var n int
	if e = db.QueryRow(`SELECT count(*) FROM metadata_owner_fields WHERE kind='item' AND entity_id=?`, item.ID).Scan(&n); e != nil || n != 0 {
		t.Fatal("manual row survived item deletion", n, e)
	}
}

func TestManualMetadataEffectiveSearchOwnerTitle(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("lib", "Library", "movie", "/owned")
	item := c.Movie(films, "/owned/source.mkv", "Automatic Alpha", 2020)
	c.Drain()
	s := New(c.DB)
	var last SearchEnvelope

	searchContains := func(term string) bool {
		t.Helper()
		libraries := []string{"lib"}
		out, err := s.Search(context.Background(), SearchRequest{
			Viewer:   Viewer{Profile: "viewer", Fence: "fence", Libraries: libraries},
			ServerID: "server", Profile: "viewer", ViewerFence: "fence", Q: term, Group: "movies", Limit: 10, Libraries: libraries,
		})
		if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" {
			t.Fatalf("search %q: %+v %v", term, out, err)
		}
		last = out
		for _, result := range out.Groups[0].Items {
			if result.ID == item.Public {
				return true
			}
		}
		return false
	}
	check := func(term string, want bool) {
		t.Helper()
		if got := searchContains(term); got != want {
			var title, document string
			_ = c.DB.QueryRow(`SELECT e.title,COALESCE((SELECT d.title FROM catalog_search_documents d WHERE d.entity_id=e.id),'') FROM catalog_entities e WHERE e.id=?`, item.ID).Scan(&title, &document)
			t.Fatalf("search %q contains item=%v, want %v; entity=%q document=%q groups=%+v", term, got, want, title, document, last.Groups)
		}
	}

	// As the metadata editor does: the owner's value is written and its lock
	// recorded, so a later automatic title is kept aside.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'title','"Custom Omega"','"Automatic Alpha"',1,'owner','now')`, item.ID); err != nil {
			return err
		}
		return compactcatalog.SetFieldsTx(ctx, tx, item.ID, compactcatalog.Owner, map[string]any{"title": "Custom Omega"})
	})
	c.Drain()
	check("Omega", true)
	check("Alpha", false)

	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, item.ID, compactcatalog.Automatic, map[string]any{"title": "Automatic Beta"})
	})
	c.Drain()
	check("Omega", true)
	check("Beta", false)
}
