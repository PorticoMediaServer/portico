package access

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

// Member is one account as administration sees it: its tier, its library allow
// list and the revision of its limits envelope, so a console can render the
// people page from one list.
type Member struct {
	ServerMaxStreams    *int   `json:"serverMaxStreams"`
	EffectiveMaxStreams *int   `json:"effectiveMaxStreams"`
	AccountID           string `json:"accountId"`
	Username            string `json:"username"`
	Email               string `json:"email,omitempty"`
	DisplayName         string `json:"displayName"`
	ProfileID           string `json:"profileId"`
	Role                string `json:"role"`
	Disabled            bool   `json:"disabled"`
	// DisabledAt and DisabledReason say when and why an account that is off
	// was turned off: "owner" (an owner or administrator did it) or "left"
	// (the person removed this server from their Portico Account).
	DisabledAt       string   `json:"disabledAt,omitempty"`
	DisabledReason   string   `json:"disabledReason,omitempty"`
	Revision         int64    `json:"revision"`
	AllowedLibraries []string `json:"allowedLibraries"`
	LimitsRevision   int64    `json:"limitsRevision"`
	Limits           Limits   `json:"limits"`
	// HostedAccountID marks a Portico Account member: no password here; its
	// password, email and second factor are managed at the Portico Account.
	HostedAccountID string   `json:"hostedAccountId,omitempty"`
	Notes           *string  `json:"notes,omitempty"`
	JoinedVia       string   `json:"joinedVia,omitempty"`
	JoinedAt        string   `json:"joinedAt,omitempty"`
	ProfileNames    []string `json:"profileNames"`
	LastActiveAt    string   `json:"lastActiveAt,omitempty"`
	LiveState       string   `json:"liveState"`
}

type MemberPage struct {
	Page
	Items        []MemberRow `json:"items"`
	TotalCount   int         `json:"totalCount"`
	MatchedCount int         `json:"matchedCount"`
}

// Members lists accounts newest username first by page. The caller's own tier is
// passed in so the list can mark which rows that caller may manage, which keeps
// the tier rule in identity.ManagesTier rather than in a client.
type MemberQuery struct {
	Search, Role, Status, Sort, Direction string
	Cursor                                string
	Limit                                 string
}

const memberColumns = `a.id,a.username,a.email,a.display_name,a.profile_id,m.role,m.disabled,m.disabled_ms,m.disabled_reason,m.revision,m.allowed_libraries,
 COALESCE(l.revision,1),COALESCE(l.body,''),COALESCE(p.hosted_account_id,''),m.notes,m.joined_via,m.joined_ms,
 (SELECT json_group_array(name) FROM (SELECT name FROM direct_profiles WHERE account_id=a.id AND deleted=0 ORDER BY position,id)),
 COALESCE((SELECT max(last_seen) FROM identity_devices WHERE account_id=a.id AND authority='local'),''),
 CASE WHEN EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.account_id=a.id AND v.ended_ms=0 AND v.lease_expires_ms>unixepoch('subsec')*1000 AND v.kind NOT IN('music','audiobook','audio')) THEN 'watching'
 WHEN EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.account_id=a.id AND v.ended_ms=0 AND v.lease_expires_ms>unixepoch('subsec')*1000 AND v.kind IN('music','audiobook','audio')) THEN 'listening' ELSE 'idle' END,m.access_tokens,m.max_profiles`
const memberTables = `accounts a JOIN direct_memberships m ON m.account_id=a.id
 LEFT JOIN access_limits l ON l.account_id=a.id LEFT JOIN account_portico_links p ON p.account_id=a.id`

