package writer

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usFamilyBirthCredentialMarker stmt.ID = "us.family.birth.credential_marker"
	usFamilyBirthCredentialAbsent stmt.ID = "us.family.birth.credential_absent"
	usFamilyBirthAPIDevice        stmt.ID = "us.family.birth.api_device"
	usFamilyBirthOriginLineage    stmt.ID = "us.family.birth.origin_lineage"
	usFamilyBirthPrincipal        stmt.ID = "us.family.birth.principal"
	usFamilyBirthServer           stmt.ID = "us.family.birth.server"
	usFamilyBirthState            stmt.ID = "us.family.birth.state"
	usFamilyBirthDevice           stmt.ID = "us.family.birth.device"
	usFamilyBirthInstallation     stmt.ID = "us.family.birth.installation"
	usFamilyBirthLineage          stmt.ID = "us.family.birth.lineage"
	usFamilyBirthNotWaiting       stmt.ID = "us.family.birth.not_waiting"
	usFamilyBirthFence            stmt.ID = "us.family.birth.fence"
	usFamilyBirthOrdinary         stmt.ID = "us.family.birth.ordinary"
	usFamilyBirthCredential       stmt.ID = "us.family.birth.credential"
	usFamilyBirthWitness          stmt.ID = "us.family.birth.witness"
)

func usFamilyBirthDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: usFamilyBirthPrincipal, SQL: `SELECT m.role,CASE WHEN p.pin_hash IS NULL THEN 0 ELSE 1 END FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1 CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id WHERE a.id=? AND a.epoch=? AND a.must_change=0 AND m.incarnation=? AND m.disabled=0 AND p.id=? AND p.lifetime=? AND p.deleted=0`, Params: []stmt.Param{t("account"), i("epoch"), t("member"), t("profile"), t("lifetime")}},
		{ID: usFamilyBirthServer, SQL: `SELECT 1 FROM configuration INDEXED BY sqlite_autoindex_configuration_1 WHERE key='id' AND value=?`, Params: []stmt.Param{t("server")}},
		{ID: usFamilyBirthState, SQL: `SELECT 1 FROM identity_state WHERE singleton=1 AND sign_in_kind='local' AND authority_generation=? AND transition_id IS NULL`, Params: []stmt.Param{i("generation")}},
		{ID: usFamilyBirthDevice, SQL: `SELECT 1 FROM identity_devices INDEXED BY sqlite_autoindex_identity_devices_1 WHERE id=? AND authority='local' AND account_id=? AND installation_id=? AND retired_ms=0`, Params: []stmt.Param{t("device"), t("account"), t("installation")}},
		{ID: usFamilyBirthInstallation, SQL: `SELECT 1 FROM identity_installations INDEXED BY sqlite_autoindex_identity_installations_1 WHERE installation_hash=? AND incarnation=? AND retired=0`, Params: []stmt.Param{t("hash"), t("incarnation")}},
		{ID: usFamilyBirthLineage, SQL: `SELECT 1 FROM identity_pin_lineages INDEXED BY sqlite_autoindex_identity_pin_lineages_1 WHERE id=? AND account_id=?`, Params: []stmt.Param{t("lineage"), t("account")}},
		{ID: usFamilyBirthNotWaiting, SQL: `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1 WHERE a.id=? AND NOT EXISTS(SELECT 1 FROM identity_move_waiting AS w INDEXED BY sqlite_autoindex_identity_move_waiting_1 WHERE w.account_id=a.id AND w.state='waiting')`, Params: []stmt.Param{t("account")}},
		{ID: usFamilyBirthFence, SQL: `SELECT id,generation FROM identity_security_fences INDEXED BY sqlite_autoindex_identity_security_fences_1 WHERE scope_kind=? AND account_id=? AND member_incarnation=? AND profile_id=? AND profile_lifetime=? AND device_id=? AND lineage_id=? AND denied=0`, Params: []stmt.Param{t("scope"), t("account"), t("member"), t("profile"), t("lifetime"), t("device"), t("lineage")}},
		{ID: usFamilyBirthOrdinary, Kind: stmt.Write, SQL: `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,pin_gate,device_id,pin_lineage_id,authority_generation,command_member_incarnation,command_profile_lifetime,command_incarnation,security_kind) VALUES(?,?,?,?,'local',?,?,?,1,?,?,?,?,?,?,?,0) RETURNING control_variable_bytes,pin_gate`, Params: []stmt.Param{t("family"), t("server"), t("account"), t("profile"), t("role"), i("epoch"), t("horizon"), i("pin_gate"), t("device"), t("lineage"), i("authority_generation"), t("member"), t("lifetime"), b("incarnation")}},
		{ID: usFamilyBirthWitness, Kind: stmt.Write, SQL: `INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES(?,?,?,?) RETURNING fence_id,confirmed_generation`, Params: []stmt.Param{t("family"), t("scope"), i("fence"), i("generation")}},
	}
	credential := defs[8]
	credential.ID = usFamilyBirthCredential
	credential.SQL = strings.Replace(credential.SQL, ",0) RETURNING", ",1) RETURNING", 1)
	defs = append(defs, credential)
	defs = append(defs,
		stmt.Definition{ID: usFamilyBirthCredentialMarker, SQL: `SELECT 1 FROM api_credentials INDEXED BY sqlite_autoindex_api_credentials_1 WHERE id=? AND kind=? AND account_id=? AND profile_id=? AND family_id=? AND device_id=? AND secret_hash=? AND name=? AND scopes=? AND hint=? AND expires_ms=? AND revoked_ms=0 AND parent_id IS NULL AND secret=''`, Params: []stmt.Param{t("id"), t("kind"), t("account"), t("profile"), t("family"), t("device"), t("hash"), t("name"), t("scopes"), t("hint"), i("expires")}},
		stmt.Definition{ID: usFamilyBirthCredentialAbsent, SQL: `SELECT 1 FROM api_credentials INDEXED BY sqlite_autoindex_api_credentials_3 WHERE family_id=?`, Params: []stmt.Param{t("family")}},
		stmt.Definition{ID: usFamilyBirthOriginLineage, SQL: `SELECT 1 FROM identity_pin_lineages INDEXED BY sqlite_autoindex_identity_pin_lineages_1 WHERE id=? AND account_id=? AND origin_device_id=?`, Params: []stmt.Param{t("lineage"), t("account"), t("device")}},
		stmt.Definition{ID: usFamilyBirthAPIDevice, Kind: stmt.Write, SQL: `INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen,remember_account,retired_ms) VALUES(?,'local',?,?,?,'api',?,'',?,?,0,0) RETURNING retired_ms`, Params: []stmt.Param{t("device"), t("account"), t("installation"), t("name"), t("app"), t("first"), t("last")}},
	)
	for n := range defs {
		if defs[n].Kind == "" {
			defs[n].Kind = stmt.Read
		}
		defs[n].Owner = "store/writer US private family birth"
		defs[n].CallSites = []string{"store/writer/us_family_birth.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
	}
	return defs
}
