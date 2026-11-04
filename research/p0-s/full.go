// Temporary full-DDL connection and schema-memory companion. Never product code.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
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
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func save(p string, v any) {
	if m, ok := v.(map[string]any); ok {
		m["engine"] = "Portico bundled modernc SQLite3.53.4"
	}
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(p+".tmp", b, 0644))
	must(os.Rename(p+".tmp", p))
}

var normalWrites, writeOnly, facetMode, frameMode, gatesOnly, browseOnly, admissionSchema, attributionMode, localControl, identitySchema, omitFTS, ablationMode, capsMode bool
var qualificationMode, scalarMode, diagnosticMode, cueMode, enrichMode bool
var traceIO, boundedWAL bool
var checkpointEvery = 50 * time.Millisecond
var walGovernor *walControl
var pagerCacheKiB = 16384
var mmapBytes int64

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
	settings += fmt.Sprintf("PRAGMA cache_size=-%d;PRAGMA mmap_size=%d;", pagerCacheKiB, mmapBytes)
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
var foldedTitles, decoupledSchema bool
var ddlPathForChildren, dbPathForChildren string

func acceptanceSQL(start, count int) string {
	if decoupledSchema {
		return sourceAcceptanceSQL(start, count)
	}
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
	if scalarMode {
		q = strings.ReplaceAll(q, "7200+id%1000", "id")
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
	must(c.QueryRowContext(context.Background(), "SELECT count(*)>0 FROM sqlite_schema WHERE name='search_publish_jobs' AND type='table'").Scan(&decoupledSchema))
	must(c.QueryRowContext(context.Background(), "SELECT count(*)>0 FROM pragma_table_info('catalog_identities') WHERE name='library_id'").Scan(&identitySchema))
	must(c.QueryRowContext(context.Background(), "SELECT count(*)>0 FROM pragma_table_info('catalog_entities') WHERE name='admission_allow_age'").Scan(&admissionSchema))
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&n))
	if n == 0 {
		q := `INSERT INTO libraries(id,name,kind,root) VALUES('full-library','Full fixture','movie','/synthetic/full');INSERT INTO catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1); INSERT OR IGNORE INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0);`
		if foldedTitles {
			q = strings.Replace(q, "catalog_libraries(id,library_id,root,name,kind) VALUES(1,'full-library','/synthetic/full','Full fixture',1)", "catalog_libraries(id,library_id,root) VALUES(1,'full-library','/synthetic/full')", 1)
		}
		if decoupledSchema {
			q += "INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'v1','0b32cf6f04014d1d81310ff7c1ac9066998937c585b7d87d796cd6cff00e1340','SQLite3.53.4','publisher-not-run','active',1);"
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
		admitWAL()
		_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE;"+acceptanceSQL(n+1, count)+"COMMIT;")
		must(e)
		observeWALCommit()
		batches = append(batches, map[string]any{"items": count, "ms": float64(time.Since(t).Nanoseconds()) / 1e6})
		n += count
		if n%100000 == 0 {
			save(filepath.Join(out, "full-seed-progress.json"), map[string]any{"ddl_sha256": hash, "entities": n, "batches": batches})
			fmt.Println("full seed", n)
		}
	}
	shape := "Basic movie acceptance with same-transaction FTS; no provider metadata/staging/scanner IO/full production typed command."
	if decoupledSchema {
		shape = "Basic movie source acceptance with subject+identity+entity+asset+link+search input job/generation+age count+library revision/event/receipt; NO FTS or published search image. No provider metadata/staging/scanner IO/full production typed command."
	}
	save(filepath.Join(out, "full-seed.json"), map[string]any{"ddl_sha256": hash, "entities": n, "batches": batches, "decoupled_search": decoupledSchema, "shape": shape})
	if facetMode || frameMode || gatesOnly || attributionMode || capsMode || scalarMode || cueMode || enrichMode {
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
					if resident < 128 && page == "random-keyset" {
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
						q := "SELECT " + projection + " FROM catalog_entities WHERE " + where + " ORDER BY " + col + " " + direction + ",id " + direction + " LIMIT 40"
						if page == "random-keyset" {
							anchor := resident/5 + ((j+21)*104729)%(resident*3/5)
							var value any
							must(c.QueryRowContext(context.Background(), fmt.Sprintf("SELECT %s FROM catalog_entities WHERE id=%d", col, anchor)).Scan(&value))
							// Cursor already carries the scalar and id; this fixture lookup is outside page timing.
							q = seekPage(where, col, direction, projection, anchor, value)
							if os.Getenv("GITHUB_RUN_ID") == "" && value != nil {
								op := ">"
								if direction == "DESC" {
									op = "<"
								}
								ref := fmt.Sprintf("SELECT %s FROM catalog_entities WHERE %s AND (%s,id)%s(%s,%d) ORDER BY %s %s,id %s LIMIT 40", projection, where, col, op, sqlLiteral(value), anchor, col, direction, direction)
								_, rn, rh, re := readSample(c, ref, time.Second)
								_, qn, qh, qe := readSample(c, q, time.Second)
								if re != "" || qe != "" || rn != qn || rh != qh {
									panic("bounded keyset equivalence failure")
								}
							}
						}
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
	traceFlag := flag.Bool("trace-io", false, "attach strace after fixture creation; diagnostics only")
	boundedFlag := flag.Bool("bounded-wal", false, "192MiB hard WAL reserve, pre-writer admission and250ms bounded title readers")
	checkpointFlag := flag.Int("checkpoint-ms", 50, "bounded PASSIVE cadence,50or250ms")
	diagnosticFlag := flag.Bool("diagnostic", false, "diagnostic-only128holds at1and16; no qualification")
	cacheFlag := flag.Int("cache-kib", 16384, "pager cache KiB/connection")
	mmapFlag := flag.Int64("mmap-bytes", 0, "requested mmap bytes; actual pragma recorded")
	scalarFlag := flag.Bool("scalar-primitives", false, "bundled complete-DDL extrema and reverse10/100")
	cueFlag := flag.Bool("cue-primitives", false, "bundled255CUE/8episode change/delete plusasset-keyed100collections")
	enrichFlag := flag.Bool("enrichment", false, "whole150credit max-to-max enrichment replacement, existing/newpeople")
	cpuFlag := flag.Bool("cpu-profile", false, "sampled CPU attribution with native_sql labels; diagnostic scope")
	qualificationFlag := flag.Bool("qualification", false, "pre-registered1000holds/size, host witnesses, sizes1–128")
	capsFlag := flag.Bool("caps-product", false, "shared100collections/255CUE size contribution collapse")
	omitFlag := flag.Bool("fts-excluded", false, "diagnostic ONLY omitFTS during measured cohort, never seed")
	ablationFlag := flag.Bool("ablation", false, "one local batch8FTS control,501tx")
	localFlag := flag.Bool("local-control", false, "authorized50k quiet-machine control; maximum82769 residents")
	attributionFlag := flag.Bool("attribution", false, "small batch and per-statement writer phases")
	gatesFlag := flag.Bool("gates", false, "exact schema open/memory only after small population")
	browseFlag := flag.Bool("browse-only", false, "skip shaped write sweep after browse")
	frameFlag := flag.Bool("frames", false, "max source-frame replacements")
	flag.Parse()
	traceIO = *traceFlag
	boundedWAL = *boundedFlag
	checkpointEvery = time.Duration(*checkpointFlag) * time.Millisecond
	diagnosticMode = *diagnosticFlag
	if traceIO && !diagnosticMode {
		panic("trace-io is diagnostic only")
	}
	pagerCacheKiB = *cacheFlag
	mmapBytes = *mmapFlag
	scalarMode = *scalarFlag
	cueMode = *cueFlag
	enrichMode = *enrichFlag
	qualificationMode = *qualificationFlag
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
	save(filepath.Join(*out, "connection-pragmas.json"), connectionPragmas(c))
	hash := hex.EncodeToString(sum[:])
	save(filepath.Join(*out, "host-run-start.json"), map[string]any{"ddl_sha256": hash, "host": hostSnapshot(), "gomaxprocs": runtime.GOMAXPROCS(0)})
	if policy, err := os.ReadFile(strings.TrimSuffix(*ddl, ".sql") + "-index-policy.json"); err == nil {
		ph := sha256.Sum256(policy)
		save(filepath.Join(*out, "index-policy.json"), map[string]any{"ddl_sha256": hash, "policy_sha256": hex.EncodeToString(ph[:]), "policy": json.RawMessage(policy)})
	}
	if *target > 0 {
		var stop chan struct{}
		var wg sync.WaitGroup
		checkpoints := []any{}
		if boundedWAL {
			if !normalWrites {
				panic("bounded WAL requires NORMAL/PASSIVE")
			}
			walGovernor = startWALControl(*dbpath, *out, hash)
			defer walGovernor.stopAndSave()
		} else if normalWrites {
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
						checkpoints = append(checkpoints, map[string]any{"start_ns": start.UnixNano(), "ms": float64(time.Since(start).Nanoseconds()) / 1e6, "disk": diskSnapshot(), "busy": busy, "log": log, "checkpointed": done, "error": err})
						checkpointMu.Unlock()
					}
				}
			}()
		}
		seedAndSweep(c, *target, *out, hash)
		save(filepath.Join(*out, "fixture-ready.json"), map[string]any{"ddl_sha256": hash, "target_residents": *target, "disk": diskSnapshot(), "host": hostSnapshot(), "pragmas": connectionPragmas(c)})
		if *cpuFlag {
			pf, err := os.Create(filepath.Join(*out, "cpu.pprof"))
			must(err)
			must(pprof.StartCPUProfile(pf))
			defer func() { pprof.StopCPUProfile(); must(pf.Close()) }()
		}
		if attributionMode {
			omitFTS = *omitFlag || decoupledSchema
			attribution(c, *out, hash)
		}
		if scalarMode {
			scalarPrimitives(c, *out, hash)
		}
		if cueMode {
			cuePrimitives(c, *out, hash)
		}
		if enrichMode {
			enrichment(c, *out, hash)
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
		if walGovernor != nil {
			walGovernor.stopAndSave()
			walGovernor = nil
		}
	}
	if decoupledSchema && (attributionMode || gatesOnly) {
		var jobs, ready, pubs, pending, seq, entities int64
		must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&entities))
		must(c.QueryRowContext(context.Background(), "SELECT count(*),count(CASE WHEN phase='capture' AND job_due_ms=0 AND input_generation=1 AND job_revision=1 THEN 1 END) FROM search_publish_jobs").Scan(&jobs, &ready))
		must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM search_publications").Scan(&pubs))
		must(c.QueryRowContext(context.Background(), "SELECT pending_subjects,captured_input_sequence FROM search_generations WHERE id=1").Scan(&pending, &seq))
		if jobs != entities || ready != jobs || pubs != 0 || pending != jobs || seq != jobs {
			panic("source/search witness drift")
		}
		var hasWitness, assetWitnesses int64
		must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM pragma_table_info('catalog_assets') WHERE name='authored_membership_revision'").Scan(&hasWitness))
		if hasWitness > 0 {
			must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_assets WHERE authored_membership_revision=1").Scan(&assetWitnesses))
			if assetWitnesses != entities {
				panic("new asset admission witness drift")
			}
		}
		save(filepath.Join(*out, "source-witness-audit.json"), map[string]any{"ddl_sha256": hash, "entities": entities, "jobs": jobs, "ready": ready, "publications": pubs, "pending_subjects": pending, "input_sequence": seq, "asset_admission_witnesses": assetWitnesses, "foreign_keys": "ON", "fts_writes": 0, "boundary": "All source acceptance rows and ready witnesses committed atomically; new assets mint authored_membership_revision1. No published search image or FTS operation. Opaque64byte recipe ID is a fixture witness, not publisher recipe qualification."})
	}
	var tables, entities int64
	var version string
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables))
	must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_entities").Scan(&entities))
	must(c.QueryRowContext(context.Background(), "SELECT sqlite_version()").Scan(&version))
	save(filepath.Join(*out, "full-manifest.json"), map[string]any{"ddl_sha256": hex.EncodeToString(sum[:]), "tables": tables, "entities": entities, "sqlite_version": version, "go": runtime.Version(), "boundary": "Exact complete DDL; Basic movie-only populated fixture, no production statement cache or full private typed writer/scanner/provider paths."})
	must(c.Close())
	must(db.Close())
	save(filepath.Join(*out, "host-run-end.json"), map[string]any{"ddl_sha256": hash, "host": hostSnapshot()})
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
			base := fmt.Sprintf("FROM catalog_entities INDEXED BY %s WHERE library_id=1 AND kind=1 AND retired=0 AND %s=%d", ageIndex(field, "sort_key"), field, band)
			q := fmt.Sprintf("WITH raw AS MATERIALIZED(SELECT * FROM(SELECT id,sort_key %s AND sort_key=? AND id>? ORDER BY id LIMIT128) UNION ALL SELECT * FROM(SELECT id,sort_key %s AND sort_key>? ORDER BY sort_key,id LIMIT128) ORDER BY sort_key,id LIMIT128) SELECT r.id,r.sort_key,CASE WHEN %s THEN1 ELSE0 END,t.term_id FROM raw r CROSS JOIN catalog_entities e ON e.id=r.id LEFT JOIN catalog_entity_terms t ON t.entity_id=r.id ORDER BY r.sort_key,r.id,t.term_id", base, base, exclude)
			q = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(q, "LIMIT128", "LIMIT 128"), "THEN1", "THEN 1"), "ELSE0", "ELSE 0")
			rows, err := c.QueryContext(ctx, q, key, after, key)
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
	for _, p := range []struct{ needle, name string }{{"INSERT INTO catalog_subjects", "subject"}, {"INSERT INTO catalog_identities", "identity-key"}, {"INSERT INTO catalog_entities", "entity-indexes"}, {"INSERT INTO catalog_assets", "asset-indexes"}, {"INSERT INTO catalog_asset_links", "link-indexes"}, {"INSERT INTO search_own_prefix", "fts"}, {"INSERT INTO search_publish_jobs", "search-input-job"}, {"UPDATE search_generations", "search-generation"}, {"UPDATE libraries", "library-revision"}, {"INSERT INTO api_events", "event"}, {"INSERT INTO catalog_age_item_counts", "age-counter"}, {"INSERT INTO admin_receipts", "receipt"}} {
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
	if qualificationMode {
		sizes = []int{1, 2, 4, 8, 16, 32, 64, 128}
		runs = 1000
		if os.Getenv("GITHUB_RUN_ID") == "" {
			runs = 128
		}
	}
	if diagnosticMode {
		sizes = []int{1, 16}
		runs = 128
		if os.Getenv("GITHUB_RUN_ID") == "" {
			runs = 2
		}
	}
	if ablationMode {
		sizes = []int{8}
		runs = 501
	}
	if traceIO {
		stopTrace := attachTrace(out, hash)
		defer stopTrace()
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
			admitWAL()
			beforeHost := hostSnapshot()
			walBefore := walCounters(c)
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
			walPrecommit := walCounters(c)
			t := time.Now()
			pprof.Do(context.Background(), pprof.Labels("native_sql", fmt.Sprintf("%s-b%d.commit", control, batch)), func(ctx context.Context) { _, e = c.ExecContext(ctx, "COMMIT") })
			must(e)
			commit := float64(time.Since(t).Nanoseconds()) / 1e6
			hold := float64(time.Since(start).Nanoseconds()) / 1e6
			observeWALCommit()
			phases = append(phases, map[string]any{"phase": "commit", "start_ns": t.UnixNano(), "ms": commit})
			phaseSamples["commit"] = append(phaseSamples["commit"], commit)
			row := map[string]any{"rep": rep, "start_ns": start.UnixNano(), "hold_ms": hold, "phases": phases, "host_before": beforeHost, "host_after": hostSnapshot(), "wal_before": walBefore, "wal_precommit": walPrecommit, "wal_after": walCounters(c)}
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
		upper, rank := quantileUpper(times, .99, .95)
		row := map[string]any{"ddl_sha256": hash, "control": control, "batch": batch, "n": reps, "p99_upper_95_ms": upper, "p99_upper_rank": rank, "hosted_p99_upper_95_pass": rank > 0 && upper <= 10, "hosted_max_directional_ms": pct(times, 1), "p50_ms": pct(times, .5), "p95_ms": pct(times, .95), "p99_ms": pct(times, .99), "max_ms": pct(times, 1), "strict": pct(times, .99) <= 10 && pct(times, 1) <= 25, "max50": pct(times, 1) <= 50, "entities_per_s": float64(batch*reps) / time.Since(totalStart).Seconds(), "phase_summary": summaries, "samples": samples, "holds_over25_ms": tails}
		results = append(results, row)
		save(filepath.Join(out, "writer-attribution.json"), map[string]any{"ddl_sha256": hash, "fts_excluded": omitFTS, "qualification_protocol": "O-08 accepted: >=3VMs/profile,1000holds/cell, per-VM95%upperp99; hostedmaxdirectional; quiet/reference max;4xmedian split inconclusive", "empirical_strict_flag_is_not_qualification": true, "results": results, "boundary": "One hosted benchmark job per VM, no concurrent child probes. NORMAL +PASSIVE100ms unless checkpoint-paused. Each statement separate ExecContext: parse/driver/index work charged to that statement, FTS deferred work may appear in COMMIT. Timestamps permit checkpoint overlap analysis; host scheduler/GC/IO are not individually attributed. No claim quiet physical host."})
		fmt.Println("attribution", batch, control, pct(times, .99), pct(times, 1))
	}
	for _, size := range sizes {
		measure(size, runs, "normal-passive")
	}
	if !ablationMode && !diagnosticMode && !boundedWAL {
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

func sqlLiteral(v any) string {
	if v == nil {
		return "NULL"
	}
	switch x := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case []byte:
		return "X'" + hex.EncodeToString(x) + "'"
	case int64:
		return fmt.Sprint(x)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	panic(fmt.Sprintf("unsupported cursor type%T", v))
}
func seekPage(where, col, direction, projection string, anchor int, value any) string {
	op := ">"
	if direction == "DESC" {
		op = "<"
	}
	conditions := []string{}
	if value == nil {
		conditions = append(conditions, fmt.Sprintf("%s IS NULL AND id%s%d", col, op, anchor))
		if direction == "ASC" {
			conditions = append(conditions, col+" IS NOT NULL")
		}
	} else {
		v := sqlLiteral(value)
		conditions = append(conditions, fmt.Sprintf("%s=%s AND id%s%d", col, v, op, anchor), fmt.Sprintf("%s%s%s", col, op, v))
		if direction == "DESC" {
			conditions = append(conditions, col+" IS NULL")
		}
	}
	parts := []string{}
	for _, condition := range conditions {
		parts = append(parts, "SELECT * FROM(SELECT "+projection+","+col+" AS page_key FROM catalog_entities WHERE "+where+" AND "+condition+" ORDER BY "+col+" "+direction+",id "+direction+" LIMIT 40)")
	}
	return "SELECT " + projection + " FROM(" + strings.Join(parts, " UNION ALL ") + ") ORDER BY page_key " + direction + ",id " + direction + " LIMIT 40"
}

func hostSnapshot() map[string]any {
	files := map[string]string{}
	paths := []string{"/proc/stat", "/proc/diskstats", "/proc/pressure/cpu", "/proc/pressure/io", "/proc/pressure/memory", "/proc/self/stat", "/proc/self/status", "/proc/vmstat"}
	if b, e := os.ReadFile("/proc/self/cgroup"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) == 3 && parts[0] == "0" {
				for _, name := range []string{"cpu.stat", "cpu.max", "memory.events", "memory.current", "memory.peak", "memory.stat", "memory.swap.current", "memory.swap.max", "io.stat"} {
					paths = append(paths, filepath.Join("/sys/fs/cgroup", parts[2], name))
				}
			}
		}
	}
	for _, p := range paths {
		if b, e := os.ReadFile(p); e == nil {
			files[p] = string(b)
		} else {
			files[p] = "UNAVAILABLE: " + e.Error()
		}
	}
	tasks := map[string]string{}
	if entries, e := os.ReadDir("/proc/self/task"); e == nil {
		for _, entry := range entries {
			if b, e := os.ReadFile(filepath.Join("/proc/self/task", entry.Name(), "schedstat")); e == nil {
				tasks[entry.Name()] = string(b)
			}
		}
	}
	return map[string]any{"at_ns": time.Now().UnixNano(), "files": files, "task_schedstat": tasks, "disk": diskSnapshot(), "boundary": "Guest steal/iowait/diskstats/PSI and per-native-thread schedstat; unavailable or zero counters do not prove absence of physical-host stalls. Samples outside writer custody; timing window includes only actual statements+COMMIT."}
}

