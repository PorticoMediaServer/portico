package writer

import (
	"strings"
	"testing"
)

func TestUSSecurityFrozenCommandScopeAndSecretProjection(t *testing.T) {
	identity, policy := usPolicyFixture()
	pin := usPINTestRequest()
	command, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, policy.memberIncarnation, pin)
	if err != nil {
		t.Fatal(err)
	}
	pin.hash[7] = 'A'
	if string(command.request.prepared.request.hash) != usPINFixtureHash {
		t.Fatal("caller hash mutation escaped preparation")
	}
	other, err := prepareUSPINCommand(usPINSet, identity, policy.ownerID, policy.issuer, strings.Repeat("f", 32), usPINTestRequest())
	if err != nil || other.digest == command.digest {
		t.Fatal("target membership lifetime omitted", err)
	}
	changed := command.request
	changed.issuer.installation = strings.Repeat("e", 32)
	digest, err := canonicalRequestDigest(usPINSet, 1, identity, changed)
	if err != nil || digest == command.digest {
		t.Fatal("issuer installation omitted", err)
	}
	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationCreate(i, scope, "invitation", usInvitationFixtureHash, usInvitationCreateInput{libraries: []string{"movies"}})
	if err != nil {
		t.Fatal(err)
	}
	invitation, err := freezeUSInvitation(p)
	if err != nil {
		t.Fatal(err)
	}
	p.libraries[0] = "changed"
	if invitation.request.libraries[0] != "movies" {
		t.Fatal("retained preparation aliased invitation command")
	}
	changedInvite := invitation.request
	changedInvite.codeHash = strings.Repeat("b", 64)
	digest, err = canonicalRequestDigest(changedInvite.kind, 1, changedInvite.identity, changedInvite)
	if err != nil || digest == invitation.digest {
		t.Fatal("generated secret manifest omitted", err)
	}
	changedInvite = invitation.request
	changedInvite.librariesJSON = `["other"]`
	digest, err = canonicalRequestDigest(changedInvite.kind, 1, changedInvite.identity, changedInvite)
	if err != nil || digest == invitation.digest {
		t.Fatal("cached SQL projection omitted", err)
	}
}
