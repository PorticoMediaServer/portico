package writer

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func usRestrictionsUnitRequest() usRestrictionsRequest {
	_, policy := usPolicyFixture()
	return usRestrictionsRequest{
		ownerID: "security-owner", issuer: policy.issuer, account: policy.issuer.account, memberIncarnation: policy.issuer.memberIncarnation,
		profile: "restricted-profile", profileLifetime: strings.Repeat("d", 32), expectedRevision: 1,
		ratingSystem: "mpaa", maximumAgeRating: "PG-13", allowUnrated: false,
		blockedLabels: []string{"violence", "language"}, allowDownloads: true, allowLiveTV: false, allowDVR: false, allowWatchTogether: true,
	}
}

func TestUSRestrictionsCommandPurpose(t *testing.T) {
	expected := Descriptor{ID: USProfileRestrictions, Name: "us_profile_restrictions", Durability: Full}
	actual, err := Describe(USProfileRestrictions)
	if err != nil || actual != expected || actual.ID < 500 || actual.ID > 599 {
		t.Fatal(actual, err)
	}
	if USProfileRestrictions != 542 {
		t.Fatal("US-08 purpose must be 542")
	}
}

func TestUSRestrictionsFrozenInput(t *testing.T) {
	request := usRestrictionsUnitRequest()
	command, err := prepareUSRestrictions(USProfileRestrictions, usRestrictionsIdentity(), request)
	if err != nil {
		t.Fatal(err)
	}
	original := command.digest
	request.blockedLabels[0] = "changed"
	request.issuer.account = "changed"
	if !reflect.DeepEqual(command.request.blockedLabels, []string{"language", "violence"}) || command.request.issuer.account != "issuer" {
		t.Fatal("caller mutation escaped frozen command")
	}
	if command.blockedJSON != `["language","violence"]` || command.maximumAge != 13 {
		t.Fatal("projection", command.blockedJSON, command.maximumAge)
	}
	again, err := canonicalRequestDigest(command.kind, 1, command.identity, command.request)
	if err != nil || again != original {
		t.Fatal("frozen request digest changed", err)
	}
	if err = validateUSRestrictionsProjection(command); err != nil {
		t.Fatal(err)
	}
	// A retargeted reading of the same digest changes every security scope.
	for n, change := range []func(*usRestrictionsRequest){
		func(q *usRestrictionsRequest) { q.ownerID += "x" },
		func(q *usRestrictionsRequest) { q.account += "x" },
		func(q *usRestrictionsRequest) { q.expectedRevision = 2 },
		func(q *usRestrictionsRequest) { q.allowDVR = true },
	} {
		q := usRestrictionsUnitRequest()
		change(&q)
		other, err := prepareUSRestrictions(USProfileRestrictions, usRestrictionsIdentity(), q)
		if err != nil || other.digest == original {
			t.Fatal("security scope omitted", n, err)
		}
	}
}

func TestUSRestrictionsPreparationRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*usRestrictionsRequest)
		want error
	}{
		{"zero_sequence", nil, ErrInvalid},
		{"missing_owner", func(q *usRestrictionsRequest) { q.ownerID = "" }, ErrInvalid},
		{"missing_profile", func(q *usRestrictionsRequest) { q.profile = "" }, ErrInvalid},
		{"short_lifetime", func(q *usRestrictionsRequest) { q.profileLifetime = "abc" }, ErrInvalid},
		{"zero_revision", func(q *usRestrictionsRequest) { q.expectedRevision = 0 }, ErrInvalid},
		{"overflow_revision", func(q *usRestrictionsRequest) { q.expectedRevision = usMaxRevision }, ErrInvalid},
		{"unknown_rating", func(q *usRestrictionsRequest) { q.maximumAgeRating = "XX-99" }, ErrInvalid},
		{"unknown_system", func(q *usRestrictionsRequest) { q.ratingSystem = "NOPE" }, ErrInvalid},
		{"newline_label", func(q *usRestrictionsRequest) { q.blockedLabels = []string{"bad\nlabel"} }, ErrInvalid},
		{"empty_label", func(q *usRestrictionsRequest) { q.blockedLabels = []string{"ok", "  "} }, ErrInvalid},
		{"oversize_input_label", func(q *usRestrictionsRequest) {
			q.blockedLabels = []string{strings.Repeat("a", usMaxRestrictionLabelBytes+1)}
		}, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity, request := usRestrictionsIdentity(), usRestrictionsUnitRequest()
			if tc.name == "zero_sequence" {
				identity.Sequence = 0
			} else {
				tc.edit(&request)
			}
			command, err := prepareUSRestrictions(USProfileRestrictions, identity, request)
			if command != nil || !errors.Is(err, tc.want) {
				t.Fatal(command, err)
			}
		})
	}
	if command, err := prepareUSRestrictions(USProfileLibraries, usRestrictionsIdentity(), usRestrictionsUnitRequest()); command != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("another US purpose was admitted by restrictions preparation", command, err)
	}
}