// Distribution-free one-sided upper confidence order statistic for a quantile.
// P(true quantile <= X_(k)) = P(Binomial(N,p) <= k-1).
// rank0 means N is insufficient even when the largest sample is used.
func quantileUpper(xs []float64, p, confidence float64) (float64, int) {
	n := len(xs)
	q := 1 - p
	prob := math.Pow(p, float64(n))
	cdf := prob
	fmax := 0
	for f := 1; f <= n; f++ {
		if 1-cdf+1e-14 < confidence {
			break
		}
		fmax = f
		prob *= float64(n-f+1) / float64(f) * q / p
		cdf += prob
	}
	if fmax == 0 {
		return 0, 0
	}
	rank := n - fmax + 1
	sorted := append([]float64{}, xs...)
	sort.Float64s(sorted)
	return sorted[rank-1], rank
}

type sqlPhase struct{ name, sql string }

func recordPrimitive(c *sql.Conn, out, hash, name string, reps int, build func(int) []sqlPhase, restore func(int)) {
	samples := []any{}
	times := []float64{}
	effects := []int64{}
	startAll := time.Now()
	for j := 0; j < reps; j++ {
		phases := build(j)
		var before, after int64
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&before))
		admitWAL()
		hostBefore := hostSnapshot()
		_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
		must(e)
		start := time.Now()
		details := []any{}
		for _, p := range phases {
			t := time.Now()
			pprof.Do(context.Background(), pprof.Labels("native_sql", name+"."+p.name), func(ctx context.Context) { _, e = c.ExecContext(ctx, p.sql) })
			must(e)
			details = append(details, map[string]any{"phase": p.name, "start_ns": t.UnixNano(), "ms": float64(time.Since(t).Nanoseconds()) / 1e6})
		}
		t := time.Now()
		pprof.Do(context.Background(), pprof.Labels("native_sql", name+".commit"), func(ctx context.Context) { _, e = c.ExecContext(ctx, "COMMIT") })
		must(e)
		end := time.Now()
		ms := float64(end.Sub(start).Nanoseconds()) / 1e6
		observeWALCommit()
		details = append(details, map[string]any{"phase": "commit", "start_ns": t.UnixNano(), "ms": float64(end.Sub(t).Nanoseconds()) / 1e6})
		times = append(times, ms)
		must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&after))
		effects = append(effects, after-before)
		samples = append(samples, map[string]any{"sample": j, "start_ns": start.UnixNano(), "end_ns": end.UnixNano(), "hold_ms": ms, "rows_changed": after - before, "phases": details, "host_before": hostBefore, "host_after": hostSnapshot()})
		if restore != nil {
			admitWAL()
			restore(j)
			observeWALCommit()
		}
	}
	upper, rank := quantileUpper(times, .99, .95)
	save(filepath.Join(out, name+".json"), map[string]any{"ddl_sha256": hash, "n": reps, "p50_ms": pct(times, .5), "p95_ms": pct(times, .95), "p99_ms": pct(times, .99), "p99_upper_95_ms": upper, "p99_upper_rank": rank, "hosted_p99_upper_95_pass": rank > 0 && upper <= 10, "hosted_max_directional_ms": pct(times, 1), "rows_changed": effects, "samples": samples, "elapsed_with_restore_and_telemetry_s": time.Since(startAll).Seconds(), "boundary": "Bundled complete-DDL primitive; all SQL phases+receipt+COMMIT included. Restore/telemetry excluded from writer hold. Not production private typed command or authority oracle."})
	fmt.Println(name, pct(times, .99), pct(times, 1))
}
func sourceAcceptanceSQL(start, count int) string {
	end := start + count - 1
	duration := "7200+id%1000"
	if scalarMode {
		duration = "id"
	}
	return fmt.Sprintf(`WITH RECURSIVE n(id) AS(VALUES(%d) UNION ALL SELECT id+1 FROM n WHERE id<%d)
 INSERT INTO catalog_subjects(id,public_id,library_id,kind) SELECT id,CAST(printf('%%016d',id) AS BLOB),1,1 FROM n;
 INSERT INTO catalog_identities(library_id,source_key,subject_id) SELECT 1,printf('/synthetic/full/%%d',id),id FROM catalog_subjects WHERE id BETWEEN %d AND %d;
 INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,added_at,duration_key,file_size_key,resolution_key,metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,admission_allow_age)
 SELECT id,public_id,1,1,'Word'||char(97+id%%26),'word'||char(97+id%%26),'w','word'||char(97+id%%26),'w',(id*2654435761)%%4294967296,printf('2026-11-04T00:00:00.%%09dZ',id),%s,1000000+id,1080,randomblob(16),randomblob(16),randomblob(16),1,-1 FROM catalog_subjects WHERE id BETWEEN %d AND %d;
 INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id)
 SELECT id,printf('asset-%%d',id),printf('/synthetic/full/%%d',id),file_size_key,1,'mkv','h264','aac',1920,1080,duration_key,1 FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO catalog_asset_links(entity_id,asset_id,info_duration,library_id) SELECT id,id,duration_key,1 FROM catalog_entities WHERE id BETWEEN %d AND %d;
 INSERT INTO search_publish_jobs(entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_revision,job_next_attempt_ms)
 SELECT id,1,1,metadata_incarnation,metadata_revision,'0b32cf6f04014d1d81310ff7c1ac9066998937c585b7d87d796cd6cff00e1340','capture',1,0 FROM catalog_entities WHERE id BETWEEN %d AND %d;
 UPDATE search_generations SET pending_subjects=pending_subjects+%d,captured_input_sequence=captured_input_sequence+%d WHERE id=1;
 INSERT INTO catalog_age_item_counts(library_id,kind,block_unrated,admission_age,total) VALUES(1,1,0,-1,%d) ON CONFLICT(library_id,kind,block_unrated,admission_age) DO UPDATE SET total=total+excluded.total;
 UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='full-library';
 INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','library','full-library',1);
 INSERT INTO admin_receipts(scope,operation_id,digest,result,created_ms) VALUES('research.ingest','%d-%d','%s','applied',1);`, start, end, start, end, duration, start, end, start, end, start, end, start, end, count, count, count, start, count, strings.Repeat("0", 64))
}

