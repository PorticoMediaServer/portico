package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-20's finite publish family. Group E is embedded, so these definitions are
// qualified against the canonical schema, including the source/blob custody
// columns; they make no Capture or adoption claim. The receipt statement is this command's OWN read: command 224 already
// stores the frozen "material_publish" label, so a publish row is recognised by
// its selected source digest and source revision, never by relabelling the E
// enum (a distinct label would be an INT change).
//
// Every read is an exact primary/unique key (receipt owner+sequence, intent id,
// source digest, blob digest, subject-job id) or the raw (intent_id,slot)
// keyset from IP-19; there is no OFFSET, temp sort or filtered scan. Writes are
// bounded PK inserts/upserts and one exact CAS each for the intent and the
// retained subject job.
const (
	ipAPReceiptOctet  stmt.ID = "ip.art.material.publish.receipt_octets"
	ipAPReceipt       stmt.ID = "ip.art.material.publish.receipt"
	ipAPIntentOctets  stmt.ID = "ip.art.material.publish.intent_octets"
	ipAPIntent        stmt.ID = "ip.art.material.publish.intent"
	ipAPBlob          stmt.ID = "ip.art.material.publish.blob"
	ipAPSource        stmt.ID = "ip.art.material.publish.source"
	ipAPSourceFacts   stmt.ID = "ip.art.material.publish.source_facts"
	ipAPIntentPublish stmt.ID = "ip.art.material.publish.intent_publish"
	ipAPSave          stmt.ID = "ip.art.material.publish.save"
)

func ipArtworkMaterialPublishDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	defs := []stmt.Definition{
		{
			// Point octet preflight before copying the pinned intent id or the
			// source digest; a retirement token is never admissible here.
			ID: ipAPReceiptOctet, SQL: `SELECT octet_length(coalesce(material_intent_id,'')),coalesce(octet_length(selected_digest),0),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// Only a publish row (selected source present) maps to 225; an IP-19
			// handoff row at the same key maps to 0 and is refused.
			ID: ipAPReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE WHEN command_kind='material_publish' AND selected_digest IS NOT NULL THEN 225 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,lease_generation,lease_until_ms,coalesce(material_intent_id,''),coalesce(selected_digest,''),1,retirement_token FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			ID: ipAPIntentOctets, SQL: `SELECT octet_length(source_digest),octet_length(accepted_mime),octet_length(state) FROM artwork_material_intents WHERE id=? AND owner_id=? AND owner_incarnation=?`,
			Params: []stmt.Param{t("intent"), i("owner"), b("owner_incarnation")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"job-1", int64(901), make([]byte, 16)}},
				{Name: "missing", Bindings: []any{"job-x", int64(901), make([]byte, 16)}},
			},
		},
		{
			// The complete installed declaration. Publish never trusts a caller
			// value it cannot re-read here, and retirement is never reopened.
			ID: ipAPIntent, SQL: `SELECT generation,revision,state,expected_slots,staged_slots,source_digest,accepted_mime,accepted_width,accepted_height,large_edge,encoder_revision,coalesce(octet_length(retirement_token),0) FROM artwork_material_intents WHERE id=? AND owner_id=? AND owner_incarnation=?`,
			Params: []stmt.Param{t("intent"), i("owner"), b("owner_incarnation")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"job-1", int64(901), make([]byte, 16)}},
				{Name: "missing", Bindings: []any{"job-x", int64(901), make([]byte, 16)}},
			},
		},
		{
			// A shared encoded blob is reused only when every physical fact and the
			// source revision already match; a changed byte or a deleting blob is
			// a conflict, never a silent overwrite.
			ID: ipAPBlob, SQL: `INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status,storage_revision,deletion_token) VALUES(?,?,?,?,?,?,'ready',1,NULL) ON CONFLICT(digest) DO UPDATE SET status='ready' WHERE artwork_blobs.mime=excluded.mime AND artwork_blobs.width=excluded.width AND artwork_blobs.height=excluded.height AND artwork_blobs.bytes=excluded.bytes AND artwork_blobs.status='ready' AND artwork_blobs.storage_revision=excluded.storage_revision AND artwork_blobs.deletion_token IS NULL`,
			Params:  []stmt.Param{t("digest"), t("mime"), i("width"), i("height"), i("bytes"), t("created_at")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_blobs"}},
			Cohorts: []stmt.Cohort{{Name: "ready", Bindings: []any{digest, "image/jpeg", int64(500), int64(750), int64(4096), "2026-11-08T00:00:00Z"}}},
		},
		{
			// The source row is the accepted original's custody: its encoded blob
			// set, the retained original bytes and a non-retiring revision. A
			// duplicate digest refuses (a republish belongs to IP-22, not here).
			ID: ipAPSource, SQL: `INSERT INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at,revision,original_bytes,retiring,retirement_token) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,1,?,0,NULL)`,
			Params:  []stmt.Param{t("digest"), t("mime"), i("width"), i("height"), t("small"), t("large"), t("ambient"), i("large_edge"), i("small_outlier"), i("large_outlier"), i("encoder_revision"), t("created_at"), i("original_bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{digest, "image/jpeg", int64(3000), int64(2000), digest, digest, digest, int64(1920), int64(0), int64(0), int64(3), "2026-11-08T00:00:00Z", int64(2 << 20)}}},
		},
		{
			// Exact source custody readback, including the retirement fence.
			ID: ipAPSourceFacts, SQL: `SELECT accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,revision,original_bytes,retiring,coalesce(octet_length(retirement_token),0) FROM artwork_sources WHERE digest=?`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// One CAS advances the installed material to published. A retiring,
			// prepared, already-published or changed intent is refused whole.
			ID: ipAPIntentPublish, SQL: `UPDATE artwork_material_intents SET state='published',revision=revision+1 WHERE id=? AND owner_id=? AND owner_incarnation=? AND generation=? AND revision=? AND state='installed' AND staged_slots=expected_slots AND expected_slots=? AND source_digest=? AND retirement_token IS NULL`,
			Params:  []stmt.Param{t("intent"), i("owner"), b("owner_incarnation"), i("generation"), i("revision"), i("expected_slots"), t("source_digest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_material_intents"}},
			Cohorts: []stmt.Cohort{{Name: "published", Bindings: []any{"job-1", int64(901), make([]byte, 16), int64(1), int64(2), int64(4), digest}}},
		},
		{
			// The publish receipt carries its own source witness (selected digest
			// and source revision) inside the frozen E table.
			ID: ipAPSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,selected_digest) VALUES(?,?,?,?,?,?,?,'material_publish',3,?,?,1,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("result_count"), i("result_bytes"), i("lease_generation"), i("lease_until"), t("intent"), t("selected_digest")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), make([]byte, 16), int64(3), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(4), int64(1 << 20), int64(1), int64(1 << 50), "job-1", digest}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipAPBlob, ipAPSource, ipAPIntentPublish, ipAPSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork material publish"
		defs[n].CallSites = []string{"store/writer/ip_artwork_material_publish.go"}
	}
	return defs
}
