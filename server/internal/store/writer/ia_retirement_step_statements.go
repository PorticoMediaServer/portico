package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	iaRetireUnitNamespace        stmt.ID = "ia.retire.unit.namespace"
	iaRetireUnitLease            stmt.ID = "ia.retire.unit.lease"
	iaRetireUnitLibrary          stmt.ID = "ia.retire.unit.library"
	iaRetireUnitSource           stmt.ID = "ia.retire.unit.source"
	iaRetireUnitSourceAsset      stmt.ID = "ia.retire.unit.source_asset"
	iaRetireUnitAsset            stmt.ID = "ia.retire.unit.asset"
	iaRetireUnitSession          stmt.ID = "ia.retire.unit.session"
	iaRetireUnitSessionDelete    stmt.ID = "ia.retire.unit.session_delete"
	iaRetireUnitSessionRemaining stmt.ID = "ia.retire.unit.session_remaining"
	iaRetireUnitOwnerCAS         stmt.ID = "ia.retire.unit.owner_cas"
	iaRetireUnitSubject          stmt.ID = "ia.retire.unit.subject"
	iaRetireUnitPersonalHold     stmt.ID = "ia.retire.unit.personal_hold"
)

func iaRetirementUnitDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: iaRetireUnitNamespace, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_libraries NOT INDEXED WHERE id=? AND library_id=?`, Params: []stmt.Param{i("library"), t("key")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}}},
		{ID: iaRetireUnitLease, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_removal_progress WHERE kind=? AND target=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND phase=? AND entity_kind=? AND entity_cursor=? AND object_cursor=? AND walk_removed=? AND walk_kept=?`, Params: []stmt.Param{t("kind"), t("target"), i("revision"), i("owner"), i("generation"), i("expires"), i("phase"), i("entity_kind"), i("entity_cursor"), t("object_cursor"), i("removed"), i("kept")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}}},
		{ID: iaRetireUnitLibrary, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_libraries WHERE id=? AND library_id=? AND root=? AND retired=1`, Params: []stmt.Param{i("library"), t("key"), t("root")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}}},
		{ID: iaRetireUnitSource, Kind: stmt.Read, SQL: `SELECT 1 FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=? AND root=? AND root_identity=? AND incarnation=? AND generation=? AND enabled=0`, Params: []stmt.Param{t("source"), t("key"), t("root"), t("identity"), t("incarnation"), i("generation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}}},
		{ID: iaRetireUnitSourceAsset, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=? AND source_id=? AND root_incarnation=? LIMIT 1`, Params: []stmt.Param{t("asset"), t("source"), t("incarnation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object"}}},
		{ID: iaRetireUnitAsset, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_assets WHERE id=? AND token=? AND library_id=?`, Params: []stmt.Param{i("asset"), t("token"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}},
		{ID: iaRetireUnitSession, Kind: stmt.Read, SQL: `SELECT generation,mp_revision FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND asset_id=? AND generation=? AND mp_revision=? AND mp_execution_id=?`, Params: []stmt.Param{t("session"), t("asset"), i("generation"), i("revision"), t("execution")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_sessions_1"}}},
		{ID: iaRetireUnitSessionDelete, Kind: stmt.Write, SQL: `DELETE FROM playback_sessions INDEXED BY sqlite_autoindex_playback_sessions_1 WHERE id=? AND asset_id=? AND generation=? AND mp_revision=? AND mp_execution_id=?`, Params: []stmt.Param{t("session"), t("asset"), i("generation"), i("revision"), t("execution")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_sessions_1"}}},
		{ID: iaRetireUnitSessionRemaining, Kind: stmt.Read, SQL: `SELECT 1 FROM playback_sessions INDEXED BY playback_sessions_asset_id_fk WHERE asset_id=? LIMIT 1`, Params: []stmt.Param{t("asset")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"playback_sessions_asset_id_fk"}}},
		{ID: iaRetireUnitOwnerCAS, Kind: stmt.Write, SQL: `UPDATE catalog_removal_progress SET job_revision=?,walk_removed=? WHERE kind=? AND target=? AND job_revision=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND phase=? AND entity_kind=? AND entity_cursor=? AND object_cursor=? AND walk_removed=? AND walk_kept=?`, Params: []stmt.Param{i("next"), i("next_removed"), t("kind"), t("target"), i("revision"), i("owner"), i("generation"), i("expires"), i("phase"), i("entity_kind"), i("entity_cursor"), t("object_cursor"), i("removed"), i("kept")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_removal_progress"}}},
		{ID: iaRetireUnitSubject, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_subjects WHERE id=? AND library_id=?`, Params: []stmt.Param{i("subject"), i("library")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_subjects"}}},
		{ID: iaRetireUnitPersonalHold, Kind: stmt.Read, SQL: `SELECT 1 FROM personal_items INDEXED BY personal_current_live_item WHERE item_id=? LIMIT 1`, Params: []stmt.Param{i("subject")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live_item"}}},
	}
	for n := range defs {
		for _, name := range defs[n].Plan.Indexes {
			table := ""
			switch name {
			case "sqlite_autoindex_library_sources_1":
				table = "library_sources"
			case "sqlite_autoindex_playback_sessions_1":
				table = "playback_sessions"
			}
			if table != "" {
				defs[n].Plan.AutoIndexes = append(defs[n].Plan.AutoIndexes, stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}})
			}
		}
		defs[n].Owner = "store/writer IA F retirement participant"
		defs[n].CallSites = []string{"store/writer/ia_retirement_step.go"}
		var bindings []any
		for _, p := range defs[n].Params {
			if p.Type == stmt.Text {
				bindings = append(bindings, "absent")
			} else {
				bindings = append(bindings, int64(1))
			}
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "absent", Bindings: bindings}}
	}
	defs = append(defs, mpGuidePinReclaimDefinitions()...)
	defs = append(defs, mpSessionParentDrainDefinitions()...)
	defs = append(defs, iaRetirementByteDefinition())
	return append(usAuthorityDefinitions(), defs...)
}
