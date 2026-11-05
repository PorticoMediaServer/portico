package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	_ "modernc.org/sqlite"
	"os"
)

func must(e error) {
	if e != nil {
		panic(e)
	}
}
func main() {
	db, e := sql.Open("sqlite", os.Args[1]+"?mode=ro")
	must(e)
	defer db.Close()
	var version string
	must(db.QueryRow("SELECT sqlite_version()").Scan(&version))
	if version != "3.53.4" {
		panic(version)
	}
	var id int
	var source string
	must(db.QueryRow("SELECT id,overview FROM catalog_entities WHERE length(CAST(overview AS BLOB))>150000 LIMIT 1").Scan(&id, &source))
	r := []rune(facettext.SearchFold(source))
	q := string(r[200:202])
	encoded := fmt.Sprintf("%06x%06x", r[200], r[201])
	var hits, paired, siblings int
	must(db.QueryRow(`SELECT count(*),COALESCE(sum(instr(f.normalized,?)>0),0) FROM search_fragment_prefix z JOIN search_fragments f ON f.id=z.rowid JOIN search_publish_jobs j ON j.entity_id=f.owner_entity AND j.search_generation_id=f.search_generation_id AND j.published_generation=f.generation WHERE search_fragment_prefix MATCH ? AND f.owner_entity=?`, q, encoded, id).Scan(&hits, &paired))
	must(db.QueryRow(`SELECT count(*) FROM search_fragments f JOIN search_publish_jobs j ON j.entity_id=f.owner_entity AND j.search_generation_id=f.search_generation_id AND j.published_generation=f.generation WHERE f.owner_entity=? AND instr(f.normalized,?)>0`, id, q).Scan(&siblings))
	b, _ := json.Marshal(map[string]any{"sqlite": version, "entity": id, "term": q, "source_offset_runes": 200, "prefix_candidate_rows": hits, "same_fragment_substring_rows": paired, "accepted_sibling_substring_rows": siblings, "scope": "counterexample to treating independently packed token and normalized chunks as co-located; native hold benchmark is not full primary-query membership qualification"})
	fmt.Println(string(b))
}
