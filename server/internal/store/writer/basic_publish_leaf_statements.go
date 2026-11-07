package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicPublishLeafProof stmt.ID = "scan.basic_accept.publish_leaf_proof"
	basicPublishScalarOld stmt.ID = "scan.basic_accept.publish_scalar_old"
	basicPublishLinkCAS   stmt.ID = "scan.basic_accept.publish_link_cas"
	basicPublishAssetCAS  stmt.ID = "scan.basic_accept.publish_asset_cas"
)

func basicPublishLeafDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	defs := []stmt.Definition{
		{ID: basicPublishLeafProof, Kind: stmt.Read, SQL: `SELECT j.id,a.info_epoch FROM catalog_info_changes AS j
INNER JOIN catalog_assets AS a ON a.id=j.asset_id AND a.token=j.asset_token
INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id AND l.entity_id=?
INNER JOIN catalog_entities AS e ON e.id=l.entity_id AND e.library_id=l.library_id
INNER JOIN catalog_subjects AS sub ON sub.id=e.id AND sub.public_id=e.public_id AND sub.library_id=e.library_id AND sub.kind=e.kind
INNER JOIN catalog_scalar_contributions AS c ON c.parent_id=e.id AND c.contributor_id=e.id AND c.source=0 AND c.library_id=e.library_id
WHERE j.id=? AND j.asset_id=? AND j.asset_token=? AND a.library_id=? AND j.expected_epoch=? AND j.input_revision=? AND j.fingerprint=? AND j.job_revision=? AND j.target_shape_id=?
AND j.slot=0 AND j.phase=4 AND j.family_mask=511 AND j.target_available=1 AND j.target_owner_object IS NULL AND j.target_owner_revision IS NULL AND j.old_path IS NULL
AND j.has_after=1 AND j.after_entity=e.id AND j.upper_entity=e.id AND j.copy_family=0 AND j.copy_phase=0 AND j.copy_id=0 AND j.copy_aux=0 AND j.copy_key='' AND j.copy_key2='' AND j.copy_key3='' AND j.fault='' AND j.job_lease_owner=0 AND j.job_lease_generation=0 AND j.job_lease_expires_ms=0
AND a.info_epoch=? AND a.size=? AND a.modified_ns=? AND a.file_size_revision=? AND a.info_invalidation_revision=? AND a.available=1 AND a.info_order_available=1 AND a.info_publication_pending=1 AND a.info_owner_object IS NULL AND a.info_owner_revision IS NULL
AND l.info_pending_change=j.id AND l.library_id=a.library_id AND l.available=1 AND l.info_shape_id=j.target_shape_id AND l.info_duration=? AND e.kind=1 AND e.retired=0
AND c.duration=? AND c.resolution=? AND c.contributor_revision=e.scalar_source_revision AND c.membership_sequence=? AND e.duration_key=c.duration AND e.resolution_key=c.resolution AND e.scalar_source_revision=? AND e.aggregate_revision=?`,
			Params:  []stmt.Param{i("entity_id"), i("change_id"), i("asset_id"), t("asset_token"), i("library_id"), i("expected_epoch"), i("input_revision"), {Name: "fingerprint", Type: stmt.Blob}, i("change_revision"), i("shape_id"), i("accepted_epoch"), i("size"), i("modified_ns"), i("file_size_revision"), i("invalidation_revision"), r("link_duration"), r("source_duration"), i("source_resolution"), i("membership_sequence"), i("scalar_revision"), i("aggregate_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"j", "a", "l", "sub", "c"}, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?", "parent_id=? AND contributor_id=? AND source=?", "library_id=? AND kind=? AND id=? AND rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "settled_first_leaf", Bindings: []any{int64(31), int64(51), int64(41), "basic-asset", int64(7), int64(17), int64(7), []byte("11111111111111111111111111111111"), int64(14), int64(61), int64(18), int64(42), int64(-11), int64(1), int64(1), float64(100), float64(100), int64(1080), int64(9), int64(18), int64(24)}}}},
		{ID: basicPublishScalarOld, Kind: stmt.Read, SQL: `SELECT field FROM catalog_info_scalar_old WHERE change_id=? ORDER BY field LIMIT 1`, Params: []stmt.Param{i("change_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_scalar_old"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "no_old_obligation", Bindings: []any{int64(51)}}}},
		{ID: basicPublishLinkCAS, Kind: stmt.Write, SQL: `UPDATE catalog_asset_links SET info_pending_change=? WHERE entity_id=? AND asset_id=? AND library_id=? AND info_shape_id=? AND info_duration=? AND info_pending_change=? RETURNING entity_id,asset_id,library_id,info_shape_id,info_duration,info_pending_change`, Params: []stmt.Param{{Name: "cleared_pending", Type: stmt.Integer, Nullable: true}, i("entity_id"), i("asset_id"), i("library_id"), i("shape_id"), r("duration"), i("change_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "clear_exact_holder_fence", Bindings: []any{nil, int64(31), int64(41), int64(7), int64(61), float64(100), int64(51)}}}},
		{ID: basicPublishAssetCAS, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET info_publication_pending=? WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND size=? AND modified_ns=? AND file_size_revision=? AND info_invalidation_revision=? AND info_publication_pending=? AND info_order_available=? AND available=1 AND info_owner_object IS NULL AND info_owner_revision IS NULL RETURNING id,library_id,info_epoch,info_publication_pending,file_size_revision,info_invalidation_revision`, Params: []stmt.Param{i("next_pending"), i("asset_id"), t("asset_token"), i("library_id"), i("accepted_epoch"), i("size"), i("modified_ns"), i("file_size_revision"), i("invalidation_revision"), i("old_pending"), i("available")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "clear_exact_asset_fence", Bindings: []any{int64(0), int64(41), "basic-asset", int64(7), int64(18), int64(42), int64(-11), int64(1), int64(1), int64(1), int64(1)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_publish_leaf.go"}
	}
	return defs
}
