package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// MP reading source selection is a closed set of point reads. The publication,
// link and asset are proved by their own native keys; the canonical lowest
// eligible object is the lowest inventory_objects.id that still satisfies every
// currentness predicate, so the plain index order is the admission order.
//
// No statement uses a subquery, CTE, CASE or OR: the read grammar stays closed
// and every plan is one SEARCH per table.
const mpReadingItemSQL = `SELECT e.public_id,e.kind,e.retired FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=?`
const mpReadingLibrarySQL = `SELECT kind FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?`
const mpReadingPublicationSQL = `SELECT p.format,l.available,a.token,a.size,a.modified_ns,a.available,a.library_id FROM reading_publications AS p INDEXED BY sqlite_autoindex_reading_publications_2 CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.entity_id=p.item_id AND l.asset_id=p.asset_id CROSS JOIN catalog_assets AS a ON a.id=p.asset_id WHERE p.item_id=? AND p.asset_id=? AND a.library_id=? AND l.library_id=a.library_id`
const mpReadingRetiredSQL = `SELECT item_id FROM dvr_catalog_retirements WHERE item_id=?`
const mpReadingObjectBase = `SELECT o.id,o.asset_id,o.source_id,o.root_incarnation,o.relative_path,o.object_identity,o.revision,o.evidence_json,o.size,o.modified_ns,o.retired,o.state,o.unsupported_reason,s.id,s.library_id,s.kind,s.generation,s.incarnation,s.root_identity,s.enabled,s.health FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing')`
const mpReadingObjectSQL = mpReadingObjectBase + ` ORDER BY o.id LIMIT ?`
const mpReadingLocalObjectSQL = mpReadingObjectBase + ` AND s.kind='local' ORDER BY o.id LIMIT ?`
const mpReadingLocalSQL = `SELECT o.id FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.root_incarnation=s.incarnation AND s.kind='local' ORDER BY o.id LIMIT 1`

const mpReadingPublicationOctetsSQL = `SELECT octet_length(p.format),octet_length(a.token) FROM reading_publications AS p INDEXED BY sqlite_autoindex_reading_publications_2 CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.entity_id=p.item_id AND l.asset_id=p.asset_id CROSS JOIN catalog_assets AS a ON a.id=p.asset_id WHERE p.item_id=? AND p.asset_id=? AND a.library_id=? AND l.library_id=a.library_id`
const mpReadingObjectOctetsBase = `SELECT octet_length(o.id),octet_length(o.asset_id),octet_length(o.source_id),octet_length(o.root_incarnation),octet_length(o.relative_path),octet_length(o.object_identity),octet_length(o.revision),octet_length(o.evidence_json),octet_length(o.state),octet_length(o.unsupported_reason),octet_length(s.id),octet_length(s.library_id),octet_length(s.kind),octet_length(s.incarnation),octet_length(s.root_identity),octet_length(s.health) FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing')`
const mpReadingObjectOctetsSQL = mpReadingObjectOctetsBase + ` ORDER BY o.id LIMIT ?`
const mpReadingLocalObjectOctetsSQL = mpReadingObjectOctetsBase + ` AND s.kind='local' ORDER BY o.id LIMIT ?`

// Variable payload columns, in the exact order of the matching octet preflight.
var mpReadingPublicationVariable = []int{0, 2}

var mpReadingObjectVariable = []int{0, 1, 2, 3, 4, 5, 6, 7, 11, 12, 13, 14, 15, 17, 18, 20}

