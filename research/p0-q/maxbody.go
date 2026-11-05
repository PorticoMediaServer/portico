package main

import (
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"strings"
	"time"
	"unicode/utf8"
)

// Deterministic high-entropy words. Repeating one long clipped prefix would
// understate the number of distinct dictionary entries and native flush work.
func stressText(chars, revision int, cjk bool) string {
	var b strings.Builder
	x := uint64(0x517cc1b727220a95) ^ uint64(revision+1)
	for n := 0; n < chars; n++ {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		if cjk {
			b.WriteRune(rune(0x4e00 + x%20000))
		} else if n%13 == 12 {
			b.WriteByte(' ')
		} else {
			b.WriteByte(byte('a' + x%26))
		}
	}
	return b.String()
}
func maxBodyBench(db *sql.DB, n, rounds int) {
	type test struct {
		name              string
		chars             int
		cjk, person, body bool
	}
	cases := []test{{"title-latin1000", 1000, false, false, false}, {"person-latin1000", 1000, false, true, false}, {"person-cjk1000", 1000, true, true, false}, {"fragment-latin4096", 4096, false, false, true}, {"fragment-latin16384", 16384, false, false, true}, {"fragment-latin65536", 65536, false, false, true}, {"fragment-cjk65536-runes", 65536, true, false, true}}
	emit("maximum_body_scope", map[string]any{"source": "high-entropy deterministic payloads; catalogue overview witnesses and accepted native fragment data; People raw name uses original SearchDocument CJK recipe", "limits": "lexical/prefix SearchDocument clips input to65536bytes; normalized legacy SQL substr limit65536characters; these are deliberately separate", "fixture": "one fragment per document as current harness; negative does not rule out bounded fragment subdivision", "canonical": candidateSHA(), "previous_schema": "97dc083d437e4da0aaab9b277bdc9c8f5dace1450a2a5e754579867916352393"})
	for ci, c := range cases {
		tag := "maximum." + c.name
		id := (ci * 7919) % n
		person := 1 + ci
		var last *pubDoc
		for r := 0; r < rounds; r++ {
			value := stressText(c.chars, ci*100000+r, c.cjk)
			title := value
			admitPublication()
			var accepted time.Time
			if c.person {
				accepted = acceptPeople(db, []int{person}, []string{value}, 1, tag)
				last = capturePub(db, -1, person, 1, value, accepted)
			} else {
				if c.body {
					title = fmt.Sprintf("Q maximum body control %d", ci)
				}
				measured(tag+".accept.tx", func() {
					exec(db, "BEGIN IMMEDIATE")
					body := ""
					if c.body {
						body = value
					}
					exec(db, "UPDATE catalog_entities SET title=?,sort_key=?,sort_key_keep_articles=?,overview=?,revision=revision+1 WHERE id=?", title, facettext.SearchFold(title), facettext.SearchFold(title), body, id)
					witness(db, id, 0, 1, time.Now())
					exec(db, "UPDATE search_revision SET input_sequence=input_sequence+1 WHERE singleton=1")
					native(db, tag+".accept.commit", "COMMIT")
				})
				accepted = time.Now()
				last = capturePub(db, id, 0, 1, title, accepted)
				if c.body {
					last.lex = facettext.SearchDocument(value)
					last.fold = facettext.SearchFold(value)
					last.fragPrefix = prefix(value)
				}
			}
			publishDocs(db, []*pubDoc{last}, tag)
			if r >= 15 && (txStat(tag+".stage.tx").Max > 25 || txStat(tag+".flip.tx").Max > 25 || txStat(tag+".reclaim.tx").Max > 25) {
				break
			}
		}
		emit("maximum_body", map[string]any{"case": c.name, "source_characters": c.chars, "source_utf8_bytes": len(stressText(c.chars, ci*100000, c.cjk)), "lexical_bytes": len(last.lex), "normalized_bytes": len(last.fold), "normalized_runes": utf8.RuneCountInString(last.fold), "fragment_prefix_bytes": len(last.fragPrefix), "own_prefix_bytes": len(last.p), "prepared_person_name_bytes": len(last.nameTokens), "source_accept": txStat(tag + ".accept.tx"), "stage": txStat(tag + ".stage.tx"), "head": txStat(tag + ".flip.tx"), "reclaim": txStat(tag + ".reclaim.tx"), "stage_p99_upper95": upper99(tag + ".stage.tx"), "head_p99_upper95": upper99(tag + ".flip.tx"), "reclaim_p99_upper95": upper99(tag + ".reclaim.tx"), "strict_observed_pass": phaseFits(tag), "debt": debt(db)})
	}
	publisherIntegrity(db, "maximum-body-final")
	emit("maximum_body_completed", map[string]any{"items": n, "cases": len(cases)})
}
