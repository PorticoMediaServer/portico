package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Private source statements for the profile-restrictions replacement (542).
// None opens, commits, authenticates, registers a command or mints authority;
// the US-01 stamp and US-08 dispatcher own those decisions. Every source string
// read is either compared inside SQLite (role narrowing) or is the writer's own
// prepared projection, so no unbounded source TEXT is materialized before the
// funded grant is charged.
const (
	// The caller's current session role must equal its current membership role,
	// and only a live managing role may edit. Comparing inside SQLite copies no
	// unbounded family role text out of the row.
	usRestrictionsScopeCheck stmt.ID = "us.restrictions.scope.check"
	// Live target profile facts: the profile must exist in the caller's own
	// account, carry the frozen lifetime, and not be retired.
	usRestrictionsTargetFacts stmt.ID = "us.restrictions.target"
	// The one replacement's current row: its revision is the CAS witness and
	// allow_dvr is the only pre-existing field the product's grant rule reads.
	usRestrictionsCurrentRow stmt.ID = "us.restrictions.current"
	usRestrictionsUpdate     stmt.ID = "us.restrictions.update"
	usRestrictionsInsert     stmt.ID = "us.restrictions.insert"
)

func usRestrictionsPoint(table, index, origin string, columns ...string) stmt.AutoIndexBinding {
	b := stmt.AutoIndexBinding{Table: table, Name: index, Origin: origin}
	for _, column := range columns {
		b.Columns = append(b.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return b
}

// usRestrictionsDefinitions is the closed source/effect statement set. The
// single replacement is an UPDATE guarded by profile/revision, or an INSERT when
// the profile has no stored row yet; both write exactly one profile_restrictions
// row and never touch direct_profiles, sessions, devices or library policy.
func usRestrictionsDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	scopePlan := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_authorization_session_families_1"},
		Seeks:   []string{"account_id=?", "id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			usRestrictionsPoint("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
			usRestrictionsPoint("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
		}}
	targetPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"},
		Seeks:       []string{"account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usRestrictionsPoint("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id")}}
	currentPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_profile_restrictions_1"},
		Seeks:       []string{"profile_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{usRestrictionsPoint("profile_restrictions", "sqlite_autoindex_profile_restrictions_1", "pk", "profile_id")}}
	defs := []stmt.Definition{
		{ID: usRestrictionsScopeCheck, SQL: `SELECT 1
FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.account_id=m.account_id
WHERE m.account_id=? AND f.id=? AND f.role=m.role AND m.role='owner' AND m.disabled=0`,
			Params:  []stmt.Param{text("account"), text("family")},
			Cohorts: []stmt.Cohort{{Name: "current_owner", Bindings: []any{"issuer", "issuer-family"}}, {Name: "missing", Bindings: []any{"no-account", "no-family"}}},
			Plan:    scopePlan},
		{ID: usRestrictionsTargetFacts, SQL: `SELECT p.deleted,p.is_primary
FROM direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2
WHERE p.account_id=? AND p.id=? AND p.lifetime=?`,
			Params:  []stmt.Param{text("account"), text("profile"), text("lifetime")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"issuer", "restricted-profile", "dddddddddddddddddddddddddddddddd"}}, {Name: "stale_lifetime", Bindings: []any{"issuer", "restricted-profile", "ffffffffffffffffffffffffffffffff"}}},
			Plan:    targetPlan},
		{ID: usRestrictionsCurrentRow, SQL: `SELECT revision,allow_dvr
FROM profile_restrictions AS r INDEXED BY sqlite_autoindex_profile_restrictions_1
WHERE r.profile_id=?`,
			Params:  []stmt.Param{text("profile")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"restricted-profile"}}},
			Plan:    currentPlan},
		{ID: usRestrictionsUpdate, SQL: `UPDATE profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1
SET rating_system=?,maximum_age_rating=?,maximum_age=?,allow_unrated=?,blocked_labels=?,allow_downloads=?,allow_live_tv=?,allow_dvr=?,allow_watch_together=?,revision=?
WHERE profile_id=? AND revision=?
RETURNING revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together`,
			Params: []stmt.Param{text("rating_system"), text("maximum_age_rating"), integer("maximum_age"), integer("allow_unrated"), text("blocked_labels"), integer("allow_downloads"), integer("allow_live_tv"), integer("allow_dvr"), integer("allow_watch_together"), integer("revision"), text("profile"), integer("expected_revision")},
			Cohorts: []stmt.Cohort{{Name: "current", Bindings: []any{"mpaa", "PG-13", int64(13), int64(0), `["violence"]`, int64(1), int64(0), int64(0), int64(1), int64(2), "restricted-profile", int64(1)}},
				{Name: "stale", Bindings: []any{"", "", int64(-1), int64(1), "[]", int64(1), int64(1), int64(0), int64(1), int64(2), "restricted-profile", int64(2)}}},
			Plan: currentPlan},
		{ID: usRestrictionsInsert, SQL: `INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision)
VALUES(?,?,?,?,?,?,?,?,?,?,?)
RETURNING revision,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together`,
			Params:  []stmt.Param{text("profile"), text("rating_system"), text("maximum_age_rating"), integer("maximum_age"), integer("allow_unrated"), text("blocked_labels"), integer("allow_downloads"), integer("allow_live_tv"), integer("allow_dvr"), integer("allow_watch_together"), integer("revision")},
			Cohorts: []stmt.Cohort{{Name: "first_replacement", Bindings: []any{"restricted-profile", "mpaa", "PG-13", int64(13), int64(1), `["violence"]`, int64(1), int64(1), int64(0), int64(1), int64(2)}}},
			Plan:    stmt.Plan{Hot: true}},
	}
	for n := range defs {
		if defs[n].Kind == "" {
			if defs[n].ID == usRestrictionsUpdate || defs[n].ID == usRestrictionsInsert {
				defs[n].Kind = stmt.Write
			} else {
				defs[n].Kind = stmt.Read
			}
		}
		defs[n].Owner = "store/writer US profile restrictions"
		defs[n].CallSites = []string{"store/writer/us_restrictions_snapshot.go", "store/writer/us_restrictions_statements.go"}
	}
	return defs
}
