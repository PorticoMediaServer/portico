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

var normalWrites, writeOnly, facetMode, frameMode, gatesOnly, browseOnly, admissionSchema, attributionMode, localControl, identitySchema, omitFTS, ablationMode, capsMode bool

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

var checkpointMu sync.Mutex
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
	if admissionSchema {
		q = fmt.Sprintf("WITH RECURSIVE n(id) AS(VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d) INSERT INTO catalog_subjects(id,public_id,library_id,kind) SELECT id,CAST(printf('%%016d',id) AS BLOB),1,1 FROM n;", start, end) + q
		q = strings.Replace(q, "initial_prefix_tokens)", "initial_prefix_tokens,admission_allow_age)", 1)
		q = strings.Replace(q, "printf('%06x',97+id%26) FROM n;", "printf('%06x',97+id%26),-1 FROM n;", 1)
		q += fmt.Sprintf("INSERT INTO catalog_age_item_counts(library_id,kind,block_unrated,admission_age,total) VALUES(1,1,0,-1,%d) ON CONFLICT(library_id,kind,block_unrated,admission_age) DO UPDATE SET total=total+excluded.total;", count)
	}
	if identitySchema {
		q = fmt.Sprintf("INSERT INTO catalog_identities(library_id,source_key,subject_id) WITH RECURSIVE n(id) AS(VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d) SELECT 1,printf('/synthetic/full/%%d',id),id FROM n;", start, end) + q
		// Subjects must precede identity FK: move this statement after its subject INSERT.
		sep := strings.Index(q, ";") + 1
		identity := q[:sep]
		q = q[sep:]
		subjectEnd := strings.Index(q, ";") + 1
		q = q[:subjectEnd] + identity + q[subjectEnd:]
	}
	if omitFTS {
		parts := strings.Split(q, ";")
		q = ""
		for _, part := range parts {
			if !strings.Contains(part, "INSERT INTO search_own_prefix") && strings.TrimSpace(part) != "" {
				q += part + ";"
			}
		}
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
	must(c.QueryRowContext(context.Background(), "SELECT count(*)>0 FROM pragma_table_info('catalog_identities') WHERE name='library_id'").Scan(&identitySchema))
	must(c.QueryRowContext(context.Background(), "SELECT count(*)>0 FROM pragma_table_info('catalog_entities') WHERE name='admission_allow_age'").Scan(&admissionSchema))
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&n))
	if n == 0 {
		q := `INSERT INTO libraries(id,name,kind,root) VALUES('full-library','Full fixture','movie','/synthetic/full');INSERT INTO catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1); INSERT OR IGNORE INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0);`
		if foldedTitles {
			q = strings.Replace(q, "catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1)", "catalog_libraries(id,library_id,root) VALUES(1,'full-library','/synthetic/full')", 1)
		}
		_, e := c.ExecContext(context.Background(), q)
		must(e)
	}
	if target > 10000 && os.Getenv("GITHUB_RUN_ID") == "" && !(localControl && target == 50000) {
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
	if facetMode || frameMode || gatesOnly || attributionMode || capsMode {
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
	if browseOnly {
		return
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
					refusals := []float64{}
					errs := map[string]int{}
					terminal := 0
					var plan []any
					var lastSQL string
					for j := -20; j < samples; j++ {
						where := "library_id=1 AND kind=1 AND retired=0"
						if col == "resolution_key" {
							where += " AND kind IN(1,2,3,4,10)"
						}
						if page == "random-keyset" {
							anchor := resident/5 + ((j+21)*104729)%(resident*3/5)
							op := ">"
							if direction == "DESC" {
								op = "<"
							}
							where += fmt.Sprintf(" AND (%s,id)%s(SELECT %s,id FROM catalog_entities WHERE id=%d)", col, op, col, anchor)
						}
						q := "SELECT " + projection + " FROM catalog_entities WHERE " + where + " ORDER BY " + col + " " + direction + ",id " + direction + " LIMIT 40"
						lastSQL = q
						ms, n, _, err := readSample(c, q, 200*time.Millisecond)
						if err != "" {
							errs[err]++
							refusals = append(refusals, ms)
							if len(refusals) >= 5 {
								break
							}
							continue
						}
						if n > 40 {
							panic("browse limit violated")
						}
						if n < 40 {
							terminal++
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
					result := map[string]any{"sort": col, "direction": direction, "projection": projection, "page": page, "n": len(xs), "refusals_ms": refusals, "errors": errs, "terminal_pages": terminal, "samples_ms": xs, "last_sql": lastSQL, "plan": plan}
					if len(xs) > 0 {
						result["p50_ms"] = pct(xs, .5)
						result["p95_ms"] = pct(xs, .95)
						result["p99_ms"] = pct(xs, .99)
						result["max_ms"] = pct(xs, 1)
					}
					results = append(results, result)
					save(filepath.Join(out, fmt.Sprintf("bundled-browse-%d.json", os.Getpid())), map[string]any{"ddl_sha256": hash, "entities": resident, "results": results, "boundary": "Movie SQL; 500 attempts after20warmups; first/changing tuple anchors; 200ms physical read deadline, refusals excluded from ready percentiles; logical partial-index guard explicit; warm OS, no HTTP/authority"})
				}
			}
		}
	}
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
	capsFlag := flag.Bool("caps-product", false, "shared100collections/255CUE size contribution collapse")
	omitFlag := flag.Bool("fts-excluded", false, "diagnostic ONLY omitFTS during measured cohort, never seed")
	ablationFlag := flag.Bool("ablation", false, "one local batch8FTS control,501tx")
	localFlag := flag.Bool("local-control", false, "authorized50k quiet-machine control; maximum82769 residents")
	attributionFlag := flag.Bool("attribution", false, "small batch and per-statement writer phases")
	gatesFlag := flag.Bool("gates", false, "exact schema open/memory only after small population")
	browseFlag := flag.Bool("browse-only", false, "skip shaped write sweep after browse")
	frameFlag := flag.Bool("frames", false, "max source-frame replacements")
	flag.Parse()
	capsMode = *capsFlag
	ablationMode = *ablationFlag
	localControl = *localFlag
	attributionMode = *attributionFlag
	gatesOnly = *gatesFlag
	browseOnly = *browseFlag
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
	if policy, err := os.ReadFile("research/p0-s/index-policy-dabf44.json"); err == nil {
		ph := sha256.Sum256(policy)
		save(filepath.Join(*out, "index-policy.json"), map[string]any{"ddl_sha256": hash, "policy_sha256": hex.EncodeToString(ph[:]), "policy": json.RawMessage(policy)})
	}
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
						checkpointMu.Lock()
						start := time.Now()
						var busy, log, done int
						e := cc.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &done)
						err := ""
						if e != nil {
							err = e.Error()
						}
						checkpoints = append(checkpoints, map[string]any{"start_ns": start.UnixNano(), "ms": float64(time.Since(start).Nanoseconds()) / 1e6, "busy": busy, "log": log, "checkpointed": done, "error": err})
						checkpointMu.Unlock()
					}
				}
			}()
		}
		seedAndSweep(c, *target, *out, hash)
		if attributionMode {
			omitFTS = *omitFlag
			attribution(c, *out, hash)
		}
		if capsMode {
			capsProduct(c, *out, hash)
		}
		if facetMode {
			facetExperiment(c, *out, hash)
		}
		if frameMode {
			frameExperiment(c, *out, hash)
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
	closeErr := rows.Close()
	if e == nil {
		e = closeErr
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
	plan := []string{}
	pr, pe := c.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q)
	must(pe)
	for pr.Next() {
		var a, b, d int
		var detail string
		must(pr.Scan(&a, &b, &d, &detail))
		plan = append(plan, detail)
	}
	must(pr.Err())
	must(pr.Close())
	result := map[string]any{"samples_ms": good, "refused_ms": bad, "errors": errors, "completed": len(good), "refused": len(bad), "rows": count, "row_hash": hash, "sql": q, "budget_ms": budget.Milliseconds(), "plan": plan}
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
	overlay := ""
	_, e := c.ExecContext(context.Background(), `UPDATE catalog_entities SET admission_own_age=CASE WHEN id%1000=0 THEN 0 WHEN id%1000=1 THEN 8 WHEN id%20=0 THEN NULL ELSE CASE id%3 WHEN 0 THEN 13 WHEN 1 THEN 17 ELSE 18 END END;UPDATE catalog_entities SET admission_allow_age=coalesce(admission_own_age,-1),admission_known_age=admission_own_age;WITH RECURSIVE n(id) AS(VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<30) INSERT INTO catalog_terms(id,vocab,key,label) SELECT id,1,printf('genre-%d',id),printf('Genre%d',id) FROM n;INSERT INTO catalog_entity_terms SELECT e.id,1+((e.id*7+v.column1)%30) FROM catalog_entities e CROSS JOIN (VALUES(0),(1),(2)) v;`)
	must(e)
	_, e = c.ExecContext(context.Background(), ageCountsFill())
	must(e)
	sorts := []string{"sort_key", "added_at", "year", "duration_key", "community_rating_key", "critic_rating_key", "file_size_key", "resolution_key", "release_key", "random_key"}
	var before int64
	must(c.QueryRowContext(context.Background(), "SELECT sum(pgsize) FROM dbstat").Scan(&before))
	results := []any{}
	progress := func() {
		save(filepath.Join(out, "band-facet-progress.json"), map[string]any{"ddl_sha256": hash, "overlay": overlay, "results": results})
	}
	samples := 500
	if os.Getenv("GITHUB_RUN_ID") == "" {
		samples = 2
	}
	budget := 100 * time.Millisecond
	for _, mode := range []int{0, 1} {
		field := "admission_allow_age"
		if mode == 1 {
			field = "admission_known_age"
		}
		for _, ceiling := range []int{8, 17} {
			source := fmt.Sprintf("SELECT t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.library_id=1 AND e.kind=1 AND e.%s IS NOT NULL AND e.%s<=%d GROUP BY t.term_id ORDER BY t.term_id", field, field, ceiling)
			fast := fmt.Sprintf("SELECT term_id,sum(total) FROM catalog_age_facet_counts WHERE library_id=1 AND kind=1 AND block_unrated=%d AND admission_age<=%d GROUP BY term_id HAVING sum(total)>0 ORDER BY term_id", mode, ceiling)
			raw := readCohort(c, source, 1, 120*time.Second)
			quick := readCohort(c, fast, samples, budget)
			if raw["row_hash"] != quick["row_hash"] {
				panic("rating-band count mismatch")
			}
			results = append(results, map[string]any{"case": "facet", "mode": mode, "ceiling": ceiling, "source": raw, "candidate": quick})
		}
	}
	// Canonical age browse: four exact indexes, no overlay.
	for _, mode := range []int{0, 1} {
		field := "admission_allow_age"
		if mode == 1 {
			field = "admission_known_age"
		}
		for _, ceiling := range []int{8, 17} {
			for _, col := range []string{"sort_key", "added_at"} {
				q := bandBrowse(field, ceiling, col, "")
				results = append(results, map[string]any{"case": "browse", "schema_tier": "exact-canonical", "mode": mode, "ceiling": ceiling, "sort": col, "candidate": readCohort(c, q, samples, budget)})
			}
		}
	}
	// Extra eight hot scalar prefixes per fixed frontier are a measured candidate only.
	for _, field := range []string{"admission_allow_age", "admission_known_age"} {
		for _, col := range sorts {
			if col == "sort_key" || col == "added_at" {
				continue
			}
			predicate := "retired=0"
			if col == "resolution_key" {
				predicate += " AND kind IN(1,2,3,4,10)"
			}
			q := fmt.Sprintf("CREATE INDEX restricted_%s_%s ON catalog_entities(library_id,kind,%s,%s,id) WHERE %s", field, col, field, col, predicate)
			_, e = c.ExecContext(context.Background(), q)
			must(e)
			overlay += q + ";"
		}
	}
	for _, mode := range []int{0, 1} {
		field := "admission_allow_age"
		if mode == 1 {
			field = "admission_known_age"
		}
		for _, ceiling := range []int{8, 17} {
			for _, col := range sorts {
				q := bandBrowse(field, ceiling, col, "")
				results = append(results, map[string]any{"case": "browse", "schema_tier": "canonical-plus16candidate-prefixes", "mode": mode, "ceiling": ceiling, "sort": col, "candidate": readCohort(c, q, samples, budget)})
			}
		}
	}
	progress()
	// Arbitrary label sets remain exact anti-joins over source membership, not another user catalogue.
	_, e = c.ExecContext(context.Background(), "INSERT OR IGNORE INTO catalog_attribute_fields VALUES(2,'label'),(3,'tag');")
	must(e)
	for j, div := range []int{11, 23, 47, 97, 193} {
		field := 2
		if j%2 == 1 {
			field = 3
		}
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO catalog_attribute_terms VALUES(%d,%d,'label-%d');INSERT INTO catalog_item_attribute_edges SELECT id,%d,'label-%d',1 FROM catalog_entities WHERE id%%%d=0;", 100001+j, field, j+1, 100001+j, j+1, div))
		must(e)
	}
	fallbackSamples := 21
	if os.Getenv("GITHUB_RUN_ID") == "" {
		fallbackSamples = 2
	}
	for _, mode := range []int{0, 1} {
		field := "admission_allow_age"
		if mode == 1 {
			field = "admission_known_age"
		}
		for _, scope := range []string{"profile-labels", "member-labels-and-tags"} {
			for _, labels := range []string{"'label-1'", "'label-1','label-2','label-3'", "'label-1','label-2','label-3','label-4','label-5'"} {
				fields := "term.field_id=2"
				value := "term.value_key"
				if scope == "member-labels-and-tags" {
					fields = "term.field_id IN(2,3)"
					value = "lower(ae.source_value)"
				}
				exclude := "NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id WHERE ae.item_id=e.id AND " + fields + " AND " + value + " IN(" + labels + "))"
				for _, ceiling := range []int{8, 17} {
					q := fmt.Sprintf("SELECT t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.library_id=1 AND e.kind=1 AND e.retired=0 AND e.%s IS NOT NULL AND e.%s<=%d AND %s GROUP BY t.term_id ORDER BY t.term_id", field, field, ceiling, exclude)
					results = append(results, map[string]any{"case": "label-facet", "mode": mode, "scope": scope, "labels": labels, "ceiling": ceiling, "result": readCohort(c, q, fallbackSamples, budget)})
					reference := map[int]int{}
					ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
					rr, err := c.QueryContext(ctx, q)
					must(err)
					for rr.Next() {
						var term, n int
						must(rr.Scan(&term, &n))
						reference[term] = n
					}
					must(rr.Err())
					must(rr.Close())
					cancel()
					exactRuns := []any{}
					nRuns := 3
					if os.Getenv("GITHUB_RUN_ID") == "" {
						nRuns = 1
					}
					for rep := 0; rep < nRuns; rep++ {
						r := quantizedFacet(c, field, ceiling, exclude)
						if r["error"] == "" {
							counts := r["counts"].(map[int]int)
							if len(counts) != len(reference) {
								panic("quantized facet size mismatch")
							}
							for term, n := range reference {
								if counts[term] != n {
									panic("quantized facet total mismatch")
								}
							}
						}
						exactRuns = append(exactRuns, r)
					}
					results = append(results, map[string]any{"case": "label-facet-quantized", "mode": mode, "scope": scope, "labels": labels, "ceiling": ceiling, "runs": exactRuns})
					progress()
					for _, sortcol := range []string{"sort_key", "added_at", "duration_key", "random_key"} {
						results = append(results, map[string]any{"case": "label-browse", "mode": mode, "scope": scope, "labels": labels, "ceiling": ceiling, "sort": sortcol, "result": readCohort(c, bandBrowse(field, ceiling, sortcol, exclude), fallbackSamples, budget)})
					}
				}
			}
		}
	}
	progress()
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
		must(c.QueryRowContext(context.Background(), fmt.Sprintf("SELECT admission_allow_age FROM catalog_entities WHERE id=%d", item)).Scan(&old))
		next := 0
		if old == 0 {
			next = 8
		}
		sqltext := fmt.Sprintf("UPDATE catalog_entities SET admission_own_age=%d WHERE id=%d;INSERT INTO admin_receipts VALUES('band','%d','%s','applied',1);", next, item, j, strings.Repeat("0", 64))
		var a, b int64
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&a))
		_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		start := time.Now()
		classSQL(c, item, next, sql.NullInt64{Int64: int64(next), Valid: true})
		_, e = c.ExecContext(context.Background(), sqltext)
		must(e)
		_, e = c.ExecContext(context.Background(), "COMMIT")
		must(e)
		writes = append(writes, float64(time.Since(start).Nanoseconds())/1e6)
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&b))
		changes = append(changes, b-a)
	}
	alloc := []any{}
	rows, e := c.QueryContext(context.Background(), "SELECT name,sum(pgsize),sum(payload) FROM dbstat WHERE name LIKE 'restricted_%' OR name LIKE 'catalog_age_%' OR name LIKE 'catalog_entities%age%' GROUP BY name")
	must(e)
	for rows.Next() {
		var name string
		var pages, payload int64
		must(rows.Scan(&name, &pages, &payload))
		alloc = append(alloc, []any{name, pages, payload})
	}
	must(rows.Close())
	agePropagation(c, out, hash)
	oh := sha256.Sum256([]byte(overlay))
	save(filepath.Join(out, "band-facet-results.json"), map[string]any{"base_ddl_sha256": hash, "overlay_sha256": hex.EncodeToString(oh[:]), "overlay": overlay, "results": results, "allocation": alloc, "write_samples_ms": writes, "write_rows": changes, "write_p99_ms": pct(writes, .99), "write_max_ms": pct(writes, 1), "counter_before_index_bytes": before, "boundary": "Exact canonical age counters/four browse prefixes plus explicitly hashed16additional-prefix overlay. Movie/per-item age-only fixed projections; arbitrary labels exact anti-join with100ms SQL deadline. No per-profile materialization. Container age propagation separate."})
}
func bandBrowse(field string, ceiling int, sortcol, exclude string) string {
	parts := []string{}
	for band := -1; band <= 21; band++ {
		if band > ceiling {
			continue
		}
		where := fmt.Sprintf("e.library_id=1 AND e.kind=1 AND e.retired=0 AND e.%s=%d", field, band)
		if sortcol == "resolution_key" {
			where += " AND kind IN(1,2,3,4,10)"
		}
		if exclude != "" {
			where += " AND " + exclude
		}
		parts = append(parts, fmt.Sprintf("SELECT * FROM(SELECT e.id,e.%s AS value FROM catalog_entities e WHERE %s ORDER BY e.%s DESC,e.id DESC LIMIT40)", sortcol, where, sortcol))
	}
	q := "SELECT * FROM(" + strings.Join(parts, " UNION ALL ") + ") ORDER BY value DESC,id DESC LIMIT40"
	return strings.ReplaceAll(q, "LIMIT40", "LIMIT 40")
}
func sourceRows(streams, chapters, book int, tag string) string {
	var q strings.Builder
	for j := 0; j < streams; j++ {
		typ, codec, channels := "audio", "aac", 2
		if j >= 255 && j < 510 {
			typ = "video"
			codec = "h264"
			channels = 0
		}
		if j >= 510 {
			typ = "subtitle"
			codec = "subrip"
			channels = 0
		}
		fmt.Fprintf(&q, "INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,has_asset_stream_breadth) VALUES('asset-1',%d,'%s','%s','en','%s-%d',%d,'stereo',0,0,1);", j, typ, codec, tag, j, channels)
	}
	for j := 0; j < chapters; j++ {
		fmt.Fprintf(&q, "INSERT INTO asset_chapters VALUES('asset-1',%d,'%s-%d',%d,%d);", j, tag, j, j*60, (j+1)*60)
	}
	for j := 0; j < book; j++ {
		fmt.Fprintf(&q, "INSERT INTO catalog_book_chapters(file_id,chapter_index,title,start_seconds,end_seconds) VALUES(1,%d,'%s-%d',%d,%d);", j, tag, j, j*60, (j+1)*60)
	}
	return q.String()
}
func versionRows(n, offset int) string {
	var q strings.Builder
	for j := 0; j < n; j++ {
		fmt.Fprintf(&q, "INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,%d,1);", 10001+offset+j)
	}
	return q.String()
}
func frameExperiment(c *sql.Conn, out, hash string) {
	_, e := c.ExecContext(context.Background(), `INSERT OR IGNORE INTO catalog_kinds VALUES(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,0,1,1);UPDATE catalog_entities SET kind=8,resolution_key=NULL WHERE id=2;UPDATE catalog_entities SET kind=9,parent_id=2,book_id=2,resolution_key=NULL WHERE id=1;INSERT INTO asset_stream_facts VALUES('asset-1',1,1000001,1,'old',1);INSERT INTO asset_chapter_facts VALUES('asset-1',1,1000001,1,'ready','old');`)
	must(e)
	for j := 0; j < 64; j++ {
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(%d,'version-%d','/versions/%d',1,1,'mkv','h264','aac',1920,1080,7200,1)", 10001+j, j, j))
		must(e)
	}
	type fcase struct {
		name        string
		s, ch, b, v int
		mode        string
	}
	cases := []fcase{
		{"765-streams-max-change", 765, 0, 0, 0, "change"},
		{"512-physical-chapters-max-change", 0, 512, 0, 0, "change"},
		{"512-book-chapters-max-change", 0, 0, 512, 0, "change"},
		{"final-mixed-physical-max-change", 765, 512, 0, 0, "change"},
		{"final-combined-max-change", 765, 512, 512, 0, "change"},
		{"final-combined-new-max", 765, 512, 512, 0, "new"},
		{"final-combined-shrink", 765, 512, 512, 0, "shrink"},
		{"final-combined-noop", 765, 512, 512, 0, "noop"},
		{"16-versions-max-change", 0, 0, 0, 16, "change"},
		{"final-combined-plus-16-versions", 765, 512, 512, 16, "change"},
		{"stress-1024-physical-chapters", 0, 1024, 0, 0, "change"},
		{"stress-1024-book-chapters", 0, 0, 1024, 0, "change"},
		{"stress-1024-combined-max-change", 765, 1024, 1024, 0, "change"},
		{"stress-32-versions", 0, 0, 0, 32, "change"},
	}
	streamStmt, e := c.PrepareContext(context.Background(), "INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,has_asset_stream_breadth) VALUES('asset-1',?,?,?,?,?,?,'stereo',0,0,1)")
	must(e)
	defer streamStmt.Close()
	chapterStmt, e := c.PrepareContext(context.Background(), "INSERT INTO asset_chapters VALUES('asset-1',?,?,?,?)")
	must(e)
	defer chapterStmt.Close()
	bookStmt, e := c.PrepareContext(context.Background(), "INSERT INTO catalog_book_chapters(file_id,chapter_index,title,start_seconds,end_seconds) VALUES(1,?,?,?,?)")
	must(e)
	defer bookStmt.Close()
	versionStmt, e := c.PrepareContext(context.Background(), "INSERT INTO catalog_asset_links(entity_id,asset_id,library_id) VALUES(1,?,1)")
	must(e)
	defer versionStmt.Close()
	reps := 101
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	results := []any{}
	for _, f := range cases {
		times := []float64{}
		changed := []int64{}
		oldS, oldC, oldB, oldV := f.s, f.ch, f.b, f.v
		newS, newC, newB, newV := f.s, f.ch, f.b, f.v
		if f.mode == "new" {
			oldS = 0
			oldC = 0
			oldB = 0
			oldV = 0
		}
		if f.mode == "shrink" {
			newS = 3
			newC = 1
			newB = 1
		}
		if f.mode == "noop" {
			newS = 0
			newC = 0
			newB = 0
			newV = 0
		}
		clear := `DELETE FROM asset_streams WHERE asset_id='asset-1';DELETE FROM asset_chapters WHERE asset_id='asset-1';DELETE FROM catalog_book_chapters WHERE file_id=1;DELETE FROM catalog_asset_links WHERE entity_id=1;`
		restore := "BEGIN;" + clear + sourceRows(oldS, oldC, oldB, "old") + versionRows(oldV, 0) + "UPDATE asset_stream_facts SET fingerprint='old' WHERE asset_id='asset-1';COMMIT;"
		body := clear + sourceRows(newS, newC, newB, "new") + versionRows(newV, 32) + `UPDATE asset_stream_facts SET revision=revision+1,fingerprint='new' WHERE asset_id='asset-1';UPDATE asset_chapter_facts SET revision=revision+1,fingerprint='new' WHERE asset_id='asset-1';UPDATE catalog_assets SET info_epoch=info_epoch+1,prepared_source_revision=prepared_source_revision+1 WHERE id=1;UPDATE catalog_entities SET revision=revision+1 WHERE id=1;UPDATE libraries SET data_revision=data_revision+1 WHERE id='full-library';`
		headers := strings.TrimPrefix(body, clear+sourceRows(newS, newC, newB, "new")+versionRows(newV, 32))
		streamData := [][]any{}
		for j := 0; j < newS; j++ {
			typ, codec, ch := "audio", "aac", 2
			if j >= 255 && j < 510 {
				typ = "video"
				codec = "h264"
				ch = 0
			}
			if j >= 510 {
				typ = "subtitle"
				codec = "subrip"
				ch = 0
			}
			streamData = append(streamData, []any{j, typ, codec, "en", fmt.Sprintf("new-%d", j), ch})
		}
		chapterData := [][]any{}
		for j := 0; j < newC; j++ {
			chapterData = append(chapterData, []any{j, fmt.Sprintf("new-%d", j), j * 60, (j + 1) * 60})
		}
		bookData := [][]any{}
		for j := 0; j < newB; j++ {
			bookData = append(bookData, []any{j, fmt.Sprintf("new-%d", j), j * 60, (j + 1) * 60})
		}
		if f.mode == "noop" {
			headers = ""
		}
		for j := 0; j < reps; j++ {
			_, e = c.ExecContext(context.Background(), restore)
			must(e)
			var a, b int64
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&a))
			_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			start := time.Now()
			var fp string
			must(c.QueryRowContext(context.Background(), "SELECT fingerprint FROM asset_stream_facts WHERE asset_id='asset-1'").Scan(&fp))
			if fp != "old" {
				panic("source fingerprint mismatch")
			}
			if f.mode != "noop" {
				_, e = c.ExecContext(context.Background(), clear)
				must(e)
				for _, row := range streamData {
					_, e = streamStmt.ExecContext(context.Background(), row...)
					must(e)
				}
				for _, row := range chapterData {
					_, e = chapterStmt.ExecContext(context.Background(), row...)
					must(e)
				}
				for _, row := range bookData {
					_, e = bookStmt.ExecContext(context.Background(), row...)
					must(e)
				}
				for j := 0; j < newV; j++ {
					_, e = versionStmt.ExecContext(context.Background(), 10033+j)
					must(e)
				}
			}
			q := headers + fmt.Sprintf("INSERT INTO admin_receipts VALUES('source-frame','%s-%d','%s','applied',1);", f.name, j, strings.Repeat("0", 64))
			if f.mode != "noop" {
				q += "INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','asset','asset-1',1);"
			}
			_, e = c.ExecContext(context.Background(), q)
			must(e)
			_, e = c.ExecContext(context.Background(), "COMMIT")
			must(e)
			times = append(times, float64(time.Since(start).Nanoseconds())/1e6)
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&b))
			changed = append(changed, b-a)
		}
		effects := oldS + oldC + oldB + oldV + newS + newC + newB + newV
		if f.mode == "noop" {
			effects = 0
		}
		results = append(results, map[string]any{"case": f.name, "old_child_rows": oldS + oldC + oldB + oldV, "new_child_rows": newS + newC + newB + newV, "expected_child_mutations": effects, "samples_ms": times, "rows_changed": changed, "p99_ms": pct(times, .99), "max_ms": pct(times, 1), "strict": pct(times, .99) <= 10 && pct(times, 1) <= 25})
		save(filepath.Join(out, "source-frame-results.json"), map[string]any{"ddl_sha256": hash, "synchronous": "NORMAL", "autocheckpoint": 0, "insert_execution": "four reusable typed prepared statements; sealed row arguments prepared before writer acquisition", "results": results, "boundary": "Target attempted atomic whole-set replacement with folded breadth, small bounded scalar/string payloads; source header checks/revisions/receipt/FULL set delta/NORMAL COMMIT included. Current production sealed-set materialization and later naming are separate/bounded commits; these measurements do not assert current whole replacement is one commit. Not a complete private typed command or staged-head implementation."})
		fmt.Println("source frame", f.name, pct(times, .99), pct(times, 1))
	}
}

