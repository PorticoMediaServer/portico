package writer

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func usPolicyFixture() (CommandIdentity, usLibraryPolicyRequest) {
	return CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}, usLibraryPolicyRequest{
		ownerID: "security-owner", issuer: usPolicyIssuer{
			server: "server", account: "issuer", profile: "issuer-profile", family: "issuer-family",
			memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32),
			installation: strings.Repeat("c", 32), tokenDigest: usCredentialFixtureHash("us-issuer-token"),
			epoch: 1, familyGeneration: 1, authorityGeneration: 1,
		},
		account: "target", memberIncarnation: strings.Repeat("e", 32), expectedRevision: 1,
		libraries: []string{"movies", "music"},
	}
}

func TestUSCommandPurposes(t *testing.T) {
	for _, expected := range []Descriptor{
		{ID: USMemberLibraries, Name: "us_member_libraries", Durability: Full},
		{ID: USProfileLibraries, Name: "us_profile_libraries", Durability: Full},
	} {
		actual, err := Describe(expected.ID)
		if err != nil || actual != expected || actual.ID < 500 || actual.ID > 599 {
			t.Fatal(actual, err)
		}
	}
}

func TestUSPolicyFrozenInput(t *testing.T) {
	identity, request := usPolicyFixture()
	command, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	original := command.digest
	request.libraries[0] = "changed"
	request.issuer.account = "changed"
	if command.request.libraries[0] != "movies" || command.request.issuer.account != "issuer" {
		t.Fatal("caller mutation escaped frozen command")
	}
	again, err := canonicalRequestDigest(command.kind, 1, command.identity, command.request)
	if err != nil || again != original {
		t.Fatal("frozen request digest changed", err)
	}
	encoded, err := command.policyJSON()
	if err != nil || encoded != `["movies","music"]` {
		t.Fatal(encoded, err)
	}
}

func TestUSPolicyInheritance(t *testing.T) {
	identity, request := usPolicyFixture()
	request.profile, request.profileLifetime = "profile", strings.Repeat("f", 32)
	request.libraries = nil
	inherit, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	value, err := inherit.policyJSON()
	if err != nil || value != nil {
		t.Fatal("inherit must bind SQL NULL", value, err)
	}
	request.libraries = []string{}
	deny, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	value, err = deny.policyJSON()
	if err != nil || value != "[]" || deny.digest == inherit.digest {
		t.Fatal("explicit deny lost nil/empty distinction", value, err)
	}
	request.profile, request.profileLifetime, request.libraries = "", "", nil
	member, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	value, err = member.policyJSON()
	if err != nil || value != "[]" {
		t.Fatal("nil member list must deny", value, err)
	}
}

func TestUSPolicyAdmission(t *testing.T) {
	cases := []struct {
		name string
		edit func(*CommandIdentity, *usLibraryPolicyRequest)
		want error
	}{
		{"zero_incarnation", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.Incarnation = [16]byte{} }, ErrInvalid},
		{"zero_operation", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.OperationID = [16]byte{} }, ErrInvalid},
		{"zero_sequence", func(i *CommandIdentity, _ *usLibraryPolicyRequest) { i.Sequence = 0 }, ErrInvalid},
		{"retargeted_profile", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.profile = "profile" }, ErrInvalid},
		{"invalid_lifetime", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.memberIncarnation = strings.Repeat("x", 32) }, ErrInvalid},
		{"revision_overflow", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.expectedRevision = usMaxRevision }, ErrInvalid},
		{"missing_issuer", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.issuer = usPolicyIssuer{} }, ErrInvalid},
		{"raw_cap", func(_ *CommandIdentity, q *usLibraryPolicyRequest) {
			q.libraries = make([]string, usMaxPolicyLibraries+1)
		}, ErrCap},
		{"duplicate", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = []string{"movies", "movies"} }, ErrInvalid},
		{"private_error", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = []string{"secret/private/path"} }, ErrInvalid},
		{"oversize_id", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = []string{strings.Repeat("a", 129)} }, ErrInvalid},
		{"invalid_utf8", func(_ *CommandIdentity, q *usLibraryPolicyRequest) { q.libraries = []string{string([]byte{0xff})} }, ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i, q := usPolicyFixture()
			tc.edit(&i, &q)
			command, err := prepareUSLibraryPolicy(USMemberLibraries, i, q)
			if command != nil || !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret/private/path") {
				t.Fatal(command, err)
			}
		})
	}
	identity, request := usPolicyFixture()
	request.libraries = make([]string, usMaxPolicyLibraries)
	for n := range request.libraries {
		request.libraries[n] = fmt.Sprintf("library-%d", n)
	}
	if _, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request); err != nil {
		t.Fatal("existing legal raw envelope refused", err)
	}
	if command, err := prepareUSLibraryPolicy(SecurityAccept, identity, request); command != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("WC purpose admitted by US preparation", command, err)
	}
}

// Retargeting a command or presenting another issuer/owner must not reuse an
// otherwise identical policy's receipt identity.
func TestUSPolicyDigestBindsSecurityScope(t *testing.T) {
	identity, request := usPolicyFixture()
	original, err := prepareUSLibraryPolicy(USMemberLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	changes := []func(*usLibraryPolicyRequest){
		func(q *usLibraryPolicyRequest) { q.ownerID += "x" },
		func(q *usLibraryPolicyRequest) { q.issuer.memberIncarnation = strings.Repeat("f", 32) },
		func(q *usLibraryPolicyRequest) { q.account += "x" },
	}
	for n, change := range changes {
		q := request
		change(&q)
		other, err := prepareUSLibraryPolicy(USMemberLibraries, identity, q)
		if err != nil || other.digest == original.digest {
			t.Fatal("security scope omitted", n, err)
		}
	}
}

func TestUSIssuerIdentityDigestEncoding(t *testing.T) {
	real := usCredentialFixtureHash("us-real-digest-control")
	for _, digest := range []string{real, real + "=", real[:42], real + "A", strings.Repeat("a", 64), strings.Repeat("!", 43), strings.Repeat("A", 42) + "B", real[:20] + "\n" + real[21:]} {
		t.Run(digest, func(t *testing.T) {
			id, q := usPolicyFixture()
			q.issuer.tokenDigest = digest
			good := digest == real
			if usIdentityDigest(digest) != good || validUSSecurityIdentity(id, q.ownerID, q.issuer) != good {
				t.Fatal("issuer digest encoding")
			}
			command, err := prepareUSLibraryPolicy(USMemberLibraries, id, q)
			if good && (err != nil || command == nil) || !good && (command != nil || !errors.Is(err, ErrInvalid)) {
				t.Fatal("policy digest encoding", err)
			}
			proof, err := prepareUSOwnerAuthority(usOwnerAuthorityWitness{issuer: q.issuer, device: "device", installationID: "installation", tokenExpiry: "2026-11-07T12:00:00Z", horizon: "2026-11-08T12:00:00Z"})
			if good && (err != nil || proof == nil) || !good && (proof != nil || !errors.Is(err, ErrInvalid)) {
				t.Fatal("authority digest encoding", err)
			}
		})
	}
}
