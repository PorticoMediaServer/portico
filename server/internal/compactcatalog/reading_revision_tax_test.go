package compactcatalog_test

// P2-64 Gate A: the Reading inventory producers resolve their catalogue
// library once per statement and keep a single input fence per affected
// library. These tests exercise the six replaced triggers against a real
// SQLite database and compare the physical row-write count (total_changes())
// with the six triggers from HEAD installed in an independent database.
//
// Expected Reading-side write deltas (current minus HEAD):
//
//	inventory insert             -1  canonical input upsert removed
//	inventory delete             -1  canonical input upsert removed
//	same-library revision update -1  duplicate old/new input upserts collapse
//	same-library asset move      -3  two producers' four input upserts become one
//	same-library source move     -3  DISTINCT collapses the old/new source pair
//	cross-library source move    -2  one fence per affected library
//	bare id rekey                +2  object UPDATE now retains the fence plus
//	                                 the old and new object keys (the trade)
//	non-Reading library           0  producers still skip other library kinds
//
// total_changes() counts the statement's own row plus every trigger row, on
// one pinned connection, so the delta is the trigger tax for that path.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// originalReadingTriggers is the six-trigger inventory block from HEAD, kept
// verbatim so the baseline database runs the production bodies this gate
// rewrites. It is installed only in a separate test database.
var originalReadingTriggers = []struct{ name, ddl string }{
	{"reading_path_inventory_insert", `CREATE TRIGGER reading_path_inventory_insert AFTER INSERT ON inventory_objects WHEN EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) BEGIN
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),'object',CAST(NEW.id AS TEXT) WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
	{"reading_path_inventory_update", `CREATE TRIGGER reading_path_inventory_update AFTER UPDATE OF source_id,asset_id,root_incarnation,relative_path,revision,state,retired,unsupported_reason ON inventory_objects WHEN (OLD.source_id IS NOT NEW.source_id OR OLD.asset_id IS NOT NEW.asset_id OR OLD.root_incarnation IS NOT NEW.root_incarnation OR OLD.relative_path IS NOT NEW.relative_path OR OLD.revision IS NOT NEW.revision OR OLD.state IS NOT NEW.state OR OLD.retired IS NOT NEW.retired OR OLD.unsupported_reason IS NOT NEW.unsupported_reason) AND (EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) OR EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic'))) BEGIN
 UPDATE reading_path_scope SET desired=desired+1,queued=1 WHERE id=(SELECT scope_id FROM reading_path_object_keys WHERE object_id=OLD.id AND context_active=1);
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),'object',CAST(OLD.id AS TEXT) WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),'object',CAST(NEW.id AS TEXT) WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
	{"reading_path_inventory_delete", `CREATE TRIGGER reading_path_inventory_delete AFTER DELETE ON inventory_objects WHEN EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) BEGIN
 UPDATE reading_path_scope SET desired=desired+1,queued=1 WHERE id=(SELECT scope_id FROM reading_path_object_keys WHERE object_id=OLD.id AND context_active=1);
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),'object',CAST(OLD.id AS TEXT) WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
	{"reading_path_inventory_canonical_insert", `CREATE TRIGGER reading_path_inventory_canonical_insert AFTER INSERT ON inventory_objects WHEN EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) BEGIN
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),'asset',NEW.asset_id WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
	{"reading_path_inventory_canonical_update", `CREATE TRIGGER reading_path_inventory_canonical_update AFTER UPDATE OF id,asset_id,source_id,root_incarnation,relative_path,retired ON inventory_objects WHEN (OLD.id IS NOT NEW.id OR OLD.asset_id IS NOT NEW.asset_id OR OLD.source_id IS NOT NEW.source_id OR OLD.root_incarnation IS NOT NEW.root_incarnation OR OLD.relative_path IS NOT NEW.relative_path OR OLD.retired IS NOT NEW.retired) AND (EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) OR EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic'))) BEGIN
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),'asset',OLD.asset_id WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')),'asset',NEW.asset_id WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=NEW.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
	{"reading_path_inventory_canonical_delete", `CREATE TRIGGER reading_path_inventory_canonical_delete AFTER DELETE ON inventory_objects WHEN EXISTS(SELECT 1 FROM library_sources s JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) BEGIN
 INSERT INTO reading_path_inputs(library_id,revision) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),1 WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id) DO UPDATE SET revision=revision+1;
 INSERT INTO reading_path_dirty_work(library_id,kind,work_key) SELECT (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')),'asset',OLD.asset_id WHERE (SELECT cl.id FROM library_sources s JOIN catalog_libraries cl ON cl.library_id=s.library_id JOIN libraries l ON l.id=s.library_id WHERE s.id=OLD.source_id AND l.kind IN('book','comic')) IS NOT NULL ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1;
END;`},
}

func installOriginalReadingTriggers(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	for _, trigger := range originalReadingTriggers {
		c.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name)
		c.Exec(trigger.ddl)
	}
}

func newRevisionTaxPair(t *testing.T) (current, original *catalogtest.Catalog) {
	t.Helper()
	current = catalogtest.Open(t)
	original = catalogtest.Open(t)
	installOriginalReadingTriggers(t, original)
	return current, original
}

type revisionTaxLibrary struct {
	t      *testing.T
	c      *catalogtest.Catalog
	name   string
	root   string
	handle int64
	source string
	inc    string
}

func newRevisionTaxLibrary(t *testing.T, c *catalogtest.Catalog, name, kind string) *revisionTaxLibrary {
	t.Helper()
	l := &revisionTaxLibrary{t: t, c: c, name: name, root: t.TempDir()}
	l.handle = c.Library(name, name, kind, l.root)
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=?`, name).Scan(&l.source, &l.inc); err != nil {
		t.Fatalf("library %q source: %v", name, err)
	}
	c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity='root' WHERE id=?`, l.source)
	return l
}

type revisionTaxWorld struct {
	t   *testing.T
	c   *catalogtest.Catalog
	a   *revisionTaxLibrary // Reading library "tax-a"
	b   *revisionTaxLibrary // Reading library "tax-b"
	non *revisionTaxLibrary // movie library negative control

	item       int64
	assetToken string
	otherToken string // a second asset for the same item

	altSource    string // a second source inside library "tax-a"
	altSourceInc string

	object   string
	relative string

	nonObject   string
	nonRelative string
	nonToken    string
}

func newRevisionTaxWorld(t *testing.T, c *catalogtest.Catalog) *revisionTaxWorld {
	t.Helper()
	w := &revisionTaxWorld{
		t:        t,
		c:        c,
		a:        newRevisionTaxLibrary(t, c, "tax-a", "book"),
		b:        newRevisionTaxLibrary(t, c, "tax-b", "book"),
		non:      newRevisionTaxLibrary(t, c, "tax-non", "movie"),
		object:   "tax-object",
		relative: "Author/Series/Series 01.epub",

		nonObject:   "tax-movie-object",
		nonRelative: "Movies/Tax Movie.mkv",
	}
	w.item = c.Entity(compactcatalog.Entity{Library: w.a.handle, Kind: compactcatalog.Ebook, Key: "tax-item", Title: w.relative}, nil).ID
	_, w.assetToken = c.File(w.item, filepath.Join(w.a.root, filepath.FromSlash(w.relative)), 0)
	_, w.otherToken = c.File(w.item, filepath.Join(w.a.root, filepath.FromSlash("Author/Series/Series 01.pdf")), 0)

	altRoot := t.TempDir()
	c.Exec(`INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES('tax-a-alt','tax-a','tax-a-alt',?,?)`, altRoot, altRoot)
	if err := c.DB.QueryRow(`SELECT incarnation FROM library_sources WHERE id='tax-a-alt'`).Scan(&w.altSourceInc); err != nil {
		t.Fatalf("alternate source: %v", err)
	}
	w.altSource = "tax-a-alt"

	movie := c.Movie(w.non.handle, filepath.Join(w.non.root, filepath.FromSlash(w.nonRelative)), "Tax Movie", 2020)
	w.nonToken = movie.Token
	return w
}

type revisionTaxExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func revisionTaxInsertObject(ctx context.Context, ex revisionTaxExecer, l *revisionTaxLibrary, object, token, relative string) error {
	_, err := ex.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,seen_job) VALUES(?,?,?,?,?,?,'r1','{}',1,1,'tax')`,
		object, l.source, token, l.inc, filepath.FromSlash(relative), filepath.Dir(filepath.FromSlash(relative)))
	return err
}