func scanMember(row interface{ Scan(...any) error }) (Member, error) {
	var m Member
	var libraries, body, profiles string
	var disabledMs, joinedMs int64
	var accessTokens bool
	var maxProfiles int
	if e := row.Scan(&m.AccountID, &m.Username, &m.Email, &m.DisplayName, &m.ProfileID, &m.Role, &m.Disabled, &disabledMs, &m.DisabledReason, &m.Revision, &libraries, &m.LimitsRevision, &body, &m.HostedAccountID, &m.Notes, &m.JoinedVia, &joinedMs, &profiles, &m.LastActiveAt, &m.LiveState, &accessTokens, &maxProfiles); e != nil {
		return m, e
	}
	m.DisabledAt = disabledAt(disabledMs)
	m.JoinedAt = disabledAt(joinedMs)
	if e := json.Unmarshal([]byte(profiles), &m.ProfileNames); e != nil {
		return m, e
	}
	if m.ProfileNames == nil {
		m.ProfileNames = []string{}
	}
	if e := json.Unmarshal([]byte(libraries), &m.AllowedLibraries); e != nil {
		return m, e
	}
	if m.AllowedLibraries == nil {
		m.AllowedLibraries = []string{}
	}
	m.Limits = DefaultLimits()
	if body != "" {
		if e := json.Unmarshal([]byte(body), &m.Limits); e != nil {
			return m, e
		}
	}
	m.Limits.AccessTokens = accessTokens
	m.Limits.MaxProfiles = maxProfiles
	m.Limits.MaximumAge = m.Limits.Ceiling()
	return m, nil
}

// Member reads one member as the list shows it, so a console can read again
// the record it is acting on without walking the list. An account that is not
// a member is sql.ErrNoRows.
func (s *Store) Member(ctx context.Context, auth Authorize, account string) (out Member, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = readMember(ctx, tx, account)
		return e
	})
	return
}

type RoleChange struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
	Role             string `json:"role"`
}

// SetRole moves an account between the admin and member tiers. Ownership is not
// reachable here: it moves only through TransferDirectOwnership, which proves
// the current owner's password.
//
// actor is the calling principal's tier. identity.ManagesTier decides both ends:
// the caller must out-rank the account's current tier and the tier it is moving
// to, so an admin can neither promote anyone to admin nor demote another admin.
// A tier change applies live: the account's sessions take the new tier in the
// same transaction, so a demoted administrator's devices lose administration
// at once and nobody is signed out.
func (s *Store) SetRole(ctx context.Context, auth Authorize, actor identity.Principal, account string, c RoleChange) (out Member, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	if !validOperationID(c.OperationID) {
		return out, invalid("operationId")
	}
	if c.Role != identity.TierAdmin && c.Role != identity.TierMember {
		return out, invalid("role")
	}
	if account == actor.AccountID {
		return out, identity.ErrForbidden
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var current string
		var revision int64
		if e := tx.QueryRowContext(ctx, `SELECT role,revision FROM direct_memberships WHERE account_id=?`, account).Scan(&current, &revision); e != nil {
			return e
		}
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		if !identity.ManagesTier(live, current) || !identity.ManagesTier(live, c.Role) || !identity.ManagesTier(actor.Role, current) || !identity.ManagesTier(actor.Role, c.Role) {
			return identity.ErrForbidden
		}
		scope := "role:" + account
		body, want, e := receipt(tx, scope, c.OperationID, c)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		if revision != c.ExpectedRevision {
			return &ConflictError{revision}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET role=?,revision=revision+1 WHERE account_id=?`, c.Role, account); e != nil {
			return e
		}
		// The account's sessions move to the new tier here, in the same
		// transaction, and stay signed in.
		if e = identity.RetierFamiliesTx(ctx, tx, account, c.Role); e != nil {
			return e
		}
		if out, e = readMember(ctx, tx, account); e != nil {
			return e
		}
		return saveReceipt(tx, scope, c.OperationID, want, out, s.now())
	})
	return
}

func readMember(ctx context.Context, tx *sql.Tx, account string) (Member, error) {
	out, e := scanMember(tx.QueryRowContext(ctx, `SELECT `+memberColumns+` FROM `+memberTables+` WHERE a.id=?`, account))
	if e != nil {
		return out, e
	}
	streams, e := streamlimits.ReadTx(ctx, tx, account)
	if e != nil {
		return out, e
	}
	out.Limits.MaxStreamsMode = streams.Mode
	out.Limits.MaxStreams = 0
	if streams.Limit != nil {
		out.Limits.MaxStreams = *streams.Limit
	}
	out.ServerMaxStreams = streams.ServerCeiling
	out.EffectiveMaxStreams = streams.EffectiveLimit
	return out, nil
}

// MemberNotesChange is revision-bound and never alters account identity.
type MemberNotesChange struct {
	ExpectedRevision int64   `json:"expectedRevision"`
	OperationID      string  `json:"operationId"`
	Notes            *string `json:"notes,omitempty"`
}

func (s *Store) SetMemberNotes(ctx context.Context, auth Authorize, actor identity.Principal, account string, q MemberNotesChange) (out Member, err error) {
	if !validID(account) || q.ExpectedRevision < 1 || !validOperationID(q.OperationID) {
		return out, invalid("accountId", "expectedRevision", "operationId")
	}
	notes, e := invitationNotes(q.Notes)
	if e != nil {
		return out, e
	}
	q.Notes = notes
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		current, e := readMember(ctx, tx, account)
		if e != nil {
			return e
		}
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		if !(live == identity.TierOwner && actor.Role == identity.TierOwner) && (!identity.ManagesTier(live, current.Role) || !identity.ManagesTier(actor.Role, current.Role)) {
			return identity.ErrForbidden
		}
		body, want, e := receipt(tx, "member-notes:"+account, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		if current.Revision != q.ExpectedRevision {
			return &ConflictError{current.Revision}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE direct_memberships SET notes=?,revision=revision+1 WHERE account_id=?`, notes, account); e != nil {
			return e
		}
		out, e = readMember(ctx, tx, account)
		if e != nil {
			return e
		}
		return saveReceipt(tx, "member-notes:"+account, q.OperationID, want, out, s.now())
	})
	return
}

