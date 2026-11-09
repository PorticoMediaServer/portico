package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-22's finite reencode family. Group E is embedded, so these definitions are
// qualified against the canonical schema; they make no Capture or adoption
// claim. The receipt statement is this
// command's OWN read: only the frozen E label 'reencode' maps to 231, while any
// other label at the same key maps to 0 and is refused.
//
// Every read is an exact primary key (receipt owner+sequence, source digest);
// the two writes are one exact source revision/encoder CAS and one receipt
// insert. The material intent/manifest and blob custody reuse IP-19/20's point
// statements. No selection, history or material row is rewritten: the source
// digest is stable and pinned references keep charging the old blobs.
const (
	ipARReceiptOctets stmt.ID = "ip.art.reencode.receipt_octets"
	ipARReceipt       stmt.ID = "ip.art.reencode.receipt"
	ipARSourceExact   stmt.ID = "ip.art.reencode.source"
	ipARSourceUpdate  stmt.ID = "ip.art.reencode.source_update"
	ipARSave          stmt.ID = "ip.art.reencode.save"
)

func ipArtworkReencodeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	defs := []stmt.Definition{
		{
			// Point octet preflight before copying the pinned intent and source
			// digest. A reencode receipt never carries a retirement token.
			ID: ipARReceiptOctets, SQL: `SELECT coalesce(octet_length(material_intent_id),0),coalesce(octet_length(selected_digest),0),octet_length(operation_id),octet_length(request_digest),octet_length(result_digest),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// Only a reencode row maps to 231; the selected source digest is the
			// stable accepted source identity.
			ID: ipARReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'reencode' THEN 231 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,coalesce(material_intent_id,''),coalesce(selected_digest,''),coalesce(storage_revision,0),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// The exact accepted source custody: encoded variant keys, accepted
			// facts, encoder generation, retained original bytes and retirement
			// fence. A stale generation/revision or retiring source is refused.
			ID: ipARSourceExact, SQL: `SELECT accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,revision,original_bytes,retiring,coalesce(octet_length(retirement_token),0) FROM artwork_sources WHERE digest=?`,
			Params: []stmt.Param{t("digest")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{digest}},
				{Name: "missing", Bindings: []any{""}},
			},
		},
		{
			// One exact revision/encoder CAS. The retained original bytes, the
			// accepted source identity and every selection/history reference are
			// untouched; only the encoded variant keys and generation advance.
			ID: ipARSourceUpdate, SQL: `UPDATE artwork_sources SET small_blob=?,large_blob=?,ambient_blob=?,large_edge=?,small_outlier=?,large_outlier=?,encoder_revision=?,revision=revision+1 WHERE digest=? AND revision=? AND retiring=0 AND retirement_token IS NULL AND small_blob=? AND large_blob=? AND ambient_blob=? AND large_edge=? AND small_outlier=? AND large_outlier=? AND encoder_revision=? AND original_bytes=?`,
			Params:  []stmt.Param{t("small"), t("large"), t("ambient"), i("edge"), i("small_outlier"), i("large_outlier"), i("encoder"), t("digest"), i("revision"), t("old_small"), t("old_large"), t("old_ambient"), i("old_edge"), i("old_small_outlier"), i("old_large_outlier"), i("old_encoder"), i("original_bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_sources"}},
			Cohorts: []stmt.Cohort{{Name: "updated", Bindings: []any{digest, digest, digest, int64(1920), int64(0), int64(0), int64(4), digest, int64(1), digest, digest, digest, int64(1920), int64(0), int64(0), int64(3), int64(2 << 20)}}},
		},
		{
			// The reencode receipt carries the stable source digest, its new
			// revision and the exact reencoded byte/count facts.
			ID: ipARSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,selected_digest,storage_revision) VALUES(?,?,?,?,?,?,?,'reencode',?,?,?,1,?,?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("accepted_revision"), i("result_count"), i("result_bytes"), i("lease_generation"), i("lease_until"), t("intent"), t("selected"), i("storage_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(901), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(2), int64(3), int64(1 << 20), int64(1), int64(1 << 50), "job-reencode", digest, int64(2)}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipARSourceUpdate, ipARSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork reencode"
		defs[n].CallSites = []string{"store/writer/ip_artwork_reencode.go"}
	}
	return defs
}
