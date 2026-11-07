package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicPrefenceHeader    stmt.ID = "scan.basic_stage.prefence_header"
	basicPrefenceAssetCAS  stmt.ID = "scan.basic_stage.prefence_asset_cas"
	basicPrefenceLinkCAS   stmt.ID = "scan.basic_stage.prefence_link_cas"
	basicPrefenceChangeCAS stmt.ID = "scan.basic_stage.prefence_change_cas"
)

func basicPrefenceDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: basicPrefenceHeader, Kind: stmt.Read, SQL: `SELECT job_revision,family_mask,upper_entity,has_after,after_entity,target_shape_id,target_available,target_owner_object,target_owner_revision,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3,job_lease_owner,job_lease_generation,job_lease_expires_ms FROM catalog_info_changes WHERE id=?`, Params: []stmt.Param{i("change_id")}, Cohorts: []stmt.Cohort{{Name: "frozen_ordinary_holder_frontier", Bindings: []any{int64(51)}}}},
		{ID: basicPrefenceAssetCAS, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET info_publication_pending=? WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND size=? AND modified_ns=? AND file_size_revision=? AND info_invalidation_revision=? AND info_publication_pending=? AND info_owner_object IS NULL AND info_owner_revision IS NULL RETURNING id,info_publication_pending`, Params: []stmt.Param{i("next_pending"), i("asset_id"), t("asset_token"), i("library_id"), i("expected_epoch"), i("size"), i("modified_ns"), i("file_size_revision"), i("invalidation_revision"), i("expected_pending")}, Cohorts: []stmt.Cohort{{Name: "block_incomplete_canonical_source", Bindings: []any{int64(1), int64(41), "basic-asset", int64(7), int64(17), int64(42), int64(-11), int64(1), int64(1), int64(0)}}}},
		{ID: basicPrefenceLinkCAS, Kind: stmt.Write, SQL: `UPDATE catalog_asset_links SET info_pending_change=? WHERE entity_id=? AND asset_id=? AND library_id=? AND cue_ordinal IS NULL AND info_pending_change IS NULL RETURNING entity_id,asset_id,info_pending_change`, Params: []stmt.Param{i("change_id"), i("entity_id"), i("asset_id"), i("library_id")}, Cohorts: []stmt.Cohort{{Name: "fence_complete_single_holder", Bindings: []any{int64(51), int64(31), int64(41), int64(7)}}}},
		{ID: basicPrefenceChangeCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_changes SET phase=?,has_after=?,after_entity=?,copy_family=?,job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND phase=? AND expected_epoch=? AND input_revision=? AND family_mask=? AND fingerprint=? AND job_revision=? AND upper_entity=? AND has_after=? AND after_entity=? AND target_shape_id=? AND target_available=? AND copy_family=? AND copy_phase=? AND copy_id=? AND copy_aux=? AND copy_key=? AND copy_key2=? AND copy_key3=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND fault=? AND target_owner_object IS NULL AND target_owner_revision IS NULL RETURNING id,job_revision,phase,after_entity,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3`, Params: []stmt.Param{i("next_phase"), i("next_has_after"), i("next_after_entity"), i("next_copy_family"), i("next_job_revision"), i("change_id"), i("asset_id"), t("asset_token"), i("slot"), i("phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), b("fingerprint"), i("job_revision"), i("upper_entity"), i("has_after"), i("after_entity"), i("shape_id"), i("target_available"), i("copy_family"), i("copy_phase"), i("copy_id"), i("copy_aux"), t("copy_key"), t("copy_key2"), t("copy_key3"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), t("fault")}, Cohorts: []stmt.Cohort{{Name: "actual_copy_start_poststate", Bindings: []any{int64(2), int64(1), int64(31), int64(0), int64(15), int64(51), int64(41), "basic-asset", int64(0), int64(1), int64(17), int64(7), int64(511), make([]byte, 32), int64(14), int64(31), int64(0), int64(0), int64(61), int64(1), int64(1), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), ""}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_prefence.go"}
		table := "catalog_info_changes"
		seek := "rowid=?"
		if defs[n].ID == basicPrefenceAssetCAS {
			table = "catalog_assets"
		}
		if defs[n].ID == basicPrefenceLinkCAS {
			table = "catalog_asset_links"
			seek = "entity_id=? AND asset_id=?"
		}
		defs[n].Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{table}, Seeks: []string{seek}}
	}
	return defs
}
