package compactcatalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type completeFixture struct {
	t                               *testing.T
	c                               *catalogtest.Catalog
	root, source, inc, library, job string
	handle                          int64
	serial                          int
	kind                            compactcatalog.Kind
}
type completePublication struct {
	item, asset              int64
	object, relative, format string
	kind                     compactcatalog.Kind
}

func newCompleteFixture(t *testing.T, c *catalogtest.Catalog, name string) *completeFixture {
	t.Helper()
	if c == nil {
		c = catalogtest.Open(t)
	}
	f := &completeFixture{t: t, c: c, root: t.TempDir(), library: name, job: "job-" + name, kind: compactcatalog.Ebook}
	f.handle = c.Library(name, name, "book", f.root)
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=?`, name).Scan(&f.source, &f.inc); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE library_sources SET identity_confirmed=1,root_identity='root' WHERE id=?`, f.source)
	c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','now')`, f.job, name)
	c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,1,?,'root',1)`, f.job, f.source, f.inc)
	return f
}
func (f *completeFixture) add(relative, format string, item int64) completePublication {
	f.t.Helper()
	f.serial++
	if item == 0 {
		item = f.c.Entity(compactcatalog.Entity{Library: f.handle, Kind: f.kind, Key: fmt.Sprint(f.serial), Title: relative}, nil).ID
	}
	asset, token := f.c.File(item, filepath.Join(f.root, filepath.FromSlash(relative)), 0)
	object := fmt.Sprintf("%s-%06d", f.library, f.serial)
	f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,directory_path,revision,evidence_json,size,modified_ns,seen_job) VALUES(?,?,?,?,?,?,'r1','{}',1,1,?)`, object, f.source, token, f.inc, filepath.FromSlash(relative), filepath.Dir(filepath.FromSlash(relative)), f.job)
	f.c.Exec(`INSERT INTO reading_publications VALUES(?,?,?)`, item, asset, format)
	return completePublication{item, asset, object, relative, format, f.kind}
}
func (f *completeFixture) settle() {
	f.c.Exec(`DELETE FROM catalog_dirty WHERE domain IN(20,28)`)
	f.c.Exec(`DELETE FROM catalog_derivation_failures WHERE domain IN(20,28)`)
	f.c.Exec(`DELETE FROM catalog_fanouts WHERE domain=28`)
}
func (f *completeFixture) seal() {
	f.c.Exec(`UPDATE inventory_runs SET phase='reconciling',authoritative=1 WHERE job_id=?`, f.job)
	f.c.Exec(`UPDATE inventory_runs SET phase='sidecars' WHERE job_id=?`, f.job)
}

func completeStep(t *testing.T, c *catalogtest.Catalog, limit int) int {
	t.Helper()
	n := 0
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		n, err = compactcatalog.StepReadingPaths(ctx, tx, limit)
		return err
	})
	if n > min(limit, 128) {
		t.Fatalf("raw work %d > budget %d", n, limit)
	}
	return n
}

func scalar(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func completeUntil(t *testing.T, c *catalogtest.Catalog, predicate func() bool) {
	t.Helper()
	for i := 0; i < 5000; i++ {
		if predicate() {
			return
		}
		completeStep(t, c, 7)
	}
	t.Fatal("bounded worker did not reach target")
}

func completeDrain(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	completeUntil(t, c, func() bool {
		return scalar(t, c.DB, `SELECT count(*) FROM reading_path_dirty_work`) == 0
	})
}

func completeValues(t *testing.T, c *catalogtest.Catalog, item int64) compactcatalog.ReadingValues {
	t.Helper()
	var v compactcatalog.ReadingValues
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		v, err = compactcatalog.ReadingAutomaticFieldsTx(ctx, tx, item)
		return err
	})
	return v
}

func onlyPath(v compactcatalog.ReadingValues) compactcatalog.ReadingValues {
	v.Title = ""
	v.PublicationType = nil
	return v
}

func assertCompleteValue(t *testing.T, c *catalogtest.Catalog, p completePublication, want compactcatalog.ReadingValues) {
	t.Helper()
	got := onlyPath(completeValues(t, c, p.item))
	if !reflect.DeepEqual(got, onlyPath(want)) {
		t.Fatalf("%s\ngot  %+v\nwant %+v", p.relative, got, want)
	}
}

