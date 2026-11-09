package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP person provider typed delta replace 235. The NEW observation is consumed
// through the same exact helpers the person accept uses (sealed build,
// evidence header, original person seal receipt, complete alias/external
// cohort, live owner and search intent); this file defines only the delta's own
// exact-old-head reads and its terminal receipt. The old publication, evidence
// and seal proof stay charged; the delta never mutates or reclaims them, and
// IP-17 owns their bounded retirement. Group D is not embedded, so these
// definitions make no Capture claim.
const (
	ipPersonDeltaPriorPublicationStmt stmt.ID = "ip.person.delta.prior_publication"
	ipPersonDeltaPriorEvidenceStmt    stmt.ID = "ip.person.delta.prior_evidence"
	ipPersonDeltaPriorSealStmt        stmt.ID = "ip.person.delta.prior_seal"
	ipPersonDeltaRecordStmt           stmt.ID = "ip.person.delta.record"
)

func ipPersonDeltaDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The exact old accepted person publication head. It must still be the
			// one named by the current source slot, never merely a same-person row.
			ID: ipPersonDeltaPriorPublicationStmt, SQL: `SELECT person_id,provider,subject_kind,provider_id,evidence_id,operation_id,accepted_revision,observed_at,proof_owner,proof_incarnation,proof_sequence,proof_kind FROM metadata_person_publications WHERE id=?`,
			Params: []stmt.Param{t("publication")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_publications"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"publication-1"}},
			},
		},
		{
			// The retained upstream observation header survives the old
			// publication/evidence for the exact no-op digest and namespace.
			ID: ipPersonDeltaPriorEvidenceStmt, SQL: `SELECT provider,subject_kind,provider_id,parser_version,language,region,request_shape,digest,sealed FROM metadata_evidence WHERE id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// The old ORIGINAL seal receipt must still be retained with a live pin.
			ID: ipPersonDeltaPriorSealStmt, SQL: `SELECT proof_pin_count FROM metadata_person_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='evidence_seal' AND evidence_id=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", make([]byte, 16), int64(4), "evidence-1"}},
			},
		},
		{
			// The one terminal delta result. It names the new publication head (or
			// the retained old head for an exact no-op) and the observed source.
			ID: ipPersonDeltaRecordStmt, SQL: `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id,publication_id,proof_pin_count) VALUES(?,?,?,?,?,?,?,'delta',?,0,?,?,?,?,1)`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"),
				b("request"), i("applied"), b("result_digest"), i("accepted_revision"),
				i("result_count"), t("evidence"), t("build"), t("publication")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"owner-1", make([]byte, 16),
				int64(8), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32),
				int64(3), int64(5), "evidence-2", "build-2", "publication-2"}}},
			Plan: stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IP person provider delta"
		defs[n].Kind = stmt.Read
		if defs[n].ID == ipPersonDeltaRecordStmt {
			defs[n].Kind = stmt.Write
		}
		defs[n].CallSites = []string{"store/writer/ip_person_provider_delta.go",
			"store/writer/ip_person_provider_delta_statements.go"}
	}
	return defs
}
