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
	basicFreezeAsset  stmt.ID = "scan.basic_stage.freeze_asset_pending"
	basicFreezeLink   stmt.ID = "scan.basic_stage.freeze_link_settleable"
	basicFreezeEntity stmt.ID = "scan.basic_stage.freeze_entity_settleable"
	basicFreezeMember stmt.ID = "scan.basic_stage.freeze_no_collection"
	basicEntityLink   stmt.ID = "scan.basic_stage.entity_link_pending"
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
		// The same link and entity shape settle requires, proven before any fence.
		{ID: basicFreezeLink, Kind: stmt.Read,
			SQL:     `SELECT entity_id FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=? AND available=1 AND cue_ordinal IS NULL AND start_seconds=0 AND end_seconds IS NULL`,
			Params:  []stmt.Param{i("entity_id"), i("asset_id"), i("library_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "whole_file_link", Bindings: []any{int64(31), int64(41), int64(7)}}}},
		{ID: basicFreezeEntity, Kind: stmt.Read,
			SQL:     `SELECT id FROM catalog_entities WHERE id=? AND library_id=? AND kind=1 AND retired=0 AND parent_id IS NULL AND aggregate_publication_id IS NULL`,
			Params:  []stmt.Param{i("entity_id"), i("library_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "ordinary_standalone_leaf", Bindings: []any{int64(31), int64(7)}}}},
		{ID: basicFreezeMember, Kind: stmt.Read,
			SQL:     `SELECT collection_id FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=? ORDER BY collection_id LIMIT 1`,
			Params:  []stmt.Param{i("item_id")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, Seeks: []string{"item_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "no_external_collection", Bindings: []any{int64(31)}}}},
		// The publication obligation: set at freeze, held through copy and
		// activation, and cleared only by the completed whole publication.
		{ID: basicFreezeAsset, Kind: stmt.Write,
			SQL:     `UPDATE catalog_assets SET info_publication_pending=1 WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND available=1 AND info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL RETURNING id,info_publication_pending`,
			Params:  []stmt.Param{i("asset_id"), t("asset_token"), i("library_id"), i("expected_epoch")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "publication_obligation_opens", Bindings: []any{int64(41), "basic-asset", int64(7), int64(17)}}}},
		// The sole leaf's pre-fence: settle and publish require this exact change.
		{ID: basicEntityLink, Kind: stmt.Write,
			SQL:     `UPDATE catalog_asset_links SET info_pending_change=? WHERE entity_id=? AND asset_id=? AND library_id=? AND info_pending_change IS NULL AND info_shape_id IS NULL AND info_duration=0 RETURNING entity_id,asset_id,info_pending_change`,
			Params:  []stmt.Param{i("change_id"), i("entity_id"), i("asset_id"), i("library_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "sole_leaf_pre_fence", Bindings: []any{int64(51), int64(31), int64(41), int64(7)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_freeze.go"}
	}
	return defs
}
