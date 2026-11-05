package main

import (
	"database/sql"
	"time"
)

func nameMatch(db *sql.DB, id int, word string, gen int) bool {
	idx := bankName("catalog_people_names", gen)
	var hit int
	e := db.QueryRow("SELECT rowid FROM "+idx+" WHERE "+idx+" MATCH ? AND rowid=?", peopleMatch(word), id).Scan(&hit)
	if e == sql.ErrNoRows {
		return false
	}
	must(e)
	return true
}
func peopleControls(db *sql.DB, n int) {
	id := 900000001
	exec(db, "BEGIN IMMEDIATE")
	exec(db, "INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(?,?,?,?,?,?)", id, "q-person-guard", "q-person-guard", "Qpeoplealpha 王菲", "Qpeoplealpha 王菲", subjectBytes(id))
	exec(db, "INSERT INTO q_edges(owner_person,entity) VALUES(?,0)", id)
	witness(db, -1, id, 1, time.Now())
	exec(db, "COMMIT")
	d := capturePub(db, -1, id, 1, "Qpeoplealpha 王菲", time.Now())
	stagePub(db, []*pubDoc{d}, "people.control")
	assertQ(!nameMatch(db, id, "qpeoplealpha", 1), "staged People name hidden")
	flipPub(db, []*pubDoc{d}, "people.control")
	reclaimPub(db, []*pubDoc{d}, "people.control")
	assertQ(nameMatch(db, id, "qpeoplealpha", 1) && nameMatch(db, id, "王菲", 1), "published People Latin/CJK recipe")
	a := acceptPeople(db, []int{id}, []string{"Qpeoplebeta 山田"}, 1, "people.control")
	next := capturePub(db, -1, id, 1, "Qpeoplebeta 山田", a)
	stagePub(db, []*pubDoc{next}, "people.control")
	assertQ(nameMatch(db, id, "qpeoplealpha", 1) && !nameMatch(db, id, "qpeoplebeta", 1), "source change does not overwrite old accepted People tokens")
	exec(db, "BEGIN IMMEDIATE")
	exec(db, `INSERT INTO catalog_people_names(catalog_people_names,rowid,name) VALUES('delete',?,?)`, id, next.oldName)
	exec(db, "UPDATE search_publish_jobs SET published_document_id=?,published_generation=? WHERE id=?", next.doc, next.version, next.job)
	exec(db, "INSERT INTO catalog_people_names(rowid,name) VALUES(?,?)", id, next.nameTokens)
	exec(db, "ROLLBACK")
	assertQ(nameMatch(db, id, "qpeoplealpha", 1) && !nameMatch(db, id, "qpeoplebeta", 1), "People native/head rollback")
	flipPub(db, []*pubDoc{next}, "people.control")
	reclaimPub(db, []*pubDoc{next}, "people.control")
	assertQ(!nameMatch(db, id, "qpeoplealpha", 1) && nameMatch(db, id, "qpeoplebeta", 1), "People old-token replacement")
	for _, name := range []string{"", "Qpeoplegamma 王菲", "", "Qpeopledelta"} {
		accepted := acceptPeople(db, []int{id}, []string{name}, 1, "people.empty")
		d = capturePub(db, -1, id, 1, name, accepted)
		publishDocs(db, []*pubDoc{d}, "people.empty")
		var count int
		must(db.QueryRow("SELECT count(*) FROM catalog_people_names_docsize WHERE id=?", id).Scan(&count))
		assertQ(count == 1, "empty People content retains native row/docsize")
		assertQ(!nameMatch(db, id, "qpeoplebeta", 1), "empty People transition leaves no ghost")
		publisherIntegrity(db, "people-empty-"+name)
	}
	_, e := db.Exec("DELETE FROM catalog_people WHERE id=?", id)
	assertQ(e != nil, "People token custody blocks physical source deletion before native retirement")
	exec(db, "DELETE FROM q_edges WHERE owner_person=?", id)
	var visible int
	must(db.QueryRow("SELECT count(*) FROM q_edges WHERE owner_person=?", id).Scan(&visible))
	assertQ(visible == 0 && nameMatch(db, id, "qpeopledelta", 1), "orphan person hidden by visible-credit predicate while native retirement remains pending")
	old := capturePub(db, -1, id, 1, "", time.Now())
	exec(db, "BEGIN IMMEDIATE")
	exec(db, `INSERT INTO catalog_people_names(catalog_people_names,rowid,name) VALUES('delete',?,?)`, id, old.oldName)
	exec(db, `UPDATE search_publish_jobs SET published_document_id=NULL,published_generation=0,document_id=NULL,phase='retire' WHERE id=?`, old.job)
	exec(db, "COMMIT")
	reclaimPub(db, []*pubDoc{old}, "people.retire")
	exec(db, "DELETE FROM search_publish_jobs WHERE id=?", old.job)
	exec(db, "DELETE FROM catalog_people WHERE id=?", id)
	assertQ(!nameMatch(db, id, "qpeopledelta", 1), "People native reclaim before source deletion")
	publisherIntegrity(db, "people-controls")
	emit("people_controls", map[string]any{"staged_hidden": true, "source_without_name_fts": true, "old_accepted_tokens_retained": true, "native_and_head_rollback": true, "empty_nonempty_empty": true, "docsize_for_empty": true, "latin_and_cjk_match": true, "visible_credit_orphan_suppression": true, "native_retirement_before_source_delete": true, "foreign_keys_on": true})
}