func revisionTaxSetupObject(t *testing.T, l *revisionTaxLibrary, object, token, relative string) {
	t.Helper()
	if err := revisionTaxInsertObject(context.Background(), l.c.DB, l, object, token, relative); err != nil {
		t.Fatalf("setup inventory object %s: %v", object, err)
	}
}

// revisionTaxMeasure runs one statement on a dedicated connection and returns
// the physical row writes it caused, triggers included.
func revisionTaxMeasure(t *testing.T, c *catalogtest.Catalog, fn func(ctx context.Context, conn *sql.Conn) error) int64 {
	t.Helper()
	ctx := context.Background()
	conn, err := c.DB.Conn(ctx)
	if err != nil {
		t.Fatalf("measure connection: %v", err)
	}
	defer conn.Close()
	var before, after int64
	if err = conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatalf("total_changes before: %v", err)
	}
	if err = fn(ctx, conn); err != nil {
		t.Fatalf("measured write: %v", err)
	}
	if err = conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatalf("total_changes after: %v", err)
	}
	return after - before
}

func revisionTaxWriteDelta(t *testing.T, path string, current, original, want int64) {
	t.Helper()
	delta := current - original
	t.Logf("write tax %-28s gate-a=%3d original=%3d delta=%+d", path, current, original, delta)
	if delta != want {
		t.Fatalf("%s: physical write delta %+d, want %+d (gate-a %d, original %d)", path, delta, want, current, original)
	}
}

