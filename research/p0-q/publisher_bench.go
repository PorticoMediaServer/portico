package main

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"time"
)

func acceptChanges(db *sql.DB, ids []int, titles []string, gens []int, tag string) time.Time {
	folds := make([]string, len(titles))
	for i, t := range titles {
		folds[i] = facettext.SearchFold(t)
	}
	measured(tag+".accept.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		for j, id := range ids {
			exec(db, `UPDATE catalog_entities SET title=?,sort_key=?,sort_key_keep_articles=?,revision=revision+1 WHERE id=?`, titles[j], folds[j], folds[j], id)
			for _, g := range gens {
				witness(db, id, 0, g, time.Now())
			}
		}
		exec(db, "UPDATE search_revision SET input_sequence=input_sequence+1 WHERE singleton=1")
		native(db, tag+".accept.commit", "COMMIT")
	})
	return time.Now()
}
func currentTitle(db *sql.DB, id int) string {
	var t string
	must(db.QueryRow("SELECT title FROM catalog_entities WHERE id=?", id).Scan(&t))
	return t
}
func txStat(tag string) Stat {
	nativeMax.Lock()
	a := append([]float64{}, nativeMax.values[tag]...)
	nativeMax.Unlock()
	return stats(a)
}
func phaseFits(tag string) bool {
	for _, p := range []string{"stage", "flip", "reclaim"} {
		s := txStat(tag + "." + p + ".tx")
		if s.N < 128 || s.P99 > 10 || s.Max > 25 {
			return false
		}
	}
	nativeMax.Lock()
	defer nativeMax.Unlock()
	for key, values := range nativeMax.values {
		if strings.HasPrefix(key, tag+".merge.tx.") {
			s := stats(values)
			if s.P99 > 10 || s.Max > 25 {
				return false
			}
		}
	}
	return true
}
func marker(i int) string { return fmt.Sprintf("qpub%08d", i%100000000) }
func matchOwn(db *sql.DB, id int, term string, gen int, live bool) bool {
	idx := bankName("search_own_prefix", gen)
	q := "SELECT rowid FROM " + idx + " WHERE " + idx + " MATCH ? AND rowid=?"
	if live {
		q = "SELECT z.rowid FROM " + idx + " z JOIN catalog_entities e ON e.id=z.rowid WHERE " + idx + " MATCH ? AND z.rowid=? AND e.retired=0"
	}
	var n int
	e := db.QueryRow(q, "title_prefix_tokens:"+encode(term)+"*", id).Scan(&n)
	if e == sql.ErrNoRows {
		return false
	}
	must(e)
	return true
}
func verifyPublished(db *sql.DB, docs []*pubDoc, tag string) []float64 {
	lags := []float64{}
	for _, d := range docs {
		terms := strings.Fields(facettext.SearchFold(d.title))
		if len(terms) == 0 {
			continue
		}
		token := terms[len(terms)-1]
		if !matchOwn(db, d.entity, token, d.searchGen, true) {
			panic("published native MATCH missing " + token)
		}
		assertQ(!d.visible.IsZero(), "native visibility timestamp")
		lags = append(lags, d.visible.Sub(d.accepted).Seconds())
	}
	emit("publisher_lag", map[string]any{"phase": tag, "lag_seconds": lags})
	return lags
}
func publisherBench(db *sql.DB, n, rounds int) {
	shortOnlyBench(db, 128)
	publisherControls(db, n)
	largest := 0
	batches := []int{1, 2, 4, 8, 16, 32, 64, 128, 256}
	if n < 10000 {
		batches = batches[:5]
	}
	for _, batch := range batches {
		tag := fmt.Sprintf("publisher.b%d", batch)
		start := time.Now()
		docsN := 0
		cycles := max(rounds, 128)
		rejected := false
		for r := 0; r < cycles; r++ {
			admitPublication()
			ids := []int{}
			titles := []string{}
			for k := 0; k < batch; k++ {
				id := (stageSequence * 7919) % n
				stageSequence++
				ids = append(ids, id)
				titles = append(titles, renamedTitle(id, stageSequence)+" "+marker(stageSequence))
			}
			accepted := acceptChanges(db, ids, titles, []int{1}, tag)
			docs := []*pubDoc{}
			for j, id := range ids {
				docs = append(docs, capturePub(db, id, 0, 1, titles[j], accepted))
			}
			publishDocs(db, docs, tag)
			docsN += len(docs)
			if r%32 == 0 {
				verifyPublished(db, docs, tag)
			}
			if r >= 15 {
				for _, p := range []string{"stage", "flip", "reclaim"} {
					s := txStat(tag + "." + p + ".tx")
					if s.Max > 25 {
						rejected = true
					}
				}
				if rejected {
					break
				}
			}
		}
		passed := phaseFits(tag)
		if passed {
			largest = batch
		}
		emit("publisher_capacity", map[string]any{"batch_documents": batch, "documents": docsN, "cycles": txStat(tag + ".stage.tx").N, "stage": txStat(tag + ".stage.tx"), "stage_p99_upper95": upper99(tag + ".stage.tx"), "head_p99_upper95": upper99(tag + ".flip.tx"), "reclaim_p99_upper95": upper99(tag + ".reclaim.tx"), "head_flip": txStat(tag + ".flip.tx"), "reclaim": txStat(tag + ".reclaim.tx"), "source_accept": txStat(tag + ".accept.tx"), "elapsed_s": time.Since(start).Seconds(), "end_to_end_docs_per_s": float64(docsN) / time.Since(start).Seconds(), "strict_observed_pass": passed, "early_rejected_max_over25": rejected, "policy": "NORMAL;auto0;crisis16;governedmerge1;full source witnesses;staged image+fragment;atomic native own+head"})
	}
	emit("publisher_choice", map[string]any{"largest_tested_strict_pass": largest, "warning": "per-VM finite sample; shared-host stalls and merge/foreground gates remain separate"})
	flowBatch := max(1, largest)
	publisherFlow(db, n, flowBatch)
	if n < 10000 || (n >= 1000000 && cjkShare == 30 && generationRun) {
		generationBench(db, n, min(flowBatch, 8))
	} else {
		emit("generation_scheduled_elsewhere", map[string]any{"scope": "full1M30%CJK both profiles plus local1k; per-subject flips/reclaim measured every capacity cell"})
	}
	publisherIntegrity(db, "final")
	emit("publisher_completed", map[string]any{"items": n, "largest_tested_strict_pass": largest, "generation_flip": true, "empty_controls": true})
}
func publisherFlow(db *sql.DB, n, batch int) {
	stopReader := startPublisherReader()
	defer stopReader()
	rate := 100.0
	if strings.Contains(profileName, "old8g") {
		rate = 40
	}
	duration := 100 * time.Second
	if mixedEnabled {
		duration = mixedDuration
	}
	if n < 10000 {
		duration = 3 * time.Second
	}
	start := time.Now()
	maxSourceQueue := 0
	sourceAdmission := []float64{}
	next := start
	granted := start
	total := 0
	queue := []*pubDoc{}
	lags := []float64{}
	maxQueue := 0
	for time.Since(start) < duration || len(queue) > 0 || total < int(duration.Seconds()*rate) {
		now := time.Now()
		scheduled := min(int(now.Sub(start).Seconds()*rate), int(duration.Seconds()*rate))
		due := scheduled - total
		maxSourceQueue = max(maxSourceQueue, due)
		if due > 0 {
			count := min(8, due)
			ids := []int{}
			titles := []string{}
			for j := 0; j < count; j++ {
				id := ((total + j) * 7919) % n
				ids = append(ids, id)
				titles = append(titles, renamedTitle(id, total+j)+" "+marker(90000000+total+j))
			}
			accepted := acceptChanges(db, ids, titles, []int{1}, "flow")
			for j, id := range ids {
				arrival := start.Add(time.Duration(float64(total+j) / rate * float64(time.Second)))
				sourceAdmission = append(sourceAdmission, float64(accepted.Sub(arrival).Microseconds())/1000)
				queue = append(queue, capturePub(db, id, 0, 1, titles[j], accepted))
			}
			total += count
			next = start.Add(time.Duration(float64(total) / rate * float64(time.Second)))
			maxQueue = max(maxQueue, len(queue))
		}
		_ = next

		if len(queue) > 0 && !time.Now().Before(granted) {
			admitPublication()
			k := min(batch, len(queue))
			docs := queue[:k]
			publishDocs(db, docs, "flow")
			lags = append(lags, verifyPublished(db, docs, "flow")...)
			queue = queue[k:]
			granted = time.Now().Add(2 * time.Millisecond)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	emit("publisher_flow", map[string]any{"source_target_docs_per_s": rate, "accepted_documents": total, "elapsed_s": time.Since(start).Seconds(), "actual_docs_per_s": float64(total) / time.Since(start).Seconds(), "publisher_batch": batch, "publisher_grant_gap_ms": 2, "max_pending_subjects": maxQueue, "max_source_arrival_backlog": maxSourceQueue, "source_admission_ms": stats(sourceAdmission), "lag_seconds_raw": lags, "lag_seconds": map[string]float64{"p50": stats(lags).P50, "p95": stats(lags).P95, "p99": stats(lags).P99, "max": stats(lags).Max}, "scope": "single writer, continuously interleaved source acceptance and governed publisher; no separate write lock"})
}
func publisherIntegrity(db *sql.DB, phase string) {
	connectionState(db, "integrity:"+phase)
	start := time.Now()
	for _, idx := range indexes {
		exec(db, "INSERT INTO "+idx+"("+idx+",rank) VALUES('integrity-check',1)")
	}
	rows, e := db.Query("PRAGMA foreign_key_check")
	must(e)
	bad := rows.Next()
	must(rows.Close())
	if bad {
		panic("foreign key check")
	}
	var ok string
	must(db.QueryRow("PRAGMA integrity_check").Scan(&ok))
	if ok != "ok" {
		panic(ok)
	}
	emit("publisher_integrity", map[string]any{"phase": phase, "all_external_content_rank1": true, "foreign_keys": true, "integrity": ok, "diagnostic_ms": float64(time.Since(start).Microseconds()) / 1000, "scope": "test-only full-index diagnostic; not a runtime publisher hold"})
}
