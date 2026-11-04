package main

import (
	"database/sql"
	"fmt"
	"time"
)

func assertQ(ok bool, name string) {
	if !ok {
		panic("publisher control: " + name)
	}
}
func fragmentMatch(db *sql.DB, fid int, word string, eligible bool) bool {
	q := `SELECT z.rowid FROM search_fragment_lexical z WHERE search_fragment_lexical MATCH ? AND z.rowid=?`
	if eligible {
		q = `SELECT z.rowid FROM search_fragment_lexical z JOIN search_fragments f ON f.id=z.rowid JOIN search_publish_jobs j ON j.entity_id=f.owner_entity AND j.search_generation_id=f.search_generation_id AND j.published_generation=f.generation WHERE search_fragment_lexical MATCH ? AND z.rowid=?`
	}
	var id int
	e := db.QueryRow(q, word, fid).Scan(&id)
	if e == sql.ErrNoRows {
		return false
	}
	must(e)
	return true
}
func publisherControls(db *sql.DB, n int) {
	id := n + 1000001
	exec(db, "BEGIN IMMEDIATE")
	exec(db, "INSERT INTO q_items VALUES(?,99)", id)
	insertEntity(db, id, 1, -1, "Qguardalpha", "", "")
	witness(db, id, 0, 1, time.Now())
	exec(db, "COMMIT")
	a := capturePub(db, id, 0, 1, "Qguardalpha", time.Now())
	stagePub(db, []*pubDoc{a}, "control.initial")
	assertQ(!matchOwn(db, id, "qguardalpha", 1, false), "unpublished own absent")
	assertQ(fragmentMatch(db, a.frag, "qguardalpha", false), "staged lexical row indexed")
	assertQ(!fragmentMatch(db, a.frag, "qguardalpha", true), "staged fragment ineligible")
	flipPub(db, []*pubDoc{a}, "control.initial")
	reclaimPub(db, []*pubDoc{a}, "control.initial")
	assertQ(matchOwn(db, id, "qguardalpha", 1, true), "initial searchable")
	accepted := acceptChanges(db, []int{id}, []string{"Qguardbeta"}, []int{1}, "control.rename")
	b := capturePub(db, id, 0, 1, "Qguardbeta", accepted)
	stagePub(db, []*pubDoc{b}, "control.rename")
	assertQ(matchOwn(db, id, "qguardalpha", 1, true) && !matchOwn(db, id, "qguardbeta", 1, true), "source rename keeps accepted old tokens until publication")
	exec(db, "BEGIN IMMEDIATE")
	exec(db, `INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',?,?,?)`, id, b.oldP, b.oldInit)
	exec(db, `UPDATE search_publish_jobs SET published_document_id=?,published_generation=? WHERE id=?`, b.doc, b.version, b.job)
	exec(db, `INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, id, b.p, b.init)
	exec(db, "ROLLBACK")
	assertQ(matchOwn(db, id, "qguardalpha", 1, true) && !matchOwn(db, id, "qguardbeta", 1, true), "native/head rollback restores accepted source")
	flipPub(db, []*pubDoc{b}, "control.rename")
	assertQ(!matchOwn(db, id, "qguardalpha", 1, true) && matchOwn(db, id, "qguardbeta", 1, true), "atomic replacement/head")
	assertQ(fragmentMatch(db, a.frag, "qguardalpha", false) && !fragmentMatch(db, a.frag, "qguardalpha", true), "old native fragment retained but ineligible before reclaim")
	reclaimPub(db, []*pubDoc{b}, "control.rename")
	assertQ(!fragmentMatch(db, a.frag, "qguardalpha", false), "old lexical native reclaim")
	for _, title := range []string{"", "Qguardgamma", "", "Qguarddelta"} {
		accepted = acceptChanges(db, []int{id}, []string{title}, []int{1}, "control.empty")
		d := capturePub(db, id, 0, 1, title, accepted)
		publishDocs(db, []*pubDoc{d}, "control.empty")
		assertQ(!matchOwn(db, id, "qguardbeta", 1, true), "no ghost old word after empty transition")
		var row int
		must(db.QueryRow("SELECT count(*) FROM search_own_prefix_docsize WHERE id=?", id).Scan(&row))
		assertQ(row == 1, "empty content keeps native row/docsize")
		publisherIntegrity(db, "empty-transition-"+title)
	}
	exec(db, "UPDATE catalog_entities SET retired=1 WHERE id=?", id)
	assertQ(matchOwn(db, id, "qguarddelta", 1, false) && !matchOwn(db, id, "qguarddelta", 1, true), "live predicate suppresses retired native hit")
	exec(db, "UPDATE catalog_entities SET retired=0 WHERE id=?", id)
	exec(db, "DELETE FROM catalog_entities WHERE id=?", id)
	assertQ(!matchOwn(db, id, "qguarddelta", 1, true), "deleted source excluded while retained subject owns old tokens")
	var held int
	must(db.QueryRow("SELECT count(*) FROM search_publications WHERE id=(SELECT published_document_id FROM search_publish_jobs WHERE entity_id=? AND search_generation_id=1)", id).Scan(&held))
	assertQ(held == 1, "deleted source preserves indexed image custody")
	var job, doc, version int
	var p, init string
	must(db.QueryRow(`SELECT j.id,j.published_document_id,j.published_generation,p.title_prefix_tokens,p.initial_prefix_tokens FROM search_publish_jobs j JOIN search_publications p ON p.id=j.published_document_id WHERE j.entity_id=? AND j.search_generation_id=1`, id).Scan(&job, &doc, &version, &p, &init))
	old := &pubDoc{entity: id, job: job, oldDoc: doc, searchGen: 1}
	rows, e := db.Query("SELECT id FROM search_fragments WHERE owner_entity=? AND generation=? AND search_generation_id=1", id, version)
	must(e)
	for rows.Next() {
		var fid int
		must(rows.Scan(&fid))
		old.oldFrags = append(old.oldFrags, fid)
	}
	must(rows.Close())
	exec(db, "BEGIN IMMEDIATE")
	exec(db, `INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',?,?,?)`, id, p, init)
	exec(db, `UPDATE search_publish_jobs SET published_document_id=NULL,published_generation=0,document_id=NULL,phase='retire' WHERE id=?`, job)
	exec(db, "COMMIT")
	reclaimPub(db, []*pubDoc{old}, "control.retire")
	assertQ(!matchOwn(db, id, "qguarddelta", 1, false), "retirement physically removes native own")
	publisherIntegrity(db, "controls-complete")
	emit("publisher_controls", map[string]any{"source_no_fts": true, "staged_invisible": true, "native_staged_visible_only_without_head_filter": true, "own_and_head_rollback": true, "old_fragment_reclaim": true, "empty_nonempty_empty": true, "empty_docsize": true, "retired_live_filter": true, "deleted_live_filter": true, "retained_subject_token_custody": true, "physical_retirement": true, "foreign_keys_on": true, "limit": "synthetic authority/rating and direct-edge adapter; full production source-cap/provenance oracle remains separate"})
	fmt.Sprint(id)
}
