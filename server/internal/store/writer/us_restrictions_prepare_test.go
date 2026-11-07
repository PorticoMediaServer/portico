package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
	"strings"
	"testing"
)

func restrictionsFixture() (usProfileCommandInput, usRestrictionsTarget, usRestrictionsEdit) {
	v := usProfileCommandFixture()
	return v, usRestrictionsTarget{profile: v.target(), present: true, revision: 3}, usRestrictionsEdit{ExpectedRevision: 3, RatingSystem: "mpaa", MaximumAgeRating: "PG-13", BlockedLabels: []string{" Zebra ", "alpha", "ALPHA"}, AllowUnrated: true, AllowDownloads: true, AllowLiveTV: true, AllowWatchTogether: true}
}
func TestUSRestrictionsScopeDigest(t *testing.T) {
	v, target, edit := restrictionsFixture()
	type input struct {
		Scope             usMemberCommandInput
		Profile, Lifetime string
		Revision          int64
	}
	original := input{v.Scope, v.Profile, v.Lifetime, v.Revision}
	usCommandWitnessMutations(t, original, func(raw any) (RequestDigest, error) {
		v := raw.(input)
		target.profile = usProfileTarget{member: v.Scope.Target.target(), profile: v.Profile, lifetime: v.Lifetime, revision: v.Revision}
		c, e := prepareUSRestrictions(v.Scope.Identity, v.Scope.Owner, v.Scope.Issuer.issuer(), target, edit)
		if e != nil {
			return RequestDigest{}, e
		}
		return c.digest, nil
	})
}
func TestUSRestrictionsFullPayload(t *testing.T) {
	v, target, edit := restrictionsFixture()
	s := v.Scope
	c, e := prepareUSRestrictions(s.Identity, s.Owner, s.Issuer.issuer(), target, edit)
	if e != nil {
		t.Fatal(e)
	}
	full := usRestrictionsRequest{ownerID: s.Owner, issuer: s.Issuer.issuer(), target: target, expectedRevision: 3, ratingSystem: "mpaa", maximumAgeRating: "PG-13", maximumAge: 13, allowUnrated: true, labels: [][]byte{[]byte("Zebra"), []byte("alpha")}, allowDownloads: true, allowLiveTV: true, allowWatchTogether: true, labelsJSON: `["Zebra","alpha"]`}
	want, e := canonicalRequestDigest(usProfileRestrictionsEdit, 1, s.Identity, full)
	if e != nil || want != c.digest {
		t.Fatal("incomplete original payload", e)
	}
	edit.BlockedLabels[0] = "changed"
	if c.request.labelsJSON != full.labelsJSON || string(c.request.labels[0]) != "Zebra" {
		t.Fatal("alias")
	}
	for _, value := range []any{*c, c, c.request, target, edit} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if got := fmt.Sprintf(verb, value); !strings.Contains(got, "redacted") || strings.Contains(got, "Zebra") {
				t.Fatal("format disclosure", got)
			}
		}
	}
	for n := 0; n < 11; n++ {
		v, target, edit := restrictionsFixture()
		switch n {
		case 0:
			edit.AllowUnrated = !edit.AllowUnrated
		case 1:
			edit.AllowDownloads = !edit.AllowDownloads
		case 2:
			edit.AllowLiveTV = !edit.AllowLiveTV
		case 3:
			edit.AllowDVR = !edit.AllowDVR
		case 4:
			edit.AllowWatchTogether = !edit.AllowWatchTogether
		case 5:
			target.currentDVR = true
		case 6:
			target.revision++
			edit.ExpectedRevision++
		case 8:
			edit.BlockedLabels = []string{"different"}
		case 9:
			edit.MaximumAgeRating = "R"
		case 10:
			edit.RatingSystem = "us-tv"
			edit.MaximumAgeRating = "TV-14"
		case 7:
			target.present = false
			target.revision = 1
			edit.ExpectedRevision = 1
		}
		other, e := prepareUSRestrictions(v.Scope.Identity, v.Scope.Owner, v.Scope.Issuer.issuer(), target, edit)
		if e != nil || other.digest == c.digest {
			t.Fatal("boolean/revision witness omitted", n, e)
		}
	}
}
func TestUSRestrictionsSourceRules(t *testing.T) {
	v, target, edit := restrictionsFixture()
	s := v.Scope
	for _, system := range securitypolicy.RatingSystems() {
		for _, rating := range system.Values {
			edit.RatingSystem = system.ID
			edit.MaximumAgeRating = rating.Code
			c, e := prepareUSRestrictions(s.Identity, s.Owner, s.Issuer.issuer(), target, edit)
			if e != nil || c.request.maximumAge != rating.MinimumAge {
				t.Fatal("rating parity", e)
			}
		}
	}
	edit.MaximumAgeRating = " "
	edit.RatingSystem = "ignored"
	edit.BlockedLabels = []string{string([]byte{255}), "a\x01b"}
	c, e := prepareUSRestrictions(s.Identity, s.Owner, s.Issuer.issuer(), target, edit)
	if e != nil || c.request.maximumAge != -1 || c.request.ratingSystem != "" {
		t.Fatal("source UTF8/default parity", e)
	}
	for _, labels := range [][]string{{""}, {"a\nb"}, {strings.Repeat("a", 121)}, make([]string, 65)} {
		edit.BlockedLabels = labels
		if _, e := prepareUSRestrictions(s.Identity, s.Owner, s.Issuer.issuer(), target, edit); e == nil {
			t.Fatal("invalid labels accepted")
		}
	}
}