func scalarKeys(where string) string {
	fields := []struct{ target, source, pred, def string }{{"duration_key", "duration", "duration IS NOT NULL", "0"}, {"community_rating_key", "community_rating", "community_rating IS NOT NULL", "0"}, {"critic_rating_key", "critic_rating", "critic_rating IS NOT NULL", "0"}, {"resolution_key", "resolution", "resolution IS NOT NULL", "0"}, {"latest_aired_key", "latest_aired_key", "latest_aired_key IS NOT NULL", "''"}}
	sets := []string{}
	for _, f := range fields {
		v := fmt.Sprintf("coalesce((SELECT %s FROM catalog_scalar_contributions c WHERE c.parent_id=catalog_entities.id AND %s ORDER BY %s DESC,contributor_id,source LIMIT 1),%s)", f.source, f.pred, f.source, f.def)
		if f.target == "resolution_key" {
			v = "CASE WHEN kind IN(1,2,3,4,10) THEN " + v + " ELSE NULL END"
		}
		sets = append(sets, f.target+"="+v)
	}
	sets = append(sets, "file_size_key=coalesce((SELECT file_size FROM catalog_asset_contributions c WHERE c.parent_id=catalog_entities.id ORDER BY file_size DESC,asset_id LIMIT 1),0)")
	return "UPDATE catalog_entities SET " + strings.Join(sets, ",") + ",revision=revision+1,collection_revision=collection_revision+1,aggregate_revision=aggregate_revision+1 WHERE " + where + ";"
}

const scalarInsert = "INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,duration,community_rating,critic_rating,resolution,latest_aired_key,admission_age) "

