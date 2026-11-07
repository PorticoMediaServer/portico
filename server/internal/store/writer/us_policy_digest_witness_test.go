package writer

import (
	identitydigest "github.com/PorticoMediaServer/portico/server/internal/identity"
	"slices"
	"strings"
	"testing"
)

func TestUSPolicyDigestEveryWitness(t *testing.T) {
	identity, request := usPolicyFixture()
	request.profile, request.profileLifetime = "profile", strings.Repeat("f", 32)
	original, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		change func(*CommandIdentity, *usLibraryPolicyRequest)
	}{
		{"identity.incarnation", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.Incarnation[1] = 1 }},
		{"identity.sequence", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.Sequence++ }},
		{"identity.operation", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.OperationID[1] = 1 }},
		{"owner", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.ownerID += "-other" }},
		{"issuer.server", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.server += "-other" }},
		{"issuer.account", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.account += "-other" }},
		{"issuer.profile", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.profile += "-other" }},
		{"issuer.family", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.family += "-other" }},
		{"issuer.member", func(_ *CommandIdentity, q *usLibraryPolicyRequest) {
			q.issuer.memberIncarnation = strings.Repeat("f", 32)
		}},
		{"issuer.profile_lifetime", func(_ *CommandIdentity, q *usLibraryPolicyRequest) {
			q.issuer.profileLifetime = strings.Repeat("f", 32)
		}},
		{"issuer.installation", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.installation = strings.Repeat("f", 32) }},
		{"issuer.token", func(_ *CommandIdentity, q *usLibraryPolicyRequest) {
			q.issuer.tokenDigest = identitydigest.Digest("us-other-issuer-token")
		}},
		{"issuer.epoch", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.epoch++ }},
		{"issuer.generation", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.familyGeneration++ }},
		{"issuer.authority_generation", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer.authorityGeneration++ }},
		{"target.account", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.account += "-other" }},
		{"target.member", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.memberIncarnation = strings.Repeat("a", 32) }},
		{"target.profile", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.profile += "-other" }},
		{"target.lifetime", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.profileLifetime = strings.Repeat("a", 32) }},
		{"target.revision", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.expectedRevision++ }},
		{"libraries.value", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries[0] = "other" }},
		{"libraries.order", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { slices.Reverse(q.libraries) }},
		{"libraries.empty", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = []string{} }},
		{"libraries.inherit", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = nil }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			i, q := identity, request
			q.libraries = slices.Clone(request.libraries)
			test.change(&i, &q)
			other, err := prepareUSLibraryPolicy(USProfileLibraries, i, q)
			if err != nil {
				t.Fatal(err)
			}
			if other.digest == original.digest {
				t.Fatal("constructor omitted changed witness")
			}
			want, err := canonicalRequestDigest(other.kind, 1, other.identity, other.request)
			if err != nil || want != other.digest {
				t.Fatal("digest copy differs from complete frozen request", err)
			}
		})
	}
}
