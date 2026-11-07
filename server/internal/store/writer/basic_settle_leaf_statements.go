package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicSettleLinkCAS    stmt.ID = "scan.basic_accept.settle_leaf_link_cas"
	basicSettleProof      stmt.ID = "scan.basic_accept.settle_leaf_proof"
	basicSettleReverse    stmt.ID = "scan.basic_accept.settle_leaf_reverse"
	basicSettleForward    stmt.ID = "scan.basic_accept.settle_leaf_forward"
	basicSettleCollection stmt.ID = "scan.basic_accept.settle_leaf_collection"
	basicSettleBefore     stmt.ID = "scan.basic_accept.settle_leaf_before"
	basicSettleScalarCAS  stmt.ID = "scan.basic_accept.settle_leaf_scalarcas"
	basicSettleEntityCAS  stmt.ID = "scan.basic_accept.settle_leaf_entitycas"
)

func basicSettleLeafDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: basicSettleLinkCAS, Kind: stmt.Write, SQL: `UPDATE catalog_asset_links SET info_shape_id=?,info_duration=? WHERE entity_id=? AND asset_id=? AND library_id=? AND info_shape_id IS NULL AND info_duration=0 AND info_pending_change=? AND cue_ordinal IS NULL AND start_seconds=0 AND end_seconds IS NULL RETURNING entity_id,asset_id,library_id,info_shape_id,info_duration,info_pending_change`, Params: []stmt.Param{{Name: "shape", Type: stmt.Integer}, {Name: "duration", Type: stmt.Real}, {Name: "entity", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "pending", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_link", Bindings: []any{int64(61), float64(100), int64(31), int64(41), int64(7), int64(51)}}}},
		{ID: basicSettleProof, Kind: stmt.Read, SQL: `SELECT e.id FROM catalog_info_changes AS j INNER JOIN catalog_assets AS a ON a.id=j.asset_id INNER JOIN catalog_entities AS e ON e.id=? INNER JOIN catalog_subjects AS sub ON sub.id=e.id AND sub.library_id=e.library_id AND sub.kind=e.kind AND sub.public_id=e.public_id INNER JOIN catalog_asset_links AS l ON l.entity_id=e.id AND l.asset_id=a.id INNER JOIN catalog_info_shapes AS sh ON sh.id=j.target_shape_id INNER JOIN catalog_asset_contributions AS ac ON ac.parent_id=e.id AND ac.asset_id=a.id
WHERE j.id=? AND j.asset_id=? AND j.asset_token=? AND a.library_id=? AND j.expected_epoch=? AND j.input_revision=? AND j.fingerprint=? AND j.job_revision=? AND j.target_shape_id=? AND j.phase=4 AND j.slot=0 AND j.has_after=1 AND j.after_entity=e.id AND j.upper_entity=e.id AND j.family_mask=511 AND j.target_owner_object IS NULL AND j.target_owner_revision IS NULL AND j.fault='' AND j.copy_family=0 AND j.copy_phase=0 AND j.copy_id=0 AND j.copy_aux=0 AND j.copy_key='' AND j.copy_key2='' AND j.copy_key3='' AND j.job_lease_owner=0 AND j.job_lease_generation=0 AND j.job_lease_expires_ms=0
AND a.token=? AND a.info_epoch=? AND a.size=? AND a.modified_ns=? AND a.file_size_revision=? AND a.info_invalidation_revision=? AND a.duration=? AND a.height=? AND a.info_publication_pending=1 AND a.info_owner_object IS NULL AND a.info_owner_revision IS NULL AND a.info_order_available=j.target_available AND sh.height=a.height AND sh.container=a.container AND sh.video_codec=a.video_codec AND sh.video_codec<>'' AND sh.ref_count>0
AND e.id=? AND e.library_id=a.library_id AND e.kind=1 AND e.retired=0 AND e.parent_id IS NULL AND e.aggregate_publication_id IS NULL AND l.library_id=a.library_id AND l.info_pending_change=j.id AND l.info_shape_id IS NULL AND l.info_duration=0 AND l.cue_ordinal IS NULL AND l.start_seconds=0 AND l.end_seconds IS NULL
AND ac.library_id=a.library_id AND ac.membership_refs=1 AND ac.authored_refs=0 AND ac.file_size=a.size AND ac.file_size_revision=a.file_size_revision`, Params: []stmt.Param{{Name: "p1", Type: stmt.Integer}, {Name: "p2", Type: stmt.Integer}, {Name: "p3", Type: stmt.Integer}, {Name: "p4", Type: stmt.Text}, {Name: "p5", Type: stmt.Integer}, {Name: "p6", Type: stmt.Integer}, {Name: "p7", Type: stmt.Integer}, {Name: "p8", Type: stmt.Blob}, {Name: "p9", Type: stmt.Integer}, {Name: "p10", Type: stmt.Integer}, {Name: "p11", Type: stmt.Text}, {Name: "p12", Type: stmt.Integer}, {Name: "p13", Type: stmt.Integer}, {Name: "p14", Type: stmt.Integer}, {Name: "p15", Type: stmt.Integer}, {Name: "p16", Type: stmt.Integer}, {Name: "p17", Type: stmt.Real}, {Name: "p18", Type: stmt.Integer}, {Name: "p19", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_leaf", Bindings: []any{int64(31), int64(51), int64(41), "basic-asset", int64(7), int64(17), int64(7), make([]byte, 32), int64(14), int64(61), "basic-asset", int64(18), int64(42), int64(-11), int64(1), int64(1), float64(100), int64(1080), int64(31)}}}},
		{ID: basicSettleReverse, Kind: stmt.Read, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_scalar_contributor"}, Seeks: []string{"contributor_id=?"}}, SQL: `SELECT library_id,parent_id,contributor_id,source FROM catalog_scalar_contributions INDEXED BY catalog_scalar_contributor WHERE contributor_id=? ORDER BY parent_id,source LIMIT 2`, Params: []stmt.Param{{Name: "p1", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_leaf", Bindings: []any{int64(31)}}}},
		{ID: basicSettleBefore, Kind: stmt.Read, SQL: `SELECT c.duration,c.resolution,e.duration_key,e.resolution_key,e.scalar_source_revision,e.aggregate_revision,c.membership_sequence,c.contributor_revision FROM catalog_entities AS e INNER JOIN catalog_scalar_contributions AS c ON c.parent_id=e.id AND c.contributor_id=e.id AND c.source=0 WHERE e.id=? AND e.library_id=? AND c.library_id=e.library_id`, Params: []stmt.Param{{Name: "p1", Type: stmt.Integer}, {Name: "p2", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_leaf", Bindings: []any{int64(31), int64(7)}}}},
		{ID: basicSettleScalarCAS, Kind: stmt.Write, SQL: `UPDATE catalog_scalar_contributions SET duration=?,resolution=?,contributor_revision=? WHERE library_id=? AND parent_id=? AND contributor_id=? AND source=0 AND duration IS ? AND resolution IS ? AND contributor_revision=? AND membership_sequence=? RETURNING library_id,parent_id,contributor_id,source,duration,resolution,contributor_revision,membership_sequence`, Params: []stmt.Param{{Name: "p1", Type: stmt.Real, Nullable: true}, {Name: "p2", Type: stmt.Integer, Nullable: true}, {Name: "p3", Type: stmt.Integer}, {Name: "p4", Type: stmt.Integer}, {Name: "p5", Type: stmt.Integer}, {Name: "p6", Type: stmt.Integer}, {Name: "p7", Type: stmt.Real, Nullable: true}, {Name: "p8", Type: stmt.Integer, Nullable: true}, {Name: "p9", Type: stmt.Integer}, {Name: "p10", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_leaf", Bindings: []any{float64(100), int64(1080), int64(2), int64(7), int64(31), int64(31), nil, nil, int64(1), int64(9)}}}},
		{ID: basicSettleEntityCAS, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET duration_key=?,resolution_key=?,scalar_source_revision=?,aggregate_revision=? WHERE id=? AND library_id=? AND duration_key=? AND resolution_key=? AND scalar_source_revision=? AND aggregate_revision=? RETURNING id,library_id,duration_key,resolution_key,scalar_source_revision,aggregate_revision`, Params: []stmt.Param{{Name: "p1", Type: stmt.Real}, {Name: "p2", Type: stmt.Integer}, {Name: "p3", Type: stmt.Integer}, {Name: "p4", Type: stmt.Integer}, {Name: "p5", Type: stmt.Integer}, {Name: "p6", Type: stmt.Integer}, {Name: "p7", Type: stmt.Real}, {Name: "p8", Type: stmt.Integer}, {Name: "p9", Type: stmt.Integer}, {Name: "p10", Type: stmt.Integer}}, Owner: "store/writer Basic domain", CallSites: []string{"store/writer/basic_settle_leaf.go"}, Cohorts: []stmt.Cohort{{Name: "first_leaf", Bindings: []any{float64(100), int64(1080), int64(2), int64(2), int64(31), int64(7), float64(0), int64(0), int64(1), int64(1)}}}},
		{ID: basicSettleForward, Kind: stmt.Read, SQL: `SELECT contributor_id,source FROM catalog_scalar_contributions WHERE parent_id=? ORDER BY contributor_id,source LIMIT 2`, Params: []stmt.Param{{Name: "parent_id", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_scalar_contributions"}, Seeks: []string{"parent_id=?"}}, Cohorts: []stmt.Cohort{{Name: "sole_self_contributor", Bindings: []any{int64(31)}}}},
		{ID: basicSettleCollection, Kind: stmt.Read, SQL: `SELECT collection_id FROM catalog_collection_members INDEXED BY catalog_collection_members_item WHERE item_id=? ORDER BY collection_id LIMIT 1`, Params: []stmt.Param{{Name: "item_id", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_item"}, Seeks: []string{"item_id=?"}}, Cohorts: []stmt.Cohort{{Name: "no_external_collection", Bindings: []any{int64(31)}}}},
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "store/writer Basic domain"
		d.CallSites = []string{"store/writer/basic_settle_leaf.go"}
		switch d.ID {
		case basicSettleProof:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"j", "a", "e", "sub", "l", "sh", "ac"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?", "parent_id=? AND asset_id=?"}}
		case basicSettleBefore:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "c"}, Seeks: []string{"rowid=?", "parent_id=? AND contributor_id=? AND source=?"}}
		case basicSettleLinkCAS:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}
		case basicSettleScalarCAS:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_scalar_contributions"}, Seeks: []string{"parent_id=? AND contributor_id=? AND source=?"}}
		case basicSettleEntityCAS:
			d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
		}
	}
	// Reuse the two existing literal definitions; there is still ONE finite
	// registry and no parallel lookup SQL or executor.
	for _, d := range basicStreamAcceptDefinitions() {
		if d.ID == basicAcceptOwners || d.ID == basicAcceptSuccessor {
			defs = append(defs, d)
		}
	}
	return defs
}