// Role reads one account's stored tier inside an existing transaction. The HTTP
// authorization helpers use it so a token's claimed role is never the authority.
func Role(tx *sql.Tx, account string) (string, error) {
	var role string
	var disabled bool
	e := tx.QueryRow(`SELECT role,disabled FROM direct_memberships WHERE account_id=?`, account).Scan(&role, &disabled)
	if errors.Is(e, sql.ErrNoRows) {
		return "", identity.ErrUnauthorized
	}
	if e != nil {
		return "", e
	}
	if disabled {
		return "", identity.ErrUnauthorized
	}
	return role, nil
}

func disabledAt(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// PeopleDevice adds public display facts without exposing addresses or session identifiers.
type PeopleDevice struct {
	identity.MemberDevice
	ProfileName  string `json:"profileName,omitempty"`
	LastNetwork  string `json:"lastNetwork"`
	LiveState    string `json:"liveState"`
	SessionCount *int   `json:"sessionCount,omitempty"`
}
type PeopleDevicePage struct {
	Limit      int            `json:"limit"`
	NextCursor string         `json:"nextCursor,omitempty"`
	Items      []PeopleDevice `json:"items"`
}
type PeopleDeviceQuery struct{ Cursor, Limit, Account, Sort string }

const peopleDeviceColumns = `d.id,d.account_id,a.username,d.last_profile_id,d.name,d.platform,d.app,d.app_version,d.first_seen,d.last_seen,d.revision,COALESCE(p.name,''),d.ip,
 CASE WHEN EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.device_id=d.id AND v.ended_ms=0 AND v.lease_expires_ms>unixepoch('subsec')*1000 AND v.kind NOT IN('music','audiobook','audio')) THEN 'watching'
 WHEN EXISTS(SELECT 1 FROM playback_v1_sessions v WHERE v.device_id=d.id AND v.ended_ms=0 AND v.lease_expires_ms>unixepoch('subsec')*1000 AND v.kind IN('music','audiobook','audio')) THEN 'listening' ELSE 'idle' END,
 (SELECT count(*) FROM identity_device_families f JOIN authorization_session_families a ON a.id=f.family_id AND a.revoked=0 AND a.authorization_horizon>? WHERE f.device_id=d.id)`
const peopleDeviceTables = `identity_devices d JOIN accounts a ON a.id=d.account_id JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN direct_profiles p ON p.id=d.last_profile_id AND p.account_id=d.account_id AND p.deleted=0`

func scanPeopleDevice(row interface{ Scan(...any) error }, networks []netip.Prefix) (PeopleDevice, error) {
	var v PeopleDevice
	var ip string
	var sessions int
	e := row.Scan(&v.ID, &v.AccountID, &v.Username, &v.ProfileID, &v.Name, &v.Platform, &v.App, &v.AppVersion, &v.FirstSeen, &v.LastSeen, &v.Revision, &v.ProfileName, &ip, &v.LiveState, &sessions)
	if e == nil {
		v.SessionCount = &sessions
	}
	v.LastNetwork = "away"
	addr, _ := netip.ParseAddr(ip)
	if networkpolicy.Contains(networks, addr) {
		v.LastNetwork = "home"
	}
	return v, e
}
func (s *Store) PeopleDevices(ctx context.Context, auth Authorize, actor identity.Principal, q PeopleDeviceQuery, networks []netip.Prefix) (out PeopleDevicePage, err error) {
	limit, e := pageLimit(q.Limit)
	if e != nil {
		return out, e
	}
	out.Limit = limit
	out.Items = []PeopleDevice{}
	if q.Sort == "" {
		q.Sort = "lastSeen"
	}
	column := map[string]string{"lastSeen": "d.last_seen", "firstSeen": "d.first_seen", "name": "d.name"}[q.Sort]
	if column == "" {
		return out, invalid("sort")
	}
	if q.Account != "" && !validID(q.Account) {
		return out, invalid("account")
	}
	key, id := "", ""
	if q.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
		var c []string
		if e != nil || len(raw) > 1024 || json.Unmarshal(raw, &c) != nil || len(c) != 5 || c[0] != q.Sort || c[1] != q.Account || c[2] != actor.AccountID || !validID(c[4]) {
			return out, invalid("cursor")
		}
		key, id = c[3], c[4]
	}
	direction, comparison := "DESC", "<"
	if q.Sort == "name" {
		direction, comparison = "ASC", ">"
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		owner := live == identity.TierOwner && actor.Role == identity.TierOwner
		if !identity.Administrative(live) || !identity.Administrative(actor.Role) {
			return identity.ErrForbidden
		}
		query := `SELECT ` + peopleDeviceColumns + ` FROM ` + peopleDeviceTables + ` WHERE d.authority='local' AND (?='' OR d.account_id=?) AND (? OR (m.role='member' AND d.account_id<>?)) AND (?='' OR (` + column + `,d.id)` + comparison + `(?,?)) ORDER BY ` + column + ` ` + direction + `,d.id ` + direction + ` LIMIT ?`
		rows, e := tx.QueryContext(ctx, query, s.Now().UTC().Format(time.RFC3339), q.Account, q.Account, owner, actor.AccountID, id, key, id, limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v, e := scanPeopleDevice(rows, networks)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return rows.Err()
	})
	if err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		v := out.Items[limit-1]
		key = v.LastSeen
		if q.Sort == "firstSeen" {
			key = v.FirstSeen
		}
		if q.Sort == "name" {
			key = v.Name
		}
		raw, _ := json.Marshal([]string{q.Sort, q.Account, actor.AccountID, key, v.ID})
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return
}
func (s *Store) PeopleDevice(ctx context.Context, auth Authorize, actor identity.Principal, id string, networks []netip.Prefix) (out PeopleDevice, err error) {
	if !validID(id) {
		return out, invalid("id")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		live, e := Role(tx, actor.AccountID)
		if e != nil {
			return e
		}
		if !identity.Administrative(live) || !identity.Administrative(actor.Role) {
			return identity.ErrForbidden
		}
		owner := live == identity.TierOwner && actor.Role == identity.TierOwner
		out, e = scanPeopleDevice(tx.QueryRowContext(ctx, `SELECT `+peopleDeviceColumns+` FROM `+peopleDeviceTables+` WHERE d.id=? AND d.authority='local' AND (? OR (m.role='member' AND d.account_id<>?))`, s.Now().UTC().Format(time.RFC3339), id, owner, actor.AccountID), networks)
		return e
	})
	return
}

