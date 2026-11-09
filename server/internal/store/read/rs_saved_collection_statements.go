package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// One saved COLLECTION is selected by its stored id. Permission is the current
// owner token, current server visibility or a current share row; no caller
// library hint and no directory union participates. Each query has a LIMIT 1
// existence point and never returns a foreign private name.
const rsSavedMembersSourceSQL = `SELECT r.revision,r.deleted FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 AND r.owner_authority=f.authority AND r.owner_account=f.account_id AND r.owner_profile=f.profile_id LIMIT 1`
const rsSavedMembersServerSQL = `SELECT r.revision,r.deleted FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0 AND r.visibility='server' LIMIT 1`
const rsSavedMembersShareSQL = `SELECT r.revision,r.deleted FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id CROSS JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=f.authority AND s.account_id=f.account_id AND s.profile_id=f.profile_id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 LIMIT 1`
const rsSavedMembersMetadataSQL = `SELECT name,summary,created_at,revision,visibility FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`
const rsSavedMembersMetadataOctetsSQL = `SELECT octet_length(name),octet_length(summary),octet_length(created_at),octet_length(visibility) FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`

// SavedCollectionDefinitions is inert: composition must Capture these exact
// definitions on the actual Reader. No runtime route is registered here.
func SavedCollectionDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) stmt.Definition {
		p.AutoIndexes = append(p.AutoIndexes, rsSavedAutoIndexes(p.Indexes)...)
		return stmt.Definition{ID: readStatementID("rs.saved.members." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_saved_collection.go:SavedCollectionReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "source", Bindings: args}}, Plan: p}
	}
	out := []stmt.Definition{
		add("source", rsSavedMembersSourceSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1"}}),
		add("generation", `SELECT revision FROM saved_resource_generation WHERE id=1`, nil, nil, stmt.Plan{Hot: true, PrimaryKeys: []string{"saved_resource_generation"}}),
	}
	out = append(out, add("server", rsSavedMembersServerSQL, []stmt.Param{text("resource")}, []any{"saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}}))
	out = append(out, add("share", rsSavedMembersShareSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1", "sqlite_autoindex_saved_resource_shares_1"}}))
	for _, pair := range []struct{ name, sql string }{{"metadata", rsSavedMembersMetadataSQL}, {"metadata_octets", rsSavedMembersMetadataOctetsSQL}} {
		out = append(out, add(pair.name, pair.sql, []stmt.Param{text("resource")}, []any{"saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}}))
	}
	return out
}

// rsSavedAutoIndexes names the primary-key index shape for each forced index.
// The Capture receipt still proves the actual plan; this only declares the
// closed native binding expected by stmt.New.
func rsSavedAutoIndexes(indexes []string) []stmt.AutoIndexBinding {
	var out []stmt.AutoIndexBinding
	for _, idx := range indexes {
		var table string
		var columns []string
		switch idx {
		case "sqlite_autoindex_saved_resources_1":
			table, columns = "saved_resources", []string{"id"}
		case "sqlite_autoindex_saved_resource_shares_1":
			table, columns = "saved_resource_shares", []string{"resource_id", "authority", "account_id", "profile_id"}
		case "sqlite_autoindex_authorization_family_tokens_1":
			table, columns = "authorization_family_tokens", []string{"token_hash"}
		case "sqlite_autoindex_authorization_session_families_1":
			table, columns = "authorization_session_families", []string{"id"}
		case "sqlite_autoindex_libraries_1":
			table, columns = "libraries", []string{"id"}
		default:
			continue
		}
		pin := stmt.AutoIndexBinding{Table: table, Name: idx, Origin: "pk"}
		for _, c := range columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		out = append(out, pin)
	}
	return out
}

func init() {
	registerRSBytePoint("rs.saved.members.metadata", rsBytePoint{lengths: "rs.saved.members.metadata_octets", columns: []int{0, 1, 2, 4}})
}
