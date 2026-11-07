package writer

import (
	"fmt"
	"strings"
	"testing"
)

func profileImageFixture() usMemberProfileImage {
	return usMemberProfileImage{id: "secret-id", name: "secret-name", primary: true, position: 7, art: "secret-art", revision: 9, pinRevision: 8, pinRequired: true, allowedLibraries: []string{"secret-lib", "second"}, restrictions: usMemberProfileRestrictionsImage{profileID: "secret-profile", ratingSystem: "secret-system", maximumAgeRating: "secret-rating", maximumAge: 13, allowUnrated: true, blockedLabels: []string{"�", "�", "last"}, allowDownloads: true, allowLiveTV: true, allowDVR: true, allowWatchTogether: true, revision: 4}, avatar: &usMemberProfileAvatarImage{profileID: "secret-avatar", version: 3, updatedAt: "secret-time", sourceMIME: "secret-mime", url: "secret-url"}}
}
func TestUSMemberProfileImageFields(t *testing.T) {
	identity := CommandIdentity{Sequence: 1}
	identity.Incarnation[0] = 1
	identity.OperationID[0] = 2
	base := profileImageFixture()
	want, err := usMemberProfileImageManifest(identity, 3, base)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*usMemberProfileImage)
	}{
		{"id", func(v *usMemberProfileImage) { v.id += "changed" }},
		{"name", func(v *usMemberProfileImage) { v.name += "changed" }},
		{"art", func(v *usMemberProfileImage) { v.art += "changed" }},
		{"primary", func(v *usMemberProfileImage) { v.primary = !v.primary }},
		{"position", func(v *usMemberProfileImage) { v.position++ }},
		{"revision", func(v *usMemberProfileImage) { v.revision++ }},
		{"pinRevision", func(v *usMemberProfileImage) { v.pinRevision++ }},
		{"pinRequired", func(v *usMemberProfileImage) { v.pinRequired = !v.pinRequired }},
		{"restrictions.profileID", func(v *usMemberProfileImage) { v.restrictions.profileID += "changed" }},
		{"restrictions.ratingSystem", func(v *usMemberProfileImage) { v.restrictions.ratingSystem += "changed" }},
		{"restrictions.maximumAgeRating", func(v *usMemberProfileImage) { v.restrictions.maximumAgeRating += "changed" }},
		{"restrictions.maximumAge", func(v *usMemberProfileImage) { v.restrictions.maximumAge++ }},
		{"restrictions.allowUnrated", func(v *usMemberProfileImage) { v.restrictions.allowUnrated = !v.restrictions.allowUnrated }},
		{"restrictions.allowDownloads", func(v *usMemberProfileImage) { v.restrictions.allowDownloads = !v.restrictions.allowDownloads }},
		{"restrictions.allowLiveTV", func(v *usMemberProfileImage) { v.restrictions.allowLiveTV = !v.restrictions.allowLiveTV }},
		{"restrictions.allowDVR", func(v *usMemberProfileImage) { v.restrictions.allowDVR = !v.restrictions.allowDVR }},
		{"restrictions.allowWatchTogether", func(v *usMemberProfileImage) { v.restrictions.allowWatchTogether = !v.restrictions.allowWatchTogether }},
		{"restrictions.revision", func(v *usMemberProfileImage) { v.restrictions.revision++ }},
		{"avatar.profileID", func(v *usMemberProfileImage) { v.avatar.profileID += "changed" }},
		{"avatar.version", func(v *usMemberProfileImage) { v.avatar.version++ }},
		{"avatar.updatedAt", func(v *usMemberProfileImage) { v.avatar.updatedAt += "changed" }},
		{"avatar.sourceMIME", func(v *usMemberProfileImage) { v.avatar.sourceMIME += "changed" }},
		{"avatar.url", func(v *usMemberProfileImage) { v.avatar.url += "changed" }},
		{"libraries", func(v *usMemberProfileImage) { v.allowedLibraries[0] = "changed" }},
		{"libraryOrder", func(v *usMemberProfileImage) {
			v.allowedLibraries[0], v.allowedLibraries[1] = v.allowedLibraries[1], v.allowedLibraries[0]
		}},
		{"label", func(v *usMemberProfileImage) { v.restrictions.blockedLabels[0] = "changed" }},
		{"labelOrder", func(v *usMemberProfileImage) {
			v.restrictions.blockedLabels[0], v.restrictions.blockedLabels[2] = v.restrictions.blockedLabels[2], v.restrictions.blockedLabels[0]
		}},
		{"labelDuplicate", func(v *usMemberProfileImage) { v.restrictions.blockedLabels = v.restrictions.blockedLabels[1:] }},
		{"avatarPresence", func(v *usMemberProfileImage) { v.avatar = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := cloneUSMemberProfileImage(base)
			tc.mutate(&v)
			got, e := usMemberProfileImageManifest(identity, 3, v)
			if e != nil || got == want {
				t.Fatalf("field not bound: %v", e)
			}
		})
	}
	for i := 0; i < 4; i++ {
		id := identity
		ordinal := uint64(3)
		switch i {
		case 0:
			id.Incarnation[0]++
		case 1:
			id.OperationID[0]++
		case 2:
			id.Sequence++
		case 3:
			ordinal++
		}
		got, e := usMemberProfileImageManifest(id, ordinal, base)
		if e != nil || got == want {
			t.Fatal("identity/ordinal not bound")
		}
	}
	if got, _ := usMemberProfileImageManifest(identity, 3, base); got != want {
		t.Fatal("clone mutated original")
	}
}
func TestUSMemberProfileImagePresenceAndClone(t *testing.T) {
	for _, labels := range []bool{false, true} {
		a := profileImageFixture()
		if labels {
			a.restrictions.blockedLabels = nil
		} else {
			a.allowedLibraries = nil
		}
		b := cloneUSMemberProfileImage(a)
		if labels {
			b.restrictions.blockedLabels = []string{}
		} else {
			b.allowedLibraries = []string{}
		}
		x, _ := usMemberProfileImageManifest(CommandIdentity{}, 0, a)
		y, _ := usMemberProfileImageManifest(CommandIdentity{}, 0, b)
		if x == y {
			t.Fatal("nil and empty collapsed")
		}
		c := cloneUSMemberProfileImage(b)
		if c.allowedLibraries == nil || c.restrictions.blockedLabels == nil {
			t.Fatal("empty lost")
		}
	}
	a := profileImageFixture()
	b := cloneUSMemberProfileImage(a)
	b.allowedLibraries[0] = "change"
	b.restrictions.blockedLabels[0] = "change"
	b.avatar.url = "change"
	if a.allowedLibraries[0] == "change" || a.restrictions.blockedLabels[0] == "change" || a.avatar.url == "change" {
		t.Fatal("alias")
	}
}
func TestUSMemberProfileImageRedaction(t *testing.T) {
	v := profileImageFixture()
	for _, held := range []any{v, &v, v.restrictions, &v.restrictions, *v.avatar, v.avatar} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%20.4v"} {
			s := fmt.Sprintf(verb, held)
			if strings.Contains(s, "secret") || !strings.Contains(s, "redacted") {
				t.Fatalf("format leaked: %s", s)
			}
		}
	}
}
func TestUSMemberProfileImageNativeData(t *testing.T) {
	v := profileImageFixture()
	v.name = string([]byte{255})
	v.restrictions.maximumAge = -1
	v.allowedLibraries = make([]string, 1000)
	v.restrictions.blockedLabels = make([]string, 1000)
	if _, e := usMemberProfileImageManifest(CommandIdentity{}, 0, v); e != nil {
		t.Fatal(e)
	}
	v.name = strings.Repeat("x", maxCanonicalRequestBytes)
	if _, e := usMemberProfileImageManifest(CommandIdentity{}, 0, v); e == nil {
		t.Fatal("staging bound missing")
	}
}
