package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"time"
)

func createBank(db *sql.DB, gen int) {
	ownView := fmt.Sprintf("q_bank%d_own_content", gen)
	fragView := fmt.Sprintf("q_bank%d_fragment_content", gen)
	ddl := fmt.Sprintf(`CREATE VIEW %s AS SELECT j.entity_id id,p.title_prefix_tokens,p.initial_prefix_tokens FROM search_publish_jobs j JOIN search_publications p ON p.id=j.published_document_id WHERE j.entity_id IS NOT NULL AND j.search_generation_id=%d AND p.generation=j.published_generation; CREATE VIEW %s AS SELECT * FROM search_fragments WHERE search_generation_id=%d;`, ownView, gen, fragView, gen)
	for _, x := range []struct{ base, cols, tokenizer, prefix, view string }{{"search_own_prefix", "title_prefix_tokens,initial_prefix_tokens", "ascii", "6 12 18 24 30 36 42 48 54 60 66 72", ownView}, {"search_fragment_prefix", "prefix_tokens", "ascii", "6 12 18 24 30 36 42 48 54 60 66 72", fragView}, {"search_fragment_lexical", "lexical_tokens", "unicode61 remove_diacritics 2", "2 3 4", fragView}, {"search_fragment_grams", "normalized", "trigram", "", fragView}} {
		name := bankName(x.base, gen)
		ddl += "CREATE VIRTUAL TABLE " + name + " USING fts5(" + x.cols + ",content='" + x.view + "',content_rowid='id',tokenize='" + x.tokenizer + "'"
		if x.prefix != "" {
			ddl += ",prefix='" + x.prefix + "'"
		}
		ddl += ");"
	}
	measured("generation.create_bank.tx", func() { exec(db, ddl) })
	for _, base := range []string{"search_own_prefix", "search_fragment_prefix", "search_fragment_lexical", "search_fragment_grams"} {
		idx := bankName(base, gen)
		indexes = append(indexes, idx)
		for _, kv := range []struct {
			k string
			v int
		}{{"automerge", 0}, {"crisismerge", 16}, {"usermerge", 2}} {
			exec(db, "INSERT INTO "+idx+"("+idx+",rank) VALUES(?,?)", kv.k, kv.v)
		}
	}
	emit("generation_bank_ddl", map[string]any{"generation": gen, "ddl": ddl, "sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))), "canonical_input": candidateSHA(), "scope": "runtime-created bank with same frozen FTS declarations; reader dispatches by active head"})
}
func generationBench(db *sql.DB, n, batch int) {
	start := time.Now()
	exec(db, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms,captured_input_sequence) SELECT 2,'v2',?,'modernc1.58.0-SQLite3.53.4','bundled-3.53.4+facettext-pin','building',?,input_sequence FROM search_revision WHERE singleton=1`, recipeVersion, time.Now().UnixMilli())
	exec(db, "UPDATE search_revision SET building_generation_id=2 WHERE singleton=1")
	createBank(db, 2)
	// Source context identities, not expanded descendants.
	rows, e := db.Query("SELECT id FROM catalog_people ORDER BY id")
	must(e)
	people := []int{}
	for rows.Next() {
		var id int
		must(rows.Scan(&id))
		people = append(people, id)
	}
	must(rows.Close())
	for _, person := range people {
		admitPublication()
		exec(db, "BEGIN IMMEDIATE")
		witness(db, -1, person, 2, time.Now())
		native(db, "generation.capture.commit", "COMMIT")
		var name string
		must(db.QueryRow("SELECT name FROM catalog_people WHERE id=?", person).Scan(&name))
		publishDocs(db, []*pubDoc{capturePub(db, -1, person, 2, name, time.Now())}, "generation.context")
	}
	rows, e = db.Query("SELECT id FROM catalog_entities WHERE kind=11 ORDER BY id")
	must(e)
	collections := []int{}
	for rows.Next() {
		var id int
		must(rows.Scan(&id))
		collections = append(collections, id)
	}
	must(rows.Close())
	for _, id := range collections {
		exec(db, "BEGIN IMMEDIATE")
		witness(db, id, 0, 2, time.Now())
		native(db, "generation.capture.commit", "COMMIT")
		publishDocs(db, []*pubDoc{capturePub(db, id, 0, 2, currentTitle(db, id), time.Now())}, "generation.context")
	}
	updates := 0
	for after := -1; ; {
		rows, e := db.Query("SELECT e.id FROM q_items q JOIN catalog_entities e ON e.id=q.id WHERE e.id>? ORDER BY e.id LIMIT ?", after, batch)
		must(e)
		ids := []int{}
		for rows.Next() {
			var id int
			must(rows.Scan(&id))
			ids = append(ids, id)
			after = id
		}
		must(rows.Close())
		if len(ids) == 0 {
			break
		}
		admitPublication()
		exec(db, "BEGIN IMMEDIATE")
		for _, id := range ids {
			witness(db, id, 0, 2, time.Now())
		}
		native(db, "generation.capture.commit", "COMMIT")
		docs := []*pubDoc{}
		for _, id := range ids {
			docs = append(docs, capturePub(db, id, 0, 2, currentTitle(db, id), time.Now()))
		}
		publishDocs(db, docs, "generation.backfill")
		if after%1000 < batch {
			id := after / 2
			t := renamedTitle(id, after) + " " + marker(80000000+after)
			a := acceptChanges(db, []int{id}, []string{t}, []int{1, 2}, "generation.dual_source")
			for _, g := range []int{1, 2} {
				publishDocs(db, []*pubDoc{capturePub(db, id, 0, g, t, a)}, "generation.dual_publish")
			}
			updates++
		}
	}
	var fence int
	must(db.QueryRow("SELECT input_sequence FROM search_revision WHERE singleton=1").Scan(&fence))
	checked := 0
	pr := &policyReader{db: db}
	for after := -1; ; {
		count := 0
		e := pr.read(context.Background(), `SELECT e.id,e.revision,j.source_revision,j.input_generation,j.published_generation,e.title,p.title FROM q_source_entities e JOIN q_items q ON q.id=e.id JOIN search_publish_jobs j ON j.entity_id=e.id AND j.search_generation_id=2 JOIN search_publications p ON p.id=j.published_document_id WHERE e.id>? ORDER BY e.id LIMIT 128`, []any{after}, func(r *sql.Rows) error {
			var id, source, job, input, published int
			var liveTitle, pubTitle string
			if e := r.Scan(&id, &source, &job, &input, &published, &liveTitle, &pubTitle); e != nil {
				return e
			}
			if source != job || input != published || liveTitle != pubTitle {
				return fmt.Errorf("generation source/head fence mismatch")
			}
			after = id
			count++
			checked++
			return nil
		})
		must(e)
		if count < 128 {
			break
		}
	}
	measured("generation.flip.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		exec(db, "UPDATE search_generations SET state='retired' WHERE id=1")
		exec(db, "UPDATE search_generations SET state='active',pending_subjects=0 WHERE id=2")
		r, e := db.Exec("UPDATE search_revision SET active_generation_id=2,building_generation_id=NULL,revision=revision+1 WHERE singleton=1 AND input_sequence=?", fence)
		must(e)
		k, e := r.RowsAffected()
		must(e)
		assertQ(k == 1, "generation source fence CAS")
		native(db, "generation.flip.commit", "COMMIT")
	})
	currentSearchGeneration = 2
	assertQ(checked == n, "generation covers every live item")
	emit("generation_flip", map[string]any{"from": 1, "to": 2, "live_items_checked": checked, "dual_source_changes_during_backfill": updates, "elapsed_s": time.Since(start).Seconds(), "flip": txStat("generation.flip.tx"), "preflight_snapshots": stats(pr.snapshots), "source_fence": fence, "recipe": recipeVersion, "native_rowids": "entity ID in both banks", "old_reader_leases": "none active in this control; production retirement lease protocol remains separate"})
	pr.close()
	// Reclaim old bank in bounded document transactions, retaining old strings
	// until its native deletes have physically returned. Old generation is inactive.
	retired := 0
	for {
		rows, e := db.Query(`SELECT id,entity_id,person_id,published_document_id,published_generation FROM search_publish_jobs WHERE search_generation_id=1 AND published_document_id IS NOT NULL ORDER BY id LIMIT ?`, batch)
		must(e)
		docs := []*pubDoc{}
		for rows.Next() {
			d := &pubDoc{searchGen: 1}
			var entity, person sql.NullInt64
			must(rows.Scan(&d.job, &entity, &person, &d.oldDoc, &d.version))
			if entity.Valid {
				d.entity = int(entity.Int64)
			} else {
				d.person = int(person.Int64)
			}
			docs = append(docs, d)
		}
		must(rows.Close())
		if len(docs) == 0 {
			break
		}
		admitPublication()
		for _, d := range docs {
			must(db.QueryRow("SELECT title_prefix_tokens,initial_prefix_tokens FROM search_publications WHERE id=?", d.oldDoc).Scan(&d.oldP, &d.oldInit))
			col, id := "owner_entity", d.entity
			if d.person > 0 {
				col, id = "owner_person", d.person
			}
			rs, e := db.Query("SELECT id FROM search_fragments WHERE "+col+"=? AND search_generation_id=1 AND generation=?", id, d.version)
			must(e)
			for rs.Next() {
				var fid int
				must(rs.Scan(&fid))
				d.oldFrags = append(d.oldFrags, fid)
			}
			must(rs.Close())
		}
		measured("generation.retire_head.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			for _, d := range docs {
				if d.person == 0 {
					exec(db, `INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',?,?,?)`, d.entity, d.oldP, d.oldInit)
				}
				exec(db, `UPDATE search_publish_jobs SET published_document_id=NULL,published_generation=0,document_id=NULL,phase='retire' WHERE id=?`, d.job)
			}
			native(db, "generation.retire_head.commit", "COMMIT")
		})
		reclaimPub(db, docs, "generation.retire")
		maintain(db, "generation.retire.merge")
		retired += len(docs)
	}
	emit("generation_reclaim", map[string]any{"documents": retired, "native_old_head": txStat("generation.retire_head.tx"), "fragment_reclaim": txStat("generation.retire.reclaim.tx"), "allocated": allocation(db, databasePath), "physical_drop": "not used; native tombstones/segments repaid by governed merge1; database free pages remain reusable"})

}
