package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The complete authored collection family enumerates every current
// (order_key,item_id) position before it reports an exact viewer count. The
// source and point seeks use the unique (library_id,id) entity position; member
// positions use catalog_collection_members_page exactly as the bounded authored
// window does. No statement executes a MAIN view or an unindexed scan.
const rsCollectionCompleteSourceSQL = `SELECT e.id,e.retired,e.revision,e.collection_revision,e.metadata_incarnation,e.source_revision,e.metadata_revision,e.aggregate_revision,e.admission_revision,e.aggregate_publication_id,e.admission_member_count,e.admission_unrated_members,e.admission_min_known_age,e.admission_allow_age,e.admission_known_age FROM catalog_entities AS e INDEXED BY catalog_entities_library_id WHERE e.library_id=? AND e.id=? AND e.kind=10 AND e.retired=0`
const rsCollectionCompleteMemberSQL = `SELECT m.item_id,m.order_key FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`
const rsCollectionCompleteMemberNextSQL = `SELECT m.item_id,m.order_key FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`
const rsCollectionCompleteMemberOctetsSQL = `SELECT octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`
const rsCollectionCompleteMemberNextOctetsSQL = `SELECT octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`
const rsCollectionCompleteMemberFactsSQL = `SELECT e.id,e.retired,e.admission_own_age,e.kind,k.container FROM catalog_entities AS e INDEXED BY catalog_entities_library_id CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.library_id=? AND e.id=?`
const rsCollectionCompletePointSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,k.container FROM catalog_entities AS e INDEXED BY catalog_entities_library_id CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.library_id=? AND e.id=?`
const rsCollectionCompletePointOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title) FROM catalog_entities AS e INDEXED BY catalog_entities_library_id WHERE e.library_id=? AND e.id=?`

func init() {
	registerRSBytePoint("rs.collection_complete.point", rsBytePoint{lengths: "rs.collection_complete.point_octets", columns: []int{1, 2, 3}})
}

// CollectionCompleteDefinitions is inert until an actual Reader Capture
// authorizes these exact definitions. The merged subject eligibility points are
// dependencies of the complete authored reduction, not new authority.
func CollectionCompleteDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, plan stmt.Plan) {
		empty := append([]any(nil), bindings...)
		if len(empty) > 0 {
			if params[0].Type == stmt.Text {
				empty[0] = "absent"
			} else {
				empty[0] = int64(999999)
			}
		}
		out = append(out, stmt.Definition{ID: readStatementID("rs.collection_complete." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_collection_complete.go:CollectionPreparation"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: plan})
	}
	add("source", rsCollectionCompleteSourceSQL, []stmt.Param{i("library"), i("collection")}, []any{int64(1), int64(1101)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id=?"}})
	for _, v := range []struct{ n, sql string }{{"member", rsCollectionCompleteMemberSQL}, {"member_octets", rsCollectionCompleteMemberOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("collection"), s("key"), i("item"), i("limit")}, []any{int64(1101), "a", int64(0), int64(4)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, Seeks: []string{"collection_id=? AND order_key=? AND item_id>?"}})
	}
	for _, v := range []struct{ n, sql string }{{"member_next", rsCollectionCompleteMemberNextSQL}, {"member_next_octets", rsCollectionCompleteMemberNextOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("collection"), s("key"), i("limit")}, []any{int64(1101), "", int64(4)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, Seeks: []string{"collection_id=? AND order_key>?"}})
	}
	add("member_facts", rsCollectionCompleteMemberFactsSQL, []stmt.Param{i("library"), i("item")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id=?"}, PrimaryKeys: []string{"k"}})
	for _, v := range []struct{ n, sql string }{{"point", rsCollectionCompletePointSQL}, {"point_octets", rsCollectionCompletePointOctetsSQL}} {
		plan := stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library_id"}, Seeks: []string{"library_id=? AND id=?"}}
		if v.n == "point" {
			plan.PrimaryKeys = []string{"k"}
		}
		add(v.n, v.sql, []stmt.Param{i("library"), i("item")}, []any{int64(1), int64(1)}, plan)
	}
	return append(out, rsDetailEligibilityDefinitions()...)
}
