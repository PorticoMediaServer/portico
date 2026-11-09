package read

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// The US security read surface is inert: bounded GV snapshots only, no writer,
// product registration or mutation ID. Authority is rechecked from CURRENT
// source in the lookup window immediately before any payload row is fetched and
// again in the fresh publication window before any value is returned. A
// principal can read only its own session/profile facts; owner administration
// reads are the sole cross-account surface. Nothing here mints a command,
// receipt, cache entry, 304 or bearer capability.
//
// Phase 4 retirement map: the identity/access/httpapi adapters replace their
// direct SQL for sessions, profiles, members and devices with these recipes.
// Until cutover this file stays uncalled and grants no product route.

const (
	usSecurityProfileMax = 8
	usSecurityDeviceMax  = 64
	// One retained page of authority-approved variable facts. A larger page is
	// refused, never truncated. The bound is far below the caller's separate
	// output grant and is charged before payload hydration.
	usSecurityPageBytes = 64 << 10
)

// Named refusals for read classes whose merged command producer is not landed.
// They are explicit capability statements, not authorization successes or
// fabricated empty pages. Product composition must surface them as "not
// available yet" rather than as an empty list.
var (
	ErrUSSecurityInvitationsUnbacked = errors.New("us security: invitation reads require merged US-12 through US-14 invitation commands")
	ErrUSSecuritySetupUnbacked       = errors.New("us security: setup and owner recovery reads require merged US-24 setup commands")
	ErrUSSecurityHostedUnbacked      = errors.New("us security: Hosted coordination reads require merged US-25 Hosted commands")
)

// The surface is selected by a private compiled class; a caller cannot choose a
// weaker authority recipe for the same payload.
type usSecurityClass uint8

const (
	usSecurityStatusRead usSecurityClass = iota + 1
	usSecurityProfilesRead
	usSecurityMembersRead
	usSecurityMemberRead
	usSecurityDevicesRead
)

type usSecurityPurpose struct {
	class        usSecurityClass
	installation string
	owner        bool
}

func (*usSecurityPurpose) readPolicyState() {}

const usSecurityPolicy readPolicyID = 32700

func init() {
	registerReadPolicy(readPolicyRecipe{
		id: usSecurityPolicy, domain: "us-security-read", kinds: []int64{1},
		stateful: true, scope: readPrincipalScope, validate: validateUSSecurityPolicy,
	})
}

// USSecurityInput carries the trusted harness's current hashed viewing token,
// clock and database incarnation in View, plus the client's raw installation
// identifier. The installation id is compared to CURRENT device source and its
// registered hash; it never appears in output, errors or logs. Account is used
// only by owner administration reads.
type USSecurityInput struct {
	View         BrowseInput
	Installation string
	Account      string
	After        *USSecurityCursor
}

// USSecurityCursor is one private member-list continuation. Its fence binds the
// issuing principal, account and authority witness; another session, a revoked
// token or a changed authority fact cannot reuse it. It is not a wire cursor.
type USSecurityCursor struct {
	fence   browseFence
	class   usSecurityClass
	account string
}

// USSecurityProfile is exactly the existing public DirectProfile response
// shape. AllowedLibraries is normalized to []string{} for a SQL NULL policy,
// matching the current profile read.
type USSecurityProfile struct {
	ID               string
	Name             string
	Primary          bool
	Position         int
	Art              string
	Revision         int64
	PINRevision      int64
	PINRequired      bool
	AllowedLibraries []string
}

// USSecurityDevice is the existing people-facing MemberDevice shape plus the
// self-session remembered-account fact. It deliberately has no installation id,
// address, token/family identifier or session count.
type USSecurityDevice struct {
	ID              string
	AccountID       string
	Username        string
	ProfileID       string
	Name            string
	Platform        string
	App             string
	AppVersion      string
	FirstSeen       string
	LastSeen        string
	Revision        int64
	RememberAccount bool
}

// USSecurityStatus is the caller's own current security status: the exact
// session family, main/profile boundary, device and PIN facts that authorize
// it. Revoked sessions, reset-file sessions, credential families, narrow grants
// and waiting moves are refused instead of described.
type USSecurityStatus struct {
	ServerID, AccountID, FamilyID, ProfileID, Role string
	Epoch, AuthorityGeneration, FamilyGeneration   int64
	Horizon, RememberUntil, ManageUntil, ExpiresAt string
	PINRequired, Remembered                        bool
	Profile                                        USSecurityProfile
	Device                                         USSecurityDevice
}

// USSecurityMember is the security-relevant subset of the existing owner/admin
// Member response that merged US commands maintain: identity, role, tier
// enablement, library policy and membership lifetime. Limits (US-22) and Hosted
// account details (US-25) are intentionally absent and are named refusals.
type USSecurityMember struct {
	AccountID        string
	Username         string
	Email            string
	DisplayName      string
	ProfileID        string
	Role             string
	Disabled         bool
	DisabledAt       string
	DisabledReason   string
	Revision         int64
	AllowedLibraries []string
	Notes            *string
	JoinedVia        string
	JoinedAt         string
}

type USSecurityMembersPage struct {
	Items     []USSecurityMember
	Exhausted bool
	Next      *USSecurityCursor
}

type USSecurityProfilesPage struct {
	AccountID string
	Items     []USSecurityProfile
	Exhausted bool
}

