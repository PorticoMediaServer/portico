package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP entity evidence begin/append 210/211. The exact provider-operation owner
// (metadata_provider_operations, provider namespace) stays the admitted owner;
// the immutable observation body is the generic metadata_evidence row and its
// metadata_evidence_builds construction cursor. The IP receipt header is group
// D and fixture-qualified until that group is embedded, so these definitions
// make no Capture claim.
const (
	ipEntityEvidenceBuildStmt          stmt.ID = "ip.entity.evidence.build"
	ipEntityEvidenceReceiptReverseStmt stmt.ID = "ip.entity.evidence.receipt_reverse"
	ipEntityEvidenceInsertEvidenceStmt stmt.ID = "ip.entity.evidence.insert_evidence"
	ipEntityEvidenceInsertBuildStmt    stmt.ID = "ip.entity.evidence.insert_build"
	ipEntityEvidenceAppendStmt         stmt.ID = "ip.entity.evidence.append"
	ipEntityEvidenceAdvanceOwnerStmt   stmt.ID = "ip.entity.evidence.advance_owner"
	ipEntityEvidenceRecordStmt         stmt.ID = "ip.entity.evidence.record"
	ipEntityEvidenceAliasStmt          stmt.ID = "ip.entity.evidence.alias"
	ipEntityEvidenceExternalStmt       stmt.ID = "ip.entity.evidence.external"
	ipEntityEvidenceTermStmt           stmt.ID = "ip.entity.evidence.term"
	ipEntityEvidenceRatingStmt         stmt.ID = "ip.entity.evidence.rating"
	ipEntityEvidenceCreditStmt         stmt.ID = "ip.entity.evidence.credit"
	ipEntityEvidenceEpisodeStmt        stmt.ID = "ip.entity.evidence.episode"
	ipEntityEvidenceMediaStmt          stmt.ID = "ip.entity.evidence.media"
	ipEntityEvidenceTrackStmt          stmt.ID = "ip.entity.evidence.track"
)

