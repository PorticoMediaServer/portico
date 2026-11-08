package writer

import (
	"strings"
	"testing"
)

func usPolicyResultFixture(t *testing.T) (CommandIdentity, RequestDigest, usPolicySavedResult) {
	t.Helper()
	i, q := usPolicyFixture()
	command, err := prepareUSLibraryPolicy(USMemberLibraries, i, q)
	if err != nil {
		t.Fatal(err)
	}
	return i, command.digest, usPolicySavedResult{kind: USMemberLibraries, ownerID: q.ownerID, account: q.account, memberIncarnation: q.memberIncarnation, revision: 2, appliedMS: 1000, changed: true, libraries: []string{"movies", "music"}}
}

func TestUSPolicyResultBindsNativeImage(t *testing.T) {
	i, request, v := usPolicyResultFixture(t)
	original, err := usPolicyResultDigest(i, request, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*usPolicySavedResult){
		func(v *usPolicySavedResult) { v.revision++ },
		func(v *usPolicySavedResult) { v.appliedMS++ },
		func(v *usPolicySavedResult) { v.changed = false },
		func(v *usPolicySavedResult) { v.libraries = []string{"music", "movies"} },
		func(v *usPolicySavedResult) { v.ownerID = "other-owner" },
	} {
		x := v
		change(&x)
		actual, err := usPolicyResultDigest(i, request, x)
		if err != nil || actual == original {
			t.Fatal("native result field omitted", err)
		}
	}
	if bytes, err := usPolicyResultVariableBytes(v); err != nil || bytes != 250 {
		t.Fatal(bytes, err)
	}
	v.libraries = []string{"movies", "movies"}
	if _, err := usPolicyResultDigest(i, request, v); err == nil {
		t.Fatal("invalid duplicate native result accepted")
	}
}

// Profile inherit (SQL NULL) and an explicit empty list are different original
// results even though both save an empty ordered cohort. The immutable image
// must keep them distinguishable, and the nil/empty request digest must remain
// the binding authority rather than the current profile row.
func TestUSPolicyResultKeepsNilAndEmptyDistinct(t *testing.T) {
	identity, request := usPolicyFixture()
	request.profile, request.profileLifetime = "profile", strings.Repeat("f", 32)
	inheritCommand, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil {
		t.Fatal(err)
	}
	request.libraries = []string{}
	denyCommand, err := prepareUSLibraryPolicy(USProfileLibraries, identity, request)
	if err != nil || denyCommand.digest == inheritCommand.digest {
		t.Fatal("nil/empty request digest collision", err)
	}
	base := usPolicySavedResult{kind: USProfileLibraries, ownerID: request.ownerID, account: request.account,
		memberIncarnation: request.memberIncarnation, profile: request.profile, profileLifetime: request.profileLifetime,
		revision: 2, appliedMS: 1000}
	inherit, deny := base, base
	inherit.inherits, inherit.libraries = true, nil
	deny.libraries = []string{}
	// Same request digest: only the saved image's inherit flag may separate them.
	one, err := usPolicyResultDigest(identity, inheritCommand.digest, inherit)
	if err != nil {
		t.Fatal(err)
	}
	other, err := usPolicyResultDigest(identity, inheritCommand.digest, deny)
	if err != nil || one == other {
		t.Fatal("inherit/deny images collided", err)
	}
	inheritBytes, err := usPolicyResultVariableBytes(inherit)
	if err != nil {
		t.Fatal(err)
	}
	denyBytes, err := usPolicyResultVariableBytes(deny)
	if err != nil || denyBytes != inheritBytes {
		t.Fatal("empty cohort changed the fixed image charge", inheritBytes, denyBytes, err)
	}
}

// Only profile inherit saves SQL NULL. A member image is always an explicit
// list, so a nil non-inherit cohort must never be minted or accepted.
func TestUSPolicyResultRejectsNilNonInherit(t *testing.T) {
	identity, request, v := usPolicyResultFixture(t)
	v.libraries = nil
	if _, err := usPolicyResultDigest(identity, request, v); err == nil {
		t.Fatal("member result with nil library cohort accepted")
	}
	profileIdentity, profileRequest := usPolicyFixture()
	profileRequest.profile, profileRequest.profileLifetime = "profile", strings.Repeat("f", 32)
	command, err := prepareUSLibraryPolicy(USProfileLibraries, profileIdentity, profileRequest)
	if err != nil {
		t.Fatal(err)
	}
	profile := usPolicySavedResult{kind: USProfileLibraries, ownerID: profileRequest.ownerID, account: profileRequest.account,
		memberIncarnation: profileRequest.memberIncarnation, profile: profileRequest.profile, profileLifetime: profileRequest.profileLifetime,
		revision: 2, appliedMS: 1000}
	if _, err = usPolicyResultDigest(profileIdentity, command.digest, profile); err == nil {
		t.Fatal("profile deny with nil library cohort accepted")
	}
}

func TestUSPolicyResultRejectsUnboundIdentity(t *testing.T) {
	i, request, v := usPolicyResultFixture(t)
	if _, err := usPolicyResultDigest(CommandIdentity{Sequence: 1}, request, v); err == nil {
		t.Fatal("zero incarnation accepted")
	}
	if _, err := usPolicyResultDigest(i, RequestDigest{}, v); err == nil {
		t.Fatal("zero request digest accepted")
	}
	v.kind = 999
	if _, err := usPolicyResultDigest(i, request, v); err == nil {
		t.Fatal("closed codec accepted an unregistered purpose")
	}
}