func scalarFromEntity(parent int, where string) string {
	return scalarFromEntitySource(parent, where, 8)
}
func scalarFromEntitySource(parent int, where string, source int) string {
	return fmt.Sprintf("SELECT library_id,%d,id,%d,scalar_source_revision,id,duration_key,community_rating_key,critic_rating_key,resolution_key,release_key,admission_own_age FROM catalog_entities WHERE %s;", parent, source, where)
}
func assetFromEntity(parent int, where string) string {
	return fmt.Sprintf("INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) SELECT 1,%d,a.id,count(*),a.size,a.file_size_revision,count(*) FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE %s GROUP BY a.id;", parent, where)
}
func sharedReceipt(name string, j int) string {
	return fmt.Sprintf("UPDATE libraries SET data_revision=data_revision+1,data_order_revision=data_order_revision+1 WHERE id='full-library';INSERT INTO api_events(audience,type,resource_kind,resource_id,at_ms) VALUES('library:full-library','changed','library','full-library',1);INSERT INTO admin_receipts VALUES('%s','%d','%s','applied',1);", name, j, strings.Repeat("0", 64))
}
func fixtureTx(c *sql.Conn, q string) {
	admitWAL()
	_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE;"+q+"COMMIT;")
	must(e)
	observeWALCommit()
}
func updateScalarSource(parent, size, duration int) string {
	q := fmt.Sprintf("UPDATE catalog_assets SET duration=%d,size=%d,file_size_revision=file_size_revision+1,info_epoch=info_epoch+1,playback_origin_revision=playback_origin_revision+1 WHERE id=1;", duration, size)
	q += fmt.Sprintf("UPDATE catalog_asset_links SET info_duration=%d WHERE entity_id=1;UPDATE catalog_entities SET duration_key=%d,file_size_key=%d,revision=revision+1,scalar_source_revision=scalar_source_revision+1 WHERE id=1;", duration, duration, size)
	where := "contributor_id=1"
	assetwhere := "asset_id=1"
	if parent > 0 {
		where += fmt.Sprintf(" AND parent_id=%d", parent)
		assetwhere += fmt.Sprintf(" AND parent_id=%d", parent)
	}
	q += fmt.Sprintf("UPDATE catalog_scalar_contributions SET duration=%d,contributor_revision=(SELECT scalar_source_revision FROM catalog_entities WHERE id=1) WHERE %s;UPDATE catalog_asset_contributions SET file_size=%d,file_size_revision=(SELECT file_size_revision FROM catalog_assets WHERE id=1) WHERE %s;", duration, where, size, assetwhere)
	return q
}
func scalarPrimitives(c *sql.Conn, out, hash string) {
	var n int
	must(c.QueryRowContext(context.Background(), "SELECT max(id) FROM catalog_entities").Scan(&n))
	members := n
	if os.Getenv("GITHUB_RUN_ID") == "" {
		members = 500
	}
	extra, parent, revLo, revHi := n+1, n+1001, n+1002, n+1101
	fixtureTx(c, acceptanceSQL(n+1, 1000))
	fixtureTx(c, acceptanceSQL(n+1001, 104))
	fixtureTx(c, fmt.Sprintf("INSERT OR IGNORE INTO catalog_kinds VALUES(10,'collection',0,1,1,1,0);PRAGMA defer_foreign_keys=ON;UPDATE catalog_subjects SET kind=10 WHERE id BETWEEN %d AND %d;UPDATE catalog_entities SET kind=10,collection_name_key=printf('scalar-%%d',id) WHERE id BETWEEN %d AND %d;", parent, revHi, parent, revHi))
	for lo := 1; lo <= members; lo += 1000 {
		hi := lo + 999
		if hi > members {
			hi = members
		}
		where := fmt.Sprintf("id BETWEEN %d AND %d", lo, hi)
		q := fmt.Sprintf("INSERT INTO catalog_collection_members SELECT %d,id,printf('%%010d',id) FROM catalog_entities WHERE %s;", parent, where) + scalarInsert + scalarFromEntity(parent, where) + assetFromEntity(parent, "e."+where)
		fixtureTx(c, q)
	}
	fixtureTx(c, scalarKeys(fmt.Sprintf("id=%d", parent))+fmt.Sprintf("UPDATE catalog_entities SET admission_member_count=%d,admission_unrated_members=%d WHERE id=%d;", members, members, parent))
	fixtureTx(c, ageCountsFill())
	var e error
	plans := []any{}
	for _, f := range []struct{ col, pred string }{{"duration", "duration IS NOT NULL"}, {"community_rating", "community_rating IS NOT NULL"}, {"critic_rating", "critic_rating IS NOT NULL"}, {"resolution", "resolution IS NOT NULL"}, {"latest_aired_key", "latest_aired_key IS NOT NULL"}} {
		q := fmt.Sprintf("SELECT %s FROM catalog_scalar_contributions WHERE parent_id=%d AND %s ORDER BY %s DESC,contributor_id,source LIMIT 1", f.col, parent, f.pred, f.col)
		plans = append(plans, readCohort(c, q, 1, time.Second))
	}
	plans = append(plans, readCohort(c, fmt.Sprintf("SELECT file_size FROM catalog_asset_contributions WHERE parent_id=%d ORDER BY file_size DESC,asset_id LIMIT 1", parent), 1, time.Second))
	save(filepath.Join(out, "scalar-fixture.json"), map[string]any{"ddl_sha256": hash, "members": members, "collection": parent, "plans": plans, "boundary": "Exact pinned scalar/asset contributions and indexes; base inline branch with aggregate_publication_id=NULL. Fixture built in bounded1k setup transactions outside measurement. Synthetic populated scalar keys, unknown age, one asset/member. Five scalar seeks+one asset-size seek; no active overlay debt or production typed/authority oracle."})
	reps := 1000
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	for _, name := range []string{"remove-extreme", "add", "update"} {
		recordPrimitive(c, out, hash, "scalar-"+name, reps, func(j int) []sqlPhase {
			currentExtra := extra + j
			q := ""
			switch name {
			case "remove-extreme":
				q = fmt.Sprintf("DELETE FROM catalog_collection_members WHERE collection_id=%d AND item_id=%d;DELETE FROM catalog_scalar_contributions WHERE parent_id=%d AND contributor_id=%d;DELETE FROM catalog_asset_contributions WHERE parent_id=%d AND asset_id=%d;UPDATE catalog_entities SET admission_member_count=admission_member_count-1,admission_unrated_members=admission_unrated_members-1 WHERE id=%d;", parent, members, parent, members, parent, members, parent)
			case "add":
				q = fmt.Sprintf("INSERT INTO catalog_collection_members VALUES(%d,%d,'%010d');", parent, currentExtra, currentExtra) + scalarInsert + scalarFromEntity(parent, fmt.Sprintf("id=%d", currentExtra)) + assetFromEntity(parent, fmt.Sprintf("e.id=%d", currentExtra)) + fmt.Sprintf("UPDATE catalog_entities SET admission_member_count=admission_member_count+1,admission_unrated_members=admission_unrated_members+1,last_content_added_at='2026-11-04T00:00:01.%09dZ' WHERE id=%d;", j, parent)
			case "update":
				q = updateScalarSource(parent, 3*members, 2*members)
			}
			if name != "update" {
				child := members
				if name == "add" {
					child = currentExtra
				}
				q += fmt.Sprintf("SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=%d AND authored_refs>0;UPDATE catalog_assets SET authored_membership_revision=authored_membership_revision+1 WHERE id=%d;UPDATE search_publish_jobs SET input_generation=input_generation+1,job_revision=job_revision+1,phase='capture',job_next_attempt_ms=0 WHERE entity_id=%d;UPDATE search_generations SET captured_input_sequence=captured_input_sequence+1 WHERE id=1;", child, child, child)
			}
			return []sqlPhase{{"contribution-delta", q}, {"six-extrema-entity", scalarKeys(fmt.Sprintf("id=%d", parent))}, {"revision-receipt", sharedReceipt("scalar-"+name, j)}}
		}, func(j int) {
			currentExtra := extra + j
			q := ""
			switch name {
			case "remove-extreme":
				var got float64
				must(c.QueryRowContext(context.Background(), "SELECT duration_key FROM catalog_entities WHERE id=?", parent).Scan(&got))
				if got != float64(members-1) {
					panic("extreme seek drift")
				}
				q = fmt.Sprintf("INSERT INTO catalog_collection_members VALUES(%d,%d,'%010d');", parent, members, members) + scalarInsert + scalarFromEntity(parent, fmt.Sprintf("id=%d", members)) + assetFromEntity(parent, fmt.Sprintf("e.id=%d", members)) + fmt.Sprintf("UPDATE catalog_entities SET admission_member_count=admission_member_count+1,admission_unrated_members=admission_unrated_members+1 WHERE id=%d;", parent)
			case "add":
				q = fmt.Sprintf("DELETE FROM catalog_scalar_contributions WHERE parent_id=%d AND contributor_id=%d;DELETE FROM catalog_asset_contributions WHERE parent_id=%d AND asset_id=%d;DELETE FROM catalog_collection_members WHERE collection_id=%d AND item_id=%d;UPDATE catalog_entities SET admission_member_count=admission_member_count-1,admission_unrated_members=admission_unrated_members-1 WHERE id=%d;", parent, currentExtra, parent, currentExtra, parent, currentExtra, parent)
			case "update":
				q = updateScalarSource(parent, 1000001, 1)
			}
			_, e = c.ExecContext(context.Background(), "BEGIN;"+q+scalarKeys(fmt.Sprintf("id=%d", parent))+"COMMIT;")
			must(e)
		})
	}
	_, e = c.ExecContext(context.Background(), fmt.Sprintf("DELETE FROM catalog_scalar_contributions WHERE parent_id=%d AND contributor_id=1;DELETE FROM catalog_asset_contributions WHERE parent_id=%d AND asset_id=1;DELETE FROM catalog_collection_members WHERE collection_id=%d AND item_id=1;UPDATE catalog_entities SET admission_member_count=admission_member_count-1,admission_unrated_members=admission_unrated_members-1 WHERE id=%d;", parent, parent, parent, parent))
	must(e)
	for _, degree := range []int{10, 100} {
		hi := revLo + degree - 1
		q := fmt.Sprintf("DELETE FROM catalog_scalar_contributions WHERE contributor_id=1;DELETE FROM catalog_asset_contributions WHERE asset_id=1;DELETE FROM catalog_collection_members WHERE item_id=1;INSERT INTO catalog_collection_members SELECT id,1,'one' FROM catalog_entities WHERE id BETWEEN %d AND %d;", revLo, hi)
		q += fmt.Sprintf("INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence,duration,community_rating,critic_rating,resolution,latest_aired_key) SELECT 1,id,1,8,(SELECT scalar_source_revision FROM catalog_entities WHERE id=1),1,1,0,0,1080,'' FROM catalog_entities WHERE id BETWEEN %d AND %d;", revLo, hi)
		q += fmt.Sprintf("INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) SELECT 1,id,1,1,(SELECT size FROM catalog_assets WHERE id=1),(SELECT file_size_revision FROM catalog_assets WHERE id=1),1 FROM catalog_entities WHERE id BETWEEN %d AND %d;UPDATE catalog_entities SET admission_member_count=1,admission_unrated_members=1 WHERE id BETWEEN %d AND %d;", revLo, hi, revLo, hi)
		_, e = c.ExecContext(context.Background(), q)
		must(e)
		recordPrimitive(c, out, hash, fmt.Sprintf("scalar-reverse-%d", degree), reps, func(j int) []sqlPhase {
			v := 2 + j%2
			return []sqlPhase{{"source-reverse-delta", updateScalarSource(0, v*1000, v)}, {"six-extrema-parents", scalarKeys("id IN(SELECT parent_id FROM catalog_scalar_contributions INDEXED BY catalog_scalar_contributor WHERE contributor_id=1)")}, {"revision-receipt", sharedReceipt(fmt.Sprintf("scalar-reverse-%d", degree), j)}}
		}, nil)
	}
}

