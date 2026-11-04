// Temporary phase-0 research harness. Never merge into product.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	_ "modernc.org/sqlite"
)

type Stat struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

func stats(a []float64) Stat {
	if len(a) == 0 {
		return Stat{}
	}
	b := append([]float64{}, a...)
	sort.Float64s(b)
	p := func(q float64) float64 { return b[int(math.Ceil(q*float64(len(b))))-1] }
	return Stat{len(b), p(.5), p(.95), p(.99), b[len(b)-1]}
}

var outMu sync.Mutex

func emit(kind string, v any) {
	outMu.Lock()
	defer outMu.Unlock()
	b, e := json.Marshal(map[string]any{"event": kind, "candidate_sha256": candidateSHA(), "at": time.Now().UTC().Format(time.RFC3339Nano), "data": v})
	if e != nil {
		panic(e)
	}
	fmt.Println(string(b))
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
func exec(db interface {
	Exec(string, ...any) (sql.Result, error)
}, q string, a ...any) {
	_, e := db.Exec(q, a...)
	must(e)
}

var nativeMax = struct {
	sync.Mutex
	values map[string][]float64
}{values: map[string][]float64{}}

func native(db interface {
	Exec(string, ...any) (sql.Result, error)
}, tag, q string, a ...any) {
	if strings.Contains(tag, "commit") || strings.Contains(tag, ".step.") {
		cpuNative(db, tag, q, a...)
		return
	}
	t := time.Now()
	profiledExec(db, tag, q, a...)
	nativeMax.Lock()
	elapsed := float64(time.Since(t).Microseconds()) / 1000
	nativeMax.values[tag] = append(nativeMax.values[tag], elapsed)
	if elapsed > 25 {
		emit("slow_native", map[string]any{"tag": tag, "ms": elapsed, "start_unix_ns": t.UnixNano(), "end_unix_ns": time.Now().UnixNano()})
	}
	nativeMax.Unlock()
}
func measured(tag string, f func()) {
	t := time.Now()
	f()
	nativeMax.Lock()
	elapsed := float64(time.Since(t).Microseconds()) / 1000
	nativeMax.values[tag] = append(nativeMax.values[tag], elapsed)
	if elapsed > 25 {
		emit("slow_writer", map[string]any{"tag": tag, "ms": elapsed, "start_unix_ns": t.UnixNano(), "end_unix_ns": time.Now().UnixNano()})
	}
	nativeMax.Unlock()
}
func encode(s string) string {
	var b strings.Builder
	for _, r := range s {
		fmt.Fprintf(&b, "%06x", r)
	}
	return b.String()
}
func prefix(s string) string {
	out := []string{}
	for _, w := range strings.Fields(facettext.SearchDocument(s)) {
		r := []rune(w)
		cjk := false
		num := true
		for _, c := range r {
			cjk = cjk || facettext.CJK(c)
			num = num && unicode.IsNumber(c)
		}
		if cjk {
			if oldCJKRecipe {
				continue
			}
			all := true
			for _, x := range r {
				all = all && facettext.CJK(x)
			}
			if !all || len(r) > 2 {
				continue
			}
		}
		cap := 12
		if num {
			cap = 6
		}
		if len(r) > cap {
			r = r[:cap]
		}
		out = append(out, encode(string(r)))
	}
	return strings.Join(out, " ")
}
func initial(s string) string {
	w := strings.Fields(facettext.SearchFold(s))
	if len(w) == 0 {
		return ""
	}
	if oldCJKRecipe {
		return prefix(w[0])
	}
	r := []rune(w[0])
	cap := 12
	num := true
	for _, x := range r {
		num = num && unicode.IsNumber(x)
	}
	if num {
		cap = 6
	}
	if len(r) > cap {
		r = r[:cap]
	}
	return encode(string(r))
}

var autoMerge, crisisMerge, buildBatch, mergePages = 2, 16, 8, 1
var durability = "FULL"
var indexes = []string{"search_own_prefix", "search_fragment_prefix", "search_fragment_lexical", "search_fragment_grams"}

func open(path string) *sql.DB {
	db, e := sql.Open("sqlite", path)
	must(e)
	db.SetMaxOpenConns(1)
	exec(db, "PRAGMA busy_timeout=30000")
	if publishedSchema {
		exec(db, "PRAGMA foreign_keys=ON")
	}
	exec(db, fmt.Sprintf("PRAGMA cache_size=%d", cacheKiB))
	if !cacheSpill {
		exec(db, "PRAGMA cache_spill=OFF")
	}
	exec(db, "PRAGMA temp_store=FILE")
	return db
}
func create(db *sql.DB, variant string) {
	if variant != "A" {
		panic("candidate schema stores A indexes; B is dispatch-only in experiments")
	}
	exec(db, "PRAGMA foreign_keys=OFF")
	exec(db, "PRAGMA journal_mode=WAL")
	exec(db, "PRAGMA synchronous="+durability)
	exec(db, "PRAGMA wal_autocheckpoint=0")
	exec(db, candidateSQL)
	exec(db, "CREATE TABLE q_items(id INTEGER PRIMARY KEY,rating INTEGER NOT NULL) STRICT")
	exec(db, `CREATE VIEW own AS SELECT e.id,e.library_id library,e.kind,q.rating,e.title,e.title_fold,e.title_prefix_tokens,e.initial_prefix_tokens FROM catalog_entities e CROSS JOIN q_items q ON q.id=e.id`)
	exec(db, `CREATE VIEW fragments AS SELECT id,CASE WHEN owner_person IS NOT NULL THEN -owner_person WHEN field=3 THEN -(owner_entity-300000000) ELSE owner_entity END owner,field,prefix_tokens,lexical_tokens,normalized FROM search_fragments`)
	exec(db, `CREATE TABLE rel(parent INTEGER NOT NULL,child INTEGER NOT NULL,PRIMARY KEY(parent,child)) WITHOUT ROWID`)
	exec(db, `CREATE INDEX rel_child ON rel(child,parent)`)
	exec(db, `CREATE TABLE edges(fragment INTEGER NOT NULL,entity INTEGER NOT NULL,PRIMARY KEY(fragment,entity)) WITHOUT ROWID`)
	exec(db, `CREATE INDEX edge_entity ON edges(entity,fragment)`)
	exec(db, "INSERT INTO search_revision VALUES(1,1)")
	for _, x := range indexes {
		for _, kv := range []struct {
			k string
			v int
		}{{"automerge", autoMerge}, {"crisismerge", crisisMerge}, {"usermerge", 2}} {
			exec(db, "INSERT INTO "+x+"("+x+",rank) VALUES(?,?)", kv.k, kv.v)
		}
	}
}
func fragment(db *sql.DB, id, owner, field int, text string) {
	p := ""
	if field != 1 {
		p = prefix(text)
	}
	lex := facettext.SearchDocument(text)
	norm := facettext.SearchFold(text)
	fragmentValues(db, id, owner, field, p, lex, norm)
}
func fragmentValues(db *sql.DB, id, owner, field int, p, lex, norm string) {
	var entity, person any
	entity = owner
	if field == 2 {
		entity = nil
		person = -owner
	}
	if field == 3 {
		entity = 300000000 - owner
	}
	exec(db, "INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized) VALUES(?,?,?,?,0,'q-source-v1',?,?,?)", id, entity, person, field, p, lex, norm)
	for _, x := range []struct{ idx, col, v string }{{"search_fragment_prefix", "prefix_tokens", p}, {"search_fragment_lexical", "lexical_tokens", lex}, {"search_fragment_grams", "normalized", norm}} {
		native(db, "fts.insert."+x.idx, "INSERT INTO "+x.idx+"(rowid,"+x.col+") VALUES(?,?)", id, x.v)
	}
}

var words = []string{"Harbor", "Night", "Silver", "River", "Moon", "Adventure", "Blue", "Garden", "Silent", "Echo", "Northern", "Empire", "Crimson", "Valley", "Winter", "Dream", "Star", "Ocean", "Hidden", "Road", "Golden", "Light", "Shadow", "Wild", "Castle", "Sun", "Journey", "Mountain", "Distant", "Love", "Clock", "Morning"}

func title(i int) string {
	if i < 9 {
		return []string{"Harbor Lights", "Northern Road", "Blue River", "1984", "東京夜話", "1234567", "Nebuchadnezzar Chronicle", "Spider-Man", "Amélie"}[i]
	}
	if cjkShare >= 0 && scriptShare(i) {
		return fmt.Sprintf("%s %d", cjkTitleNames[i%len(cjkTitleNames)], i)
	}
	if cjkShare < 0 && i%47 == 0 {
		return fmt.Sprintf("東京夜話 %d", i)
	}
	if i%37 == 0 {
		return fmt.Sprintf("1984 Chronicle %d", i)
	}
	return fmt.Sprintf("%s %s %s %d", words[i%len(words)], words[(i/32)%len(words)], words[(i/1024)%len(words)], i)
}

// Ten percent books, thirty percent music, forty-five percent TV, fifteen percent movies.
// Within each 100 IDs, show/season and artist/album/song links are direct, never flattened.
func shape(i int) (int, int) {
	r := i % 100
	b := i - r
	switch {
	case r < 15:
		return 1, -1
	case r == 15:
		return 2, -1
	case r >= 16 && r <= 18:
		return 3, -1
	case r >= 19 && r < 60:
		return 4, b + 15
	case r == 60:
		return 5, -1
	case r == 61 || r == 62:
		return 6, b + 60
	case r >= 63 && r < 90:
		return 7, b + 61 + (r-63)%2
	default:
		return 8, -1
	}
}
func insertItem(db *sql.DB, i, shared int) {
	t := title(i)
	if strings.ContainsFunc(t, facettext.CJK) {
		cjkTitlesBuilt++
	}
	p, init := prefix(t), initial(t)
	kind, parent := shape(i)
	exec(db, "INSERT INTO q_items VALUES(?,?)", i, i%100)
	insertEntity(db, i, kind, parent, t, p, init)
	native(db, "fts.insert.search_own_prefix", "INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)", i, p, init)
	fragment(db, 100000000+i, i, 1, t)
	if parent >= 0 {
		exec(db, "INSERT INTO rel VALUES(?,?)", parent, i)
	}
	for j := 0; j < 2; j++ {
		exec(db, "INSERT OR IGNORE INTO edges VALUES(?,?)", 1+(i*17+j*43)%shared, i)
	}
	exec(db, "INSERT OR IGNORE INTO edges VALUES(?,?)", shared+1+(i/100)%max(1, shared/10), i)
	if i == 0 {
		exec(db, "INSERT OR IGNORE INTO edges VALUES(?,?)", 8, i)
	}
	if i == 1 {
		exec(db, "INSERT OR IGNORE INTO edges VALUES(?,?)", 2, i)
	}
	if i == 2 {
		exec(db, "INSERT OR IGNORE INTO edges VALUES(?,?)", shared+4, i)
	}
}

var databasePath string
var profileName string

func checkpoint(db *sql.DB) {
	if passiveEnabled {
		requestCheckpoint()
		return
	}
	if f, e := os.Stat(databasePath + "-wal"); e == nil {
		emit("wal_checkpoint_before", map[string]any{"physical_bytes": f.Size()})
	}
	native(db, "checkpoint", "PRAGMA wal_checkpoint(TRUNCATE)")
}
func build(db *sql.DB, n int) {
	shared := max(128, n/50)
	for i := 1; i <= shared+max(1, shared/10); {
		admitPublication()
		serviceForeground(db)
		measured("build.shared.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			for end := min(shared+max(1, shared/10)+1, i+buildBatch); i < end; i++ {
				if i <= shared {
					name := personName(i)
					fragment(db, i, -i, 2, name)
				} else {
					j := i - shared
					fragment(db, i, -i, 3, collectionName(j))
				}
			}
			native(db, "build.shared.commit", "COMMIT")
		})
		maintain(db, "build.shared.merge")
	}
	checkpoint(db)
	start := time.Now()
	for i := 0; i < n; {
		admitPublication()
		serviceForeground(db)
		measured("build.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			for end := min(n, i+buildBatch); i < end; i++ {
				insertItem(db, i, shared)
			}
			native(db, "build.commit", "COMMIT")
		})
		maintain(db, "build.merge")
		if i%8192 == 0 {
			checkpoint(db)
		}
		if i%100000 == 0 {
			emit("build_progress", map[string]any{"items": i, "elapsed_s": time.Since(start).Seconds()})
		}
	}
	checkpoint(db)
	emit("build_done", map[string]any{"items": n, "cjk_titles": cjkTitlesBuilt, "actual_cjk_title_percent": 100 * float64(cjkTitlesBuilt) / float64(n), "elapsed_s": time.Since(start).Seconds()})
}
func allocation(db *sql.DB, path string) map[string]any {
	rows, e := db.Query("SELECT name,sum(pgsize),sum(payload) FROM dbstat GROUP BY name ORDER BY name")
	must(e)
	defer rows.Close()
	m := map[string]any{}
	var fts, other int64
	for rows.Next() {
		var name string
		var bytes, payload int64
		must(rows.Scan(&name, &bytes, &payload))
		m[name] = map[string]int64{"allocated": bytes, "payload": payload}
		isfts := false
		for _, x := range indexes {
			if strings.HasPrefix(name, x+"_") {
				isfts = true
			}
		}
		if isfts {
			fts += bytes
		} else {
			other += bytes
		}
	}
	files := map[string]int64{}
	for _, s := range []string{"", "-wal", "-shm"} {
		if f, e := os.Stat(path + s); e == nil {
			files[s] = f.Size()
		}
	}
	return map[string]any{"objects": m, "fts_bytes": fts, "source_edges_indexes_bytes": other, "files": files}
}
func structure(db *sql.DB, idx string) map[string]any {
	var b []byte
	must(db.QueryRow("SELECT block FROM " + idx + "_data WHERE id=10").Scan(&b))
	pos := 4
	get := func() uint64 {
		var v uint64
		for j := 0; j < 9; j++ {
			if pos >= len(b) {
				panic("truncated FTS structure")
			}
			c := b[pos]
			pos++
			if j == 8 {
				return v<<8 | uint64(c)
			}
			v = v<<7 | uint64(c&127)
			if c < 128 {
				return v
			}
		}
		panic("invalid varint")
	}
	levels, total, writes := int(get()), int(get()), get()
	if levels > 64 {
		panic("unrecognized FTS structure version")
	}
	a := []map[string]any{}
	maxseg := 0
	sum := 0
	for level := 0; level < levels; level++ {
		merging, n := int(get()), int(get())
		sum += n
		maxseg = max(maxseg, n)
		pages := uint64(0)
		for j := 0; j < n; j++ {
			_ = get()
			first, last := get(), get()
			if last >= first {
				pages += last - first + 1
			}
		}
		a = append(a, map[string]any{"level": level, "segments": n, "merging": merging, "leaf_pages": pages})
	}
	if sum != total || pos != len(b) {
		panic(fmt.Sprintf("FTS structure decode mismatch %s %d %d pos %d len %d", idx, sum, total, pos, len(b)))
	}
	return map[string]any{"levels": a, "segments": total, "max_same_level": maxseg, "write_counter": writes}
}
func debt(db *sql.DB) map[string]any {
	m := map[string]any{}
	for _, x := range indexes {
		m[x] = structure(db, x)
	}
	return m
}

