package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func (f UOMembershipFlag) valid() bool {
	return f >= UOMembershipWatchlist && f <= UOMembershipNotInterested
}

// membershipName is the closed public suffix. It never reaches SQL.
func (f UOMembershipFlag) membershipName() string {
	switch f {
	case UOMembershipWatchlist:
		return "watchlist"
	case UOMembershipFavorite:
		return "favorite"
	case UOMembershipNotInterested:
		return "not-interested"
	}
	return ""
}

// membershipColumn and membershipIndex are the closed sparse flag column and its
// own partial index. Each flag has exactly one fixed statement.
func (f UOMembershipFlag) membershipColumn() string {
	switch f {
	case UOMembershipWatchlist:
		return "watchlisted"
	case UOMembershipFavorite:
		return "favorite"
	case UOMembershipNotInterested:
		return "not_interested"
	}
	return ""
}

func (f UOMembershipFlag) membershipIndex() string {
	switch f {
	case UOMembershipWatchlist:
		return "personal_current_watchlist"
	case UOMembershipFavorite:
		return "personal_current_favorite"
	case UOMembershipNotInterested:
		return "personal_current_not_interested"
	}
	return ""
}

// UOMembershipDefinitions returns the inert UO membership definitions for
// explicit harness composition alongside HarnessDefinitions. Capture them on the
// actual reader before registration. The product does not select this layer
// before cutover.
func UOMembershipDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	key := func(table, name string, columns ...string) stmt.AutoIndexBinding {
		v := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range columns {
			v.Columns = append(v.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return v
	}
	token := key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash")
	family := key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")
	var out []stmt.Definition
	for _, flag := range []UOMembershipFlag{UOMembershipWatchlist, UOMembershipFavorite, UOMembershipNotInterested} {
		for _, tail := range []bool{false, true} {
			variant := "first"
			if tail {
				variant = "tail"
			}
			params := []stmt.Param{s("token")}
			present := []any{"token"}
			seek := "profile_id=?"
			if tail {
				params = append(params, b("after"))
				present = append(present, make([]byte, 16))
				seek = "profile_id=? AND item_public_id>?"
			}
			empty := append([]any(nil), present...)
			empty[0] = "missing"
			for _, lengths := range []bool{false, true} {
				name := "foreground.uo.membership." + flag.membershipName() + "." + variant
				if lengths {
					name += ".bytes"
				}
				out = append(out, stmt.Definition{
					ID: readStatementID(name), Owner: "store/read UO membership",
					CallSites: []string{"store/read/uo_membership.go:ForegroundReader.MembershipWindow"}, Kind: stmt.Read,
					SQL:    uoMembershipSQL(flag, tail, lengths),
					Params: params,
					Cohorts: []stmt.Cohort{
						{Name: "present", Bindings: present},
						{Name: "empty", Bindings: empty},
					},
					Plan: stmt.Plan{
						Hot: true, PrimaryKeys: []string{"e"},
						Indexes:     []string{token.Name, family.Name, flag.membershipIndex()},
						AutoIndexes: []stmt.AutoIndexBinding{token, family},
						Seeks:       []string{"token_hash=?", "id=?", seek},
					},
				})
			}
		}
	}
	return out
}

// uoMembershipSQL reads at most 64 raw sparse positions for one profile and one
// literal flag before any entity/library/kind authorization. The token/family
// cross join only derives the profile of the already checked viewing token; the
// shared fence remains the authority. No library/live/visibility predicate is
// silently added to the sparse membership, and no catalogue row drives it.
func uoMembershipSQL(flag UOMembershipFlag, tail, lengths bool) string {
	columns := "p.item_public_id,e.id,'',e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind"
	if lengths {
		columns = "p.item_public_id,e.id,octet_length(e.title)"
	}
	seek := ""
	if tail {
		seek = " AND p.item_public_id>?"
	}
	return "SELECT " + columns + `
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN personal_items AS p INDEXED BY ` + flag.membershipIndex() + ` ON p.profile_id=f.profile_id
LEFT JOIN catalog_entities AS e ON e.id=p.item_id
WHERE t.token_hash=? AND ` + flag.membershipColumn() + `=1` + seek + `
ORDER BY p.item_public_id ASC LIMIT 64`
}
