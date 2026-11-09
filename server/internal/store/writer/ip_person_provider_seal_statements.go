package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP person evidence seal 233. The person build, receipt header, seal proof and
// evidence carriers are group D and fixture-qualified until that group is
// embedded, so these definitions make no Capture claim. Every read is an exact
// primary key; every write is a full CAS over the one open build and its live
// owner. No latest, MAX or scan selection exists.
const (
	ipPersonSealEvidenceStmt stmt.ID = "ip.person.seal.evidence"
	ipPersonSealAliasesStmt  stmt.ID = "ip.person.seal.aliases"
	ipPersonSealExternalStmt stmt.ID = "ip.person.seal.external"
	ipPersonSealRecordStmt   stmt.ID = "ip.person.seal.record"
	ipPersonSealEvidenceUpd  stmt.ID = "ip.person.seal.seal_evidence"
	ipPersonSealBuildStmt    stmt.ID = "ip.person.seal.build"
	ipPersonSealOwnerStmt    stmt.ID = "ip.person.seal.owner"
)

func ipPersonEvidenceSealDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The exact immutable person observation BEGIN inserted. The seal may
			// only close this unsealed body, never a relabeled movie/entity one.
			ID: ipPersonSealEvidenceStmt, SQL: `SELECT subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,observed_at,digest,sealed,title,biography,biography_source,biography_source_url,source_url,etag,last_modified,birth_date,death_date,birth_place FROM metadata_evidence WHERE id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// Every persisted alias ordinal and value must equal the sealed
			// manifest exactly; the primary key prefix bounds the read.
			ID: ipPersonSealAliasesStmt, SQL: `SELECT ordinal,requested_id,canonical_id,alias,observed_at FROM metadata_evidence_aliases WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_aliases"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			ID: ipPersonSealExternalStmt, SQL: `SELECT provider,subject_kind,provider_id FROM metadata_evidence_external_ids WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// The seal receipt carries the complete proof tuple. The unique
			// (owner,incarnation,sequence,kind,evidence) key and the build proof FK
			// make it the single seal pin for this evidence body.
			ID: ipPersonSealRecordStmt, SQL: `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count) VALUES(?,?,?,?,?,?,?,'evidence_seal',?,0,1,?,?,?,?,?,?,?,1)`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				i("applied"), b("result_digest"), i("accepted_revision"), t("evidence"), t("build"),
				t("algorithm"), i("version"), b("commitment"), i("rows"), i("bytes")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"owner-1", make([]byte, 16), int64(4),
				make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(3), "evidence-1",
				"build-1", "portico-ip-person-seal.sha256.v1", int64(1), make([]byte, 32), int64(3),
				int64(2048)}}},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: ipPersonSealEvidenceUpd, SQL: `UPDATE metadata_evidence SET sealed=1 WHERE id=? AND subject_id=? AND parser_version=? AND provider=? AND subject_kind='person' AND provider_id=? AND language=? AND region=? AND request_shape=? AND digest=? AND observed_at=? AND sealed=0`,
			Params: []stmt.Param{t("evidence"), i("subject"), t("parser"), t("provider"), t("provider_id"),
				t("language"), t("region"), t("shape"), t("digest"), t("observed")},
			Cohorts: []stmt.Cohort{{Name: "sealed", Bindings: []any{"evidence-1", int64(42),
				"tmdb.person.header-exact-dates.v1", "tmdb", "42", "en-US", "US",
				"tmdb.person.detail.header-exact-dates.v1",
				"0000000000000000000000000000000000000000000000000000000000000000",
				"2026-01-01T00:00:00Z"}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
		},
		{
			// Finalize is the ONLY command that marks a person build ready or pins
			// its seal. All six staged/expected/charge witnesses are required.
			ID: ipPersonSealBuildStmt, SQL: `UPDATE metadata_person_builds SET state='ready',seal_owner=?,seal_incarnation=?,seal_sequence=?,staged_rows=?,staged_bytes=?,materialized_charge=materialized_charge+?,content_revision=content_revision+1 WHERE id=? AND evidence_id=? AND provider=? AND subject_kind='person' AND provider_id=? AND owner_id=? AND owner_incarnation=? AND owner_generation=? AND generation=? AND state='building' AND seal_owner IS NULL AND cursor=? AND staged_rows=? AND staged_bytes=? AND materialized_charge=? AND content_revision=? RETURNING content_revision,staged_rows,staged_bytes,materialized_charge`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), i("rows"), i("bytes"),
				i("charge"), t("build"), t("evidence"), t("provider"), t("provider_id"), t("owner_id"),
				b("owner_incarnation"), i("owner_generation"), i("generation"), i("old_cursor"),
				i("old_staged_rows"), i("old_staged_bytes"), i("old_charge"), i("old_content_revision")},
			Cohorts: []stmt.Cohort{{Name: "sealed", Bindings: []any{"owner-1", make([]byte, 16), int64(4),
				int64(3), int64(2048), int64(16), "build-1", "evidence-1", "tmdb", "42", "owner-1",
				make([]byte, 16), int64(1), int64(1), int64(3), int64(3), int64(2032), int64(2032), int64(2)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_builds"}},
		},
		{
			// Seal closes the owner phase and advances its revision exactly once.
			ID: ipPersonSealOwnerStmt, SQL: `UPDATE metadata_person_operations SET phase='sealed',revision=revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND generation=? AND revision=? AND phase='building' AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params: []stmt.Param{i("sequence"), t("owner"), b("incarnation"), i("generation"), i("revision"),
				t("lease"), i("lease_generation"), i("lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "sealed", Bindings: []any{int64(4), "owner-1", make([]byte, 16),
				int64(1), int64(2), "lease-1", int64(1), int64(300_000), int64(3), int64(3)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipPersonSealRecordStmt, ipPersonSealEvidenceUpd, ipPersonSealBuildStmt, ipPersonSealOwnerStmt:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP person evidence seal"
		defs[n].CallSites = []string{"store/writer/ip_person_provider_seal.go", "store/writer/ip_person_provider_seal_statements.go"}
	}
	return defs
}