func quantizedFacet(c *sql.Conn, field string, ceiling int, exclude string) map[string]any {
	start := time.Now()
	counts := map[int]int{}
	quanta := []float64{}
	var failure string
	rawRows := 0
	_, e := c.ExecContext(context.Background(), "BEGIN")
	must(e)
	for band := -1; band <= 21; band++ {
		if band > ceiling {
			continue
		}
		key := ""
		after := int64(0)
		for {
			if time.Since(start) > 10*time.Second {
				failure = "cold preparation budget10s"
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			t := time.Now()
			q := fmt.Sprintf("WITH raw AS MATERIALIZED(SELECT id,sort_key FROM catalog_entities INDEXED BY %s WHERE library_id=1 AND kind=1 AND retired=0 AND %s=%d AND (sort_key,id)>(?,?) ORDER BY sort_key,id LIMIT128) SELECT r.id,r.sort_key,CASE WHEN %s THEN1 ELSE0 END,t.term_id FROM raw r CROSS JOIN catalog_entities e ON e.id=r.id LEFT JOIN catalog_entity_terms t ON t.entity_id=r.id ORDER BY r.sort_key,r.id,t.term_id", ageIndex(field, "sort_key"), field, band, exclude)
			q = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(q, "LIMIT128", "LIMIT 128"), "THEN1", "THEN 1"), "ELSE0", "ELSE 0")
			rows, err := c.QueryContext(ctx, q, key, after)
			n := 0
			nRaw := 0
			lastID := after
			lastKey := key
			if err == nil {
				for rows.Next() {
					var id int64
					var k string
					var visible bool
					var term sql.NullInt64
					if err = rows.Scan(&id, &k, &visible, &term); err != nil {
						break
					}
					if visible && term.Valid {
						counts[int(term.Int64)]++
					}
					if id != lastID {
						nRaw++
					}
					lastID = id
					lastKey = k
					n++
				}
				if err == nil {
					err = rows.Err()
				}
				closeErr := rows.Close()
				if err == nil {
					err = closeErr
				}
			}
			cancel()
			quanta = append(quanta, float64(time.Since(t).Nanoseconds())/1e6)
			if err != nil {
				failure = err.Error()
				break
			}
			rawRows += n
			key = lastKey
			after = lastID
			if nRaw < 128 {
				break
			}
		}
		if failure != "" {
			break
		}
	}
	_, e = c.ExecContext(context.Background(), "COMMIT")
	must(e)
	if failure != "" {
		counts = nil
	}
	result := map[string]any{"completion_ms": float64(time.Since(start).Nanoseconds()) / 1e6, "quantum_ms": quanta, "error": failure, "counts": counts, "source_term_rows_visited": rawRows, "snapshot": "one source read transaction, query-local30 counters only; no stored permitted ID set or profile/user projection", "budget_ms": 100, "cold_budget_ms": 10000}
	if len(quanta) > 0 {
		result["quantum_p95_ms"] = pct(quanta, .95)
		result["quantum_max_ms"] = pct(quanta, 1)
	}
	return result
}

