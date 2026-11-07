package writer

import "testing"

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
		func(v *usPolicySavedResult) { v.libraries = []string{"music", "movies"} },
	} {
		x := v
		change(&x)
		actual, err := usPolicyResultDigest(i, request, x)
		if err != nil || actual == original {
			t.Fatal("native result field omitted", err)
		}
	}
	if bytes, err := usPolicyResultVariableBytes(v); err != nil || bytes <= 0 {
		t.Fatal(bytes, err)
	}
	v.libraries = []string{"movies", "movies"}
	if _, err := usPolicyResultDigest(i, request, v); err == nil {
		t.Fatal("invalid duplicate native result accepted")
	}
}