var maxDebt = map[string]int{}

func maintain(db *sql.DB, tag string) {
	for _, idx := range indexes {
		for step := 0; step < 32768; step++ {
			serviceForeground(db)
			admitPublication()
			d := structure(db, idx)
			if step > 0 && step%512 == 0 {
				emit("debt_repayment_progress", map[string]any{"index": idx, "steps": step, "debt": d})
			}
			n := d["max_same_level"].(int)
			maxDebt[idx] = max(maxDebt[idx], n)
			if n < 4 {
				break
			}
			if step == 32767 {
				emit("debt_admission_blocked", map[string]any{"index": idx, "debt": d})
				panic("cannot repay segment debt")
			}
			measured(tag+".tx."+idx, func() {
				exec(db, "BEGIN IMMEDIATE")
				native(db, tag+".step."+idx, "INSERT INTO "+idx+"("+idx+",rank) VALUES('merge',?)", mergePages)
				native(db, tag+".commit."+idx, "COMMIT")
			})
		}
	}
}
func merge(db *sql.DB, tag string) {
	for _, x := range indexes {
		serviceForeground(db)
		admitPublication()
		measured(tag+".tx."+x, func() {
			exec(db, "BEGIN IMMEDIATE")
			native(db, tag+".step."+x, "INSERT INTO "+x+"("+x+",rank) VALUES('merge',?)", mergePages)
			native(db, tag+".commit."+x, "COMMIT")
		})
	}
}
func renameFragment(db *sql.DB, id int, s string) {
	renameFragmentValues(db, id, prefix(s), facettext.SearchDocument(s), facettext.SearchFold(s))
}
func renameFragmentValues(db *sql.DB, id int, pNew, lNew, nNew string) {
	var owner, field int
	var p, l, n string
	must(db.QueryRow("SELECT owner,field,prefix_tokens,lexical_tokens,normalized FROM fragments WHERE id=?", id).Scan(&owner, &field, &p, &l, &n))
	for _, x := range []struct{ idx, col, v string }{{"search_fragment_prefix", "prefix_tokens", p}, {"search_fragment_lexical", "lexical_tokens", l}, {"search_fragment_grams", "normalized", n}} {
		native(db, "fts.delete."+x.idx, "INSERT INTO "+x.idx+"("+x.idx+",rowid,"+x.col+") VALUES('delete',?,?)", id, x.v)
	}
	exec(db, "DELETE FROM search_fragments WHERE id=?", id)
	if field == 1 {
		pNew = ""
	}
	fragmentValues(db, id, owner, field, pNew, lNew, nNew)
}
func renameTitle(db *sql.DB, id int, s string) {
	var p, in string
	must(db.QueryRow("SELECT title_prefix_tokens,initial_prefix_tokens FROM own WHERE id=?", id).Scan(&p, &in))
	native(db, "fts.delete.search_own_prefix", "INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',?,?,?)", id, p, in)
	exec(db, "UPDATE catalog_entities SET title=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=?,title_fold=?,title_prefix_tokens=?,initial_prefix_tokens=? WHERE id=?", s, facettext.SearchFold(s), string([]rune(s)[0]), facettext.SearchFold(s), string([]rune(s)[0]), facettext.SearchFold(s), prefix(s), initial(s), id)
	native(db, "fts.insert.search_own_prefix", "INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)", id, prefix(s), initial(s))
	renameFragment(db, 100000000+id, s)
}

