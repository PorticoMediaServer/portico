package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP entity evidence seal 212. The exact provider-operation owner
// (metadata_provider_operations) and the generic metadata_evidence_builds
// prefix stay the admitted construction owner; the immutable observation body
// remains the generic metadata_evidence row. The IP proof tuple, the receipt
// seal unique and the evidence proof index are group D and fixture-qualified
// until that group is embedded, so these definitions make no Capture claim.
const (
	ipEntitySealBuildStmt          stmt.ID = "ip.entity.seal.build"
	ipEntitySealReceiptReverseStmt stmt.ID = "ip.entity.seal.receipt_reverse"
	ipEntitySealFinalizeStmt       stmt.ID = "ip.entity.seal.finalize"
	ipEntitySealRecordStmt         stmt.ID = "ip.entity.seal.record"
	ipEntitySealAttachStmt         stmt.ID = "ip.entity.seal.attach"
	ipEntitySealSavedStmt          stmt.ID = "ip.entity.seal.saved"
	ipEntitySealAliasCountStmt     stmt.ID = "ip.entity.seal.alias_count"
	ipEntitySealExternalCountStmt  stmt.ID = "ip.entity.seal.external_count"
	ipEntitySealTermCountStmt      stmt.ID = "ip.entity.seal.term_count"
	ipEntitySealRatingCountStmt    stmt.ID = "ip.entity.seal.rating_count"
	ipEntitySealCreditCountStmt    stmt.ID = "ip.entity.seal.credit_count"
	ipEntitySealEpisodeCountStmt   stmt.ID = "ip.entity.seal.episode_count"
	ipEntitySealMediaCountStmt     stmt.ID = "ip.entity.seal.media_count"
	ipEntitySealTrackCountStmt     stmt.ID = "ip.entity.seal.track_count"
)

