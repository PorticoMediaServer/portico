package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsSavedCollectionSourceSQL = `SELECT r.revision,r.deleted,e.rowid FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id LEFT JOIN saved_resource_entries AS e INDEXED BY saved_resource_entries_order ON e.resource_id=r.id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 AND r.owner_authority=f.authority AND r.owner_account=f.account_id AND r.owner_profile=f.profile_id LIMIT 1`
const rsSavedCollectionServerSQL = `SELECT r.revision,r.deleted,e.rowid FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 LEFT JOIN saved_resource_entries AS e INDEXED BY saved_resource_entries_order ON e.resource_id=r.id WHERE r.id=? AND r.kind='collection' AND r.deleted=0 AND r.visibility='server' LIMIT 1`
const rsSavedCollectionShareSQL = `SELECT r.revision,r.deleted,e.rowid FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id CROSS JOIN saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 ON r.id=r.id CROSS JOIN saved_resource_shares AS s INDEXED BY sqlite_autoindex_saved_resource_shares_1 ON s.resource_id=r.id AND s.authority=f.authority AND s.account_id=f.account_id AND s.profile_id=f.profile_id LEFT JOIN saved_resource_entries AS e INDEXED BY saved_resource_entries_order ON e.resource_id=r.id WHERE t.token_hash=? AND r.id=? AND f.authority='local' AND r.kind='collection' AND r.deleted=0 LIMIT 1`
const rsSavedCollectionMetadataSQL = `SELECT name,summary,created_at FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`
const rsSavedCollectionOctetsSQL = `SELECT octet_length(name),octet_length(summary),octet_length(created_at) FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`

// SavedCollectionDefinitions is inert: composition must Capture these exact
// definitions on the actual Reader. No runtime route is registered here.
func SavedCollectionDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) stmt.Definition {
		for _, idx := range p.Indexes {
			var table string
			var columns []string
			switch idx {
			case "sqlite_autoindex_saved_resources_1":
				table = "saved_resources"
				columns = []string{"id"}
			case "sqlite_autoindex_saved_resource_shares_1":
				table = "saved_resource_shares"
				columns = []string{"resource_id", "authority", "account_id", "profile_id"}
			case "sqlite_autoindex_authorization_family_tokens_1":
				table = "authorization_family_tokens"
				columns = []string{"token_hash"}
			case "sqlite_autoindex_authorization_session_families_1":
				table = "authorization_session_families"
				columns = []string{"id"}
			}
			if table != "" {
				pin := stmt.AutoIndexBinding{Table: table, Name: idx, Origin: "pk"}
				for _, c := range columns {
					pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
				}
				p.AutoIndexes = append(p.AutoIndexes, pin)
			}
		}
		return stmt.Definition{ID: readStatementID("rs.saved_collection." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_saved_collection.go:SavedCollectionReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "source", Bindings: args}}, Plan: p}
	}
	out := []stmt.Definition{
		add("source", rsSavedCollectionSourceSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1", "saved_resource_entries_order"}}),
		add("generation", `SELECT revision FROM saved_resource_generation WHERE id=1`, nil, nil, stmt.Plan{Hot: true, PrimaryKeys: []string{"saved_resource_generation"}}),
	}
	out = append(out, add("server", rsSavedCollectionServerSQL, []stmt.Param{text("resource")}, []any{"saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1", "saved_resource_entries_order"}}))
	out = append(out, add("share", rsSavedCollectionShareSQL, []stmt.Param{text("token"), text("resource")}, []any{"token", "saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_saved_resources_1", "sqlite_autoindex_saved_resource_shares_1", "saved_resource_entries_order"}}))

	for _, pair := range []struct{ name, sql string }{{"metadata", rsSavedCollectionMetadataSQL}, {"metadata_octets", rsSavedCollectionOctetsSQL}} {
		out = append(out, add(pair.name, pair.sql, []stmt.Param{text("resource")}, []any{"saved"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}}))
	}
	// Position-only seeks never decode an unmeasured TEXT id/order key. Full
	// member delivery requires the cross-library eligibility composition below.
	out = append(out, add("nonempty", `SELECT 1 FROM saved_resource_entries INDEXED BY saved_resource_entries_order WHERE resource_id=? LIMIT 1`, []stmt.Param{text("resource")}, []any{"saved"}, stmt.Plan{Hot: true, Indexes: []string{"saved_resource_entries_order"}, Seeks: []string{"resource_id=?"}}))
	return out
}
func init() {
	registerRSBytePoint("rs.saved_collection.metadata", rsBytePoint{lengths: "rs.saved_collection.metadata_octets", columns: []int{0, 1, 2}})
}
