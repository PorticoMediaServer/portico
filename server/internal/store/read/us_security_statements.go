package read

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Inert compiled definitions for the bounded US security read surface. They
// become executable only after HN Capture on an actual GV reader and explicit
// harness composition. No product registration, writer or mutation ID appears
// here. Every statement is a point or a bounded keyset page over a named native
// key; none scans, sorts in a temporary B-tree, or follows a caller-supplied
// expression.
const (
	usSecuritySession        = "read.us.security.session"
	usSecurityFence          = "read.us.security.fence"
	usSecurityStatus         = "read.us.security.status"
	usSecurityStatusOctets   = "read.us.security.status_octets"
	usSecurityMembers        = "read.us.security.members"
	usSecurityMembersOctets  = "read.us.security.members_octets"
	usSecurityMember         = "read.us.security.member"
	usSecurityMemberOctets   = "read.us.security.member_octets"
	usSecurityProfiles       = "read.us.security.profiles"
	usSecurityProfilesOctets = "read.us.security.profiles_octets"
	usSecurityDevices        = "read.us.security.devices"
	usSecurityDevicesOctets  = "read.us.security.devices_octets"
)

const usSecurityOwner = "store/read US security read surface"

// The current-source witness contains only fixed-width identity, lifetime,
// role, epoch, generation, gate and presence facts. Variable profile/device
// payload is fetched separately after the authority and fence checks. The four
// absence joins reject a narrow grant, an API-credential family, a reset-file
// session and a waiting move without a second statement or a family scan.
const usSecuritySessionSQL = `SELECT t.generation,t.expires_at,f.id,f.server_id,f.account_id,f.profile_id,f.role,f.epoch,f.authority_generation,f.authorization_horizon,f.remember_until,f.manage_until,f.pin_gate,f.pin_lineage_id,f.security_kind,a.epoch,a.must_change,a.profile_id,m.role,m.incarnation,m.disabled,p.lifetime,p.deleted,p.is_primary,length(p.pin_hash),d.id,d.account_id,d.installation_id,d.retired_ms
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
CROSS JOIN identity_state AS st INDEXED BY fk_child_identity_state_0 ON st.singleton=? AND st.transition_id IS NULL
CROSS JOIN configuration AS cfg INDEXED BY sqlite_autoindex_configuration_1 ON cfg.key=?
LEFT JOIN identity_installations AS i INDEXED BY sqlite_autoindex_identity_installations_1 ON i.installation_hash=?
LEFT JOIN authorization_family_grants AS g INDEXED BY sqlite_autoindex_authorization_family_grants_1 ON g.family_id=f.id
LEFT JOIN api_credentials AS c INDEXED BY sqlite_autoindex_api_credentials_3 ON c.family_id=f.id
LEFT JOIN reset_file_sessions AS r INDEXED BY sqlite_autoindex_reset_file_sessions_1 ON r.family_id=f.id
LEFT JOIN identity_move_waiting AS w INDEXED BY sqlite_autoindex_identity_move_waiting_1 ON w.account_id=f.account_id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0
AND f.authority='local' AND f.security_kind=0 AND d.authority='local' AND d.retired_ms=0
AND d.account_id=f.account_id AND d.installation_id=? AND m.role=f.role
AND a.must_change=0 AND st.sign_in_kind='local' AND st.authority_generation=f.authority_generation
AND cfg.value=f.server_id AND i.installation_hash IS NOT NULL AND i.retired=0
AND g.family_id IS NULL AND c.family_id IS NULL AND r.family_id IS NULL AND coalesce(w.state,'')<>'waiting'`

// One sparse lifetime-bound witness per closed scope kind. The plan matches the
// merged US-01 native fence point: family PK then fence integer PK. A missing
// row, a denied row or a stale confirmed generation is never a grant.
const usSecurityFenceSQL = `SELECT w.confirmed_generation,s.generation
FROM authorization_family_security_fences AS w
CROSS JOIN identity_security_fences AS s ON s.id=w.fence_id AND s.scope_kind=w.scope_kind
WHERE w.family_id=? AND w.scope_kind=? AND s.account_id=? AND s.member_incarnation=? AND s.profile_id=? AND s.profile_lifetime=? AND s.device_id=? AND s.lineage_id=? AND s.denied=0`

