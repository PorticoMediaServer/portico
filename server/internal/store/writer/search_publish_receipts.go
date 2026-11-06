package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Composed only by W's closed registry. These definitions confer no disclosure,
// turn, profile, native-slot or replay authority on their own.
const (
	searchPublisherOwnerID   stmt.ID = "search.publish.owner.receipt_job"
	searchPublisherIdentity  stmt.ID = "search.publish.receipt.identity"
	searchPublisherImageRead stmt.ID = "search.publish.receipt.image_result"
	searchPublisherAck       stmt.ID = "search.publish.ack.publisher"
	searchPublisherImagePut  stmt.ID = "search.publish.receipt.image_insert"
)

const searchImageReceiptKind = "image_stage"

func searchPublisherReceiptDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	inc := [16]byte{17}
	defs := []stmt.Definition{
		{ID: searchPublisherOwnerID, Kind: stmt.Read,
			SQL: `SELECT entity_id,person_id,command_incarnation,last_command_sequence,acknowledged_command_sequence
FROM search_publish_jobs WHERE id=?`,
			Params: []stmt.Param{i("job_id")}},
		{ID: searchPublisherIdentity, Kind: stmt.Read,
			SQL: `SELECT operation_id,request_digest,command_kind FROM search_publish_receipts
WHERE publish_job_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("job_id"), b("incarnation"), i("sequence")}},
		{ID: searchPublisherImageRead, Kind: stmt.Read,
			// Historical IDs are VALUES. No join/FK to live image custody may
			// make an unacknowledged result depend on today's native rows.
			SQL: `SELECT accepted_revision,applied_ms,input_generation,image_id,fragment_id,
after_field,after_ordinal,after_fragment_id,seal_chain,published_generation,
staged_fragments,staged_bytes,verified_fragments,verified_bytes,seal_algorithm
FROM search_publish_receipts
WHERE publish_job_id=? AND command_incarnation=? AND command_sequence=? AND seal_algorithm=?`,
			Params: []stmt.Param{i("job_id"), b("incarnation"), i("sequence"), t("algorithm")}},
		{ID: searchPublisherAck, Kind: stmt.Write,
			SQL: `UPDATE search_publish_jobs SET acknowledged_command_sequence=?
WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?`,
			Params: []stmt.Param{i("next_ack"), i("job_id"), b("incarnation"), i("expected_last"), i("expected_ack")},
			Cohorts: []stmt.Cohort{
				{Name: "unresolved_result", Bindings: []any{int64(8), int64(30), inc[:], int64(8), int64(7)}},
				{Name: "stale_floor", Bindings: []any{int64(8), int64(30), inc[:], int64(8), int64(6)}},
			}},
		{ID: searchPublisherImagePut, Kind: stmt.Write,
			SQL: `INSERT INTO search_publish_receipts(
publish_job_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
accepted_revision,applied_ms,input_generation,image_id,fragment_id,after_field,after_ordinal,
after_fragment_id,seal_chain,published_generation,staged_fragments,staged_bytes,
verified_fragments,verified_bytes,seal_algorithm) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{i("job_id"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), t("kind"),
				i("accepted_revision"), i("applied_ms"), i("input_generation"), i("image_id"),
				{Name: "fragment_id", Type: stmt.Integer, Nullable: true}, i("after_field"), i("after_ordinal"),
				i("after_fragment_id"), b("seal_chain"), i("published_generation"), i("staged_fragments"), i("staged_bytes"),
				i("verified_fragments"), i("verified_bytes"), t("seal_algorithm")},
			Cohorts: []stmt.Cohort{{Name: "new_hidden_image", Bindings: []any{int64(29), inc[:], int64(8),
				make([]byte, 16), make([]byte, 32), searchImageReceiptKind, int64(20), int64(1000), int64(5), int64(13), nil,
				int64(0), int64(0), int64(0), make([]byte, 32), int64(3), int64(0), int64(0), int64(0), int64(0), searchmanifest.Algorithm}}},
		},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.search.publisher"
		d.CallSites = []string{"writer/search_publish_history.go"}
		if d.ID == searchPublisherImagePut {
			d.CallSites = []string{"writer/search_image_stage.go"}
		}
		d.Plan = stmt.Plan{Hot: true}
		switch d.ID {
		case searchPublisherOwnerID, searchPublisherAck:
			d.Plan.PrimaryKeys, d.Plan.Seeks = []string{"search_publish_jobs"}, []string{"rowid=?"}
		case searchPublisherIdentity, searchPublisherImageRead:
			d.Plan.PrimaryKeys = []string{"search_publish_receipts"}
			d.Plan.Seeks = []string{"publish_job_id=? AND command_incarnation=? AND command_sequence=?"}
		}
		if d.Kind != stmt.Read {
			continue
		}
		for _, cohort := range []struct {
			name string
			job  int64
		}{{"fresh", 29}, {"replay", 30}, {"conflict", 31}, {"retired", 32}, {"missing_proof", 33}, {"retired_missing", 34}, {"missing_owner", 999}} {
			args := []any{cohort.job}
			if d.ID != searchPublisherOwnerID {
				args = append(args, inc[:], int64(8))
			}
			if d.ID == searchPublisherImageRead {
				args = append(args, searchmanifest.Algorithm)
			}
			d.Cohorts = append(d.Cohorts, stmt.Cohort{Name: cohort.name, Bindings: args})
		}
	}
	return defs
}
