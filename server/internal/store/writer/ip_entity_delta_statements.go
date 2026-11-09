package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP nonmovie entity provider typed delta replace 217. The NEW observation is
// consumed through the same small helpers IP-09 uses (evidence header, exact
// seal proof, optional TV set proof, sealed build and complete child cohort);
// this file defines only the delta's own exact-old-head reads, its previous_*
// publication head, the old-head selection CAS and its retained proof keys.
//
// The old accepted source and its proof pins stay charged: the delta never
// touches the old publication, the old evidence or the old seal receipt, and it
// proves the old seal receipt is still terminal with a live pin before the
// first write. Groups D/E are not embedded, so these definitions make no
// Capture claim.
const (
	ipEntityDeltaPriorHeadStmt      stmt.ID = "ip.entity.delta.prior_head"
	ipEntityDeltaPriorEvidenceStmt  stmt.ID = "ip.entity.delta.prior_evidence"
	ipEntityDeltaPriorSealStmt      stmt.ID = "ip.entity.delta.prior_seal"
	ipEntityDeltaPriorSetStmt       stmt.ID = "ip.entity.delta.prior_set"
	ipEntityDeltaPriorSetSealStmt   stmt.ID = "ip.entity.delta.prior_set_seal"
	ipEntityDeltaOriginsStmt        stmt.ID = "ip.entity.delta.origins"
	ipEntityDeltaOwnerAutomaticStmt stmt.ID = "ip.entity.delta.owner_automatic"
	ipEntityDeltaPublicationStmt    stmt.ID = "ip.entity.delta.publication"
	ipEntityDeltaSelectionCASStmt   stmt.ID = "ip.entity.delta.selection_cas"
	ipEntityDeltaRetainProofStmt    stmt.ID = "ip.entity.delta.retain_proof"
	ipEntityDeltaRetainSetProofStmt stmt.ID = "ip.entity.delta.retain_set_proof"
	ipEntityDeltaRecordStmt         stmt.ID = "ip.entity.delta.record"
	ipEntityDeltaSavedStmt          stmt.ID = "ip.entity.delta.saved"
)

func ipEntityDeltaDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The exact old accepted head. It must still be the selection's
			// accepted publication; the stored selection_revision is the revision
			// that publication head was accepted from.
			ID: ipEntityDeltaPriorHeadStmt, SQL: `SELECT provider,subject_kind,provider_id,evidence_id,set_id,decision,operation_id,selection_revision FROM metadata_publications WHERE id=? AND entity_id=?`,
			Params: []stmt.Param{t("publication"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publications"}},
		},
		{
			// The retained upstream source proof header. Its digest is the exact
			// observation content identity used for the no-op decision.
			ID: ipEntityDeltaPriorEvidenceStmt, SQL: `SELECT subject_id,provider,subject_kind,provider_id,parser_version,language,region,request_shape,digest,sealed,ip_seal_owner,ip_seal_incarnation,ip_seal_sequence FROM metadata_evidence WHERE id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
		},
		{
			// The old ORIGINAL seal receipt must still be terminal and charged.
			ID: ipEntityDeltaPriorSealStmt, SQL: `SELECT command_incarnation,command_sequence,terminal,seal_rows,seal_bytes,proof_pin_count FROM metadata_ip_receipts WHERE owner_id=? AND evidence_id=? AND command_kind='evidence_seal'`,
			Params: []stmt.Param{t("owner"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_evidence"}},
		},
		{
			// A retained set proof must still be complete and own the old
			// evidence. The set header has no evidence column of its own; its
			// exact IP set-seal receipt is the proof that names the evidence.
			ID: ipEntityDeltaPriorSetStmt, SQL: `SELECT r.evidence_id,s.complete,s.coverage_complete FROM metadata_publication_sets AS s JOIN metadata_ip_receipts AS r ON r.owner_id=s.ip_proof_owner AND r.command_incarnation=s.ip_proof_incarnation AND r.command_sequence=s.ip_proof_sequence AND r.command_kind='set_seal' AND r.set_id=s.id WHERE s.id=? AND s.entity_id=?`,
			Params: []stmt.Param{t("set"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			ID: ipEntityDeltaPriorSetSealStmt, SQL: `SELECT command_incarnation,command_sequence,terminal,proof_pin_count FROM metadata_ip_receipts WHERE owner_id=? AND set_id=? AND command_kind='set_seal'`,
			Params: []stmt.Param{t("owner"), t("set")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			// The typed field names the old accepted head published, in one bounded
			// row. Field names are from the closed descriptive API and never carry
			// the unit separator.
			ID: ipEntityDeltaOriginsStmt, SQL: `SELECT count(*),coalesce(group_concat(field,char(31)),'') FROM metadata_field_origins WHERE entity_id=? AND publication_id=?`,
			Params: []stmt.Param{i("entity"), t("publication")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_field_origin_publication"}},
		},
		{
			// Durable automatic value for a non-projection descriptive field.
			ID: ipEntityDeltaOwnerAutomaticStmt, SQL: `SELECT automatic_value,automatic_present FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND field=?`,
			Params: []stmt.Param{t("kind"), i("entity"), t("field")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_owner_fields"}},
		},
		{
			// One new accepted publication head linked to the exact old head.
			// previous_* are the old publication's own provider namespace.
			ID: ipEntityDeltaPublicationStmt, SQL: `INSERT INTO metadata_publications(previous_provider,previous_subject_kind,previous_provider_id,id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,set_id,previous_id,selection_revision,generation,decision,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'accepted',?)`,
			Params: []stmt.Param{
				t("previous_provider"), t("previous_subject_kind"), t("previous_provider_id"),
				t("publication"), t("operation"), i("entity"), t("provider"), t("subject_kind"),
				t("provider_id"), t("evidence"), nt("set"), t("previous_id"),
				i("selection_revision"), i("generation"), t("observed_at"),
			},
		},
		{
			// The accepted slot moves only from the exact old accepted head.
			ID: ipEntityDeltaSelectionCASStmt, SQL: `UPDATE metadata_subject_providers SET accepted_evidence_id=?,accepted_set_id=?,accepted_publication_id=?,revision=revision+1,observed_at=? WHERE entity_id=? AND provider=? AND role='primary' AND incarnation=? AND revision=? AND subject_kind=? AND provider_id=? AND status='selected' AND accepted_track_id IS NULL AND accepted_evidence_id IS ? AND accepted_set_id IS ? AND accepted_publication_id IS ? AND revision<9223372036854775807 RETURNING revision`,
			Params: []stmt.Param{
				t("evidence"), nt("set"), t("publication"), t("observed_at"),
				i("entity"), t("provider"), b("incarnation"), i("revision"),
				t("subject_kind"), t("provider_id"), nt("old_evidence"), nt("old_set"),
				nt("old_publication"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_subject_providers"}},
		},
		{
			// The new accepted source pins its ORIGINAL seal receipt past ACK.
			ID: ipEntityDeltaRetainProofStmt, SQL: `UPDATE metadata_ip_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1 WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='evidence_seal' AND evidence_id=? AND proof_pin_count>0 AND proof_pin_count<9223372036854775807 AND proof_revision<9223372036854775807`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
		{
			ID: ipEntityDeltaRetainSetProofStmt, SQL: `UPDATE metadata_ip_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1 WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='set_seal' AND set_id=? AND evidence_id=? AND proof_pin_count>0 AND proof_pin_count<9223372036854775807 AND proof_revision<9223372036854775807`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("set"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
		{
			// The one terminal delta result. It names the new publication head (or
			// the retained old head for an exact no-op) and the observed source.
			ID: ipEntityDeltaRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,publication_id,proof_pin_count) VALUES(?,?,?,?,?,'delta',?,?,?,'accepted',0,0,'',?,?,1,?,?,?,?,?,?,1)`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				i("accepted_revision"), i("applied_ms"), b("result_digest"),
				i("result_count"), i("result_bytes"), t("evidence"), i("subject"),
				t("parser"), t("build"), nt("set"), t("publication"),
			},
		},
		{
			// Typed replay reads only this saved delta header, never the mutable
			// selection, publication, entity projection or search job.
			ID: ipEntityDeltaSavedStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_phase,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,publication_id,result_count,result_bytes FROM metadata_ip_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP entity delta"
		defs[x].Kind = stmt.Read
		switch defs[x].ID {
		case ipEntityDeltaPublicationStmt, ipEntityDeltaSelectionCASStmt,
			ipEntityDeltaRetainProofStmt, ipEntityDeltaRetainSetProofStmt, ipEntityDeltaRecordStmt:
			defs[x].Kind = stmt.Write
		}
		defs[x].CallSites = []string{"store/writer/ip_entity_delta.go", "store/writer/ip_entity_delta_statements.go"}
		defs[x].Cohorts = []stmt.Cohort{ipEntityDeltaCohort(defs[x])}
	}
	return defs
}

// ipEntityDeltaCohort is one named missing-key fixture per statement. Values
// follow the declared parameter types; no delta-specific authority is implied.
func ipEntityDeltaCohort(def stmt.Definition) stmt.Cohort {
	args := make([]any, len(def.Params))
	for i, p := range def.Params {
		switch p.Type {
		case stmt.Text:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = ""
			}
		case stmt.Blob:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = make([]byte, 16)
			}
		case stmt.Real:
			args[i] = 1.0
		default:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = int64(1)
			}
		}
	}
	return stmt.Cohort{Name: "missing_native_keys", Bindings: args}
}
