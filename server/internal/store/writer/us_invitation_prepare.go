package writer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
)

// US-12 owns the explicit invitation source purposes. The shared descriptor
// registry in commands_us.go is owned by the train's registry task, so these
// identities are declared here and only the descriptor rows are reported for
// that owner to add. They are preparation metadata, never permission.
const (
	usInvitationCreate CommandID = 550
	usInvitationEdit   CommandID = 551
	usInvitationRevoke CommandID = 552
)

// These remaining constants preserve existing API envelopes. They are not a
// product quota or admission: root composes the live-invitation capacity.
const (
	usInvitationMaxLibraries       = 256
	usInvitationMaxHours           = 720
	usInvitationHourMS       int64 = 3600000
	usInvitationMaxSpanMS          = usInvitationMaxHours * usInvitationHourMS
)

func describeUSInvitationCommand(id CommandID) (Descriptor, bool) {
	switch id {
	case usInvitationCreate:
		return Descriptor{ID: id, Name: "us_invitation_create", Durability: Full}, true
	case usInvitationEdit:
		return Descriptor{ID: id, Name: "us_invitation_edit", Durability: Full}, true
	case usInvitationRevoke:
		return Descriptor{ID: id, Name: "us_invitation_revoke", Durability: Full}, true
	default:
		return Descriptor{}, false
	}
}

// Captured issuer witnesses are comparisons, never authority. Current issuer
// authentication, original receipt classification and target-tier management
// belong to root's same-turn composition BEFORE applying these source effects.
type usInvitationScope struct {
	ownerID, operationID string
	issuer               usPolicyIssuer
}

type usInvitationAccess struct {
	mode                      string
	streams                   int
	allowRemote, accessTokens bool
	rating                    string
}

type usInvitationCreateInput struct {
	notes          *string
	role           string
	libraries      []string
	expiresInHours int
	boundUsername  string
	access         *usInvitationAccess
}

type usInvitationEditInput struct {
	expectedRevision int64
	notes            *string
	role             string
	libraries        []string
	expiresAt        string
	access           *usInvitationAccess // nil preserves CURRENT stored access
}

// Frozen preparation only, with no executor, Applied flag or secret output.
// Root wraps the COMPLETE payload with canonicalRequestDigest(kind,1,identity,
// payload) once the shared registry admits this purpose. Generated ID/hash must
// be kept for retries of the original operation, never regenerated on an
// uncertain COMMIT.
type usInvitationPrepared struct {
	kind             CommandID
	identity         CommandIdentity
	scope            usInvitationScope
	id               string
	expectedRevision int64
	notes            sql.NullString
	role             string
	libraries        []string
	librariesJSON    string
	hours            int
	expiresMS        int64
	boundUsername    string
	access           usInvitationAccess
	replaceAccess    bool
	codeHash         string // SHA-256 lowercase hex; never the long or short capability
}

// A captured sign-in-kind revision is a comparison fact, never authority. Root
// proves the current local/Portico identity state in the same frame; this
// binding only turns kind or revision drift into a refusal.
type usInvitationSignInKind struct {
	kind     string
	revision int64
}

func prepareUSInvitationSignInKind(kind string, revision int64) (usInvitationSignInKind, error) {
	if kind != "local" && kind != "portico" || revision < 1 || revision >= usMaxRevision {
		return usInvitationSignInKind{}, invalid("us_invitation_sign_in_kind")
	}
	return usInvitationSignInKind{kind: kind, revision: revision}, nil
}

func (b usInvitationSignInKind) valid() bool {
	return (b.kind == "local" || b.kind == "portico") && b.revision >= 1 && b.revision < usMaxRevision
}

func prepareUSInvitationCreate(i CommandIdentity, scope usInvitationScope, id, codeHash string, q usInvitationCreateInput) (*usInvitationPrepared, error) {
	if !validUSInvitationScope(i, scope) || !usIdentifier(id) || !usHex(codeHash, 64) {
		return nil, invalid("us_invitation_identity")
	}
	if q.role == "" {
		q.role = "member"
	}
	if q.expiresInHours == 0 {
		q.expiresInHours = 168
	}
	if q.expiresInHours < 1 || q.expiresInHours > usInvitationMaxHours {
		return nil, invalid("expiresInHours")
	}
	// Trim is a view; bound runes BEFORE the allocating case conversion.
	bound := strings.TrimSpace(q.boundUsername)
	if !safeUSInvitationText(bound, 64) {
		return nil, invalid("porticoUsername")
	}
	bound = strings.ToLower(bound)
	if len(bound) > 64 || !safeUSInvitationText(bound, 64) || strings.ContainsAny(bound, " \t\r\n") {
		return nil, invalid("porticoUsername")
	}
	p, err := prepareUSInvitationFields(q.notes, q.role, q.libraries, q.access)
	if err != nil {
		return nil, err
	}
	p.kind, p.identity, p.scope, p.id = usInvitationCreate, i, scope, id
	p.hours, p.boundUsername, p.codeHash = q.expiresInHours, strings.Clone(bound), codeHash
	p.replaceAccess = true
	return p, nil
}

