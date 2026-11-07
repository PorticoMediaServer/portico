package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicShapeLookup    stmt.ID = "scan.basic_stage.shape_lookup"
	basicShapeInsert    stmt.ID = "scan.basic_stage.shape_insert"
	basicShapeRefCAS    stmt.ID = "scan.basic_stage.shape_ref_cas"
	basicShapeHeaderCAS stmt.ID = "scan.basic_stage.shape_header_cas"
)

func basicShapeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: basicShapeLookup, Kind: stmt.Read, SQL: `SELECT id,ref_count FROM catalog_info_shapes WHERE container=? AND video_codec=? AND height=?`, Params: []stmt.Param{t("container"), t("video_codec"), i("height")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_shapes_1"}, Seeks: []string{"container=? AND video_codec=? AND height=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_new_tuple", Bindings: []any{"mkv", "h264", int64(0)}}}},
		{ID: basicShapeInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_shapes(container,video_codec,height,ref_count) VALUES(?,?,?,?) RETURNING id,container,video_codec,height,ref_count`, Params: []stmt.Param{t("container"), t("video_codec"), i("height"), i("ref_count")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_dictionary_header_reference", Bindings: []any{"mkv", "h264", int64(0), int64(1)}}}},
		{ID: basicShapeRefCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_shapes SET ref_count=? WHERE id=? AND ref_count=? AND container=? AND video_codec=? AND height=? RETURNING id,ref_count`, Params: []stmt.Param{i("next_refs"), i("shape_id"), i("expected_refs"), t("container"), t("video_codec"), i("height")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_shapes"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "reuse_exact_dictionary_row", Bindings: []any{int64(3), int64(61), int64(2), "mkv", "h264", int64(0)}}}},
		{ID: basicShapeHeaderCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_changes SET target_shape_id=?,copy_family=?,job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND phase=? AND expected_epoch=? AND input_revision=? AND family_mask=? AND fingerprint=? AND job_revision=? AND copy_family=? AND copy_aux=? AND fault=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND target_shape_id IS NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL AND target_available IS NULL RETURNING target_shape_id,copy_family,job_revision`, Params: []stmt.Param{i("shape_id"), i("next_copy_family"), i("next_job_revision"), i("change_id"), i("asset_id"), t("asset_token"), i("slot"), i("phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), b("fingerprint"), i("job_revision"), i("copy_family"), i("copy_aux"), t("fault"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "pin_exact_shape_and_null_owner", Bindings: []any{int64(61), int64(1), int64(2), int64(51), int64(41), "basic-asset", int64(0), int64(0), int64(17), int64(7), int64(511), make([]byte, 32), int64(1), int64(0), int64(0), "", int64(0), int64(0), int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_shape.go"}
	}
	return defs
}
