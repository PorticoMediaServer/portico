// Temporary full-DDL connection and schema-memory companion. Never product code.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	_ "github.com/PorticoMediaServer/portico/server/internal/dbwork"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func save(p string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(p+".tmp", b, 0644))
	must(os.Rename(p+".tmp", p))
}

var normalWrites, writeOnly, facetMode, frameMode bool

func open(path string) (*sql.DB, *sql.Conn) {
	db, e := sql.Open("sqlite", path)
	must(e)
	db.SetMaxOpenConns(1)
	c, e := db.Conn(context.Background())
	must(e)
	settings := "PRAGMA busy_timeout=1000; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA cache_size=-16384; PRAGMA temp_store=MEMORY;"
	if normalWrites {
		settings = strings.Replace(settings, "synchronous=FULL", "synchronous=NORMAL", 1) + "PRAGMA wal_autocheckpoint=0;"
	}
	_, e = c.ExecContext(context.Background(), settings)
	must(e)
	r, e := c.QueryContext(context.Background(), "SELECT id,title FROM catalog_entities LIMIT 40")
	must(e)
	for r.Next() {
		var id int64
		var title string
		must(r.Scan(&id, &title))
	}
	must(r.Err())
	must(r.Close())
	return db, c
}
func memory(c *sql.Conn) map[string]int {
	out := map[string]int{}
	must(c.Raw(func(dc any) error {
		st, ok := dc.(sqlite.DBStatus)
		if !ok {
			return fmt.Errorf("bundled driver has no DBStatus")
		}
		for n, op := range map[string]sqlite.DBStatusOp{"schema": sqlite.DBStatusSchemaUsed, "statement": sqlite.DBStatusStmtUsed, "pager": sqlite.DBStatusCacheUsed} {
			v, _, e := st.Status(op, false)
			if e != nil {
				return e
			}
			out[n] = v
		}
		return nil
	}))
	return out
}
func rss() int64 {
	b, e := os.ReadFile("/proc/self/status")
	must(e)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			v, e := strconv.ParseInt(strings.Fields(l)[1], 10, 64)
			must(e)
			return v * 1024
		}
	}
	return -1
}
func pct(a []float64, q float64) float64 {
	b := append([]float64{}, a...)
	sort.Float64s(b)
	return b[int(math.Ceil(float64(len(b))*q))-1]
}

var foldedTitles bool
var ddlPathForChildren, dbPathForChildren string

