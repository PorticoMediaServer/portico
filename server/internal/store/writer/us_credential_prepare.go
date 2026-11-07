package writer

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Existing access/credentials.go expiry envelope, not a new product quota.
const usCredentialLifetimeMS int64 = int64(10 * 365 * 24 * time.Hour / time.Millisecond)

func describeUSCredentialCommand(id CommandID) (Descriptor, bool) {
	switch id {
	case usLocalPasswordReplace:
		return Descriptor{ID: id, Name: "us_local_password_replace", Durability: Full}, true
	case usAPICredentialIssue:
		return Descriptor{ID: id, Name: "us_api_credential_issue", Durability: Full}, true
	case usAPICredentialRevoke:
		return Descriptor{ID: id, Name: "us_api_credential_revoke", Durability: Full}, true
	default:
		return Descriptor{}, false
	}
}

// Source comparison witnesses, never proof of password possession, a role or
// permission. Names are included for the immutable Credential.Member result;
// native point comparisons prove them without copying unbounded source text.
type usCredentialTarget struct {
	account, memberIncarnation, profile, profileLifetime string
	username, profileName                                string
	epoch, memberRevision, profileRevision               int64
	disabled, deleted, mustChange                        bool
}

func validUSCredentialTarget(t usCredentialTarget) bool {
	return usIdentifier(t.account) && usHex(t.memberIncarnation, 32) &&
		usIdentifier(t.profile) && usHex(t.profileLifetime, 32) &&
		t.epoch > 0 && t.epoch <= usMaxRevision &&
		t.memberRevision > 0 && t.memberRevision <= usMaxRevision &&
		t.profileRevision > 0 && t.profileRevision <= usMaxRevision &&
		t.username != "" && safeUSInvitationText(t.username, 64) &&
		t.profileName != "" && len(t.profileName) <= 120 && utf8.ValidString(t.profileName) && strings.IndexFunc(t.profileName, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

type usPasswordReplaceRequest struct {
	target                 usCredentialTarget
	previousHash, nextHash []byte
}

type usPasswordReplacePrepared struct{ request usPasswordReplaceRequest }

// Ordinary LOCAL password replacement only. Proof verification, password
// validation (including new != current), hash admission, bcrypt and entropy
// belong outside the writer. The old hash is a comparison witness from root's
// independently verified proof; a well-formed hash does not establish proof.
// Reset-file/member-reset and factor-disable recipes are deliberately absent.
func prepareUSPasswordReplace(in usPasswordReplaceRequest) (*usPasswordReplacePrepared, error) {
	if !validUSCredentialTarget(in.target) || in.target.epoch >= usMaxRevision || in.target.disabled || in.target.deleted || in.target.mustChange || !usCredentialBcryptEnvelope(in.previousHash, false) || !usCredentialBcryptEnvelope(in.nextHash, true) {
		return nil, invalid("us_password_preparation")
	}
	in.previousHash, in.nextHash = slices.Clone(in.previousHash), slices.Clone(in.nextHash)
	return &usPasswordReplacePrepared{request: in}, nil
}

// Encoding only, no hashing/verification. Existing password hashes can have an
// older supported work factor; newly generated passwords use cost 10, exactly
// identity/hash_cost.go. Canonical unused bits prevent alternate byte images.
func usCredentialBcryptEnvelope(hash []byte, newlyGenerated bool) bool {
	if len(hash) != 60 || hash[0] != '$' || hash[1] != '2' ||
		(hash[2] != 'a' && hash[2] != 'b' && hash[2] != 'y') ||
		hash[3] != '$' || hash[4] < '0' || hash[4] > '9' || hash[5] < '0' || hash[5] > '9' || hash[6] != '$' {
		return false
	}
	cost := int(hash[4]-'0')*10 + int(hash[5]-'0')
	if cost < 4 || cost > 31 || newlyGenerated && cost != 10 {
		return false
	}
	const alphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	for n, b := range hash[7:] {
		i := strings.IndexByte(alphabet, b)
		if i < 0 || n == 21 && i&15 != 0 || n == 52 && i&3 != 0 {
			return false
		}
	}
	return true
}

type usCredentialIssueRequest struct {
	target                           usCredentialTarget
	id, kind, name                   string
	scopes                           []string
	secretHash, hint, family, device string
	expiresAt                        string
}

// One immutable top-level source row, not an issued credential. A prepared
// hash/hint and proposed family/device cannot establish that any secret exists
// or that root may issue it. Derived integration sessions/keys need their own
// parent-scope and lifetime design and are not accepted by this preparation.
type usCredentialIssuePrepared struct {
	target                           usCredentialTarget
	id, kind, name, scopes           string
	secretHash, hint, family, device string
	expiresMS                        int64
	expirySeconds                    int64
	expiryNanos                      int32
}

func prepareUSCredentialIssue(in usCredentialIssueRequest, nowMS int64) (*usCredentialIssuePrepared, error) {
	if !validUSCredentialTarget(in.target) || in.target.disabled || in.target.deleted || in.target.mustChange || !usIdentifier(in.id) || !usIdentifier(in.family) || !usIdentifier(in.device) || !usHex(in.secretHash, 64) {
		return nil, invalid("us_credential_identity")
	}
	if in.kind != "key" && in.kind != "token" && in.kind != "app_password" {
		return nil, invalid("us_credential_kind")
	}
	name := strings.TrimSpace(in.name)
	if name == "" || !safeUSInvitationText(name, 80) {
		return nil, invalid("name")
	}
	// Four closed existing scopes, in the published order. This is validation,
	// never permission inclusion (admin does not imply another scope).
	scopes, ok := usCredentialScopes(in.scopes)
	if !ok {
		return nil, invalid("scopes")
	}
	if in.kind == "app_password" && in.hint != "" || in.kind != "app_password" && !usCredentialHint(in.hint) {
		return nil, invalid("us_credential_hint")
	}
	if !usCredentialTime(nowMS) {
		return nil, invalid("us_credential_time")
	}
	var expiry int64
	var expirySeconds int64
	var expiryNanos int32
	if in.expiresAt != "" {
		// 64 bytes bounds this private RFC3339 recipe before parsing. It is
		// not a product lifetime/count quota; ordinary UTC/offset encodings fit.
		if len(in.expiresAt) > 64 {
			return nil, invalid("expiresAt")
		}
		t, err := time.Parse(time.RFC3339, in.expiresAt)
		if err != nil || !t.After(time.UnixMilli(nowMS).Add(time.Minute)) || t.After(time.UnixMilli(nowMS).Add(10*365*24*time.Hour)) {
			return nil, invalid("expiresAt")
		}
		expirySeconds, expiryNanos = t.Unix(), int32(t.Nanosecond())
		// Existing key/token issuance stores whole seconds. App passwords
		// retain milliseconds in their credential row.
		if in.kind != "app_password" {
			t = t.Truncate(time.Second)
		}
		expiry = t.UnixMilli()
	}
	return &usCredentialIssuePrepared{target: in.target, id: in.id, kind: in.kind, name: strings.Clone(name), scopes: scopes, secretHash: in.secretHash, hint: in.hint, family: in.family, device: in.device, expiresMS: expiry, expirySeconds: expirySeconds, expiryNanos: expiryNanos}, nil
}

func usCredentialTime(nowMS int64) bool {
	return nowMS > 0 && nowMS <= usMaxRevision-usCredentialLifetimeMS
}

func usCredentialScopes(in []string) (string, bool) {
	if len(in) < 1 || len(in) > 4 {
		return "", false
	}
	const order = "read,playback,library,admin"
	var bits uint8
	for _, scope := range in {
		var bit uint8
		switch scope {
		case "read":
			bit = 1
		case "playback":
			bit = 2
		case "library":
			bit = 4
		case "admin":
			bit = 8
		default:
			return "", false
		}
		if bits&bit != 0 {
			return "", false
		}
		bits |= bit
	}
	out := make([]string, 0, 4)
	for i, scope := range strings.Split(order, ",") {
		if bits&(1<<i) != 0 {
			out = append(out, scope)
		}
	}
	return strings.Join(out, ","), true
}

func usCredentialHint(v string) bool {
	if len(v) != 6 {
		return false
	}
	for _, b := range []byte(v) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return false
		}
	}
	return true
}

