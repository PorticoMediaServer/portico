package plan

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed octet points for US-09b's PIN-owned copy of the 541 caller-snapshot
// loader. The SQL is byte-identical to the reviewed 541 preflight so the two
// loaders cannot drift; the IDs are PIN-owned and registered here, not by
// widening the policy scope. Each point returns INTEGER octet/count facts only;
// the loader charges the funded frame grant before any TEXT/BLOB Scan, JSON
// decode or DTO allocation.
func init() {
	account := usPolicyImageOctetShape{
		params:  []string{"account"},
		indexes: []string{"sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"},
		seeks:   []string{"id=?", "account_id=?"},
		pins: []stmt.AutoIndexBinding{
			usPolicyImagePin("accounts", "sqlite_autoindex_accounts_1", "pk", "id"),
			usPolicyImagePin("direct_memberships", "sqlite_autoindex_direct_memberships_1", "pk", "account_id"),
		},
		tables: []string{"accounts", "direct_memberships"},
		point:  usPolicyImagePoint("sqlite_autoindex_accounts_1", "sqlite_autoindex_direct_memberships_1"),
	}
	family := usPolicyImageOctetShape{
		params:  []string{"family", "account", "profile"},
		indexes: []string{"sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"},
		seeks:   []string{"id=?", "account_id=? AND id=?"},
		pins: []stmt.AutoIndexBinding{
			usPolicyImagePin("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "pk", "id"),
			usPolicyImagePin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id"),
		},
		tables: []string{"authorization_session_families", "direct_profiles"},
		point:  usPolicyImagePoint("sqlite_autoindex_authorization_session_families_1", "sqlite_autoindex_direct_profiles_2"),
	}
	policy := usPolicyImageOctetShape{
		primaryKeys: []string{"p", "s"},
		seeks:       []string{"rowid=?"},
		tables:      []string{"identity_sign_in_policy", "identity_state"},
		point:       usPolicyImagePoint(),
	}
	hosted := usPolicyImageOctetShape{
		params:  []string{"account"},
		indexes: []string{"sqlite_autoindex_account_portico_links_1"},
		seeks:   []string{"account_id=?"},
		pins: []stmt.AutoIndexBinding{
			usPolicyImagePin("account_portico_links", "sqlite_autoindex_account_portico_links_1", "pk", "account_id"),
		},
		tables: []string{"account_portico_links"},
		point:  usPolicyImagePoint("sqlite_autoindex_account_portico_links_1"),
	}
	custodian := usPolicyImageOctetShape{
		primaryKeys: []string{"hosted_membership_sync"},
		seeks:       []string{"rowid=?"},
		tables:      []string{"hosted_membership_sync"},
		point:       usPolicyImagePoint(),
	}
	profiles := usPolicyImageOctetShape{
		params:   []string{"account", "row_limit"},
		indexes:  []string{"direct_profiles_account"},
		seeks:    []string{"account_id=? AND deleted=?"},
		tables:   []string{"direct_profiles"},
		profiles: true,
		point:    usPolicyImageProfilesPoint,
	}
	for id, entry := range map[stmt.ID]struct {
		sql   string
		shape usPolicyImageOctetShape
	}{
		"us.pin.snapshot.account_octets":   {usPolicyImageAccountOctetsSQL, account},
		"us.pin.snapshot.family_octets":    {usPolicyImageFamilyOctetsSQL, family},
		"us.pin.snapshot.policy_octets":    {usPolicyImagePolicyOctetsSQL, policy},
		"us.pin.snapshot.hosted_octets":    {usPolicyImageHostedOctetsSQL, hosted},
		"us.pin.snapshot.custodian_octets": {usPolicyImageCustodianOctetsSQL, custodian},
		"us.pin.snapshot.profiles_octets":  {usPolicyImageProfilesOctetsSQL, profiles},
	} {
		shape := entry.shape
		sqlText := entry.sql
		pointOctets.add(octetShape{
			id:       id,
			sql:      sqlText,
			mismatch: "US PIN snapshot preflight requires the exact closed 541 octet definition",
			definition: func(d stmt.Definition) bool {
				return usPolicyImageOctetDefinition(d, sqlText, shape)
			},
			audit: func(ctx context.Context, conn *sql.Conn) error {
				return auditUSPolicyImageOctet(ctx, conn, shape)
			},
			point: shape.point,
		})
	}
}
