package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	searchReclaimOwner         stmt.ID = "search.reclaim.owner.job"
	searchReclaimIdentity      stmt.ID = "search.reclaim.receipt.identity"
	searchReclaimResult        stmt.ID = "search.reclaim.receipt.fragment_result"
	searchReclaimReceiptPut    stmt.ID = "search.reclaim.receipt.fragment_insert"
	searchReclaimPublication   stmt.ID = "search.reclaim.owner.publication"
	searchReclaimSubject       stmt.ID = "search.reclaim.owner.subject"
	searchReclaimBank          stmt.ID = "search.reclaim.owner.bank"
	searchReclaimPointers      stmt.ID = "search.reclaim.owner.pointers"
	searchReclaimNext          stmt.ID = "search.reclaim.owner.next_fragment"
	searchReclaimFragment      stmt.ID = "search.reclaim.owner.fragment"
	searchReclaimSeal          stmt.ID = "search.reclaim.owner.seal"
	searchReclaimPrefix        stmt.ID = "search.reclaim.native.prefix_delete"
	searchReclaimLexical       stmt.ID = "search.reclaim.native.lexical_delete"
	searchReclaimGrams         stmt.ID = "search.reclaim.native.grams_delete"
	searchReclaimContentDelete stmt.ID = "search.reclaim.cas.content_delete"
	searchReclaimNoDue         stmt.ID = "search.reclaim.cas.fragment_no_due"
	searchReclaimDue           stmt.ID = "search.reclaim.cas.fragment_due"
)
const searchReclaimFragmentKind = "fragment_reclaim"

func searchFragmentReclaimDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	pk := func(table string) stmt.Plan {
		return stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{"rowid=?"}}
	}
	ds := []stmt.Definition{
		{ID: searchReclaimOwner, Kind: stmt.Read, SQL: `SELECT id,publication_id,live_publication_id,phase,command_incarnation,job_revision,after_fragment_id,last_command_sequence,acknowledged_command_sequence,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms FROM search_reclaim_jobs WHERE id=?`, Params: []stmt.Param{i("job")}, Plan: pk("search_reclaim_jobs")},
		{ID: searchReclaimIdentity, Kind: stmt.Read, SQL: `SELECT operation_id,request_digest,command_kind FROM search_reclaim_receipts WHERE reclaim_job_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("job"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_reclaim_receipts"}, Seeks: []string{"reclaim_job_id=? AND command_incarnation=? AND command_sequence=?"}}},
		{ID: searchReclaimResult, Kind: stmt.Read, SQL: `SELECT accepted_revision,applied_ms,publication_id,fragment_id,after_fragment_id,complete FROM search_reclaim_receipts WHERE reclaim_job_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("job"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_reclaim_receipts"}, Seeks: []string{"reclaim_job_id=? AND command_incarnation=? AND command_sequence=?"}}},
		{ID: searchReclaimReceiptPut, Kind: stmt.Write, SQL: `INSERT INTO search_reclaim_receipts(reclaim_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,publication_id,fragment_id,after_fragment_id,complete) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("job"), b("incarnation"), i("sequence"), b("operation"), b("request"), t("kind"), i("accepted_revision"), i("applied_ms"), i("publication"), i("fragment"), i("after_fragment"), i("complete")}, Plan: stmt.Plan{Hot: true}},
		{ID: searchReclaimPublication, Kind: stmt.Read, SQL: `SELECT id,entity_id,library_id,kind,search_generation_id,generation FROM search_publications WHERE id=? AND person_id IS NULL`, Params: []stmt.Param{i("image")}, Plan: pk("search_publications")},
		{ID: searchReclaimSubject, Kind: stmt.Read, SQL: `SELECT id FROM catalog_subjects WHERE id=? AND library_id=? AND kind=?`, Params: []stmt.Param{i("entity"), i("library"), i("kind")}, Plan: pk("catalog_subjects")},
		{ID: searchReclaimBank, Kind: stmt.Read, SQL: `SELECT id FROM search_generations WHERE id=? AND bank_key=? AND engine_version=?`, Params: []stmt.Param{i("bank"), t("key"), t("engine")}, Plan: pk("search_generations")},
		{ID: searchReclaimPointers, Kind: stmt.Read, SQL: `SELECT published_document_id,document_id FROM search_publish_jobs WHERE entity_id=? AND search_generation_id=?`, Params: []stmt.Param{i("entity"), i("bank")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_search_publish_jobs_1"}, Seeks: []string{"entity_id=? AND search_generation_id=?"}}},
		{ID: searchReclaimNext, Kind: stmt.Read, SQL: `SELECT id FROM search_fragments INDEXED BY search_fragment_reclaim WHERE publication_id=? AND id>? ORDER BY id LIMIT ?`, Params: []stmt.Param{i("image"), i("after_fragment"), i("limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"search_fragment_reclaim"}, Seeks: []string{"publication_id=? AND id>?"}}},
		{ID: searchReclaimFragment, Kind: stmt.Read, SQL: `SELECT id,publication_id,search_generation_id,generation,owner_entity,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized FROM search_fragments WHERE id=? AND publication_id=? AND owner_person IS NULL`, Params: []stmt.Param{i("fragment"), i("image")}, Plan: pk("search_fragments")},
		{ID: searchReclaimSeal, Kind: stmt.Read, SQL: `SELECT publication_id FROM search_image_seals WHERE publication_id=?`, Params: []stmt.Param{i("image")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"search_image_seals"}, Seeks: []string{"publication_id=?"}}},
		{ID: searchReclaimContentDelete, Kind: stmt.Write, SQL: `DELETE FROM search_fragments WHERE id=? AND publication_id=? AND search_generation_id=? AND generation=? AND owner_person IS NULL AND field=? AND ordinal=? AND source_revision=? AND prefix_tokens=? AND lexical_tokens=? AND normalized=? RETURNING id`, Params: []stmt.Param{i("fragment"), i("image"), i("bank"), i("generation"), i("field"), i("ordinal"), t("revision"), t("prefix"), t("lexical"), t("normalized")}, Plan: pk("search_fragments")},
	}
	for _, v := range []struct {
		id   stmt.ID
		slot stmt.FTSRetainedSlot
	}{{searchReclaimPrefix, stmt.SearchFragmentPrefixDelete}, {searchReclaimLexical, stmt.SearchFragmentLexicalDelete}, {searchReclaimGrams, stmt.SearchFragmentGramsDelete}} {
		s, _ := v.slot.Specification()
		ds = append(ds, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: s.SQL, Params: []stmt.Param{t("control"), i("rowid"), t(s.Columns[0])}, Plan: stmt.Plan{Hot: true, NativeRetained: v.slot}})
	}
	for _, v := range []struct {
		id  stmt.ID
		due bool
	}{{searchReclaimNoDue, false}, {searchReclaimDue, true}} {
		q := `UPDATE search_reclaim_jobs SET phase=?,after_fragment_id=?,job_revision=?,last_command_sequence=? WHERE id=? AND publication_id=? AND live_publication_id=? AND command_incarnation=? AND phase=? AND job_revision=? AND after_fragment_id=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND job_attempts=?`
		ps := []stmt.Param{t("reclaim"), i("next_fragment"), i("next_revision"), i("next_sequence"), i("job"), i("publication"), i("live_publication"), b("incarnation"), t("phase"), i("revision"), i("after_fragment"), i("last_sequence"), i("ack_sequence"), i("lease_owner"), i("lease_generation"), i("lease_expires"), i("attempts")}
		if v.due {
			q += ` AND job_next_attempt_ms=?`
			ps = append(ps, i("due"))
		} else {
			q += ` AND job_next_attempt_ms IS NULL`
		}
		q += ` RETURNING job_revision`
		ds = append(ds, stmt.Definition{ID: v.id, Kind: stmt.Write, SQL: q, Params: ps, Plan: pk("search_reclaim_jobs")})
	}
	inc, op, digest := [16]byte{61}, [16]byte{91}, [32]byte{92}
	for n := range ds {
		d := &ds[n]
		d.Owner = "writer.search.reclaimer"
		d.CallSites = []string{"writer/search_reclaim_stage.go"}
		var args []any
		switch d.ID {
		case searchReclaimOwner:
			args = []any{int64(2)}
		case searchReclaimIdentity, searchReclaimResult:
			args = []any{int64(2), inc[:], int64(1)}
		case searchReclaimReceiptPut:
			args = []any{int64(2), inc[:], int64(1), op[:], digest[:], searchReclaimFragmentKind, int64(3), int64(1000), int64(13), int64(601), int64(601), int64(0)}
		case searchReclaimPublication, searchReclaimSeal:
			args = []any{int64(13)}
		case searchReclaimSubject:
			args = []any{int64(41), int64(3), int64(1)}
		case searchReclaimBank:
			args = []any{int64(7), "v1", searchpolicy.EngineVersion}
		case searchReclaimPointers:
			args = []any{int64(41), int64(7)}
		case searchReclaimNext:
			args = []any{int64(13), int64(0), int64(1)}
		case searchReclaimFragment:
			args = []any{int64(601), int64(13)}
		case searchReclaimContentDelete:
			args = []any{int64(601), int64(13), int64(7), int64(3), int64(1), int64(0), "09", "00006f00006c000064", "old", "old"}
		case searchReclaimPrefix:
			args = []any{"delete", int64(601), "00006f00006c000064"}
		case searchReclaimLexical, searchReclaimGrams:
			args = []any{"delete", int64(601), "old"}
		case searchReclaimNoDue, searchReclaimDue:
			args = []any{"reclaim", int64(601), int64(3), int64(1), int64(2), int64(13), int64(13), inc[:], "ready", int64(2), int64(0), int64(0), int64(0), int64(1), int64(2), int64(2100000000000), int64(0)}
			if d.ID == searchReclaimDue {
				args = append(args, int64(1000))
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "one_retained_fragment", Bindings: args}}
	}
	return ds
}
