package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP person provider custody 206/207/208 and person evidence BEGIN 209. The
// person operation owner, receipt header and build are group D and
// fixture-qualified until that group is embedded, so these definitions make no
// Capture claim. Every lookup uses an exact retained key: the person token, the
// immutable provider-subject tuple, the person operation PK or the unique
// (owner_id,owner_incarnation) build key. No latest/MAX selection exists.
const (
	ipPersonPersonStmt         stmt.ID = "ip.person.person"
	ipPersonSubjectStmt        stmt.ID = "ip.person.subject"
	ipPersonPolicyStmt         stmt.ID = "ip.person.policy"
	ipPersonOwnerStmt          stmt.ID = "ip.person.owner"
	ipPersonReceiptStmt        stmt.ID = "ip.person.receipt"
	ipPersonInsertOwnerStmt    stmt.ID = "ip.person.insert_owner"
	ipPersonRenewStmt          stmt.ID = "ip.person.renew"
	ipPersonFinishStmt         stmt.ID = "ip.person.finish"
	ipPersonAdvanceOwnerStmt   stmt.ID = "ip.person.advance_owner"
	ipPersonBuildOwnerStmt     stmt.ID = "ip.person.build_owner"
	ipPersonReceiptReverseStmt stmt.ID = "ip.person.build_reverse"
	ipPersonBuildStmt          stmt.ID = "ip.person.build"
	ipPersonInsertEvidenceStmt stmt.ID = "ip.person.insert_evidence"
	ipPersonInsertBuildStmt    stmt.ID = "ip.person.insert_build"
	ipPersonRecoveryStmt       stmt.ID = "ip.person.recovery"
	ipPersonRecordStmt         stmt.ID = "ip.person.record"
)

func ipPersonDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{
			// Exact captured person identity. No latest person, name search or
			// numeric-ID coincidence may stand in for this token/revision.
			ID: ipPersonPersonStmt, SQL: `SELECT revision FROM catalog_people WHERE id=? AND token=?`,
			Params: []stmt.Param{i("person"), t("token")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(42), "person-token-42"}},
				{Name: "missing", Bindings: []any{int64(42), "person-token-99"}},
			},
		},
		{
			// The immutable provider subject tuple is the person namespace. The
			// numeric subject ID is never treated as a catalogue person ID.
			ID: ipPersonSubjectStmt, SQL: `SELECT incarnation,revision FROM metadata_provider_subjects WHERE id=? AND provider=? AND subject_kind='person' AND provider_id=? AND language=? AND region=? AND request_shape=?`,
			Params: []stmt.Param{i("subject"), t("provider"), t("provider_id"), t("language"), t("region"), t("shape")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_subjects"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(501), "tmdb", "42", "en-US", "US", "tmdb.person.detail.header-exact-dates.v1"}},
				{Name: "missing", Bindings: []any{int64(501), "tvdb", "42", "en-US", "US", "tmdb.person.detail.header-exact-dates.v1"}},
			},
		},
		{
			// One retained library/provider policy. Incarnation and revision are
			// compared with the captured owner values by the caller.
			ID: ipPersonPolicyStmt, SQL: `SELECT incarnation,revision,enabled,region FROM metadata_provider_policy WHERE library_id=? AND provider=?`,
			Params: []stmt.Param{i("library"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_provider_policy"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(777), "tmdb"}},
				{Name: "missing", Bindings: []any{int64(777), "tvdb"}},
			},
		},
		{
			// Exact person operation owner joined to its retained library. The
			// person token/revision and namespace tuple are captured witnesses.
			ID: ipPersonOwnerStmt, SQL: `SELECT o.command_incarnation,o.last_command_sequence,o.acknowledged_command_sequence,o.generation,o.revision,o.phase,o.lease_owner,o.lease_generation,o.lease_until_ms,o.next_attempt_ms,o.person_id,o.captured_person_token,o.captured_person_revision,o.subject_id,o.subject_incarnation,o.subject_revision,o.policy_incarnation,o.policy_revision,o.consent_revision,o.provider,o.provider_id,o.language,o.region,o.request_shape,o.library_id,l.library_id,l.retired FROM metadata_person_operations AS o JOIN catalog_libraries AS l ON l.id=o.library_id WHERE o.id=? AND o.provider=? AND o.provider_id=? AND o.person_id=?`,
			Params: []stmt.Param{t("owner"), t("provider"), t("provider_id"), i("person")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"o", "l"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", "tmdb", "42", int64(42)}},
				{Name: "missing", Bindings: []any{"owner-1", "tvdb", "42", int64(42)}},
			},
		},
		{
			ID: ipPersonReceiptStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_count,evidence_id,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes FROM metadata_person_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_receipts"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", make([]byte, 16), int64(1)}},
				{Name: "missing", Bindings: []any{"owner-1", make([]byte, 16), int64(2)}},
			},
		},
		{
			// One exact operation birth from the captured person token/revision,
			// subject tuple and policy. The claim keeps owner sequence 1 so a lost
			// receipt is resolved only from the durable owner and receipt rows.
			ID: ipPersonInsertOwnerStmt, SQL: `INSERT INTO metadata_person_operations(id,person_id,provider,library_id,subject_id,subject_kind,provider_id,language,region,request_shape,captured_person_token,captured_person_revision,subject_incarnation,subject_revision,policy_incarnation,policy_revision,consent_revision,generation,revision,phase,lease_owner,lease_generation,lease_until_ms,command_incarnation,last_command_sequence) VALUES(?,?,?,?,?,'person',?,?,?,?,?,?,?,?,?,?,?,1,1,'claimed',?,1,?,?,1)`,
			Params:  []stmt.Param{t("owner"), i("person"), t("provider"), i("library"), i("subject"), t("provider_id"), t("language"), t("region"), t("shape"), t("token"), i("person_revision"), b("subject_incarnation"), i("subject_revision"), b("policy_incarnation"), i("policy_revision"), i("consent"), t("lease"), i("lease_until"), b("command_incarnation")},
			Cohorts: []stmt.Cohort{{Name: "created", Bindings: []any{"owner-1", int64(42), "tmdb", int64(777), int64(501), "42", "en-US", "US", "tmdb.person.detail.header-exact-dates.v1", "person-token-42", int64(1), make([]byte, 16), int64(1), make([]byte, 16), int64(1), int64(1), "lease-1", int64(300_000), make([]byte, 16)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// Renew is a full CAS over the exact live owner and advances only its
			// lease generation/revision/floor. A stale lease or person cannot renew.
			ID: ipPersonRenewStmt, SQL: `UPDATE metadata_person_operations SET lease_until_ms=?,lease_generation=lease_generation+1,revision=revision+1,last_command_sequence=? WHERE id=? AND provider=? AND provider_id=? AND person_id=? AND command_incarnation=? AND generation=? AND revision=? AND phase='claimed' AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision,lease_generation`,
			Params:  []stmt.Param{i("lease_until"), i("sequence"), t("owner"), t("provider"), t("provider_id"), i("person"), b("incarnation"), i("generation"), i("revision"), t("lease"), i("lease_generation"), i("old_lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "renewed", Bindings: []any{int64(600_000), int64(2), "owner-1", "tmdb", "42", int64(42), make([]byte, 16), int64(1), int64(1), "lease-1", int64(1), int64(300_000), int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
		{
			// Finish is a full CAS over any live nonterminal phase. Retry schedules
			// the next attempt; done closes the operation and forbids a build.
			ID: ipPersonFinishStmt, SQL: `UPDATE metadata_person_operations SET phase=?,lease_until_ms=?,next_attempt_ms=?,revision=revision+1,last_command_sequence=? WHERE id=? AND provider=? AND provider_id=? AND person_id=? AND command_incarnation=? AND generation=? AND revision=? AND phase IN('claimed','building','sealed','stale') AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params:  []stmt.Param{t("phase"), i("lease_until"), stmt.Param{Name: "next_attempt", Type: stmt.Integer, Nullable: true}, i("sequence"), t("owner"), t("provider"), t("provider_id"), i("person"), b("incarnation"), i("generation"), i("revision"), t("lease"), i("lease_generation"), i("old_lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "finished", Bindings: []any{"done", int64(400_000), nil, int64(3), "owner-1", "tmdb", "42", int64(42), make([]byte, 16), int64(1), int64(2), "lease-1", int64(2), int64(600_000), int64(2), int64(2)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
		{
			// BEGIN claims the exact live owner and opens the single build. The
			// sequence floor and prior acknowledgement are witnesses.
			ID: ipPersonAdvanceOwnerStmt, SQL: `UPDATE metadata_person_operations SET phase='building',revision=revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND generation=? AND revision=? AND phase='claimed' AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params:  []stmt.Param{i("sequence"), t("owner"), b("incarnation"), i("generation"), i("revision"), t("lease"), i("lease_generation"), i("lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "advanced", Bindings: []any{int64(2), "owner-1", make([]byte, 16), int64(1), int64(1), "lease-1", int64(1), int64(300_000), int64(1), int64(1)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
		{
			// Exactly one build per owner/incarnation, probed through the unique
			// index before the insert so a second BEGIN is a clear conflict.
			ID: ipPersonBuildOwnerStmt, SQL: `SELECT count(*) FROM metadata_person_builds INDEXED BY metadata_person_build_owner WHERE owner_id=? AND owner_incarnation=?`,
			Params: []stmt.Param{t("owner"), b("incarnation")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_person_build_owner"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", make([]byte, 16)}},
			},
		},
		{
			// Reverse BEGIN receipt index: an evidence ID already claimed by a
			// receipt cannot be reused by another operation.
			ID: ipPersonReceiptReverseStmt, SQL: `SELECT count(*) FROM metadata_person_receipts WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_person_receipt_evidence"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			ID: ipPersonBuildStmt, SQL: `SELECT evidence_id,owner_id,owner_incarnation,owner_generation,generation,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,materialized_charge,content_revision,provider,subject_kind,provider_id FROM metadata_person_builds WHERE id=?`,
			Params: []stmt.Param{t("build")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_builds"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"build-1"}},
			},
		},
		{
			// The observation is always inserted unsealed with exact person
			// carriers. Only the later person seal command may write the proof.
			ID: ipPersonInsertEvidenceStmt, SQL: `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,etag,last_modified,title,original_title,overview,tagline,content_rating,studio,network,country,release_date,year,artist,length_millis,artist_type,begin_year,end_year,wikidata_id,biography,biography_source,biography_source_url,disambiguation,release_group_id,group_title,primary_type,barcode,birth_date,death_date,birth_place,sealed) VALUES(?,?,?,?,'person',?,?,?,?,?,?,?,?,?,?,'','','','','','','','',0,'',NULL,NULL,NULL,NULL,'',?,?,?,'','','','','',?,?,?,0)`,
			Params:  []stmt.Param{t("id"), i("subject"), t("parser"), t("provider"), t("provider_id"), t("language"), t("region"), t("shape"), t("digest"), t("observed_at"), t("source_url"), t("etag"), t("last_modified"), t("title"), t("biography"), t("biography_source"), t("biography_source_url"), t("birth_date"), t("death_date"), t("birth_place")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"evidence-1", int64(501), "tmdb.person.header-exact-dates.v1", "tmdb", "42", "en-US", "US", "tmdb.person.detail.header-exact-dates.v1", "0000000000000000000000000000000000000000000000000000000000000000", "2026-01-01T00:00:00Z", "https://api.themoviedb.org/3/person/42", "", "", "Ada Lovelace", "Mathematician.", "tmdb", "https://api.themoviedb.org/3/person/42", "1815-12-10", "1852-11-27", "London"}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// The unique owner build carries the expected complete manifest
			// commitment and bytes; BEGIN records only the header prefix.
			ID: ipPersonInsertBuildStmt, SQL: `INSERT INTO metadata_person_builds(id,evidence_id,provider,subject_kind,provider_id,owner_id,owner_incarnation,owner_generation,generation,content_revision,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,materialized_charge) VALUES(?,?,?,'person',?,?,?,?,?,1,'building',1,?,?,?,?,1,?,?)`,
			Params:  []stmt.Param{t("build"), t("evidence"), t("provider"), t("provider_id"), t("owner"), b("owner_incarnation"), i("owner_generation"), i("generation"), i("expected_rows"), i("expected_bytes"), t("algorithm"), b("commitment"), i("staged_bytes"), i("charge")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"build-1", "evidence-1", "tmdb", "42", "owner-1", make([]byte, 16), int64(1), int64(1), int64(3), int64(2048), "portico-ip-person-source-sha256-v1", make([]byte, 32), int64(1024), int64(1024)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// The unique build-owner index is the ONLY recovery selector: no
			// latest build, MAX, generation scan or ID-tail fallback.
			ID: ipPersonRecoveryStmt, SQL: `SELECT o.generation,o.phase,o.revision,b.id,b.evidence_id,b.generation,b.state,b.cursor,b.expected_rows,b.expected_bytes,b.staged_rows,b.staged_bytes,b.materialized_charge,b.content_revision,b.expected_algorithm,b.expected_commitment,b.owner_generation,b.owner_id FROM metadata_person_operations AS o JOIN catalog_people AS p ON p.id=o.person_id LEFT JOIN metadata_person_builds AS b INDEXED BY metadata_person_build_owner ON b.owner_id=o.id AND b.owner_incarnation=o.command_incarnation WHERE o.id=? AND o.command_incarnation=? AND o.person_id=? AND p.token=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("person"), t("token")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"o", "p"}, Indexes: []string{"metadata_person_build_owner"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", make([]byte, 16), int64(42), "person-token-42"}},
			},
		},
		{
			ID: ipPersonRecordStmt, SQL: `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id) VALUES(?,?,?,?,?,?,?,?,?,0,?,?,?)`,
			Params:  []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation_id"), b("request_digest"), i("applied_ms"), b("result_digest"), t("kind"), i("accepted_revision"), i("result_count"), nt("evidence"), nt("build")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"owner-1", make([]byte, 16), int64(1), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), "begin", int64(1), int64(1), nil, nil}}},
			Plan:    stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipPersonInsertOwnerStmt, ipPersonRenewStmt, ipPersonFinishStmt, ipPersonAdvanceOwnerStmt,
			ipPersonInsertEvidenceStmt, ipPersonInsertBuildStmt, ipPersonRecordStmt:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP person provider"
		defs[n].CallSites = []string{"store/writer/ip_person_provider_lifecycle.go", "store/writer/ip_person_provider_evidence.go", "store/writer/ip_person_provider_recovery.go"}
	}
	return defs
}
