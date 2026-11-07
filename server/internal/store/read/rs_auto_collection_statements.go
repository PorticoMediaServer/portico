package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Discovery never executes catalog_automatic_members_current. The canonical
// predicate is expanded over base tables at each enumerated item point. The
// schema views remain independent oracles, with no execution permission.
const rsAutoIdentitySQL = `SELECT a.provider,a.provider_id FROM catalog_automatic_collections AS a INDEXED BY catalog_automatic_collection_entity WHERE a.entity_id=? AND a.library_id=? AND a.removed=0 LIMIT 1`
const rsAutoIdentityOctetsSQL = `SELECT octet_length(a.provider),octet_length(a.provider_id) FROM catalog_automatic_collections AS a INDEXED BY catalog_automatic_collection_entity WHERE a.entity_id=? AND a.library_id=? AND a.removed=0 LIMIT 1`
const rsAutoEvidenceSQL = `SELECT evidence_id,name FROM metadata_evidence_collections INDEXED BY metadata_evidence_collection_members WHERE provider=? AND collection_kind='collection' AND collection_id=? AND evidence_id>? ORDER BY evidence_id LIMIT 1`
const rsAutoEvidenceOctetsSQL = `SELECT octet_length(evidence_id),octet_length(name) FROM metadata_evidence_collections INDEXED BY metadata_evidence_collection_members WHERE provider=? AND collection_kind='collection' AND collection_id=? AND evidence_id>? ORDER BY evidence_id LIMIT 1`
const rsAuthoredTailSQL = `SELECT item_id,order_key FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key=? AND item_id>? ORDER BY order_key,item_id LIMIT 1`
const rsAuthoredTailOctetsSQL = `SELECT octet_length(order_key) FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key=? AND item_id>? ORDER BY order_key,item_id LIMIT 1`
const rsAuthoredNextSQL = `SELECT item_id,order_key FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key>? ORDER BY order_key,item_id LIMIT 1`
const rsAuthoredNextOctetsSQL = `SELECT octet_length(order_key) FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key>? ORDER BY order_key,item_id LIMIT 1`
const rsCollectionCardSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,k.container FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.id=? AND e.library_id=? AND e.retired=0`
const rsCollectionCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title) FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.id=? AND e.library_id=? AND e.retired=0`

func init() {
	registerRSBytePoint("rs.auto.identity", rsBytePoint{lengths: "rs.auto.identity_octets", columns: []int{0, 1}})
	registerRSBytePoint("rs.auto.evidence", rsBytePoint{lengths: "rs.auto.evidence_octets", columns: []int{0, 1}})
	registerRSBytePoint("rs.auto.authored_tail", rsBytePoint{lengths: "rs.auto.authored_tail_octets", columns: []int{1}})
	registerRSBytePoint("rs.auto.authored_next", rsBytePoint{lengths: "rs.auto.authored_next_octets", columns: []int{1}})
	registerRSBytePoint("rs.auto.card", rsBytePoint{lengths: "rs.auto.card_octets", columns: []int{1, 2, 3}})
}