func statementPhase(q string) string {
	for _, p := range []struct{ needle, name string }{{"INSERT INTO catalog_subjects", "subject"}, {"INSERT INTO catalog_identities", "identity-key"}, {"INSERT INTO catalog_entities", "entity-indexes"}, {"INSERT INTO catalog_assets", "asset-indexes"}, {"INSERT INTO catalog_asset_links", "link-indexes"}, {"INSERT INTO search_own_prefix", "fts"}, {"UPDATE libraries", "library-revision"}, {"INSERT INTO api_events", "event"}, {"INSERT INTO catalog_age_item_counts", "age-counter"}, {"INSERT INTO admin_receipts", "receipt"}} {
		if strings.Contains(q, p.needle) {
			return p.name
		}
	}
	panic("unclassified acceptance statement: " + q)
}
func attribution(c *sql.Conn, out, hash string) {
	var resident int
	must(c.QueryRowContext(context.Background(), "SELECT max(id) FROM catalog_entities").Scan(&resident))
	sizes := []int{1, 2, 4, 8, 16, 32, 64, 128, 256, 1000}
	runs := 128
	if localControl {
		sizes = []int{1, 2, 4, 8, 16, 32, 64, 128}
	} else if os.Getenv("GITHUB_RUN_ID") == "" {
		sizes = []int{1, 2}
		runs = 2
	}
	depths := map[string]int64{}
	dr, de := c.QueryContext(context.Background(), "SELECT name,max(length(path)-length(replace(path,'/','')))-1 FROM dbstat WHERE name='catalog_entities' OR name IN(SELECT name FROM sqlite_schema WHERE type='index' AND tbl_name IN('catalog_entities','catalog_assets','catalog_subjects')) GROUP BY name")
	must(de)
	for dr.Next() {
		var name string
		var depth int64
		must(dr.Scan(&name, &depth))
		depths[name] = depth
	}
	must(dr.Err())
	must(dr.Close())
	save(filepath.Join(out, "initial-btree-depths.json"), map[string]any{"ddl_sha256": hash, "residents": resident, "depth_edges": depths, "boundary": "Root depth0, max root-to-leaf edges; collected before timed writer cohorts"})
	if ablationMode {
		sizes = []int{8}
		runs = 501
	}
	results := []any{}
	measure := func(batch, reps int, control string) {
		times := []float64{}
		samples := []any{}
		phaseSamples := map[string][]float64{}
		tails := []any{}
		totalStart := time.Now()
		for rep := 0; rep < reps; rep++ {
			q := acceptanceSQL(resident+1, batch)
			statements := strings.Split(q, ";")
			_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			start := time.Now()
			phases := []any{}
			for _, stmt := range statements {
				if strings.TrimSpace(stmt) == "" {
					continue
				}
				name := statementPhase(stmt)
				t := time.Now()
				_, e = c.ExecContext(context.Background(), stmt)
				must(e)
				ms := float64(time.Since(t).Nanoseconds()) / 1e6
				phaseSamples[name] = append(phaseSamples[name], ms)
				phases = append(phases, map[string]any{"phase": name, "start_ns": t.UnixNano(), "ms": ms})
			}
			t := time.Now()
			_, e = c.ExecContext(context.Background(), "COMMIT")
			must(e)
			commit := float64(time.Since(t).Nanoseconds()) / 1e6
			hold := float64(time.Since(start).Nanoseconds()) / 1e6
			phases = append(phases, map[string]any{"phase": "commit", "start_ns": t.UnixNano(), "ms": commit})
			phaseSamples["commit"] = append(phaseSamples["commit"], commit)
			row := map[string]any{"rep": rep, "start_ns": start.UnixNano(), "hold_ms": hold, "phases": phases}
			samples = append(samples, row)
			times = append(times, hold)
			if hold > 25 {
				tails = append(tails, row)
			}
			resident += batch
		}
		summaries := map[string]any{}
		for phase, xs := range phaseSamples {
			summaries[phase] = map[string]any{"p50_ms": pct(xs, .5), "p99_ms": pct(xs, .99), "max_ms": pct(xs, 1)}
		}
		row := map[string]any{"ddl_sha256": hash, "control": control, "batch": batch, "n": reps, "p50_ms": pct(times, .5), "p95_ms": pct(times, .95), "p99_ms": pct(times, .99), "max_ms": pct(times, 1), "strict": pct(times, .99) <= 10 && pct(times, 1) <= 25, "max50": pct(times, 1) <= 50, "entities_per_s": float64(batch*reps) / time.Since(totalStart).Seconds(), "phase_summary": summaries, "samples": samples, "holds_over25_ms": tails}
		results = append(results, row)
		save(filepath.Join(out, "writer-attribution.json"), map[string]any{"ddl_sha256": hash, "fts_excluded": omitFTS, "results": results, "boundary": "One hosted benchmark job per VM, no concurrent child probes. NORMAL +PASSIVE100ms unless checkpoint-paused. Each statement separate ExecContext: parse/driver/index work charged to that statement, FTS deferred work may appear in COMMIT. Timestamps permit checkpoint overlap analysis; host scheduler/GC/IO are not individually attributed. No claim quiet physical host."})
		fmt.Println("attribution", batch, control, pct(times, .99), pct(times, 1))
	}
	for _, size := range sizes {
		measure(size, runs, "normal-passive")
	}
	if !ablationMode {
		checkpointMu.Lock()
		measure(1, runs, "checkpoint-paused")
		checkpointMu.Unlock()
	}
	// Snapshot index btree entries around one complete Basic, outside writer timing.
	snapshot := func() map[string][3]int64 {
		rows, e := c.QueryContext(context.Background(), "SELECT name,sum(ncell),sum(pgsize),max(length(path)-length(replace(path,'/','')))-1 FROM dbstat GROUP BY name")
		must(e)
		data := map[string][3]int64{}
		for rows.Next() {
			var name string
			var cells, bytes, depth int64
			must(rows.Scan(&name, &cells, &bytes, &depth))
			data[name] = [3]int64{cells, bytes, depth}
		}
		must(rows.Err())
		must(rows.Close())
		return data
	}
	before := snapshot()
	var a, b int64
	must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&a))
	_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE;"+acceptanceSQL(resident+1, 1)+"COMMIT;")
	must(e)
	must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&b))
	after := snapshot()
	indexes := []any{}
	rows, e := c.QueryContext(context.Background(), "SELECT name,tbl_name,coalesce(sql,'') FROM sqlite_schema WHERE type='index' ORDER BY name")
	must(e)
	for rows.Next() {
		var name, table, ddl string
		must(rows.Scan(&name, &table, &ddl))
		old, new := before[name], after[name]
		if new[0] != old[0] || table == "catalog_entities" || table == "catalog_subjects" || table == "catalog_assets" || table == "catalog_asset_links" {
			indexes = append(indexes, map[string]any{"name": name, "table": table, "sql": ddl, "entries_before": old[0], "entries_after": new[0], "entry_delta": new[0] - old[0], "bytes_after": new[1], "byte_delta": new[1] - old[1]})
		}
	}
	must(rows.Err())
	must(rows.Close())
	save(filepath.Join(out, "basic-index-footprint.json"), map[string]any{"ddl_sha256": hash, "resident_before": resident, "native_total_changes_delta": b - a, "indexes": indexes, "objects_before": before, "objects_after": after, "basic_sql": acceptanceSQL(resident+1, 1), "boundary": "dbstat btree cells delta around one complete Basic; ordinary index entries include implicit indexes. FTS uses compressed postings/segment blocks, not fixed1posting=1btree-cell. Net btree count does not count delete+insert rewrites of existing library/count records. No scalar membership contributions for a standalone movie."})
}