// The child inserts are one closed set of normalized nonmovie evidence rows.
// Begin constructs the header and the build cursor; append only materializes a
// bounded page of one family and never marks the build ready or the evidence
// sealed.
func ipEntityEvidenceDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	n := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	defs := []stmt.Definition{
		{
			// The build row is the exact evidence construction owner: one build
			// per evidence, the admitted owner/incarnation/generation and the
			// raw cursor/digest/charge chain.
			ID: ipEntityEvidenceBuildStmt, SQL: `SELECT b.evidence_id,b.generation,b.owner_operation,b.owner_incarnation,b.owner_generation,b.state,b.phase,b.cursor,b.expected_rows,b.materialized_rows,b.materialized_charge,b.materialized_digest,b.expires_at,b.content_revision,e.subject_id,e.provider,e.subject_kind,e.provider_id,e.parser_version,e.sealed,e.observed_at FROM metadata_evidence_builds AS b JOIN metadata_evidence AS e ON e.id=b.evidence_id WHERE b.id=?`,
			Params: []stmt.Param{t("build")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_builds", "metadata_evidence"}},
		},
		{
			// The reverse receipt index proves an evidence ID is not already
			// owned by another accepted begin. This is the exact group-D
			// receipt evidence reverse path, not a job scan.
			ID: ipEntityEvidenceReceiptReverseStmt, SQL: `SELECT count(*) FROM metadata_ip_receipts WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}, Indexes: []string{"metadata_ip_receipt_evidence"}},
		},
		{
			// The observation is always inserted unsealed. Only IP-07's closed
			// seal command may write the ip seal proof columns.
			ID: ipEntityEvidenceInsertEvidenceStmt, SQL: `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,etag,last_modified,title,original_title,overview,tagline,content_rating,studio,network,country,release_date,year,artist,length_millis,artist_type,begin_year,end_year,wikidata_id,biography,biography_source,biography_source_url,disambiguation,release_group_id,group_title,primary_type,barcode,sealed) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("id"), i("subject_id"), t("parser_version"), t("provider"), t("subject_kind"),
				t("provider_id"), t("language"), t("region"), t("request_shape"), t("digest"),
				t("observed_at"), t("source_url"), t("etag"), t("last_modified"), t("title"),
				t("original_title"), t("overview"), t("tagline"), t("content_rating"), t("studio"),
				t("network"), t("country"), t("release_date"), i("year"), t("artist"),
				n("length_millis"), {Name: "artist_type", Type: stmt.Text, Nullable: true},
				n("begin_year"), n("end_year"), t("wikidata_id"), t("biography"),
				t("biography_source"), t("biography_source_url"), t("disambiguation"),
				t("release_group_id"), t("group_title"), t("primary_type"), t("barcode"), i("sealed"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			// The header is row zero; expected rows counts the header plus every
			// declared family child. The cursor starts after the header.
			ID: ipEntityEvidenceInsertBuildStmt, SQL: `INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,parent_build,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision) VALUES(?,?,?,?,?,?,NULL,'building','children',1,?,1,?,?,?,?,1)`,
			Params: []stmt.Param{t("build"), t("evidence"), i("generation"), t("owner"), b("owner_incarnation"), i("owner_generation"), i("expected_rows"), t("shape_digest"), t("digest"), i("charge"), t("expires")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			// Append is a full CAS over the exact owner, build, cursor, charge,
			// digest and content revision. It never advances state/phase and
			// cannot pass the declared expected row count.
			ID: ipEntityEvidenceAppendStmt, SQL: `UPDATE metadata_evidence_builds SET cursor=cursor+?,materialized_rows=cursor+?,materialized_charge=materialized_charge+?,materialized_digest=?,content_revision=content_revision+1 WHERE id=? AND evidence_id=? AND generation=? AND owner_operation=? AND owner_incarnation=? AND owner_generation=? AND state='building' AND phase='children' AND cursor=? AND materialized_rows=? AND materialized_charge=? AND materialized_digest=? AND content_revision=? AND cursor+?<=expected_rows RETURNING cursor,materialized_rows,materialized_charge,content_revision`,
			Params: []stmt.Param{
				i("count"), i("count_again"), i("charge"), t("digest"),
				t("build"), t("evidence"), i("generation"), t("owner"), b("owner_incarnation"),
				i("owner_generation"), i("cursor"), i("materialized_rows"), i("materialized_charge"),
				t("materialized_digest"), i("content_revision"), i("bound"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_builds"}},
		},
		{
			ID: ipEntityEvidenceAdvanceOwnerStmt, SQL: `UPDATE metadata_provider_operations SET last_command_sequence=? WHERE id=? AND provider=? AND command_incarnation=? AND generation=? AND status='claimed' AND lease=? AND lease_until=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING last_command_sequence`,
			Params: []stmt.Param{
				i("sequence"), t("operation"), t("provider"), b("incarnation"),
				i("generation"), t("lease"), t("lease_until"), i("old_last"), i("old_ack"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_operations"}},
		},
		{
			// Begin/appends save the exact evidence/build identity and the raw
			// family/ordinal/byte chain. terminal stays zero until IP-07 seals.
			ID: ipEntityEvidenceRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id) VALUES(?,?,?,?,?,?,?,?,?,'building',?,?,?,?,?,0,?,?,?,?)`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				t("kind"), i("accepted_revision"), i("applied_ms"), b("result_digest"),
				i("after_family"), i("after_ordinal"), t("after_key"), i("result_count"),
				i("result_bytes"), t("evidence"), i("subject"), t("parser"), t("build"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceAliasStmt, SQL: `INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES(?,?,?,?,?,?)`,
			Params: []stmt.Param{t("evidence"), i("ordinal"), t("requested"), t("canonical"), t("alias"), t("observed")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceExternalStmt, SQL: `INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) VALUES(?,?,?,?)`,
			Params: []stmt.Param{t("evidence"), t("provider"), t("subject_kind"), t("provider_id")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceTermStmt, SQL: `INSERT INTO metadata_evidence_terms(evidence_id,vocabulary,ordinal,value) VALUES(?,?,?,?)`,
			Params: []stmt.Param{t("evidence"), t("vocabulary"), i("ordinal"), t("value")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceRatingStmt, SQL: `INSERT INTO metadata_evidence_ratings(evidence_id,provider,role,value,scale,votes,source_url) VALUES(?,?,?,?,?,?,?)`,
			Params: []stmt.Param{t("evidence"), t("provider"), t("role"), r("value"), r("scale"), i("votes"), t("source_url")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceCreditStmt, SQL: `INSERT INTO metadata_evidence_credits(provider,evidence_kind,credit_class,evidence_id,scope,ordinal,provider_person_id,name,artist_name,sort_name,disambiguation,join_phrase,role,department,credit_id,source_ordinal,billing) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("provider"), t("evidence_kind"), t("credit_class"), t("evidence"), t("scope"),
				i("ordinal"), t("provider_person_id"), t("name"), t("artist_name"), t("sort_name"),
				t("disambiguation"), t("join_phrase"), t("role"), t("department"), t("credit_id"),
				n("source_ordinal"), n("billing"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceEpisodeStmt, SQL: `INSERT INTO metadata_evidence_episodes(provider,evidence_kind,evidence_id,ordinal,episode_evidence_id,episode_order,season_number,episode_number,absolute_number) VALUES(?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("provider"), t("evidence_kind"), t("evidence"), i("ordinal"),
				t("episode_evidence"), t("episode_order"), n("season"), n("episode"), n("absolute"),
			},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceMediaStmt, SQL: `INSERT INTO metadata_evidence_media(provider,evidence_kind,evidence_id,position,format,title) VALUES(?,?,?,?,?,?)`,
			Params: []stmt.Param{t("provider"), t("evidence_kind"), t("evidence"), i("position"), t("format"), t("title")},
			Plan:   stmt.Plan{Hot: true},
		},
		{
			ID: ipEntityEvidenceTrackStmt, SQL: `INSERT INTO metadata_evidence_tracks(provider,evidence_kind,evidence_id,track_id,medium_position,position,number,title,artist,length_millis,recording_evidence_id) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				t("provider"), t("evidence_kind"), t("evidence"), t("track_id"), i("medium_position"),
				i("position"), t("number"), t("title"), t("artist"), n("length_millis"), t("recording_evidence"),
			},
			Plan: stmt.Plan{Hot: true},
		},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP entity evidence"
		defs[x].Kind = stmt.Read
		switch defs[x].ID {
		case ipEntityEvidenceInsertEvidenceStmt, ipEntityEvidenceInsertBuildStmt, ipEntityEvidenceAppendStmt,
			ipEntityEvidenceAdvanceOwnerStmt, ipEntityEvidenceRecordStmt, ipEntityEvidenceAliasStmt,
			ipEntityEvidenceExternalStmt, ipEntityEvidenceTermStmt, ipEntityEvidenceRatingStmt,
			ipEntityEvidenceCreditStmt, ipEntityEvidenceEpisodeStmt, ipEntityEvidenceMediaStmt,
			ipEntityEvidenceTrackStmt:
			defs[x].Kind = stmt.Write
		}
		defs[x].CallSites = []string{"store/writer/ip_entity_evidence.go", "store/writer/ip_entity_evidence_statements.go"}
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