// Per-token exact sets are materialized before choosing the rarest lead. Synthetic recipes
// all fit the production caps. This is not a production full-recipe implementation.
func termSQL(term, variant string, fallback bool) (string, []any) {
	q := `SELECT o.rowid entity,CASE WHEN o.rowid IN(SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ?) THEN 2 ELSE 1 END tier,0.0 score FROM search_own_prefix o WHERE search_own_prefix MATCH ?`
	args := []any{"initial_prefix_tokens:" + encode(term) + "*", "title_prefix_tokens:" + encode(term) + "*"}
	if fallback {
		q = `SELECT f.owner entity,1 tier,s.score*4 score FROM fallback_scores s JOIN fragments f ON f.id=s.id WHERE f.field=1 AND instr(f.normalized,?)>0`
		args = []any{term}
	}
	if fallback || variant == "A" || len([]rune(term)) >= 3 {
		fragq := `SELECT e.entity,0 tier,0.0 score FROM search_fragment_prefix p JOIN edges e ON e.fragment=p.rowid WHERE search_fragment_prefix MATCH ?`
		a := []any{encode(term) + "*"}
		if fallback {
			fragq = `SELECT e.entity,0 tier,s.score FROM fallback_scores s JOIN fragments f ON f.id=s.id JOIN edges e ON e.fragment=f.id WHERE instr(f.normalized,?)>0`
			a = []any{term}
		}
		q += " UNION ALL " + fragq
		args = append(args, a...)
	}
	return `WITH seeds AS MATERIALIZED (` + q + `), expanded AS (SELECT entity,tier,score FROM seeds UNION ALL SELECT r.child,0,CASE WHEN s.tier=1 THEN s.score/4 ELSE s.score END FROM seeds s JOIN rel r ON r.parent=s.entity WHERE s.tier>0 UNION ALL SELECT r2.child,0,CASE WHEN s.tier=1 THEN s.score/4 ELSE s.score END FROM seeds s JOIN rel r1 ON r1.parent=s.entity JOIN rel r2 ON r2.parent=r1.child WHERE s.tier>0) SELECT entity,max(tier),max(score) FROM expanded CROSS JOIN own ON own.id=expanded.entity WHERE own.library BETWEEN 0 AND 7 GROUP BY entity`, args
}
func fallbackQuery(q string) bool {
	for _, t := range facettext.SearchTerms(q) {
		r := []rune(t)
		numeric := true
		for _, x := range r {

			numeric = numeric && unicode.IsNumber(x)
		}
		if len(r) > 12 || (numeric && len(r) > 6) {
			return true
		}
	}
	return false
}