func revisionTaxInputs(t *testing.T, c *catalogtest.Catalog, libraries ...int64) map[int64]int64 {
	t.Helper()
	out := make(map[int64]int64, len(libraries))
	for _, library := range libraries {
		var revision int64
		err := c.DB.QueryRow(`SELECT revision FROM reading_path_inputs WHERE library_id=?`, library).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			revision = 0
		} else if err != nil {
			t.Fatalf("reading_path_inputs library %d: %v", library, err)
		}
		out[library] = revision
	}
	return out
}

func revisionTaxInputGain(t *testing.T, c *catalogtest.Catalog, before map[int64]int64, library int64) int64 {
	t.Helper()
	return revisionTaxInputs(t, c, library)[library] - before[library]
}

func revisionTaxDirtyRequested(t *testing.T, c *catalogtest.Catalog, library int64, kind, key string) int64 {
	t.Helper()
	var requested int64
	err := c.DB.QueryRow(`SELECT requested FROM reading_path_dirty_work WHERE library_id=? AND kind=? AND work_key=?`, library, kind, key).Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("reading_path_dirty_work %d/%s/%s: %v", library, kind, key, err)
	}
	return requested
}

func revisionTaxWantDirty(t *testing.T, c *catalogtest.Catalog, library int64, kind, key string, want int64) {
	t.Helper()
	if got := revisionTaxDirtyRequested(t, c, library, kind, key); got != want {
		t.Fatalf("library %d %s key %q requested=%d, want %d", library, kind, key, got, want)
	}
}

func revisionTaxActivateObject(t *testing.T, c *catalogtest.Catalog, w *revisionTaxWorld) int64 {
	t.Helper()
	c.Exec(`INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key) VALUES(?,?,?,'','Author/Series')`, w.a.handle, w.a.source, w.a.inc)
	var scope int64
	if err := c.DB.QueryRow(`SELECT id FROM reading_path_scope WHERE source_id=? AND directory_key='Author/Series'`, w.a.source).Scan(&scope); err != nil {
		t.Fatalf("scope: %v", err)
	}
	c.Exec(`INSERT INTO reading_path_object_keys(object_id,scope_id,item_id,context_active) VALUES(?,?,?,1)`, w.object, scope, w.item)
	return scope
}

func revisionTaxScopeDesired(t *testing.T, c *catalogtest.Catalog, scope int64) int64 {
	t.Helper()
	var desired int64
	if err := c.DB.QueryRow(`SELECT desired FROM reading_path_scope WHERE id=?`, scope).Scan(&desired); err != nil {
		t.Fatalf("scope %d: %v", scope, err)
	}
	return desired
}

