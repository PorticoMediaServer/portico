package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// O-101: capture and every normalize sweep now hydrate a bounded id window with
// one statement. These tests pin the two properties that make that safe:
//
//  1. readPathObjects returns exactly what readPathObject returns per id, for
//     every branch of the projection (eligible, ineligible, contextual, absent).
//  2. Both statements still seek; batching never introduced a catalogue scan.

func openPathBatchDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func execPathBatch(t *testing.T, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(q, args...); err != nil {
		t.Fatalf("%v: %s", err, q)
	}
}

func pathBatchEntity(t *testing.T, tx *sql.Tx, library int64, kind int) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(`INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head) VALUES(randomblob(16),?,?,'Title','','') RETURNING id`, library, kind).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func pathBatchAsset(t *testing.T, tx *sql.Tx, item int64, token, path, format string) int64 {
	t.Helper()
	var asset int64
	if err := tx.QueryRow(`INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(?,?,1,1,'','','',0,0,0) RETURNING id`, token, path).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	execPathBatch(t, tx, `INSERT INTO catalog_asset_links(entity_id,asset_id) VALUES(?,?)`, item, asset)
	execPathBatch(t, tx, `INSERT INTO reading_publications VALUES(?,?,?)`, item, asset, format)
	return asset
}

func pathBatchObject(t *testing.T, tx *sql.Tx, id, source, incarnation, token, relative, revision string) {
	t.Helper()
	execPathBatch(t, tx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,seen_job) VALUES(?,?,?,?,?,?,?,'{}',1,1,'job')`, id, source, token, incarnation, relative, filepath.Dir(relative), revision)
}

// pathBatchSeed is one raw object and the exact hydration the projection must
// agree on. Absent objects match the point read's sql.ErrNoRows.
type pathBatchSeed struct {
	id                 string
	hydrated, eligible bool
	context            bool
}

// seedPathBatchObjects builds every branch readPathObject can take. The two
// hydration statements must be indistinguishable on all of them.
func seedPathBatchObjects(t *testing.T, tx *sql.Tx) []pathBatchSeed {
	t.Helper()
	for _, library := range []struct{ id, kind, root string }{{"books", "book", "/books"}, {"comics", "comic", "/comics"}, {"comics-remote", "comic", "/comics-remote"}, {"films", "movie", "/films"}} {
		execPathBatch(t, tx, `INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, library.id, library.id, library.kind, library.root)
	}
	execPathBatch(t, tx, `UPDATE library_sources SET kind='network' WHERE id='comics-remote'`)
	source := func(library string) (int64, string, string) {
		var handle int64
		var id, incarnation string
		if err := tx.QueryRow(`SELECT cl.id,s.id,s.incarnation FROM catalog_libraries cl JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.library_id=?`, library).Scan(&handle, &id, &incarnation); err != nil {
			t.Fatal(err)
		}
		return handle, id, incarnation
	}
	books, bookSource, bookInc := source("books")
	comics, comicSource, comicInc := source("comics")
	remote, remoteSource, remoteInc := source("comics-remote")
	films, filmSource, filmInc := source("films")

	ebook := pathBatchEntity(t, tx, books, 20)
	comic := pathBatchEntity(t, tx, comics, 21)
	remoteComic := pathBatchEntity(t, tx, remote, 21)
	film := pathBatchEntity(t, tx, films, 20)
	spare := pathBatchEntity(t, tx, books, 20)

	pathBatchAsset(t, tx, ebook, "tok-a", "/books/Author/Series/01 - Title.epub", "epub")
	pathBatchAsset(t, tx, ebook, "tok-b", "/books/Author/Series/02 - Second.pdf", "pdf")
	pathBatchAsset(t, tx, ebook, "tok-unsupported", "/books/Author/Series/03 - Unsupported.epub", "epub")
	pathBatchAsset(t, tx, ebook, "tok-context", "/elsewhere/04 - Context.epub", "epub")
	pathBatchAsset(t, tx, comic, "tok-comic", "/comics/Run/01.cbz", "cbz")
	pathBatchAsset(t, tx, comic, "tok-cbr", "/comics/Run/02.cbr", "cbr")
	pathBatchAsset(t, tx, remoteComic, "tok-remote", "/comics-remote/Run/03.cbr", "cbr")
	pathBatchAsset(t, tx, film, "tok-film", "/films/Feature.epub", "epub")
	pathBatchAsset(t, tx, spare, "tok-retired", "/books/Author/Series/05 - Retired.epub", "epub")
	pathBatchAsset(t, tx, spare, "tok-no-publication", "/books/Author/Series/06 - Bare.epub", "epub")
	execPathBatch(t, tx, `DELETE FROM reading_publications WHERE asset_id=(SELECT id FROM catalog_assets WHERE token='tok-no-publication')`)
	pathBatchAsset(t, tx, spare, "tok-unavailable", "/books/Author/Series/07 - Gone.epub", "epub")
	execPathBatch(t, tx, `UPDATE catalog_assets SET available=0 WHERE token='tok-unavailable'`)

	pathBatchObject(t, tx, "b1", bookSource, bookInc, "tok-a", "Author/Series/01 - Title.epub", "r1")
	// The canonical witness clause only arbitrates between rows that could share
	// (source,relative_path,asset,incarnation). The baseline's partial unique
	// index allows exactly one retired=0 object per source/path, so an ACTIVE
	// duplicate is not representable. Pin that invariant here instead of
	// inventing a fixture the schema forbids.
	if _, err := tx.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,seen_job) VALUES('b2',?,?,?,?,?,'r1','{}',1,1,'job')`, bookSource, "tok-a", bookInc, "Author/Series/01 - Title.epub", "Author/Series"); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("active source/path duplicate: %v, want a UNIQUE violation on inventory_objects_path", err)
	}
	// A retired twin on the same witness key is representable. The projection's
	// retired=0 witness seek must neither hydrate it nor let it shadow b1.
	execPathBatch(t, tx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,seen_job,retired) VALUES('b1-twin',?,?,?,?,?,'r1','{}',1,1,'job',1)`, bookSource, "tok-a", bookInc, "Author/Series/01 - Title.epub", "Author/Series")
	pathBatchObject(t, tx, "b3", bookSource, bookInc, "tok-unsupported", "Author/Series/03 - Unsupported.epub", "r1")
	execPathBatch(t, tx, `UPDATE inventory_objects SET unsupported_reason='reject' WHERE id='b3'`)
	// The asset path does not round-trip from root+relative: context fails even
	// though SQL eligibility passes.
	pathBatchObject(t, tx, "b4", bookSource, bookInc, "tok-context", "Author/Series/04 - Context.epub", "r1")
	pathBatchObject(t, tx, "c1", comicSource, comicInc, "tok-comic", "Run/01.cbz", "r1")
	pathBatchObject(t, tx, "c2", comicSource, comicInc, "tok-cbr", "Run/02.cbr", "r1")
	pathBatchObject(t, tx, "c3", remoteSource, remoteInc, "tok-remote", "Run/03.cbr", "r1")
	pathBatchObject(t, tx, "m1", filmSource, filmInc, "tok-film", "Feature.epub", "r1")
	pathBatchObject(t, tx, "r1", bookSource, bookInc, "tok-retired", "Author/Series/05 - Retired.epub", "r1")
	execPathBatch(t, tx, `UPDATE inventory_objects SET retired=1 WHERE id='r1'`)
	pathBatchObject(t, tx, "n1", bookSource, bookInc, "tok-no-publication", "Author/Series/06 - Bare.epub", "r1")
	pathBatchObject(t, tx, "u1", bookSource, bookInc, "tok-unavailable", "Author/Series/07 - Gone.epub", "r1")

	return []pathBatchSeed{
		{id: "b1", hydrated: true, eligible: true, context: true},
		// The retired twin shares b1's witness key but never hydrates, and it
		// must not displace b1 in the witness seek.
		{id: "b1-twin"},
		{id: "b3", hydrated: true},
		{id: "b4", hydrated: true},
		{id: "c1", hydrated: true, eligible: true, context: true},
		{id: "c2", hydrated: true, eligible: true, context: true},
		// cb7/cbr outside a local source is not eligible for a comic.
		{id: "c3", hydrated: true},
		{id: "n1", hydrated: true},
		{id: "u1", hydrated: true},
		// A movie library is not a Reading library.
		{id: "m1"},
		// Retired raw objects are outside the projection entirely.
		{id: "r1"},
		// An id that does not exist is the point read's sql.ErrNoRows.
		{id: "missing-object"},
	}
}

