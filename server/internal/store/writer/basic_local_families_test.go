package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func basicLocalNFORowObservationFixture() basicNFORowObservation {
	return basicNFORowObservation{
		locator:    "Shows/Season 01/Episode 01.nfo",
		kind:       "episode",
		coordinate: "[1,1,1]",
		digest:     "",
		payload:    "",
		size:       0,
		modifiedNS: -11,
		observedAt: "2026-11-08T00:00:00Z",
	}
}

func basicLocalNFORowTextFields(r *basicNFORowObservation) []*string {
	return []*string{&r.locator, &r.kind, &r.coordinate, &r.digest, &r.payload, &r.observedAt}
}

func TestPrepareBasicNFORowsRoundTripAndIndependence(t *testing.T) {
	empty := basicLocalNFORowObservationFixture()
	full := basicNFORowObservation{
		locator:    "Movies/Arrival.nfo",
		kind:       "movie",
		coordinate: "[null,null,null]",
		digest:     "native-digest",
		payload:    `{"title":"Arrival"}`,
		size:       42,
		modifiedNS: -11,
		observedAt: "2026-11-08T00:00:01Z",
	}
	observed := []basicNFORowObservation{empty, full}
	got, err := prepareBasicNFORows(observed)
	if err != nil {
		t.Fatal(err)
	}
	want := []basicNFORow{
		{locator: empty.locator, kind: empty.kind, coordinate: empty.coordinate, digest: "", payload: "", size: 0, modifiedNS: -11, observedAt: empty.observedAt},
		{locator: full.locator, kind: full.kind, coordinate: full.coordinate, digest: full.digest, payload: full.payload, size: 42, modifiedNS: -11, observedAt: full.observedAt},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip %#v", got)
	}
	observed[0].locator = "mutated"
	observed[1].digest = "mutated"
	observed[1].size = 7
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output aliased input %#v", got)
	}
}

func TestPrepareBasicNFORowsEmpty(t *testing.T) {
	for _, observed := range [][]basicNFORowObservation{nil, {}} {
		got, err := prepareBasicNFORows(observed)
		if err != nil || got != nil {
			t.Fatalf("empty set %#v %v", got, err)
		}
	}
}

func TestPrepareBasicNFORowsCaps(t *testing.T) {
	rows := make([]basicNFORowObservation, basicLocalNFOMaxRows+1)
	for n := range rows {
		rows[n] = basicLocalNFORowObservationFixture()
	}
	if got, err := prepareBasicNFORows(rows); !errors.Is(err, ErrCap) || got != nil {
		t.Fatalf("row cap %#v %v", got, err)
	}
	for n := range basicLocalNFORowTextFields(&basicNFORowObservation{}) {
		row := basicLocalNFORowObservationFixture()
		*basicLocalNFORowTextFields(&row)[n] = strings.Repeat("x", basicLocalNFOMaxTextBytes+1)
		if got, err := prepareBasicNFORows([]basicNFORowObservation{row}); !errors.Is(err, ErrCap) || got != nil {
			t.Fatalf("text cap field %d %#v %v", n, got, err)
		}
	}
}

func TestPrepareBasicNFORowsRefusals(t *testing.T) {
	for n := range basicLocalNFORowTextFields(&basicNFORowObservation{}) {
		row := basicLocalNFORowObservationFixture()
		*basicLocalNFORowTextFields(&row)[n] = string([]byte{'x', 0xff})
		if got, err := prepareBasicNFORows([]basicNFORowObservation{row}); !errors.Is(err, ErrInvalid) || got != nil {
			t.Fatalf("utf8 field %d %#v %v", n, got, err)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*basicNFORowObservation)
	}{
		{"empty_observed_at", func(r *basicNFORowObservation) { r.observedAt = "" }},
		{"negative_size", func(r *basicNFORowObservation) { r.size = -1 }},
	} {
		row := basicLocalNFORowObservationFixture()
		tc.mutate(&row)
		if got, err := prepareBasicNFORows([]basicNFORowObservation{row}); !errors.Is(err, ErrInvalid) || got != nil {
			t.Fatalf("%s %#v %v", tc.name, got, err)
		}
	}
}

