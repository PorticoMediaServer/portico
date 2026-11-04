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
func open(path string) (*sql.DB, *sql.Conn) {
	db, e := sql.Open("sqlite", path)
	must(e)
	db.SetMaxOpenConns(1)
	c, e := db.Conn(context.Background())
	must(e)
	_, e = c.ExecContext(context.Background(), "PRAGMA busy_timeout=1000; PRAGMA foreign_keys=ON; PRAGMA synchronous=FULL; PRAGMA cache_size=-16384; PRAGMA temp_store=MEMORY;")
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
func acceptanceSQL(start, count int) string {
	end := start + count - 1
	return fmt.Sprintf(`WITH RECURSIVE n(id) AS(VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d)
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,duration_key,file_size_key,resolution_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision)
 SELECT id,CAST(printf('%%016d',id) AS BLOB),1,1,'Word'||char(97+id%%26),'word'||char(97+id%%26),'w','word'||char(97+id%%26),'w',(id*2654435761)%%4294967296,printf('2026-11-04T00:00:00.%%09dZ',id),7200+id%%1000,1000000+id,1080,randomblob(16),randomblob(16),randomblob(16),1 FROM n;
 INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration)
 SELECT id,printf('asset-%%d',id),printf('/synthetic/full/%%d',id),file_size_key,1,'mkv','h264','aac',1920,1080,duration_key FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO catalog_asset_links(entity_id,asset_id,info_duration) SELECT id,id,duration_key FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO search_own(entity_id,library_id,kind,title_fold,title_prefix_tokens,initial_prefix_tokens) SELECT id,library_id,kind,sort_key,'00007700006f000072000064'||printf('%%06x',97+id%%26),'00007700006f000072000064'||printf('%%06x',97+id%%26) FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) SELECT entity_id,title_prefix_tokens,initial_prefix_tokens FROM search_own WHERE entity_id BETWEEN %d AND %d;
 UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='full-library';
 INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','library','full-library',1);`, start, end, start, end, start, end, start, end, start, end)
}
func seedAndSweep(c *sql.Conn, target int, out, hash string) {
	var n int
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&n))
	if n == 0 {
		_, e := c.ExecContext(context.Background(), `INSERT INTO libraries(id,name,kind,root) VALUES('full-library','Full fixture','movie','/synthetic/full');INSERT INTO catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1); INSERT OR IGNORE INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0);`)
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
		row := map[string]any{"ddl_sha256": hash, "batch": count, "n": len(xs), "samples_ms": xs, "p50_ms": pct(xs, .5), "p95_ms": pct(xs, .95), "p99_ms": p99, "max_ms": mx, "strict": p99 <= 10 && mx <= 25, "max50": mx <= 50, "entities_per_s": float64(count*runs) / time.Since(t).Seconds(), "resident_entities": n}
		rows = append(rows, row)
		save(filepath.Join(out, "full-write-sweep.json"), rows)
		fmt.Println("full sweep", count, "p99", p99, "max", mx)
	}
	_, e := c.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	must(e)
}
func main() {
	ddl := flag.String("ddl", "", "exact integrated DDL")
	dbpath := flag.String("db", "", "disposable db")
	out := flag.String("out", "", "receipts")
	child := flag.Bool("child", false, "100 physical opens")
	target := flag.Int("seed", 1000000, "resident Basic movie facts")
	flag.Parse()
	must(os.MkdirAll(*out, 0700))
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
		seedAndSweep(c, *target, *out, hash)
	}
	var tables, entities int64
	var version string
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables))
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&entities))
	must(c.QueryRowContext(context.Background(), "SELECT sqlite_version()").Scan(&version))
	save(filepath.Join(*out, "full-manifest.json"), map[string]any{"ddl_sha256": hex.EncodeToString(sum[:]), "tables": tables, "entities": entities, "sqlite_version": version, "go": runtime.Version(), "boundary": "Exact complete DDL; Basic movie-only populated fixture, no production statement cache or full private typed writer/scanner/provider paths."})
	must(c.Close())
	must(db.Close())
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