func cuePrimitives(c *sql.Conn, out, hash string) {
	reps := 1000
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	for _, nchild := range []int{255, 8} {
		cue := nchild == 255
		label := "cue255"
		if !cue {
			label = "episode8"
		}
		var n int
		must(c.QueryRowContext(context.Background(), "SELECT max(id) FROM catalog_entities").Scan(&n))
		owner, lo, hi, p1, p2 := n+1, n+2, n+1+nchild, n+2+nchild, n+3+nchild
		fixtureTx(c, acceptanceSQL(owner, nchild+103))
		parentWhere := fmt.Sprintf("id BETWEEN %d AND %d", p1, p2)
		childWhere := fmt.Sprintf("id BETWEEN %d AND %d", lo, hi)
		total := nchild * 100
		q := "INSERT OR IGNORE INTO catalog_kinds VALUES(2,'show',0,1,1,1,0),(3,'season',0,1,1,1,0),(4,'episode',1,0,1,1,0),(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(10,'collection',0,1,1,1,0);PRAGMA defer_foreign_keys=ON;"
		if cue {
			q += fmt.Sprintf("UPDATE catalog_subjects SET kind=5 WHERE id=%d;UPDATE catalog_subjects SET kind=6 WHERE id=%d;UPDATE catalog_subjects SET kind=7 WHERE %s;UPDATE catalog_entities SET kind=5,resolution_key=NULL WHERE id=%d;UPDATE catalog_entities SET kind=6,artist_id=%d,resolution_key=NULL WHERE id=%d;UPDATE catalog_entities SET kind=7,artist_id=%d,album_id=%d,parent_id=%d,resolution_key=NULL WHERE %s;", p1, p2, childWhere, p1, p1, p2, p1, p2, p2, childWhere)
		} else {
			q += fmt.Sprintf("UPDATE catalog_subjects SET kind=2 WHERE id=%d;UPDATE catalog_subjects SET kind=3 WHERE id=%d;UPDATE catalog_subjects SET kind=4 WHERE %s;UPDATE catalog_entities SET kind=2 WHERE id=%d;UPDATE catalog_entities SET kind=3,show_id=%d,season_number=1,parent_id=%d WHERE id=%d;UPDATE catalog_entities SET kind=4,show_id=%d,season_id=%d,parent_id=%d,numbering='seasonal',episode_number=id-%d WHERE %s;", p1, p2, childWhere, p1, p1, p1, p2, p1, p2, p2, owner, childWhere)
		}
		q += fmt.Sprintf("DELETE FROM catalog_asset_links WHERE entity_id BETWEEN %d AND %d;UPDATE catalog_entities SET admission_member_count=%d,admission_unrated_members=%d WHERE %s;", lo, p2, nchild, nchild, parentWhere)
		fixtureTx(c, q)
		baseline := func() string {
			h, w, codec, video := 1080, 1920, "aac", "h264"
			if cue {
				h, w, codec, video = 0, 0, "flac", ""
			}
			q := fmt.Sprintf("INSERT OR IGNORE INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(%d,'asset-%d','/synthetic/full/%d',1000000,1,'fixture','%s','%s',%d,%d,%d,1);UPDATE catalog_assets SET size=1000000,duration=%d,height=%d,file_size_revision=file_size_revision+1 WHERE id=%d;", owner, owner, owner, video, codec, w, h, total, total, h, owner)
			q += fmt.Sprintf("INSERT OR REPLACE INTO catalog_asset_links(entity_id,asset_id,library_id,info_duration) VALUES(%d,%d,1,%d);", owner, owner, total)
			if cue {
				q += fmt.Sprintf("INSERT OR REPLACE INTO catalog_asset_links(entity_id,asset_id,library_id,cue_ordinal,start_seconds,end_seconds,info_duration) SELECT id,%d,1,id-%d,(id-%d)*100.0,CASE WHEN id=%d THEN NULL ELSE (id-%d+1)*100.0 END,100 FROM catalog_entities WHERE %s;", owner, owner, lo, hi, lo, childWhere)
			} else {
				q += fmt.Sprintf("INSERT OR REPLACE INTO catalog_asset_links(entity_id,asset_id,library_id,info_duration) SELECT id,%d,1,%d FROM catalog_entities WHERE %s;", owner, total, childWhere)
			}
			duration := fmt.Sprintf("%d", total)
			if cue {
				duration = fmt.Sprintf("CASE WHEN id=%d THEN %d ELSE 100 END", owner, total)
			}
			q += fmt.Sprintf("UPDATE catalog_entities SET file_size_key=1000000,duration_key=%s,resolution_key=CASE WHEN kind IN(1,2,3,4,10) THEN %d ELSE NULL END,scalar_source_revision=scalar_source_revision+1,revision=revision+1 WHERE id BETWEEN %d AND %d;UPDATE catalog_scalar_contributions SET duration=(SELECT duration_key FROM catalog_entities WHERE id=contributor_id),resolution=(SELECT resolution_key FROM catalog_entities WHERE id=contributor_id),contributor_revision=(SELECT scalar_source_revision FROM catalog_entities WHERE id=contributor_id) WHERE contributor_id BETWEEN %d AND %d;", duration, h, owner, hi, lo, hi)
			q += fmt.Sprintf("INSERT OR REPLACE INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) SELECT 1,id,%d,%d,1000000,(SELECT file_size_revision FROM catalog_assets WHERE id=%d),0 FROM catalog_entities WHERE %s;", owner, nchild, owner, parentWhere)
			return q + scalarKeys(parentWhere)
		}
		fixtureTx(c, baseline())
		if cue {
			fixtureTx(c, scalarInsert+scalarFromEntitySource(p1, childWhere, 4)+scalarInsert+scalarFromEntitySource(p1, childWhere, 5)+scalarInsert+scalarFromEntitySource(p2, childWhere, 3))
		} else {
			fixtureTx(c, scalarInsert+scalarFromEntitySource(p1, childWhere, 1)+scalarInsert+scalarFromEntitySource(p2, childWhere, 2))
		}
		fixtureTx(c, scalarKeys(parentWhere)+ageCountsFill())
		for _, mode := range []string{"change-corrected", "change-old-full-bound", "delete"} {
			if !cue && mode == "change-old-full-bound" {
				continue
			}
			name := label + "-" + mode
			recordPrimitive(c, out, hash, name, reps, func(j int) []sqlPhase {
				size, duration, height := 1000001+j%2, total+1+j%2, 720+360*(j%2)
				if cue {
					height = 0
				}
				validate := fmt.Sprintf("SELECT file_size_revision,authored_membership_revision FROM catalog_assets WHERE id=%d;", owner)
				var delta string
				if mode == "delete" {
					delta = fmt.Sprintf("DELETE FROM catalog_asset_contributions WHERE asset_id=%d;DELETE FROM catalog_asset_links WHERE asset_id=%d;UPDATE catalog_entities SET file_size_key=0,duration_key=0,resolution_key=CASE WHEN kind IN(1,2,3,4,10) THEN 0 ELSE NULL END,revision=revision+1,scalar_source_revision=scalar_source_revision+1 WHERE id BETWEEN %d AND %d;UPDATE catalog_scalar_contributions SET duration=0,resolution=CASE WHEN resolution IS NULL THEN NULL ELSE 0 END,contributor_revision=(SELECT scalar_source_revision FROM catalog_entities WHERE id=contributor_id) WHERE contributor_id BETWEEN %d AND %d;DELETE FROM catalog_assets WHERE id=%d;", owner, owner, owner, hi, lo, hi, owner)
				} else {
					delta = fmt.Sprintf("UPDATE catalog_assets SET size=%d,duration=%d,height=%d,file_size_revision=file_size_revision+1,info_epoch=info_epoch+1,playback_origin_revision=playback_origin_revision+1 WHERE id=%d;UPDATE catalog_entities SET file_size_key=%d,revision=revision+1 WHERE id BETWEEN %d AND %d;UPDATE catalog_asset_contributions SET file_size=%d,file_size_revision=(SELECT file_size_revision FROM catalog_assets WHERE id=%d) WHERE asset_id=%d;", size, duration, height, owner, size, owner, hi, size, owner, owner)
					changed := fmt.Sprintf("entity_id BETWEEN %d AND %d", owner, hi)
					entities := fmt.Sprintf("id BETWEEN %d AND %d", owner, hi)
					contributors := fmt.Sprintf("contributor_id BETWEEN %d AND %d", lo, hi)
					if cue && mode == "change-corrected" {
						changed = fmt.Sprintf("(entity_id=%d OR entity_id=%d)", owner, hi)
						entities = fmt.Sprintf("(id=%d OR id=%d)", owner, hi)
						contributors = fmt.Sprintf("contributor_id=%d", hi)
					}
					dur := fmt.Sprintf("%d", duration)
					if cue && mode == "change-corrected" {
						dur = fmt.Sprintf("%d-start_seconds", duration)
					}
					delta += fmt.Sprintf("UPDATE catalog_asset_links SET info_duration=%s,playback_origin_revision=playback_origin_revision+1 WHERE asset_id=%d AND %s;UPDATE catalog_entities SET duration_key=(SELECT info_duration FROM catalog_asset_links WHERE entity_id=catalog_entities.id AND asset_id=%d),resolution_key=CASE WHEN kind IN(1,2,3,4,10) THEN %d ELSE NULL END,scalar_source_revision=scalar_source_revision+1 WHERE %s;UPDATE catalog_scalar_contributions SET duration=(SELECT duration_key FROM catalog_entities WHERE id=contributor_id),resolution=(SELECT resolution_key FROM catalog_entities WHERE id=contributor_id),contributor_revision=(SELECT scalar_source_revision FROM catalog_entities WHERE id=contributor_id) WHERE %s;", dur, owner, changed, owner, height, entities, contributors)
				}
				return []sqlPhase{{"source-witness", validate}, {"source-links-leaves-contributions", delta}, {"coalesced-parent-extrema", scalarKeys(parentWhere)}, {"revision-receipt", sharedReceipt(name, j)}}
			}, func(j int) {
				fixtureTx(c, baseline())
				if cue {
					var closed float64
					must(c.QueryRowContext(context.Background(), "SELECT duration_key FROM catalog_entities WHERE id=?", lo).Scan(&closed))
					if closed != 100 {
						panic("closed CUE range drift")
					}
				}
			})
		}
		if cue {
			clo, chi := p2+1, p2+100
			fixtureTx(c, fmt.Sprintf("PRAGMA defer_foreign_keys=ON;UPDATE catalog_subjects SET kind=10 WHERE id BETWEEN %d AND %d;UPDATE catalog_entities SET kind=10,resolution_key=0,collection_name_key=printf('cue-collapse-%%d',id),admission_member_count=255,admission_unrated_members=255 WHERE id BETWEEN %d AND %d;", clo, chi, clo, chi))
			for parent := clo; parent <= chi; parent++ {
				fixtureTx(c, fmt.Sprintf("INSERT INTO catalog_collection_members SELECT %d,id,printf('%%010d',id) FROM catalog_entities WHERE %s;", parent, childWhere)+scalarInsert+scalarFromEntity(parent, childWhere)+fmt.Sprintf("INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,file_size,file_size_revision,authored_refs) SELECT 1,%d,%d,255,size,file_size_revision,255 FROM catalog_assets WHERE id=%d;", parent, owner, owner))
			}
			fixtureTx(c, fmt.Sprintf("UPDATE catalog_assets SET authored_membership_revision=authored_membership_revision+1 WHERE id=%d;", owner)+scalarKeys(fmt.Sprintf("id BETWEEN %d AND %d", clo, chi))+ageCountsFill())
			recordPrimitive(c, out, hash, "cue255-size-collapsed100", reps, func(j int) []sqlPhase {
				size := 2000000 + j%2
				return []sqlPhase{{"admission-witness", fmt.Sprintf("SELECT authored_membership_revision FROM catalog_assets WHERE id=%d;SELECT count(*) FROM catalog_asset_contributions WHERE asset_id=%d AND authored_refs>0;", owner, owner)}, {"asset-size-only", fmt.Sprintf("UPDATE catalog_assets SET size=%d,file_size_revision=file_size_revision+1,info_epoch=info_epoch+1 WHERE id=%d;UPDATE catalog_entities SET file_size_key=%d,revision=revision+1 WHERE id BETWEEN %d AND %d;UPDATE catalog_asset_contributions SET file_size=%d,file_size_revision=(SELECT file_size_revision FROM catalog_assets WHERE id=%d) WHERE asset_id=%d;", size, owner, size, owner, hi, size, owner, owner)}, {"102-parent-size-keys", fmt.Sprintf("UPDATE catalog_entities SET file_size_key=(SELECT file_size FROM catalog_asset_contributions WHERE parent_id=catalog_entities.id AND asset_id=%d),aggregate_revision=aggregate_revision+1,revision=revision+1 WHERE id BETWEEN %d AND %d;", owner, p1, chi)}, {"revision-receipt", sharedReceipt("cue255-size-collapsed100", j)}}
			}, nil)
		}
		save(filepath.Join(out, label+"-fixture.json"), map[string]any{"ddl_sha256": hash, "children": nchild, "owner": owner, "automatic_parents": []int{p1, p2}, "base_inline": true, "boundary": "Single-library1owner+255CUE(or8episodes),2automatic parents, asset-scoped size witnesses. Synthetic unparented movie owner deliberately adds a conservative owner row; not a music owner identity oracle. CUEduration closed=end-start/openlast=asset-start, audio resolutionNULL; oldfull propagation is a semantic negative upper bound. Delete removes physical asset/links, retains subjects/entities/hierarchy/metadata and zeroes unavailable metrics; no entity retirement/pinned-prepared custody oracle. CAPSc adds100shared authored collections,25500scalar tuples untouched onsize-only change,102per(parent,asset)size rows. No active aggregate overlay debt, no per-profile visibility."})
	}
}

