package writer

import (
	"bytes"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"testing"
	"time"
)

const usCredentialFixturePassword = "$2a$10$....................................................."

var usCredentialFixtureDigest = identity.Digest("synthetic-credential-fixture")

func usCredentialTestTarget() usCredentialTarget {
	return usCredentialTarget{account: "us-credential-account", memberIncarnation: strings.Repeat("a", 32), profile: "us-credential-profile", profileLifetime: strings.Repeat("b", 32), username: "credential-user", profileName: "Credential profile", epoch: 1, memberRevision: 1, profileRevision: 1}
}

func usCredentialTestIssue() usCredentialIssueRequest {
	return usCredentialIssueRequest{target: usCredentialTestTarget(), id: "us-credential", kind: "token", name: "  Automation  ", scopes: []string{"playback", "read"}, secretHash: usCredentialFixtureDigest, hint: "abcdef", family: "us-credential-family", device: "us-credential-device"}
}

func usCredentialTestRevoke() usCredentialRevokeRequest {
	return usCredentialRevokeRequest{target: usCredentialTestTarget(), id: "us-credential", kind: "token", name: "Automation", scopes: "read,playback", secretHash: usCredentialFixtureDigest, hint: "abcdef", family: "us-credential-family", device: "us-credential-device", createdMS: 1000}
}

func usCredentialTestPassword() usPasswordReplaceRequest {
	return usPasswordReplaceRequest{target: usCredentialTestTarget(), previousHash: []byte(usCredentialFixturePassword), nextHash: []byte(usCredentialFixturePassword)}
}

func TestUSCredentialPreparationFreezeAndManifest(t *testing.T) {
	in := usCredentialTestPassword()
	p, err := prepareUSPasswordReplace(in)
	if err != nil {
		t.Fatal(err)
	}
	in.previousHash[7], in.nextHash[7] = 'A', 'A'
	if !bytes.Equal(p.request.previousHash, []byte(usCredentialFixturePassword)) || !bytes.Equal(p.request.nextHash, []byte(usCredentialFixturePassword)) {
		t.Fatal("hash input aliases preparation")
	}
	for _, kind := range []CommandID{520, 521, 522} {
		d, ok := describeUSCredentialCommand(kind)
		if !ok || d.ID != kind || d.Durability != Full || d.Name == "" {
			t.Fatal("missing explicit FULL purpose")
		}
	}
	if _, ok := describeUSCredentialCommand(usPINSet); ok {
		t.Fatal("borrowed unrelated purpose")
	}
	q := usCredentialTestIssue()
	issue, err := prepareUSCredentialIssue(q, 1000)
	if err != nil {
		t.Fatal(err)
	}
	q.scopes[0] = "admin"
	if issue.scopes != "read,playback" || issue.name != "Automation" {
		t.Fatal("normalization/freeze failed")
	}
	manifest := issue.protectedManifest()
	if manifest.id != q.id || manifest.prefix != "pat." || manifest.secretHash != q.secretHash || manifest.hint != "abcdef" || manifest.requiresOwnerRecovery || manifest.family != q.family || manifest.device != q.device || manifest.target != issue.target || manifest.name != issue.name || manifest.scopes != issue.scopes || manifest.expiresMS != issue.expiresMS {
		t.Fatal("incomplete protected-output binding manifest")
	}
	for _, kind := range []string{"key", "app_password"} {
		q = usCredentialTestIssue()
		q.kind = kind
		prefix := "pk."
		if kind == "app_password" {
			q.hint = ""
			prefix = "papp."
		}
		p, err := prepareUSCredentialIssue(q, 1000)
		if err != nil || !p.protectedManifest().requiresOwnerRecovery || p.protectedManifest().prefix != prefix {
			t.Fatal("missing existing owner recovery requirement", err)
		}
	}
}