// Status payload: the caller's own current family, member, profile and device.
// No token digest, installation id, PIN hash, grant or credential column is
// selected; PIN presence is one scalar length and is never the stored hash.
const usSecurityStatusSQL = `SELECT f.id,f.server_id,f.account_id,f.profile_id,f.role,f.epoch,f.authority_generation,f.authorization_horizon,f.remember_until,f.manage_until,t.generation,t.expires_at,p.name,p.is_primary,p.position,p.art,p.revision,p.pin_revision,length(p.pin_hash),p.allowed_libraries,d.id,a.username,d.name,d.platform,d.app,d.app_version,d.first_seen,d.last_seen,d.last_profile_id,d.remember_account,d.revision
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0
AND f.authority='local' AND f.security_kind=0 AND d.authority='local' AND d.account_id=a.id
AND d.installation_id=? AND d.retired_ms=0 AND p.deleted=0`

const usSecurityStatusOctetsSQL = `SELECT octet_length(f.id),octet_length(f.server_id),octet_length(f.account_id),octet_length(f.profile_id),octet_length(f.role),octet_length(f.authorization_horizon),octet_length(f.remember_until),octet_length(f.manage_until),octet_length(t.expires_at),octet_length(p.name),octet_length(p.art),octet_length(p.allowed_libraries),octet_length(d.id),octet_length(a.username),octet_length(d.name),octet_length(d.platform),octet_length(d.app),octet_length(d.app_version),octet_length(d.first_seen),octet_length(d.last_seen),octet_length(d.last_profile_id)
FROM authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=f.account_id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.id=f.profile_id AND p.account_id=a.id
CROSS JOIN identity_devices AS d INDEXED BY sqlite_autoindex_identity_devices_1 ON d.id=f.device_id
WHERE t.token_hash=? AND t.retired=0 AND t.generation=f.current_generation AND f.revoked=0
AND f.authority='local' AND f.security_kind=0 AND d.authority='local' AND d.account_id=a.id
AND d.installation_id=? AND d.retired_ms=0 AND p.deleted=0`

// Owner administration member point/list. The only variable fields are the
// account and membership display/policy facts; limits and Hosted details are
// named refusals until their producers (US-22/US-25) have merged commands.
const usSecurityMemberSQL = `SELECT a.id,a.username,a.email,a.display_name,a.profile_id,m.role,m.disabled,m.disabled_ms,m.disabled_reason,m.revision,m.allowed_libraries,m.notes,m.joined_via,m.joined_ms
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`

const usSecurityMemberOctetsSQL = `SELECT octet_length(a.id),octet_length(a.username),octet_length(a.email),octet_length(a.display_name),octet_length(a.profile_id),octet_length(m.role),octet_length(m.disabled_reason),octet_length(m.allowed_libraries),octet_length(m.notes),octet_length(m.joined_via)
FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id=?`

const usSecurityMembersSQL = `SELECT a.id,a.username,a.email,a.display_name,a.profile_id,m.role,m.disabled,m.disabled_ms,m.disabled_reason,m.revision,m.allowed_libraries,m.notes,m.joined_via,m.joined_ms
FROM accounts AS a
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id>? ORDER BY a.id LIMIT ?`

const usSecurityMembersOctetsSQL = `SELECT octet_length(a.id),octet_length(a.username),octet_length(a.email),octet_length(a.display_name),octet_length(a.profile_id),octet_length(m.role),octet_length(m.disabled_reason),octet_length(m.allowed_libraries),octet_length(m.notes),octet_length(m.joined_via)
FROM accounts AS a
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
WHERE a.id>? ORDER BY a.id LIMIT ?`

// One account's live profiles in native position,id order. NULL allowed
// libraries stay a policy inheritance fact at the source; the DTO normalizes
// them to an empty slice exactly as the public profile read does.
const usSecurityProfilesSQL = `SELECT p.id,p.name,p.is_primary,p.position,p.art,p.revision,p.pin_revision,length(p.pin_hash),p.allowed_libraries
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`

const usSecurityProfilesOctetsSQL = `SELECT octet_length(p.id),octet_length(p.name),octet_length(p.art),octet_length(p.allowed_libraries)
FROM direct_profiles AS p INDEXED BY direct_profiles_account
WHERE p.account_id=? AND p.deleted=0 ORDER BY p.position,p.id LIMIT ?`

// A complete bounded owner device page for one account. The official people
// activity index supplies account/authority then platform and last-seen order,
// so no temporary sort is needed. Address, installation id and session counts
// are deliberately absent.
const usSecurityDevicesSQL = `SELECT d.id,d.account_id,a.username,d.last_profile_id,d.name,d.platform,d.app,d.app_version,d.first_seen,d.last_seen,d.revision
FROM identity_devices AS d INDEXED BY identity_devices_people_activity
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=d.account_id
WHERE d.account_id=? AND d.authority='local' ORDER BY d.platform,d.last_seen DESC LIMIT ?`

