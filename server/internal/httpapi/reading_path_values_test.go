package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// readingTreeFile writes a publication under the fixture root. A caller may
// supply exact bytes to exercise a publication without the built-in creator.
func readingTreeFile(t *testing.T, r *readingFixture, relative string, supplied ...[]byte) string {
	t.Helper()
	if len(supplied) > 1 {
		t.Fatal("one publication is required")
	}
	name := filepath.Join(filepath.Dir(r.path), filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if len(supplied) == 1 {
		data = supplied[0]
	} else {
		switch filepath.Ext(name) {
		case ".epub":
			data = readingtest.EPUB()
		case ".cbz":
			data = readingtest.CBZ()
		default:
			data = readingtest.PDF()
		}
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func readingPathItem(t *testing.T, r *readingFixture, path string) string {
	t.Helper()
	var id string
	if err := r.f.db.QueryRow(`SELECT pid(e.public_id) FROM catalog_assets a JOIN reading_publications p ON p.asset_id=a.id JOIN catalog_entities e ON e.id=p.item_id WHERE a.path=?`, path).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReadingPrivatePathProducerGeneratedTree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	for _, relative := range []string{
		"Frank Herbert/Dune Saga/Dune.epub", "Frank Herbert/Dune Saga/Dune Messiah.pdf",
		"Frank Herbert/Dune (12)/Dune.epub", "Frank Herbert/Dune (12)/Dune.pdf",
		"Isaac Asimov/Foundation.pdf", "Isaac Asimov/Isaac Asimov - Foundation.epub",
		"Books/Sci-fi/Frank Herbert/Dune.pdf", "Ebooks/Foundation.pdf",
		"Some Book [Group] (Digital) 1080p.pdf", "[Group] (Digital).pdf",
		"Books/Identity/Identity One.pdf", "Downloads/Identity/Identity Two.pdf",
		"     /Reader Book.pdf", "Author\nName - Work.pdf",
	} {
		readingTreeFile(t, r, relative)
	}
	readingScan(t, r.f, r.library)
	list, err := r.s.List(ctx, r.p, r.library, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]int{}
	for i, v := range list.Items {
		byID[v.ID] = i
	}
	for _, tc := range []struct {
		path, title, author, series string
		source                      compactcatalog.ReadingValueSource
	}{
		{"Frank Herbert/Dune Saga/Dune.epub", "Dune", "Frank Herbert", "Dune Saga", compactcatalog.ReadingFolder},
		{"Frank Herbert/Dune Saga/Dune Messiah.pdf", "Dune Messiah", "Frank Herbert", "Dune Saga", compactcatalog.ReadingFolder},
		{"Frank Herbert/Dune (12)/Dune.epub", "Dune", "Frank Herbert", "", compactcatalog.ReadingFolder},
		{"Isaac Asimov/Foundation.pdf", "Foundation", "Isaac Asimov", "", compactcatalog.ReadingFolder},
		{"Isaac Asimov/Isaac Asimov - Foundation.epub", "Foundation", "Isaac Asimov", "", compactcatalog.ReadingFileName},
		{"Books/Sci-fi/Frank Herbert/Dune.pdf", "Dune", "Frank Herbert", "", compactcatalog.ReadingFolder},
		{"Ebooks/Foundation.pdf", "Foundation", "", "", ""},
		{"Some Book [Group] (Digital) 1080p.pdf", "Some Book", "", "", ""},
		{"[Group] (Digital).pdf", "[Group] (Digital)", "", "", ""},
		{"Books/Identity/Identity One.pdf", "Identity One", "Identity", "", compactcatalog.ReadingFolder},
		{"Downloads/Identity/Identity Two.pdf", "Identity Two", "Identity", "", compactcatalog.ReadingFolder},
		{"     /Reader Book.pdf", "Reader Book", "", "", ""},
		{"Author\nName - Work.pdf", "Work", "Author Name", "", compactcatalog.ReadingFileName},
	} {
		t.Run(tc.path, func(t *testing.T) {
			id := readingPathItem(t, r, filepath.Join(filepath.Dir(r.path), filepath.FromSlash(tc.path)))
			i, ok := byID[id]
			if !ok {
				t.Fatal("readable file was dropped")
			}
			row := list.Items[i]
			if row.Title != tc.title {
				t.Errorf("title=%q want %q", row.Title, tc.title)
			}
			if tc.author == "" {
				if row.Author != nil {
					t.Errorf("generic/root attribution invented: %+v", row.Author)
				}
			} else if row.Author == nil || row.Author.Value != tc.author || row.Author.Source != tc.source {
				t.Errorf("author=%+v want %q/%s", row.Author, tc.author, tc.source)
			}
			if tc.series == "" {
				if row.Series != nil {
					t.Errorf("per-book folder became series: %+v", row.Series)
				}
			} else if row.Series == nil || row.Series.Name != tc.series || row.Series.Source != compactcatalog.ReadingFolder {
				t.Errorf("series=%+v want %q", row.Series, tc.series)
			}
		})
	}
	first := readingPathItem(t, r, filepath.Join(filepath.Dir(r.path), "Frank Herbert/Dune (12)/Dune.epub"))
	second := readingPathItem(t, r, filepath.Join(filepath.Dir(r.path), "Frank Herbert/Dune (12)/Dune.pdf"))
	if first != second {
		t.Fatal("formats counted/attached as two titles")
	}
	versions, err := r.s.Versions(ctx, r.p, first)
	if err != nil || len(versions) != 2 {
		t.Fatal("actual alternative versions lost", versions, err)
	}
}

func TestReadingPrivatePathComicFolderReclassification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "comic", "cbz")
	ctx := context.Background()
	first := readingTreeFile(t, r, "Little Nemo/Little Nemo 01.cbz")
	readingScan(t, r.f, r.library)
	id := readingPathItem(t, r, first)
	get := func() compactcatalog.ReadingValues {
		t.Helper()
		var entity int64
		if err := r.f.db.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, id).Scan(&entity); err != nil {
			t.Fatal(err)
		}
		g, err := dbwork.BeginSnapshot(ctx, r.f.db)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Rollback()
		v, err := compactcatalog.ReadingAutomaticFieldsTx(ctx, g.Tx(), entity)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := get()
	if v.Author == nil || v.Author.Value != "Little Nemo" || v.Series != nil {
		t.Fatal("one-title folder guess is not genuine", v)
	}
	second := readingTreeFile(t, r, "Little Nemo/02.cbz")
	readingScan(t, r.f, r.library)
	v = get()
	if v.Author != nil || v.Series == nil || v.Series.Value != "Little Nemo" || v.SeriesNumber == nil || v.SeriesNumber.Value != 1 {
		t.Fatal("1-to-2 folder refresh failed", v)
	}
	secondID := readingPathItem(t, r, second)
	list, err := r.s.List(ctx, r.p, r.library, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, row := range list.Items {
		if row.ID == secondID {
			seen = row.Series != nil && row.Series.Number != nil && *row.Series.Number == 2
		}
	}
	if !seen {
		t.Fatal("bare sequence token was not published")
	}
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	readingScan(t, r.f, r.library)
	v = get()
	if v.Author == nil || v.Author.Value != "Little Nemo" || v.Series != nil {
		t.Fatal("missing sibling did not refresh available title", v)
	}
}

func TestReadingPrivatePathCandidatesAndCurrentAssociation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	ctx := context.Background()
	name := readingTreeFile(t, r, "Attribution/Work.pdf")
	readingScan(t, r.f, r.library)
	id := readingPathItem(t, r, name)
	var item, asset int64
	var object, revision string
	if err := r.f.db.QueryRow(`SELECT v.item_id,v.asset_id,v.object_id,v.source_revision FROM reading_path_head v JOIN catalog_entities e ON e.id=v.item_id WHERE e.public_id=pid_blob(?) AND v.author_source='folder'`, id).Scan(&item, &asset, &object, &revision); err != nil {
		t.Fatal("real ingest did not publish provenance", err)
	}
	automatic := func() compactcatalog.ReadingValues {
		t.Helper()
		g, err := dbwork.BeginSnapshot(ctx, r.f.db)
		if err != nil {
			t.Fatal(err)
		}
		defer g.Rollback()
		v, err := compactcatalog.ReadingAutomaticFieldsTx(ctx, g.Tx(), item)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// A canonical accepted candidate fixture tests precedence, not a grant to
	// read embedded archive metadata (that future producer is not implemented).
	if _, err := r.f.db.Exec(`INSERT INTO reading_source_values(item_id,asset_id,field,text_value,source,inventory_object_id,source_revision) VALUES(?,?,'author','Embedded attribution','file-metadata',?,?)`, item, asset, object, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.db.Exec(`UPDATE reading_path_head SET author='Previous path guess' WHERE item_id=? AND author_source='folder'`, item); err != nil {
		t.Fatal(err)
	}
	readingTreeFile(t, r, "Attribution/Another.pdf")
	readingScan(t, r.f, r.library)
	v := automatic()
	if v.Author == nil || v.Author.Value != "Embedded attribution" || v.Author.Source != compactcatalog.ReadingFileMetadata {
		t.Fatal("path refresh overwrote higher source", v)
	}
	var pathValue string
	if err := r.f.db.QueryRow(`SELECT author FROM reading_path_head WHERE item_id=? AND asset_id=? AND author_source='folder'`, item, asset).Scan(&pathValue); err != nil || pathValue != "Attribution" {
		t.Fatal("real path refresh did not run", pathValue, err)
	}
	if _, err := r.f.db.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author','A & B','Embedded attribution',1,?,datetime('now'))`, item, identity.PersonalKey(r.p.Viewer)); err != nil {
		t.Fatal(err)
	}
	var sourceID string
	if err := r.f.db.QueryRow(`SELECT token FROM catalog_assets WHERE id=?`, asset).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	session, document, err := r.s.Prepare(ctx, r.p, id, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if document.Metadata.Author != "A & B" {
		t.Fatal("manifest revived archive/path identity over actual owner", document.Metadata.Author)
	}
	if err = r.s.CloseSession(r.p, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = r.f.db.Exec(`UPDATE metadata_owner_fields SET value='',locked=0 WHERE kind='item' AND entity_id=? AND field='author'`, item); err != nil {
		t.Fatal(err)
	}
	list, err := r.s.List(ctx, r.p, r.library, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, row := range list.Items {
		if row.ID == id {
			matched = row.Author != nil && row.Author.Value == "Embedded attribution" && row.Author.Source == compactcatalog.ReadingFileMetadata
		}
	}
	if !matched {
		t.Fatal("clear did not restore genuine automatic value/source")
	}
	if _, err = r.f.db.Exec(`DELETE FROM reading_source_values WHERE item_id=? AND asset_id=? AND source='file-metadata'`, item, asset); err != nil {
		t.Fatal(err)
	}
	v = automatic()
	if v.Author == nil || v.Author.Value != "Attribution" || v.Author.Source != compactcatalog.ReadingFolder {
		t.Fatal("path fallback lost", v)
	}
	if _, err = r.f.db.Exec(`UPDATE reading_path_head SET source_revision='stale-source-revision' WHERE item_id=? AND asset_id=?`, item, asset); err != nil {
		t.Fatal(err)
	}
	if v = automatic(); v.Author != nil {
		t.Fatal("stale revision accepted", v)
	}
	if _, err = r.f.db.Exec(`UPDATE reading_path_head SET object_id=(SELECT o.id FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE a.path=? LIMIT 1),source_revision=(SELECT o.revision FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE a.path=? LIMIT 1) WHERE item_id=? AND asset_id=?`, r.path, r.path, item, asset); err != nil {
		t.Fatal(err)
	}
	if v = automatic(); v.Author != nil {
		t.Fatal("foreign object with genuine current revision accepted", v)
	}
}
