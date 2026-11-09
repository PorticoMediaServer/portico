package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP person evidence child append 232. The person operation owner, its single
// build and the receipt header are group D and fixture-qualified until that
// group is embedded, so these definitions make no Capture claim. The alias and
// external child pages are the generic embedded evidence children; every read
// is a prefix of their primary key and every write is a bounded insert or an
// exact build/owner CAS. No latest, MAX or scan selection exists.
const (
	ipPersonAppendAliasCountStmt    stmt.ID = "ip.person.append.alias_count"
	ipPersonAppendExternalCountStmt stmt.ID = "ip.person.append.external_count"
	ipPersonAppendAliasStmt         stmt.ID = "ip.person.append.alias"
	ipPersonAppendExternalStmt      stmt.ID = "ip.person.append.external"
	ipPersonAppendBuildStmt         stmt.ID = "ip.person.append.build"
	ipPersonAppendOwnerStmt         stmt.ID = "ip.person.append.owner"
	ipPersonAppendRecordStmt        stmt.ID = "ip.person.append.record"
)

func ipPersonEvidenceAppendDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// Exact persisted count of the one selected family. The page ordinal
			// must start exactly here, so a gap or interleave cannot persist.
			ID: ipPersonAppendAliasCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_aliases WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_aliases"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			ID: ipPersonAppendExternalCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_external_ids WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// Alias rows keep their exact contiguous ordinal. The canonical ID is
			// the captured person provider ID, never another person/entity row.
			ID:     ipPersonAppendAliasStmt,
			SQL:    `INSERT INTO metadata_evidence_aliases(evidence_id,ordinal,requested_id,canonical_id,alias,observed_at) VALUES(?,?,?,?,?,?)`,
			Params: []stmt.Param{t("evidence"), i("ordinal"), t("requested"), t("canonical"), t("alias"), t("observed")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"evidence-1", int64(0), "requested-0",
				"42", "Ada", "2026-01-01T00:00:00Z"}}},
			Plan: stmt.Plan{Hot: true},
		},
		{
			// External IDs are person-kind only; the primary key refuses a
			// duplicate tuple inside one person observation.
			ID:      ipPersonAppendExternalStmt,
			SQL:     `INSERT INTO metadata_evidence_external_ids(evidence_id,provider,subject_kind,provider_id) VALUES(?,?,'person',?)`,
			Params:  []stmt.Param{t("evidence"), t("provider"), t("provider_id")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"evidence-1", "imdb", "nm0000001"}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// Append advances only the exact open build: cursor, staged rows and
			// bytes, charge and content revision. State, seal proof and identity
			// witnesses are CAS guards, never caller-selected values.
			ID: ipPersonAppendBuildStmt, SQL: `UPDATE metadata_person_builds SET cursor=cursor+?,staged_rows=staged_rows+?,staged_bytes=staged_bytes+?,materialized_charge=materialized_charge+?,content_revision=content_revision+1 WHERE id=? AND evidence_id=? AND provider=? AND subject_kind='person' AND provider_id=? AND owner_id=? AND owner_incarnation=? AND owner_generation=? AND generation=? AND state='building' AND seal_owner IS NULL AND cursor=? AND staged_rows=? AND staged_bytes=? AND materialized_charge=? AND content_revision=? RETURNING cursor,staged_rows,staged_bytes,materialized_charge,content_revision`,
			Params: []stmt.Param{i("rows"), i("staged_rows"), i("page_bytes"), i("charge"), t("build"),
				t("evidence"), t("provider"), t("provider_id"), t("owner"), b("incarnation"),
				i("owner_generation"), i("generation"), i("old_cursor"), i("old_staged_rows"),
				i("old_staged_bytes"), i("old_charge"), i("old_content_revision")},
			Cohorts: []stmt.Cohort{{Name: "appended", Bindings: []any{int64(1), int64(1), int64(128), int64(128),
				"build-1", "evidence-1", "tmdb", "42", "owner-1", make([]byte, 16), int64(1), int64(1),
				int64(1), int64(1), int64(2048), int64(2048), int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_builds"}},
		},
		{
			// The command floor advances. Owner revision, generation and lease are
			// witnesses this child page cannot change.
			ID: ipPersonAppendOwnerStmt, SQL: `UPDATE metadata_person_operations SET last_command_sequence=? WHERE id=? AND command_incarnation=? AND generation=? AND revision=? AND phase='building' AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params: []stmt.Param{i("sequence"), t("owner"), b("incarnation"), i("generation"), i("revision"),
				t("lease"), i("lease_generation"), i("lease_until"), i("old_last"), i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "advanced", Bindings: []any{int64(3), "owner-1", make([]byte, 16),
				int64(1), int64(2), "lease-1", int64(1), int64(300_000), int64(2), int64(2)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
		{
			// The append receipt retains the page family position and exact
			// evidence/build identity for replay and the seal proof pin.
			ID: ipPersonAppendRecordStmt, SQL: `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id) VALUES(?,?,?,?,?,?,?,'append',?,?,?,?,?)`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				i("applied"), b("result_digest"), i("accepted_revision"), i("after_ordinal"),
				i("result_count"), t("evidence"), t("build")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"owner-1", make([]byte, 16), int64(3),
				make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32), int64(2), int64(2),
				int64(1), "evidence-1", "build-1"}}},
			Plan: stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipPersonAppendAliasStmt, ipPersonAppendExternalStmt, ipPersonAppendBuildStmt,
			ipPersonAppendOwnerStmt, ipPersonAppendRecordStmt:
			defs[n].Kind = stmt.Write
		}
		defs[n].Owner = "store/writer IP person evidence append"
		defs[n].CallSites = []string{"store/writer/ip_person_provider_append.go", "store/writer/ip_person_provider_append_statements.go"}
	}
	return defs
}