type actionPhase struct {
	name string
	run  func()
}
type enrichmentInput struct {
	entity, people                                                            int
	key, evidence, publication, operation, creditpub, digest, title, overview string
	old                                                                       bool
}

func syntheticID(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:16]) }
func enrichment(c *sql.Conn, out, hash string) {
	reps := 1000
	if os.Getenv("GITHUB_RUN_ID") == "" {
		reps = 2
	}
	var resident int
	must(c.QueryRowContext(context.Background(), "SELECT max(id) FROM catalog_entities").Scan(&resident))
	parent := resident + 1
	fixtureTx(c, acceptanceSQL(parent, 1)+fmt.Sprintf("INSERT OR IGNORE INTO catalog_kinds VALUES(10,'collection',0,1,1,1,0);PRAGMA defer_foreign_keys=ON;UPDATE catalog_subjects SET kind=10 WHERE id=%d;UPDATE catalog_entities SET kind=10,collection_name_key='enrichment-parent' WHERE id=%d;INSERT OR IGNORE INTO metadata_providers VALUES('tmdb','fixture',1,0);INSERT INTO metadata_provider_policy(library_id,provider,incarnation,revision,enabled,priority,region,algorithm,refresh_mode) VALUES(1,'tmdb',randomblob(16),1,1,1,'US','fixture','replace_unlocked');INSERT INTO catalog_credit_labels VALUES(1,'Actor'),(2,'Producer'),(3,'Acting'),(4,'Production');", parent, parent))
	prepared := map[string]*sql.Stmt{}
	prepare := func(name, q string) { s, e := c.PrepareContext(context.Background(), q); must(e); prepared[name] = s }
	defer func() {
		for _, s := range prepared {
			must(s.Close())
		}
	}()
	x := func(name string, args ...any) {
		_, e := prepared[name].ExecContext(context.Background(), args...)
		must(e)
	}
	prepare("rating", "INSERT INTO metadata_evidence_ratings(evidence_id,provider,role,value,scale,votes,source_url) VALUES(?,'tmdb',?,?,?,100,'https://example.invalid/accepted')")
	prepare("subject", "INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision) VALUES(?,'tmdb','movie',?,'en-US','US','movie-v1',randomblob(16),1)")
	prepare("evidence", "INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,title,original_title,overview,tagline,content_rating,release_date,year) VALUES(?,?,'fixture','tmdb','movie',?,'en-US','US','movie-v1',?,'2026-11-04T00:00:00.000000000Z','https://example.invalid/accepted',?,?,?,'accepted tagline','PG-13','2026-11-04',2026)")
	prepare("evidence-credit", "INSERT INTO metadata_evidence_credits(provider,evidence_kind,credit_class,evidence_id,ordinal,provider_person_id,name,role,department,credit_id) VALUES('tmdb','movie',?,?,?,?,?,?,?,?)")
	prepare("source-credit", "INSERT INTO credit_source_rows(source_row_id,entity_id,provider,tuple_digest,original_record_reference,original_credit_id,original_provider_person_id,original_person_identity_key,original_person_token,original_target_kind,original_target_id,original_credited_name,original_role,original_department,original_credit_class,original_source_ordinal) VALUES(?,?,'tmdb',?,?,?,?,?,?,'person',?,?,?,?,?,?)")
	prepare("credit-member", "INSERT INTO credit_source_publication_members(publication_id,entity_id,provider,position,source_row_id,reference_status) VALUES(?,?,'tmdb',?,?,'new')")
	prepare("effective-credit", "INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,provider_person_id,source_ordinal,source_row_id) VALUES(?,?,?,?,?,?,'tmdb',?,?,?,?)")
	prepare("people", "INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(?,?,?,?,?,randomblob(16))")
	prepare("person-id", "INSERT INTO metadata_person_external_ids VALUES(?,'tmdb',?)")
	prepare("origin", "INSERT INTO metadata_field_origins(entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id,evidence_id,subject_kind,provider_id) VALUES(?,?,'tmdb','provider','https://example.invalid/accepted',1,'2026-11-04T00:00:00.000000000Z','en-US','US',1,?,?,'movie',?) ON CONFLICT(entity_id,field) DO UPDATE SET publication_id=excluded.publication_id,evidence_id=excluded.evidence_id,provider_id=excluded.provider_id,selection_revision=selection_revision+1")
	prepare("art", "INSERT INTO artwork_selections(kind,entity_id,role,choice,digest,provider,image_id,origin,attribution,locale,effective_language,source_fence,actor,observed_at) VALUES('entity',?,?,'matched',?,'tmdb',?,'provider','fixture','en-US','en-US',?,'background','2026-11-04T00:00:00.000000000Z') ON CONFLICT(kind,entity_id,role,choice) DO UPDATE SET image_id=excluded.image_id,source_fence=excluded.source_fence,revision=revision+1")
	peopleRows := func(start int) {
		for i := 0; i < 150; i++ {
			id := start + i
			token := fmt.Sprintf("person-%d", id)
			name := fmt.Sprintf("Accepted Person %d", id)
			x("people", id, token, token, name, strings.ToLower(name))
			x("person-id", id, strconv.Itoa(id))
		}
		_, e := c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO search_publish_jobs(person_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_next_attempt_ms) SELECT id,1,1,artwork_incarnation,revision,'0b32cf6f04014d1d81310ff7c1ac9066998937c585b7d87d796cd6cff00e1340','capture',0 FROM catalog_people WHERE id BETWEEN %d AND %d;UPDATE search_generations SET pending_subjects=pending_subjects+150,captured_input_sequence=captured_input_sequence+150 WHERE id=1;", start, start+149))
		must(e)
	}
	admitWAL()
	_, e := c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
	must(e)
	peopleRows(1)
	_, e = c.ExecContext(context.Background(), "COMMIT")
	must(e)
	observeWALCommit()
	artDigests := []string{}
	for i := 0; i < 4; i++ {
		digest := fmt.Sprintf("%064x", i+1)
		artDigests = append(artDigests, digest)
		fixtureTx(c, fmt.Sprintf("INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at) VALUES('%s','image/jpeg',1280,720,100000,'2026-11-04T00:00:00Z');INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) VALUES('%s','image/jpeg',1280,720,'%s','%s','%s',1280,0,0,1,'2026-11-04T00:00:00Z');", digest, digest, digest, digest, digest))
	}
	input := func(entity int, version string, people int) enrichmentInput {
		key := fmt.Sprintf("enrich-%d-%s", entity, version)
		digest := sha256.Sum256([]byte(key))
		prefix, body := "W", "m"
		if version == "old" {
			prefix, body = "V", "o"
		}
		return enrichmentInput{entity: entity, people: people, key: fmt.Sprintf("movie-%d", entity), evidence: syntheticID(key + "-evidence"), publication: syntheticID(key + "-publication"), operation: syntheticID(key + "-operation"), creditpub: syntheticID(key + "-credits"), digest: hex.EncodeToString(digest[:]), title: prefix + strings.Repeat("x", 999), overview: strings.Repeat(body, 20000), old: version == "old"}
	}
	operation := func(a enrichmentInput) {
		_, e := c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO metadata_provider_operations(id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,query_digest,input_digest,selected_id,manual,review_search,qualification,created_at) SELECT '%s',e.id,'tmdb','accept','ready',1,'fixture','2026-11-04T01:00:00Z',e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,1,1,1,p.incarnation,p.revision,1,'fixture','replace_unlocked','accepted','',2026,'%s','%s','%s',0,0,'fixture','2026-11-04T00:00:00Z' FROM catalog_entities e JOIN metadata_provider_policy p ON p.library_id=e.library_id AND p.provider='tmdb' WHERE e.id=%d;", a.operation, a.digest, a.digest, a.key, a.entity))
		must(e)
	}
	credits := func(a enrichmentInput) {
		_, e := c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count) VALUES('%s',%d,'tmdb','%s','2026-11-04T00:00:00Z',150);DELETE FROM catalog_credits WHERE entity_id=%d;", a.creditpub, a.entity, a.evidence, a.entity))
		must(e)
		for i := 0; i < 150; i++ {
			person := a.people + i
			name := fmt.Sprintf("Accepted Person %d", person)
			kind, role, department, roleID, deptID := "cast", "Actor", "Acting", 1, 3
			if i >= 100 {
				kind, role, department, roleID, deptID = "crew", "Producer", "Production", 2, 4
			}
			credit := fmt.Sprintf("%s-%d", a.evidence, i)
			row := syntheticID(credit)
			tuple := sha256.Sum256([]byte(credit + name + role + department))
			token := fmt.Sprintf("person-%d", person)
			x("evidence-credit", kind, a.evidence, i, strconv.Itoa(person), name, role, department, credit)
			x("source-credit", row, a.entity, hex.EncodeToString(tuple[:]), a.evidence, credit, strconv.Itoa(person), token, token, token, name, role, department, kind, i)
			x("credit-member", a.creditpub, a.entity, i, row)
			x("effective-credit", a.entity, i, person, roleID, deptID, name, credit, strconv.Itoa(person), i, row)
		}
		_, e = c.ExecContext(context.Background(), fmt.Sprintf("UPDATE credit_source_publications SET sealed=1 WHERE publication_id='%s';INSERT INTO credit_source_heads(entity_id,provider,publication_id,revision) VALUES(%d,'tmdb','%s',1) ON CONFLICT(entity_id,provider) DO UPDATE SET publication_id=excluded.publication_id,revision=revision+1;", a.creditpub, a.entity, a.creditpub))
		must(e)
	}
	apply := func(a enrichmentInput, newPeople bool) []actionPhase {
		rating, critic := 8, 90
		if a.old {
			rating, critic = 5, 50
		}
		return []actionPhase{{"fence", func() {
			var current, captured int64
			must(c.QueryRowContext(context.Background(), "SELECT e.metadata_revision,o.metadata_revision FROM catalog_entities e JOIN metadata_provider_operations o ON o.entity_id=e.id WHERE e.id=? AND o.id=? AND o.status='ready' AND o.entity_incarnation=e.metadata_incarnation", a.entity, a.operation).Scan(&current, &captured))
			if current != captured {
				panic("enrichment captured source fence drift")
			}
		}}, {"observation", func() {
			x("evidence", a.evidence, a.entity, a.key, a.digest, a.title, a.title, a.overview)
			x("rating", a.evidence, "community", rating, 10)
			x("rating", a.evidence, "critic", critic, 100)
		}}, {"people-and-search-intents", func() {
			if newPeople {
				peopleRows(a.people)
			}
		}}, {"150-four-credit-relations", func() { credits(a) }}, {"seal-publication-binding-accounting", func() {
			q := fmt.Sprintf("UPDATE metadata_evidence SET sealed=1 WHERE id='%s';UPDATE metadata_evidence_accounting SET direct_refs=direct_refs-1,retention_refs=retention_refs+1 WHERE evidence_id=(SELECT accepted_evidence_id FROM metadata_subject_providers WHERE entity_id=%d AND provider='tmdb' AND role='primary');INSERT INTO metadata_evidence_accounting VALUES('%s',262144,'2026-11-04T00:00:00Z',0,1,0);INSERT INTO metadata_evidence_totals VALUES('tmdb',0,0,1,262144) ON CONFLICT(provider) DO UPDATE SET other_evidence=other_evidence+1,charge=charge+262144;INSERT INTO metadata_publications(id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,selection_revision,generation,decision,observed_at) VALUES('%s','%s',%d,'tmdb','movie','%s','%s',1,1,'accepted','2026-11-04T00:00:00Z');UPDATE metadata_provider_subjects SET accepted_evidence_id='%s',revision=revision+1 WHERE id=%d;INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,accepted_evidence_id,accepted_publication_id,status,observed_at) VALUES(%d,'tmdb','primary',randomblob(16),1,'movie','%s','%s','%s','accepted','2026-11-04T00:00:00Z') ON CONFLICT(entity_id,provider,role) DO UPDATE SET revision=revision+1,accepted_evidence_id=excluded.accepted_evidence_id,accepted_publication_id=excluded.accepted_publication_id;UPDATE metadata_provider_operations SET phase='complete',status='applied',finished_at='2026-11-04T00:00:00Z' WHERE id='%s';", a.evidence, a.entity, a.evidence, a.publication, a.operation, a.entity, a.key, a.evidence, a.evidence, a.entity, a.entity, a.key, a.evidence, a.publication, a.operation)
			_, e := c.ExecContext(context.Background(), q)
			must(e)
		}}, {"entity-and-field-origins", func() {
			_, e := c.ExecContext(context.Background(), "UPDATE catalog_entities SET title=?,original_title=?,overview=?,sort_key=?,sort_key_keep_articles=?,sort_head=?,sort_head_keep_articles=?,year=2026,release_date='2026-11-04',release_key='2026-11-04T00:00:00.000Z',content_rating='PG-13',admission_own_age=13,admission_allow_age=13,admission_known_age=13,community_rating_key=?,critic_rating_key=?,metadata_language='en-US',metadata_revision=metadata_revision+1,artwork_revision=artwork_revision+1,scalar_source_revision=scalar_source_revision+1,revision=revision+1 WHERE id=?", a.title, a.title, a.overview, strings.ToLower(a.title), strings.ToLower(a.title), strings.ToLower(a.title[:1]), strings.ToLower(a.title[:1]), rating, critic, a.entity)
			must(e)
			for _, field := range []string{"title", "original_title", "overview", "year", "release_date", "content_rating", "community_rating", "critic_rating", "metadata_language"} {
				x("origin", a.entity, field, a.publication, a.evidence, a.key)
			}
		}}, {"four-selected-artwork", func() {
			for i, role := range []string{"poster", "backdrop", "logo", "banner"} {
				x("art", a.entity, role, artDigests[i], a.evidence+role, a.publication)
			}
		}}, {"parent-contribution-and-extrema", func() {
			_, e := c.ExecContext(context.Background(), fmt.Sprintf("UPDATE catalog_scalar_contributions SET community_rating=%d,critic_rating=%d,latest_aired_key='2026-11-04T00:00:00.000Z',admission_age=13,contributor_revision=(SELECT scalar_source_revision FROM catalog_entities WHERE id=%d) WHERE parent_id=%d AND contributor_id=%d;", rating, critic, a.entity, parent, a.entity)+scalarKeys(fmt.Sprintf("id=%d", parent)))
			must(e)
		}}, {"search-input-and-receipt", func() {
			_, e := c.ExecContext(context.Background(), fmt.Sprintf("UPDATE search_publish_jobs SET input_generation=input_generation+1,job_revision=job_revision+1,source_revision=(SELECT metadata_revision FROM catalog_entities WHERE id=%d),phase='capture',job_next_attempt_ms=0 WHERE entity_id=%d;UPDATE search_generations SET captured_input_sequence=captured_input_sequence+1 WHERE id=1;", a.entity, a.entity)+sharedReceipt("enrichment-"+a.evidence, 0))
			must(e)
		}}}
	}
	inputs := map[string][]enrichmentInput{"existing-people": {}, "new-people": {}}
	for mode := 0; mode < 2; mode++ {
		name := []string{"existing-people", "new-people"}[mode]
		for j := 0; j < reps; j++ {
			entity := 1 + j*997 + mode*400
			if os.Getenv("GITHUB_RUN_ID") == "" {
				entity = 1 + j*2 + mode
			}
			old := input(entity, "old", 1)
			admitWAL()
			_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			x("subject", entity, old.key)
			operation(old)
			for _, p := range apply(old, false) {
				p.run()
			}
			_, e = c.ExecContext(context.Background(), fmt.Sprintf("INSERT INTO catalog_collection_members VALUES(%d,%d,'%010d');", parent, entity, entity)+scalarInsert+scalarFromEntity(parent, fmt.Sprintf("id=%d", entity))+assetFromEntity(parent, fmt.Sprintf("e.id=%d", entity))+"COMMIT;")
			must(e)
			observeWALCommit()
			people := 1
			if mode == 1 {
				people = 151 + j*150
			}
			a := input(entity, "new", people)
			admitWAL()
			_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			operation(a)
			_, e = c.ExecContext(context.Background(), "COMMIT")
			must(e)
			observeWALCommit()
			inputs[name] = append(inputs[name], a)
		}
	}
	fixtureTx(c, fmt.Sprintf("UPDATE catalog_entities SET admission_member_count=%d,admission_unrated_members=0,admission_allow_age=13,admission_known_age=13 WHERE id=%d;", 2*reps, parent)+ageCountsFill())
	for _, name := range []string{"existing-people", "new-people"} {
		samples := []any{}
		times := []float64{}
		rowsTouched := []int64{}
		for j, a := range inputs[name] {
			phases := apply(a, name == "new-people")
			admitWAL()
			var before, after int64
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&before))
			hostBefore := hostSnapshot()
			_, e = c.ExecContext(context.Background(), "BEGIN IMMEDIATE")
			must(e)
			start := time.Now()
			details := []any{}
			for _, p := range phases {
				t := time.Now()
				pprof.Do(context.Background(), pprof.Labels("native_sql", "enrichment-"+name+"."+p.name), func(ctx context.Context) { p.run() })
				details = append(details, map[string]any{"phase": p.name, "start_ns": t.UnixNano(), "ms": float64(time.Since(t).Nanoseconds()) / 1e6})
			}
			t := time.Now()
			pprof.Do(context.Background(), pprof.Labels("native_sql", "enrichment-"+name+".commit"), func(ctx context.Context) { _, e = c.ExecContext(ctx, "COMMIT") })
			must(e)
			end := time.Now()
			ms := float64(end.Sub(start).Nanoseconds()) / 1e6
			details = append(details, map[string]any{"phase": "commit", "start_ns": t.UnixNano(), "ms": float64(end.Sub(t).Nanoseconds()) / 1e6})
			observeWALCommit()
			must(c.QueryRowContext(context.Background(), "SELECT total_changes()").Scan(&after))
			times = append(times, ms)
			rowsTouched = append(rowsTouched, after-before)
			samples = append(samples, map[string]any{"sample": j, "entity": a.entity, "start_ns": start.UnixNano(), "end_ns": end.UnixNano(), "hold_ms": ms, "rows_changed": after - before, "phases": details, "host_before": hostBefore, "host_after": hostSnapshot()})
			var count int
			must(c.QueryRowContext(context.Background(), "SELECT count(*) FROM catalog_credits WHERE entity_id=?", a.entity).Scan(&count))
			if count != 150 {
				panic("150credit replacement drift")
			}
		}
		upper, rank := quantileUpper(times, .99, .95)
		save(filepath.Join(out, "enrichment-"+name+".json"), map[string]any{"ddl_sha256": hash, "n": reps, "p50_ms": pct(times, .5), "p95_ms": pct(times, .95), "p99_ms": pct(times, .99), "p99_upper_95_ms": upper, "p99_upper_rank": rank, "hosted_p99_upper_95_pass": rank > 0 && upper <= 10, "hosted_max_directional_ms": pct(times, 1), "rows_changed": rowsTouched, "samples": samples})
		fmt.Println("enrichment", name, pct(times, .99), pct(times, 1))
	}
	save(filepath.Join(out, "enrichment-fixture.json"), map[string]any{"ddl_sha256": hash, "initial_basic_residents": resident, "target_entities": 2 * reps, "credits_old_and_new_per_entity": 150, "retained_credit_relations": []string{"metadata_evidence_credits", "credit_source_rows", "credit_source_publication_members", "catalog_credits"}, "cast": 100, "crew": 50, "title_characters": 1000, "overview_characters": 20000, "selected_art_roles": 4, "authored_parents_per_entity": 1, "incoming_facts": "Pretrimmed accepted fixture. Provider operation/policy captured before acceptance;150oldcredits seeded. Whole newimmutableevidence+credits/current replacement/seal/publication/binding/accounting/9fieldorigins+entity+4selectedreadyart+oneparentcontribution/extrema+searchintent/revisions/event/receipt/COMMIT measured. Secondcell creates150newpeople+providerIDs+searchjobs insideacceptance. Samegenre/ratingband13, no facet/classchange; changes there are separate age gates. Existingreadyart blobs/physicalIO outside writer; noFTS. Basic1Mcorpus has no credits outside2000targettitles, so fullproductioncredit-density/referencehardware/authority/policyselection/immutablequota-transition protocols remainOPEN. Source/tokenssynthetic; exactFKgraphon. No ownereditrows created because source acceptance is not an owneredit."})
}