// Exact protected-output manifest for root's US-001 packet. No plaintext,
// encryption key, ciphertext codec or saved-result schema is invented here.
// Root must bind the actual outside-writer generated secret's prefix+digest+
// hint to this identity and its protected artifact before accepting a result.
// key/app_password also require explicit owner-recovery custody. A token's
// issuance output is protected too, but has no owner-recoverable source secret.
type usCredentialProtectedManifest struct {
	target                                             usCredentialTarget
	id, kind, prefix, secretHash, hint, family, device string
	name, scopes                                       string
	expiresMS                                          int64
	requiresOwnerRecovery                              bool
}

func (p *usCredentialIssuePrepared) protectedManifest() usCredentialProtectedManifest {
	if p == nil {
		return usCredentialProtectedManifest{}
	}
	prefix := "pat."
	if p.kind == "key" {
		prefix = "pk."
	}
	if p.kind == "app_password" {
		prefix = "papp."
	}
	return usCredentialProtectedManifest{target: p.target, id: p.id, kind: p.kind, prefix: prefix, secretHash: p.secretHash, hint: p.hint, family: p.family, device: p.device, name: p.name, scopes: p.scopes, expiresMS: p.expiresMS, requiresOwnerRecovery: p.kind != "token"}
}

// Complete bounded immutable API source-row witness for top-level revocation.
// API credentials have no source revision/lifetime column: exact identity,
// digest, family/device and created time prevent an ID-only stale revocation.
// Names/scopes/hint/expiry/ended/revoked are compared as well, so post-results
// never reconstruct the old operation from a subsequently edited source row.
type usCredentialRevokeRequest struct {
	target                           usCredentialTarget
	id, kind, name, scopes           string
	secretHash, hint, family, device string
	createdMS, expiresMS, revokedMS  int64
	endedReason                      string
}