// The product trims, refuses control characters, removes case-insensitive
// duplicates and sorts. JSON's replacement of invalid UTF-8 then decides the
// response spelling; duplicates are collapsed by the product's own key.
func TestUSRestrictionsLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []string
		want []string
	}{
		{"trim_sort", []string{" violence ", "language", "Violence"}, []string{"language", "violence"}},
		{"empty", nil, []string{}},
		{"invalid_utf8_replaced", []string{"\xffviolence"}, []string{"\ufffdviolence"}},
		{"merged_positions_collapsed", []string{"\xff", "\xfe"}, []string{"\ufffd"}},
		{"max_input", []string{strings.Repeat("a", usMaxRestrictionLabelBytes)}, []string{strings.Repeat("a", usMaxRestrictionLabelBytes)}},
		{"max_replacement", []string{strings.Repeat("\xff", usMaxRestrictionLabelBytes)}, []string{strings.Repeat("\ufffd", usMaxRestrictionLabelBytes)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := usRestrictionLabels(tc.raw)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, err)
			}
			if err = usRestrictionsValidateLabels(got); err != nil {
				t.Fatal("the product's own response spelling was refused", err)
			}
		})
	}
	if got, err := usRestrictionLabels(make([]string, usMaxRestrictionLabels+1)); got != nil || !errors.Is(err, ErrCap) {
		t.Fatal("label count cap", got, err)
	}
	if got, err := usRestrictionLabels([]string{strings.Repeat("a", usMaxRestrictionLabelBytes-1) + "\xff"}); err != nil || len(got) != 1 || len(got[0]) > usMaxRestrictionResponseBytes {
		// 119 ASCII + one invalid byte is 118+1 input bytes and 121 response octets.
		t.Fatal("replacement bound", got, err)
	}
}

func TestUSRestrictionsRating(t *testing.T) {
	for _, tc := range []struct {
		system, code, wantSystem, wantCode string
		wantAge                            int64
		ok                                 bool
	}{
		{"mpaa", "PG-13", "mpaa", "PG-13", 13, true},
		{" mpaa ", " R ", "mpaa", "R", 17, true},
		{"mpaa", "", "", "", -1, true},
		{"", "", "", "", -1, true},
		{"mpaa", "XX-99", "", "", 0, false},
		{"NOPE", "PG-13", "", "", 0, false},
		{"", "PG-13", "", "", 0, false},
	} {
		system, code, age, err := usRestrictionRating(tc.system, tc.code)
		if tc.ok != (err == nil) || system != tc.wantSystem || code != tc.wantCode || age != tc.wantAge {
			t.Fatalf("%q/%q: %q %q %d %v", tc.system, tc.code, system, code, age, err)
		}
	}
}

func usRestrictionsSavedFixture() usRestrictionsSavedResult {
	return usRestrictionsSavedResult{kind: USProfileRestrictions, ownerID: "security-owner", account: "issuer",
		memberIncarnation: strings.Repeat("a", 32), profile: "restricted-profile", profileLifetime: strings.Repeat("d", 32),
		revision: 2, appliedMS: 1000, ratingSystem: "mpaa", maximumAgeRating: "PG-13", maximumAge: 13,
		allowDownloads: true, allowWatchTogether: true, labels: []string{"language", "violence"}}
}