// MemberProfiles reads another person's profiles for People. auth proves, in
// the read, that the caller's tier manages that person's.
func (s *Store) MemberProfiles(ctx context.Context, auth Authorize, account string) (out identity.MemberProfiles, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = identity.MemberProfilesTx(ctx, tx, account)
		return e
	})
	return
}

// EditMemberProfile renames or recolours one of another person's profiles.
func (s *Store) EditMemberProfile(ctx context.Context, auth Authorize, account, profile string, q identity.MemberProfileEdit) (out identity.MemberProfile, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = identity.EditMemberProfileTx(ctx, tx, account, profile, q)
		return e
	})
	return
}

// SetMemberProfileRestrictions replaces one of another person's profile restrictions.
func (s *Store) SetMemberProfileRestrictions(ctx context.Context, auth Authorize, account, profile string, q identity.ProfileRestrictionEdit, ownerGrant bool) (out identity.MemberProfile, err error) {
	if !validID(account) {
		return out, invalid("accountId")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = identity.SetMemberProfileRestrictionsTx(ctx, tx, account, profile, q, ownerGrant)
		return e
	})
	return
}

// ClearMemberProfilePIN removes the PIN from one of another person's profiles.
func (s *Store) ClearMemberProfilePIN(ctx context.Context, auth Authorize, ids *identity.Service, account, profile string, q identity.MemberProfilePINReset) (out identity.MemberProfile, err error) {
	if !validID(account) || ids == nil {
		return out, invalid("accountId")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		var e error
		out, e = ids.ClearMemberProfilePINTx(ctx, tx, account, profile, q)
		return e
	})
	return
}

