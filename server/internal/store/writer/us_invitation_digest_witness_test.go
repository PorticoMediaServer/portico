package writer

import (
	"strings"
	"testing"
)

// TestUSInvitationDigestEveryIssuerField proves the frozen preparation's 11
// issuer fields are inside the canonical request digest. The frozen document
// embeds the prepared scope, so no extra copy is needed; changing one field must
// change the digest and must also equal a fresh canonical digest.
func TestUSInvitationDigestEveryIssuerField(t *testing.T) {
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	changes := []struct {
		name   string
		change func(*usPolicyIssuer)
	}{
		{"server", func(i *usPolicyIssuer) { i.server += "-other" }},
		{"account", func(i *usPolicyIssuer) { i.account += "-other" }},
		{"profile", func(i *usPolicyIssuer) { i.profile += "-other" }},
		{"family", func(i *usPolicyIssuer) { i.family += "-other" }},
		{"member_incarnation", func(i *usPolicyIssuer) { i.memberIncarnation = strings.Repeat("d", 32) }},
		{"profile_lifetime", func(i *usPolicyIssuer) { i.profileLifetime = strings.Repeat("e", 32) }},
		{"installation", func(i *usPolicyIssuer) { i.installation = strings.Repeat("f", 32) }},
		{"token_digest", func(i *usPolicyIssuer) { i.tokenDigest = usCredentialFixtureHash("us-other-token") }},
		{"epoch", func(i *usPolicyIssuer) { i.epoch++ }},
		{"family_generation", func(i *usPolicyIssuer) { i.familyGeneration++ }},
		{"authority_generation", func(i *usPolicyIssuer) { i.authorityGeneration++ }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			other := *cmd
			prepared := *cmd.prepared
			test.change(&prepared.scope.issuer)
			other.prepared = &prepared
			digest, err := usInvitationCommandDigest(&other)
			if err != nil {
				t.Fatal(err)
			}
			if digest == cmd.digest {
				t.Fatal("digest omitted a frozen issuer field")
			}
			key := cmd.protected.keyID
			frozen := usInvitationFrozenDocument{Actor: other.actor, SignInKind: other.signIn.kind,
				SignInRevision: other.signIn.revision, Prepared: prepared, CodeKeyID: &key,
				CodeNonce: cmd.protected.nonce, CodeBody: cmd.protected.body}
			want, err := canonicalRequestDigest(other.kind, 1, other.identity, frozen)
			if err != nil || want != digest {
				t.Fatal("digest copy differs from the complete frozen request", err)
			}
		})
	}
}