func TestReadingCompleteMovedEntityKeepsBothLibraryWorkFences(t *testing.T) {
	a := newCompleteFixture(t, nil, "move-from")
	b := newCompleteFixture(t, a.c, "move-to")
	p := a.add("Author/Series/01 - Title.epub", "epub", 0)
	a.settle()
	a.seal()
	b.seal()
	completeDrain(t, a.c)
	a.c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'item',?)`, a.handle, fmt.Sprint(p.item))
	a.c.Exec(`UPDATE reading_path_dirty_work SET served=7 WHERE library_id=? AND kind='item' AND work_key=?`, a.handle, fmt.Sprint(p.item))
	a.c.Exec(`UPDATE catalog_entities SET library_id=? WHERE id=?`, b.handle, p.item)
	if scalar(t, a.c.DB, `SELECT served FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, a.handle, fmt.Sprint(p.item)) != 7 {
		t.Fatal("enqueuing the new-library key rewrote the old-library service ticket")
	}
	for _, library := range []int64{a.handle, b.handle} {
		if got := scalar(t, a.c.DB, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, library, fmt.Sprint(p.item)); got != 1 {
			t.Fatalf("library %d has %d moved-item fences, want one", library, got)
		}
	}
	// An additional notification coalesces only within its own library. Retiring
	// one library's pass must not consume the other pass with the same work key.
	a.c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'item',?) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, b.handle, fmt.Sprint(p.item))
	want := scalar(t, a.c.DB, `SELECT requested FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, b.handle, fmt.Sprint(p.item))
	a.c.Exec(`UPDATE reading_path_dirty_work SET served=1000000 WHERE library_id=? AND kind='item' AND work_key=?`, b.handle, fmt.Sprint(p.item))
	for i := 0; i < 5000 && scalar(t, a.c.DB, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, a.handle, fmt.Sprint(p.item)) != 0; i++ {
		completeStep(t, a.c, 1)
	}
	if scalar(t, a.c.DB, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, a.handle, fmt.Sprint(p.item)) != 0 {
		t.Fatal("old-library pass did not retire")
	}
	if want != 2 {
		t.Fatalf("new-library notifications did not coalesce independently: %d", want)
	}
	if got := scalar(t, a.c.DB, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=? AND requested=2 AND captured=0`, b.handle, fmt.Sprint(p.item)); got != 1 {
		var requested, captured, served int64
		err := a.c.DB.QueryRow(`SELECT requested,captured,served FROM reading_path_dirty_work WHERE library_id=? AND kind='item' AND work_key=?`, b.handle, fmt.Sprint(p.item)).Scan(&requested, &captured, &served)
		t.Fatalf("retiring old pass altered new pass: requested=%d captured=%d served=%d error=%v", requested, captured, served, err)
	}
	a.settle()
	completeDrain(t, a.c)
}

func TestReadingCompleteRawRejectedPrefixAndManyVersionsAreBounded(t *testing.T) {
	f := newCompleteFixture(t, nil, "raw")
	p := f.add("Shelf/Series 01.epub", "epub", 0)
	for i := 0; i < 135; i++ {
		v := f.add(fmt.Sprintf("Shelf/Books/Series %03d.pdf", i), "pdf", p.item)
		if i < 129 {
			f.c.Exec(`UPDATE inventory_objects SET unsupported_reason='reject' WHERE id=?`, v.object)
		}
	}
	f.settle()
	f.seal()
	completeDrain(t, f.c)
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_head WHERE author='Shelf'`) != 7 {
		t.Fatal("raw prefix skipped eligible versions or counted versions as titles")
	}
}

func TestReadingCompleteAcceptedFactsKeepOriginalSourcePredicates(t *testing.T) {
	f := newCompleteFixture(t, nil, "acceptance")
	p := f.add("Writer/Series/Series 01.epub", "epub", 0)
	f.add("Writer/Series/Series 02.epub", "epub", 0)
	f.settle()
	f.seal()
	completeDrain(t, f.c)
	before := completeValues(t, f.c, p.item)
	f.c.Exec(`UPDATE library_sources SET enabled=0,health='offline',generation=generation+1,name='Renamed source' WHERE id=?`, f.source)
	f.c.Exec(`UPDATE inventory_objects SET state='missing' WHERE id=?`, p.object)
	f.c.Exec(`UPDATE catalog_assets SET available=0 WHERE id=?`, p.asset)
	f.c.Exec(`UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, p.item)
	if after := completeValues(t, f.c, p.item); !reflect.DeepEqual(before, after) {
		t.Fatal("descriptive acceptance gained a health/enabled/generation/availability predicate")
	}
	f.c.Exec(`UPDATE library_sources SET incarnation='different-root' WHERE id=?`, f.source)
	assertCompleteValue(t, f.c, p, compactcatalog.ReadingValues{})
}

func TestReadingCompleteComicPureRuleCorpus(t *testing.T) {
	f := newCompleteFixture(t, nil, "comics")
	f.kind = compactcatalog.Comic
	f.c.Exec(`UPDATE libraries SET kind='comic' WHERE id=?`, f.library)
	var pubs []completePublication
	for _, r := range []struct{ path, format string }{{"Nemo/Nemo 01.cbz", "cbz"}, {"Nemo/Books/Nemo 02.pdf", "pdf"}, {"作者/シリーズ/シリーズ 01.cb7", "cb7"}, {"作者/シリーズ/シリーズ 02.cbr", "cbr"}, {"Books/Artist - Root #03.cbz", "cbz"}} {
		pubs = append(pubs, f.add(r.path, r.format, 0))
	}
	f.settle()
	f.seal()
	completeDrain(t, f.c)
	inputs := make([]compactcatalog.ReadingRuleInput, 0, len(pubs))
	for _, p := range pubs {
		inputs = append(inputs, compactcatalog.ReadingRuleInput{Item: p.item, SourceRoot: f.source, RelativePath: p.relative, Kind: p.kind})
	}
	want, err := compactcatalog.ReadingPathValues(inputs)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pubs {
		assertCompleteValue(t, f.c, p, want[p.item])
	}
}