const usSecurityDevicesOctetsSQL = `SELECT octet_length(d.id),octet_length(d.account_id),octet_length(a.username),octet_length(d.last_profile_id),octet_length(d.name),octet_length(d.platform),octet_length(d.app),octet_length(d.app_version),octet_length(d.first_seen),octet_length(d.last_seen)
FROM identity_devices AS d INDEXED BY identity_devices_people_activity
CROSS JOIN accounts AS a INDEXED BY sqlite_autoindex_accounts_1 ON a.id=d.account_id
WHERE d.account_id=? AND d.authority='local' ORDER BY d.platform,d.last_seen DESC LIMIT ?`

func usSecurityKey(table, name, origin string, columns ...string) stmt.AutoIndexBinding {
	binding := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
	for _, column := range columns {
		binding.Columns = append(binding.Columns, stmt.IndexColumn{Name: column, Collation: "BINARY"})
	}
	return binding
}

func usSecurityPointPlan(indexes []string, seeks []string, bindings ...stmt.AutoIndexBinding) stmt.Plan {
	return stmt.Plan{Hot: true, Indexes: indexes, Seeks: seeks, AutoIndexes: bindings}
}

func usSecurityText(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
func usSecurityInt(name string) stmt.Param  { return stmt.Param{Name: name, Type: stmt.Integer} }
func usSecurityCohort(name string, values ...any) stmt.Cohort {
	return stmt.Cohort{Name: name, Bindings: values}
}

// usSecurityDefinitions is the complete inert statement set. Cohorts name a
// present native row and an absent one; they are Capture fixtures, never
// permissions. The US security reader is the only call site.
func usSecurityDefinitions() []stmt.Definition {
	tokenPK := usSecurityKey("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "pk", "token_hash")
	familyPK := usSecurityKey("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id")
	accountPK := usSecurityKey("accounts", "sqlite_autoindex_accounts_1", "pk", "id")
	memberPK := usSecurityKey("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id")
	profileKey := usSecurityKey("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id")
	devicePK := usSecurityKey("identity_devices", "sqlite_autoindex_identity_devices_1", "pk", "id")
	configPK := usSecurityKey("configuration", "sqlite_autoindex_configuration_1", "pk", "key")
	installPK := usSecurityKey("identity_installations", "sqlite_autoindex_identity_installations_1", "pk", "installation_hash")
	grantPK := usSecurityKey("authorization_family_grants", "sqlite_autoindex_authorization_family_grants_1", "pk", "family_id")
	credentialKey := usSecurityKey("api_credentials", "sqlite_autoindex_api_credentials_3", "u", "family_id")
	resetPK := usSecurityKey("reset_file_sessions", "sqlite_autoindex_reset_file_sessions_1", "pk", "family_id")
	movePK := usSecurityKey("identity_move_waiting", "sqlite_autoindex_identity_move_waiting_1", "pk", "account_id")

	sessionPlan := stmt.Plan{
		Hot: true,
		Indexes: []string{tokenPK.Name, familyPK.Name, accountPK.Name, memberPK.Name, profileKey.Name,
			devicePK.Name, "fk_child_identity_state_0", configPK.Name, installPK.Name, grantPK.Name, credentialKey.Name, resetPK.Name, movePK.Name},
		Seeks: []string{"token_hash=?", "id=?", "account_id=?", "account_id=? AND id=?", "transition_id=?", "rowid=?", "key=?",
			"installation_hash=?", "family_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{tokenPK, familyPK, accountPK, memberPK, profileKey, devicePK, configPK, installPK, grantPK, credentialKey, resetPK, movePK},
	}
	statusPlan := usSecurityPointPlan(
		[]string{tokenPK.Name, familyPK.Name, accountPK.Name, profileKey.Name, devicePK.Name},
		[]string{"token_hash=?", "id=?", "account_id=? AND id=?"},
		tokenPK, familyPK, accountPK, profileKey, devicePK)
	memberPlan := usSecurityPointPlan(
		[]string{accountPK.Name, memberPK.Name},
		[]string{"id=?", "account_id=?"},
		accountPK, memberPK)
	membersPlan := usSecurityPointPlan(
		[]string{accountPK.Name, memberPK.Name},
		[]string{"id>?", "account_id=?"},
		memberPK)
	profilesPlan := stmt.Plan{Hot: true, Indexes: []string{"direct_profiles_account"}, Seeks: []string{"account_id=? AND deleted=?"}}
	devicesPlan := stmt.Plan{
		Hot: true, Indexes: []string{"identity_devices_people_activity", accountPK.Name},
		Seeks:       []string{"account_id=? AND authority=?", "id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{accountPK},
	}
	fencePlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"w", "s"}, Seeks: []string{"family_id=? AND scope_kind=?", "rowid=?"}}

	installation := strings.Repeat("i", 32)
	member := strings.Repeat("a", 32)
	defs := []stmt.Definition{
		{ID: usSecuritySession, SQL: usSecuritySessionSQL,
			Params: []stmt.Param{usSecurityInt("singleton"), usSecurityText("configuration_key"), usSecurityText("installation_hash"), usSecurityText("token"), usSecurityText("installation")},
			Cohorts: []stmt.Cohort{
				usSecurityCohort("present", int64(1), "id", strings.Repeat("f", 64), "token", installation),
				usSecurityCohort("empty", int64(1), "id", strings.Repeat("f", 64), "missing", installation),
			}, Plan: sessionPlan},
		{ID: usSecurityFence, SQL: usSecurityFenceSQL,
			Params: []stmt.Param{usSecurityText("family"), usSecurityText("scope_kind"), usSecurityText("account"), usSecurityText("member_incarnation"), usSecurityText("profile"), usSecurityText("profile_lifetime"), usSecurityText("device"), usSecurityText("lineage")},
			Cohorts: []stmt.Cohort{
				usSecurityCohort("present", "f", "device", "a", member, "", "", "d", ""),
				usSecurityCohort("empty", "missing", "device", "a", member, "", "", "d", ""),
			}, Plan: fencePlan},
		{ID: usSecurityStatus, SQL: usSecurityStatusSQL,
			Params:  []stmt.Param{usSecurityText("token"), usSecurityText("installation")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "token", installation), usSecurityCohort("empty", "missing", installation)},
			Plan:    statusPlan},
		{ID: usSecurityStatusOctets, SQL: usSecurityStatusOctetsSQL,
			Params:  []stmt.Param{usSecurityText("token"), usSecurityText("installation")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "token", installation), usSecurityCohort("empty", "missing", installation)},
			Plan:    statusPlan},
		{ID: usSecurityMember, SQL: usSecurityMemberSQL,
			Params:  []stmt.Param{usSecurityText("account")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a"), usSecurityCohort("empty", "missing")},
			Plan:    memberPlan},
		{ID: usSecurityMemberOctets, SQL: usSecurityMemberOctetsSQL,
			Params:  []stmt.Param{usSecurityText("account")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a"), usSecurityCohort("empty", "missing")},
			Plan:    memberPlan},
		{ID: usSecurityMembers, SQL: usSecurityMembersSQL,
			Params:  []stmt.Param{usSecurityText("after"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "", int64(2)), usSecurityCohort("empty", "zzz", int64(2))},
			Plan:    membersPlan},
		{ID: usSecurityMembersOctets, SQL: usSecurityMembersOctetsSQL,
			Params:  []stmt.Param{usSecurityText("after"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "", int64(2)), usSecurityCohort("empty", "zzz", int64(2))},
			Plan:    membersPlan},
		{ID: usSecurityProfiles, SQL: usSecurityProfilesSQL,
			Params:  []stmt.Param{usSecurityText("account"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a", int64(usSecurityProfileMax+1)), usSecurityCohort("empty", "missing", int64(usSecurityProfileMax+1))},
			Plan:    profilesPlan},
		{ID: usSecurityProfilesOctets, SQL: usSecurityProfilesOctetsSQL,
			Params:  []stmt.Param{usSecurityText("account"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a", int64(usSecurityProfileMax+1)), usSecurityCohort("empty", "missing", int64(usSecurityProfileMax+1))},
			Plan:    profilesPlan},
		{ID: usSecurityDevices, SQL: usSecurityDevicesSQL,
			Params:  []stmt.Param{usSecurityText("account"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a", int64(usSecurityDeviceMax+1)), usSecurityCohort("empty", "missing", int64(usSecurityDeviceMax+1))},
			Plan:    devicesPlan},
		{ID: usSecurityDevicesOctets, SQL: usSecurityDevicesOctetsSQL,
			Params:  []stmt.Param{usSecurityText("account"), usSecurityInt("limit")},
			Cohorts: []stmt.Cohort{usSecurityCohort("present", "a", int64(usSecurityDeviceMax+1)), usSecurityCohort("empty", "missing", int64(usSecurityDeviceMax+1))},
			Plan:    devicesPlan},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = usSecurityOwner
		defs[n].CallSites = []string{"store/read/us_security.go:USSecurityReader"}
	}
	return defs
}

// USSecurityDefinitions is the inert US security recipe plus the shared
// principal fence it composes. Capture and register these on the actual reader
// before invoking it; production wiring remains out of scope.
func USSecurityDefinitions() []stmt.Definition {
	return append(browseDefinitions(), usSecurityDefinitions()...)
}
