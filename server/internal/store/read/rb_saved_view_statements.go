package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

// Inert definitions: composition must Capture its actual merged schema/Reader.
// Nothing registers these with a production dispatcher or legacy DB service.
func rbSavedViewAllDraftDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pk := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	resource := pk("saved_resources", "sqlite_autoindex_saved_resources_1", "id")
	shares := pk("saved_resource_shares", "sqlite_autoindex_saved_resource_shares_1", "resource_id", "authority", "account_id", "profile_id")
	plan := stmt.Plan{Hot: true, Indexes: []string{resource.Name, shares.Name}, AutoIndexes: []stmt.AutoIndexBinding{resource, shares}, Seeks: []string{"id>?", "resource_id=? AND authority=? AND account_id=? AND profile_id=?"}}
	add := func(name, sql string, p []stmt.Param, args []any, proof stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("rb.saved.view." + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_saved_view.go"}, SQL: sql, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}}, Plan: proof}
	}
	// Every raw ID is visited, including deleted, foreign and collection rows.
	// Names/definitions are never projected into this source or its continuation.
	token := pk("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := pk("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")

	candidatePlan := plan
	candidatePlan.Indexes = append(append([]string{}, plan.Indexes...), token.Name, family.Name)
	candidatePlan.AutoIndexes = append(append([]stmt.AutoIndexBinding{}, plan.AutoIndexes...), token, family)
	candidatePlan.Seeks = []string{"token_hash=?", "id=?", "resource_id=? AND authority=? AND account_id=? AND profile_id=?"}
	candidate := `SELECT r.deleted,length(NULLIF(r.kind,'view')),length(NULLIF(r.owner_authority,f.authority)),length(NULLIF(r.owner_account,f.account_id)),length(NULLIF(r.owner_profile,f.profile_id)),s.role,r.revision
 FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
 CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=?
 LEFT JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=f.authority AND s.account_id=f.account_id AND s.profile_id=f.profile_id
 WHERE t.token_hash=? AND f.authority='local'`
	rawPlan := stmt.Plan{Hot: true, Indexes: []string{resource.Name}, AutoIndexes: []stmt.AutoIndexBinding{resource}, Seeks: []string{"id>?"}}
	out := []stmt.Definition{
		add("generation", "SELECT revision FROM saved_resource_generation WHERE id=?", []stmt.Param{i("id")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"saved_resource_generation"}}),
		add("candidates.first", "SELECT r.id FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id>=? ORDER BY r.id LIMIT 1", []stmt.Param{s("after")}, []any{""}, rawPlan),
		add("candidates", "SELECT r.id FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id>? ORDER BY r.id LIMIT 1", []stmt.Param{s("after")}, []any{"saved-view"}, rawPlan),
		add("candidate", candidate, []stmt.Param{s("id"), s("token")}, []any{"saved-view", "token"}, candidatePlan),
	}
	// Separate compiled owner/shared recipes avoid OR and project no foreign
	// private payload even if an internal caller bypasses the scalar gate.
	for _, owner := range []bool{true, false} {
		suffix := "shared"
		from := ` FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1
 LEFT JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=? AND s.account_id=? AND s.profile_id=?
 WHERE r.id=? AND r.kind='view' AND r.deleted=0`
		params := []stmt.Param{s("authority"), s("share_account"), s("share_profile"), s("id")}
		args := []any{"local", "a", "p", "saved-view"}
		pointPlan := plan
		pointPlan.Seeks = []string{"id=?", "resource_id=? AND authority=? AND account_id=? AND profile_id=?"}
		if owner {
			suffix = "owner"
			from = " FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='view' AND r.deleted=0 AND r.owner_authority=? AND r.owner_account=? AND r.owner_profile=?"
			params = []stmt.Param{s("id"), s("owner_authority"), s("owner_account"), s("owner_profile")}
			args = []any{"saved-view", "local", "a", "p"}
			pointPlan = stmt.Plan{Hot: true, Indexes: []string{resource.Name}, AutoIndexes: []stmt.AutoIndexBinding{resource}, Seeks: []string{"id=?"}}
		} else {
			from += " AND s.role IN('viewer','editor')"
		}

		out = append(out,
			add("resource."+suffix+".bytes", "SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.definition)"+from, params, args, pointPlan),
			add("resource."+suffix, "SELECT r.name,r.summary,r.definition,r.revision"+from, params, args, pointPlan),
		)
	}

	return out
}

// Only the scalar directory sources qualify under the present HN grammar.
func rbSavedViewDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, d := range rbSavedViewAllDraftDefinitions() {
		if !strings.Contains(string(d.ID), ".resource.") {
			out = append(out, d)
		}
	}
	return out
}

// Publication gate: HN must certify these exact native octet points before any
// registry may admit execution. No ordinary length()/columns*max substitute.
func rbSavedViewDefinitionDrafts() []stmt.Definition {
	var out []stmt.Definition
	for _, d := range rbSavedViewAllDraftDefinitions() {
		if strings.Contains(string(d.ID), ".resource.") {
			out = append(out, d)
		}
	}
	return out
}