// AutomaticCollectionDefinitions is inert until actual Reader Capture authorizes
// the complete definitions. Every enumerator has a raw-position bound, including
// rejected origins/overrides; all joins of the source predicate are item points.
func AutomaticCollectionDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition
	add := func(n, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		empty := append([]any(nil), args...)
		if params[0].Type == stmt.Text {
			empty[0] = "absent"
		} else {
			empty[0] = int64(999999)
		}
		if n == "accepted" || n == "accepted_local" {
			empty[0] = args[0]
			empty[2] = int64(999999)
		}
		defs = append(defs, stmt.Definition{ID: readStatementID("rs.auto." + n), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_auto_collection.go:CollectionPreparation"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	// LIMIT2 detects an ambiguous automatic identity rather than selecting one.
	add("source", `SELECT e.id,e.revision,e.collection_revision,e.metadata_incarnation,e.source_revision,e.metadata_revision,e.aggregate_revision,e.admission_revision,a.removed,a.owner_edited,policy.enabled,policy.incarnation,policy.revision,provider.enabled FROM catalog_entities AS e NOT INDEXED LEFT JOIN catalog_automatic_collections AS a INDEXED BY catalog_automatic_collection_entity ON a.entity_id=e.id LEFT JOIN metadata_provider_policy AS policy INDEXED BY sqlite_autoindex_metadata_provider_policy_1 ON policy.library_id=a.library_id AND policy.provider=a.provider LEFT JOIN metadata_providers AS provider INDEXED BY sqlite_autoindex_metadata_providers_1 ON provider.code=a.provider WHERE e.id=? AND e.library_id=? AND e.kind=10 AND e.retired=0 LIMIT 2`, []stmt.Param{i("collection"), i("library")}, []any{int64(1101), int64(1)}, rsCollectionSourcePlan())
	for _, v := range []struct{ n, sql string }{{"identity", rsAutoIdentitySQL}, {"identity_octets", rsAutoIdentityOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("collection"), i("library")}, []any{int64(1101), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_automatic_collection_entity"}, Seeks: []string{"entity_id=?"}})
	}
	for _, v := range []struct{ n, sql string }{{"evidence", rsAutoEvidenceSQL}, {"evidence_octets", rsAutoEvidenceOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{s("provider"), s("external"), s("after")}, []any{"tmdb", "C", ""}, stmt.Plan{Hot: true, Indexes: []string{"metadata_evidence_collection_members"}, Seeks: []string{"provider=? AND collection_kind=? AND collection_id=? AND evidence_id>?"}})
	}
	add("origins", `SELECT entity_id FROM metadata_field_origins INDEXED BY metadata_field_origin_evidence WHERE evidence_id=? AND entity_id>? AND field='relations' ORDER BY entity_id LIMIT ?`, []stmt.Param{s("evidence"), i("after"), i("limit")}, []any{"e1", int64(0), int64(4)}, stmt.Plan{Hot: true, Indexes: []string{"metadata_field_origin_evidence"}, Seeks: []string{"evidence_id=? AND entity_id>?"}})
	add("local", `SELECT id FROM catalog_entities INDEXED BY catalog_entities_automatic_local WHERE library_id=? AND automatic_collection_name_key=? AND id>? AND kind=1 AND retired=0 ORDER BY id LIMIT ?`, []stmt.Param{i("library"), s("external"), i("after"), i("limit")}, []any{int64(1), "local", int64(0), int64(4)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_automatic_local"}, Seeks: []string{"library_id=? AND automatic_collection_name_key=? AND id>?"}})
	add("overrides", `SELECT item_id,present FROM catalog_automatic_member_overrides INDEXED BY sqlite_autoindex_catalog_automatic_member_overrides_1 WHERE collection_id=? AND item_id>? ORDER BY item_id LIMIT ?`, []stmt.Param{i("collection"), i("after"), i("limit")}, []any{int64(1101), int64(0), int64(4)}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_automatic_member_overrides_1"}, Seeks: []string{"collection_id=? AND item_id>?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_automatic_member_overrides", Name: "sqlite_autoindex_catalog_automatic_member_overrides_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "collection_id", Collation: "BINARY"}, {Name: "item_id", Collation: "BINARY"}}}}})

	for _, v := range []struct{ n, sql string }{{"authored_tail", rsAuthoredTailSQL}, {"authored_tail_octets", rsAuthoredTailOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("collection"), s("key"), i("item")}, []any{int64(1101), "a", int64(0)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, Seeks: []string{"collection_id=? AND order_key=? AND item_id>?"}})
	}
	for _, v := range []struct{ n, sql string }{{"authored_next", rsAuthoredNextSQL}, {"authored_next_octets", rsAuthoredNextOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("collection"), s("key")}, []any{int64(1101), ""}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, Seeks: []string{"collection_id=? AND order_key>?"}})
	}

	// Expand the canonical source predicate over base tables because HN grants
	// no execution authority to views. Each relation below is a point seek.
	add("accepted", `SELECT e.id FROM catalog_entities AS e
 CROSS JOIN metadata_field_origins AS o INDEXED BY sqlite_autoindex_metadata_field_origins_1 ON o.entity_id=e.id
 CROSS JOIN metadata_evidence_collections AS c INDEXED BY sqlite_autoindex_metadata_evidence_collections_1 ON c.evidence_id=o.evidence_id AND c.provider=o.provider
 CROSS JOIN catalog_libraries AS cl ON cl.id=e.library_id AND cl.retired=0
 CROSS JOIN libraries AS lib INDEXED BY sqlite_autoindex_libraries_1 ON lib.id=cl.library_id
 CROSS JOIN metadata_subject_providers AS b INDEXED BY sqlite_autoindex_metadata_subject_providers_1 ON b.entity_id=e.id AND b.provider=c.provider AND b.accepted_evidence_id=c.evidence_id AND b.accepted_publication_id=o.publication_id
 CROSS JOIN metadata_publications AS p INDEXED BY sqlite_autoindex_metadata_publications_1 ON p.id=b.accepted_publication_id AND p.entity_id=e.id AND p.evidence_id=c.evidence_id
 CROSS JOIN metadata_provider_operations AS op INDEXED BY sqlite_autoindex_metadata_provider_operations_1 ON op.id=p.operation_id AND op.entity_id=e.id AND op.provider=c.provider
 CROSS JOIN metadata_provider_policy AS policy INDEXED BY sqlite_autoindex_metadata_provider_policy_1 ON policy.library_id=e.library_id AND policy.provider=c.provider AND policy.enabled=1 AND policy.incarnation=op.policy_incarnation AND policy.revision=op.policy_revision
 CROSS JOIN metadata_evidence AS ev INDEXED BY sqlite_autoindex_metadata_evidence_1 ON ev.id=c.evidence_id AND ev.sealed=1 AND ev.language=lib.metadata_language
 CROSS JOIN metadata_providers AS provider INDEXED BY sqlite_autoindex_metadata_providers_1 ON provider.code=c.provider AND provider.enabled=1
 LEFT JOIN catalog_automatic_member_overrides AS x INDEXED BY sqlite_autoindex_catalog_automatic_member_overrides_1 ON x.collection_id=? AND x.item_id=e.id
 WHERE e.library_id=? AND e.id=? AND c.provider=? AND c.collection_id=? AND e.kind=1 AND e.retired=0 AND e.automatic_collection_name_key='' AND c.collection_id<>'' AND c.evidence_id=? AND e.metadata_language=lib.metadata_language AND o.evidence_id=b.accepted_evidence_id AND o.publication_id=b.accepted_publication_id AND (x.item_id IS NULL OR x.present=0) AND o.field='relations' AND c.collection_kind='collection' AND b.role='primary' AND b.status='matched' AND p.decision='accepted' AND op.status='applied'`,
		[]stmt.Param{i("collection"), i("library"), i("item"), s("provider"), s("external"), s("evidence")}, []any{int64(1101), int64(1), int64(1), "tmdb", "C", "e1"}, rsAutomaticAcceptedPlan())
	add("accepted_local", `SELECT e.id FROM catalog_entities AS e LEFT JOIN catalog_automatic_member_overrides AS x INDEXED BY sqlite_autoindex_catalog_automatic_member_overrides_1 ON x.collection_id=? AND x.item_id=e.id WHERE e.library_id=? AND e.id=? AND 'nfo'=? AND e.automatic_collection_name_key=? AND e.automatic_collection_name_key<>'' AND e.kind=1 AND e.retired=0 AND x.item_id IS NULL`, []stmt.Param{i("collection"), i("library"), i("item"), s("provider"), s("external")}, []any{int64(1101), int64(1), int64(5), "nfo", "local"}, rsAutomaticLocalPlan())

	add("member", `SELECT e.id,e.retired,e.admission_own_age,e.kind,k.container FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.id=? AND e.library_id=?`, []stmt.Param{i("item"), i("library")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "k"}})
	for _, v := range []struct{ n, sql string }{{"card", rsCollectionCardSQL}, {"card_octets", rsCollectionCardOctetsSQL}} {
		add(v.n, v.sql, []stmt.Param{i("item"), i("library")}, []any{int64(1), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "k"}})
	}
	return defs
}