func TestReadingRevisionTaxInventoryInsertDelete(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)

	currentInputs := revisionTaxInputs(t, current, cw.a.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		return revisionTaxInsertObject(ctx, conn, cw.a, cw.object, cw.assetToken, cw.relative)
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		return revisionTaxInsertObject(ctx, conn, ow.a, ow.object, ow.assetToken, ow.relative)
	})
	revisionTaxWriteDelta(t, "inventory insert", currentWrites, originalWrites, -1)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a insert fenced library %d times, want once", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 2 {
		t.Fatalf("original insert fenced library %d times, want twice", gain)
	}
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 1)
	// The item's file link already queued the asset key during setup.
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 2)
	revisionTaxWantDirty(t, original, ow.a.handle, "object", ow.object, 1)
	revisionTaxWantDirty(t, original, ow.a.handle, "asset", ow.assetToken, 2)

	currentInputs = revisionTaxInputs(t, current, cw.a.handle)
	originalInputs = revisionTaxInputs(t, original, ow.a.handle)
	currentWrites = revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, cw.object)
		return err
	})
	originalWrites = revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, ow.object)
		return err
	})
	revisionTaxWriteDelta(t, "inventory delete", currentWrites, originalWrites, -1)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a delete fenced library %d times, want once", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 2 {
		t.Fatalf("original delete fenced library %d times, want twice", gain)
	}
	// The worker still finds both independent queues after a delete.
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 2)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 3)
}

func TestReadingRevisionTaxSameLibraryRevisionUpdate(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)
	revisionTaxSetupObject(t, cw.a, cw.object, cw.assetToken, cw.relative)
	revisionTaxSetupObject(t, ow.a, ow.object, ow.assetToken, ow.relative)

	currentInputs := revisionTaxInputs(t, current, cw.a.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET revision='r2' WHERE id=?`, cw.object)
		return err
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET revision='r2' WHERE id=?`, ow.object)
		return err
	})
	revisionTaxWriteDelta(t, "same-library revision update", currentWrites, originalWrites, -1)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a revision update fenced library %d times, want once", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 2 {
		t.Fatalf("original revision update fenced library %d times, want twice (old and new projection)", gain)
	}
	// The update trigger still queues the object twice (old and new projection of
	// the same id), while the canonical producer has no asset work for a revision.
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 3)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 2)
}

func TestReadingRevisionTaxSameLibraryAssetMove(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)
	revisionTaxSetupObject(t, cw.a, cw.object, cw.assetToken, cw.relative)
	revisionTaxSetupObject(t, ow.a, ow.object, ow.assetToken, ow.relative)
	currentScope := revisionTaxActivateObject(t, current, cw)
	originalScope := revisionTaxActivateObject(t, original, ow)
	currentDesired := revisionTaxScopeDesired(t, current, currentScope)
	originalDesired := revisionTaxScopeDesired(t, original, originalScope)

	currentInputs := revisionTaxInputs(t, current, cw.a.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET asset_id=? WHERE id=?`, cw.otherToken, cw.object)
		return err
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET asset_id=? WHERE id=?`, ow.otherToken, ow.object)
		return err
	})
	revisionTaxWriteDelta(t, "same-library asset move", currentWrites, originalWrites, -3)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a asset move fenced library %d times, want once", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 4 {
		t.Fatalf("original asset move fenced library %d times, want four", gain)
	}
	// The scope restart is unchanged by the deduplicated producers.
	if got, want := revisionTaxScopeDesired(t, current, currentScope)-currentDesired, int64(1); got != want {
		t.Fatalf("gate-a asset move bumped scope desired %d, want %d", got, want)
	}
	if got, want := revisionTaxScopeDesired(t, original, originalScope)-originalDesired, int64(1); got != want {
		t.Fatalf("original asset move bumped scope desired %d, want %d", got, want)
	}
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 3)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 3)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.otherToken, 2)
}

func TestReadingRevisionTaxSameLibrarySourceMove(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)
	revisionTaxSetupObject(t, cw.a, cw.object, cw.assetToken, cw.relative)
	revisionTaxSetupObject(t, ow.a, ow.object, ow.assetToken, ow.relative)

	currentInputs := revisionTaxInputs(t, current, cw.a.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET source_id=?,root_incarnation=? WHERE id=?`, cw.altSource, cw.altSourceInc, cw.object)
		return err
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET source_id=?,root_incarnation=? WHERE id=?`, ow.altSource, ow.altSourceInc, ow.object)
		return err
	})
	revisionTaxWriteDelta(t, "same-library source move", currentWrites, originalWrites, -3)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a two-source move fenced one library %d times, want once (DISTINCT pair)", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 4 {
		t.Fatalf("original two-source move fenced one library %d times, want four", gain)
	}
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 3)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 4)
}