func TestUSRestrictionsSavedResult(t *testing.T) {
	value := usRestrictionsSavedFixture()
	if err := validateUSRestrictionsResult(value); err != nil {
		t.Fatal(err)
	}
	digest, err := usRestrictionsResultDigest(usRestrictionsIdentity(), RequestDigest{7}, value)
	if err != nil || digest == (ResultDigest{}) {
		t.Fatal(digest, err)
	}
	again, err := usRestrictionsResultDigest(usRestrictionsIdentity(), RequestDigest{7}, value)
	if err != nil || again != digest {
		t.Fatal("result digest is not deterministic", err)
	}
	for n, change := range []func(*usRestrictionsSavedResult){
		func(v *usRestrictionsSavedResult) { v.revision++ },
		func(v *usRestrictionsSavedResult) { v.ratingSystem = "TV" },
		func(v *usRestrictionsSavedResult) { v.maximumAgeRating = "R" },
		func(v *usRestrictionsSavedResult) { v.maximumAge++ },
		func(v *usRestrictionsSavedResult) { v.allowUnrated = true },
		func(v *usRestrictionsSavedResult) { v.labels = []string{"violence", "language"} },
	} {
		other := usRestrictionsSavedFixture()
		change(&other)
		if changed, err := usRestrictionsResultDigest(usRestrictionsIdentity(), RequestDigest{7}, other); err != nil || changed == digest {
			t.Fatal("result field omitted from the digest", n, err)
		}
	}
	// The charge is the exact receipt header + typed row + ordered label rows.
	want := int64(len(value.ownerID) + 96 + len(usRestrictionsResultCodec))
	want += int64(len(value.ownerID) + 16 + len(value.account) + len(value.memberIncarnation) +
		len(value.profile) + len(value.profileLifetime) + len(value.ratingSystem) + len(value.maximumAgeRating))
	for _, label := range value.labels {
		want += int64(len(value.ownerID) + 16 + len(label))
	}
	if bytes, err := usRestrictionsResultVariableBytes(value); err != nil || bytes != want {
		t.Fatal(bytes, want, err)
	}
}

func TestUSRestrictionsSavedResultRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*usRestrictionsSavedResult)
	}{
		{"first_revision", func(v *usRestrictionsSavedResult) { v.revision = 1 }},
		{"nil_labels", func(v *usRestrictionsSavedResult) { v.labels = nil }},
		{"empty_label", func(v *usRestrictionsSavedResult) { v.labels = []string{""} }},
		{"oversize_label", func(v *usRestrictionsSavedResult) {
			v.labels = []string{strings.Repeat("a", usMaxRestrictionResponseBytes+1)}
		}},
		{"invalid_utf8", func(v *usRestrictionsSavedResult) { v.labels = []string{"\xff"} }},
		{"control_label", func(v *usRestrictionsSavedResult) { v.labels = []string{"a\tb"} }},
		{"system_without_code", func(v *usRestrictionsSavedResult) { v.maximumAge = -1 }},
		{"code_without_system", func(v *usRestrictionsSavedResult) { v.ratingSystem = "" }},
		{"age_below_floor", func(v *usRestrictionsSavedResult) { v.maximumAge = -2 }},
		{"missing_target", func(v *usRestrictionsSavedResult) { v.profile = "" }},
		{"wrong_kind", func(v *usRestrictionsSavedResult) { v.kind = USProfileLibraries }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := usRestrictionsSavedFixture()
			tc.edit(&value)
			if err := validateUSRestrictionsResult(value); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("invalid saved result accepted", err)
			}
			if bytes, err := usRestrictionsResultVariableBytes(value); bytes != 0 || err == nil {
				t.Fatal("invalid saved result charged", bytes, err)
			}
		})
	}
	// Equal spellings at distinct positions are the product's response, never a
	// corruption: the source has no UNIQUE(label) and is never re-sorted.
	merged := usRestrictionsSavedFixture()
	merged.labels = []string{"\ufffd", "\ufffd"}
	if err := validateUSRestrictionsResult(merged); err != nil {
		t.Fatal("merged label positions were refused", err)
	}
}

// The projection is not an independent request: a composition bug cannot keep
// the frozen digest while changing the SQL TEXT or the resolved ceiling.
func TestUSRestrictionsProjectionRefusals(t *testing.T) {
	command, err := prepareUSRestrictions(USProfileRestrictions, usRestrictionsIdentity(), usRestrictionsUnitRequest())
	if err != nil {
		t.Fatal(err)
	}
	for n, change := range []func(*usRestrictionsCommand){
		func(c *usRestrictionsCommand) { c.maximumAge++ },
		func(c *usRestrictionsCommand) { c.blockedJSON = `["language"]` },
		func(c *usRestrictionsCommand) { c.request.blockedLabels = []string{"language"} },
		func(c *usRestrictionsCommand) { c.request.ratingSystem = "TV" },
	} {
		copied := *command
		copied.request = command.request
		copied.request.blockedLabels = append([]string(nil), command.request.blockedLabels...)
		change(&copied)
		if err := validateUSRestrictionsProjection(&copied); err == nil {
			t.Fatal("projection drift accepted", n, fmt.Sprint(copied.blockedJSON))
		}
	}
}
