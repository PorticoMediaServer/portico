package writer

import (
	"bytes"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	basicFreezeLinks  stmt.ID = "scan.basic_stage.freeze_links"
	basicFreezeSealed stmt.ID = "scan.basic_stage.freeze_sealed"
	basicFreezeCAS    stmt.ID = "scan.basic_stage.freeze_cas"
	basicEntityCAS    stmt.ID = "scan.basic_stage.entity_cas"
)

// FIRST freeze (0->1) and entity selection (1->2). Each CAS restates the whole
// captured header so a concurrent or stale change refuses with no effect.
func basicFreezeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	fp := bytes.Repeat([]byte{0x31}, 32)
	header := ` WHERE id=? AND asset_id=? AND asset_token=? AND slot=0 AND expected_epoch=? AND input_revision=? AND family_mask=511 AND fingerprint=? AND job_revision=? AND copy_family=1 AND copy_phase=0 AND copy_id=0 AND copy_aux=0 AND copy_key='' AND copy_key2='' AND copy_key3='' AND target_shape_id=? AND target_owner_object IS NULL AND target_owner_revision IS NULL AND fault='' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0`
	headerParams := []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("expected_epoch"), i("input_revision"), b("fingerprint"), i("job_revision"), i("shape_id")}
	defs := []stmt.Definition{
		{ID: basicFreezeLinks, Kind: stmt.Read, SQL: `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? ORDER BY entity_id LIMIT 2`,
			Params: []stmt.Param{i("asset_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "sole_first_leaf_link", Bindings: []any{int64(41)}}}},
		{ID: basicFreezeSealed, Kind: stmt.Read, SQL: `SELECT family,rows_expected,rows_staged,sealed FROM catalog_info_input_families WHERE change_id=? ORDER BY family LIMIT 10`,
			Params: []stmt.Param{i("change_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_families"}, Seeks: []string{"change_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "nine_sealed_families", Bindings: []any{int64(51)}}}},
		{ID: basicFreezeCAS, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_changes SET phase=1,has_after=0,target_available=1,upper_entity=?,job_revision=?` + header + ` AND phase=0 AND has_after=0 AND target_available IS NULL RETURNING phase,has_after,target_available,upper_entity,job_revision`,
			Params:  append([]stmt.Param{i("upper_entity"), i("next_job_revision")}, headerParams...),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "freeze_pinned_first_input", Bindings: []any{int64(31), int64(3), int64(51), int64(41), "basic-asset", int64(17), int64(1), fp, int64(2), int64(61)}}}},
		{ID: basicEntityCAS, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_changes SET phase=2,has_after=1,after_entity=?,copy_family=0,job_revision=?` + header + ` AND phase=1 AND has_after=0 AND after_entity=0 AND upper_entity=? AND target_available=1 RETURNING phase,has_after,after_entity,copy_family,job_revision`,
			Params:  append(append([]stmt.Param{i("after_entity"), i("next_job_revision")}, headerParams...), i("upper_entity")),
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "select_sole_leaf_entity", Bindings: []any{int64(31), int64(4), int64(51), int64(41), "basic-asset", int64(17), int64(1), fp, int64(3), int64(61), int64(31)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_freeze.go"}
	}
	return defs
}