func TestReadingRevisionTaxCrossLibrarySourceMove(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)
	revisionTaxSetupObject(t, cw.a, cw.object, cw.assetToken, cw.relative)
	revisionTaxSetupObject(t, ow.a, ow.object, ow.assetToken, ow.relative)

	currentInputs := revisionTaxInputs(t, current, cw.a.handle, cw.b.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle, ow.b.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET source_id=?,root_incarnation=? WHERE id=?`, cw.b.source, cw.b.inc, cw.object)
		return err
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET source_id=?,root_incarnation=? WHERE id=?`, ow.b.source, ow.b.inc, ow.object)
		return err
	})
	revisionTaxWriteDelta(t, "cross-library source move", currentWrites, originalWrites, -2)
	for _, library := range []int64{cw.a.handle, cw.b.handle} {
		if gain := revisionTaxInputGain(t, current, currentInputs, library); gain != 1 {
			t.Fatalf("gate-a cross-library move fenced library %d %d times, want once", library, gain)
		}
	}
	for _, library := range []int64{ow.a.handle, ow.b.handle} {
		if gain := revisionTaxInputGain(t, original, originalInputs, library); gain != 2 {
			t.Fatalf("original cross-library move fenced library %d %d times, want twice", library, gain)
		}
	}
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 2)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 3)
	revisionTaxWantDirty(t, current, cw.b.handle, "object", cw.object, 1)
	revisionTaxWantDirty(t, current, cw.b.handle, "asset", cw.assetToken, 1)
}

func TestReadingRevisionTaxBareIDRekey(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)
	revisionTaxSetupObject(t, cw.a, cw.object, cw.assetToken, cw.relative)
	revisionTaxSetupObject(t, ow.a, ow.object, ow.assetToken, ow.relative)

	const rekeyed = "tax-object-rekeyed"
	currentInputs := revisionTaxInputs(t, current, cw.a.handle)
	originalInputs := revisionTaxInputs(t, original, ow.a.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET id=? WHERE id=?`, rekeyed, cw.object)
		return err
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET id=? WHERE id=?`, rekeyed, ow.object)
		return err
	})
	// The original inventory UPDATE omits id, so the bare rekey lost its input
	// fence and old object key. Gate A retains both, queues the new object key
	// (a fresh insert plus its enqueue stamp) and drops one input upsert.
	revisionTaxWriteDelta(t, "bare id rekey", currentWrites, originalWrites, +2)
	if gain := revisionTaxInputGain(t, current, currentInputs, cw.a.handle); gain != 1 {
		t.Fatalf("gate-a rekey fenced library %d times, want once", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.a.handle); gain != 2 {
		t.Fatalf("original rekey fenced library %d times, want twice", gain)
	}
	revisionTaxWantDirty(t, current, cw.a.handle, "object", cw.object, 2)
	revisionTaxWantDirty(t, current, cw.a.handle, "object", rekeyed, 1)
	revisionTaxWantDirty(t, current, cw.a.handle, "asset", cw.assetToken, 4)
}