func basicLocalArtworkTemplateFixture() basicArtworkInput {
	return basicArtworkInput{
		source: basicStreamFactStage{
			changeID: 51, inputRevision: 7, libraryID: 41, assetID: 42, expectedEpoch: 17,
			assetToken: "asset-token", runID: "run", sourceID: "source", incarnation: "incarnation",
			objectID: "object", objectRevision: "revision", sourceGeneration: 3, policyRevision: 9,
			fingerprint: "fingerprint",
		},
		entityID:                41,
		ownerSourceRevision:     2,
		ownerArtworkRevision:    3,
		selectionRevision:       4,
		ownerArtworkIncarnation: [16]byte{1},
		jobID:                   "job",
		role:                    "template-role",
		imageID:                 "template-image",
		origin:                  "template-origin",
		attribution:             "template-attribution",
		locale:                  "template-locale",
		effectiveLanguage:       "template-language",
		sourceFence:             "template-fence",
		valid:                   true,
	}
}

func basicLocalArtworkObservationFixture() basicArtworkObservation {
	return basicArtworkObservation{
		role:              "poster",
		imageID:           "image-1",
		origin:            "local",
		attribution:       "attribution",
		locale:            "en-US",
		effectiveLanguage: "en",
		sourceFence:       "fence",
	}
}

func TestPrepareBasicArtworkSelectionObservedOnly(t *testing.T) {
	template := basicLocalArtworkTemplateFixture()
	observed := basicLocalArtworkObservationFixture()
	got, err := prepareBasicArtworkSelection(template, observed)
	if err != nil {
		t.Fatal(err)
	}
	if !got.valid {
		t.Fatal("selection not valid")
	}
	if got.role != "poster" || got.imageID != "image-1" || got.origin != "local" || got.attribution != "attribution" || got.locale != "en-US" || got.effectiveLanguage != "en" || got.sourceFence != "fence" {
		t.Fatalf("observed fields %#v", got)
	}
	if !reflect.DeepEqual(got.source, template.source) || got.entityID != 41 || got.ownerSourceRevision != 2 || got.ownerArtworkRevision != 3 || got.selectionRevision != 4 || got.ownerArtworkIncarnation != ([16]byte{1}) || got.jobID != "job" {
		t.Fatalf("template identity changed %#v", got)
	}
	observed.role, observed.imageID = "mutated", "mutated"
	template.source.assetToken, template.jobID = "mutated", "mutated"
	if got.role != "poster" || got.imageID != "image-1" || got.source.assetToken != "asset-token" || got.jobID != "job" {
		t.Fatalf("output aliased input %#v", got)
	}
}

func TestPrepareBasicArtworkSelectionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*basicArtworkObservation)
		want   error
	}{
		{"thumb_role", func(o *basicArtworkObservation) { o.role = "thumb" }, ErrInvalid},
		{"thumbnail_role", func(o *basicArtworkObservation) { o.role = "thumbnail" }, ErrInvalid},
		{"empty_role", func(o *basicArtworkObservation) { o.role = "" }, ErrInvalid},
		{"empty_image_id", func(o *basicArtworkObservation) { o.imageID = "" }, ErrInvalid},
		{"empty_origin", func(o *basicArtworkObservation) { o.origin = "" }, ErrInvalid},
		{"invalid_utf8", func(o *basicArtworkObservation) { o.locale = string([]byte{'x', 0xff}) }, ErrInvalid},
		{"text_cap", func(o *basicArtworkObservation) { o.attribution = strings.Repeat("x", 33554433) }, ErrCap},
	} {
		observed := basicLocalArtworkObservationFixture()
		tc.mutate(&observed)
		got, err := prepareBasicArtworkSelection(basicLocalArtworkTemplateFixture(), observed)
		if !errors.Is(err, tc.want) || got != nil {
			t.Fatalf("%s %#v %v", tc.name, got, err)
		}
	}
}

func TestBasicLocalFamiliesSubtitleTextPending(t *testing.T) {
	for _, observed := range []bool{true, false} {
		if got := prepareBasicSubtitleTextPending(observed); got != observed {
			t.Fatalf("subtitle switch %v", got)
		}
	}
}
