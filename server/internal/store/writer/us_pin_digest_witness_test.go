package writer

import (
	"slices"
	"strings"
	"testing"
)

func TestUSPINDigestEveryWitness(t *testing.T) {
	identity, policy := usPolicyFixture()
	pin := usPINTestRequest()
	original, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, policy.memberIncarnation, pin)
	if err != nil {
		t.Fatal(err)
	}
	type candidate struct {
		identity CommandIdentity
		owner    string
		issuer   usPolicyIssuer
		member   string
		pin      usPINRequest
	}
	changes := []struct {
		name   string
		change func(*candidate)
	}{
		{"identity.incarnation", func(q *candidate) { q.identity.Incarnation[1] = 1 }},
		{"identity.sequence", func(q *candidate) { q.identity.Sequence++ }},
		{"identity.operation", func(q *candidate) { q.identity.OperationID[1] = 1 }},
		{"owner", func(q *candidate) { q.owner += "-other" }},
		{"issuer.server", func(q *candidate) { q.issuer.server += "-other" }},
		{"issuer.account", func(q *candidate) { q.issuer.account += "-other" }},
		{"issuer.profile", func(q *candidate) { q.issuer.profile += "-other" }},
		{"issuer.family", func(q *candidate) { q.issuer.family += "-other" }},
		{"issuer.member", func(q *candidate) { q.issuer.memberIncarnation = strings.Repeat("f", 32) }},
		{"issuer.profile_lifetime", func(q *candidate) { q.issuer.profileLifetime = strings.Repeat("f", 32) }},
		{"issuer.installation", func(q *candidate) { q.issuer.installation = strings.Repeat("f", 32) }},
		{"issuer.token", func(q *candidate) { q.issuer.tokenDigest = usCredentialFixtureHash("us-other-issuer-token") }},
		{"issuer.epoch", func(q *candidate) { q.issuer.epoch++ }},
		{"issuer.generation", func(q *candidate) { q.issuer.familyGeneration++ }},
		{"issuer.authority_generation", func(q *candidate) { q.issuer.authorityGeneration++ }},
		{"target.member", func(q *candidate) { q.member = strings.Repeat("a", 32) }},
		{"target.account", func(q *candidate) { q.pin.account += "-other" }},
		{"target.profile", func(q *candidate) { q.pin.profile += "-other" }},
		{"target.lifetime", func(q *candidate) { q.pin.profileLifetime = strings.Repeat("a", 32) }},
		{"target.revision", func(q *candidate) { q.pin.expectedRevision++ }},
		{"target.pin_revision", func(q *candidate) { q.pin.expectedPINRevision++ }},
		{"hash.manifest", func(q *candidate) { q.pin.hash[7] = 'A' }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			q := candidate{identity, policy.ownerID, policy.issuer, policy.memberIncarnation, pin}
			q.pin.hash = slices.Clone(pin.hash)
			test.change(&q)
			other, err := prepareUSPINCommand(usPINSet, q.identity, q.owner, q.issuer, q.member, q.pin)
			if err != nil {
				t.Fatal(err)
			}
			if other.digest == original.digest {
				t.Fatal("constructor omitted changed witness")
			}
			want, err := canonicalRequestDigest(other.request.prepared.kind, 1, other.identity, other.request)
			if err != nil || want != other.digest {
				t.Fatal("digest copy differs from complete frozen request", err)
			}
		})
	}
}
