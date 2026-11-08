package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	ipEntryEntityOwner   stmt.ID = "ip.entry.entity_owner"
	ipEntryPersonOwner   stmt.ID = "ip.entry.person_owner"
	ipEntryHeader        stmt.ID = "ip.entry.header"
	ipEntryEntity        stmt.ID = "ip.entry.entity"
	ipEntryPerson        stmt.ID = "ip.entry.person"
	ipEntryLibrary       stmt.ID = "ip.entry.library"
	ipEntryFreshEntity   stmt.ID = "ip.entry.fresh_entity"
	ipEntryFreshPerson   stmt.ID = "ip.entry.fresh_person"
	ipEntryPolicy        stmt.ID = "ip.entry.policy"
	ipEntryScreenConsent stmt.ID = "ip.entry.screen_consent"
	ipEntryMusicConsent  stmt.ID = "ip.entry.music_consent"
)

// Native comparisons copy fixed primitives only. Owner history remains separate
// from current mutable source/provider policy/consent eligibility.
func ipEntryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: ipEntryEntityOwner, SQL: `SELECT id,command_incarnation,last_command_sequence,acknowledged_command_sequence,CASE WHEN kind=? THEN 1 ELSE 0 END,retired_ms IS NULL FROM metadata_edit_owners WHERE entity_id=? AND person_id IS NULL`, Params: []stmt.Param{t("kind"), i("target")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_edit_owners_1"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{"item", int64(777)}}}},
		{ID: ipEntryPersonOwner, SQL: `SELECT id,command_incarnation,last_command_sequence,acknowledged_command_sequence,CASE WHEN kind='person' THEN 1 ELSE 0 END,retired_ms IS NULL FROM metadata_edit_owners WHERE person_id=? AND entity_id IS NULL`, Params: []stmt.Param{i("target")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_edit_owners_2"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777)}}}},
		{ID: ipEntryHeader, SQL: `SELECT operation_id,request_digest,CASE command_kind WHEN 'field_edit' THEN 240 WHEN 'field_lock' THEN 241 WHEN 'field_restore' THEN 242 ELSE 0 END,result_count,result_digest FROM metadata_edit_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{i("owner"), b("incarnation"), i("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_edit_receipts"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777), make([]byte, 16), int64(1)}}}},
		{ID: ipEntryEntity, SQL: `SELECT kind FROM catalog_subjects WHERE id=? AND library_id=?`, Params: []stmt.Param{i("target"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_subjects"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777), int64(777)}}}},
		{ID: ipEntryPerson, SQL: `SELECT 1 FROM catalog_people WHERE id=?`, Params: []stmt.Param{i("target")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777)}}}},
		{ID: ipEntryLibrary, SQL: `SELECT CASE WHEN library_id=? THEN 1 ELSE 0 END FROM catalog_libraries WHERE id=?`, Params: []stmt.Param{t("key"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{"library", int64(777)}}}},
		{ID: ipEntryFreshEntity, SQL: `SELECT 1 FROM catalog_entities AS e CROSS JOIN catalog_libraries AS l ON l.id=e.library_id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.metadata_incarnation=? AND e.source_revision=? AND e.identity_revision=? AND e.metadata_revision=? AND e.retired=0 AND l.retired=0`, Params: []stmt.Param{i("target"), i("library"), i("kind"), b("incarnation"), i("source"), i("identity"), i("metadata")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "l"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777), int64(777), int64(1), make([]byte, 16), int64(1), int64(1), int64(1)}}}},
		{ID: ipEntryFreshPerson, SQL: `SELECT 1 FROM catalog_people WHERE id=? AND token=? AND revision=?`, Params: []stmt.Param{i("target"), t("token"), i("revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777), "person", int64(1)}}}},
		{ID: ipEntryPolicy, SQL: `SELECT 1 FROM metadata_provider_policy AS p CROSS JOIN metadata_providers AS v ON v.code=p.provider WHERE p.library_id=? AND p.provider=? AND p.incarnation=? AND p.revision=? AND p.enabled=1 AND v.enabled=1`, Params: []stmt.Param{i("library"), t("provider"), b("incarnation"), i("revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"p", "v"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(777), "tmdb", make([]byte, 16), int64(1)}}}},
		{ID: ipEntryScreenConsent, SQL: `SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND confirmed=1 AND revision=?`, Params: []stmt.Param{i("revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"screen_metadata_consent"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(1)}}}},
		{ID: ipEntryMusicConsent, SQL: `SELECT 1 FROM metadata_runtime_settings WHERE singleton=1 AND online_lookups=1 AND lookup_epoch=?`, Params: []stmt.Param{i("epoch")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_runtime_settings"}}, Cohorts: []stmt.Cohort{{Name: "missing", Bindings: []any{int64(1)}}}},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer IP owner entry"
		defs[n].CallSites = []string{"store/writer/ip_command_entry.go"}
	}
	return append(usAuthorityDefinitions(), defs...)
}
