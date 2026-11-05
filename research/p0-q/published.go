package main

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"time"
)

var publishedSchema = strings.Contains(candidateSQL, "CREATE TABLE search_publications(")
var currentSearchGeneration = 1
var generationRun = true
var peopleDivisor = 50
var peopleBank = strings.Contains(candidateSQL, "person_name_tokens")

const recipeVersion = "A-CJK-uni-bi-v1"

var stageSequence int

func exactTitleSQL() string {
	if !publishedSchema {
		return `SELECT id,title_fold,retired,kind FROM catalog_entities INDEXED BY catalog_entities_search_exact WHERE library_id IN(0,1,2,3,4,5,6,7) AND kind IN(1,2,3,4,5,6,7,8) AND title_fold=? AND id>? ORDER BY id LIMIT 64`
	}
	return `SELECT p.entity_id,p.title_fold,e.retired,p.kind FROM search_publications p INDEXED BY search_publications_exact CROSS JOIN search_publish_jobs j ON j.published_document_id=p.id CROSS JOIN q_search_entities e ON e.id=p.entity_id WHERE p.search_generation_id=(SELECT active_generation_id FROM search_revision WHERE singleton=1) AND p.library_id IN(0,1,2,3,4,5,6,7) AND p.kind IN(1,2,3,4,5,6,7,8) AND p.title_fold=? AND p.entity_id>? ORDER BY p.entity_id LIMIT 64`
}
func createPublished(db *sql.DB) {
	exec(db, "PRAGMA journal_mode=WAL")
	exec(db, "PRAGMA synchronous=NORMAL")
	exec(db, "PRAGMA wal_autocheckpoint=0")
	exec(db, candidateSQL)
	for i := 0; i < 8; i++ {
		exec(db, "INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,?,?)", i, fmt.Sprintf("q-library-%d", i), fmt.Sprintf("/fixture/%d", i))
	}
	for i := 1; i <= 11; i++ {
		exec(db, "INSERT INTO catalog_kinds VALUES(?,?,1,1,1,1,0)", i, fmt.Sprintf("fixture-kind-%d", i))
	}
	exec(db, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms) VALUES(1,'v1',?,'modernc1.58.0-SQLite3.53.4',?,'active',?)`, recipeVersion, engineFingerprint, time.Now().UnixMilli())
	exec(db, "INSERT INTO search_revision(singleton,revision,active_generation_id) VALUES(1,1,1)")
	exec(db, "CREATE TABLE q_items(id INTEGER PRIMARY KEY,rating INTEGER NOT NULL) STRICT")
	exec(db, `CREATE TABLE rel(parent INTEGER NOT NULL,child INTEGER NOT NULL,PRIMARY KEY(parent,child)) WITHOUT ROWID`)
	exec(db, `CREATE INDEX rel_child ON rel(child,parent)`)
	exec(db, `CREATE TABLE q_edges(owner_entity INTEGER REFERENCES catalog_subjects(id),owner_person INTEGER REFERENCES catalog_people(id),entity INTEGER NOT NULL REFERENCES catalog_subjects(id),CHECK((owner_entity IS NULL)<>(owner_person IS NULL)))`)
	exec(db, `CREATE UNIQUE INDEX q_edge_person ON q_edges(owner_person,entity) WHERE owner_person IS NOT NULL`)
	exec(db, `CREATE UNIQUE INDEX q_edge_entity ON q_edges(owner_entity,entity) WHERE owner_entity IS NOT NULL`)
	exec(db, `CREATE INDEX q_edge_target ON q_edges(entity,owner_entity,owner_person)`)
	exec(db, `CREATE VIEW q_source_entities AS SELECT * FROM catalog_entities`)
	exec(db, `CREATE VIEW q_search_entities AS SELECT e.*,p.title_fold,p.title_prefix_tokens,p.initial_prefix_tokens FROM catalog_entities e JOIN q_items q ON q.id=e.id JOIN search_publish_jobs j ON j.entity_id=e.id AND j.search_generation_id=(SELECT active_generation_id FROM search_revision WHERE singleton=1) JOIN search_publications p ON p.id=j.published_document_id`)
	exec(db, `CREATE VIEW edges AS SELECT f.id fragment,x.entity FROM search_fragments f JOIN search_publish_jobs j ON j.person_id=f.owner_person AND j.search_generation_id=f.search_generation_id AND j.published_generation=f.generation JOIN q_edges x ON x.owner_person=f.owner_person WHERE f.owner_person IS NOT NULL AND f.search_generation_id=(SELECT active_generation_id FROM search_revision WHERE singleton=1) UNION ALL SELECT f.id fragment,x.entity FROM search_fragments f JOIN search_publish_jobs j ON j.entity_id=f.owner_entity AND j.search_generation_id=f.search_generation_id AND j.published_generation=f.generation JOIN q_edges x ON x.owner_entity=f.owner_entity WHERE f.owner_entity IS NOT NULL AND f.search_generation_id=(SELECT active_generation_id FROM search_revision WHERE singleton=1)`)

	if peopleBank {
		indexes = append(indexes, "catalog_people_names")
	}
	for _, x := range indexes {
		for _, kv := range []struct {
			k string
			v int
		}{{"automerge", 0}, {"crisismerge", 16}, {"usermerge", 2}} {
			exec(db, "INSERT INTO "+x+"("+x+",rank) VALUES(?,?)", kv.k, kv.v)
		}
	}
}

type pubDoc struct {
	visible                                                               time.Time
	sourceVersion                                                         int
	entity, person, job, doc, frag, version, searchGen, kind, lib, oldDoc int
	oldFrags                                                              []int
	title, p, init, fold, lex, fragPrefix                                 string
	nameTokens, oldName                                                   string
	oldP, oldInit                                                         string
	accepted                                                              time.Time
}

func subjectBytes(i int) []byte {
	b := make([]byte, 16)
	for k := 0; k < 8; k++ {
		b[k] = byte(uint64(i) >> (8 * k))
	}
	copy(b[8:], "Qfixture")
	return b
}
func witness(db *sql.DB, entity, person, gen int, at time.Time) {
	var e, p any
	revision := 1
	table := "catalog_entities"
	subject := entity
	if person > 0 {
		table = "catalog_people"
		subject = person
	}
	must(db.QueryRow("SELECT revision FROM "+table+" WHERE id=?", subject).Scan(&revision))
	key, col, id := "entity_id", "entity_id", entity
	if person > 0 {
		p = person
		key = "person_id"
		col = key
		id = person
	} else {
		e = entity
	}
	exec(db, `INSERT INTO search_publish_jobs(entity_id,person_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase,job_next_attempt_ms) VALUES(?,?,?,1,?,?,?,'capture',?) ON CONFLICT(`+key+`,search_generation_id) DO UPDATE SET input_generation=input_generation+1,source_revision=excluded.source_revision,phase='capture',job_next_attempt_ms=excluded.job_next_attempt_ms,job_revision=job_revision+1`, e, p, gen, subjectBytes(id), revision, recipeVersion, at.UnixMilli())
	_ = col
}
func capturePub(db *sql.DB, entity, person, gen int, title string, accepted time.Time) *pubDoc {
	d := &pubDoc{entity: entity, person: person, title: title, p: prefix(title), init: initial(title), fold: facettext.SearchFold(title), lex: facettext.SearchDocument(title), searchGen: gen, accepted: accepted}
	col, id := "entity_id", entity
	if person > 0 {
		col, id = "person_id", person
		d.fragPrefix = d.p
		d.nameTokens = d.lex
		d.p = ""
		d.init = ""
	}
	var old sql.NullInt64
	must(db.QueryRow("SELECT id,input_generation,published_document_id,source_revision FROM search_publish_jobs WHERE "+col+"=? AND search_generation_id=?", id, gen).Scan(&d.job, &d.version, &old, &d.sourceVersion))
	if old.Valid {
		d.oldDoc = int(old.Int64)
		must(db.QueryRow("SELECT title_prefix_tokens,initial_prefix_tokens FROM search_publications WHERE id=?", d.oldDoc).Scan(&d.oldP, &d.oldInit))
		if peopleBank && person > 0 {
			must(db.QueryRow("SELECT person_name_tokens FROM search_publications WHERE id=?", d.oldDoc).Scan(&d.oldName))
		}
		rows, e := db.Query("SELECT id FROM search_fragments WHERE "+strings.Replace("owner_"+col, "_id", "", 1)+"=? AND search_generation_id=? AND generation=(SELECT published_generation FROM search_publish_jobs WHERE id=?)", id, gen, d.job)
		must(e)
		for rows.Next() {
			var fid int
			must(rows.Scan(&fid))
			d.oldFrags = append(d.oldFrags, fid)
		}
		must(rows.Close())
	}
	if entity >= 0 && person == 0 {
		must(db.QueryRow("SELECT library_id,kind FROM catalog_entities WHERE id=?", entity).Scan(&d.lib, &d.kind))
		if d.kind == 11 {
			d.fragPrefix = d.p
		}
	}
	return d
}
func stagePub(db *sql.DB, docs []*pubDoc, tag string) {
	measured(tag+".stage.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		for _, d := range docs {
			var e, p, lib, kind any
			field := 1
			if d.person > 0 {
				p = d.person
				field = 2
			} else {
				e = d.entity
				lib = d.lib
				kind = d.kind
				if d.kind == 11 {
					field = 3
				}
			}
			r, err := db.Exec(`INSERT INTO search_publications(entity_id,person_id,search_generation_id,title,person_name_tokens,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e, p, d.searchGen, d.title, d.nameTokens, lib, kind, d.version, subjectBytes(max(d.entity, d.person)), d.sourceVersion, recipeVersion, d.fold, d.p, d.init)
			must(err)
			id, err := r.LastInsertId()
			must(err)
			d.doc = int(id)
			r, err = db.Exec(`INSERT INTO search_fragments(owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id) VALUES(?,?,?,0,?,?,?,?,?,?)`, e, p, field, fmt.Sprint(d.sourceVersion), d.fragPrefix, d.lex, d.fold, d.version, d.searchGen)
			must(err)
			id, err = r.LastInsertId()
			must(err)
			d.frag = int(id)
			for _, x := range []struct{ idx, col, value string }{{"search_fragment_prefix", "prefix_tokens", d.fragPrefix}, {"search_fragment_lexical", "lexical_tokens", d.lex}, {"search_fragment_grams", "normalized", d.fold}} {
				idx := bankName(x.idx, d.searchGen)
				native(db, tag+".stage."+idx, "INSERT INTO "+idx+"(rowid,"+x.col+") VALUES(?,?)", d.frag, x.value)
			}
			exec(db, `UPDATE search_publish_jobs SET document_id=?,phase='publish',expected_fragments=1,staged_fragments=1,staged_bytes=?,job_revision=job_revision+1 WHERE id=? AND input_generation=?`, d.doc, len(d.p)+len(d.lex)+len(d.fold), d.job, d.version)
		}
		native(db, tag+".stage.commit", "COMMIT")
	})
}
func flipPub(db *sql.DB, docs []*pubDoc, tag string) {
	measured(tag+".flip.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		for _, d := range docs {
			idx := bankName("search_own_prefix", d.searchGen)
			if d.person > 0 && d.oldDoc > 0 && peopleBank {
				idx := bankName("catalog_people_names", d.searchGen)
				native(db, tag+".person.delete", "INSERT INTO "+idx+"("+idx+",rowid,name) VALUES('delete',?,?)", d.person, d.oldName)
			}
			if d.person == 0 && d.oldDoc > 0 {
				native(db, tag+".own.delete", "INSERT INTO "+idx+"("+idx+",rowid,title_prefix_tokens,initial_prefix_tokens) VALUES('delete',?,?,?)", d.entity, d.oldP, d.oldInit)
			}
			r, e := db.Exec(`UPDATE search_publish_jobs SET published_document_id=?,published_generation=?,phase='reclaim',job_revision=job_revision+1 WHERE id=? AND input_generation=? AND document_id=?`, d.doc, d.version, d.job, d.version, d.doc)
			must(e)
			n, e := r.RowsAffected()
			must(e)
			if n != 1 {
				panic("stale publisher CAS")
			}
			if d.person > 0 && peopleBank {
				idx := bankName("catalog_people_names", d.searchGen)
				native(db, tag+".person.insert", "INSERT INTO "+idx+"(rowid,name) VALUES(?,?)", d.person, d.nameTokens)
			}
			if d.person == 0 {
				native(db, tag+".own.insert", "INSERT INTO "+idx+"(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)", d.entity, d.p, d.init)
			}
		}
		native(db, tag+".flip.commit", "COMMIT")
	})
	for _, d := range docs {
		if d.person > 0 && peopleBank {
			idx := bankName("catalog_people_names", d.searchGen)
			tokens := strings.Fields(d.nameTokens)
			var id int
			if len(tokens) > 0 {
				must(db.QueryRow("SELECT rowid FROM "+idx+" WHERE "+idx+" MATCH ? AND rowid=?", `"`+tokens[0]+`"*`, d.person).Scan(&id))
			} else {
				must(db.QueryRow("SELECT id FROM "+idx+"_docsize WHERE id=?", d.person).Scan(&id))
			}
			d.visible = time.Now()
		}
		if d.person == 0 {
			idx := bankName("search_own_prefix", d.searchGen)
			tokens := strings.Fields(d.p)
			var id int
			if len(tokens) > 0 {
				must(db.QueryRow("SELECT rowid FROM "+idx+" WHERE "+idx+" MATCH ? AND rowid=?", "title_prefix_tokens:"+tokens[0], d.entity).Scan(&id))
			} else {
				must(db.QueryRow("SELECT id FROM "+idx+"_docsize WHERE id=?", d.entity).Scan(&id))
			}
			d.visible = time.Now()
		}
	}

}
func reclaimPub(db *sql.DB, docs []*pubDoc, tag string) {
	measured(tag+".reclaim.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		for _, d := range docs {
			for _, fid := range d.oldFrags {
				var p, l, norm string
				must(db.QueryRow("SELECT prefix_tokens,lexical_tokens,normalized FROM search_fragments WHERE id=?", fid).Scan(&p, &l, &norm))
				for _, x := range []struct{ idx, col, v string }{{"search_fragment_prefix", "prefix_tokens", p}, {"search_fragment_lexical", "lexical_tokens", l}, {"search_fragment_grams", "normalized", norm}} {
					idx := bankName(x.idx, d.searchGen)
					native(db, tag+".reclaim."+idx, "INSERT INTO "+idx+"("+idx+",rowid,"+x.col+") VALUES('delete',?,?)", fid, x.v)
				}
				exec(db, "DELETE FROM search_fragments WHERE id=?", fid)
			}
			if d.oldDoc > 0 {
				native(db, tag+".image.delete", "DELETE FROM search_publications WHERE id=?", d.oldDoc)
			}
			exec(db, "UPDATE search_publish_jobs SET phase='idle',job_next_attempt_ms=NULL,job_revision=job_revision+1 WHERE id=?", d.job)
		}
		native(db, tag+".reclaim.commit", "COMMIT")
	})
}
func bankName(name string, gen int) string {
	if gen == 1 {
		return name
	}
	return fmt.Sprintf("q_bank%d_%s", gen, name)
}
func publishDocs(db *sql.DB, docs []*pubDoc, tag string) {
	admitPublication()
	serviceForeground(db)
	stagePub(db, docs, tag)
	admitPublication()
	serviceForeground(db)
	flipPub(db, docs, tag)
	admitPublication()
	serviceForeground(db)
	reclaimPub(db, docs, tag)
	maintain(db, tag+".merge")
}
func buildPublished(db *sql.DB, n int) {
	shared := max(128, n/peopleDivisor)
	start := time.Now()
	for i := 1; i <= shared+max(1, shared/10); i++ {
		person, entity, t := i, -1, personName(i)
		measured("seed.context.accept.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			if i <= shared {
				exec(db, "INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation) VALUES(?,?,?,?,?,?)", i, fmt.Sprint(i), fmt.Sprint(i), t, t, subjectBytes(i))
			} else {
				person = 0
				entity = 300000000 + i
				t = collectionName(i - shared)
				insertEntity(db, entity, 11, -1, t, "", "")
			}
			witness(db, entity, person, 1, time.Now())
			native(db, "seed.context.accept.commit", "COMMIT")
		})
		d := capturePub(db, entity, person, 1, t, time.Now())
		publishDocs(db, []*pubDoc{d}, "seed.context")
	}
	for i := 0; i < n; {
		admitPublication()
		begin := i
		accepted := time.Now()
		measured("accept.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			for end := min(n, i+8); i < end; i++ {
				kind, parent := shape(i)
				exec(db, "INSERT INTO q_items VALUES(?,?)", i, i%100)
				insertEntity(db, i, kind, parent, title(i), "", "")
				if parent >= 0 {
					exec(db, "INSERT INTO rel VALUES(?,?)", parent, i)
				}
				for j := 0; j < 2; j++ {
					exec(db, "INSERT OR IGNORE INTO q_edges(owner_person,entity) VALUES(?,?)", 1+(i*17+j*43)%shared, i)
				}
				exec(db, "INSERT OR IGNORE INTO q_edges(owner_entity,entity) VALUES(?,?)", 300000000+shared+1+(i/100)%max(1, shared/10), i)
				if i == 0 {
					exec(db, "INSERT OR IGNORE INTO q_edges(owner_person,entity) VALUES(8,0)")
				}
				if i == 1 {
					exec(db, "INSERT OR IGNORE INTO q_edges(owner_person,entity) VALUES(2,1)")
				}
				if i == 2 {
					exec(db, "INSERT OR IGNORE INTO q_edges(owner_entity,entity) VALUES(?,2)", 300000000+shared+4)
				}
				witness(db, i, 0, 1, accepted)
			}
			exec(db, "UPDATE search_revision SET input_sequence=input_sequence+1 WHERE singleton=1")
			native(db, "accept.commit", "COMMIT")
		})
		accepted = time.Now()
		docs := []*pubDoc{}
		for j := begin; j < i; j++ {
			docs = append(docs, capturePub(db, j, 0, 1, title(j), accepted))
		}
		publishDocs(db, docs, "seed.entity")
		if i%100000 == 0 {
			emit("build_progress", map[string]any{"items": i, "elapsed_s": time.Since(start).Seconds()})
		}
	}
	emit("build_done", map[string]any{"items": n, "elapsed_s": time.Since(start).Seconds(), "model": "ef4b source+witness;stage;own-token replacement+head;native reclaim", "foreign_keys": true})
}
