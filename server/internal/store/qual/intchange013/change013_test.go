// Package intchange013 is a disposable delta-only schema control harness.
// It is not imported by the product and does not change the embedded schema.
package intchange013

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

//go:embed testdata/candidate.sql
var candidate string

//go:embed testdata/before.sql
var before string

const first = `SELECT id FROM catalog_entities INDEXED BY catalog_entities_album_search WHERE album_id IS NOT NULL AND album_id=? ORDER BY id DESC LIMIT ?`
const next = `SELECT id FROM catalog_entities INDEXED BY catalog_entities_album_search WHERE album_id IS NOT NULL AND album_id=? AND id<? ORDER BY id DESC LIMIT ?`
const albumIndex = `CREATE INDEX catalog_entities_album_search ON catalog_entities(album_id,id) WHERE album_id IS NOT NULL;`
const birthCheck = `CHECK(typeof(search_incarnation)='blob' AND length(search_incarnation)=16)`

type fixture struct {
	db   *sql.DB
	c    *sql.Conn
	path string
}

func open(t *testing.T, path, ddl string) *fixture {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.Conn(t.Context())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	f := &fixture{db, c, path}
	t.Cleanup(func() { c.Close(); db.Close() })
	if _, err = sqliteconn.Assert(t.Context(), c, cfg); err != nil {
		t.Fatal(err)
	}
	t.Logf("Go=%s engine=%s FK=%d DQS=%d/%d ddlSHA=%x", runtime.Version(), settings.Engine, settings.ForeignKeys, settings.DQSDDL, settings.DQSDML, sha256.Sum256([]byte(ddl)))
	if settings.Engine != "3.53.4" || settings.ForeignKeys != 1 || settings.DQSDDL != 0 || settings.DQSDML != 0 {
		t.Fatal("engine/policy", settings)
	}
	if ddl != "" {
		f.exec(t, ddl)
	}
	return f
}
func native(t *testing.T, ddl string) *fixture {
	return open(t, filepath.Join(t.TempDir(), "013.sqlite"), ddl)
}
func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, e := f.c.ExecContext(t.Context(), q, args...); e != nil {
		t.Fatalf("%s: %v", q, e)
	}
}
func (f *fixture) reject(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, e := f.c.ExecContext(t.Context(), q, args...); e == nil {
		t.Fatalf("unexpected admission: %s", q)
	}
}
func (f *fixture) scalar(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if e := f.c.QueryRowContext(t.Context(), q, args...).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (f *fixture) birth(t *testing.T, id int) []byte {
	t.Helper()
	var b []byte
	if e := f.c.QueryRowContext(t.Context(), "SELECT search_incarnation FROM catalog_people WHERE id=?", id).Scan(&b); e != nil {
		t.Fatal(e)
	}
	return b
}
func personSQL(extra string) string {
	return `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation` + extra + `) VALUES(1,'person','person','Keanu','Keanu',zeroblob(16)` + map[bool]string{true: ",?", false: ""}[extra != ""] + `)`
}
func evidence(t *testing.T, name string, value any) {
	t.Helper()
	p := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if p == "" {
		return
	}
	if e := os.MkdirAll(p, 0700); e != nil {
		t.Fatal(e)
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(p, name), append(b, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}

func TestChange013PeopleBirth(t *testing.T) {
	f := native(t, candidate)
	f.exec(t, personSQL(""))
	birth := f.birth(t, 1)
	if len(birth) != 16 {
		t.Fatal("bad default birth")
	}
	f.exec(t, `UPDATE catalog_people SET name='Keanu Reeves',revision=revision+1 WHERE id=1 AND revision=1 AND name<>'Keanu Reeves'`)
	if !bytes.Equal(birth, f.birth(t, 1)) || f.scalar(t, `SELECT revision FROM catalog_people WHERE id=1`) != 2 {
		t.Fatal("rename changed birth or failed real revision")
	}
	f.exec(t, `UPDATE catalog_people SET name='Keanu Reeves',revision=revision+1 WHERE id=1 AND revision=2 AND name<>'Keanu Reeves'`)
	if f.scalar(t, `SELECT revision FROM catalog_people WHERE id=1`) != 2 {
		t.Fatal("noop bumped")
	}
	f.exec(t, `UPDATE catalog_people SET artwork_incarnation=randomblob(16),artwork_revision=artwork_revision+1 WHERE id=1`)
	f.exec(t, `INSERT INTO catalog_people_aliases(alias,person_id) VALUES('Reeves',1)`)
	f.exec(t, `UPDATE catalog_people SET biography='updated' WHERE id=1`)
	if !bytes.Equal(birth, f.birth(t, 1)) {
		t.Fatal("ordinary/art/alias changed source birth")
	}
	// Existing revision semantics are a writer CAS, not a new trigger or CHECK.
	f.exec(t, `UPDATE catalog_people SET revision=9223372036854775807 WHERE id=1`)
	res, e := f.c.ExecContext(t.Context(), `UPDATE catalog_people SET revision=revision+1 WHERE id=1 AND revision=9223372036854775807 AND revision<9223372036854775807`)
	if e != nil {
		t.Fatal(e)
	}
	n, _ := res.RowsAffected()
	if n != 0 {
		t.Fatal("overflow guard did not refuse")
	}
	for _, v := range []any{nil, []byte{1}, make([]byte, 17), "1234567890123456", int64(16)} {
		f.reject(t, `UPDATE catalog_people SET search_incarnation=? WHERE id=1`, v)
	}
	path := f.path
	f.c.Close()
	f.db.Close()
	f = open(t, path, "")
	if !bytes.Equal(birth, f.birth(t, 1)) {
		t.Fatal("restart lost birth")
	}
	f.exec(t, `DELETE FROM catalog_people_aliases WHERE person_id=1`)
	f.exec(t, `DELETE FROM catalog_people WHERE id=1`)
	f.exec(t, personSQL(""))
	if bytes.Equal(birth, f.birth(t, 1)) {
		t.Fatal("equal-ID recreation reused birth")
	}
	f.exec(t, `INSERT INTO catalog_people(token,identity_key,name,sort_name,artwork_incarnation) VALUES('other','other','Other','Other',zeroblob(16))`)
	if bytes.Equal(f.birth(t, 1), f.birth(t, 2)) {
		t.Fatal("separate births equal")
	}
	if f.scalar(t, `SELECT count(*) FROM pragma_foreign_key_check`) != 0 {
		t.Fatal("FK")
	}
	evidence(t, "013-people.json", map[string]any{"scope": "delta model, not actual People publisher/reader authority", "candidate": fmt.Sprintf("%x", sha256.Sum256([]byte(candidate))), "default_blob16": true, "update_preserved": true, "restart_preserved": true, "equal_id_new_birth": true, "invalid_values_rejected": 5, "revision_overflow_cas_refused": true})
}

func seedAlbum(t *testing.T, f *fixture) {
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1)`)
	f.exec(t, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(1,'music','/music')`)
	for _, r := range []struct{ id, kind, parent, album, disc, track int }{{1, 5, 0, 0, 0, 0}, {2, 6, 1, 0, 0, 0}, {3, 6, 1, 0, 0, 0}, {10, 7, 2, 2, 9, 1}, {20, 7, 2, 2, 1, 9}, {30, 7, 2, 2, 5, 3}, {40, 7, 3, 3, 1, 1}} {
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,randomblob(16),1,?)`, r.id, r.kind)
		var parent, artist, album, disc, track any
		if r.parent != 0 {
			parent = r.parent
		}
		if r.kind == 6 {
			artist = 1
		}
		if r.album != 0 {
			album = r.album
			disc = r.disc
			track = r.track
		}
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,parent_id,artist_id,album_id,disc_number,track_number,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,?,?,?,?,?,'title','title','t','title','t',1,zeroblob(16),zeroblob(16) FROM catalog_subjects WHERE id=?`, parent, artist, album, disc, track, r.id)
	}
}
func queryIDs(t *testing.T, f *fixture, q string, args ...any) []int64 {
	t.Helper()
	r, e := f.c.QueryContext(t.Context(), q, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	ids := []int64{}
	for r.Next() {
		var id int64
		if e = r.Scan(&id); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	if e = r.Err(); e != nil {
		t.Fatal(e)
	}
	return ids
}
func plan(t *testing.T, f *fixture, q string, args ...any) ([]string, bool) {
	t.Helper()
	r, e := f.c.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+q, args...)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	ds := []string{}
	good := false
	for r.Next() {
		var id, p, unused int
		var d string
		if e = r.Scan(&id, &p, &unused, &d); e != nil {
			t.Fatal(e)
		}
		ds = append(ds, d)
		if strings.Contains(d, "SCAN ") || strings.Contains(d, "TEMP B-TREE") {
			return ds, false
		}
		if strings.Contains(d, "SEARCH ") && strings.Contains(d, "catalog_entities_album_search") && strings.Contains(d, "album_id=?") {
			good = true
		}
	}
	if e = r.Err(); e != nil {
		t.Fatal(e)
	}
	return ds, good
}
func TestChange013AlbumKeysets(t *testing.T) {
	b := native(t, before)
	if _, e := b.c.PrepareContext(t.Context(), first); e == nil {
		t.Fatal("baseline unexpectedly has requested index")
	}
	f := native(t, candidate)
	seedAlbum(t, f)
	proof := map[string]any{}
	for _, stats := range []string{"tiny", "analyzed"} {
		if stats == "analyzed" {
			f.exec(t, "ANALYZE")
		}
		for _, v := range []struct {
			name, q string
			args    []any
			want    string
		}{{"first", first, []any{2, 2}, "[30 20]"}, {"next", next, []any{2, 20, 64}, "[10]"}, {"empty", first, []any{99, 64}, "[]"}, {"limit1", first, []any{2, 1}, "[30]"}, {"limit64", first, []any{2, 64}, "[30 20 10]"}} {
			ds, ok := plan(t, f, v.q, v.args...)
			if !ok {
				t.Fatal("not bounded SEARCH/noTEMP", ds)
			}
			if v.q == next && !strings.Contains(strings.Join(ds, " "), "id<?") {
				t.Fatal("no ID range", ds)
			}
			got := fmt.Sprint(queryIDs(t, f, v.q, v.args...))
			if got != v.want {
				t.Fatal(v.name, got, v.want)
			}
			proof[stats+"/"+v.name] = ds
		}
	}
	if f.scalar(t, `SELECT count(*) FROM sqlite_schema WHERE type='index' AND name='catalog_entities_album'`) != 1 {
		t.Fatal("disc/track index removed")
	}
	if f.scalar(t, `SELECT count(*) FROM pragma_foreign_key_check`) != 0 {
		t.Fatal("FK")
	}
	var integrity string
	if e := f.c.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
		t.Fatal(integrity, e)
	}
	evidence(t, "013-album.json", proof)
}
func TestChange013Falsifiers(t *testing.T) {
	if strings.Count(candidate, birthCheck) != 1 || strings.Count(candidate, albumIndex) != 1 {
		t.Fatal("mutant source mismatch")
	}
	// Valid TEXT16 must be refused; length alone silently admits it.
	loose := strings.Replace(candidate, birthCheck, `CHECK(length(search_incarnation)=16)`, 1)
	f := native(t, loose)
	f.exec(t, personSQL(",search_incarnation"), "1234567890123456")
	if f.scalar(t, `SELECT typeof(search_incarnation)='text' FROM catalog_people WHERE id=1`) != 1 {
		t.Fatal("birth-type mutant not distinguished")
	}
	// Same forced index name and album equality still SEARCH, but wrong key
	// order requires a temp sorter. Reject that actual plan shape.
	wrong := strings.Replace(candidate, albumIndex, `CREATE INDEX catalog_entities_album_search ON catalog_entities(album_id,COALESCE(disc_number,1),COALESCE(track_number,0),id) WHERE album_id IS NOT NULL;`, 1)
	g := native(t, wrong)
	seedAlbum(t, g)
	ds, good := plan(t, g, first, 2, 64)
	if good || !strings.Contains(strings.Join(ds, " "), "TEMP B-TREE") {
		t.Fatal("wrong-order mutant not distinguished", ds)
	}
	evidence(t, "013-mutants.json", map[string]any{"loose_birth_type_sha": fmt.Sprintf("%x", sha256.Sum256([]byte(loose))), "wrong_album_order_sha": fmt.Sprintf("%x", sha256.Sum256([]byte(wrong))), "both_loaded": true, "loose_type_admits_TEXT16": true, "wrong_order_plan": ds})
}
