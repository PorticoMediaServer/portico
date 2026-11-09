package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert compiled definitions: no product registration or runtime cutover.
func browseDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
		pin := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, column := range columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY", Desc: false})
		}
		return pin
	}
	pins := map[string][]stmt.AutoIndexBinding{
		"principal": {
			key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "pk", "token_hash"),
			key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
			key("identity_devices", "sqlite_autoindex_identity_devices_1", "pk", "id"),
			key("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			key("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
			key("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
			key("profile_restrictions", "sqlite_autoindex_profile_restrictions_1", "pk", "profile_id"),
			key("access_limits", "sqlite_autoindex_access_limits_1", "pk", "account_id"),
		},
		"library": {
			key("libraries", "sqlite_autoindex_libraries_1", "pk", "id"),
			key("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "pk", "profile_id", "library_id"),
		},
	}
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		p.AutoIndexes = pins[name]
		empty := append([]any(nil), bindings...)
		switch name {
		case "principal":
			empty[0] = "missing"
		case "library":
			empty[1] = "missing"
		case "library_identity":
			empty[0] = int64(3)
		case "global_authority":
			empty[0] = int64(2)
		case "kind":
			empty[0] = int64(99)
		default:
			empty[0] = int64(3)
		}
		sites := []string{"store/read/publication.go:BrowseReader.Window"}
		switch name {
		case "global_authority", "principal", "library_identity", "library", "kind":
			sites = append(sites, "store/read/search_permission.go:SearchDispatcher.permissionFence")
		}
		out = append(out, stmt.Definition{ID: statementID(name), SQL: sql, Owner: "store/read", CallSites: sites, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("global_authority", "SELECT authority_generation FROM identity_state WHERE singleton=?", []stmt.Param{i("singleton")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}})
	add("principal", `SELECT a.id,p.id,m.incarnation,p.lifetime,a.epoch,m.role,m.allowed_libraries,p.allowed_libraries,t.expires_at,f.authorization_horizon,f.authority_generation,m.revision,COALESCE(pr.maximum_age,-1),COALESCE(pr.allow_unrated,1),COALESCE(pr.blocked_labels,'[]'),lim.account_id,p.revision,COALESCE(pr.revision,0)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
LEFT JOIN profile_restrictions AS pr INDEXED BY sqlite_autoindex_profile_restrictions_1 ON pr.profile_id=p.id
LEFT JOIN access_limits AS lim INDEXED BY sqlite_autoindex_access_limits_1 ON lim.account_id=a.id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0 AND f.pin_gate=0
AND f.authority='local' AND d.authority='local' AND d.retired_ms=0 AND d.account_id=a.id AND a.epoch=f.epoch
AND f.authority_generation=? AND m.disabled=0 AND p.deleted=0
AND f.command_member_incarnation=m.incarnation AND f.command_profile_lifetime=p.lifetime`, []stmt.Param{s("token"), i("authority_generation")}, []any{"token", int64(1)}, stmt.Plan{Hot: true, Seeks: []string{"token_hash=?", "account_id=? AND id=?"}, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_identity_devices_1", "sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_2", "sqlite_autoindex_profile_restrictions_1", "sqlite_autoindex_access_limits_1"}})
	add("library_identity", "SELECT library_id,root FROM catalog_libraries WHERE id=? AND retired=0", []stmt.Param{i("library")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}})
	add("library", `SELECT l.id,l.data_revision,l.data_order_revision,l.data_metadata_revision,COALESCE(v.revision,0),l.age_input_revision,l.age_published_revision,l.age_pending_operations,l.root,l.playback_origin_incarnation,l.playback_origin_revision
FROM libraries AS l INDEXED BY sqlite_autoindex_libraries_1
LEFT JOIN viewer_revisions AS v INDEXED BY sqlite_autoindex_viewer_revisions_1 ON v.library_id=l.id AND v.profile_id=?
WHERE l.id=?`, []stmt.Param{s("profile"), s("library")}, []any{"p", "movies"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1", "sqlite_autoindex_viewer_revisions_1"}, Seeks: []string{"id=?", "profile_id=? AND library_id=?"}})
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