func TestReadingRevisionTaxNonReadingLibraryControl(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	cw := newRevisionTaxWorld(t, current)
	ow := newRevisionTaxWorld(t, original)

	currentInputs := revisionTaxInputs(t, current, cw.non.handle)
	originalInputs := revisionTaxInputs(t, original, ow.non.handle)
	currentWrites := revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		return revisionTaxInsertObject(ctx, conn, cw.non, cw.nonObject, cw.nonToken, cw.nonRelative)
	})
	originalWrites := revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		return revisionTaxInsertObject(ctx, conn, ow.non, ow.nonObject, ow.nonToken, ow.nonRelative)
	})
	revisionTaxWriteDelta(t, "non-Reading insert", currentWrites, originalWrites, 0)

	currentWrites = revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET revision='r2' WHERE id=?`, cw.nonObject)
		return err
	})
	originalWrites = revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `UPDATE inventory_objects SET revision='r2' WHERE id=?`, ow.nonObject)
		return err
	})
	revisionTaxWriteDelta(t, "non-Reading update", currentWrites, originalWrites, 0)

	currentWrites = revisionTaxMeasure(t, current, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, cw.nonObject)
		return err
	})
	originalWrites = revisionTaxMeasure(t, original, func(ctx context.Context, conn *sql.Conn) error {
		_, err := conn.ExecContext(ctx, `DELETE FROM inventory_objects WHERE id=?`, ow.nonObject)
		return err
	})
	revisionTaxWriteDelta(t, "non-Reading delete", currentWrites, originalWrites, 0)

	if gain := revisionTaxInputGain(t, current, currentInputs, cw.non.handle); gain != 0 {
		t.Fatalf("non-Reading library received %d input fences", gain)
	}
	if gain := revisionTaxInputGain(t, original, originalInputs, ow.non.handle); gain != 0 {
		t.Fatalf("original non-Reading library received %d input fences", gain)
	}
	if got := scalar(t, current.DB, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, cw.non.handle); got != 0 {
		t.Fatalf("non-Reading library received %d dirty work rows", got)
	}
}

// revisionTaxCorpus seeds the existing Reading fixture corpus on one database.
func revisionTaxCorpus(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	f := newCompleteFixture(t, c, "tax-corpus")
	var editions []completePublication
	for _, relative := range []string{
		"Frank Herbert/Dune/Dune 01 - Dune.epub",
		"Frank Herbert/Dune/Dune 02 - Dune Messiah.epub",
		"Frank Herbert/Dune/Books/Dune 03 - Children.epub",
		"Écrivain/Étoiles/Étoiles 01 - Été.epub",
		"Écrivain/Étoiles/Étoiles 02 - Lune.epub",
		"Ursula Le Guin/Earthsea/Cycle One/Earthsea 01.epub",
		"Mixed/Number 01.epub",
		"Mixed/Unnumbered.epub",
		"Solo/Only.epub",
	} {
		editions = append(editions, f.add(relative, "epub", 0))
	}
	// A second version of one title in the same folder keeps the canonical pair.
	f.add("Écrivain/Étoiles/Étoiles 01 - Été.pdf", "pdf", editions[3].item)
	f.settle()
	f.seal()
	completeDrain(t, f.c)
}

func revisionTaxHeads(t *testing.T, c *catalogtest.Catalog) []string {
	t.Helper()
	// publisher_sequence counts builds, and the baseline trigger set legitimately
	// runs more of them; ready parity compares the published content instead.
	rows, err := c.DB.Query(`SELECT o.relative_path,h.object_id,h.source_revision,
		COALESCE(h.author,''),COALESCE(h.author_source,''),COALESCE(h.issue_date,''),
		COALESCE(h.series,''),COALESCE(h.series_source,''),CAST(COALESCE(h.series_number,0) AS TEXT)
		FROM reading_path_head h JOIN inventory_objects o ON o.id=h.object_id
		ORDER BY o.relative_path`)
	if err != nil {
		t.Fatalf("reading_path_head: %v", err)
	}
	defer rows.Close()
	var heads []string
	for rows.Next() {
		var relative, object, revision, author, authorSource, issue, series, seriesSource, number string
		if err = rows.Scan(&relative, &object, &revision, &author, &authorSource, &issue, &series, &seriesSource, &number); err != nil {
			t.Fatalf("reading_path_head row: %v", err)
		}
		heads = append(heads, fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s", relative, object, revision, author, authorSource, issue, series, seriesSource, number))
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("reading_path_head rows: %v", err)
	}
	return heads
}

// TestReadingRevisionTaxWorkerReadyParity drains the same Reading corpus under
// both trigger sets and compares the published head, so the deduplicated input
// fences change the write tax without changing what the worker makes ready.
func TestReadingRevisionTaxWorkerReadyParity(t *testing.T) {
	current, original := newRevisionTaxPair(t)
	revisionTaxCorpus(t, current)
	revisionTaxCorpus(t, original)

	currentHeads := revisionTaxHeads(t, current)
	originalHeads := revisionTaxHeads(t, original)
	if len(currentHeads) == 0 {
		t.Fatal("gate-a corpus published no Reading head rows")
	}
	if !reflect.DeepEqual(currentHeads, originalHeads) {
		for i := 0; i < len(currentHeads) || i < len(originalHeads); i++ {
			var got, want string
			if i < len(currentHeads) {
				got = currentHeads[i]
			}
			if i < len(originalHeads) {
				want = originalHeads[i]
			}
			if got != want {
				t.Fatalf("ready head %d differs\ngate-a %s\noriginal %s", i, got, want)
			}
		}
		t.Fatal("ready heads differ")
	}
	if got := scalar(t, current.DB, `SELECT count(*) FROM reading_path_dirty_work`); got != 0 {
		t.Fatalf("gate-a worker left %d dirty work rows", got)
	}
	if got := scalar(t, original.DB, `SELECT count(*) FROM reading_path_dirty_work`); got != 0 {
		t.Fatalf("original worker left %d dirty work rows", got)
	}
}