func TestUSCredentialPreparationRefusalsAndExpiry(t *testing.T) {
	for _, edit := range []func(*usPasswordReplaceRequest){
		func(q *usPasswordReplaceRequest) { q.target.memberIncarnation = "" },
		func(q *usPasswordReplaceRequest) { q.target.profileLifetime = strings.Repeat("B", 32) },
		func(q *usPasswordReplaceRequest) { q.target.epoch = usMaxRevision },
		func(q *usPasswordReplaceRequest) { q.target.mustChange = true },
		func(q *usPasswordReplaceRequest) { q.nextHash[5] = '8' },
		func(q *usPasswordReplaceRequest) { q.nextHash[28] = '/' },
		func(q *usPasswordReplaceRequest) { q.previousHash = nil },
	} {
		q := usCredentialTestPassword()
		edit(&q)
		if p, err := prepareUSPasswordReplace(q); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid password preparation accepted")
		}
	}
	old := usCredentialTestPassword()
	old.previousHash[4], old.previousHash[5] = '0', '8'
	if _, err := prepareUSPasswordReplace(old); err != nil {
		t.Fatal("supported older stored cost refused", err)
	}
	for _, edit := range []func(*usCredentialIssueRequest){
		func(q *usCredentialIssueRequest) { q.kind = "integration_session" },
		func(q *usCredentialIssueRequest) { q.name = "\n" },
		func(q *usCredentialIssueRequest) { q.scopes = []string{"read", "read"} },
		func(q *usCredentialIssueRequest) { q.scopes = []string{"unknown"} },
		func(q *usCredentialIssueRequest) { q.secretHash = "plaintext is not a digest" },
		func(q *usCredentialIssueRequest) { q.hint = "" },
		func(q *usCredentialIssueRequest) { q.target.disabled = true },
	} {
		q := usCredentialTestIssue()
		edit(&q)
		if p, err := prepareUSCredentialIssue(q, 1000); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid credential preparation accepted")
		}
	}
	for _, expiry := range []string{"1970-01-01T00:01:01Z", "1981-01-01T00:00:00Z", "bad"} {
		q := usCredentialTestIssue()
		q.expiresAt = expiry
		if p, err := prepareUSCredentialIssue(q, 1000); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid chosen expiry accepted")
		}
	}
	for _, kind := range []string{"token", "app_password"} {
		q := usCredentialTestIssue()
		q.kind = kind
		q.expiresAt = "1970-01-01T00:02:00.123456789Z"
		if kind == "app_password" {
			q.hint = ""
		}
		p, err := prepareUSCredentialIssue(q, 1000)
		want := int64(120000)
		if kind == "app_password" {
			want = 120123
		}
		if err != nil || p.expiresMS != want || p.expirySeconds != 120 || p.expiryNanos != 123456789 {
			t.Fatal("lost exact expiry comparison or existing storage precision", err)
		}
	}
	q := usCredentialTestIssue()
	q.expiresAt = time.UnixMilli(1000 + usCredentialLifetimeMS).UTC().Format(time.RFC3339)
	if _, err := prepareUSCredentialIssue(q, 1000); err != nil {
		t.Fatal("existing maximum chosen lifetime refused", err)
	}
	for _, edit := range []func(*usCredentialRevokeRequest){
		func(q *usCredentialRevokeRequest) { q.secretHash = "" },
		func(q *usCredentialRevokeRequest) { q.family = "" },
		func(q *usCredentialRevokeRequest) { q.scopes = "playback,read" },
		func(q *usCredentialRevokeRequest) { q.createdMS = 0 },
		func(q *usCredentialRevokeRequest) { q.endedReason = "role is authority" },
	} {
		q := usCredentialTestRevoke()
		edit(&q)
		if p, err := prepareUSCredentialRevoke(q); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid revoke manifest accepted")
		}
	}
}

func TestUSCredentialTargetUsesDirectProfileNameRules(t *testing.T) {
	target := usCredentialTestTarget()
	target.profileName = "Profile\u200dname"
	if !validUSCredentialTarget(target) {
		t.Fatal("existing direct profile name rejected")
	}
	target.profileName = strings.Repeat("é", 61)
	if validUSCredentialTarget(target) {
		t.Fatal("profile name byte limit changed to rune limit")
	}
}

func TestUSCredentialAppPasswordRawNameBound(t *testing.T) {
	q := usCredentialTestIssue()
	q.name = strings.Repeat(" ", 79) + "ok"
	if _, err := prepareUSCredentialIssue(q, 1000); err != nil {
		t.Fatal("key/token normalized name policy", err)
	}
	q.kind = "app_password"
	q.hint = ""
	if p, err := prepareUSCredentialIssue(q, 1000); p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("existing app-password raw name bound not applied")
	}
}

func TestUSCredentialCurrentDigestEncoding(t *testing.T) {
	real := identity.Digest("pat.synthetic-only-format-control")
	if len(real) != 43 || !usCredentialDigest(real) {
		t.Fatal("current identity.Digest encoding rejected")
	}
	issue := usCredentialTestIssue()
	issue.secretHash = real
	if _, err := prepareUSCredentialIssue(issue, 1000); err != nil {
		t.Fatal("real issuer digest refused on issue", err)
	}
	revoke := usCredentialTestRevoke()
	revoke.secretHash = real
	if _, err := prepareUSCredentialRevoke(revoke); err != nil {
		t.Fatal("real stored digest refused on revoke", err)
	}
	for _, bad := range []string{real + "=", real[:42], real + "A", strings.Repeat("a", 64), strings.Repeat("!", 43), strings.Repeat("A", 42) + "B"} {
		issue.secretHash = bad
		revoke.secretHash = bad
		if p, err := prepareUSCredentialIssue(issue, 1000); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("noncanonical issue digest accepted")
		}
		if p, err := prepareUSCredentialRevoke(revoke); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("noncanonical revoke digest accepted")
		}
	}
}

func TestUSCredentialNewPasswordRejectsStoredOlderCosts(t *testing.T) {
	for _, cost := range []string{"04", "08"} {
		q := usCredentialTestPassword()
		copy(q.previousHash[4:6], cost)
		if _, err := prepareUSPasswordReplace(q); err != nil {
			t.Fatal("supported stored hash rejected", err)
		}
		copy(q.nextHash[4:6], cost)
		if p, err := prepareUSPasswordReplace(q); p != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("new password accepted at old cost", cost)
		}
	}
}