var (
	mpReadingPublicationIndex = stmt.AutoIndexBinding{Table: "reading_publications", Name: "sqlite_autoindex_reading_publications_2", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "item_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}
	mpReadingLinkIndex        = stmt.AutoIndexBinding{Table: "catalog_asset_links", Name: "sqlite_autoindex_catalog_asset_links_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "entity_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}
	mpReadingLibraryIndex     = stmt.AutoIndexBinding{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	mpReadingSourceIndex      = stmt.AutoIndexBinding{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
)

func init() {
	// The payload/length pairing is compiled here; plan's own domain registry
	// still owns the closed native octet shape and its EQP audit.
	registerRSBytePoint("mp.reading.publication", rsBytePoint{lengths: "mp.reading.publication_octets", columns: mpReadingPublicationVariable})
	registerRSBytePoint("mp.reading.object", rsBytePoint{lengths: "mp.reading.object_octets", columns: mpReadingObjectVariable})
	registerRSBytePoint("mp.reading.local_object", rsBytePoint{lengths: "mp.reading.local_object_octets", columns: mpReadingObjectVariable})
}

// MPReadingSourceDefinitions is the closed statement recipe for the source
// decoder. It carries no command identity, writer turn or execution authority.
func MPReadingSourceDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	call := []string{"store/read/mp_reading_source.go:MPReadingSourceReader.Select"}

	itemParams := []stmt.Param{i("item"), i("library"), i("kind")}
	libraryParams := []stmt.Param{text("library")}
	publicationParams := []stmt.Param{i("item"), i("asset"), i("library")}
	retiredParams := []stmt.Param{i("item")}
	objectParams := []stmt.Param{text("token"), text("library"), i("size"), i("modified"), i("limit")}
	localParams := []stmt.Param{text("token"), text("library")}

	publicationPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingPublicationIndex.Name, mpReadingLinkIndex.Name}, PrimaryKeys: []string{"a"}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingPublicationIndex, mpReadingLinkIndex}}
	objectPlan := stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", mpReadingSourceIndex.Name}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingSourceIndex}}

	itemCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1101), int64(1), int64(20)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(20)}}}
	libraryCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"movies"}}, {Name: "empty", Bindings: []any{"missing"}}}
	publicationCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1101), int64(201), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(201), int64(1)}}}
	retiredCohorts := []stmt.Cohort{{Name: "empty", Bindings: []any{int64(1101)}}}
	objectCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"book-asset", "movies", int64(1000), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{"missing", "movies", int64(1000), int64(1), int64(1)}}}
	archiveCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"comic-archive-asset", "other", int64(2002), int64(2), int64(1)}}, {Name: "empty", Bindings: []any{"missing", "other", int64(2002), int64(2), int64(1)}}}
	localCohorts := []stmt.Cohort{{Name: "present", Bindings: []any{"comic-archive-asset", "other"}}, {Name: "empty", Bindings: []any{"missing", "other"}}}

	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, cohorts []stmt.Cohort, plan stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("mp.reading." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read MP", CallSites: call, Params: params, Cohorts: cohorts, Plan: plan})
	}
	add("item", mpReadingItemSQL, itemParams, itemCohorts, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	add("library", mpReadingLibrarySQL, libraryParams, libraryCohorts, stmt.Plan{Hot: true, Indexes: []string{mpReadingLibraryIndex.Name}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingLibraryIndex}})
	add("publication", mpReadingPublicationSQL, publicationParams, publicationCohorts, publicationPlan)
	add("retired", mpReadingRetiredSQL, retiredParams, retiredCohorts, stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_catalog_retirements"}})
	add("object", mpReadingObjectSQL, objectParams, objectCohorts, objectPlan)
	add("local_object", mpReadingLocalObjectSQL, objectParams, archiveCohorts, objectPlan)
	add("local", mpReadingLocalSQL, localParams, localCohorts, objectPlan)
	add("publication_octets", mpReadingPublicationOctetsSQL, publicationParams, publicationCohorts, publicationPlan)
	add("object_octets", mpReadingObjectOctetsSQL, objectParams, objectCohorts, objectPlan)
	add("local_object_octets", mpReadingLocalObjectOctetsSQL, objectParams, archiveCohorts, objectPlan)
	return out
}

// MPReadingSourceHarnessDefinitions composes the shared read fences with MP's
// own closed recipe. It is a Capture input, never a runtime grant.
func MPReadingSourceHarnessDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), MPReadingSourceDefinitions()...)
}
