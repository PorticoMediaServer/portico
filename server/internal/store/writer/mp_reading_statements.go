package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mpReadingSavedBytes  stmt.ID = "mp.reading_intent.saved_bytes"
	mpReadingSavedRead   stmt.ID = "mp.reading_intent.saved"
	mpReadingPersonal    stmt.ID = "mp.reading_intent.personal"
	mpReadingLibraryKey  stmt.ID = "mp.reading_intent.library_key"
	mpReadingMetadata    stmt.ID = "mp.reading_intent.metadata"
	mpReadingResultBytes stmt.ID = "mp.reading_intent.result_bytes"
	mpReadingResultRead  stmt.ID = "mp.reading_intent.result"
)

// MP01's source SQL and its exact closed octet registrations are reused verbatim.
// These inert definitions DO NOT open 770/771. UO's public Check/Save primitive
// must own disclosure/classification/quota, personal effects and receipt writes.
func mpReadingIntentDefinitions() []stmt.Definition {
	out := read.MPReadingSourceDefinitions()
	add := func(id stmt.ID, sql, types string, plan stmt.Plan) {
		d := stmt.Definition{ID: id, SQL: sql, Kind: stmt.Read, Owner: "store/writer MP reading", CallSites: []string{"store/writer/mp_reading.go", "store/writer/mp_reading_result.go"}, Plan: plan}
		args := []any{}
		for i, c := range types {
			p := stmt.Param{Name: string(rune('a' + i)), Type: stmt.Integer}
			var v any = int64(1)
			if c == 's' {
				p.Type = stmt.Text
				v = "unit"
			}
			if c == 'b' {
				p.Type = stmt.Blob
				v = make([]byte, 16)
			}
			d.Params = append(d.Params, p)
			args = append(args, v)
		}
		d.Cohorts = []stmt.Cohort{{Name: "empty", Bindings: args}}
		out = append(out, d)
	}
	add(mpReadingPersonal, `SELECT id,coalesce(reading_revision,0),revision FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=? AND item_public_id=?`, "sib", stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}})
	add(mpReadingLibraryKey, `SELECT e.library_id,l.library_id FROM catalog_entities AS e CROSS JOIN catalog_libraries AS l ON l.id=e.library_id WHERE e.id=? AND e.retired=0 AND l.retired=0`, "i", stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l"}})
	add(mpReadingMetadata, `SELECT metadata_revision FROM catalog_entities WHERE id=? AND retired=0`, "i", stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}})
	key := stmt.AutoIndexBinding{Table: "user_mutation_reading_results", Name: "sqlite_autoindex_user_mutation_reading_results_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "receipt_key", Collation: "BINARY"}}}
	p := stmt.Plan{Hot: true, Indexes: []string{key.Name}, AutoIndexes: []stmt.AutoIndexBinding{key}}
	add(mpReadingResultBytes, `SELECT variable_bytes FROM user_mutation_reading_results INDEXED BY sqlite_autoindex_user_mutation_reading_results_1 WHERE receipt_key=?`, "s", p)
	add(mpReadingResultRead, `SELECT command_kind,result_version,item_public_id,asset_id,source_token,source_revision,format,cfi,section,page_index,progression,reading_revision,personal_revision,observed_ms,accepted,stale,cleared,reason FROM user_mutation_reading_results INDEXED BY sqlite_autoindex_user_mutation_reading_results_1 WHERE receipt_key=?`, "s", p)
	pk := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_personal_reading_resumes_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "personal_reading_resumes", Name: "sqlite_autoindex_personal_reading_resumes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "personal_item_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}}}
	add(mpReadingSavedBytes, `SELECT variable_bytes FROM personal_reading_resumes INDEXED BY sqlite_autoindex_personal_reading_resumes_1 WHERE personal_item_id=? AND asset_id=?`, "ii", pk)
	add(mpReadingSavedRead, `SELECT source_token,source_revision,format,cfi,section,page_index,progression,revision,observed_ms FROM personal_reading_resumes INDEXED BY sqlite_autoindex_personal_reading_resumes_1 WHERE personal_item_id=? AND asset_id=?`, "ii", pk)
	return out
}