func acceptanceSQL(start, count int) string {
	end := start + count - 1
	q := fmt.Sprintf(`WITH RECURSIVE n(id) AS(VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,duration_key,file_size_key,resolution_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
 SELECT id,CAST(printf('%%016d',id) AS BLOB),1,1,'Word'||char(97+id%%26),'word'||char(97+id%%26),'w','word'||char(97+id%%26),'w',(id*2654435761)%%4294967296,printf('2026-11-04T00:00:00.%%09dZ',id),7200+id%%1000,1000000+id,1080,randomblob(16),randomblob(16),randomblob(16),1 FROM n;
 INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
 SELECT id,printf('asset-%%d',id),printf('/synthetic/full/%%d',id),file_size_key,1,'mkv','h264','aac',1920,1080,duration_key FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO catalog_asset_links(entity_id,asset_id,info_duration) SELECT id,id,duration_key FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO search_own(entity_id,library_id,kind,title_fold,title_prefix_tokens,initial_prefix_tokens) SELECT id,library_id,kind,sort_key,'00007700006f000072000064'||printf('%%06x',97+id%%26),'00007700006f000072000064'||printf('%%06x',97+id%%26) FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) SELECT entity_id,title_prefix_tokens,initial_prefix_tokens FROM search_own WHERE entity_id BETWEEN %d AND %d;
 UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='full-library';
 INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','library','full-library',1);`, start, end, start, end, start, end, start, end, start, end)
	if foldedTitles {
		q = strings.Replace(q, "playback_origin_incarnation,playback_origin_revision)", "playback_origin_incarnation,playback_origin_revision,title_fold,title_prefix_tokens,initial_prefix_tokens)", 1)
		q = strings.Replace(q, "randomblob(16),randomblob(16),randomblob(16),1 FROM n;", "randomblob(16),randomblob(16),randomblob(16),1,'word'||char(97+id%26),'00007700006f000072000064'||printf('%06x',97+id%26),'00007700006f000072000064'||printf('%06x',97+id%26) FROM n;", 1)
		q = strings.Replace(q, "audio_codec,width,height,duration)", "audio_codec,width,height,duration,library_id)", 1)
		q = strings.Replace(q, "1920,1080,duration_key FROM catalog_entities", "1920,1080,duration_key,1 FROM catalog_entities", 1)
		q = strings.Replace(q, "catalog_asset_links(entity_id,asset_id,info_duration) SELECT id,id,duration_key", "catalog_asset_links(entity_id,asset_id,info_duration,library_id) SELECT id,id,duration_key,1", 1)
		a := strings.Index(q, " INSERT INTO search_own(entity_id")
		b := a + strings.Index(q[a:], " UPDATE libraries")
		q = q[:a] + fmt.Sprintf(" INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) SELECT id,title_prefix_tokens,initial_prefix_tokens FROM catalog_entities WHERE id BETWEEN %d AND %d;\n", start, end) + q[b:]
	}
	if normalWrites {
		q += fmt.Sprintf("INSERT INTO admin_receipts(scope,operation_id,digest,result,created_ms) VALUES('research.ingest','%d-%d','%s','applied',1);", start, count, strings.Repeat("0", 64))
	}
	return q
}
func seedAndSweep(c *sql.Conn, target int, out, hash string) {
	var n int
	var legacyOwn int
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='search_own'").Scan(&legacyOwn))
	foldedTitles = legacyOwn == 0
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&n))
	if n == 0 {
		q := `INSERT INTO libraries(id,name,kind,root) VALUES('full-library','Full fixture','movie','/synthetic/full');INSERT INTO catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1); INSERT OR IGNORE INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0);`
		if foldedTitles {
			q = strings.Replace(q, "catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1)", "catalog_libraries(id,library_id,root) VALUES(1,'full-library','/synthetic/full')", 1)
		}
		_, e := c.ExecContext(context.Background(), q)
		must(e)
	}
	if target > 10000 && os.Getenv("GITHUB_RUN_ID") == "" {
		panic("full fixture >10k is hosted only")
	}
	batches := []any{}
	for n < target {
		count := 1000
		if target-n < count {
			count = target - n
		}
		t := time.Now()
		_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE;"+acceptanceSQL(n+1, count)+"COMMIT;")
		must(e)
		batches = append(batches, map[string]any{"items": count, "ms": float64(time.Since(t).Nanoseconds()) / 1e6})
		n += count
		if n%100000 == 0 {
			save(filepath.Join(out, "full-seed-progress.json"), map[string]any{"ddl_sha256": hash, "entities": n, "batches": batches})
			fmt.Println("full seed", n)
		}
	}
	save(filepath.Join(out, "full-seed.json"), map[string]any{"ddl_sha256": hash, "entities": n, "batches": batches, "shape": "Basic movie acceptance: entity+asset+link+encoded own-title FTS+library revision/event. No provider metadata, staging, scanner IO, prepared-media or full production typed command."})
	if facetMode || frameMode {
		return
	}
	if !writeOnly {
		for rep := 0; rep < 3; rep++ {
			cmd := exec.Command(os.Args[0], "--ddl", ddlPathForChildren, "--db", dbPathForChildren, "--out", out, "--browse-child")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			must(cmd.Run())
		}
	}
	rows := []any{}
	sizes := []int{16, 32, 64, 128, 256, 1000}
	runs := 128
	if os.Getenv("GITHUB_RUN_ID") == "" {
		sizes = []int{16}
		runs = 1
	}
	for _, count := range sizes {
		xs := []float64{}
		t := time.Now()
		for rep := 0; rep < runs; rep++ {
			sqltext := acceptanceSQL(n+1, count)
			_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			start := time.Now()
			_, e = c.ExecContext(context.Background(), sqltext)
			must(e)
			_, e = c.ExecContext(context.Background(), "COMMIT")
			must(e)
			xs = append(xs, float64(time.Since(start).Nanoseconds())/1e6)
			n += count
		}
		mx := pct(xs, 1)
		p99 := pct(xs, .99)
		row := map[string]any{"ddl_sha256": hash, "batch": count, "synchronous": map[bool]string{true: "NORMAL", false: "FULL"}[normalWrites], "autocheckpoint": map[bool]int{true: 0, false: 1000}[normalWrites], "n": len(xs), "samples_ms": xs, "p50_ms": pct(xs, .5), "p95_ms": pct(xs, .95), "p99_ms": p99, "max_ms": mx, "strict": p99 <= 10 && mx <= 25, "max50": mx <= 50, "entities_per_s": float64(count*runs) / time.Since(t).Seconds(), "resident_entities": n}
		rows = append(rows, row)
		save(filepath.Join(out, "full-write-sweep.json"), rows)
		fmt.Println("full sweep", count, "p99", p99, "max", mx)
	}
	_, e := c.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	must(e)
}
func browse(c *sql.Conn, out, hash string) {
	results := []any{}
	var resident int
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&resident))
	sorts := []string{"sort_key COLLATE NOCASE", "sort_key_keep_articles COLLATE NOCASE", "added_at", "year", "duration_key", "community_rating_key", "critic_rating_key", "file_size_key", "resolution_key", "release_key", "random_key"}
	samples := 500
	if os.Getenv("GITHUB_RUN_ID") == "" {
		samples = 2
	}
	for _, col := range sorts {
		for _, direction := range []string{"ASC", "DESC"} {
			for _, projection := range []string{"id", "id,title,overview,original_title,content_rating,added_at,duration_key,file_size_key,resolution_key,year"} {
				for _, page := range []string{"first", "random-keyset"} {
					if resident < 4096 && page == "random-keyset" {
						continue
					}
					xs := []float64{}
					var plan []any
					var lastSQL string
					for j := -20; j < samples; j++ {
						where := "library_id=1 AND kind=1 AND retired=0"
						if page == "random-keyset" {
							anchor := 2048 + ((j+21)*104729)%(resident-4096)
							op := ">"
							if direction == "DESC" {
								op = "<"
							}
							where += fmt.Sprintf(" AND (%s,id)%s(SELECT %s,id FROM catalog_entities WHERE id=%d)", col, op, col, anchor)
						}
						q := "SELECT " + projection + " FROM catalog_entities WHERE " + where + " ORDER BY " + col + " " + direction + ",id " + direction + " LIMIT 40"
						lastSQL = q
						start := time.Now()
						rows, e := c.QueryContext(context.Background(), q)
						must(e)
						columns, e := rows.Columns()
						must(e)
						count := 0
						for rows.Next() {
							v := make([]any, len(columns))
							ptr := make([]any, len(columns))
							for k := range v {
								ptr[k] = &v[k]
							}
							must(rows.Scan(ptr...))
							count++
						}
						must(rows.Err())
						must(rows.Close())
						ms := float64(time.Since(start).Nanoseconds()) / 1e6
						if count != 40 {
							panic(fmt.Sprintf("incomplete browse rows %d", count))
						}
						if j >= 0 {
							xs = append(xs, ms)
						}
					}
					rows, e := c.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+lastSQL)
					must(e)
					for rows.Next() {
						var a, b, d int
						var detail string
						must(rows.Scan(&a, &b, &d, &detail))
						plan = append(plan, detail)
					}
					must(rows.Close())
					results = append(results, map[string]any{"sort": col, "direction": direction, "projection": projection, "page": page, "n": len(xs), "p50_ms": pct(xs, .5), "p95_ms": pct(xs, .95), "p99_ms": pct(xs, .99), "max_ms": pct(xs, 1), "samples_ms": xs, "last_sql": lastSQL, "plan": plan})
				}
			}
		}
	}
	save(filepath.Join(out, fmt.Sprintf("bundled-browse-%d.json", os.Getpid())), map[string]any{"ddl_sha256": hash, "entities": resident, "results": results, "boundary": "Movie entity SQL pages; ID-only vs card columns, first vs changing tuple anchors, complete40rows,500samples after20warmups; no authority/HTTP, OS warm"})
}
func main() {
	ddl := flag.String("ddl", "", "exact integrated DDL")
	dbpath := flag.String("db", "", "disposable db")
	out := flag.String("out", "", "receipts")
	child := flag.Bool("child", false, "100 physical opens")
	browseChild := flag.Bool("browse-child", false, "500 samples per entity sort/page")
	target := flag.Int("seed", 1000000, "resident Basic movie facts")
	normalFlag := flag.Bool("normal", false, "NORMAL writer and separate PASSIVE checkpoint")
	writeFlag := flag.Bool("write-only", false, "seed and writer quanta only")
	facetFlag := flag.Bool("facets", false, "rating-band counts and restricted indexes")
	frameFlag := flag.Bool("frames", false, "max source-frame replacements")
	flag.Parse()
	facetMode = *facetFlag
	frameMode = *frameFlag
	normalWrites = *normalFlag
	writeOnly = *writeFlag
	ddlPathForChildren = *ddl
	dbPathForChildren = *dbpath
	must(os.MkdirAll(*out, 0700))
	if *browseChild {
		b, e := os.ReadFile(*ddl)
		must(e)
		sum := sha256.Sum256(b)
		d, c := open(*dbpath)
		browse(c, *out, hex.EncodeToString(sum[:]))
		must(c.Close())
		must(d.Close())
		return
	}
	if *child {
		xs := []float64{}
		for j := 0; j < 100; j++ {
			t := time.Now()
			db, c := open(*dbpath)
			xs = append(xs, float64(time.Since(t).Nanoseconds())/1e6)
			must(c.Close())
			must(db.Close())
		}
		body, e := os.ReadFile(*ddl)
		must(e)
		hash := sha256.Sum256(body)
		save(filepath.Join(*out, fmt.Sprintf("bundled-opens-%d.json", os.Getpid())), map[string]any{"ddl_sha256": hex.EncodeToString(hash[:]), "samples_ms": xs, "n": 100, "p50_ms": pct(xs, .5), "p95_ms": pct(xs, .95), "max_ms": pct(xs, 1)})
		return
	}
	body, e := os.ReadFile(*ddl)
	must(e)
	sum := sha256.Sum256(body)
	if _, e = os.Stat(*dbpath); os.IsNotExist(e) {
		db, e := sql.Open("sqlite", *dbpath)
		must(e)
		_, e = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; BEGIN;" + string(body) + " COMMIT;")
		must(e)
		must(db.Close())
	}
	db, c := open(*dbpath)
	hash := hex.EncodeToString(sum[:])
	if *target > 0 {
		var stop chan struct{}
		var wg sync.WaitGroup
		checkpoints := []any{}
		if normalWrites {
			stop = make(chan struct{})
			wg.Add(1)
			go func() {
				defer wg.Done()
				d, cc := open(*dbpath)
				defer d.Close()
				defer cc.Close()
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						start := time.Now()
						var busy, log, done int
						e := cc.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &done)
						err := ""
						if e != nil {
							err = e.Error()
						}
						checkpoints = append(checkpoints, map[string]any{"start_ns": start.UnixNano(), "ms": float64(time.Since(start).Nanoseconds()) / 1e6, "busy": busy, "log": log, "checkpointed": done, "error": err})
					}
				}
			}()
		}
		seedAndSweep(c, *target, *out, hash)
		if facetMode {
			facetExperiment(c, *out, hash)
		}
		if stop != nil {
			close(stop)
			wg.Wait()
			save(filepath.Join(*out, "normal-checkpoints.json"), map[string]any{"ddl_sha256": hash, "interval_ms": 100, "synchronous": "NORMAL", "writer_wal_autocheckpoint": 0, "samples": checkpoints})
		}
	}
	var tables, entities int64
	var version string
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables))
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&entities))
	must(c.QueryRowContext(context.Background(), "SELECT sqlite_version()").Scan(&version))
	save(filepath.Join(*out, "full-manifest.json"), map[string]any{"ddl_sha256": hex.EncodeToString(sum[:]), "tables": tables, "entities": entities, "sqlite_version": version, "go": runtime.Version(), "boundary": "Exact complete DDL; Basic movie-only populated fixture, no production statement cache or full private typed writer/scanner/provider paths."})
	must(c.Close())
	must(db.Close())
	if writeOnly {
		return
	}
	for j := 0; j < 3; j++ {
		cmd := exec.Command(os.Args[0], "--ddl", *ddl, "--db", *dbpath, "--out", *out, "--child")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		must(cmd.Run())
	}
	type held struct {
		d *sql.DB
		c *sql.Conn
	}
	connections := []held{}
	pools := []any{}
	for _, n := range []int{0, 1, 8, 16, 32} {
		for len(connections) < n {
			d, c := open(*dbpath)
			connections = append(connections, held{d, c})
		}
		runtime.GC()
		var gm runtime.MemStats
		runtime.ReadMemStats(&gm)
		native := []any{}
		for _, h := range connections {
			native = append(native, memory(h.c))
		}
		pools = append(pools, map[string]any{"connections": n, "rss_bytes": rss(), "go_heap_alloc": gm.HeapAlloc, "go_sys": gm.Sys, "native": native})
	}
	save(filepath.Join(*out, "bundled-memory.json"), map[string]any{"ddl_sha256": hash, "pools": pools})
	for _, h := range connections {
		must(h.c.Close())
		must(h.d.Close())
	}
}

