package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicActivationHeader    stmt.ID = "scan.basic_accept.activation_header"
	basicActivationFact      stmt.ID = "scan.basic_accept.activation_fact"
	basicActivationAssetCAS  stmt.ID = "scan.basic_accept.activation_asset_cas"
	basicActivationChangeCAS stmt.ID = "scan.basic_accept.activation_change_cas"
)

func basicActivationDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: basicActivationHeader, Kind: stmt.Read, SQL: `SELECT job_revision,family_mask,target_shape_id,target_available,target_owner_object,target_owner_revision,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3,job_lease_owner,job_lease_generation,job_lease_expires_ms FROM catalog_info_changes WHERE id=?`, Params: []stmt.Param{i("change_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_copy_poststate", Bindings: []any{int64(51)}}}},
		{ID: basicActivationFact, Kind: stmt.Read, SQL: `SELECT revision,size,modified_ns,fingerprint,detail_version FROM asset_stream_facts WHERE asset_id=?`, Params: []stmt.Param{t("asset_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "installed_exact_probe", Bindings: []any{"basic-asset"}}}},
		{ID: basicActivationAssetCAS, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET info_epoch=?,info_order_available=?,info_owner_object=?,info_owner_revision=? WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND size=? AND modified_ns=? AND info_publication_pending=? AND file_size_revision=? AND info_invalidation_revision=? AND info_owner_object IS NULL AND info_owner_revision IS NULL RETURNING id,info_epoch,info_order_available,info_owner_object,info_owner_revision,info_publication_pending,file_size_revision,info_invalidation_revision`, Params: []stmt.Param{i("next_info_epoch"), i("target_available"), nt("owner_object"), nt("owner_revision"), i("asset_id"), t("asset_token"), i("library_id"), i("expected_info_epoch"), i("size"), i("modified_ns"), i("pending"), i("file_size_revision"), i("invalidation_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "actual_post_info_epoch", Bindings: []any{int64(18), int64(1), nil, nil, int64(41), "basic-asset", int64(7), int64(17), int64(42), int64(-11), int64(1), int64(1), int64(1)}}}},
		{ID: basicActivationChangeCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_changes SET phase=?,job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND phase=? AND expected_epoch=? AND input_revision=? AND family_mask=? AND fingerprint=? AND job_revision=? AND target_shape_id=? AND target_available=? AND target_owner_object IS NULL AND target_owner_revision IS NULL AND copy_family=? AND copy_phase=? AND copy_id=? AND copy_aux=? AND copy_key=? AND copy_key2=? AND copy_key3=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND fault=? RETURNING job_revision,phase`, Params: []stmt.Param{i("next_phase"), i("next_job_revision"), i("change_id"), i("asset_id"), t("asset_token"), i("slot"), i("phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), b("fingerprint"), i("job_revision"), i("shape_id"), i("target_available"), i("copy_family"), i("copy_phase"), i("copy_id"), i("copy_aux"), t("copy_key"), t("copy_key2"), t("copy_key3"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), t("fault")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "phase_four_after_asset_cas", Bindings: []any{int64(4), int64(14), int64(51), int64(41), "basic-asset", int64(0), int64(3), int64(17), int64(7), int64(7), make([]byte, 32), int64(13), int64(61), int64(1), int64(0), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), ""}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_activation.go"}
	}
	return defs
}
