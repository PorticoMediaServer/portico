package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP-22's finite owner-upload family. Group E is embedded, so these definitions
// are qualified against the canonical schema; they make no Capture or adoption
// claim. The receipt
// statement is this command's OWN read: only the frozen E label 'upload_accept'
// maps to 228, while any other label at the same key maps to 0 and is refused.
//
// The material intent/manifest, source/blob custody and locked-selection writes
// reuse IP-19/20/21's exact point statements; this family adds only the native
// upload job proof and the closed upload receipt. Every read is an exact primary
// key; writes are one receipt insert and one owner CAS (IP-19's statement).
const (
	ipAUReceiptOctets stmt.ID = "ip.art.upload.receipt_octets"
	ipAUReceipt       stmt.ID = "ip.art.upload.receipt"
	ipAUJob           stmt.ID = "ip.art.upload.job"
	ipAUSave          stmt.ID = "ip.art.upload.save"
)

func ipArtworkUploadDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	defs := []stmt.Definition{
		{
			// Point octet preflight before copying the pinned intent, role and
			// digests. An upload receipt never carries a retirement token.
			ID: ipAUReceiptOctets, SQL: `SELECT coalesce(octet_length(material_intent_id),0),coalesce(octet_length(role),0),coalesce(octet_length(selected_digest),0),coalesce(octet_length(previous_digest),0),octet_length(operation_id),octet_length(request_digest),octet_length(result_digest),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// Only an upload_accept row maps to 228. The closed owner choice and
			// selected source are part of the same row.
			ID: ipAUReceipt, SQL: `SELECT operation_id,request_digest,result_digest,CASE command_kind WHEN 'upload_accept' THEN 228 ELSE 0 END,accepted_revision,applied_ms,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,coalesce(material_intent_id,''),coalesce(role,''),coalesce(choice,''),coalesce(selection_revision,0),coalesce(selected_digest,''),coalesce(previous_digest,''),1,coalesce(history_id,0),coalesce(octet_length(retirement_token),0) FROM artwork_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(901), make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{int64(901), make([]byte, 16), int64(2)}},
			},
		},
		{
			// The upload is a typed completed native image job for this subject and
			// role, never a permission flag; a supplied "upload" label on another
			// provider or an unfinished job is refused.
			ID: ipAUJob, SQL: `SELECT 1 FROM artwork_jobs WHERE id=? AND subject_job_id=? AND kind=? AND entity_id=? AND role=? AND choice='locked' AND provider='upload' AND status='complete'`,
			Params: []stmt.Param{t("job"), i("subject_job"), t("kind"), i("entity"), t("role")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "complete", Bindings: []any{"job-person-portrait", int64(902), "person", int64(778), "portrait"}},
				{Name: "missing", Bindings: []any{"job-x", int64(902), "person", int64(778), "portrait"}},
			},
		},
		{
			// The upload receipt carries the closed owner choice witness, the
			// selected source, its original-byte custody and the history pin.
			ID: ipAUSave, SQL: `INSERT INTO artwork_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,result_bytes,result_complete,lease_generation,lease_until_ms,material_intent_id,role,choice,selection_revision,selected_digest,previous_digest,history_id) VALUES(?,?,?,?,?,?,?,'upload_accept',?,?,?,1,?,?,?,?, 'locked',?,?,?,?)`,
			Params:  []stmt.Param{i("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), i("accepted_revision"), i("result_count"), i("result_bytes"), i("lease_generation"), i("lease_until"), t("intent"), t("role"), i("selection_revision"), t("selected"), nt("previous"), i("history")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"artwork_command_receipts"}},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(902), make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(3), int64(4), int64(1 << 20), int64(1), int64(1 << 50), "job-person-portrait", "portrait", int64(1), digest, nil, int64(1)}}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipAUSave:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP artwork upload"
		defs[n].CallSites = []string{"store/writer/ip_artwork_upload.go"}
	}
	return defs
}