func rsAutomaticAcceptedPlan() stmt.Plan {
	p := rsAutomaticLocalPlan()
	p.Indexes = append(p.Indexes, "sqlite_autoindex_metadata_providers_1")
	p.AutoIndexes = append(p.AutoIndexes, rsCollectionProviderPin())
	p.PrimaryKeys = []string{"e", "cl"}
	for _, table := range []string{"libraries", "metadata_publications", "metadata_provider_operations", "metadata_evidence"} {
		name := "sqlite_autoindex_" + table + "_1"
		p.Indexes = append(p.Indexes, name)
		p.AutoIndexes = append(p.AutoIndexes, stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}})
	}
	p.Indexes = append(p.Indexes, "sqlite_autoindex_metadata_subject_providers_1")
	p.AutoIndexes = append(p.AutoIndexes, stmt.AutoIndexBinding{Table: "metadata_subject_providers", Name: "sqlite_autoindex_metadata_subject_providers_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "entity_id", Collation: "BINARY"}, {Name: "provider", Collation: "BINARY"}, {Name: "role", Collation: "BINARY"}}})
	for _, v := range []struct {
		table   string
		columns []string
	}{{"metadata_provider_policy", []string{"library_id", "provider"}}, {"metadata_field_origins", []string{"entity_id", "field"}}, {"metadata_evidence_collections", []string{"evidence_id", "collection_kind", "collection_id"}}} {
		name := "sqlite_autoindex_" + v.table + "_1"
		p.Indexes = append(p.Indexes, name)
		pin := stmt.AutoIndexBinding{Table: v.table, Name: name, Origin: "pk"}
		for _, column := range v.columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
		}
		p.AutoIndexes = append(p.AutoIndexes, pin)
	}
	return p
}

func rsAutomaticLocalPlan() stmt.Plan {
	return stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"sqlite_autoindex_catalog_automatic_member_overrides_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_automatic_member_overrides", Name: "sqlite_autoindex_catalog_automatic_member_overrides_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "collection_id", Collation: "BINARY"}, {Name: "item_id", Collation: "BINARY"}}}}}
}

func rsCollectionProviderPin() stmt.AutoIndexBinding {
	return stmt.AutoIndexBinding{Table: "metadata_providers", Name: "sqlite_autoindex_metadata_providers_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "code", Collation: "BINARY"}}}
}
func rsCollectionSourcePlan() stmt.Plan {
	return stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{"catalog_automatic_collection_entity", "sqlite_autoindex_metadata_provider_policy_1", "sqlite_autoindex_metadata_providers_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "metadata_provider_policy", Name: "sqlite_autoindex_metadata_provider_policy_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "library_id", Collation: "BINARY"}, {Name: "provider", Collation: "BINARY"}}}, rsCollectionProviderPin()}}
}
