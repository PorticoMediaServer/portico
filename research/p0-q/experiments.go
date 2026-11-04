package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"os"
	"strings"
	"time"
)

//go:embed candidate.sql
var candidateSQL string

var candidateDigest = fmt.Sprintf("%x", sha256.Sum256([]byte(candidateSQL)))

func candidateSHA() string { return candidateDigest }
func configure(name string) {
	switch name {
	case "normal-passive":
		autoMerge = 0
		durability = "NORMAL"
		passiveEnabled = true
	case "normal-passive-250":
		autoMerge = 0
		durability = "NORMAL"
		passiveEnabled = true
		checkpointEvery = 250 * time.Millisecond
	case "normal-passive-1000":
		autoMerge = 0
		durability = "NORMAL"
		passiveEnabled = true
		checkpointEvery = time.Second
	case "normal-auto2":
		durability = "NORMAL"
		passiveEnabled = true
	case "normal-auto2-nospill":
		durability = "NORMAL"
		passiveEnabled = true
		cacheSpill = false
	case "normal-auto2-cache256":
		durability = "NORMAL"
		passiveEnabled = true
		cacheKiB = -262144
	case "full-cpu":
	case "baseline":
	case "noauto-b8":
		autoMerge = 0
	case "noauto-b1":
		autoMerge = 0
		buildBatch = 1
	case "noauto-normal-b1":
		autoMerge = 0
		buildBatch = 1
		durability = "NORMAL"
	case "noauto-crisis8-b1":
		autoMerge = 0
		buildBatch = 1
		crisisMerge = 8
	case "noauto-merge4-b1":
		autoMerge = 0
		buildBatch = 1
		mergePages = 4
	default:
		panic("unknown configuration: " + name)
	}
}
func matched(terms int) string {
	q := "SELECT v0.entity FROM vt0 v0 "
	for i := 1; i < terms; i++ {
		q += fmt.Sprintf("CROSS JOIN vt%d v%d ON v%d.entity=v0.entity ", i, i, i)
	}
	return q
}
func fastTitlePage(db *sql.DB, q Query) ([]int, string) {
	terms := facettext.SearchTerms(q.Text)
	parts := []string{}
	for _, t := range terms {
		parts = append(parts, "title_prefix_tokens:"+encode(t)+"*")
	}
	primary := strings.Join(parts, " AND ")
	initial := "initial_prefix_tokens:" + encode(terms[0]) + "*"
	ids := []int{}
	rows, e := db.Query("SELECT id FROM catalog_entities WHERE library_id IN(0,1,2,3,4,5,6,7) AND kind IN(1,2,3,4,5,6,7,8) AND title_fold=? ORDER BY id LIMIT 41", facettext.SearchFold(q.Text))
	must(e)
	for rows.Next() {
		var id int
		must(rows.Scan(&id))
		ids = append(ids, id)
	}
	rows.Close()
	for _, match := range []string{"(" + primary + ") AND " + initial, "(" + primary + ") NOT " + initial} {
		if len(ids) >= 41 {
			break
		}
		args := []any{match}
		sqlq := "SELECT f.rowid FROM search_own_prefix f CROSS JOIN catalog_entities s ON s.id=f.rowid WHERE search_own_prefix MATCH ? AND s.library_id IN(0,1,2,3,4,5,6,7) AND s.kind IN(1,2,3,4,5,6,7,8)"
		if len(ids) > 0 {
			ph := []string{}
			for _, id := range ids {
				ph = append(ph, "?")
				args = append(args, id)
			}
			sqlq += " AND f.rowid NOT IN(" + strings.Join(ph, ",") + ")"
		}
		sqlq += " ORDER BY f.rowid LIMIT ?"
		args = append(args, 41-len(ids))
		rows, e = db.Query(sqlq, args...)
		must(e)
		for rows.Next() {
			var id int
			must(rows.Scan(&id))
			ids = append(ids, id)
		}
		rows.Close()
	}
	return ids, primary
}
func hybridOnce(db *sql.DB, q Query) (float64, float64, map[string]any) {
	start := time.Now()
	exec(db, "BEGIN")
	titlePage, primaryMatch := fastTitlePage(db, q)
	firstMs := float64(time.Since(start).Microseconds()) / 1000
	// Copy the complete title-first population only after response values are ready.
	deferredStart := time.Now()
	exec(db, "CREATE TEMP TABLE IF NOT EXISTS ctitles(entity INTEGER PRIMARY KEY)")
	exec(db, "DELETE FROM ctitles")
	exec(db, "INSERT INTO ctitles SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ?", primaryMatch)
	var titles int
	must(db.QueryRow("SELECT count(*) FROM ctitles").Scan(&titles))
	_, contextPage := searchExcluding(db, "A", q, true)
	sqlset := matched(len(strings.Fields(q.Text)))
	var complete, context int
	must(db.QueryRow("SELECT count(*) FROM (" + sqlset + ")").Scan(&complete))
	must(db.QueryRow("SELECT count(*) FROM (" + sqlset + ") hits WHERE NOT EXISTS(SELECT 1 FROM ctitles t WHERE t.entity=hits.entity)").Scan(&context))
	if complete != titles+context {
		panic("hybrid partition lost or duplicated membership")
	}
	witnesses := map[int]string{0: "Harbor Lights", 1: "Northern Road", 2: "Blue River"}
	w := map[string]any{}
	for id, name := range witnesses {
		var a, t, c int
		must(db.QueryRow("SELECT count(*) FROM ("+sqlset+") WHERE entity=?", id).Scan(&a))
		must(db.QueryRow("SELECT count(*) FROM ctitles WHERE entity=?", id).Scan(&t))
		c = a - t
		w[name] = map[string]int{"A": a, "C_titles": t, "C_context": c}
	}
	// Complete context set remains in vt tables: ranked context page size is NOT a truncation.
	// Continuation is a separate runtime/API contract; this prototype proves the full set and partition.
	exec(db, "COMMIT")
	deferredMs := float64(time.Since(deferredStart).Microseconds()) / 1000
	return firstMs, deferredMs, map[string]any{"titles": titles, "context": context, "A_total": complete, "title_page": titlePage, "context_page": contextPage, "witnesses": w}
}
func hybridBench(db *sql.DB, samples int, phase string) {
	for _, text := range []string{"a", "al", "jo", "harbor ad"} {
		q := Query{Text: text}
		a, b, c, d := []float64{}, []float64{}, []float64{}, []float64{}
		var proof map[string]any
		for i := 0; i < samples+1; i++ {
			t := time.Now()
			search(db, "A", q)
			am := float64(time.Since(t).Microseconds()) / 1000
			t = time.Now()
			search(db, "B", q)
			bm := float64(time.Since(t).Microseconds()) / 1000
			cm, dm, p := hybridOnce(db, q)
			proof = p
			if i > 0 {
				a = append(a, am)
				b = append(b, bm)
				c = append(c, cm)
				d = append(d, dm)
			}
		}
		emit("hybrid_query", map[string]any{"phase": phase, "query": text, "A": stats(a), "B_dispatch_on_A_index": stats(b), "C_first_titles": stats(c), "C_deferred_complete": stats(d), "raw_ms": map[string]any{"A": a, "B": b, "C_first": c, "C_deferred": d}, "proof": proof})
	}
}
func tuneBench(db *sql.DB, samples int, phase string) {
	for _, q := range []Query{{Name: "prefix1", Text: "a"}, {Name: "prefix3", Text: "ada"}, {Name: "full_word", Text: "harbor"}, {Name: "cjk2", Text: "東京"}} {
		a := []float64{}
		for i := 0; i < samples+1; i++ {
			t := time.Now()
			search(db, "A", q)
			if i > 0 {
				a = append(a, float64(time.Since(t).Microseconds())/1000)
			}
		}
		emit("tune_query", map[string]any{"phase": phase, "query": q.Text, "latency": stats(a), "raw_ms": a})
	}
}
func dumpOperations() {
	nativeMax.Lock()
	defer nativeMax.Unlock()
	for tag, a := range nativeMax.values {
		emit("operation", map[string]any{"tag": tag, "latency": stats(a)})
	}
}
func experiment(db *sql.DB, path string, n, samples, rounds int, mode string) {
	bench := func(phase string) {
		if mode == "recipe" {
			return
		}
		if mode == "hybrid" {
			hybridBench(db, samples, phase)
		} else {
			tuneBench(db, samples, phase)
		}
	}
	if !mixedEnabled {
		bench("base")
	}
	shared := max(128, n/50)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		rd := open(path)
		defer rd.Close()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			q := Query{Text: []string{"a", "al", "jo"}[i%3]}
			t := time.Now()
			cpActive, cpSeq := cpRunning.Load(), cpSequence.Load()
			if mode == "hybrid" {
				first, deferred, proof := hybridOnce(rd, q)
				emit("concurrent_hybrid", map[string]any{"query": q.Text, "first_ms": first, "deferred_ms": deferred, "proof": proof})
			} else {
				if boundedReaders {
					func() { _, _, e := shortSuggestion(context.Background(), rd, q.Text, false); must(e) }()
				} else {
					search(rd, "A", q)
				}
				emit("concurrent_tune", map[string]any{"query": q.Text, "ms": float64(time.Since(t).Microseconds()) / 1000, "checkpoint_overlap": cpActive || cpRunning.Load() || cpSeq != cpSequence.Load()})
			}
			readerCount.Add(1)
			i++
			if boundedReaders {
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()
	peakWal := int64(0)
	churnOps := 0
	for i, started := 0, time.Now(); i < rounds || (mixedEnabled && time.Since(started) < mixedDuration); i++ {
		serviceForeground(db)
		admitPublication()
		measured("churn.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			if i < rounds {
				insertItem(db, n+i, shared)
			}
			renameTitle(db, (i*7919)%n, renamedTitle((i*7919)%n, i))
			renameFragment(db, 1+i%shared, renamedPerson(1+i%shared, i))
			native(db, "churn.commit", "COMMIT")
		})
		churnOps++
		if f, e := os.Stat(path + "-wal"); e == nil {
			peakWal = max(peakWal, f.Size())
		}
		maintain(db, "merge")
		if i%64 == 0 {
			emit("debt_churn", map[string]any{"round": i, "segments": debt(db), "wal_peak_bytes": peakWal})
			checkpoint(db)
		}
	}
	close(stop)
	<-done
	finishMixed(db)
	emit("debt_before_drain", debt(db))
	for i := 0; i < 256; i++ {
		merge(db, "drain")
	}
	checkpoint(db)
	emit("debt_after_drain", debt(db))
	emit("allocation_churn", allocation(db, path))
	if !mixedEnabled {
		bench("after_churn")
	}
	controls(db, "A")
	var body strings.Builder
	for j := 0; body.Len() < 65536; j++ {
		fmt.Fprintf(&body, "commonprefixterm commonprefix%06d ", j)
	}
	big := body.String()[:65536]
	for i := 0; i < 16; i++ {
		text := fmt.Sprintf("%04d", i) + big[4:]
		p, l, norm := prefix(text), facettext.SearchDocument(text), facettext.SearchFold(text)
		measured("max_subject.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			renameFragmentValues(db, 1, p, l, norm)
			native(db, "max_subject.commit", "COMMIT")
		})
		merge(db, "max_subject.merge")
	}
	checkpoint(db)
	emit("debt_final", debt(db))
	finishMixed(db)
	dumpOperations()
	emit("completed", map[string]any{"wal_peak_bytes": peakWal, "peak_same_level": maxDebt, "tombstone_operations": churnOps * 2, "churn_operations": churnOps, "new_items": min(churnOps, rounds)})
}
