package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usInvitationShortInsert stmt.ID = "us.invitation.short.insert"
	usInvitationShortUpdate stmt.ID = "us.invitation.short.update"
)
const usInvitationShortSourceColumns = `invitation_id,lookup_hash,binding_hash,server_id,invitation_revision,sign_in_kind,kind_revision,purpose,expires_ms`

// Parent classifies the original receipt, verifies current authority, invitation
// and staged-key facts, and preflights the actual replacement bytes first. The
// separate INSERT/point UPDATE implement source upsert inside the same frame.
func usInvitationShortEffectDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	params := []stmt.Param{t("lookup"), t("binding"), t("server"), i("revision"), t("kind"), i("kind_revision"), t("purpose"), i("expires"), t("invitation")}
	args := []any{usInvitationFixtureHash, usInvitationFixtureHash, "srv_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", int64(1), "local", int64(1), "accept", int64(2000), "invite"}
	defs := []stmt.Definition{
		{ID: usInvitationShortInsert, SQL: `INSERT INTO access_invitation_short_codes(lookup_hash,binding_hash,server_id,invitation_revision,sign_in_kind,kind_revision,purpose,expires_ms,invitation_id) VALUES(?,?,?,?,?,?,?,?,?) RETURNING ` + usInvitationShortSourceColumns, Params: params, Plan: stmt.Plan{Hot: true}},
		{ID: usInvitationShortUpdate, SQL: `UPDATE access_invitation_short_codes INDEXED BY sqlite_autoindex_access_invitation_short_codes_1 SET lookup_hash=?,binding_hash=?,server_id=?,invitation_revision=?,sign_in_kind=?,kind_revision=?,purpose=?,expires_ms=? WHERE invitation_id=? RETURNING ` + usInvitationShortSourceColumns, Params: params, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitation_short_codes_1"}, Seeks: []string{"invitation_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitation_short_codes", Name: "sqlite_autoindex_access_invitation_short_codes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "invitation_id", Collation: "BINARY"}}}}}},
	}
	for n := range defs {
		defs[n].Kind = stmt.Write
		defs[n].Owner = "store/writer US short invitation effects"
		defs[n].CallSites = []string{"store/writer/us_invitation_short_statements.go"}
		defs[n].Cohorts = []stmt.Cohort{{Name: "point", Bindings: args}}
	}
	return defs
}
