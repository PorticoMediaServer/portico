package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const basicCopyPositionCAS stmt.ID = "scan.basic_stage.copy_position_cas"

func basicCopyStepDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	return []stmt.Definition{{ID: basicCopyPositionCAS, Kind: stmt.Write, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_copy_step.go"},
		SQL:     `UPDATE catalog_info_changes SET job_revision=?,copy_family=?,copy_phase=?,copy_id=?,copy_aux=?,copy_key=?,copy_key2=?,copy_key3=? WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND phase=? AND expected_epoch=? AND input_revision=? AND family_mask=? AND fingerprint=? AND job_revision=? AND upper_entity=? AND has_after=? AND after_entity=? AND target_shape_id=? AND target_available=? AND copy_family=? AND copy_phase=? AND copy_id=? AND copy_aux=? AND copy_key=? AND copy_key2=? AND copy_key3=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND fault=? AND target_owner_object IS NULL AND target_owner_revision IS NULL RETURNING id,job_revision,phase,after_entity,copy_family,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3`,
		Params:  []stmt.Param{i("next_job_revision"), i("next_copy_family"), i("next_copy_phase"), i("next_copy_id"), i("next_copy_aux"), t("next_copy_key"), t("next_copy_key2"), t("next_copy_key3"), i("change_id"), i("asset_id"), t("asset_token"), i("slot"), i("phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), b("fingerprint"), i("job_revision"), i("upper_entity"), i("has_after"), i("after_entity"), i("target_shape_id"), i("target_available"), i("copy_family"), i("copy_phase"), i("copy_id"), i("copy_aux"), t("copy_key"), t("copy_key2"), t("copy_key3"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms"), t("fault")},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "post_row_copy_prefix", Bindings: []any{int64(17), int64(2), int64(0), int64(1), int64(0), "", "", "", int64(51), int64(41), "basic-asset", int64(0), int64(2), int64(17), int64(7), int64(511), []byte("11111111111111111111111111111111"), int64(16), int64(31), int64(1), int64(31), int64(61), int64(1), int64(2), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), ""}}},
	}}
}
