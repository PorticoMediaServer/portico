package plan

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Closed octet points for US-09's private PIN original-result image (the
// complete US-04 MemberProfile of the edited profile). Each point returns
// INTEGER octet/count facts only; the loader charges the funded frame grant from
// them before any TEXT/BLOB Scan, JSON decode or DTO allocation. The exact SQL,
// parameters and plan declarations are pinned here so a changed writer
// definition is a mismatch, never a silent reuse.
const (
	usPINImageProfileOctetsSQL = `SELECT octet_length(id),octet_length(name),octet_length(art),coalesce(octet_length(allowed_libraries),0),revision,pin_revision,length(pin_hash),is_primary,position,coalesce(length(avatar_digest),-1),avatar_version,octet_length(avatar_updated_at),octet_length(avatar_source_mime)
FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2
WHERE account_id=? AND id=? AND deleted=0`
	usPINImageRestrictionOctetsSQL = `SELECT octet_length(rating_system),octet_length(maximum_age_rating),octet_length(blocked_labels),maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision
FROM profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1
WHERE profile_id=?`
)

func init() {
	profile := usPolicyImageOctetShape{
		params:  []string{"account", "profile"},
		indexes: []string{"sqlite_autoindex_direct_profiles_2"},
		seeks:   []string{"account_id=? AND id=?"},
		pins:    []stmt.AutoIndexBinding{usPolicyImagePin("direct_profiles", "sqlite_autoindex_direct_profiles_2", "u", "account_id", "id")},
		tables:  []string{"direct_profiles"},
		point:   usPolicyImagePoint("sqlite_autoindex_direct_profiles_2"),
	}
	restrictions := usPolicyImageOctetShape{
		params:  []string{"profile"},
		indexes: []string{"sqlite_autoindex_profile_restrictions_1"},
		seeks:   []string{"profile_id=?"},
		pins:    []stmt.AutoIndexBinding{usPolicyImagePin("profile_restrictions", "sqlite_autoindex_profile_restrictions_1", "pk", "profile_id")},
		tables:  []string{"profile_restrictions"},
		point:   usPolicyImagePoint("sqlite_autoindex_profile_restrictions_1"),
	}
	for id, entry := range map[stmt.ID]struct {
		sql   string
		shape usPolicyImageOctetShape
	}{
		"us.pin.image.profile_octets":     {usPINImageProfileOctetsSQL, profile},
		"us.pin.image.restriction_octets": {usPINImageRestrictionOctetsSQL, restrictions},
	} {
		shape := entry.shape
		sqlText := entry.sql
		pointOctets.add(octetShape{
			id:       id,
			sql:      sqlText,
			mismatch: "US PIN image preflight requires the exact closed octet definition",
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