func TestReadPathObjectsMatchesPointReads(t *testing.T) {
	db := openPathBatchDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()

	seeds := seedPathBatchObjects(t, tx)
	ids := make([]string, len(seeds))
	for i, seed := range seeds {
		ids[i] = seed.id
	}
	batch, err := readPathObjects(ctx, tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range seeds {
		want, err := readPathObject(ctx, tx, seed.id)
		if errors.Is(err, sql.ErrNoRows) {
			if _, ok := batch[seed.id]; ok {
				t.Fatalf("%s: batch hydrated an object the point read rejects", seed.id)
			}
			if seed.hydrated {
				t.Fatalf("%s: point read unexpectedly empty", seed.id)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, ok := batch[seed.id]
		if !ok {
			t.Fatalf("%s: batch lost a hydrated object", seed.id)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: batch and point hydration differ\nbatch %+v\npoint %+v", seed.id, got, want)
		}
		if !seed.hydrated {
			t.Fatalf("%s: expected the projection to drop this object", seed.id)
		}
		if got.Eligible != seed.eligible || got.Context != seed.context {
			t.Fatalf("%s: eligible=%v context=%v, want %v/%v", seed.id, got.Eligible, got.Context, seed.eligible, seed.context)
		}
	}
}

func TestReadPathObjectsChunksPastTheWindow(t *testing.T) {
	db := openPathBatchDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ctx := context.Background()

	var handle int64
	var source, incarnation string
	execPathBatch(t, tx, `INSERT INTO libraries(id,name,kind,root) VALUES('bulk','Bulk','book','/bulk')`)
	if err = tx.QueryRow(`SELECT cl.id,s.id,s.incarnation FROM catalog_libraries cl JOIN library_sources s ON s.library_id=cl.library_id WHERE cl.library_id='bulk'`).Scan(&handle, &source, &incarnation); err != nil {
		t.Fatal(err)
	}
	item := pathBatchEntity(t, tx, handle, 20)
	ids := make([]string, pathObjectBatchMax+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("bulk-%04d", i)
		relative := fmt.Sprintf("Author/Series/%04d.epub", i)
		token := fmt.Sprintf("tok-bulk-%04d", i)
		pathBatchAsset(t, tx, item, token, "/bulk/"+relative, "epub")
		pathBatchObject(t, tx, ids[i], source, incarnation, token, relative, "r1")
	}
	batch, err := readPathObjects(ctx, tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != len(ids) {
		t.Fatalf("batch of %d ids hydrated %d objects", len(ids), len(batch))
	}
	for _, id := range ids {
		want, err := readPathObject(ctx, tx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := batch[id]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: batch and point hydration differ\nbatch %+v\npoint %+v", id, got, want)
		}
	}
}

// Query plans don't depend on parameter values, so plan the real statements
// with NULL parameters. A batch must start at json_each and then seek; the
// point read must seek. Any SCAN of a catalogue table fails.
func TestReadingPathObjectHydrationSeeks(t *testing.T) {
	db := openPathBatchDB(t)
	for name, q := range map[string]string{"point": pathObjectPointSQL, "batch": pathObjectBatch} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+q, placeholders(q)...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		for _, line := range plan {
			if strings.Contains(line, "USE TEMP B-TREE") {
				t.Fatalf("%s hydration materializes scalar rows: %s", name, line)
			}
			if !strings.HasPrefix(line, "SCAN ") {
				continue
			}
			if name == "batch" && strings.HasPrefix(line, "SCAN j VIRTUAL TABLE") {
				continue
			}
			t.Fatalf("%s hydration scans: %s\nplan:\n%s", name, line, strings.Join(plan, "\n"))
		}
	}
}

// readPathObjects hydrates an unbounded batch with one statement per window. No
// production caller uses it: every batched path uses readPathObjectsCapped so a
// window stays inside the ordinary byte budget, and the one-object seam uses
// the capped read too. It stays as the parity oracle for tests. An absent id
// means the same thing the point read's sql.ErrNoRows meant to its callers; the
// one-id window keeps using the point read unchanged.
func readPathObjects(ctx context.Context, tx *sql.Tx, ids []string) (map[string]pathObject, error) {
	out := make(map[string]pathObject, min(len(ids), pathObjectBatchMax))
	for start := 0; start < len(ids); start += pathObjectBatchMax {
		chunk := ids[start:min(start+pathObjectBatchMax, len(ids))]
		if len(chunk) == 1 {
			v, err := readPathObject(ctx, tx, chunk[0])
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			out[v.ID] = v
			continue
		}
		listed, err := json.Marshal(chunk)
		if err != nil {
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, pathObjectBatch, string(listed))
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			_, v, err := scanPathObjectRow(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			// QueryRow keeps a point read's first row; keep the batch's first
			// row per id so a duplicate can never change a decision.
			if _, seen := out[v.ID]; !seen {
				out[v.ID] = v
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