func ageIndex(field, col string) string {
	prefix := "catalog_entities_age_"
	if field == "admission_known_age" {
		prefix = "catalog_entities_known_age_"
	}
	if col == "sort_key" {
		return prefix + "title"
	}
	if col == "added_at" {
		return prefix + "added"
	}
	return "restricted_" + field + "_" + col
}
func ageCountsFill() string {
	q := "DELETE FROM catalog_age_item_counts;DELETE FROM catalog_age_facet_counts;"
	for mode, field := range []string{"admission_allow_age", "admission_known_age"} {
		q += fmt.Sprintf("INSERT INTO catalog_age_item_counts SELECT library_id,kind,%d,%s,count(*) FROM catalog_entities WHERE retired=0 AND %s IS NOT NULL GROUP BY library_id,kind,%s;", mode, field, field, field)
		q += fmt.Sprintf("INSERT INTO catalog_age_facet_counts SELECT e.library_id,e.kind,%d,e.%s,t.term_id,count(*) FROM catalog_entities e JOIN catalog_entity_terms t ON t.entity_id=e.id WHERE e.retired=0 AND e.%s IS NOT NULL GROUP BY e.library_id,e.kind,e.%s,t.term_id;", mode, field, field, field)
	}
	return q
}
func classSQL(c *sql.Conn, item, allow int, known sql.NullInt64) {
	var lib, kind int
	var oldAllow, oldKnown sql.NullInt64
	must(c.QueryRowContext(context.Background(), "SELECT library_id,kind,admission_allow_age,admission_known_age FROM catalog_entities WHERE id=?", item).Scan(&lib, &kind, &oldAllow, &oldKnown))
	olds := []sql.NullInt64{oldAllow, oldKnown}
	news := []sql.NullInt64{{Int64: int64(allow), Valid: true}, known}
	q := ""
	for mode := 0; mode < 2; mode++ {
		if olds[mode] == news[mode] {
			continue
		}
		if olds[mode].Valid {
			q += fmt.Sprintf("UPDATE catalog_age_item_counts SET total=total-1 WHERE library_id=%d AND kind=%d AND block_unrated=%d AND admission_age=%d;UPDATE catalog_age_facet_counts SET total=total-1 WHERE library_id=%d AND kind=%d AND block_unrated=%d AND admission_age=%d AND term_id IN(SELECT term_id FROM catalog_entity_terms WHERE entity_id=%d);", lib, kind, mode, olds[mode].Int64, lib, kind, mode, olds[mode].Int64, item)
		}
		if news[mode].Valid {
			q += fmt.Sprintf("INSERT INTO catalog_age_item_counts VALUES(%d,%d,%d,%d,1) ON CONFLICT(library_id,kind,block_unrated,admission_age) DO UPDATE SET total=total+1;INSERT INTO catalog_age_facet_counts SELECT %d,%d,%d,%d,term_id,1 FROM catalog_entity_terms WHERE entity_id=%d ON CONFLICT(library_id,kind,block_unrated,admission_age,term_id) DO UPDATE SET total=total+1;", lib, kind, mode, news[mode].Int64, lib, kind, mode, news[mode].Int64, item)
		}
	}
	v := "NULL"
	if known.Valid {
		v = fmt.Sprint(known.Int64)
	}
	q += fmt.Sprintf("UPDATE catalog_entities SET admission_allow_age=%d,admission_known_age=%s,admission_revision=admission_revision+1,revision=revision+1 WHERE id=%d;", allow, v, item)
	_, e := c.ExecContext(context.Background(), q)
	must(e)
}
func agePropagation(c *sql.Conn, out, hash string) {
	var n int
	must(c.QueryRowContext(context.Background(), "SELECT max(id) FROM catalog_entities").Scan(&n))
	show, season, collection := n+1, n+2, n+3
	_, e := c.ExecContext(context.Background(), `INSERT OR IGNORE INTO catalog_kinds VALUES(2,'show',0,1,1,1,0),(3,'season',0,1,0,1,0),(4,'episode',1,0,1,1,0),(10,'collection',0,1,1,1,0);`)
	must(e)
	for j, kind := range []int{2, 3, 10} {
		id := n + 1 + j
		extra, values := "", ""
		if kind == 3 {
			extra = ",show_id,season_number,parent_id"
			values = fmt.Sprintf(",%d,1,%d", show, show)
		}
		q := fmt.Sprintf("INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(%d,CAST(printf('%%016d',%d) AS BLOB),1,%d);INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,admission_member_count,admission_min_known_age,admission_allow_age,admission_known_age%s) VALUES(%d,CAST(printf('%%016d',%d) AS BLOB),1,%d,'Container','container','c','container','c',%d,randomblob(16),randomblob(16),21,100,0,0,0%s);INSERT INTO catalog_entity_terms SELECT %d,1+((%d*7+v.column1)%%30) FROM (VALUES(0),(1),(2)) v;", id, id, kind, extra, id, id, kind, id, values, id, id)
		_, e = c.ExecContext(context.Background(), q)
		must(e)
	}
	// FK subject kind is updated before entities under deferred FK check inside setup transaction.
	_, e = c.ExecContext(context.Background(), fmt.Sprintf("BEGIN;PRAGMA defer_foreign_keys=ON;UPDATE catalog_subjects SET kind=4 WHERE id<=100;UPDATE catalog_entities SET kind=4,parent_id=%d,show_id=%d,season_id=%d,numbering='seasonal',episode_number=id,admission_own_age=8,admission_allow_age=8,admission_known_age=8 WHERE id<=100;UPDATE catalog_entities SET admission_own_age=0,admission_allow_age=0,admission_known_age=0 WHERE id=1;COMMIT;", season, show, season))
	must(e)
	// Current browse membership is flattened to actual items. Each ancestor has direct leaf contributions.
	for _, parent := range []int{season, show, collection} {
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,admission_age) SELECT 1,%d,id,0,revision,id,admission_own_age FROM catalog_entities WHERE id<=100;", parent))
		must(e)
	}
	_, e = c.ExecContext(context.Background(), ageCountsFill())
	must(e)
	move := func(age int) {
		classSQL(c, 1, age, sql.NullInt64{Int64: int64(age), Valid: true})
		_, e = c.ExecContext(context.Background(), "UPDATE catalog_entities SET admission_own_age=? WHERE id=1;", age)
		must(e)
		_, e = c.ExecContext(context.Background(), "UPDATE catalog_scalar_contributions SET admission_age=?,contributor_revision=(SELECT revision FROM catalog_entities WHERE id=1) WHERE contributor_id=1;", age)
		must(e)
		for _, parent := range []int{season, show, collection} {
			var known sql.NullInt64
			must(c.QueryRowContext(context.Background(), "SELECT (SELECT admission_age FROM catalog_scalar_contributions WHERE parent_id=? AND admission_age IS NOT NULL ORDER BY admission_age,contributor_id,source LIMIT 1)", parent).Scan(&known))
			a := -1
			if known.Valid {
				a = int(known.Int64)
			}
			classSQL(c, parent, a, known)
			_, e = c.ExecContext(context.Background(), "UPDATE catalog_entities SET admission_min_known_age=? WHERE id=?", known, parent)
			must(e)
		}
	}
	times := []float64{}
	effects := []int64{}
	reps := 101
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	for j := 0; j < reps; j++ {
		var before, after int64
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&before))
		_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		start := time.Now()
		move(18)
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("UPDATE libraries SET data_revision=data_revision+1 WHERE id='full-library';INSERT INTO admin_receipts VALUES('age','%d','%s','applied',1);", j, strings.Repeat("0", 64)))
		must(e)
		_, e = c.ExecContext(context.Background(), "COMMIT")
		must(e)
		times = append(times, float64(time.Since(start).Nanoseconds())/1e6)
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&after))
		effects = append(effects, after-before)
		for _, parent := range []int{season, show, collection} {
			var a int
			must(c.QueryRowContext(context.Background(), "SELECT admission_allow_age FROM catalog_entities WHERE id=?", parent).Scan(&a))
			if a != 8 {
				panic("age minimum propagation drift")
			}
		}
		_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		move(0)
		_, e = c.ExecContext(context.Background(), "COMMIT")
		must(e)
	}
	save(filepath.Join(out, "age-propagation.json"), map[string]any{"ddl_sha256": hash, "samples_ms": times, "rows_changed": effects, "p50_ms": pct(times, .5), "p99_ms": pct(times, .99), "max_ms": pct(times, 1), "boundary": "Canonical contributions admission_age with exact nonNULL predicate; flattened100leaf membership per season/show/collection. One leaf0->18 moves all3parent minima0->8. Canonical item/facet count deltas and4canonical+16candidate age indexes, source/entity revisions, library revision,receipt,NORMAL commit. Own container age21 ignored. Empty containers bothfrontiers=-1; unknown-only nonempty allow=-1/knownNULL. No per-profile/user state."})
}

