package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The source point is exactly the merged scan issuer's source fact shape, read
// side: the configured/resolved native root pair, current generation and
// incarnation, and the health/enabled state. library_sources.id is a TEXT primary
// key, so the point seeks the native pk autoindex and still filters the library.
const iaSourcePointSQL = `SELECT id,library_id,kind,name,configured_root,root,classification,incarnation,root_identity,health,generation,enabled,follow_symlinks,interval_seconds,identity_confirmed FROM library_sources WHERE id=? AND library_id=?`
const iaSourcePointOctetsSQL = `SELECT octet_length(id),octet_length(library_id),octet_length(kind),octet_length(name),octet_length(configured_root),octet_length(root),octet_length(classification),octet_length(incarnation),octet_length(root_identity),octet_length(health) FROM library_sources WHERE id=? AND library_id=?`

// The library root is the numeric catalogue identity's exact native root. It is
// resolved first so the source point can filter the authoritative library key
// instead of trusting a caller string. catalog_libraries.id is the rowid PK and
// library_id is its UNIQUE identity.
const iaSourceRootsSQL = `SELECT library_id,root FROM catalog_libraries WHERE id=?`
const iaSourceRootsOctetsSQL = `SELECT octet_length(library_id),octet_length(root) FROM catalog_libraries WHERE id=?`

// IASourceHarnessDefinitions is inert harness composition, not registration or
// authorization. Capture these exact definitions on the actual reader first.
func IASourceHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, cohorts []stmt.Cohort, p stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("foreground.ia.source." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read/ia_source", CallSites: []string{"store/read/ia_source.go:IASourcePoint"}, Cohorts: cohorts, Plan: p}
	}
	pointPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}
	rootsPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}
	pointCohorts := func() []stmt.Cohort {
		return []stmt.Cohort{
			{Name: "present", Bindings: []any{"source-1", "movies"}},
			{Name: "empty", Bindings: []any{"missing", "movies"}},
			{Name: "foreign", Bindings: []any{"source-1", "other"}},
		}
	}
	return []stmt.Definition{
		add("point_octets", iaSourcePointOctetsSQL, []stmt.Param{s("source"), s("library")}, pointCohorts(), pointPlan),
		add("point", iaSourcePointSQL, []stmt.Param{s("source"), s("library")}, pointCohorts(), pointPlan),
		add("roots_octets", iaSourceRootsOctetsSQL, []stmt.Param{i("library")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(9999999)}}}, rootsPlan),
		add("roots", iaSourceRootsSQL, []stmt.Param{i("library")}, []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(9999999)}}}, rootsPlan),
	}
}