type Query struct {
	Name, Text string
	Restricted bool
}

var queries = []Query{{"prefix1", "a", false}, {"prefix2", "jo", false}, {"prefix3", "ada", false}, {"full_word", "harbor", false}, {"multi_word", "harbor ada", false}, {"numeric6", "1984", false}, {"numeric_long", "1234567", false}, {"cjk1", "東", false}, {"cjk2", "東京", false}, {"long_word", "nebuchadnezzar", false}, {"restricted", "a", true}}

func search(db *sql.DB, v string, q Query) (int, []int) { return searchExcluding(db, v, q, false) }
func searchExcluding(db *sql.DB, v string, query Query, exclude bool) (int, []int) {
	terms := facettext.SearchTerms(query.Text)
	lead, count := 0, int(^uint(0)>>1)
	for i, t := range terms {
		tbl := fmt.Sprintf("vt%d", i)
		exec(db, "CREATE TEMP TABLE IF NOT EXISTS "+tbl+"(entity INTEGER PRIMARY KEY,tier INTEGER NOT NULL,score REAL NOT NULL)")
		exec(db, "DELETE FROM "+tbl)
		if fallbackQuery(query.Text) {
			exec(db, "CREATE TEMP TABLE IF NOT EXISTS fallback_scores(id INTEGER PRIMARY KEY, score REAL NOT NULL)")
			exec(db, "DELETE FROM fallback_scores")
			exec(db, "INSERT INTO fallback_scores SELECT rowid,-bm25(search_fragment_lexical) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ?", `"`+t+`"*`)
		}
		s, args := termSQL(t, v, fallbackQuery(query.Text))
		exec(db, "INSERT INTO "+tbl+" "+s, args...)
		var n int
		must(db.QueryRow("SELECT count(*) FROM " + tbl).Scan(&n))
		if n < count {
			lead, count = i, n
		}
	}
	sqlq := fmt.Sprintf("SELECT o.id FROM vt%d v CROSS JOIN own o ON o.id=v.entity ", lead)
	for i := range terms {
		if i != lead {
			sqlq += fmt.Sprintf("CROSS JOIN vt%d t%d ON t%d.entity=v.entity ", i, i, i)
		}
	}
	sqlq += "WHERE 1=1 "
	if exclude {
		sqlq += "AND NOT EXISTS(SELECT 1 FROM ctitles c WHERE c.entity=o.id) "
	}
	if query.Restricted {
		sqlq += "AND o.rating=0 "
	}
	if fallbackQuery(query.Text) {
		scores := "v.score"
		for i := range terms {
			if i != lead {
				scores += fmt.Sprintf("+t%d.score", i)
			}
		}
		sqlq += "ORDER BY round(" + scores + "+CASE WHEN o.title_fold=? THEN 1000 ELSE 0 END+CASE WHEN o.title_fold LIKE ? THEN 100 ELSE 0 END,6) DESC,o.id LIMIT 41"
	} else {
		sqlq += "ORDER BY CASE WHEN o.title_fold=? THEN 3 ELSE v.tier END DESC,o.id LIMIT 41"
	}
	qargs := []any{facettext.SearchFold(query.Text)}
	if fallbackQuery(query.Text) {
		qargs = append(qargs, facettext.SearchFold(query.Text)+"%")
	}
	rows, e := db.Query(sqlq, qargs...)
	must(e)
	defer rows.Close()
	ids := []int{}
	for rows.Next() {
		var id int
		must(rows.Scan(&id))
		ids = append(ids, id)
	}
	return count, ids
}
func latency(db *sql.DB, v string, samples int, phase string) {
	for _, q := range queries {
		times := []float64{}
		var n int
		var ids []int
		for i := 0; i < samples+1; i++ {
			t := time.Now()
			n, ids = search(db, v, q)
			if i > 0 {
				times = append(times, float64(time.Since(t).Microseconds())/1000)
			}
		}
		emit("query", map[string]any{"phase": phase, "variant": v, "name": q.Name, "query": q.Text, "restricted": q.Restricted, "latency": stats(times), "lead_population": n, "page": ids})
	}
}
func quality(db *sql.DB, v string) {
	for _, s := range []string{"al", "jo", "harbor ad", "ada", "night", "li", "東京"} {
		n, ids := search(db, v, Query{Text: s})
		emit("quality", map[string]any{"variant": v, "query": s, "population": n, "page": ids})
	}
	var n int
	must(db.QueryRow("SELECT count(*) FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ?", encode("jo")+"*").Scan(&n))
	emit("native_short_prefix_still_matches", n)
}
func controls(db *sql.DB, v string) {
	exec(db, "BEGIN IMMEDIATE")
	renameTitle(db, 0, "RollbackSentinel")
	exec(db, "ROLLBACK")
	var n int
	must(db.QueryRow("SELECT count(*) FROM search_own_prefix WHERE search_own_prefix MATCH ?", encode("rollbacksent")+"*").Scan(&n))
	if n != 0 {
		panic("rollback leaked MATCH")
	}
	exec(db, "BEGIN IMMEDIATE")
	renameFragment(db, 1, "UniqueRenameSentinel")
	exec(db, "COMMIT")
	must(db.QueryRow("SELECT count(*) FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ?", encode("uniquerenam")+"*").Scan(&n))
	if n != 1 {
		panic("rename MATCH failed")
	}
	for _, idx := range indexes {
		exec(db, "INSERT INTO "+idx+"("+idx+",rank) VALUES('integrity-check',1)")
	}
	emit("controls", map[string]any{"rollback_match": true, "renamed_match": true, "external_content_integrity": true})
}
func main() {
	mode := flag.String("mode", "baseline", "baseline, hybrid or tune")
	config := flag.String("config", "baseline", "native policy experiment")
	n := flag.Int("n", 1000, "base items")
	v := flag.String("variant", "A", "A or B")
	profile := flag.String("profile", "smoke", "hardware profile")
	dir := flag.String("dir", "", "scratch output")
	ramHeads := flag.Int("frontier-ram-heads", 8192, "source heads retained before ephemeral spill")
	genRun := flag.Bool("generation", true, "run full generation backfill for designated cell")
	samples := flag.Int("samples", 16, "query samples excluding warmup")
	rounds := flag.Int("rounds", 2048, "churn transactions")
	cpuPath := flag.String("cpu-profile", "", "labeled diagnostic CPU profile")
	share := flag.Int("cjk-share", -1, "percent CJK titles/people/collections, -1 legacy witness fixture")
	oldRecipe := flag.Bool("old-cjk-recipe", false, "historical skip-CJK prefix control")
	pinCap := flag.Duration("snapshot-cap", 0, "controlled preparation snapshot lifetime")
	bounded := flag.Bool("bounded-readers", false, "20Hz bounded title-page read control")
	mixed := flag.Bool("mixed", false, "mixed security/progress traffic")
	minimum := flag.Duration("mixed-duration", 600*time.Second, "minimum mixed observation")
	flag.Parse()
	frontierRAMHeads = *ramHeads
	generationRun = *genRun
	profileName = *profile
	cjkShare = *share
	oldCJKRecipe = *oldRecipe
	snapshotCap = *pinCap
	boundedReaders = *bounded
	mixedEnabled = *mixed
	mixedDuration = *minimum
	configure(*config)
	startCPUProfile(*cpuPath)
	defer stopCPUProfile()
	must(os.MkdirAll(*dir, 0755))
	path := filepath.Join(*dir, "search.sqlite")
	databasePath = path
	db := open(path)
	defer db.Close()
	var version string
	must(db.QueryRow("SELECT sqlite_version()").Scan(&version))
	if version != "3.53.4" {
		panic("qualifying measurements require bundled SQLite 3.53.4, got " + version)
	}
	driverVersion := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range bi.Deps {
			if dep.Path == "modernc.org/sqlite" {
				driverVersion = dep.Version
				if dep.Replace != nil {
					panic("unqualified modernc replacement")
				}
			}
		}
	}
	if driverVersion != "v1.58.0" {
		panic("qualifying measurements require bundled modernc v1.58.0, got " + driverVersion)
	}
	var sqliteSourceID string
	must(db.QueryRow("SELECT sqlite_source_id()").Scan(&sqliteSourceID))
	engineFingerprint = frozenFingerprint(sqliteSourceID)
	emit("environment", map[string]any{"sqlite": version, "engine": "bundled-modernc", "driver_module": "modernc.org/sqlite", "driver_version": driverVersion, "sqlite_source_id": sqliteSourceID, "unicode_fingerprint": engineFingerprint, "go": runtime.Version(), "cpus": runtime.NumCPU(), "profile": *profile, "variant": *v, "items": *n, "pid": os.Getpid(), "mode": *mode, "config": *config, "automerge": autoMerge, "crisismerge": crisisMerge, "batch": buildBatch, "merge_pages": mergePages, "synchronous": durability, "candidate_sha256": candidateSHA(), "candidate_fk_checks": publishedSchema, "cjk_share_percent": cjkShare, "old_cjk_recipe": oldCJKRecipe, "token_recipe": "CJK-NGRAM-RECIPE", "previous_candidate_sha256": "ef4b74ad542e0951c23760d15a3e60f295210d3bf0a1c29a9f9b944f1a4b722c"})
	if *mode == "reclaim-plan-fk-index" {
		ddl := "CREATE INDEX search_publish_document_fk ON search_publish_jobs(document_id) WHERE document_id IS NOT NULL"
		exec(db, ddl)
		emit("schema_overlay", map[string]any{"ddl": ddl, "scope": "explicit local diagnosis only; not canonical qualification"})
	}
	if *mode == "reclaim-plan" || *mode == "reclaim-plan-fk-index" {
		for _, q := range []string{"EXPLAIN QUERY PLAN DELETE FROM search_publications WHERE id=1", "EXPLAIN QUERY PLAN DELETE FROM search_fragments WHERE id=1"} {
			rows, e := db.Query(q)
			must(e)
			plan := []string{}
			for rows.Next() {
				var a, b, c int
				var detail string
				must(rows.Scan(&a, &b, &c, &detail))
				plan = append(plan, detail)
			}
			must(rows.Close())
			emit("reclaim_plan", map[string]any{"sql": q, "plan": plan})
		}
		return
	}
	if publishedSchema {
		createPublished(db)
	} else {
		create(db, *v)
	}
	setupExperiment(db, path)
	defer stopExperiment()
	defer dumpOperations()
	if publishedSchema {
		buildPublished(db, *n)
	} else {
		build(db, *n)
	}
	dumpOperations() // preserve source/build distributions before long/cancelled phases
	emit("allocation_base", allocation(db, path))
	emit("debt_base", debt(db))
	if *mode == "seed-only" {
		emit("seed_completed", map[string]any{"items": *n})
		return
	}
	if *mode == "publisher-wal" {
		shortOnlyBench(db, 128)
		publisherFlow(db, *n, 8)
		finishMixed(db)
		emit("publisher_wal_completed", map[string]any{"items": *n})
		return
	}
	if *mode == "publisher" {
		publisherBench(db, *n, *rounds)
		return
	}
	if *mode == "first-page" {
		firstPageBench(db, *n, *samples)
		emit("first_completed", map[string]any{"items": *n, "samples": *samples, "single_term_known_lead": true})
		return
	}
	if *mode == "policy" {
		policyHygiene(db)
		policyBench(db, *n, *samples)
		experiment(db, path, *n, *samples, *rounds, "recipe")
		return
	}
	if *mode != "baseline" {
		experiment(db, path, *n, *samples, *rounds, *mode)
		return
	}
	for _, t := range []string{"a", "jo", "ada", "harbor", "東京"} {
		s, args := termSQL(t, *v, false)
		rows, e := db.Query("EXPLAIN QUERY PLAN "+s, args...)
		must(e)
		plan := []string{}
		for rows.Next() {
			var id, p, u int
			var detail string
			must(rows.Scan(&id, &p, &u, &detail))
			plan = append(plan, detail)
		}
		rows.Close()
		emit("plan", map[string]any{"term": t, "sql": s, "args": args, "plan": plan})
	}
	quality(db, *v)
	latency(db, *v, *samples, "base")
	// One writer with foreground readers on separate connections; reader SQL owns its temp sets.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		rd := open(path)
		defer rd.Close()
		k := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			q := queries[k%len(queries)]
			t := time.Now()
			_, _ = search(rd, *v, q)
			emit("concurrent_query", map[string]any{"name": q.Name, "ms": float64(time.Since(t).Microseconds()) / 1000})
			k++
			time.Sleep(20 * time.Millisecond)
		}
	}()
	shared := max(128, *n/50)
	tombstones := 0
	peakWal := int64(0)
	for i := 0; i < *rounds; i++ {
		measured("churn.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			insertItem(db, *n+i, shared)
			renameTitle(db, (i*7919)%*n, renamedTitle((i*7919)%*n, i))
			renameFragment(db, 1+i%shared, renamedPerson(1+i%shared, i))
			tombstones += 2
			native(db, "churn.commit", "COMMIT")
		})
		if f, e := os.Stat(path + "-wal"); e == nil {
			peakWal = max(peakWal, f.Size())
		}
		maintain(db, "merge")
		if i%64 == 0 {
			emit("debt_churn", map[string]any{"round": i, "tombstone_operations": tombstones, "segments": debt(db), "wal_peak_bytes": peakWal})
			checkpoint(db)
		}
	}
	close(stop)
	<-done
	emit("debt_before_drain", debt(db))
	for i := 0; i < 256; i++ {
		merge(db, "drain")
	}
	checkpoint(db)
	emit("debt_after_drain", debt(db))
	emit("allocation_churn", allocation(db, path))
	latency(db, *v, *samples, "after_churn")
	controls(db, *v)
	// Maximum accepted body common-term/tombstone test and forced rollback of a merge.
	var body strings.Builder
	for j := 0; body.Len() < 65536; j++ {
		fmt.Fprintf(&body, "commonprefixterm commonprefix%06d ", j)
	}
	big := body.String()[:65536]
	for i := 0; i < 16; i++ {
		text := fmt.Sprintf("%04d", i) + big[4:]
		p, l, n := prefix(text), facettext.SearchDocument(text), facettext.SearchFold(text)
		measured("max_subject.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			renameFragmentValues(db, 1, p, l, n)
			native(db, "max_subject.commit", "COMMIT")
		})
		merge(db, "max_subject.merge")
	}
	for _, idx := range indexes {
		measured("merge_rollback."+idx, func() {
			exec(db, "BEGIN IMMEDIATE")
			exec(db, "INSERT INTO "+idx+"("+idx+",rank) VALUES('merge',?)", mergePages)
			native(db, "rollback", "ROLLBACK")
		})
	}
	checkpoint(db)
	emit("debt_final", debt(db))
	nativeMax.Lock()
	for tag, a := range nativeMax.values {
		emit("operation", map[string]any{"tag": tag, "latency": stats(a)})
	}
	nativeMax.Unlock()
	emit("completed", map[string]any{"wal_peak_bytes": peakWal, "tombstone_operations": tombstones, "peak_same_level": maxDebt})
}