func capsProduct(c *sql.Conn, out, hash string) {
	children, parents, reps := 255, 100, 101
	if os.Getenv("GITHUB_RUN_ID") == "" {
		children, parents, reps = 3, 2, 2
	}
	endChild := children + 1
	startParent := endChild + 1
	endParent := endChild + parents
	setup := fmt.Sprintf(`INSERT OR IGNORE INTO catalog_kinds VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(10,'collection',0,1,1,1,0);
 BEGIN;PRAGMA defer_foreign_keys=ON;
 UPDATE catalog_subjects SET kind=5 WHERE id=998;UPDATE catalog_subjects SET kind=6 WHERE id=999;UPDATE catalog_entities SET kind=5,resolution_key=NULL WHERE id=998;UPDATE catalog_entities SET kind=6,artist_id=998,resolution_key=NULL WHERE id=999;
 UPDATE catalog_subjects SET kind=7 WHERE id<=%d;UPDATE catalog_subjects SET kind=10 WHERE id BETWEEN %d AND %d;
 UPDATE catalog_entities SET kind=7,album_id=999,artist_id=998,resolution_key=NULL,file_size_key=1000001 WHERE id<=%d;
 UPDATE catalog_entities SET kind=10,collection_name_key=printf('collection-%%d',id),file_size_key=1000001,admission_member_count=%d,admission_unrated_members=%d WHERE id BETWEEN %d AND %d;
 DELETE FROM catalog_asset_links WHERE entity_id BETWEEN 2 AND %d;
 INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,cue_ordinal,start_seconds,end_seconds,info_duration) SELECT 1,id,1,id-1,(id-2)*100.0,(id-1)*100.0,100 FROM catalog_entities WHERE id BETWEEN 2 AND %d;
 WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<%d)
 INSERT INTO catalog_collection_members SELECT %d+(i-1)%%%d,2+(i-1)/%d,printf('%%09d',i) FROM n;
 INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,file_size)
 SELECT 1,collection_id,item_id,8,1,1,1000001 FROM catalog_collection_members;COMMIT;`, endChild, startParent, endParent, endChild, children, children, startParent, endParent, endChild, endChild, children*parents, startParent, parents, parents)
	_, e := c.ExecContext(context.Background(), setup)
	must(e)
	_, e = c.ExecContext(context.Background(), ageCountsFill())
	must(e)
	var n int
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_scalar_contributions").Scan(&n))
	if n != children*parents {
		panic("CAPS membership count drift")
	}
	overlay := `CREATE TABLE catalog_collection_asset_sizes(library_id INTEGER NOT NULL,parent_id INTEGER NOT NULL,asset_id INTEGER NOT NULL,asset_revision INTEGER NOT NULL,file_size INTEGER NOT NULL,PRIMARY KEY(parent_id,asset_id),FOREIGN KEY(library_id,parent_id) REFERENCES catalog_entities(library_id,id),FOREIGN KEY(library_id,asset_id) REFERENCES catalog_assets(library_id,id)) WITHOUT ROWID,STRICT;
 CREATE INDEX collection_asset_size_extreme ON catalog_collection_asset_sizes(parent_id,file_size DESC,asset_id);
 CREATE INDEX collection_asset_size_reverse ON catalog_collection_asset_sizes(asset_id,parent_id);`
	body := func(size int, collapsed bool) string {
		q := fmt.Sprintf("UPDATE catalog_assets SET size=%d,info_epoch=info_epoch+1,playback_origin_revision=playback_origin_revision+1 WHERE id=1;UPDATE catalog_entities SET file_size_key=%d,revision=revision+1,playback_origin_revision=playback_origin_revision+1 WHERE id<=%d;", size, size, endChild)
		if collapsed {
			q += fmt.Sprintf("UPDATE catalog_collection_asset_sizes SET file_size=%d,asset_revision=(SELECT info_epoch FROM catalog_assets WHERE id=1) WHERE asset_id=1;UPDATE catalog_entities SET file_size_key=(SELECT file_size FROM catalog_collection_asset_sizes WHERE parent_id=catalog_entities.id ORDER BY file_size DESC,asset_id LIMIT 1),revision=revision+1 WHERE id BETWEEN %d AND %d;", size, startParent, endParent)
		} else {
			q += fmt.Sprintf("UPDATE catalog_scalar_contributions SET file_size=%d,contributor_revision=(SELECT revision FROM catalog_entities WHERE id=contributor_id) WHERE contributor_id BETWEEN 2 AND %d;UPDATE catalog_entities SET file_size_key=(SELECT file_size FROM catalog_scalar_contributions WHERE parent_id=catalog_entities.id ORDER BY file_size DESC,contributor_id,source LIMIT 1),revision=revision+1 WHERE id BETWEEN %d AND %d;", size, endChild, startParent, endParent)
		}
		return q
	}
	measure := func(collapsed bool, count int) map[string]any {
		times := []float64{}
		changes := []int64{}
		for rep := 0; rep < count; rep++ {
			size := 2000001 + rep
			scope := "caps-inline"
			if collapsed {
				scope = "caps-collapsed"
			}
			q := body(size, collapsed) + fmt.Sprintf("UPDATE libraries SET data_revision=data_revision+1 WHERE id='full-library';INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','library','full-library',1);INSERT INTO admin_receipts VALUES('%s','%d','%s','applied',1);", scope, rep, strings.Repeat("0", 64))
			var before, after int64
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&before))
			_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			start := time.Now()
			var epoch int
			must(c.QueryRowContext(context.Background(), "SELECT info_epoch FROM catalog_assets WHERE id=1").Scan(&epoch))
			_, e = c.ExecContext(context.Background(), q)
			must(e)
			_, e = c.ExecContext(context.Background(), "COMMIT")
			must(e)
			times = append(times, float64(time.Since(start).Nanoseconds())/1e6)
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&after))
			changes = append(changes, after-before)
			var ok int
			must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities WHERE id BETWEEN ? AND ? AND file_size_key=?", startParent, endParent, size).Scan(&ok))
			if ok != parents {
				panic("CAPS parent scalar drift")
			}
		}
		return map[string]any{"collapsed": collapsed, "n": count, "samples_ms": times, "p50_ms": pct(times, .5), "p99_ms": pct(times, .99), "max_ms": pct(times, 1), "rows_changed": changes, "sql": body(2000001, collapsed)}
	}
	baseline := measure(false, 1)
	_, e = c.ExecContext(context.Background(), overlay)
	must(e)
	_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO catalog_collection_asset_sizes SELECT 1,id,1,(SELECT info_epoch FROM catalog_assets WHERE id=1),(SELECT size FROM catalog_assets WHERE id=1) FROM catalog_entities WHERE id BETWEEN %d AND %d;UPDATE catalog_scalar_contributions SET file_size=0;", startParent, endParent))
	must(e)
	candidate := measure(true, reps)
	oh := sha256.Sum256([]byte(overlay))
	save(filepath.Join(out, "caps-product.json"), map[string]any{"ddl_sha256": hash, "cue_children": children, "shared_collections": parents, "baseline_contribution_rows": children * parents, "collapsed_rows": parents, "baseline_single_inline": baseline, "candidate": candidate, "overlay": overlay, "overlay_sha256": hex.EncodeToString(oh[:]), "boundary": "All children share same100authored collections; per(collection,asset) stores size only, other child metrics remain per-child. Current-source asset+owner/CUE entity keys+parentkeys+libraryrevision/event/receipt/NORMAL COMMIT included. Candidate does NOT advance25500unaffected child-tuple revision witnesses: INT must select field-scoped revision semantics, otherwise broad contributor_revision rewrites erase collapse benefit. Focused collection propagation; album/artist hierarchy publication is not charged. No claim this overlay is canonical, complete typed writer or arbitrary disjoint-collection collapse. No budget sweep, chunking or local qualification."})
}
