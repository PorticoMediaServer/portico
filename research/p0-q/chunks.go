package main

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"time"
	"unicode/utf8"
)

func splitTokens(s string, budget int) []string {
	out := []string{}
	part := ""
	for _, t := range strings.Fields(s) {
		if len(part) > 0 && len(part)+1+len(t) > budget {
			out = append(out, part)
			part = ""
		}
		if part != "" {
			part += " "
		}
		part += t
	}
	if part != "" {
		out = append(out, part)
	}
	return out
}
func splitNormalized(s string) []string {
	r := []rune(s)
	out := []string{}
	// Search input is currently <=128 Unicode scalars. An overlap of512 folded
	// scalars covers the pinned fold's expansion for tested Latin/CJK queries.
	// Exhaustive Unicode normalization/phrase compatibility remains a build gate.
	for start := 0; start < len(r); start += 512 {
		out = append(out, string(r[start:min(len(r), start+1024)]))
	}
	return out
}
func stageChunks(db *sql.DB, d *pubDoc, tag string) int {
	ps, ls, ns := splitTokens(d.fragPrefix, 1024), splitTokens(d.lex, 1024), splitNormalized(d.fold)
	count := max(1, len(ps), len(ls), len(ns))
	get := func(a []string, i int) string {
		if i < len(a) {
			return a[i]
		}
		return ""
	}
	measured(tag+".image.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		var entity, person, lib, kind any
		if d.person > 0 {
			person = d.person
		} else {
			entity = d.entity
			lib = d.lib
			kind = d.kind
		}
		r, e := db.Exec(`INSERT INTO search_publications(entity_id,person_id,search_generation_id,title,person_name_tokens,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title_fold,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, entity, person, d.searchGen, d.title, d.nameTokens, lib, kind, d.version, subjectBytes(max(d.entity, d.person)), d.sourceVersion, recipeVersion, facettext.SearchFold(d.title), d.p, d.init)
		must(e)
		id, e := r.LastInsertId()
		must(e)
		d.doc = int(id)
		exec(db, `UPDATE search_publish_jobs SET document_id=?,phase='stage',expected_fragments=?,staged_fragments=0,staged_bytes=0,job_revision=job_revision+1 WHERE id=? AND input_generation=?`, d.doc, count, d.job, d.version)
		native(db, tag+".image.commit", "COMMIT")
	})
	peak := 0
	for i := 0; i < count; i++ {
		p, l, n := get(ps, i), get(ls, i), get(ns, i)
		peak = max(peak, len(p)+len(l)+len(n))
		admitPublication()
		measured(tag+".fragment.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			var entity, person any
			field := 1
			if d.person > 0 {
				person = d.person
				field = 2
			} else {
				entity = d.entity
			}
			r, e := db.Exec(`INSERT INTO search_fragments(owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, entity, person, field, i, fmt.Sprint(d.sourceVersion), p, l, n, d.version, d.searchGen)
			must(e)
			fid, e := r.LastInsertId()
			must(e)
			for _, x := range []struct{ idx, col, value string }{{"search_fragment_prefix", "prefix_tokens", p}, {"search_fragment_lexical", "lexical_tokens", l}, {"search_fragment_grams", "normalized", n}} {
				idx := bankName(x.idx, d.searchGen)
				native(db, tag+".fragment."+idx, "INSERT INTO "+idx+"(rowid,"+x.col+") VALUES(?,?)", fid, x.value)
			}
			phase := "stage"
			if i+1 == count {
				phase = "publish"
			}
			exec(db, `UPDATE search_publish_jobs SET staged_fragments=staged_fragments+1,staged_bytes=staged_bytes+?,after_ordinal=?,phase=?,job_revision=job_revision+1 WHERE id=? AND input_generation=?`, len(p)+len(l)+len(n), i, phase, d.job, d.version)
			native(db, tag+".fragment.commit", "COMMIT")
		})
		maintain(db, tag+".merge")
	}
	emit("chunk_shape", map[string]any{"tag": tag, "fragments": count, "peak_prepared_fragment_bytes": peak, "token_budget": 1024, "normalized_core_runes": 512, "normalized_overlap_runes": 512, "atomic_token_exception": "a single lexical token is retained whole even if over1024bytes; its measured hold is separate from a hard byte guarantee"})
	return count
}
func reclaimChunks(db *sql.DB, d *pubDoc, tag string) {
	for _, fid := range d.oldFrags {
		admitPublication()
		measured(tag+".fragment_reclaim.tx", func() {
			exec(db, "BEGIN IMMEDIATE")
			var p, l, n string
			must(db.QueryRow("SELECT prefix_tokens,lexical_tokens,normalized FROM search_fragments WHERE id=?", fid).Scan(&p, &l, &n))
			for _, x := range []struct{ idx, col, value string }{{"search_fragment_prefix", "prefix_tokens", p}, {"search_fragment_lexical", "lexical_tokens", l}, {"search_fragment_grams", "normalized", n}} {
				idx := bankName(x.idx, d.searchGen)
				native(db, tag+".fragment_reclaim."+idx, "INSERT INTO "+idx+"("+idx+",rowid,"+x.col+") VALUES('delete',?,?)", fid, x.value)
			}
			exec(db, "DELETE FROM search_fragments WHERE id=?", fid)
			native(db, tag+".fragment_reclaim.commit", "COMMIT")
		})
		maintain(db, tag+".merge")
	}
	admitPublication()
	measured(tag+".image_reclaim.tx", func() {
		exec(db, "BEGIN IMMEDIATE")
		if d.oldDoc > 0 {
			exec(db, "DELETE FROM search_publications WHERE id=?", d.oldDoc)
		}
		exec(db, "UPDATE search_publish_jobs SET phase='idle',job_next_attempt_ms=NULL,job_revision=job_revision+1 WHERE id=?", d.job)
		native(db, tag+".image_reclaim.commit", "COMMIT")
	})
}
func chunkControls(db *sql.DB, d *pubDoc, tag string) {
	// Exact token-set reconstruction, not merely equal result counts.
	p, l := []string{}, []string{}
	rows, e := db.Query(`SELECT prefix_tokens,lexical_tokens FROM search_fragments WHERE owner_entity=? AND generation=? AND search_generation_id=? ORDER BY ordinal`, d.entity, d.version, d.searchGen)
	must(e)
	for rows.Next() {
		var a, b string
		must(rows.Scan(&a, &b))
		p = append(p, strings.Fields(a)...)
		l = append(l, strings.Fields(b)...)
	}
	must(rows.Err())
	must(rows.Close())
	assertQ(strings.Join(p, " ") == strings.Join(strings.Fields(d.fragPrefix), " "), "chunk prefix token sequence")
	assertQ(strings.Join(l, " ") == strings.Join(strings.Fields(d.lex), " "), "chunk lexical token sequence")
	exec(db, "CREATE VIRTUAL TABLE q_chunk_oracle USING fts5(normalized,tokenize='trigram')")
	exec(db, "INSERT INTO q_chunk_oracle(rowid,normalized) VALUES(1,?)", d.fold)
	r := []rune(d.fold)
	tested := 0
	for _, length := range []int{3, 12, 128, 384} {
		for _, at := range []int{500, 510, 1000, 1020, 2040} {
			if at+length > len(r) {
				continue
			}
			q := `"` + strings.ReplaceAll(string(r[at:at+length]), `"`, `""`) + `"`
			var a, b int
			must(db.QueryRow("SELECT count(*) FROM q_chunk_oracle WHERE q_chunk_oracle MATCH ?", q).Scan(&a))
			idx := bankName("search_fragment_grams", d.searchGen)
			must(db.QueryRow("SELECT count(*) FROM "+idx+" m JOIN search_fragments f ON f.id=m.rowid WHERE "+idx+" MATCH ? AND f.owner_entity=? AND f.generation=? AND f.search_generation_id=?", q, d.entity, d.version, d.searchGen).Scan(&b))
			assertQ((a > 0) == (b > 0), "chunk trigram boundary phrase")
			tested++
		}
	}
	exec(db, "DROP TABLE q_chunk_oracle")
	emit("chunk_controls", map[string]any{"tag": tag, "prefix_token_sequence": true, "lexical_token_sequence": true, "boundary_phrase_cases": tested, "scope": "tested Latin/CJK normalized substrings through384runes; exhaustive Unicode expansion, full virtual recipe/authority and every approved fallback remain build gates"})
}
func chunkBench(db *sql.DB, n, rounds int) {
	cases := []struct {
		name  string
		chars int
		cjk   bool
	}{{"latin16384", 16384, false}, {"latin65536", 65536, false}, {"cjk65536-runes", 65536, true}}
	for ci, c := range cases {
		tag := "chunks." + c.name
		start := time.Now()
		total := 0
		for r := 0; r < rounds; r++ {
			body := stressText(c.chars, ci*100000+r, c.cjk)
			title := fmt.Sprintf("Q chunk body %d", ci)
			id := ci * 7919 % n
			admitPublication()
			measured(tag+".accept.tx", func() {
				exec(db, "BEGIN IMMEDIATE")
				exec(db, "UPDATE catalog_entities SET title=?,overview=?,revision=revision+1 WHERE id=?", title, body, id)
				witness(db, id, 0, 1, time.Now())
				exec(db, "UPDATE search_revision SET input_sequence=input_sequence+1 WHERE singleton=1")
				native(db, tag+".accept.commit", "COMMIT")
			})
			d := capturePub(db, id, 0, 1, title, time.Now())
			d.lex = facettext.SearchDocument(body)
			d.fold = facettext.SearchFold(body)
			d.fragPrefix = prefix(body)
			total += stageChunks(db, d, tag)
			admitPublication()
			flipPub(db, []*pubDoc{d}, tag)
			reclaimChunks(db, d, tag)
			if r == 0 || r+1 == rounds {
				chunkControls(db, d, tag)
			}
		}
		fields := map[string]any{"case": c.name, "documents": rounds, "fragments": total, "elapsed_s": time.Since(start).Seconds(), "documents_per_s": float64(rounds) / time.Since(start).Seconds(), "source_characters": c.chars, "source_utf8_bytes": len(stressText(c.chars, 0, c.cjk)), "source_valid_utf8": utf8.ValidString(stressText(c.chars, 0, c.cjk)), "debt": debt(db)}
		for _, p := range []string{"accept", "image", "fragment", "flip", "fragment_reclaim", "image_reclaim"} {
			fields[p] = txStat(tag + "." + p + ".tx")
			fields[p+"_p99_upper95"] = upper99(tag + "." + p + ".tx")
		}
		emit("chunk_capacity", fields)
	}
	publisherIntegrity(db, "chunked-body-final")
	emit("chunk_completed", map[string]any{"items": n, "rounds": rounds, "schema_overlay": false})
}