type USSecurityDevicesPage struct {
	AccountID string
	Items     []USSecurityDevice
	Exhausted bool
}

// Refusal-only result types keep unbacked classes explicit in composition.
type USSecurityInvitations struct{}
type USSecuritySetup struct{}
type USSecurityHosted struct{}

type USSecurityReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewUSSecurityReader(s *governor.Snapshots) (*USSecurityReader, error) {
	if s == nil {
		return nil, ErrInput
	}
	return &USSecurityReader{snapshots: s, pool: s}, nil
}

// Status returns the caller's own current session/device/profile security facts.
func (r *USSecurityReader) Status(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityStatus, BrowseReadResult, error) {
	if r == nil {
		return USSecurityStatus{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (USSecurityStatus, BrowseReadResult, error) {
		in.View.Now = at
		return r.status(ctx, request, budget, output, in)
	})
}

func (r *USSecurityReader) status(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityStatus, BrowseReadResult, error) {
	if r == nil || in.After != nil || in.Account != "" || !validUSSecurityView(in.View) || !validUSSecurityInstallation(in.Installation) {
		return USSecurityStatus{}, BrowseReadResult{}, ErrInput
	}
	s := &usSecuritySnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	view := usSecurityView(in.View, usSecurityStatusRead, in.Installation, false)
	steps := []readStep[USSecurityStatus]{func(ctx context.Context, q browseQuery, f browseFence, d *USSecurityStatus) error {
		row, err := s.point(usSecurityStatus, usSecurityStatusVariable, view.TokenHash, in.Installation)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		status, err := usSecurityStatusValue(row, f)
		if err != nil {
			return err
		}
		*d = status
		return ctx.Err()
	}}
	page, result, err := publishRead(ctx, s, request, budget, output, view, steps)
	result.LookupStatements += s.points
	return page, result, err
}

// Profiles returns the caller's own account profiles in native position,id
// order. The complete legal cohort is at most eight profiles; a ninth live row
// is refused rather than silently cut. No other account's profiles are visible.
func (r *USSecurityReader) Profiles(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityProfilesPage, BrowseReadResult, error) {
	if r == nil {
		return USSecurityProfilesPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (USSecurityProfilesPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.profiles(ctx, request, budget, output, in)
	})
}

func (r *USSecurityReader) profiles(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityProfilesPage, BrowseReadResult, error) {
	if r == nil || in.After != nil || in.Account != "" || !validUSSecurityView(in.View) || !validUSSecurityInstallation(in.Installation) {
		return USSecurityProfilesPage{}, BrowseReadResult{}, ErrInput
	}
	s := &usSecuritySnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	view := usSecurityView(in.View, usSecurityProfilesRead, in.Installation, false)
	steps := []readStep[USSecurityProfilesPage]{func(ctx context.Context, q browseQuery, f browseFence, d *USSecurityProfilesPage) error {
		points, err := q(usSecurityProfilesOctets, f.account, int64(usSecurityProfileMax+1))
		if err != nil {
			return err
		}
		if len(points) > usSecurityProfileMax {
			return ErrUnqualified
		}
		if err = usSecurityBytesBound(points); err != nil {
			return err
		}
		rows, err := q(usSecurityProfiles, f.account, int64(usSecurityProfileMax+1))
		if err != nil {
			return err
		}
		if len(rows) != len(points) {
			return ErrStale
		}
		profiles := make([]USSecurityProfile, 0, len(rows))
		for _, row := range rows {
			profile, err := usSecurityProfileValue(row)
			if err != nil {
				return err
			}
			profiles = append(profiles, profile)
		}
		*d = USSecurityProfilesPage{AccountID: f.account, Items: profiles, Exhausted: true}
		return ctx.Err()
	}}
	return publishRead(ctx, s, request, budget, output, view, steps)
}

// Members returns an owner-only account page ordered by account id. Page+1 is
// fetched; a full raw page continues at the exact issued authority fence. An
// administrator or member token is denied, not downgraded.
func (r *USSecurityReader) Members(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityMembersPage, BrowseReadResult, error) {
	if r == nil {
		return USSecurityMembersPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (USSecurityMembersPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.members(ctx, request, budget, output, in)
	})
}

func (r *USSecurityReader) members(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityMembersPage, BrowseReadResult, error) {
	if r == nil || in.Account != "" || !validUSSecurityView(in.View) || !validUSSecurityInstallation(in.Installation) {
		return USSecurityMembersPage{}, BrowseReadResult{}, ErrInput
	}
	s := &usSecuritySnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	view := usSecurityView(in.View, usSecurityMembersRead, in.Installation, true)
	steps := []readStep[USSecurityMembersPage]{func(ctx context.Context, q browseQuery, f browseFence, d *USSecurityMembersPage) error {
		after := ""
		if in.After != nil {
			if in.After.fence != f || in.After.class != usSecurityMembersRead {
				return ErrStale
			}
			after = in.After.account
		}
		limit := view.Limit
		points, err := q(usSecurityMembersOctets, after, int64(limit+1))
		if err != nil {
			return err
		}
		truncated := len(points) > limit
		if truncated {
			points = points[:limit]
		}
		if err = usSecurityBytesBound(points); err != nil {
			return err
		}
		rows, err := q(usSecurityMembers, after, int64(limit+1))
		if err != nil {
			return err
		}
		if (len(rows) > limit) != truncated || min(len(rows), limit) != len(points) {
			return ErrStale
		}
		if truncated {
			rows = rows[:limit]
		}
		members := make([]USSecurityMember, 0, len(rows))
		for _, row := range rows {
			member, err := usSecurityMemberValue(row)
			if err != nil {
				return err
			}
			members = append(members, member)
		}
		*d = USSecurityMembersPage{Items: members, Exhausted: !truncated}
		if truncated && len(members) > 0 {
			*d = USSecurityMembersPage{Items: members, Next: &USSecurityCursor{fence: f, class: usSecurityMembersRead, account: members[len(members)-1].AccountID}}
		}
		return ctx.Err()
	}}
	return publishRead(ctx, s, request, budget, output, view, steps)
}

// Member returns one account's owner-administration facts. A missing or
// non-member account is denied with a zero value; no existence oracle leaks.
func (r *USSecurityReader) Member(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityMember, BrowseReadResult, error) {
	if r == nil {
		return USSecurityMember{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (USSecurityMember, BrowseReadResult, error) {
		in.View.Now = at
		return r.member(ctx, request, budget, output, in)
	})
}

func (r *USSecurityReader) member(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityMember, BrowseReadResult, error) {
	if r == nil || in.After != nil || !validUSSecurityIdentifier(in.Account) || !validUSSecurityView(in.View) || !validUSSecurityInstallation(in.Installation) {
		return USSecurityMember{}, BrowseReadResult{}, ErrInput
	}
	s := &usSecuritySnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	view := usSecurityView(in.View, usSecurityMemberRead, in.Installation, true)
	steps := []readStep[USSecurityMember]{func(ctx context.Context, q browseQuery, f browseFence, d *USSecurityMember) error {
		points, err := q(usSecurityMemberOctets, in.Account)
		if err != nil {
			return err
		}
		if len(points) != 1 {
			return ErrDenied
		}
		if err = usSecurityBytesBound(points); err != nil {
			return err
		}
		rows, err := q(usSecurityMember, in.Account)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrDenied
		}
		member, err := usSecurityMemberValue(rows[0])
		if err != nil {
			return err
		}
		if member.AccountID != in.Account {
			return ErrStale
		}
		*d = member
		return ctx.Err()
	}}
	return publishRead(ctx, s, request, budget, output, view, steps)
}

// Devices returns one account's complete owner-only device page in people
// activity order. A 65th device is refused rather than truncated; the product
// device surface is bounded for a single household. Installation ids, addresses
// and session identifiers never appear.
func (r *USSecurityReader) Devices(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityDevicesPage, BrowseReadResult, error) {
	if r == nil {
		return USSecurityDevicesPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.View.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (USSecurityDevicesPage, BrowseReadResult, error) {
		in.View.Now = at
		return r.devices(ctx, request, budget, output, in)
	})
}

func (r *USSecurityReader) devices(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in USSecurityInput) (USSecurityDevicesPage, BrowseReadResult, error) {
	if r == nil || in.After != nil || !validUSSecurityIdentifier(in.Account) || !validUSSecurityView(in.View) || !validUSSecurityInstallation(in.Installation) {
		return USSecurityDevicesPage{}, BrowseReadResult{}, ErrInput
	}
	s := &usSecuritySnapshots{base: r.snapshots, copyLimit: output.Request.Need[governor.MemoryBytes] / 4}
	view := usSecurityView(in.View, usSecurityDevicesRead, in.Installation, true)
	steps := []readStep[USSecurityDevicesPage]{func(ctx context.Context, q browseQuery, f browseFence, d *USSecurityDevicesPage) error {
		points, err := q(usSecurityDevicesOctets, in.Account, int64(usSecurityDeviceMax+1))
		if err != nil {
			return err
		}
		if len(points) > usSecurityDeviceMax {
			return ErrUnqualified
		}
		if err = usSecurityBytesBound(points); err != nil {
			return err
		}
		rows, err := q(usSecurityDevices, in.Account, int64(usSecurityDeviceMax+1))
		if err != nil {
			return err
		}
		if len(rows) != len(points) {
			return ErrStale
		}
		devices := make([]USSecurityDevice, 0, len(rows))
		for _, row := range rows {
			device, err := usSecurityDeviceValue(row)
			if err != nil {
				return err
			}
			if device.AccountID != in.Account {
				return ErrStale
			}
			devices = append(devices, device)
		}
		*d = USSecurityDevicesPage{AccountID: in.Account, Items: devices, Exhausted: true}
		return ctx.Err()
	}}
	return publishRead(ctx, s, request, budget, output, view, steps)
}

// Invitations, Setup and Hosted are explicit refusals: their merged US-12..US-25
// command producers are not landed on this train, so no closed source recipe can
// back a truthful read. They return no page and mint no authority.
func (r *USSecurityReader) Invitations(context.Context, governor.Request, time.Duration, BrowseOutput) (USSecurityInvitations, BrowseReadResult, error) {
	return USSecurityInvitations{}, BrowseReadResult{}, ErrUSSecurityInvitationsUnbacked
}

func (r *USSecurityReader) Setup(context.Context, governor.Request, time.Duration, BrowseOutput) (USSecuritySetup, BrowseReadResult, error) {
	return USSecuritySetup{}, BrowseReadResult{}, ErrUSSecuritySetupUnbacked
}

func (r *USSecurityReader) Hosted(context.Context, governor.Request, time.Duration, BrowseOutput) (USSecurityHosted, BrowseReadResult, error) {
	return USSecurityHosted{}, BrowseReadResult{}, ErrUSSecurityHostedUnbacked
}

func usSecurityView(in BrowseInput, class usSecurityClass, installation string, owner bool) BrowseInput {
	view := in
	view.Library, view.Kind = 0, 1
	view.After, view.Descending, view.KeepArticles = nil, false, false
	view.policy = readPolicyBinding{recipe: usSecurityPolicy, state: &usSecurityPurpose{class: class, installation: installation, owner: owner}}
	return view
}

func validUSSecurityView(in BrowseInput) bool {
	return in.After == nil && !in.Descending && !in.KeepArticles && in.Library == 0
}

func validUSSecurityIdentifier(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

// Installation ids are random client secrets, never device fingerprints. The
// read requires the existing minimum grammar and bounded UTF-8 without control
// separators; the value is never copied into output or an error.
func validUSSecurityInstallation(value string) bool {
	return len(value) >= 32 && len(value) <= 128 && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n\t ")
}

func usSecurityInstallationHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// usSecurityAuthorityFacts are the owned current-source facts used only by the
// validator. Payload columns stay separate.
type usSecurityAuthorityFacts struct {
	tokenGeneration, authorityGeneration, epoch          int64
	expiresAt, horizon, rememberUntil, manageUntil       string
	family, serverID, account, profile, lifetime, role   string
	memberRole, memberIncarnation                        string
	deviceID, deviceAccount, installationID              string
	pinGate, securityKind, mustChange, disabled, deleted int64
	primary                                              int64
	accountProfile                                       string
	pinLength                                            sql.NullInt64
	lineage                                              string
}

// validateUSSecurityPolicy is the Tier A authority recheck. It runs after the
// common principal/birth fence in BOTH the lookup and fresh publication
// snapshots. Every branch fails closed; a missing authority row, a narrow grant,
// an API-credential family, a reset-file session, a waiting move, a PIN gate, a
// stale fence generation or an expired token/horizon is a denial.
func validateUSSecurityPolicy(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	purpose, ok := in.policy.state.(*usSecurityPurpose)
	if !ok || purpose == nil || in.Library != 0 || in.Kind != 1 || in.After != nil || in.Descending || in.KeepArticles || !validUSSecurityInstallation(purpose.installation) {
		return "", ErrInput
	}
	if !validUSSecurityIdentifier(in.TokenHash) || !validUSSecurityIdentifier(f.account) || !validUSSecurityIdentifier(f.profile) {
		return "", ErrDenied
	}
	facts, err := usSecurityAuthority(q, in.TokenHash, purpose.installation, purpose.owner, in.Now)
	if err != nil {
		return "", err
	}
	// The common fence and this witness are two statements over one snapshot.
	// Refuse a torn mix rather than trusting either half alone.
	if f.account != facts.account || f.profile != facts.profile || f.lifetime != facts.lifetime || f.role != facts.role ||
		f.epoch != facts.epoch || f.authority != facts.authorityGeneration || f.token != in.TokenHash ||
		f.expiresAt != facts.expiresAt || f.horizon != facts.horizon {
		return "", ErrDenied
	}
	return usSecurityWitness(facts, purpose.owner), nil
}

func usSecurityAuthority(q browseQuery, token, installation string, owner bool, now time.Time) (usSecurityAuthorityFacts, error) {
	rows, err := q(usSecuritySession, int64(1), "id", usSecurityInstallationHash(installation), token, installation)
	if err != nil {
		return usSecurityAuthorityFacts{}, err
	}
	if len(rows) != 1 {
		return usSecurityAuthorityFacts{}, ErrDenied
	}
	row := rows[0]
	if len(row) != 29 {
		return usSecurityAuthorityFacts{}, ErrUnqualified
	}
	str := func(i int) (string, error) { return usSecurityString(row[i], 128) }
	facts := usSecurityAuthorityFacts{}
	var e error
	if facts.tokenGeneration, e = usSecurityIntValue(row[0]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.expiresAt, e = usSecurityString(row[1], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.family, e = str(2); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.serverID, e = str(3); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.account, e = str(4); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.profile, e = str(5); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.role, e = str(6); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.epoch, e = usSecurityIntValue(row[7]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.authorityGeneration, e = usSecurityIntValue(row[8]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.horizon, e = usSecurityString(row[9], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.rememberUntil, e = usSecurityOptional(row[10], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.manageUntil, e = usSecurityOptional(row[11], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.pinGate, e = usSecurityIntValue(row[12]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.lineage, e = usSecurityOptional(row[13], 128); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.securityKind, e = usSecurityIntValue(row[14]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	var accountEpoch, mustChange int64
	if accountEpoch, e = usSecurityIntValue(row[15]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if mustChange, e = usSecurityIntValue(row[16]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.accountProfile, e = str(17); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.memberRole, e = str(18); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.memberIncarnation, e = usSecurityString(row[19], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.disabled, e = usSecurityIntValue(row[20]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.lifetime, e = usSecurityString(row[21], 32); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.deleted, e = usSecurityIntValue(row[22]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.primary, e = usSecurityIntValue(row[23]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	switch value := row[24].(type) {
	case nil:
	case int64:
		if value < 0 {
			return usSecurityAuthorityFacts{}, ErrUnqualified
		}
		facts.pinLength = sql.NullInt64{Int64: value, Valid: true}
	default:
		return usSecurityAuthorityFacts{}, ErrUnqualified
	}
	if facts.deviceID, e = str(25); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.deviceAccount, e = str(26); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	if facts.installationID, e = usSecurityString(row[27], 128); e != nil {
		return usSecurityAuthorityFacts{}, e
	}
	var retired int64
	if retired, e = usSecurityIntValue(row[28]); e != nil {
		return usSecurityAuthorityFacts{}, e
	}

	// Closed source invariants that the common principal fence does not cover.
	// A read has no caller-supplied installation incarnation, so this recipe
	// proves the registered non-retired installation hash and the current device
	// binding instead of comparing a second claimed incarnation.
	if facts.tokenGeneration <= 0 || facts.epoch <= 0 || facts.authorityGeneration <= 0 ||
		facts.securityKind != 0 || facts.pinGate != 0 || mustChange != 0 ||
		accountEpoch != facts.epoch || facts.memberRole != facts.role || !usSecurityRole(facts.role) ||
		facts.disabled != 0 || facts.deleted != 0 || retired != 0 ||
		facts.deviceAccount != facts.account || facts.installationID != installation ||
		!usSecurityHex(facts.memberIncarnation, 32) || !usSecurityHex(facts.lifetime, 32) ||
		facts.deviceID == "" || facts.lineage != "" && !validUSSecurityIdentifier(facts.lineage) {
		return usSecurityAuthorityFacts{}, ErrDenied
	}
	tokenExpiry, ok := usSecurityTime(facts.expiresAt)
	if !ok {
		return usSecurityAuthorityFacts{}, ErrUnqualified
	}
	horizon, ok := usSecurityTime(facts.horizon)
	if !ok {
		return usSecurityAuthorityFacts{}, ErrUnqualified
	}
	if !tokenExpiry.After(now) || !horizon.After(now) {
		return usSecurityAuthorityFacts{}, ErrDenied
	}
	until := tokenExpiry
	if horizon.Before(until) {
		until = horizon
	}
	protected := facts.pinLength.Valid && facts.pinLength.Int64 > 0
	if protected {
		if _, err := usSecurityFenceGeneration(q, facts, "profile_pin"); err != nil {
			return usSecurityAuthorityFacts{}, err
		}
		if facts.rememberUntil != "" {
			for _, kind := range []string{"profile_remember", "profile_lineage"} {
				if _, err := usSecurityFenceGeneration(q, facts, kind); err != nil {
					return usSecurityAuthorityFacts{}, err
				}
			}
			remember, ok := usSecurityTime(facts.rememberUntil)
			if !ok {
				return usSecurityAuthorityFacts{}, ErrUnqualified
			}
			if remember.After(now) {
				if facts.manageUntil == "" {
					return usSecurityAuthorityFacts{}, ErrDenied
				}
				manage, ok := usSecurityTime(facts.manageUntil)
				if !ok {
					return usSecurityAuthorityFacts{}, ErrUnqualified
				}
				if !manage.After(now) {
					return usSecurityAuthorityFacts{}, ErrDenied
				}
				if manage.Before(until) {
					until = manage
				}
				if remember.Before(until) {
					until = remember
				}
			}
		}
	}
	// A cleared PIN preserves sign-ins: remember/manage timestamps are then
	// residual facts with no gate to apply.
	if owner {
		// The merged US-01 first instance proves only a direct owner main-profile
		// session with no profile PIN. Broader admin roles/capabilities refuse.
		if facts.role != "owner" || facts.memberRole != "owner" || facts.primary != 1 ||
			facts.accountProfile != facts.profile || protected {
			return usSecurityAuthorityFacts{}, ErrDenied
		}
	}
	if !until.After(now) {
		return usSecurityAuthorityFacts{}, ErrDenied
	}
	if _, err := usSecurityFenceGeneration(q, facts, "device"); err != nil {
		return usSecurityAuthorityFacts{}, err
	}
	return facts, nil
}

func usSecurityFenceGeneration(q browseQuery, facts usSecurityAuthorityFacts, kind string) (int64, error) {
	profile, lifetime, device, lineage := "", "", "", ""
	switch kind {
	case "device":
		device = facts.deviceID
	case "profile_pin", "profile_remember":
		profile, lifetime = facts.profile, facts.lifetime
	case "profile_lineage":
		profile, lifetime, lineage = facts.profile, facts.lifetime, facts.lineage
	default:
		return 0, ErrInput
	}
	rows, err := q(usSecurityFence, facts.familyID(), kind, facts.account, facts.memberIncarnation, profile, lifetime, device, lineage)
	if err != nil {
		return 0, err
	}
	if len(rows) != 1 {
		return 0, ErrDenied
	}
	if len(rows[0]) != 2 {
		return 0, ErrUnqualified
	}
	confirmed, err := usSecurityIntValue(rows[0][0])
	if err != nil {
		return 0, err
	}
	generation, err := usSecurityIntValue(rows[0][1])
	if err != nil {
		return 0, err
	}
	if confirmed <= 0 || confirmed != generation {
		return 0, ErrDenied
	}
	return generation, nil
}

// The authority row does not carry the family id; it is the token's family and
// the fence statement is keyed by it, so it must be retained by the caller.
// familyID is filled by usSecurityAuthority after the session point.
func (f usSecurityAuthorityFacts) familyID() string { return f.family }

func usSecurityWitness(facts usSecurityAuthorityFacts, owner bool) string {
	h := sha256.New()
	write := func(value string) {
		var size [8]byte
		n := len(value)
		for i := 0; i < 8; i++ {
			size[i] = byte(n >> (8 * i))
		}
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	write(facts.expiresAt)
	write(facts.horizon)
	write(facts.rememberUntil)
	write(facts.manageUntil)
	write(facts.family)
	write(facts.serverID)
	write(facts.account)
	write(facts.profile)
	write(facts.lifetime)
	write(facts.role)
	write(facts.memberRole)
	write(facts.memberIncarnation)
	write(facts.deviceID)
	write(facts.installationID)
	write(facts.accountProfile)
	var numbers [9]int64
	numbers = [9]int64{facts.tokenGeneration, facts.authorityGeneration, facts.epoch, facts.pinGate, facts.securityKind, facts.mustChange, facts.disabled, facts.deleted, facts.primary}
	for _, n := range numbers {
		write(fmt.Sprintf("%d", n))
	}
	write(fmt.Sprintf("%t", owner))
	return hex.EncodeToString(h.Sum(nil))
}

func usSecurityRole(value string) bool {
	return value == "owner" || value == "admin" || value == "member"
}

func usSecurityHex(value string, length int) bool {
	if length > 0 && len(value) != length {
		return false
	}
	if value == "" && length != 0 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

func usSecurityTime(value string) (time.Time, bool) {
	if len(value) != 20 {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.UTC().Format(time.RFC3339) != value {
		return time.Time{}, false
	}
	return parsed, true
}

func usSecurityString(value any, max int) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" || len(text) > max || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r\n") {
		return "", ErrUnqualified
	}
	return text, nil
}

func usSecurityOptional(value any, max int) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || len(text) > max || !utf8.ValidString(text) || strings.ContainsAny(text, "\x00\r\n") {
		return "", ErrUnqualified
	}
	return text, nil
}

func usSecurityIntValue(value any) (int64, error) {
	number, ok := value.(int64)
	if !ok {
		return 0, ErrUnqualified
	}
	return number, nil
}

func usSecurityBool(value any) (bool, error) {
	number, ok := value.(int64)
	if !ok || number != 0 && number != 1 {
		return false, ErrUnqualified
	}
	return number == 1, nil
}

func usSecurityMillis(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339)
}

func usSecurityLibraries(value any) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	text, ok := value.(string)
	if !ok || len(text) > usSecurityPageBytes || !utf8.ValidString(text) {
		return nil, ErrUnqualified
	}
	var libraries []string
	if err := json.Unmarshal([]byte(text), &libraries); err != nil || libraries == nil || len(libraries) > 100 {
		return nil, ErrUnqualified
	}
	for _, library := range libraries {
		if library == "" || len(library) > 256 || !utf8.ValidString(library) || strings.ContainsAny(library, "\x00\r\n") {
			return nil, ErrUnqualified
		}
	}
	return libraries, nil
}

// usSecurityBytesBound sums exact octet preflight lengths and refuses a page
// larger than the closed page bound before any payload row is hydrated.
func usSecurityBytesBound(rows [][]any) error {
	var total int64
	for _, row := range rows {
		for _, value := range row {
			if value == nil {
				continue
			}
			length, ok := value.(int64)
			if !ok || length < 0 || length > usSecurityPageBytes-total {
				return ErrUnqualified
			}
			total += length
		}
	}
	if total > usSecurityPageBytes {
		return ErrUnqualified
	}
	return nil
}

func usSecurityProfileValue(row []any) (USSecurityProfile, error) {
	if len(row) != 9 {
		return USSecurityProfile{}, ErrUnqualified
	}
	id, err := usSecurityString(row[0], 128)
	if err != nil {
		return USSecurityProfile{}, err
	}
	name, err := usSecurityString(row[1], 256)
	if err != nil {
		return USSecurityProfile{}, err
	}
	primary, err := usSecurityBool(row[2])
	if err != nil {
		return USSecurityProfile{}, err
	}
	position, err := usSecurityIntValue(row[3])
	if err != nil || position < 0 || position > 1<<20 {
		return USSecurityProfile{}, ErrUnqualified
	}
	art, err := usSecurityString(row[4], 32)
	if err != nil {
		return USSecurityProfile{}, err
	}
	revision, err := usSecurityIntValue(row[5])
	if err != nil || revision <= 0 {
		return USSecurityProfile{}, ErrUnqualified
	}
	pinRevision, err := usSecurityIntValue(row[6])
	if err != nil || pinRevision <= 0 {
		return USSecurityProfile{}, ErrUnqualified
	}
	pinLength, err := usSecurityIntValue(row[7])
	if err != nil || pinLength <= 0 {
		// length(NULL) is the only legal "no PIN" shape; a zero-length hash is
		// corrupt source.
		if row[7] != nil {
			return USSecurityProfile{}, ErrUnqualified
		}
		pinLength = 0
	}
	libraries, err := usSecurityLibraries(row[8])
	if err != nil {
		return USSecurityProfile{}, err
	}
	return USSecurityProfile{ID: id, Name: name, Primary: primary, Position: int(position), Art: art,
		Revision: revision, PINRevision: pinRevision, PINRequired: pinLength > 0, AllowedLibraries: libraries}, nil
}

func usSecurityDeviceValue(row []any) (USSecurityDevice, error) {
	if len(row) != 11 {
		return USSecurityDevice{}, ErrUnqualified
	}
	required := make([]string, 3)
	for i := range required {
		value, err := usSecurityString(row[i], 256)
		if err != nil {
			return USSecurityDevice{}, err
		}
		required[i] = value
	}
	optional := make([]string, 7)
	for i := range optional {
		value, err := usSecurityOptional(row[i+3], 256)
		if err != nil {
			return USSecurityDevice{}, err
		}
		optional[i] = value
	}
	revision, err := usSecurityIntValue(row[10])
	if err != nil || revision <= 0 {
		return USSecurityDevice{}, ErrUnqualified
	}
	return USSecurityDevice{ID: required[0], AccountID: required[1], Username: required[2], ProfileID: optional[0],
		Name: optional[1], Platform: optional[2], App: optional[3], AppVersion: optional[4], FirstSeen: optional[5], LastSeen: optional[6], Revision: revision}, nil
}

func usSecurityStatusValue(row []any, f browseFence) (USSecurityStatus, error) {
	if len(row) != 31 {
		return USSecurityStatus{}, ErrUnqualified
	}
	str := func(i int) (string, error) { return usSecurityString(row[i], 256) }
	status := USSecurityStatus{}
	var err error
	if status.FamilyID, err = str(0); err != nil {
		return USSecurityStatus{}, err
	}
	if status.ServerID, err = str(1); err != nil {
		return USSecurityStatus{}, err
	}
	if status.AccountID, err = str(2); err != nil {
		return USSecurityStatus{}, err
	}
	if status.ProfileID, err = str(3); err != nil {
		return USSecurityStatus{}, err
	}
	if status.Role, err = str(4); err != nil {
		return USSecurityStatus{}, err
	}
	if status.Epoch, err = usSecurityIntValue(row[5]); err != nil {
		return USSecurityStatus{}, err
	}
	if status.AuthorityGeneration, err = usSecurityIntValue(row[6]); err != nil {
		return USSecurityStatus{}, err
	}
	if status.Horizon, err = usSecurityString(row[7], 32); err != nil {
		return USSecurityStatus{}, err
	}
	if status.RememberUntil, err = usSecurityOptional(row[8], 32); err != nil {
		return USSecurityStatus{}, err
	}
	if status.ManageUntil, err = usSecurityOptional(row[9], 32); err != nil {
		return USSecurityStatus{}, err
	}
	if status.FamilyGeneration, err = usSecurityIntValue(row[10]); err != nil {
		return USSecurityStatus{}, err
	}
	if status.ExpiresAt, err = usSecurityString(row[11], 32); err != nil {
		return USSecurityStatus{}, err
	}
	profile := USSecurityProfile{}
	if profile.ID, err = str(3); err != nil {
		return USSecurityStatus{}, err
	}
	if profile.Name, err = str(12); err != nil {
		return USSecurityStatus{}, err
	}
	if profile.Primary, err = usSecurityBool(row[13]); err != nil {
		return USSecurityStatus{}, err
	}
	position, err := usSecurityIntValue(row[14])
	if err != nil || position < 0 || position > 1<<20 {
		return USSecurityStatus{}, ErrUnqualified
	}
	profile.Position = int(position)
	if profile.Art, err = str(15); err != nil {
		return USSecurityStatus{}, err
	}
	if profile.Revision, err = usSecurityIntValue(row[16]); err != nil || profile.Revision <= 0 {
		return USSecurityStatus{}, ErrUnqualified
	}
	if profile.PINRevision, err = usSecurityIntValue(row[17]); err != nil || profile.PINRevision <= 0 {
		return USSecurityStatus{}, ErrUnqualified
	}
	switch value := row[18].(type) {
	case nil:
	case int64:
		if value <= 0 {
			return USSecurityStatus{}, ErrUnqualified
		}
		profile.PINRequired = true
	default:
		return USSecurityStatus{}, ErrUnqualified
	}
	if profile.AllowedLibraries, err = usSecurityLibraries(row[19]); err != nil {
		return USSecurityStatus{}, err
	}
	device := USSecurityDevice{}
	if device.ID, err = str(20); err != nil {
		return USSecurityStatus{}, err
	}
	device.AccountID = status.AccountID
	if device.Username, err = str(21); err != nil {
		return USSecurityStatus{}, err
	}
	if device.Name, err = usSecurityOptional(row[22], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.Platform, err = usSecurityOptional(row[23], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.App, err = usSecurityOptional(row[24], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.AppVersion, err = usSecurityOptional(row[25], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.FirstSeen, err = usSecurityOptional(row[26], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.LastSeen, err = usSecurityOptional(row[27], 256); err != nil {
		return USSecurityStatus{}, err
	}
	if device.ProfileID, err = usSecurityOptional(row[28], 128); err != nil {
		return USSecurityStatus{}, err
	}
	if device.RememberAccount, err = usSecurityBool(row[29]); err != nil {
		return USSecurityStatus{}, err
	}
	if device.Revision, err = usSecurityIntValue(row[30]); err != nil || device.Revision <= 0 {
		return USSecurityStatus{}, ErrUnqualified
	}
	status.Profile, status.Device = profile, device
	status.PINRequired = profile.PINRequired
	status.Remembered = status.RememberUntil != ""
	// Bind the payload to the exact authorized principal; a payload row from a
	// different family can never be returned under this fence.
	if status.AccountID != f.account || status.ProfileID != f.profile || status.FamilyID == "" || status.Role != f.role || status.Epoch != f.epoch {
		return USSecurityStatus{}, ErrDenied
	}
	return status, nil
}

func usSecurityMemberValue(row []any) (USSecurityMember, error) {
	if len(row) != 14 {
		return USSecurityMember{}, ErrUnqualified
	}
	str := func(i int, max int) (string, error) { return usSecurityString(row[i], max) }
	member := USSecurityMember{}
	var err error
	if member.AccountID, err = str(0, 128); err != nil {
		return USSecurityMember{}, err
	}
	if member.Username, err = str(1, 256); err != nil {
		return USSecurityMember{}, err
	}
	if member.Email, err = usSecurityOptional(row[2], 256); err != nil {
		return USSecurityMember{}, err
	}
	if member.DisplayName, err = usSecurityOptional(row[3], 256); err != nil {
		return USSecurityMember{}, err
	}
	if member.ProfileID, err = str(4, 128); err != nil {
		return USSecurityMember{}, err
	}
	if member.Role, err = str(5, 16); err != nil {
		return USSecurityMember{}, err
	}
	if member.Disabled, err = usSecurityBool(row[6]); err != nil {
		return USSecurityMember{}, err
	}
	disabledMS, err := usSecurityIntValue(row[7])
	if err != nil || disabledMS < 0 {
		return USSecurityMember{}, ErrUnqualified
	}
	member.DisabledAt = usSecurityMillis(disabledMS)
	if member.DisabledReason, err = usSecurityOptional(row[8], 64); err != nil {
		return USSecurityMember{}, err
	}
	if member.Revision, err = usSecurityIntValue(row[9]); err != nil || member.Revision <= 0 {
		return USSecurityMember{}, ErrUnqualified
	}
	if member.AllowedLibraries, err = usSecurityLibraries(row[10]); err != nil {
		return USSecurityMember{}, err
	}
	if row[11] != nil {
		note, err := usSecurityString(row[11], 500)
		if err != nil {
			return USSecurityMember{}, err
		}
		member.Notes = &note
	}
	if member.JoinedVia, err = usSecurityOptional(row[12], 64); err != nil {
		return USSecurityMember{}, err
	}
	joinedMS, err := usSecurityIntValue(row[13])
	if err != nil || joinedMS < 0 {
		return USSecurityMember{}, ErrUnqualified
	}
	member.JoinedAt = usSecurityMillis(joinedMS)
	if !usSecurityRole(member.Role) || !validUSSecurityIdentifier(member.AccountID) {
		return USSecurityMember{}, ErrUnqualified
	}
	return member, nil
}

// usSecurityStatusVariable is the exact payload projection paired with the
// status octet point. Order matches usSecurityStatusSQL and usSecurityStatusOctetsSQL.
var usSecurityStatusVariable = []int{0, 1, 2, 3, 4, 7, 8, 9, 11, 12, 15, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28}

// usSecuritySnapshots adds exact-octet point pairs to the shared bounded
// snapshot protocol. It mints no grant: every pair still runs through the
// registered QueryExactBytes API inside the caller's admitted scope.
type usSecuritySnapshots struct {
	base              browseSnapshots
	scope             *governor.ReadScope
	points            int
	copied, copyLimit int64
}

func (s *usSecuritySnapshots) NativeRowReservation() int64 { return s.base.NativeRowReservation() }

func (s *usSecuritySnapshots) Read(ctx context.Context, request governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	callback := func(scope *governor.ReadScope) error {
		s.scope = scope
		defer func() { s.scope = nil }()
		return read(scope)
	}
	if pool, ok := s.base.(interface {
		ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	}); ok {
		queries := min(uint32(4096), request.Budget.RawRows)
		if queries == 0 {
			queries = 1
		}
		return pool.ReadForecasted(ctx, request, budget, governor.SnapshotForecast{NativeRowBytes: s.NativeRowReservation(), MaxQueries: uint16(queries)}, callback)
	}
	return s.base.Read(ctx, request, budget, callback)
}

func (s *usSecuritySnapshots) point(name string, columns []int, args ...any) ([]any, error) {
	if s == nil || s.scope == nil || len(columns) == 0 {
		return nil, ErrInput
	}
	row, err := s.scope.QueryExactBytes(readStatementID(name+"_octets"), readStatementID(name), columns, args...)
	s.points += 2
	if err != nil {
		return nil, err
	}
	s.copied += 64 + int64(len(row))*32
	for _, value := range row {
		switch value := value.(type) {
		case string:
			s.copied += 2 * int64(len(value))
		case []byte:
			s.copied += 3 * int64(len(value))
		}
	}
	if s.copied > s.copyLimit {
		return nil, governor.ErrSnapshotBudget
	}
	return row, nil
}