// Experimental WAL admission follows Q/passive.go's64MiB soft,96MiB
// restart-pressure and192MiB hard envelope; no TRUNCATE/RESTART checkpoint.
type walInfo struct {
	Physical, ActiveBytes, Uncheckpointed int64
	Frames, Backfill                      uint32
	Salt                                  string
	Valid                                 bool
}
type walControl struct {
	path, out, hash                     string
	stop, done, readerDone, request     chan struct{}
	mu                                  sync.Mutex
	finish                              sync.Once
	checkpoints, reads, pauses, commits []any
	lastSalt                            string
	resets                              int
	peakPhysical, peakActive            int64
}

func walState() walInfo {
	w := walInfo{}
	if st, e := os.Stat(dbPathForChildren + "-wal"); e == nil {
		w.Physical = st.Size()
	}
	f, e := os.Open(dbPathForChildren + "-shm")
	if e != nil {
		return w
	}
	defer f.Close()
	b := make([]byte, 100)
	if _, e = f.ReadAt(b, 0); e != nil {
		return w
	}
	if !bytes.Equal(b[:48], b[48:96]) || binary.LittleEndian.Uint32(b[:4]) != 3007000 {
		return w
	}
	w.Frames = binary.LittleEndian.Uint32(b[16:20])
	w.Backfill = binary.LittleEndian.Uint32(b[96:100])
	if w.Backfill > w.Frames {
		return w
	}
	size := int64(binary.LittleEndian.Uint16(b[14:16]))
	if size == 1 {
		size = 65536
	}
	if size == 0 {
		return w
	}
	w.Salt = hex.EncodeToString(b[32:40])
	w.ActiveBytes = 32 + int64(w.Frames)*(size+24)
	w.Uncheckpointed = int64(w.Frames-w.Backfill) * (size + 24)
	w.Valid = true
	return w
}
func compactMemory() map[string]string {
	out := map[string]string{}
	b, _ := os.ReadFile("/proc/self/cgroup")
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "0::") {
			base := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::"))
			for _, name := range []string{"memory.current", "memory.stat", "memory.events", "cpu.stat", "io.stat"} {
				v, e := os.ReadFile(filepath.Join(base, name))
				if e == nil {
					out[name] = string(v)
				}
			}
		}
	}
	for _, name := range []string{"io", "memory"} {
		v, e := os.ReadFile("/proc/pressure/" + name)
		if e == nil {
			out["psi_"+name] = string(v)
		}
	}
	return out
}
func startWALControl(path, out, hash string) *walControl {
	g := &walControl{path: path, out: out, hash: hash, stop: make(chan struct{}), done: make(chan struct{}), readerDone: make(chan struct{}), request: make(chan struct{}, 1)}
	go func() {
		defer close(g.done)
		d, cc := open(path)
		defer d.Close()
		defer cc.Close()
		_, e := cc.ExecContext(context.Background(), "PRAGMA busy_timeout=0")
		must(e)
		ticker := time.NewTicker(checkpointEvery)
		defer ticker.Stop()
		var previous time.Time
		for {
			trigger := "ticker"
			select {
			case <-g.stop:
				return
			case <-ticker.C:
			case <-g.request:
				trigger = "pressure-request"
			}
			if !previous.IsZero() {
				if wait := checkpointEvery - time.Since(previous); wait > 0 {
					timer := time.NewTimer(wait)
					select {
					case <-g.stop:
						timer.Stop()
						return
					case <-timer.C:
					}
				}
			}
			t := time.Now()
			interval := float64(0)
			if !previous.IsZero() {
				interval = float64(t.Sub(previous).Nanoseconds()) / 1e6
			}
			previous = t
			before := walState()
			var busy, log, done int
			e := cc.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &done)
			row := map[string]any{"start_ns": t.UnixNano(), "ms": float64(time.Since(t).Nanoseconds()) / 1e6, "busy": busy, "log": log, "checkpointed": done, "wal_before": before, "wal_after": walState(), "memory": compactMemory(), "trigger": trigger, "start_interval_ms": interval}
			if e != nil {
				row["error"] = e.Error()
			}
			g.mu.Lock()
			g.checkpoints = append(g.checkpoints, row)
			g.mu.Unlock()
		}
	}()
	go func() {
		defer close(g.readerDone)
		d, cc := open(path)
		defer d.Close()
		defer cc.Close()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-g.stop:
				return
			case <-ticker.C:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			t := time.Now()
			rows, e := cc.QueryContext(ctx, "SELECT id,title FROM catalog_entities WHERE library_id=1 AND kind=1 AND retired=0 ORDER BY sort_key,id LIMIT 40")
			count := 0
			if e == nil {
				for rows.Next() {
					var id int64
					var title string
					e = rows.Scan(&id, &title)
					if e != nil {
						break
					}
					count++
				}
				if e == nil {
					e = rows.Err()
				}
				ce := rows.Close()
				if e == nil {
					e = ce
				}
			}
			cancel()
			ms := float64(time.Since(t).Nanoseconds()) / 1e6
			row := map[string]any{"start_ns": t.UnixNano(), "ms": ms, "rows": count, "requested_deadline_ms": 250}
			if e != nil {
				row["error"] = e.Error()
			}
			g.mu.Lock()
			g.reads = append(g.reads, row)
			g.mu.Unlock()
		}
	}()
	return g
}
func admitWAL() {
	g := walGovernor
	if g == nil {
		return
	}
	w := walState()
	if w.Valid && w.ActiveBytes < 96<<20 && w.Uncheckpointed < 64<<20 {
		return
	}
	if !w.Valid && w.Physical == 0 {
		return
	}
	t := time.Now()
	select {
	case g.request <- struct{}{}:
	default:
	}
	for {
		time.Sleep(time.Millisecond)
		w = walState()
		if w.Valid && ((w.ActiveBytes < 96<<20 && w.Uncheckpointed < 32<<20) || (w.ActiveBytes >= 96<<20 && w.Backfill == w.Frames)) {
			break
		}
		if time.Since(t) > 30*time.Second {
			save(filepath.Join(g.out, "wal-admission-failure.json"), map[string]any{"ddl_sha256": g.hash, "wal": w, "memory": compactMemory()})
			panic("bounded WAL admission failed to repay in30s")
		}
	}
	if w.ActiveBytes > 176<<20 {
		panic("WAL16MiB reserved transaction headroom exhausted")
	}
	g.mu.Lock()
	g.pauses = append(g.pauses, map[string]any{"start_ns": t.UnixNano(), "ms": float64(time.Since(t).Nanoseconds()) / 1e6, "state": w})
	g.mu.Unlock()
}
func observeWALCommit() {
	g := walGovernor
	if g == nil {
		return
	}
	w := walState()
	g.mu.Lock()
	defer g.mu.Unlock()
	if w.Valid && g.lastSalt != "" && w.Salt != g.lastSalt {
		g.resets++
	}
	if w.Valid {
		g.lastSalt = w.Salt
	}
	if w.Physical > g.peakPhysical {
		g.peakPhysical = w.Physical
	}
	if w.ActiveBytes > g.peakActive {
		g.peakActive = w.ActiveBytes
	}
	g.commits = append(g.commits, map[string]any{"at_ns": time.Now().UnixNano(), "wal": w})
	if w.Physical > 192<<20 || w.ActiveBytes > 192<<20 {
		save(filepath.Join(g.out, "wal-cap-failure.json"), map[string]any{"ddl_sha256": g.hash, "wal": w, "resets": g.resets})
		panic("WAL hard192MiB cap exceeded; invalid qualification")
	}
}
func (g *walControl) stopAndSave() {
	g.finish.Do(func() {
		close(g.stop)
		<-g.done
		<-g.readerDone
		save(filepath.Join(g.out, "bounded-wal.json"), map[string]any{"ddl_sha256": g.hash, "checkpoint_ms": checkpointEvery.Milliseconds(), "soft_bytes": 64 << 20, "hard_bytes": 192 << 20, "transaction_reserve_bytes": 16 << 20, "peak_physical_bytes": g.peakPhysical, "peak_active_bytes": g.peakActive, "resets": g.resets, "checkpoints": g.checkpoints, "readers": g.reads, "admission_pauses": g.pauses, "commits": g.commits, "boundary": "PASSIVE only on separate physical connection; writer pre-admission outside hold; no retained SQL readers,20Hz title40probes,250ms requested deadlines with actual durations retained. Q-style64/96/192MiB envelope. No forced RESTART/TRUNCATE. No publisher/authority oracle."})
	})
}

