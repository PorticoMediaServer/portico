package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func statementID(name string) stmt.ID { return stmt.ID("read.browse." + name) }

// Registry returns a closed set of compiled shapes for GV composition and HN
// qualification. No input can choose SQL, an index or a native bank identifier.
func Registry() (*stmt.Registry, error) { return stmt.New(browseDefinitions()) }

func browseDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		switch name {
		case "principal":
			empty[0] = "missing"
		case "library":
			empty[1] = int64(3)
		case "kind":
			empty[0] = int64(99)
		default:
			empty[0] = int64(3)
		}
		out = append(out, stmt.Definition{ID: statementID(name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/browse.go:BrowseWindow"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("principal", `SELECT a.id,p.id,m.incarnation,p.lifetime,a.epoch,m.role,m.allowed_libraries,p.allowed_libraries,t.expires_at,f.authorization_horizon,g.authority_generation,m.revision,COALESCE(pr.maximum_age,-1),COALESCE(pr.allow_unrated,1),COALESCE(pr.blocked_labels,'[]'),lim.account_id,p.revision,COALESCE(pr.revision,0)
FROM authorization_family_tokens AS t
CROSS JOIN authorization_session_families AS f ON f.id=t.family_id
CROSS JOIN identity_devices AS d ON d.id=f.device_id
CROSS JOIN accounts AS a ON a.id=f.account_id
CROSS JOIN direct_memberships AS m ON m.account_id=a.id
CROSS JOIN direct_profiles AS p ON p.id=f.profile_id AND p.account_id=a.id
CROSS JOIN identity_state AS g ON g.singleton=1
LEFT JOIN profile_restrictions AS pr ON pr.profile_id=p.id
LEFT JOIN access_limits AS lim ON lim.account_id=a.id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0
AND f.authority='local' AND d.authority='local' AND d.account_id=a.id AND a.epoch=f.epoch
AND g.authority_generation=f.authority_generation AND m.disabled=0 AND p.deleted=0`, []stmt.Param{s("token")}, []any{"token"}, stmt.Plan{Hot: true, PrimaryKeys: []string{"g"}, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_identity_devices_1", "sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_1", "sqlite_autoindex_profile_restrictions_1", "sqlite_autoindex_access_limits_1"}})
	add("library", `SELECT c.library_id,l.data_revision,l.data_order_revision,l.data_metadata_revision,COALESCE(v.revision,0),l.age_input_revision,l.age_published_revision,l.age_pending_operations
FROM catalog_libraries AS c CROSS JOIN libraries AS l ON l.id=c.library_id
LEFT JOIN viewer_revisions AS v ON v.library_id=c.library_id AND v.profile_id=?
WHERE c.id=? AND c.retired=0`, []stmt.Param{s("profile"), i("library")}, []any{"p", int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"c"}, Indexes: []string{"sqlite_autoindex_libraries_1", "sqlite_autoindex_viewer_revisions_1"}})
	add("kind", "SELECT container FROM catalog_kinds WHERE id=? AND browsable=1", []stmt.Param{i("kind")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_kinds"}})
	// Only this finite compiled enum is assembled at construction; requests
	// cannot contribute identifiers or operators. Equality-tail and next-key
	// are separate disjoint seeks with the index's exact NOCASE collation.
	for _, article := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if article {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, desc := range []bool{false, true} {
			order, op, direction := "ASC", ">", "asc."
			if desc {
				order, op, direction = "DESC", "<", "desc."
			}
			base := "SELECT id," + key + ",public_id,title,retired,revision,admission_own_age FROM catalog_entities INDEXED BY " + index + " WHERE library_id=? AND kind=?"
			end := " ORDER BY " + key + " COLLATE NOCASE " + order + ",id " + order + " LIMIT ?"
			p := func(seek string) stmt.Plan {
				return stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
			}
			add("title."+variant+direction+"first", base+end, []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(64)}, p("library_id=? AND kind=?"))
			add("title."+variant+direction+"tail", base+" AND "+key+" COLLATE NOCASE=? AND id"+op+"?"+end, []stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")}, []any{int64(1), int64(1), "a", int64(500), int64(64)}, p(key+"=? AND id"+op+"?"))
			add("title."+variant+direction+"next", base+" AND "+key+" COLLATE NOCASE"+op+"?"+end, []stmt.Param{i("library"), i("kind"), s("key"), i("limit")}, []any{int64(1), int64(1), "a", int64(64)}, p(key+op+"?"))
		}
	}
	return out
}