type usCredentialRevokePrepared struct{ request usCredentialRevokeRequest }

func prepareUSCredentialRevoke(in usCredentialRevokeRequest) (*usCredentialRevokePrepared, error) {
	if !validUSCredentialTarget(in.target) || !usIdentifier(in.id) || !usIdentifier(in.family) || !usIdentifier(in.device) || !usHex(in.secretHash, 64) ||
		(in.kind != "key" && in.kind != "token" && in.kind != "app_password") || in.createdMS <= 0 || in.createdMS > usMaxRevision || in.expiresMS < 0 || in.expiresMS > usMaxRevision || in.revokedMS < 0 || in.revokedMS > usMaxRevision {
		return nil, invalid("us_credential_revoke")
	}
	if in.name == "" || !safeUSInvitationText(in.name, 80) || len(in.scopes) > len("read,playback,library,admin") {
		return nil, invalid("us_credential_manifest")
	}
	scopes, ok := usCredentialScopes(strings.Split(in.scopes, ","))
	if !ok || scopes != in.scopes || in.kind == "app_password" && in.hint != "" || in.kind != "app_password" && !usCredentialHint(in.hint) {
		return nil, invalid("us_credential_manifest")
	}
	switch in.endedReason {
	case "", "member_disabled", "member_deleted", "profile_deleted", "permission_removed", "unavailable":
	default:
		return nil, invalid("us_credential_ended_reason")
	}
	return &usCredentialRevokePrepared{request: in}, nil
}