func diskSnapshot() map[string]any {
	out := map[string]any{}
	if dbPathForChildren == "" {
		return out
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := dbPathForChildren + suffix
		if st, e := os.Stat(p); e == nil {
			v := map[string]any{"size": st.Size()}
			if s, ok := st.Sys().(*syscall.Stat_t); ok {
				v["allocated_bytes"] = s.Blocks * 512
			}
			out[filepath.Base(p)] = v
		} else {
			out[filepath.Base(p)] = map[string]any{"absent": true}
		}
	}
	return out
}
func connectionPragmas(c *sql.Conn) map[string]any {
	out := map[string]any{"requested_cache_kib": pagerCacheKiB, "requested_mmap_bytes": mmapBytes}
	for _, name := range []string{"journal_mode", "synchronous", "wal_autocheckpoint", "foreign_keys", "cache_size", "mmap_size", "page_size", "page_count", "freelist_count"} {
		var v any
		must(c.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&v))
		out[name] = v
	}
	return out
}

func walCounters(c *sql.Conn) sqlite.WALIOStats {
	var result sqlite.WALIOStats
	must(c.Raw(func(dc any) error {
		if w, ok := dc.(interface{ WALIO() sqlite.WALIOStats }); ok {
			result = w.WALIO()
		}
		return nil
	}))
	return result
}
func attachTrace(out, hash string) func() {
	args := []string{"-f", "-ttt", "-T", "-yy", "-s0", "-e", "trace=pwrite64,fsync,fdatasync", "-p", strconv.Itoa(os.Getpid()), "-o", filepath.Join(out, "wal-io.strace")}
	name := "strace"
	if os.Geteuid() != 0 {
		args = append([]string{"-n", "strace"}, args...)
		name = "sudo"
	}
	cmd := exec.Command(name, args...)
	stderr, e := cmd.StderrPipe()
	must(e)
	started := time.Now()
	e = cmd.Start()
	if e != nil {
		save(filepath.Join(out, "io-trace-status.json"), map[string]any{"ddl_sha256": hash, "error": e.Error()})
		return func() {}
	}
	ready := make(chan bool, 1)
	done := make(chan struct{})
	lines := []string{}
	go func() {
		defer close(done)
		s := bufio.NewScanner(stderr)
		sent := false
		for s.Scan() {
			line := s.Text()
			lines = append(lines, line)
			if !sent && strings.Contains(line, "attached") {
				ready <- true
				sent = true
			}
		}
		if !sent {
			ready <- false
		}
	}()
	attached := false
	select {
	case attached = <-ready:
	case <-time.After(5 * time.Second):
	}
	save(filepath.Join(out, "io-trace-status.json"), map[string]any{"ddl_sha256": hash, "attached": attached, "attach_start_ns": started.UnixNano(), "ready_ns": time.Now().UnixNano(), "method": "strace -f -ttt -T -yy -s0 pwrite64/fsync/fdatasync; attach after seed/depth scan. Diagnostic only; wall distribution perturbed."})
	return func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		_ = cmd.Wait()
		<-done
		save(filepath.Join(out, "io-trace-status.json"), map[string]any{"ddl_sha256": hash, "attached": attached, "attach_start_ns": started.UnixNano(), "detach_ns": time.Now().UnixNano(), "messages": lines, "method": "Diagnostic sampled CPU plus timed WAL syscalls; no primary qualification claim. Match epoch timestamps to operation intervals."})
	}
}
