package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgAuthorityViewerFamily    stmt.ID = "mg.authority.viewer-family"
	mgAuthorityViewerPrincipal stmt.ID = "mg.authority.viewer-principal"
	mgAuthorityViewerPIN       stmt.ID = "mg.authority.viewer-pin"
	mgAuthoritySource          stmt.ID = "mg.authority.source"
	mgAuthorityHead            stmt.ID = "mg.authority.head"
	mgAuthorityPolicy          stmt.ID = "mg.authority.policy"
)

func mgAuthorityDefinitions() []stmt.Definition {
	// Exactly US's bounded native proof, with a separate member-family query.
	// An elevated family cannot claim the viewer branch or bypass primary/PIN.
	defs := []stmt.Definition{}
	for _, d := range usAuthorityDefinitions() {
		if d.ID != usAuthorityFence {
			defs = append(defs, d)
		}
	}
	for _, d := range usAuthorityDefinitions() {
		switch d.ID {
		case usAuthorityFamily:
			d.ID = mgAuthorityViewerFamily
			d.SQL = strings.Replace(d.SQL, "f.role='owner'", "f.role='member'", 1)
		case usAuthorityPrincipal, usAuthorityPrincipalPIN:
			if d.ID == usAuthorityPrincipal {
				d.ID = mgAuthorityViewerPrincipal
			} else {
				d.ID = mgAuthorityViewerPIN
			}
			d.SQL = strings.Replace(d.SQL, " AND m.role='owner'", "", 1)
			d.SQL = strings.Replace(d.SQL, " AND p.is_primary=1 AND a.profile_id=p.id", "", 1)
		default:
			continue
		}
		d.Owner = "writer.mg.authority"
		d.CallSites = []string{"writer/mg_authority.go"}
		defs = append(defs, d)
	}
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	pin := func(table, index string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	}
	base := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1", "sqlite_autoindex_live_source_identities_1"}, AutoIndexes: []stmt.AutoIndexBinding{pin("live_sources", "sqlite_autoindex_live_sources_1"), pin("live_source_identities", "sqlite_autoindex_live_source_identities_1")}}
	join := ` FROM live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 CROSS JOIN live_source_identities AS x INDEXED BY sqlite_autoindex_live_source_identities_1 ON x.id=s.id WHERE s.id=?`
	defs = append(defs,
		stmt.Definition{ID: mgAuthoritySource, SQL: `SELECT s.revision,x.has_live_source_removals` + join, Params: []stmt.Param{text("source")}, Plan: base, Cohorts: []stmt.Cohort{{Name: "source", Bindings: []any{"source"}}}},
		stmt.Definition{ID: mgAuthorityPolicy, SQL: `SELECT 1` + join + ` AND s.state='active' AND x.has_live_source_removals=0 AND x.settings_viewer_access=?`, Params: []stmt.Param{text("source"), text("access")}, Plan: base, Cohorts: []stmt.Cohort{{Name: "source-policy", Bindings: []any{"source", "server-members"}}}},
		stmt.Definition{ID: mgAuthorityHead, SQL: `SELECT 1 FROM live_sources INDEXED BY sqlite_autoindex_live_sources_1 WHERE id=? AND revision=? AND active_generation=?`, Params: []stmt.Param{text("source"), {Name: "revision", Type: stmt.Integer}, text("head")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}, AutoIndexes: []stmt.AutoIndexBinding{pin("live_sources", "sqlite_autoindex_live_sources_1")}}, Cohorts: []stmt.Cohort{{Name: "source-head", Bindings: []any{"source", int64(7), "old"}}}},
	)
	for i := range defs {
		if defs[i].ID == mgAuthoritySource || defs[i].ID == mgAuthorityPolicy || defs[i].ID == mgAuthorityHead {
			defs[i].Owner = "writer.mg.authority"
			defs[i].CallSites = []string{"writer/mg_source_dispatch.go"}
			defs[i].Kind = stmt.Read
		}
	}
	defs = append(defs, mgAuthorityFenceDefinitions()...)
	return defs
}