// AuthorizeMemberProfilePicture checks before reading an upload or decoding it.
func (s *Store) AuthorizeMemberProfilePicture(ctx context.Context, auth Authorize, account, profile string, revision int64) error {
	if !validID(account) {
		return invalid("accountId")
	}
	return s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		return identity.CheckMemberProfileRevisionTx(ctx, tx, account, profile, revision)
	})
}

func (s *Store) SetMemberProfilePicture(ctx context.Context, auth Authorize, ids *identity.Service, account, profile string, revision int64, picture *identity.PreparedProfileAvatar) (out identity.MemberProfile, err error) {
	if !validID(account) || ids == nil {
		return out, invalid("accountId")
	}
	// A picture is a user edit, not an authority fence. A transparent 512px
	// rendition can use a megabyte; replacement also collects the old renditions.
	ctx = dbwork.WithPlan(ctx, dbwork.WritePlan{Name: "member-profile-picture", Rows: 64, Pages: 1024, VMOps: 100000, Why: "The fixed renditions, profile revision and removal of superseded personal images publish atomically."})
	gated, err := dbwork.Begin(ctx, s.DB, dbwork.ClassInteractive)
	if err != nil {
		return out, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if auth != nil {
		if err = auth(ctx, tx); err != nil {
			return out, err
		}
	}
	out, err = ids.SetMemberProfilePictureTx(ctx, tx, account, profile, revision, picture)
	if err != nil {
		return out, err
	}
	return out, gated.Commit()
}

func (s *Store) MemberProfilePicture(ctx context.Context, auth Authorize, account, profile string, size int) (raw []byte, version int64, updated string, err error) {
	if !validID(account) {
		return nil, 0, "", invalid("accountId")
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var e error
		raw, version, updated, e = identity.ReadMemberProfilePictureTx(ctx, tx, account, profile, size)
		return e
	})
	return
}
