package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usMemberPolicyOctets  stmt.ID = "us.policy.member.octets"
	usMemberPolicyFacts   stmt.ID = "us.policy.member.facts"
	usProfilePolicyOctets stmt.ID = "us.policy.profile.octets"
	usProfilePolicyFacts  stmt.ID = "us.policy.profile.facts"
)

// Inert target-fact statements, not current issuer authentication or disclosure
// authority. W must authorize FIRST and reserve actual point-preflight bytes in
// the SAME snapshot before the TEXT payload statements. No table scan, callback,
// connection or accepting registration escapes this domain.
func usPolicyDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	memberParams := []stmt.Param{text("account"), text("member_incarnation")}
	profileParams := []stmt.Param{text("account"), text("member_incarnation"), text("profile"), text("profile_lifetime")}
	memberCohorts := []stmt.Cohort{
		{Name: "current", Bindings: []any{"us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}},
		{Name: "wrong_lifetime", Bindings: []any{"us-target", "ffffffffffffffffffffffffffffffff"}},
		{Name: "missing", Bindings: []any{"no-account", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}},
	}
	profileCohorts := []stmt.Cohort{
		{Name: "explicit_empty", Bindings: []any{"us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "us-profile", "ffffffffffffffffffffffffffffffff"}},
		{Name: "inherit", Bindings: []any{"us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "us-inherit", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
		{Name: "wrong_account", Bindings: []any{"us-other", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "us-profile", "ffffffffffffffffffffffffffffffff"}},
		{Name: "wrong_member_lifetime", Bindings: []any{"us-target", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "us-profile", "ffffffffffffffffffffffffffffffff"}},
		{Name: "wrong_profile_lifetime", Bindings: []any{"us-target", "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "us-profile", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	memberFrom := ` FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
WHERE m.account_id=? AND m.incarnation=?`
	profileFrom := ` FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=m.account_id
WHERE m.account_id=? AND m.incarnation=? AND p.id=? AND p.lifetime=?`
	memberPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1"}, Seeks: []string{"account_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}}}}
	profilePlan := memberPlan
	profilePlan.Indexes = append([]string{"sqlite_autoindex_direct_memberships_1"}, "sqlite_autoindex_direct_profiles_2")
	profilePlan.Seeks = []string{"account_id=?", "account_id=? AND id=?"}
	profilePlan.AutoIndexes = append(append([]stmt.AutoIndexBinding(nil), memberPlan.AutoIndexes...), stmt.AutoIndexBinding{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}})
	defs := []stmt.Definition{
		{ID: usMemberPolicyOctets, SQL: `SELECT octet_length(m.role),octet_length(m.allowed_libraries)` + memberFrom, Params: memberParams, Cohorts: memberCohorts, Plan: memberPlan},
		{ID: usMemberPolicyFacts, SQL: `SELECT m.role,m.revision,m.disabled,m.allowed_libraries` + memberFrom, Params: memberParams, Cohorts: memberCohorts, Plan: memberPlan},
		{ID: usProfilePolicyOctets, SQL: `SELECT octet_length(m.role),octet_length(m.allowed_libraries),coalesce(octet_length(p.allowed_libraries),0)` + profileFrom, Params: profileParams, Cohorts: profileCohorts, Plan: profilePlan},
		{ID: usProfilePolicyFacts, SQL: `SELECT m.role,m.revision,m.disabled,m.allowed_libraries,p.revision,p.deleted,p.is_primary,p.allowed_libraries` + profileFrom, Params: profileParams, Cohorts: profileCohorts, Plan: profilePlan},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US library policy"
		defs[n].CallSites = []string{"store/writer/us_policy_statements.go"}
	}
	return defs
}
