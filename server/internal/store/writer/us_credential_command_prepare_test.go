package writer

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

type usCredentialCommandCandidate struct {
	identity CommandIdentity
	owner    string
	issuer   usPolicyIssuer
	password usPasswordReplaceRequest
	issue    usCredentialIssueRequest
	revoke   usCredentialRevokeRequest
}

func usCredentialCommandFixture() usCredentialCommandCandidate {
	i, p := usPolicyFixture()
	return usCredentialCommandCandidate{i, p.ownerID, p.issuer, usCredentialTestPassword(), usCredentialTestIssue(), usCredentialTestRevoke()}
}
func TestUSCredentialCommandWitnesses(t *testing.T) {
	changes := []struct {
		name   string
		change func(*usCredentialCommandCandidate)
		only   string
	}{
		{"incarnation", func(q *usCredentialCommandCandidate) { q.identity.Incarnation[1]++ }, ""},
		{"operation", func(q *usCredentialCommandCandidate) { q.identity.OperationID[1]++ }, ""},
		{"sequence", func(q *usCredentialCommandCandidate) { q.identity.Sequence++ }, ""},
		{"owner", func(q *usCredentialCommandCandidate) { q.owner += "-other" }, ""},
		{"issuer.server", func(q *usCredentialCommandCandidate) { q.issuer.server += "-other" }, ""},
		{"issuer.account", func(q *usCredentialCommandCandidate) { q.issuer.account += "-other" }, ""},
		{"issuer.profile", func(q *usCredentialCommandCandidate) { q.issuer.profile += "-other" }, ""},
		{"issuer.family", func(q *usCredentialCommandCandidate) { q.issuer.family += "-other" }, ""},
		{"issuer.memberIncarnation", func(q *usCredentialCommandCandidate) { q.issuer.memberIncarnation = strings.Repeat("f", 32) }, ""},
		{"issuer.profileLifetime", func(q *usCredentialCommandCandidate) { q.issuer.profileLifetime = strings.Repeat("f", 32) }, ""},
		{"issuer.installation", func(q *usCredentialCommandCandidate) { q.issuer.installation = strings.Repeat("f", 32) }, ""},
		{"issuer.tokenDigest", func(q *usCredentialCommandCandidate) { q.issuer.tokenDigest = strings.Repeat("f", 64) }, ""},
		{"issuer.epoch", func(q *usCredentialCommandCandidate) { q.issuer.epoch++ }, ""},
		{"issuer.familyGeneration", func(q *usCredentialCommandCandidate) { q.issuer.familyGeneration++ }, ""},
		{"issuer.authorityGeneration", func(q *usCredentialCommandCandidate) { q.issuer.authorityGeneration++ }, ""},
		{"target.account", func(q *usCredentialCommandCandidate) {
			q.password.target.account += "-other"
			q.issue.target.account += "-other"
			q.revoke.target.account += "-other"
		}, ""},
		{"target.profile", func(q *usCredentialCommandCandidate) {
			q.password.target.profile += "-other"
			q.issue.target.profile += "-other"
			q.revoke.target.profile += "-other"
		}, ""},
		{"target.username", func(q *usCredentialCommandCandidate) {
			q.password.target.username += "-other"
			q.issue.target.username += "-other"
			q.revoke.target.username += "-other"
		}, ""},
		{"target.profileName", func(q *usCredentialCommandCandidate) {
			q.password.target.profileName += "-other"
			q.issue.target.profileName += "-other"
			q.revoke.target.profileName += "-other"
		}, ""},
		{"target.memberIncarnation", func(q *usCredentialCommandCandidate) {
			q.password.target.memberIncarnation = strings.Repeat("f", 32)
			q.issue.target.memberIncarnation = strings.Repeat("f", 32)
			q.revoke.target.memberIncarnation = strings.Repeat("f", 32)
		}, ""},
		{"target.profileLifetime", func(q *usCredentialCommandCandidate) {
			q.password.target.profileLifetime = strings.Repeat("f", 32)
			q.issue.target.profileLifetime = strings.Repeat("f", 32)
			q.revoke.target.profileLifetime = strings.Repeat("f", 32)
		}, ""},
		{"target.epoch", func(q *usCredentialCommandCandidate) {
			q.password.target.epoch++
			q.issue.target.epoch++
			q.revoke.target.epoch++
		}, ""},
		{"target.memberRevision", func(q *usCredentialCommandCandidate) {
			q.password.target.memberRevision++
			q.issue.target.memberRevision++
			q.revoke.target.memberRevision++
		}, ""},
		{"target.profileRevision", func(q *usCredentialCommandCandidate) {
			q.password.target.profileRevision++
			q.issue.target.profileRevision++
			q.revoke.target.profileRevision++
		}, ""},
		{"password.previousHash", func(q *usCredentialCommandCandidate) { q.password.previousHash[7] = 'A' }, "password"},
		{"password.nextHash", func(q *usCredentialCommandCandidate) { q.password.nextHash[7] = 'A' }, "password"},
		{"issue.id", func(q *usCredentialCommandCandidate) { q.issue.id += "-other" }, "issue"},
		{"issue.kind", func(q *usCredentialCommandCandidate) { q.issue.kind = "key" }, "issue"},
		{"issue.name", func(q *usCredentialCommandCandidate) { q.issue.name += "-other" }, "issue"},
		{"issue.scopes", func(q *usCredentialCommandCandidate) { q.issue.scopes = []string{"admin"} }, "issue"},
		{"issue.secretHash", func(q *usCredentialCommandCandidate) { q.issue.secretHash = strings.Repeat("f", 64) }, "issue"},
		{"issue.hint", func(q *usCredentialCommandCandidate) { q.issue.hint = "ghijkl" }, "issue"},
		{"issue.family", func(q *usCredentialCommandCandidate) { q.issue.family += "-other" }, "issue"},
		{"issue.device", func(q *usCredentialCommandCandidate) { q.issue.device += "-other" }, "issue"},
		{"revoke.id", func(q *usCredentialCommandCandidate) { q.revoke.id += "-other" }, "revoke"},
		{"revoke.kind", func(q *usCredentialCommandCandidate) { q.revoke.kind = "key" }, "revoke"},
		{"revoke.name", func(q *usCredentialCommandCandidate) { q.revoke.name += "-other" }, "revoke"},
		{"revoke.scopes", func(q *usCredentialCommandCandidate) { q.revoke.scopes = "admin" }, "revoke"},
		{"revoke.secretHash", func(q *usCredentialCommandCandidate) { q.revoke.secretHash = strings.Repeat("f", 64) }, "revoke"},
		{"revoke.hint", func(q *usCredentialCommandCandidate) { q.revoke.hint = "ghijkl" }, "revoke"},
		{"revoke.family", func(q *usCredentialCommandCandidate) { q.revoke.family += "-other" }, "revoke"},
		{"revoke.device", func(q *usCredentialCommandCandidate) { q.revoke.device += "-other" }, "revoke"},
		{"issue.expiresAt", func(q *usCredentialCommandCandidate) { q.issue.expiresAt = "1970-01-01T00:02:00.123456789Z" }, "issue"},
		{"issue.expiryNanos", func(q *usCredentialCommandCandidate) { q.issue.expiresAt = "1970-01-01T00:02:00.123456788Z" }, "issue"},
		{"revoke.createdMS", func(q *usCredentialCommandCandidate) { q.revoke.createdMS++ }, "revoke"},
		{"revoke.expiresMS", func(q *usCredentialCommandCandidate) { q.revoke.expiresMS++ }, "revoke"},
		{"revoke.revokedMS", func(q *usCredentialCommandCandidate) { q.revoke.revokedMS++ }, "revoke"},
		{"revoke.endedReason", func(q *usCredentialCommandCandidate) { q.revoke.endedReason = "unavailable" }, "revoke"},
		{"revoke.target.disabled", func(q *usCredentialCommandCandidate) { q.revoke.target.disabled = true }, "revoke"},
		{"revoke.target.deleted", func(q *usCredentialCommandCandidate) { q.revoke.target.deleted = true }, "revoke"},
		{"revoke.target.mustChange", func(q *usCredentialCommandCandidate) { q.revoke.target.mustChange = true }, "revoke"},
	}
	for _, family := range []string{"password", "issue", "revoke"} {
		t.Run(family, func(t *testing.T) {
			build := func(q usCredentialCommandCandidate) RequestDigest {
				t.Helper()
				switch family {
				case "password":
					p, e := prepareUSPasswordReplaceCommand(q.identity, q.owner, q.issuer, q.password)
					if e != nil {
						t.Fatal(e)
					}
					return p.digest
				case "issue":
					p, e := prepareUSCredentialIssueCommand(q.identity, q.owner, q.issuer, q.issue, 1000)
					if e != nil {
						t.Fatal(e)
					}
					return p.digest
				default:
					p, e := prepareUSCredentialRevokeCommand(q.identity, q.owner, q.issuer, q.revoke)
					if e != nil {
						t.Fatal(e)
					}
					return p.digest
				}
			}
			base := usCredentialCommandFixture()
			original := build(base)
			for _, tc := range changes {
				if tc.only != "" && tc.only != family {
					continue
				}
				t.Run(tc.name, func(t *testing.T) {
					q := usCredentialCommandFixture()
					tc.change(&q)
					if build(q) == original {
						t.Fatal("constructor omitted witness")
					}
					if build(base) != original {
						t.Fatal("source changed")
					}
				})
			}
		})
	}
}
func TestUSCredentialCommandFreezeAndRedaction(t *testing.T) {
	q := usCredentialCommandFixture()
	p, e := prepareUSPasswordReplaceCommand(q.identity, q.owner, q.issuer, q.password)
	if e != nil {
		t.Fatal(e)
	}
	i, e := prepareUSCredentialIssueCommand(q.identity, q.owner, q.issuer, q.issue, 1000)
	if e != nil {
		t.Fatal(e)
	}
	r, e := prepareUSCredentialRevokeCommand(q.identity, q.owner, q.issuer, q.revoke)
	if e != nil {
		t.Fatal(e)
	}
	q.password.previousHash[7] = 'A'
	q.password.nextHash[7] = 'A'
	q.issue.scopes[0] = "admin"
	if !bytes.Equal(p.request.prepared.request.previousHash, []byte(usCredentialFixturePassword)) || !bytes.Equal(p.request.prepared.request.nextHash, []byte(usCredentialFixturePassword)) || i.request.prepared.scopes != "read,playback" {
		t.Fatal("mutable source aliases frozen request")
	}
	for _, v := range []any{*p, p.request, *i, i.request, *r, r.request} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if !strings.Contains(fmt.Sprintf(verb, v), "redacted") {
				t.Fatal("format exposed request")
			}
		}
	}
	for _, bad := range []usCredentialCommandCandidate{{}, {identity: q.identity, owner: q.owner}} {
		if p, e := prepareUSPasswordReplaceCommand(bad.identity, bad.owner, bad.issuer, q.password); e == nil || p != nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
func TestUSCredentialCommandExpiryPrecision(t *testing.T) {
	q := usCredentialCommandFixture()
	q.issue.expiresAt = "1970-01-01T00:02:00.123456789Z"
	a, e := prepareUSCredentialIssueCommand(q.identity, q.owner, q.issuer, q.issue, 1000)
	if e != nil {
		t.Fatal(e)
	}
	q.issue.expiresAt = "1970-01-01T00:02:00.123456788Z"
	b, e := prepareUSCredentialIssueCommand(q.identity, q.owner, q.issuer, q.issue, 1000)
	if e != nil {
		t.Fatal(e)
	}
	if a.digest == b.digest || a.request.prepared.expiresMS != b.request.prepared.expiresMS {
		t.Fatal("exact expiry not bound independently of SQL projection")
	}
}
