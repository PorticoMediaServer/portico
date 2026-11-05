package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"sort"
	"strings"
	"time"
)

func peopleMatch(q string) string {
	terms := facettext.SearchTerms(q)
	m := []string{}
	for _, t := range terms {
		m = append(m, `"`+strings.ReplaceAll(t, `"`, `""`)+`"*`)
	}
	return strings.Join(m, " AND ")
}

// A synthetic1%-allowed viewer adapter, not the production authority/label oracle.
func allowedPeople(ctx context.Context, pr *policyReader) ([]int, error) {
	seen := map[int]bool{}
	last := -1
	for {
		ids := []int{}
		raw := 0
		e := pr.read(ctx, `WITH c AS MATERIALIZED(SELECT id FROM q_items INDEXED BY q_rating WHERE rating=0 AND id>? ORDER BY id LIMIT 64) SELECT c.id,e.retired FROM c LEFT JOIN q_source_entities e ON e.id=c.id ORDER BY c.id`, []any{last}, func(r *sql.Rows) error {
			var id int
			var retired sql.NullInt64
			if e := r.Scan(&id, &retired); e != nil {
				return e
			}
			last = id
			raw++
			if retired.Valid && retired.Int64 == 0 {
				ids = append(ids, id)
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		if len(ids) > 0 {
			le, lp := -1, -1
			for {
				n := 0
				e = pr.read(ctx, `SELECT x.entity,x.owner_person FROM q_edges x INDEXED BY q_edge_target WHERE x.entity IN(SELECT value FROM json_each(?)) AND x.owner_entity IS NULL AND x.owner_person IS NOT NULL AND (x.entity,x.owner_person)>(?,?) ORDER BY x.entity,x.owner_person LIMIT 128`, []any{encodedJSON(ids), le, lp}, func(r *sql.Rows) error {
					var entity, person int
					if e := r.Scan(&entity, &person); e != nil {
						return e
					}
					le, lp = entity, person
					n++
					seen[person] = true
					return nil
				})
				if e != nil {
					return nil, e
				}
				if n < 128 {
					break
				}
			}
		}
		if raw < 64 {
			break
		}
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, nil
}
func peopleSQL(index, q string, restricted bool, allowed []int) (string, []any) {
	fold := facettext.SearchFold(q)
	sqlq := `SELECT pe.id,(-bm25(` + index + `))+(CASE WHEN p.title_fold=? THEN 1000 ELSE 0 END)+(CASE WHEN substr(p.title_fold,1,length(?))=? THEN 100 ELSE 0 END) rel FROM ` + index + ` m CROSS JOIN catalog_people pe ON pe.id=m.rowid CROSS JOIN search_publish_jobs j ON j.person_id=pe.id AND j.search_generation_id=? CROSS JOIN search_publications p ON p.id=j.published_document_id WHERE ` + index + ` MATCH ?`
	args := []any{fold, fold, fold, currentSearchGeneration, peopleMatch(q)}
	if strings.ContainsFunc(q, facettext.CJK) {
		for _, t := range strings.Fields(fold) {
			if strings.ContainsFunc(t, facettext.CJK) {
				sqlq += ` AND instr(p.title_fold,?)>0`
				args = append(args, t)
			}
		}
	}
	if restricted {
		sqlq += ` AND pe.id IN(SELECT value FROM json_each(?))`
		args = append(args, encodedJSON(allowed))
	} else {
		sqlq += ` AND EXISTS(SELECT 1 FROM q_edges c INDEXED BY q_edge_person JOIN q_source_entities e ON e.id=c.entity WHERE c.owner_person=pe.id AND e.retired=0)`
	}
	sqlq += ` ORDER BY rel DESC,pe.sort_name,pe.id LIMIT 20`
	return sqlq, args
}
func peopleOracle(db *sql.DB) {
	exec(db, `CREATE VIRTUAL TABLE q_people_oracle USING fts5(name,tokenize='unicode61 remove_diacritics 2',prefix='2 3 4')`)
	after := -1
	for {
		type row struct {
			id   int
			name string
		}
		batch := []row{}
		rows, e := db.Query("SELECT id,name FROM catalog_people WHERE id>? ORDER BY id LIMIT 64", after)
		must(e)
		for rows.Next() {
			var r row
			must(rows.Scan(&r.id, &r.name))
			batch = append(batch, r)
			after = r.id
		}
		must(rows.Close())
		if len(batch) == 0 {
			break
		}
		admitPublication()
		exec(db, "BEGIN")
		for _, r := range batch {
			exec(db, "INSERT INTO q_people_oracle(rowid,name) VALUES(?,?)", r.id, facettext.SearchDocument(r.name))
		}
		exec(db, "COMMIT")
	}
	emit("people_oracle", map[string]any{"source": "legacy trigger portico_search_text(name) = facettext.SearchDocument(name)", "scope": "untimed internal-content control, same bundled engine; compares complete ranked top20 and scores"})
}
func peopleReadBench(db *sql.DB, samples int) {
	connectionState(db, "people-read-start")
	exec(db, "CREATE INDEX q_rating ON q_items(rating,id)")
	peopleOracle(db)
	for _, q := range []string{"a", "jo", "ada", "ada lovelace", "王", "王菲", "山田", "1984", "nebuchadnezzar"} {
		for _, restricted := range []bool{false, true} {
			refPr := &policyReader{db: db}
			allowed := []int{}
			var e error
			if restricted {
				allowed, e = allowedPeople(context.Background(), refPr)
				must(e)
			}
			refPr.close()
			refQ, refArgs := peopleSQL("q_people_oracle", q, restricted, allowed)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			rows, e := db.QueryContext(ctx, refQ, refArgs...)
			must(e)
			expected := []int{}
			scores := []float64{}
			for rows.Next() {
				var id int
				var score float64
				must(rows.Scan(&id, &score))
				expected = append(expected, id)
				scores = append(scores, score)
			}
			must(rows.Err())
			must(rows.Close())
			cancel()
			lat, prep, snaps := []float64{}, []float64{}, []float64{}
			failures := []string{}
			var ids []int
			for rep := 0; rep < samples; rep++ {
				p := &policyReader{db: db}
				start := time.Now()
				allowed = nil
				if restricted {
					allowed, e = allowedPeople(context.Background(), p)
					if e != nil {
						failures = append(failures, e.Error())
						p.close()
						continue
					}
				}
				prepared := time.Since(start)
				sqlq, args := peopleSQL(bankName("catalog_people_names", currentSearchGeneration), q, restricted, allowed)
				ids = nil
				scoreValues := []float64{}
				e = p.read(context.Background(), sqlq, args, func(r *sql.Rows) error {
					var id int
					var score float64
					if e := r.Scan(&id, &score); e != nil {
						return e
					}
					ids = append(ids, id)
					scoreValues = append(scoreValues, score)
					return nil
				})
				if e != nil {
					failures = append(failures, e.Error())
				} else {
					assertQ(len(ids) == len(expected), "People legacy top20 length")
					for i, id := range ids {
						assertQ(id == expected[i] && scoreValues[i] == scores[i], "People legacy rank/score parity")
					}
					lat = append(lat, float64(time.Since(start).Microseconds())/1000)
					prep = append(prep, float64(prepared.Microseconds())/1000)
				}
				snaps = append(snaps, p.snapshots...)
				p.close()
			}
			connectionState(db, fmt.Sprintf("people-read:%s:restricted=%v", q, restricted))
			emit("people_query", map[string]any{"query": q, "explicit_short_action": shortInput(q), "restricted": restricted, "samples_requested": samples, "latency": stats(lat), "raw_ms": lat, "visibility_prepare": stats(prep), "snapshots": stats(snaps), "failures": failures, "ids": ids, "rank_and_score_oracle": true, "ranking": "existing -bm25 + exact1000 + name-prefix100; sort_name/person ID tie", "scope": "match/rank/visible-credit filtering; no portrait/roles/credit-count HTTP hydration; no exact total before ready"})
		}
	}
	exec(db, "DROP TABLE q_people_oracle")
}
func acceptPeople(db *sql.DB, ids []int, names []string, gen int, tag string) time.Time {
	measured(tag+".accept.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		for i, id := range ids {
			exec(db, "UPDATE catalog_people SET name=?,sort_name=?,revision=revision+1 WHERE id=?", names[i], names[i], id)
			witness(db, -1, id, gen, time.Now())
		}
		exec(db, "UPDATE search_revision SET input_sequence=input_sequence+1 WHERE singleton=1")
		native(db, tag+".accept.commit", "COMMIT")
	})
	return time.Now()
}
func peopleBench(db *sql.DB, n, rounds, samples int) {
	var count int
	must(db.QueryRow("SELECT count(*) FROM catalog_people").Scan(&count))
	emit("people_population", map[string]any{"items": n, "people": count, "items_per_person": peopleDivisor, "cjk_share_percent": cjkShare, "engine": "bundled modernc3.53.4", "canonical": candidateSHA(), "previous_schema": "97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393"})
	peopleReadBench(db, samples)
	peopleControls(db, count)
	largest := 0
	for _, batch := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		tag := fmt.Sprintf("people.b%d", batch)
		start := time.Now()
		done := 0
		for r := 0; r < rounds; r++ {
			admitPublication()
			ids := []int{}
			names := []string{}
			for k := 0; k < batch; k++ {
				id := 1 + (stageSequence*17)%count
				stageSequence++
				ids = append(ids, id)
				names = append(names, renamedPerson(id, stageSequence)+" "+marker(stageSequence))
			}
			accepted := acceptPeople(db, ids, names, 1, tag)
			docs := []*pubDoc{}
			for i, id := range ids {
				docs = append(docs, capturePub(db, -1, id, 1, names[i], accepted))
			}
			publishDocs(db, docs, tag)
			done += len(docs)
			if r >= 15 && (txStat(tag+".stage.tx").Max > 25 || txStat(tag+".flip.tx").Max > 25 || txStat(tag+".reclaim.tx").Max > 25) {
				break
			}
		}
		if phaseFits(tag) {
			largest = batch
		}
		emit("people_capacity", map[string]any{"batch_people": batch, "documents": done, "stage": txStat(tag + ".stage.tx"), "head_and_name_fts": txStat(tag + ".flip.tx"), "reclaim": txStat(tag + ".reclaim.tx"), "source_without_fts": txStat(tag + ".accept.tx"), "strict_observed_pass": phaseFits(tag), "stage_p99_upper95": upper99(tag + ".stage.tx"), "head_p99_upper95": upper99(tag + ".flip.tx"), "reclaim_p99_upper95": upper99(tag + ".reclaim.tx"), "end_to_end_people_per_s": float64(done) / time.Since(start).Seconds()})
	}
	peopleFlow(db, count, max(1, min(largest, 8)), n)
	if n < 10000 || (n >= 1000000 && cjkShare == 30 && generationRun) {
		generationBench(db, n, 8)
	}
	publisherIntegrity(db, "people-final")
	emit("people_completed", map[string]any{"items": n, "people": count, "external_name_fts": true})
}