// The seal reads the complete construction cursor (exact count, shape and
// digest chain) from the one build PK and the immutable evidence body, proves
// no earlier seal receipt owns the evidence, finalizes the build to ready,
// saves one terminal evidence_seal receipt, attaches the IP proof tuple to the
// unsealed evidence and advances the exact owner sequence. No statement writes
// an EN seal column or an accepted source/publication.
func ipEntitySealDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The exact build/evidence tuple and construction cursor. The join
			// stays on the two primary keys; no child scan chooses the build.
			ID: ipEntitySealBuildStmt, SQL: `SELECT b.evidence_id,b.generation,b.owner_operation,b.owner_incarnation,b.owner_generation,b.state,b.phase,b.cursor,b.expected_rows,b.materialized_rows,b.materialized_charge,b.expected_shape_digest,b.materialized_digest,b.expires_at,b.content_revision,e.subject_id,e.provider,e.subject_kind,e.provider_id,e.parser_version,e.language,e.region,e.request_shape,e.observed_at,e.digest,e.sealed,e.seal_proof_owner,e.ip_seal_owner FROM metadata_evidence_builds AS b JOIN metadata_evidence AS e ON e.id=b.evidence_id WHERE b.id=?`,
			Params: []stmt.Param{t("build")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_builds", "metadata_evidence"}},
		},
		{
			// One evidence exact seal unique: no second evidence_seal receipt may
			// own this evidence. The reverse evidence index is the exact group-D
			// receipt path, never a whole-owner scan.
			ID: ipEntitySealReceiptReverseStmt, SQL: `SELECT count(*) FROM metadata_ip_receipts WHERE evidence_id=? AND command_kind='evidence_seal'`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}, Indexes: []string{"metadata_ip_receipt_evidence"}},
		},
		{
			// The build advances to ready only from the complete exact prefix and
			// the unchanged staged digest chain. It never rewinds state/phase.
			ID: ipEntitySealFinalizeStmt, SQL: `UPDATE metadata_evidence_builds SET state='ready',phase='sealed',content_revision=content_revision+1,materialized_digest=?,materialized_charge=materialized_charge+? WHERE id=? AND evidence_id=? AND generation=? AND content_revision=? AND state='building' AND phase='children' AND cursor=? AND expected_rows=? AND materialized_rows=? AND materialized_digest=? AND materialized_charge=? AND owner_operation=? AND owner_incarnation=? AND owner_generation=? RETURNING content_revision,cursor,materialized_charge`,
			Params: []stmt.Param{
				t("manifest"), i("proof_charge"), t("build"), t("evidence"), i("generation"),
				i("content_revision"), i("cursor"), i("expected"), i("materialized"),
				t("staged_digest"), i("staged_charge"), t("owner"), b("incarnation"),
				i("owner_generation"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_builds"}},
		},
		{
			// The receipt is terminal and carries the complete seal manifest. The
			// evidence FK pins the exact evidence/subject/parser tuple.
			ID: ipEntitySealRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count,entity_id,provider,evidence_subject_kind,evidence_provider_id) VALUES(?,?,?,?,?,'evidence_seal',?,?,?,'sealed',0,0,'',0,?,1,?,?,?,?,?,?,?,?,?,1,?,?,?,?)`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				i("accepted_revision"), i("applied_ms"), b("result_digest"), i("result_bytes"),
				t("evidence"), i("subject"), t("parser"), t("build"), t("algorithm"),
				i("version"), b("commitment"), i("seal_rows"), i("seal_bytes"),
				i("entity"), t("provider"), t("subject_kind"), t("provider_id"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			// The unsealed generic evidence body gains the IP proof tuple. The
			// EN seal proof keys stay NULL: the two families are exclusive.
			ID: ipEntitySealAttachStmt, SQL: `UPDATE metadata_evidence SET sealed=1,ip_seal_owner=?,ip_seal_incarnation=?,ip_seal_sequence=? WHERE id=? AND subject_id=? AND parser_version=? AND provider=? AND subject_kind=? AND provider_id=? AND language=? AND region=? AND request_shape=? AND digest=? AND observed_at=? AND sealed=0 AND seal_proof_owner IS NULL AND ip_seal_owner IS NULL`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), t("evidence"), i("subject"),
				t("parser"), t("provider"), t("subject_kind"), t("provider_id"), t("language"),
				t("region"), t("request_shape"), t("digest"), t("observed"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
		},
		{
			// The exact saved seal header, including every manifest witness used by
			// typed replay. A replay never reads mutable build/evidence state.
			ID: ipEntitySealSavedStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_phase,terminal,evidence_id,evidence_subject_id,parser_version,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes FROM metadata_ip_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
		{
			ID: ipEntitySealAliasCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_aliases"}},
		},
		{
			ID: ipEntitySealExternalCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_external_ids WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
		},
		{
			ID: ipEntitySealTermCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_terms WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_terms"}},
		},
		{
			ID: ipEntitySealRatingCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_ratings WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_ratings"}},
		},
		{
			ID: ipEntitySealCreditCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_credits"}},
		},
		{
			ID: ipEntitySealEpisodeCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_episodes WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_episodes"}},
		},
		{
			ID: ipEntitySealMediaCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_media WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_media"}},
		},
		{
			ID: ipEntitySealTrackCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_tracks WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_tracks"}},
		},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP entity seal"
		defs[x].Kind = stmt.Read
		switch defs[x].ID {
		case ipEntitySealFinalizeStmt, ipEntitySealRecordStmt, ipEntitySealAttachStmt:
			defs[x].Kind = stmt.Write
		}
		defs[x].CallSites = []string{"store/writer/ip_entity_seal.go", "store/writer/ip_entity_seal_statements.go"}
		var args []any
		for _, p := range defs[x].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "")
			case stmt.Blob:
				args = append(args, make([]byte, 16))
			case stmt.Real:
				args = append(args, 1.0)
			default:
				if p.Nullable {
					args = append(args, nil)
				} else {
					args = append(args, int64(1))
				}
			}
		}
		defs[x].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: args}}
	}
	return defs
}
