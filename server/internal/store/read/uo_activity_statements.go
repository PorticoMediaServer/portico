package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// uoActivityAuto pins one exact PK autoindex. It never accepts a name from a
// request.
func uoActivityAuto(table, name string, columns ...string) stmt.AutoIndexBinding {
	out := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
	for _, column := range columns {
		out.Columns = append(out.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return out
}

// uoActivityHeadSQL reads the current personal generation root for the viewing
// token's own profile. A missing row is the ordinary empty head, not permission.
const uoActivityHeadSQL = `SELECT COALESCE(r.profile_id,''),COALESCE(r.revision,0),COALESCE(r.activity_generation,0),COALESCE(r.recommendation_generation,0),COALESCE(r.history_visible_from_id,0),COALESCE(r.action_sequence,0)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
LEFT JOIN personal_activity_revisions AS r INDEXED BY sqlite_autoindex_personal_activity_revisions_1 ON r.profile_id=f.profile_id
WHERE t.token_hash=?`

// uoActivityPersonalSQL reads at most the bound raw positions of one closed
// current-personal time stream for the viewing token's own profile. Ordering is
// the exact sparse index order; no catalogue scan or temporary sort drives it.
func uoActivityPersonalSQL(f UOActivityFamily, variant string, sizes bool) string {
	columns := `p.item_public_id,e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,p.activity_generation,p.watched,p.resume_position,p.resume_unit,p.play_count,p.favorite,p.watchlisted,p.not_interested,p.rating,p.` + f.activityColumn() + `,p.resume_completed,e.library_id,e.kind`
	if sizes {
		columns = `p.item_public_id,e.id,octet_length(e.title)`
	}
	seek := ""
	switch variant {
	case "same":
		seek = ` AND p.` + f.activityColumn() + `=? AND p.item_public_id>?`
	case "older":
		seek = ` AND p.` + f.activityColumn() + `<?`
	}
	return `SELECT ` + columns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_items AS p INDEXED BY ` + f.activityIndex() + ` ON p.profile_id=f.profile_id
LEFT JOIN catalog_entities AS e ON e.id=p.item_id
WHERE t.token_hash=? AND p.activity_visible=1 AND p.` + f.activityColumn() + ` IS NOT NULL` + seek + `
ORDER BY p.` + f.activityColumn() + ` DESC,p.item_public_id ASC LIMIT ?`
}

// uoActivityHistorySQL reads bounded immutable actor/item/time occurrences for
// the viewing token's own account and profile. Only current-lifetime rows are
// eligible; a retired membership or profile birth is never copied.
func uoActivityHistorySQL(variant string, sizes bool) string {
	columns := `h.id,h.occurred_at,h.event_kind,h.item_public_id,h.item_kind,h.position,h.played_ms,h.completed,h.activity_generation,h.accepted_sequence,h.subject_id,h.membership_incarnation,h.profile_lifetime,h.library_public_id`
	if sizes {
		columns = `h.id,octet_length(h.occurred_at),octet_length(h.event_kind)`
	}
	seek := ""
	switch variant {
	case "same":
		seek = ` AND h.occurred_at=? AND h.id<?`
	case "older":
		seek = ` AND h.occurred_at<?`
	}
	return `SELECT ` + columns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN play_history AS h INDEXED BY play_history_admin_user ON h.account_key=f.account_id
WHERE t.token_hash=? AND h.profile_id=f.profile_id AND h.membership_incarnation=f.command_member_incarnation AND h.profile_lifetime=f.command_profile_lifetime` + seek + `
ORDER BY h.occurred_at DESC,h.id DESC LIMIT ?`
}

func uoActivityStatementID(f UOActivityFamily, variant string, sizes bool) stmt.ID {
	id := "foreground.uo.activity." + f.activityName() + "." + variant
	if sizes {
		id += ".bytes"
	}
	return stmt.ID("read." + id)
}

// UOActivityDefinitions returns the inert UO activity definitions for explicit
// harness composition alongside HarnessDefinitions. Capture them on the actual
// reader before registration. The product does not select this layer before
// cutover.
func UOActivityDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	token := uoActivityAuto("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := uoActivityAuto("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	revisions := uoActivityAuto("personal_activity_revisions", "sqlite_autoindex_personal_activity_revisions_1", "profile_id")
	out := []stmt.Definition{{
		ID: stmt.ID("read.uo.activity.head"), Owner: "store/read UO activity",
		CallSites: []string{"store/read/uo_activity.go:ForegroundReader.activityWindow"},
		Kind:      stmt.Read, SQL: uoActivityHeadSQL,
		Params: []stmt.Param{s("token")},
		Cohorts: []stmt.Cohort{
			{Name: "present", Bindings: []any{"token"}},
			{Name: "empty", Bindings: []any{"missing"}},
		},
		Plan: stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, revisions.Name}, AutoIndexes: []stmt.AutoIndexBinding{token, family, revisions}, Seeks: []string{"token_hash=?", "profile_id=?"}},
	}}
	for _, f := range []UOActivityFamily{UOActivityRecent, UOActivityProgress, UOActivityCompleted} {
		for _, tail := range []bool{false, true} {
			variants := []string{"first"}
			if tail {
				variants = []string{"same", "older"}
			}
			for _, variant := range variants {
				for _, sizes := range []bool{false, true} {
					params := []stmt.Param{s("token"), i("limit")}
					bindings := []any{"token", int64(MaxRaw + 1)}
					if variant == "same" {
						params = []stmt.Param{s("token"), s("time"), b("public"), i("limit")}
						bindings = []any{"token", "2026-11-05T12:00:00.000000000Z", make([]byte, 16), int64(MaxRaw + 1)}
					}
					if variant == "older" {
						params = []stmt.Param{s("token"), s("time"), i("limit")}
						bindings = []any{"token", "2026-11-05T12:00:00.000000000Z", int64(MaxRaw + 1)}
					}
					empty := append([]any(nil), bindings...)
					empty[0] = "missing"
					plan := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Indexes: []string{token.Name, family.Name, f.activityIndex()}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "profile_id=?"}}
					out = append(out, stmt.Definition{
						ID: uoActivityStatementID(f, variant, sizes), Owner: "store/read UO activity",
						CallSites: []string{"store/read/uo_activity.go:ForegroundReader.activityWindow"},
						Kind:      stmt.Read, SQL: uoActivityPersonalSQL(f, variant, sizes),
						Params: params,
						Cohorts: []stmt.Cohort{
							{Name: "present", Bindings: bindings},
							{Name: "empty", Bindings: empty},
						},
						Plan: plan,
					})
				}
			}
		}
	}
	for _, tail := range []bool{false, true} {
		variants := []string{"first"}
		if tail {
			variants = []string{"same", "older"}
		}
		for _, variant := range variants {
			for _, sizes := range []bool{false, true} {
				params := []stmt.Param{s("token"), i("limit")}
				bindings := []any{"token", int64(MaxRaw + 1)}
				if variant == "same" {
					params = []stmt.Param{s("token"), s("time"), i("id"), i("limit")}
					bindings = []any{"token", "2026-11-05T12:00:00.000000000Z", int64(100), int64(MaxRaw + 1)}
				}
				if variant == "older" {
					params = []stmt.Param{s("token"), s("time"), i("limit")}
					bindings = []any{"token", "2026-11-05T12:00:00.000000000Z", int64(MaxRaw + 1)}
				}
				empty := append([]any(nil), bindings...)
				empty[0] = "missing"
				out = append(out, stmt.Definition{
					ID: uoActivityStatementID(UOActivityHistory, variant, sizes), Owner: "store/read UO activity",
					CallSites: []string{"store/read/uo_activity.go:ForegroundReader.activityWindow"},
					Kind:      stmt.Read, SQL: uoActivityHistorySQL(variant, sizes),
					Params: params,
					Cohorts: []stmt.Cohort{
						{Name: "present", Bindings: bindings},
						{Name: "empty", Bindings: empty},
					},
					Plan: stmt.Plan{Hot: true, Indexes: []string{token.Name, family.Name, UOActivityHistory.activityIndex()}, AutoIndexes: []stmt.AutoIndexBinding{token, family}, Seeks: []string{"token_hash=?", "account_key=?"}},
				})
			}
		}
	}
	return out
}
