package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	usSecretDrainSource stmt.ID = "us.credential.secret_drain.source"
	usSecretDrainBytes  stmt.ID = "us.credential.secret_drain.bytes"
	usSecretDrainDelete stmt.ID = "us.credential.secret_drain.delete"
	usSecretDrainAbsent stmt.ID = "us.credential.secret_drain.absent"
)

func usCredentialSecretDrainDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: usSecretDrainSource, SQL: `SELECT 1 FROM api_credentials AS c INDEXED BY sqlite_autoindex_api_credentials_1 CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=c.family_id WHERE c.id=? AND c.kind=? AND c.account_id=? AND c.profile_id=? AND c.family_id=? AND c.secret_hash=? AND c.revoked_ms>0 AND f.server_id=? AND f.command_member_incarnation=? AND f.command_profile_lifetime=? AND f.command_incarnation=? AND f.current_generation=? AND f.command_last_sequence=? AND f.command_acknowledged_sequence=? AND f.account_id=c.account_id AND f.profile_id=c.profile_id AND f.authority='local' AND f.security_kind=1 AND f.revoked=1 AND NOT EXISTS(SELECT 1 FROM api_credentials AS child INDEXED BY api_credentials_parent WHERE child.parent_id=c.id)`, Params: []stmt.Param{t("credential"), t("kind"), t("account"), t("profile"), t("family"), t("hash"), t("server"), t("member"), t("lifetime"), b("incarnation"), i("generation"), i("last"), i("ack")}},
		{ID: usSecretDrainBytes, SQL: `SELECT CASE WHEN kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND family_id=? AND family_incarnation=? AND secret_hash=? THEN variable_bytes ELSE -1 END FROM security_credential_secrets INDEXED BY sqlite_autoindex_security_credential_secrets_1 WHERE credential_id=?`, Params: []stmt.Param{t("kind"), t("account"), t("member"), t("profile"), t("lifetime"), t("family"), b("incarnation"), t("hash"), t("credential")}},
		{ID: usSecretDrainDelete, Kind: stmt.Write, SQL: `DELETE FROM security_credential_secrets INDEXED BY sqlite_autoindex_security_credential_secrets_1 WHERE credential_id=? AND kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND family_id=? AND family_incarnation=? AND secret_hash=? AND variable_bytes=? RETURNING variable_bytes`, Params: []stmt.Param{t("credential"), t("kind"), t("account"), t("member"), t("profile"), t("lifetime"), t("family"), b("incarnation"), t("hash"), i("bytes")}},
		{ID: usSecretDrainAbsent, SQL: `SELECT 1 FROM security_credential_secrets INDEXED BY sqlite_autoindex_security_credential_secrets_1 WHERE credential_id=?`, Params: []stmt.Param{t("credential")}},
	}
	pin := func(table, name string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	}
	secretPin := pin("security_credential_secrets", "sqlite_autoindex_security_credential_secrets_1")
	secretPin.Columns[0].Name = "credential_id"
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US private credential secret retirement"
		defs[n].CallSites = []string{"store/writer/us_credential_secret_drain.go"}
		if defs[n].ID == usSecretDrainSource {
			defs[n].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_api_credentials_1", "sqlite_autoindex_authorization_session_families_1", "api_credentials_parent"}, Seeks: []string{"id=?", "parent_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{pin("api_credentials", "sqlite_autoindex_api_credentials_1"), pin("authorization_session_families", "sqlite_autoindex_authorization_session_families_1")}}
		} else {
			defs[n].Plan = stmt.Plan{Hot: true, Indexes: []string{secretPin.Name}, Seeks: []string{"credential_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{secretPin}}
		}
	}
	return defs
}