// nowMS is a preparation clock observation, not permission to execute later.
// The native edit repeats old/new expiry checks against trusted transaction time.
func prepareUSInvitationEdit(i CommandIdentity, scope usInvitationScope, id string, q usInvitationEditInput, nowMS int64) (*usInvitationPrepared, error) {
	if !validUSInvitationScope(i, scope) || !usIdentifier(id) || q.expectedRevision < 1 || q.expectedRevision >= usMaxRevision {
		return nil, invalid("us_invitation_identity")
	}
	if nowMS <= 0 || nowMS > usMaxRevision-usInvitationMaxSpanMS {
		return nil, invalid("expiresAt")
	}
	expires, err := time.Parse(time.RFC3339, q.expiresAt)
	if err != nil || !expires.After(time.UnixMilli(nowMS)) || expires.After(time.UnixMilli(nowMS).Add(720*time.Hour)) || expires.UnixMilli() <= nowMS {
		return nil, invalid("expiresAt")
	}
	p, err := prepareUSInvitationFields(q.notes, q.role, q.libraries, q.access)
	if err != nil {
		return nil, err
	}
	p.kind, p.identity, p.scope, p.id = usInvitationEdit, i, scope, id
	p.expectedRevision, p.expiresMS, p.replaceAccess = q.expectedRevision, expires.UnixMilli(), q.access != nil
	return p, nil
}

func prepareUSInvitationRevoke(i CommandIdentity, scope usInvitationScope, id string, revision int64) (*usInvitationPrepared, error) {
	if !validUSInvitationScope(i, scope) || !usIdentifier(id) || revision < 1 || revision >= usMaxRevision {
		return nil, invalid("us_invitation_identity")
	}
	return &usInvitationPrepared{kind: usInvitationRevoke, identity: i, scope: scope, id: id, expectedRevision: revision}, nil
}

func validUSInvitationScope(i CommandIdentity, scope usInvitationScope) bool {
	v := scope.issuer
	return i.Incarnation != [16]byte{} && i.OperationID != [16]byte{} && i.Sequence > 0 &&
		usIdentifier(scope.ownerID) && len(scope.operationID) >= 8 && len(scope.operationID) <= 128 && safeUSInvitationText(scope.operationID, 128) &&
		usIdentifier(v.server) && usIdentifier(v.account) && usIdentifier(v.profile) && usIdentifier(v.family) &&
		usHex(v.memberIncarnation, 32) && usHex(v.profileLifetime, 32) && usHex(v.installation, 32) && usIdentityDigest(v.tokenDigest) &&
		v.epoch > 0 && v.familyGeneration > 0 && v.authorityGeneration > 0
}

func prepareUSInvitationFields(notes *string, role string, libraries []string, access *usInvitationAccess) (*usInvitationPrepared, error) {
	if role != "member" && role != "admin" {
		return nil, invalid("role")
	}
	if len(libraries) > usInvitationMaxLibraries {
		return nil, invalid("allowedLibraries")
	}
	for _, id := range libraries {
		if !usIdentifier(id) {
			return nil, invalid("allowedLibraries")
		}
	}
	seen := make(map[string]bool, len(libraries))
	for _, id := range libraries {
		if seen[id] {
			return nil, invalid("allowedLibraries")
		}
		seen[id] = true
	}
	p := &usInvitationPrepared{role: role, libraries: slices.Clone(libraries)}
	if p.libraries == nil {
		p.libraries = []string{}
	} // invitation [] means all libraries
	if notes != nil {
		v := strings.TrimSpace(*notes)
		if !safeUSInvitationText(v, 500) {
			return nil, invalid("notes")
		}
		p.notes = sql.NullString{String: strings.Clone(v), Valid: v != ""}
	}
	p.access = usInvitationAccess{mode: "inherit", allowRemote: true, accessTokens: true}
	if access != nil {
		p.access = *access
		p.access.rating = strings.TrimSpace(p.access.rating)
		if utf8.RuneCountInString(p.access.rating) > 32 {
			return nil, invalid("access.maxContentRating")
		}
		p.access.rating = strings.ToUpper(p.access.rating)
		if !securitypolicy.ValidStreamMode(p.access.mode, p.access.streams) {
			return nil, invalid("access.maxStreams")
		}
		if len(p.access.rating) > 32 {
			return nil, invalid("access.maxContentRating")
		}
		if _, ok := securitypolicy.RatingAge(p.access.rating); p.access.rating != "" && !ok {
			return nil, invalid("access.maxContentRating")
		}
		p.access.rating = strings.Clone(p.access.rating)
	}
	raw, err := json.Marshal(p.libraries)
	if err != nil {
		return nil, err
	}
	p.librariesJSON = string(raw)
	return p, nil
}

func safeUSInvitationText(v string, max int) bool {
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) > max {
		return false
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// Logging these preparations must never traverse witnesses or protected wire.
func (usInvitationScope) String() string   { return "usInvitationScope{redacted}" }
func (usInvitationScope) GoString() string { return "usInvitationScope{redacted}" }
func (usInvitationScope) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationScope")
}

func (usInvitationAccess) String() string   { return "usInvitationAccess{redacted}" }
func (usInvitationAccess) GoString() string { return "usInvitationAccess{redacted}" }
func (usInvitationAccess) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationAccess")
}

func (usInvitationCreateInput) String() string   { return "usInvitationCreateInput{redacted}" }
func (usInvitationCreateInput) GoString() string { return "usInvitationCreateInput{redacted}" }
func (usInvitationCreateInput) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationCreateInput")
}

func (usInvitationEditInput) String() string   { return "usInvitationEditInput{redacted}" }
func (usInvitationEditInput) GoString() string { return "usInvitationEditInput{redacted}" }
func (usInvitationEditInput) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationEditInput")
}

func (usInvitationPrepared) String() string   { return "usInvitationPrepared{redacted}" }
func (usInvitationPrepared) GoString() string { return "usInvitationPrepared{redacted}" }
func (usInvitationPrepared) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationPrepared")
}

func (usInvitationSignInKind) String() string   { return "usInvitationSignInKind{redacted}" }
func (usInvitationSignInKind) GoString() string { return "usInvitationSignInKind{redacted}" }
func (usInvitationSignInKind) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationSignInKind")
}