func readSample(c *sql.Conn, q string, deadline time.Duration) (float64, int, string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	t := time.Now()
	rows, e := c.QueryContext(ctx, q)
	if e != nil {
		return float64(time.Since(t).Nanoseconds()) / 1e6, 0, "", e.Error()
	}
	defer rows.Close()
	cols, e := rows.Columns()
	must(e)
	n := 0
	h := sha256.New()
	for rows.Next() {
		v := make([]any, len(cols))
		p := make([]any, len(cols))
		for j := range v {
			p[j] = &v[j]
		}
		if e = rows.Scan(p...); e != nil {
			break
		}
		fmt.Fprintln(h, v)
		n++
	}
	if e == nil {
		e = rows.Err()
	}
	err := ""
	if e != nil {
		err = e.Error()
	}
	return float64(time.Since(t).Nanoseconds()) / 1e6, n, hex.EncodeToString(h.Sum(nil)), err
}
func readCohort(c *sql.Conn, q string, samples int, budget time.Duration) map[string]any {
	good := []float64{}
	bad := []float64{}
	errors := map[string]int{}
	var count int
	var hash string
	for j := 0; j < samples; j++ {
		ms, n, h, e := readSample(c, q, budget)
		if e != "" {
			bad = append(bad, ms)
			errors[e]++
		} else {
			good = append(good, ms)
			count = n
			hash = h
		}
	}
	result := map[string]any{"samples_ms": good, "refused_ms": bad, "errors": errors, "completed": len(good), "refused": len(bad), "rows": count, "row_hash": hash, "sql": q, "budget_ms": budget.Milliseconds()}
	if len(good) > 0 {
		result["p50_ms"] = pct(good, .5)
		result["p95_ms"] = pct(good, .95)
		result["p99_ms"] = pct(good, .99)
	}
	if len(bad) > 0 {
		result["refusal_p95_ms"] = pct(bad, .95)
		result["refusal_max_ms"] = pct(bad, 1)
	}
	return result
}
func facetExperiment(c *sql.Conn, out, hash string) {
	overlay := `ALTER TABLE catalog_entities ADD COLUMN age_any INTEGER NOT NULL DEFAULT -1; ALTER TABLE catalog_entities ADD COLUMN age_rated INTEGER;
 ALTER TABLE catalog_scalar_contributions ADD COLUMN child_age_any INTEGER; ALTER TABLE catalog_scalar_contributions ADD COLUMN child_age_rated INTEGER;
 CREATE INDEX scalar_age_any ON catalog_scalar_contributions(parent_id,child_age_any,contributor_id) WHERE child_age_any IS NOT NULL;
 CREATE INDEX scalar_age_rated ON catalog_scalar_contributions(parent_id,child_age_rated,contributor_id) WHERE child_age_rated IS NOT NULL;
 CREATE TABLE rating_band_facet_counts(library_id INTEGER NOT NULL,kind INTEGER NOT NULL,term_id INTEGER NOT NULL,mode INTEGER NOT NULL CHECK(mode IN(0,1)),rating_band INTEGER NOT NULL,total INTEGER NOT NULL CHECK(total>=0),PRIMARY KEY(library_id,kind,term_id,mode,rating_band)) WITHOUT ROWID,STRICT;`
	_, e := c.ExecContext(context.Background(), overlay)
	must(e)
	_, e = c.ExecContext(context.Background(), `UPDATE catalog_entities SET age_any=CASE WHEN id%1000=0 THEN 0 WHEN id%1000=1 THEN 8 WHEN id%20=0 THEN -1 ELSE CASE id%3 WHEN 0 THEN 13 WHEN 1 THEN 17 ELSE 18 END END;UPDATE catalog_entities SET age_rated=CASE WHEN age_any>=0 THEN age_any END;WITH RECURSIVE n(id) AS(VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<30) INSERT INTO catalog_terms(id,vocab,key,label) SELECT id,1,printf('genre-%d',id),printf('Genre%d',id) FROM n;INSERT INTO catalog_entity_terms SELECT e.id,1+((e.id*7+v.column1)%30) FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2)) v;`)
	must(e)
	// Normalized rating eligibility is fixed-policy source-derived, never keyed by user/profile.
	fill := `INSERT INTO rating_band_facet_counts SELECT e.library_id,e.kind,t.term_id,0,e.age_any,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id GROUP BY e.library_id,e.kind,t.term_id,e.age_any;
 INSERT INTO rating_band_facet_counts SELECT e.library_id,e.kind,t.term_id,1,e.age_rated,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.age_rated IS NOT NULL GROUP BY e.library_id,e.kind,t.term_id,e.age_rated;`
	_, e = c.ExecContext(context.Background(), fill)
	must(e)
	sorts := []string{"sort_key", "added_at", "year", "duration_key", "community_rating_key", "critic_rating_key", "file_size_key", "resolution_key", "release_key", "random_key"}
	var before int64
	must(c.QueryRowContext(context.Background(), "SELECT sum(pgsize) FROM dbstat").Scan(&before))
	results := []any{}
	samples := 500
	if os.Getenv("GITHUB_RUN_ID") == "" {
		samples = 2
	}
	budget := 100 * time.Millisecond
	for _, mode := range []int{0, 1} {
		field := "age_any"
		if mode == 1 {
			field = "age_rated"
		}
		for _, ceiling := range []int{8, 17} {
			source := fmt.Sprintf("SELECT t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.library_id=1 AND e.kind=1 AND e.%s IS NOT NULL AND e.%s<=%d GROUP BY t.term_id ORDER BY t.term_id", field, field, ceiling)
			fast := fmt.Sprintf("SELECT term_id,sum(total) FROM rating_band_facet_counts WHERE library_id=1 AND kind=1 AND mode=%d AND rating_band<=%d GROUP BY term_id HAVING sum(total)>0 ORDER BY term_id", mode, ceiling)
			raw := readCohort(c, source, 1, 120*time.Second)
			quick := readCohort(c, fast, samples, budget)
			if raw["row_hash"] != quick["row_hash"] {
				panic("rating-band count mismatch")
			}
			results = append(results, map[string]any{"case": "facet", "mode": mode, "ceiling": ceiling, "source": raw, "candidate": quick})
		}
	}
	// Add exact prefix indexes after baseline facets; size and extra per-change work are charged.
	for _, field := range []string{"age_any", "age_rated"} {
		for _, sortcol := range sorts {
			q := fmt.Sprintf("CREATE INDEX restricted_%s_%s ON catalog_entities(library_id,kind,%s,%s,id)", field, sortcol, field, sortcol)
			_, e = c.ExecContext(context.Background(), q)
			must(e)
			overlay += q + ";"
		}
	}
	for _, mode := range []int{0, 1} {
		field := "age_any"
		if mode == 1 {
			field = "age_rated"
		}
		for _, ceiling := range []int{8, 17} {
			for _, sortcol := range sorts {
				q := bandBrowse(field, ceiling, sortcol, "")
				results = append(results, map[string]any{"case": "browse", "mode": mode, "ceiling": ceiling, "sort": sortcol, "candidate": readCohort(c, q, samples, budget)})
			}
		}
	}
	// Arbitrary label sets remain exact anti-joins over source membership, not another user catalogue.
	_, e = c.ExecContext(context.Background(), `CREATE TABLE research_labels(item_id INTEGER NOT NULL,label_id INTEGER NOT NULL,PRIMARY KEY(item_id,label_id)) WITHOUT ROWID;`)
	must(e)
	for j, div := range []int{11, 23, 47, 97, 193} {
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO research_labels SELECT id,%d FROM catalog_entities WHERE id%%%d=0", j+1, div))
		must(e)
	}
	fallbackSamples := 21
	if os.Getenv("GITHUB_RUN_ID") == "" {
		fallbackSamples = 2
	}
	for _, labels := range []string{"1", "1,2,3", "1,2,3,4,5"} {
		exclude := "NOT EXISTS(SELECT 1 FROM research_labels l WHERE l.item_id=e.id AND l.label_id IN(" + labels + "))"
		for _, ceiling := range []int{8, 17} {
			q := fmt.Sprintf("SELECT t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.library_id=1 AND e.kind=1 AND e.age_rated IS NOT NULL AND e.age_rated<=%d AND %s GROUP BY t.term_id ORDER BY t.term_id", ceiling, exclude)
			results = append(results, map[string]any{"case": "label-facet", "labels": labels, "ceiling": ceiling, "result": readCohort(c, q, fallbackSamples, budget)})
			for _, sortcol := range []string{"sort_key", "added_at", "duration_key", "random_key"} {
				results = append(results, map[string]any{"case": "label-browse", "labels": labels, "ceiling": ceiling, "sort": sortcol, "result": readCohort(c, bandBrowse("age_rated", ceiling, sortcol, exclude), fallbackSamples, budget)})
			}
		}
	}
	writes := []float64{}
	changes := []int64{}
	reps := 501
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	var resident int
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&resident))
	for j := 0; j < reps; j++ {
		item := 1 + (j*7919)%resident
		var old int
		must(c.QueryRowContext(context.Background(), fmt.Sprintf("SELECT age_any FROM catalog_entities WHERE id=%d", item)).Scan(&old))
		next := 0
		if old == 0 {
			next = 8
		}
		sqltext := bandDeltaSQL(item, old, next) + fmt.Sprintf("UPDATE catalog_entities SET age_any=%d,age_rated=%d WHERE id=%d;INSERT INTO admin_receipts VALUES('band','%d','%s','applied',1);", next, next, item, j, strings.Repeat("0", 64))
		var a, b int64
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&a))
		_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		start := time.Now()
		_, e = c.ExecContext(context.Background(), sqltext)
		must(e)
		_, e = c.ExecContext(context.Background(), "COMMIT")
		must(e)
		writes = append(writes, float64(time.Since(start).Nanoseconds())/1e6)
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&b))
		changes = append(changes, b-a)
	}
	alloc := []any{}
	rows, e := c.QueryContext(context.Background(), "SELECT name,sum(pgsize),sum(payload) FROM dbstat WHERE name LIKE 'restricted_%' OR name='rating_band_facet_counts' GROUP BY name")
	must(e)
	for rows.Next() {
		var name string
		var pages, payload int64
		must(rows.Scan(&name, &pages, &payload))
		alloc = append(alloc, []any{name, pages, payload})
	}
	must(rows.Close())
	oh := sha256.Sum256([]byte(overlay))
	save(filepath.Join(out, "band-facet-results.json"), map[string]any{"base_ddl_sha256": hash, "overlay_sha256": hex.EncodeToString(oh[:]), "overlay": overlay, "results": results, "allocation": alloc, "write_samples_ms": writes, "write_rows": changes, "write_p99_ms": pct(writes, .99), "write_max_ms": pct(writes, 1), "counter_before_index_bytes": before, "boundary": "Movie/per-item age-only fixed projections; arbitrary labels exact anti-join with100ms SQL deadline. No per-profile materialization. Container age propagation separate."})
}
func bandBrowse(field string, ceiling int, sortcol, exclude string) string {
	parts := []string{}
	for _, band := range []int{-1, 0, 8, 13, 17, 18} {
		if band > ceiling {
			continue
		}
		where := fmt.Sprintf("e.library_id=1 AND e.kind=1 AND e.%s=%d", field, band)
		if exclude != "" {
			where += " AND " + exclude
		}
		parts = append(parts, fmt.Sprintf("SELECT * FROM(SELECT e.id,e.%s AS value FROM catalog_entities e WHERE %s ORDER BY e.%s DESC,e.id DESC LIMIT40)", sortcol, where, sortcol))
	}
	q := "SELECT * FROM(" + strings.Join(parts, " UNION ALL ") + ") ORDER BY value DESC,id DESC LIMIT40"
	return strings.ReplaceAll(q, "LIMIT40", "LIMIT 40")
}
func bandDeltaSQL(item, old, next int) string {
	q := ""
	for _, mode := range []int{0, 1} {
		q += fmt.Sprintf("UPDATE rating_band_facet_counts SET total=total-1 WHERE library_id=1 AND kind=1 AND mode=%d AND rating_band=%d AND term_id IN(SELECT term_id FROM catalog_entity_terms WHERE entity_id=%d);", mode, old, item)
		q += fmt.Sprintf("INSERT INTO rating_band_facet_counts SELECT1,1,term_id,%d,%d,1 FROM catalog_entity_terms WHERE entity_id=%d ON CONFLICT(library_id,kind,term_id,mode,rating_band) DO UPDATE SET total=total+1;", mode, next, item)
	}
	return strings.ReplaceAll(q, "SELECT1", "SELECT 1")
}
