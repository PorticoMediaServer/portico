package providers

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

const (
	mbObservedAt = "2026-11-07T13:01:02.000000003Z"

	mbReleaseID       = "b1a9c0fe-9c2e-4f5a-9d1a-2f7f3b6c1d20"
	mbReleaseGroupID  = "c4b1b2f3-2e3d-4f40-9a0b-1c2d3e4f5061"
	mbArtistID        = "b3a0a1e2-1d2c-4d3e-8f9a-0b1c2d3e4f50"
	mbFeaturedID      = "e6d3d4b5-4051-4b62-9c2d-3e4f50617283"
	mbSecondArtistID  = "a1b2c3d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	mbTrackOneID      = "d5c2c3a4-3f40-4a51-8b1c-2d3e4f506172"
	mbTrackTwoID      = "a8f5f6d7-6273-4d84-9e4f-506172839405"
	mbTrackThreeID    = "f9a6a7b8-7384-4e95-8f50-617283940516"
	mbRecordingOneID  = "f7e4e5c6-5162-4c73-8d3e-4f5061728394"
	mbRecordingTwoID  = "b9a6a7e8-7384-4e95-8f50-617283940516"
	mbRecordingThrID  = "c0b7b8f9-8495-4fa6-9a61-728394051627"
	mbRedirectGroupID = "1b2c3d4e-5f60-4b7c-9d8e-0f1a2b3c4d5e"
	mbRedirectBodyID  = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	mbRequestedID     = "9f8e7d6c-5b4a-4392-8170-6f5e4d3c2b1a"

	mbRecordingOneEvidence = "recording-evidence-one"
	mbRecordingTwoEvidence = "recording-evidence-two"
	mbRecordingThrEvidence = "recording-evidence-three"
	mbRecordingOneSubject  = 51
	mbRecordingTwoSubject  = 52
	mbRecordingThrSubject  = 53

	mbRedirectRecordingID       = "4e5f6071-8293-4ea0-90b1-3c4d5e6f7081"
	mbRedirectRecordingEvidence = "recording-evidence-redirect"
	mbRedirectRecordingSubject  = 61
)

func mbFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mbConsent() MBConsent { return MBConsent{OnlineLookups: 1, LookupEpoch: 7} }

func mbRecordingTargets() map[string]MBRecordingTarget {
	return map[string]MBRecordingTarget{
		mbRecordingOneID: {EvidenceID: mbRecordingOneEvidence, SubjectID: mbRecordingOneSubject},
		mbRecordingTwoID: {EvidenceID: mbRecordingTwoEvidence, SubjectID: mbRecordingTwoSubject},
		mbRecordingThrID: {EvidenceID: mbRecordingThrEvidence, SubjectID: mbRecordingThrSubject},
	}
}

func mbRedirectRecordingTargets() map[string]MBRecordingTarget {
	return map[string]MBRecordingTarget{
		mbRedirectRecordingID: {EvidenceID: mbRedirectRecordingEvidence, SubjectID: mbRedirectRecordingSubject},
	}
}

func mbObservation(kind enrich.SubjectKind, requested string) MBObservation {
	path, ok := mbSourcePath(kind, requested)
	if !ok {
		panic("invalid test route")
	}
	o := MBObservation{Route: MBRoute{Kind: kind, RequestedID: requested},
		EvidenceID: "mb-evidence", SubjectID: 41, ObservedAt: mbObservedAt,
		SourceURL: musicBrainzOrigin + path, ETag: "etag-original",
		LastModified: "original validator"}
	if kind == enrich.Release {
		switch requested {
		case mbReleaseID:
			o.Recordings = mbRecordingTargets()
		case mbRequestedID, mbRedirectBodyID:
			o.Recordings = mbRedirectRecordingTargets()
		}
	}
	return o
}

// mbMutate replaces exactly one occurrence and fails loudly when the fixture
// text drifted, so a mutation test cannot silently pass on an unchanged body.
func mbMutate(t *testing.T, body []byte, old, replacement string) []byte {
	t.Helper()
	if !strings.Contains(string(body), old) {
		t.Fatalf("fixture lacks mutation anchor %q", old)
	}
	return []byte(strings.Replace(string(body), old, replacement, 1))
}

func mbJSONArray(t *testing.T, values ...string) string {
	t.Helper()
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func mbCreditJSON(name string) string {
	return `{"name":"` + name + `","joinphrase":"","artist":{"id":"` + mbArtistID +
		`","name":"` + name + `","sort-name":"` + name + `","disambiguation":""}}`
}

func mbGeneratedRecordingID(index int) string {
	return fmt.Sprintf("20000000-0000-4000-8000-%012d", index)
}

func mbGeneratedTargets(count int) map[string]MBRecordingTarget {
	out := make(map[string]MBRecordingTarget, count)
	for i := 1; i <= count; i++ {
		out[mbGeneratedRecordingID(i)] = MBRecordingTarget{
			EvidenceID: fmt.Sprintf("generated-recording-evidence-%d", i),
			SubjectID:  int64(1000 + i)}
	}
	return out
}

func mbGeneratedObservation(base MBObservation, recordings int) MBObservation {
	base.Recordings = mbGeneratedTargets(recordings)
	return base
}

func mbGeneratedTrack(index int, artistCredit string) string {
	trackID := fmt.Sprintf("10000000-0000-4000-8000-%012d", index)
	return `{"id":"` + trackID + `","number":"` + strconv.Itoa(index) + `","title":"Track",` +
		`"length":1000,"position":` + strconv.Itoa(index) + `,"artist-credit":` + artistCredit +
		`,"recording":{"id":"` + mbGeneratedRecordingID(index) + `","title":"Track","length":1000,` +
		`"artist-credit":[` + mbCreditJSON("Recording Artist") + `]}}`
}

func mbGeneratedMedium(position int, tracks []string) string {
	return `{"position":` + strconv.Itoa(position) + `,"format":"CD","title":"","track-count":` +
		strconv.Itoa(len(tracks)) + `,"tracks":[` + strings.Join(tracks, ",") + `]}`
}

func mbGeneratedRelease(creditList, mediaList string) []byte {
	return []byte(`{"id":"` + mbReleaseID + `","title":"Generated","date":"2001-01-01",` +
		`"country":"GB","barcode":"123","disambiguation":"","artist-credit":[` + creditList + `],` +
		`"release-group":{"id":"` + mbReleaseGroupID + `","title":"Generated",` +
		`"primary-type":"Album","secondary-types":[]},"media":[` + mediaList + `]}`)
}

func mbWantInput(o MBObservation, body []byte, title, artist string) writer.EvidenceHeaderInput {
	path, _ := mbSourcePath(o.Route.Kind, o.Route.RequestedID)
	return writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID,
		Provider: "musicbrainz", SubjectKind: string(o.Route.Kind), ProviderID: o.Route.RequestedID,
		ParserVersion: mbParser(o.Route.Kind), RequestShape: mbShape(o.Route.Kind),
		ObservationDigest: sha256.Sum256(body), ObservedAt: o.ObservedAt,
		SourceURL: musicBrainzOrigin + path, ETag: o.ETag, LastModified: o.LastModified,
		Title: title, Artist: artist}
}

func mbWantRecordingInput(o MBObservation, body []byte, target MBRecordingTarget,
	providerID, title, artist, disambiguation string, length writer.EvidenceInteger) writer.EvidenceHeaderInput {
	path, _ := mbSourcePath(o.Route.Kind, o.Route.RequestedID)
	return writer.EvidenceHeaderInput{ID: target.EvidenceID, SubjectID: target.SubjectID,
		Provider: "musicbrainz", SubjectKind: "recording", ProviderID: providerID,
		ParserVersion: mbParser(enrich.Recording), RequestShape: mbShape(enrich.Recording),
		ObservationDigest: sha256.Sum256(body), ObservedAt: o.ObservedAt,
		SourceURL: musicBrainzOrigin + path, ETag: o.ETag, LastModified: o.LastModified,
		Title: title, Artist: artist, Disambiguation: disambiguation, LengthMillis: length}
}

func mbAssertRefused(t *testing.T, consent MBConsent, o MBObservation, body []byte, want error) {
	t.Helper()
	doc, err := PrepareMusicBrainz(consent, o, body)
	if !errors.Is(err, want) || !reflect.DeepEqual(doc, MBEvidence{}) {
		t.Fatalf("accepted invalid observation: %+v, %v", doc, err)
	}
}

func TestIPMBReleaseExactEvidence(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	doc, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	want := mbWantInput(o, body, "Collected", "Massive Attack")
	want.ReleaseDate, want.Year = "1991-04-08", 1991
	want.Country, want.Barcode = "GB", "0077778622821"
	want.ReleaseGroupID, want.GroupTitle, want.PrimaryType = mbReleaseGroupID, "Collected", "Album"
	if !reflect.DeepEqual(doc.HeaderInput(), want) {
		t.Fatalf("header input %+v", doc.HeaderInput())
	}
	wantHeader, err := writer.NewEvidenceHeader(want)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Header(), wantHeader) || doc.Header().ID() != o.EvidenceID ||
		doc.Route() != o.Route || doc.LookupEpoch() != 7 || doc.Class() != MBClassCompilation {
		t.Fatalf("header/route/epoch/class %+v", doc)
	}
	if len(doc.Redirects()) != 0 {
		t.Fatal("an unchanged MBID minted a redirect alias")
	}
	wantMedia := []MBMedium{{Position: 1, Format: "CD", Title: ""},
		{Position: 2, Format: "Vinyl", Title: "Bonus Disc"}}
	if !reflect.DeepEqual(doc.Media(), wantMedia) {
		t.Fatalf("media %+v", doc.Media())
	}
	wantTracks := []MBTrack{
		{TrackID: mbTrackOneID, MediumPosition: 1, Position: 1, Number: "1",
			Title: "Safe from Harm", Artist: "Massive Attack feat. Shara Nelson",
			LengthMillis: writer.EvidenceInteger{Present: true, Value: 341000}, RecordingID: mbRecordingOneID,
			RecordingEvidenceID: mbRecordingOneEvidence,
			Recording: mbWantRecordingInput(o, body, mbRecordingTargets()[mbRecordingOneID],
				mbRecordingOneID, "Safe from Harm", "Massive Attack feat. Shara Nelson", "album version",
				writer.EvidenceInteger{Present: true, Value: 341000})},
		{TrackID: mbTrackTwoID, MediumPosition: 1, Position: 2, Number: "2",
			Title: "Unfinished Sympathy", Artist: "",
			LengthMillis: writer.EvidenceInteger{Present: true, Value: 0}, RecordingID: mbRecordingTwoID,
			RecordingEvidenceID: mbRecordingTwoEvidence,
			Recording: mbWantRecordingInput(o, body, mbRecordingTargets()[mbRecordingTwoID],
				mbRecordingTwoID, "Unfinished Sympathy", "Massive Attack", "",
				writer.EvidenceInteger{})},
		{TrackID: mbTrackThreeID, MediumPosition: 2, Position: 1, Number: "A1",
			Title: "Hymn of the Big Wheel", Artist: "Massive Attack & Horace Andy",
			LengthMillis: writer.EvidenceInteger{Present: true, Value: 40000}, RecordingID: mbRecordingThrID,
			RecordingEvidenceID: mbRecordingThrEvidence,
			Recording: mbWantRecordingInput(o, body, mbRecordingTargets()[mbRecordingThrID],
				mbRecordingThrID, "Hymn of the Big Wheel", "Massive Attack & Horace Andy", "",
				writer.EvidenceInteger{Present: true, Value: 40000})},
	}
	if !reflect.DeepEqual(doc.Tracks(), wantTracks) {
		t.Fatalf("tracks %+v", doc.Tracks())
	}
	// Every recording header is pre-staged once, in first-seen track order, and
	// each track names its exact evidence identity.
	wantRecordings := []writer.EvidenceHeaderInput{
		wantTracks[0].Recording, wantTracks[1].Recording, wantTracks[2].Recording,
	}
	if !reflect.DeepEqual(doc.Recordings(), wantRecordings) {
		t.Fatalf("recordings %+v", doc.Recordings())
	}
	wantTerms := []MBTerm{{Ordinal: 0, Vocabulary: mbSecondaryVocabulary, Value: "Compilation"},
		{Ordinal: 1, Vocabulary: mbSecondaryVocabulary, Value: "Remix"}}
	if !reflect.DeepEqual(doc.Terms(), wantTerms) {
		t.Fatalf("terms %+v", doc.Terms())
	}
	// The release's own credit stays subject-scoped. Each track's complete
	// artist credit is retained as track-scoped rows in provider order, never
	// dropped in favour of the joined display alone.
	wantCredits := []MBCredit{
		{Ordinal: 0, Class: "artist", ProviderPersonID: mbArtistID, Name: "Massive Attack",
			ArtistName: "Massive Attack", SortName: "Massive Attack", SourceOrdinalPresent: true},
		{Ordinal: 1, Class: "artist", Scope: mbTrackOneID, ProviderPersonID: mbArtistID, Name: "Massive Attack",
			ArtistName: "Massive Attack", SortName: "Massive Attack", JoinPhrase: " feat. ",
			SourceOrdinalPresent: true},
		{Ordinal: 2, Class: "artist", Scope: mbTrackOneID, ProviderPersonID: mbFeaturedID, Name: "Shara Nelson",
			ArtistName: "Shara Nelson", SortName: "Nelson, Shara", Disambiguation: "British singer",
			SourceOrdinal: 1, SourceOrdinalPresent: true},
		{Ordinal: 3, Class: "artist", Scope: mbTrackThreeID, ProviderPersonID: mbArtistID, Name: "Massive Attack",
			ArtistName: "Massive Attack", SortName: "Massive Attack", JoinPhrase: " & ",
			SourceOrdinalPresent: true},
		{Ordinal: 4, Class: "artist", Scope: mbTrackThreeID, ProviderPersonID: mbSecondArtistID, Name: "Horace Andy",
			ArtistName: "Horace Andy", SortName: "Andy, Horace", SourceOrdinal: 1, SourceOrdinalPresent: true},
	}
	if !reflect.DeepEqual(doc.Credits(), wantCredits) {
		t.Fatalf("credits %+v", doc.Credits())
	}
	if doc.HeaderInput().ParserVersion != "ip.musicbrainz.release.evidence.complete-ordered-families.v2" ||
		doc.HeaderInput().RequestShape != "ip.musicbrainz.release.evidence.detail+recordings+media+tracks+credits.v2" {
		t.Fatal("release parser/shape drifted from the admitted IP-06 pair")
	}
	// The recorded body is not borrowed: mutating the caller's bytes after
	// preparation cannot change the sealed projection.
	clear(body)
	if !reflect.DeepEqual(doc.HeaderInput(), want) || !reflect.DeepEqual(doc.Tracks(), wantTracks) {
		t.Fatal("observation body was borrowed")
	}
}

func TestIPMBReleaseVersusGroupIdentity(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	doc, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.HeaderInput().ProviderID != mbReleaseID ||
		doc.HeaderInput().ReleaseGroupID != mbReleaseGroupID ||
		doc.HeaderInput().ProviderID == doc.HeaderInput().ReleaseGroupID {
		t.Fatalf("release and group identity collapsed: %+v", doc.HeaderInput())
	}
	// A response that answers with the release-group MBID as the release body is
	// not the same entity and is refused rather than renamed.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"id": "`+mbReleaseID+`"`, `"id": "`+mbReleaseGroupID+`"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, mbReleaseGroupID, strings.ToUpper(mbReleaseGroupID)), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"primary-type": "Album"`, `"primary-type": ""`), ErrMBEvidence)
}

func TestIPMBRedirectRetainsRequestedID(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release_redirect.json")
	o := mbObservation(enrich.Release, mbRequestedID)
	doc, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	if doc.HeaderInput().ProviderID != mbRedirectBodyID ||
		doc.HeaderInput().ProviderID == doc.HeaderInput().ReleaseGroupID ||
		doc.Route().RequestedID != mbRequestedID {
		t.Fatalf("redirect identity %+v", doc.HeaderInput())
	}
	want := []MBRedirect{{RequestedID: mbRequestedID, CanonicalID: mbRedirectBodyID,
		Name: "Collected (Remastered)"}}
	if !reflect.DeepEqual(doc.Redirects(), want) {
		t.Fatalf("redirect alias %+v", doc.Redirects())
	}
	if doc.HeaderInput().SourceURL != musicBrainzOrigin+"/ws/2/release/"+mbRequestedID {
		t.Fatal("source URL lost the original requested coordinate")
	}
	// The canonical body's single track still gets its own pre-staged recording
	// header bound to the redirect observation's recording identity.
	tracks := doc.Tracks()
	if len(tracks) != 1 || tracks[0].RecordingEvidenceID != mbRedirectRecordingEvidence {
		t.Fatalf("redirect tracks %+v", tracks)
	}
	wantRecording := mbWantRecordingInput(o, body, mbRedirectRecordingTargets()[mbRedirectRecordingID],
		mbRedirectRecordingID, "Safe from Harm", "Massive Attack", "",
		writer.EvidenceInteger{Present: true, Value: 341000})
	if !reflect.DeepEqual(tracks[0].Recording, wantRecording) {
		t.Fatalf("redirect recording %+v", tracks[0].Recording)
	}
	// The redirect fixture declares no track artist credit, so only the
	// subject's own credit row exists and no credit is scoped to the track.
	if credits := doc.Credits(); len(credits) != 1 || credits[0].Scope != "" {
		t.Fatalf("redirect credits %+v", credits)
	}
	// A partial MusicBrainz date is retained as observed, never extended.
	if doc.HeaderInput().ReleaseDate != "2006" || doc.HeaderInput().Year != 2006 {
		t.Fatalf("partial date %+v", doc.HeaderInput())
	}
	// The canonical body MBID must itself be a canonical MBID; an unrelated
	// string is a forged body, not a redirect.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, mbRedirectBodyID, "not-an-mbid"), ErrMBEvidence)
	// A release body that claims its own release-group MBID is refused.
	mbAssertRefused(t, mbConsent(), mbObservation(enrich.Release, mbRedirectBodyID),
		mbMutate(t, body, mbRedirectBodyID, mbRedirectGroupID), ErrMBEvidence)
}

func TestIPMBSecondaryTypesVocabularyAndClassification(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	prepare := func(input []byte) MBEvidence {
		t.Helper()
		doc, err := PrepareMusicBrainz(mbConsent(), o, input)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := prepare(mbMutate(t, body, `["Compilation", "Remix"]`,
		mbJSONArray(t, "Remix", "Compilation", "Field recording")))
	want := []MBTerm{{Ordinal: 0, Vocabulary: mbSecondaryVocabulary, Value: "Remix"},
		{Ordinal: 1, Vocabulary: mbSecondaryVocabulary, Value: "Compilation"},
		{Ordinal: 2, Vocabulary: mbSecondaryVocabulary, Value: "Field recording"}}
	if !reflect.DeepEqual(doc.Terms(), want) {
		t.Fatalf("unknown ordered secondary types %+v", doc.Terms())
	}
	// Compilation wins even when it is not first and the primary type is EP.
	doc = prepare(mbMutate(t, mbMutate(t, body, `"primary-type": "Album"`, `"primary-type": "EP"`),
		`["Compilation", "Remix"]`, mbJSONArray(t, "Live", "Compilation")))
	if doc.Class() != MBClassCompilation || doc.HeaderInput().PrimaryType != "EP" {
		t.Fatalf("compilation precedence %+v", doc)
	}
	for _, tc := range []struct {
		primary   string
		secondary []string
		want      MBReleaseClass
	}{
		{"EP", nil, MBClassEP},
		{"ep", nil, MBClassEP},
		{"Single", []string{"Live"}, MBClassSingle},
		{"Album", nil, MBClassAlbum},
		{"Soundtrack", []string{"Live"}, MBClassAlbum},
		// Compilation is a MusicBrainz secondary type, never a primary one:
		// it outranks the primary Album classification.
		{"Album", []string{"Compilation"}, MBClassCompilation},
	} {
		got := mbClassifyRelease(tc.primary, tc.secondary)
		if got != tc.want {
			t.Fatalf("%q class %q, want %q", tc.primary, got, tc.want)
		}
	}
	// Unknown primary types are retained in the header, never converted.
	doc = prepare(mbMutate(t, mbMutate(t, body, `"primary-type": "Album"`, `"primary-type": "Soundtrack"`),
		`["Compilation", "Remix"]`, mbJSONArray(t)))
	if doc.HeaderInput().PrimaryType != "Soundtrack" || doc.Class() != MBClassAlbum || len(doc.Terms()) != 0 {
		t.Fatalf("unknown primary type %+v", doc.HeaderInput())
	}
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `["Compilation", "Remix"]`, mbJSONArray(t, "Compilation", " ")), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `["Compilation", "Remix"]`, mbJSONArray(t, "Compilation", "")), ErrMBEvidence)
	tooMany := make([]string, maxMBTerms+1)
	for i := range tooMany {
		tooMany[i] = "Secondary" + strconv.Itoa(i)
	}
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `["Compilation", "Remix"]`, mbJSONArray(t, tooMany...)), ErrMBEvidence)
}

func TestIPMBCompleteMediaAndTracks(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	if _, err := PrepareMusicBrainz(mbConsent(), o, body); err != nil {
		t.Fatal("the complete fixture was refused:", err)
	}
	// A declared track count that the body does not supply is a partial medium.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"track-count": 2,`, `"track-count": 3,`), ErrMBEvidence)
	// Contiguous provider positions are required for both mediums and tracks.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"position": 2,`+"\n"+`      "format": "Vinyl"`,
			`"position": 3,`+"\n"+`      "format": "Vinyl"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"position": 2,`+"\n"+`          "artist-credit": []`,
			`"position": 3,`+"\n"+`          "artist-credit": []`), ErrMBEvidence)
	// Track identifiers are unique across the whole release.
	mbAssertRefused(t, mbConsent(), o, mbMutate(t, body, mbTrackTwoID, mbTrackOneID), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o, mbMutate(t, body, mbRecordingOneID, `""`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, mbTrackOneID, strings.ToUpper(mbTrackOneID)), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"length": 341000,`+"\n"+`          "position": 1,`,
			`"length": 1000001,`+"\n"+`          "position": 1,`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"title": "Unfinished Sympathy",`+"\n"+`          "length": 0,`,
			`"title": "",`+"\n"+`          "length": 0,`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"number": "A1",`, `"number": "",`), ErrMBEvidence)
	// A medium's format and title are exact provider strings and are preserved
	// even when the provider leaves them empty; no fallback label is invented.
	empty := mbMutate(t, mbMutate(t, body, `"format": "CD"`, `"format": ""`),
		`"title": "Bonus Disc"`, `"title": ""`)
	doc, err := PrepareMusicBrainz(mbConsent(), o, empty)
	if err != nil {
		t.Fatal("empty medium strings were refused:", err)
	}
	wantEmpty := []MBMedium{{Position: 1, Format: "", Title: ""}, {Position: 2, Format: "Vinyl", Title: ""}}
	if !reflect.DeepEqual(doc.Media(), wantEmpty) {
		t.Fatalf("empty medium strings %+v", doc.Media())
	}
	// The embedded recording is a complete observation of its own, so a blank
	// title, an out-of-range length or a missing artist credit is refused.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"title": "Unfinished Sympathy",`+"\n"+`            "length": null,`,
			`"title": "",`+"\n"+`            "length": null,`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"length": null,`, `"length": 1000001,`), ErrMBEvidence)
	generated := mbGeneratedRelease(mbCreditJSON("Massive Attack"),
		mbGeneratedMedium(1, []string{strings.Replace(mbGeneratedTrack(1, "[]"),
			`"artist-credit":[`+mbCreditJSON("Recording Artist")+`]`, `"artist-credit":[]`, 1)}))
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1), generated, ErrMBEvidence)
	// Every declared recording needs its own exact binding: a missing, extra,
	// shared-evidence, empty, over-long or non-positive target is refused, and a
	// non-release subject may not bind a release's recordings.
	refuseTargets := func(targets map[string]MBRecordingTarget) {
		t.Helper()
		observation := o
		observation.Recordings = targets
		mbAssertRefused(t, mbConsent(), observation, body, ErrMBEvidence)
	}
	refuseTargets(nil)
	missing := mbRecordingTargets()
	delete(missing, mbRecordingTwoID)
	refuseTargets(missing)
	extra := mbRecordingTargets()
	extra[mbRedirectRecordingID] = MBRecordingTarget{EvidenceID: "extra-evidence", SubjectID: 99}
	refuseTargets(extra)
	shared := mbRecordingTargets()
	shared[mbRecordingTwoID] = MBRecordingTarget{EvidenceID: mbRecordingOneEvidence, SubjectID: mbRecordingTwoSubject}
	refuseTargets(shared)
	emptyID := mbRecordingTargets()
	emptyID[mbRecordingTwoID] = MBRecordingTarget{EvidenceID: "", SubjectID: 1}
	refuseTargets(emptyID)
	longID := mbRecordingTargets()
	longID[mbRecordingTwoID] = MBRecordingTarget{EvidenceID: strings.Repeat("e", maxMBEvidenceID+1), SubjectID: 1}
	refuseTargets(longID)
	zeroSubject := mbRecordingTargets()
	zeroSubject[mbRecordingTwoID] = MBRecordingTarget{EvidenceID: "recording-evidence-four", SubjectID: 0}
	refuseTargets(zeroSubject)
	artistObservation := mbObservation(enrich.Artist, mbArtistID)
	artistObservation.Recordings = mbRecordingTargets()
	mbAssertRefused(t, mbConsent(), artistObservation, mbFixture(t, "musicbrainz_artist.json"), ErrMBEvidence)
	// An empty media list is not a complete release.
	index := strings.Index(string(body), `"media": [`)
	if index < 0 {
		t.Fatal("fixture lacks media")
	}
	mbAssertRefused(t, mbConsent(), o,
		[]byte(string(body)[:index]+`"media": []}`), ErrMBEvidence)
	// A declared empty medium is refused: no medium may claim zero tracks.
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"track-count": 1,`, `"track-count": 0,`), ErrMBEvidence)
}

func TestIPMBArtistAndRecordingEvidence(t *testing.T) {
	body := mbFixture(t, "musicbrainz_artist.json")
	o := mbObservation(enrich.Artist, mbArtistID)
	doc, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	want := mbWantInput(o, body, "Massive Attack", "")
	want.Country, want.Disambiguation = "GB", "Bristol trip hop collective"
	want.ArtistType = writer.EvidenceText{Present: true, Value: "Group"}
	want.BeginYear = writer.EvidenceInteger{Present: true, Value: 1988}
	if !reflect.DeepEqual(doc.HeaderInput(), want) {
		t.Fatalf("artist header %+v", doc.HeaderInput())
	}
	if len(doc.Terms()) != 0 || len(doc.Media()) != 0 || len(doc.Tracks()) != 0 ||
		len(doc.Credits()) != 0 || doc.Class() != "" || doc.HeaderInput().LengthMillis.Present {
		t.Fatalf("artist carried foreign families: %+v", doc)
	}
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"begin": "1988-01"`, `"begin": "1988-13"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, mbMutate(t, body, `"begin": "1988-01"`, `"begin": "1990"`),
			`"end": ""`, `"end": "1980"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"country": "GB"`, `"country": "GBR"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"name": "Massive Attack",`, `"name": "",`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"type": "Group"`, `"type": "`+strings.Repeat("T", maxMBShortText+1)+`"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"sort-name": "Massive Attack",`, `"sort-name": "`+strings.Repeat("S", maxMBNameText+1)+`",`), ErrMBEvidence)

	recording := mbFixture(t, "musicbrainz_recording.json")
	ro := mbObservation(enrich.Recording, mbRecordingOneID)
	r, err := PrepareMusicBrainz(mbConsent(), ro, recording)
	if err != nil {
		t.Fatal(err)
	}
	wantRecording := mbWantInput(ro, recording, "Safe from Harm",
		"Massive Attack feat. Shara Nelson")
	wantRecording.Disambiguation = "album version"
	wantRecording.LengthMillis = writer.EvidenceInteger{Present: true, Value: 341000}
	if !reflect.DeepEqual(r.HeaderInput(), wantRecording) {
		t.Fatalf("recording header %+v", r.HeaderInput())
	}
	wantCredits := []MBCredit{
		{Ordinal: 0, Class: "artist", ProviderPersonID: mbArtistID, Name: "Massive Attack",
			ArtistName: "Massive Attack", SortName: "Massive Attack", JoinPhrase: " feat. ",
			SourceOrdinalPresent: true},
		{Ordinal: 1, Class: "artist", ProviderPersonID: mbFeaturedID, Name: "Shara Nelson",
			ArtistName: "Shara Nelson", SortName: "Nelson, Shara", Disambiguation: "British singer",
			SourceOrdinal: 1, SourceOrdinalPresent: true},
	}
	if !reflect.DeepEqual(r.Credits(), wantCredits) || len(r.Tracks()) != 0 || len(r.Terms()) != 0 {
		t.Fatalf("recording families %+v", r.Credits())
	}
	// A null length is an absent length, not a zero observation.
	nullLength, err := PrepareMusicBrainz(mbConsent(), ro,
		mbMutate(t, recording, `"length": 341000,`+"\n"+`  "disambiguation"`,
			`"length": null,`+"\n"+`  "disambiguation"`))
	if err != nil || nullLength.HeaderInput().LengthMillis.Present {
		t.Fatalf("null length %+v, %v", nullLength.HeaderInput().LengthMillis, err)
	}
	mbAssertRefused(t, mbConsent(), ro,
		mbMutate(t, recording, `"length": 341000,`+"\n"+`  "disambiguation"`,
			`"length": -1,`+"\n"+`  "disambiguation"`), ErrMBEvidence)
	mbAssertRefused(t, mbConsent(), ro,
		mbMutate(t, recording, `"length": 341000,`+"\n"+`  "disambiguation"`,
			`"length": 1000001,`+"\n"+`  "disambiguation"`), ErrMBEvidence)
	index := strings.Index(string(recording), `"artist-credit": [`)
	if index < 0 {
		t.Fatal("fixture lacks artist credits")
	}
	mbAssertRefused(t, mbConsent(), ro,
		[]byte(string(recording)[:index]+`"artist-credit": []}`), ErrMBEvidence)
}

func TestIPMBRejectsConsentNamespaceAndBody(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	for _, consent := range []MBConsent{
		{OnlineLookups: 0, LookupEpoch: 7},
		{OnlineLookups: 2, LookupEpoch: 7},
		{OnlineLookups: 1, LookupEpoch: 0},
		{OnlineLookups: 1, LookupEpoch: -1},
	} {
		mbAssertRefused(t, consent, o, body, ErrMBConsent)
	}
	for _, mutate := range []func(*MBObservation){
		func(o *MBObservation) { o.Route.Kind = enrich.Movie },
		func(o *MBObservation) { o.Route.RequestedID = "" },
		func(o *MBObservation) { o.Route.RequestedID = strings.ToUpper(mbReleaseID) },
		func(o *MBObservation) { o.Route.RequestedID = mbReleaseID[:35] },
		func(o *MBObservation) { o.SourceURL = "http://musicbrainz.org/ws/2/release/" + mbReleaseID },
		func(o *MBObservation) {
			o.SourceURL = "https://user:credential@musicbrainz.org/ws/2/release/" + mbReleaseID
		},
		func(o *MBObservation) { o.SourceURL = "https://musicbrainz.org.evil/ws/2/release/" + mbReleaseID },
		func(o *MBObservation) { o.SourceURL = "https://musicbrainz.org:444/ws/2/release/" + mbReleaseID },
		func(o *MBObservation) { o.SourceURL = "https://musicbrainz.org/ws/2/artist/" + mbReleaseID },
		func(o *MBObservation) { o.SourceURL = "https://musicbrainz.org/ws/2/release/" + mbReleaseID + "/extra" },
		func(o *MBObservation) { o.EvidenceID = "" },
		func(o *MBObservation) { o.SubjectID = 0 },
		func(o *MBObservation) { o.ObservedAt = "invented" },
		func(o *MBObservation) { o.ETag = strings.Repeat("x", maxMBResponseBytes+1) },
		func(o *MBObservation) { o.LastModified = "bad\x00validator" },
	} {
		observation := o
		mutate(&observation)
		mbAssertRefused(t, mbConsent(), observation, body, ErrMBEvidence)
	}
	// Credentials never enter evidence or an error message.
	leaky := o
	leaky.SourceURL = "https://user:credential@musicbrainz.org/ws/2/release/" + mbReleaseID
	if _, err := PrepareMusicBrainz(mbConsent(), leaky, body); err == nil ||
		strings.Contains(err.Error(), "credential") {
		t.Fatal("credential leaked through preparation:", err)
	}
	// Query and fragment never become evidence: only the closed origin/path is
	// reconstructed from an accepted HTTPS source URL.
	withQuery := o
	withQuery.SourceURL = "https://musicbrainz.org/ws/2/release/" + mbReleaseID +
		"?fmt=json&inc=artist-credits#fragment"
	doc, err := PrepareMusicBrainz(mbConsent(), withQuery, body)
	if err != nil || doc.HeaderInput().SourceURL != musicBrainzOrigin+"/ws/2/release/"+mbReleaseID {
		t.Fatalf("source URL %q, %v", doc.HeaderInput().SourceURL, err)
	}
	for _, bad := range [][]byte{
		nil,
		[]byte(`[]`),
		[]byte(`{}`),
		[]byte(`{"id":"` + mbReleaseID + `"}`),
		[]byte(`{"id":"` + mbReleaseID + `","id":"` + mbReleaseID + `","title":"T","artist-credit":[],"release-group":{},"media":[]}`),
		[]byte(strings.Repeat("[", maxMBJSONDepth+8) + strings.Repeat("]", maxMBJSONDepth+8)),
		append(append([]byte{}, body...), []byte(`{}`)...),
		{0xff, 0xfe},
		make([]byte, maxMBResponseBytes+1),
	} {
		mbAssertRefused(t, mbConsent(), o, bad, ErrMBEvidence)
	}
	mbAssertRefused(t, mbConsent(), o,
		mbMutate(t, body, `"title": "Collected",`, `"title": "Collected\u0000",`), ErrMBEvidence)
}

func TestIPMBReleaseBounds(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	for _, mutation := range [][2]string{
		{`"title": "Collected",`, `"title": "",`},
		{`    "title": "Collected",`, `    "title": "",`},
		{`"barcode": "0077778622821"`, `"barcode": "ABC"`},
		{`"country": "GB"`, `"country": "gb"`},
		{`"date": "1991-04-08"`, `"date": "1991-02-30"`},
		{`"format": "CD"`, `"format": "` + strings.Repeat("F", maxMBShortText+1) + `"`},
		{`"title": "Bonus Disc"`, `"title": "` + strings.Repeat("T", maxMBText+1) + `"`},
		{`"title": "Collected",`, `"title": "` + strings.Repeat("T", maxMBText+1) + `",`},
		{`["Compilation", "Remix"]`, mbJSONArray(t, strings.Repeat("S", maxMBShortText+1))},
	} {
		mbAssertRefused(t, mbConsent(), o, mbMutate(t, body, mutation[0], mutation[1]), ErrMBEvidence)
	}
	// A body that structurally exceeds a family bound is refused, not trimmed.
	// Each generated body carries complete recording bindings so the refusal is
	// for the named family bound, not a missing recording target.
	oneCredit := mbCreditJSON("A")
	var media []string
	for i := 1; i <= maxMBMedia+1; i++ {
		media = append(media, mbGeneratedMedium(i, []string{mbGeneratedTrack(i, "[]")}))
	}
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, maxMBMedia+1),
		mbGeneratedRelease(oneCredit, strings.Join(media, ",")), ErrMBEvidence)
	var tracks []string
	for i := 1; i <= maxMBTracks+1; i++ {
		tracks = append(tracks, mbGeneratedTrack(i, "[]"))
	}
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, maxMBTracks+1),
		mbGeneratedRelease(oneCredit, mbGeneratedMedium(1, tracks)), ErrMBEvidence)
	// The per-array artist-credit bound is 128.
	credits := make([]string, maxMBArtistCredits)
	for i := range credits {
		credits[i] = mbCreditJSON("A")
	}
	if _, err := PrepareMusicBrainz(mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease(strings.Join(credits, ","),
			mbGeneratedMedium(1, []string{mbGeneratedTrack(1, "[]")}))); err != nil {
		t.Fatal("the exact 128-credit array was refused:", err)
	}
	overArray := make([]string, maxMBArtistCredits+1)
	for i := range overArray {
		overArray[i] = mbCreditJSON("A")
	}
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease(strings.Join(overArray, ","),
			mbGeneratedMedium(1, []string{mbGeneratedTrack(1, "[]")})), ErrMBEvidence)
	// A track's own credit array is validated before any track-scoped row is
	// materialized.
	badTrackCredit := strings.Replace(mbCreditJSON("A"), mbArtistID, "not-an-mbid", 1)
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease(oneCredit, mbGeneratedMedium(1, []string{
			mbGeneratedTrack(1, "["+badTrackCredit+"]"),
		})), ErrMBEvidence)
	// A release must declare at least one artist credit.
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease("", mbGeneratedMedium(1, []string{mbGeneratedTrack(1, "[]")})), ErrMBEvidence)
	// A single credit name and the joined display are separately bounded.
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease(mbCreditJSON(strings.Repeat("N", maxMBNameText+1)),
			mbGeneratedMedium(1, []string{mbGeneratedTrack(1, "[]")})), ErrMBEvidence)
	longJoin := strings.Replace(mbCreditJSON(strings.Repeat("N", maxMBNameText)),
		`"joinphrase":""`, `"joinphrase":"`+strings.Repeat("J", maxMBShortText)+`"`, 1)
	mbAssertRefused(t, mbConsent(), mbGeneratedObservation(o, 1),
		mbGeneratedRelease(longJoin+","+mbCreditJSON(strings.Repeat("N", maxMBNameText)),
			mbGeneratedMedium(1, []string{mbGeneratedTrack(1, "[]")})), ErrMBEvidence)
}

func TestIPMBEvidenceCopiesCallerInputs(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	doc, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	before := doc.Digest()
	media, tracks, credits, terms := doc.Media(), doc.Tracks(), doc.Credits(), doc.Terms()
	if len(media) == 0 || len(tracks) == 0 || len(credits) == 0 || len(terms) == 0 {
		t.Fatal("fixture families missing")
	}
	media[0].Title, tracks[0].Title, credits[0].Name, terms[0].Value = "changed", "changed", "changed", "changed"
	if doc.Media()[0].Title != "" || doc.Tracks()[0].Title != "Safe from Harm" ||
		doc.Credits()[0].Name != "Massive Attack" || doc.Terms()[0].Value != "Compilation" {
		t.Fatal("accessor returned doc-owned slices")
	}
	input := doc.HeaderInput()
	input.Title = "changed"
	if doc.HeaderInput().Title != "Collected" {
		t.Fatal("header input was not copied")
	}
	if doc.Digest() != before {
		t.Fatal("digest changed without an observation change")
	}
	other, err := PrepareMusicBrainz(MBConsent{OnlineLookups: 1, LookupEpoch: 8}, o, body)
	if err != nil {
		t.Fatal(err)
	}
	if other.Digest() == before || other.LookupEpoch() != 8 {
		t.Fatal("the consent epoch is not bound to the preparation digest")
	}
}

// TestIPMBEvidenceMatchesIP06AdmissionShape pins the adapter output to the IP-06
// admission shape through the exported writer evidence types. The writer's
// begin/append guards are unexported, so this asserts every invariant they
// enforce for this projection: the exact v2 pair per subject kind, a header the
// writer accepts for every family, pre-staged distinct recording headers, media
// and track coordinates, and track-scoped credits that name exactly one
// declared track while preserving provider order.
func TestIPMBEvidenceMatchesIP06AdmissionShape(t *testing.T) {
	releaseBody := mbFixture(t, "musicbrainz_release.json")
	releaseObs := mbObservation(enrich.Release, mbReleaseID)
	release, err := PrepareMusicBrainz(mbConsent(), releaseObs, releaseBody)
	if err != nil {
		t.Fatal(err)
	}
	artist, err := PrepareMusicBrainz(mbConsent(),
		mbObservation(enrich.Artist, mbArtistID), mbFixture(t, "musicbrainz_artist.json"))
	if err != nil {
		t.Fatal(err)
	}
	recordingObs := mbObservation(enrich.Recording, mbRecordingOneID)
	recording, err := PrepareMusicBrainz(mbConsent(), recordingObs,
		mbFixture(t, "musicbrainz_recording.json"))
	if err != nil {
		t.Fatal(err)
	}

	// ipEntityEvidencePair's exact spelling for this provider. The superseded
	// v1 pair encodes the old credit-before-media/track order and is refused.
	for _, tc := range []struct {
		kind          enrich.SubjectKind
		parser, shape string
	}{
		{enrich.Release, "ip.musicbrainz.release.evidence.complete-ordered-families.v2",
			"ip.musicbrainz.release.evidence.detail+recordings+media+tracks+credits.v2"},
		{enrich.Artist, "ip.musicbrainz.artist.evidence.complete-ordered-families.v2",
			"ip.musicbrainz.artist.evidence.detail+recordings+media+tracks+credits.v2"},
		{enrich.Recording, "ip.musicbrainz.recording.evidence.complete-ordered-families.v2",
			"ip.musicbrainz.recording.evidence.detail+recordings+media+tracks+credits.v2"},
	} {
		if got, want := mbParser(tc.kind), tc.parser; got != want {
			t.Fatalf("%s parser %q, want %q", tc.kind, got, want)
		}
		if got, want := mbShape(tc.kind), tc.shape; got != want {
			t.Fatalf("%s shape %q, want %q", tc.kind, got, want)
		}
	}
	for _, doc := range []MBEvidence{release, artist, recording} {
		header := doc.HeaderInput()
		if strings.Contains(header.ParserVersion, ".v1") || strings.Contains(header.RequestShape, ".v1") {
			t.Fatalf("superseded header identity %+v", header)
		}
	}

	// The release header and each pre-staged recording header pass the exported
	// writer header construction and control preflight.
	controls := append([]writer.EvidenceHeaderInput{release.HeaderInput()}, release.Recordings()...)
	for _, input := range controls {
		if input.Provider != "musicbrainz" || input.ID == "" || input.ProviderID == "" || input.SubjectID <= 0 {
			t.Fatalf("evidence identity %+v", input)
		}
		if _, err := writer.NewEvidenceHeader(input); err != nil {
			t.Fatalf("writer refused evidence header %q: %v", input.ID, err)
		}
		if _, err := writer.CheckEvidenceStageControls(input, 0, 0); err != nil {
			t.Fatalf("writer refused evidence controls %q: %v", input.ID, err)
		}
	}

	// Media positions are positive and contiguous from one in family order.
	for i, medium := range release.Media() {
		if medium.Position != int64(i)+1 {
			t.Fatalf("medium %d position %d", i, medium.Position)
		}
	}
	// Each distinct recording header is staged once, in first-seen order, and
	// every track names one of them without reusing a track ID.
	recordings := release.Recordings()
	if len(recordings) == 0 {
		t.Fatal("no recording headers")
	}
	recordingByID := make(map[string]bool, len(recordings))
	for _, header := range recordings {
		if header.SubjectKind != "recording" || header.Provider != "musicbrainz" ||
			header.ParserVersion != mbParser(enrich.Recording) ||
			header.RequestShape != mbShape(enrich.Recording) || recordingByID[header.ID] {
			t.Fatalf("recording header %+v", header)
		}
		recordingByID[header.ID] = true
	}
	seenTrack := make(map[string]bool, len(release.Tracks()))
	seenRecording := make(map[string]bool, len(recordings))
	for _, track := range release.Tracks() {
		if seenTrack[track.TrackID] || track.TrackID == "" || track.MediumPosition <= 0 || track.Position <= 0 {
			t.Fatalf("track identity %+v", track)
		}
		seenTrack[track.TrackID] = true
		if track.RecordingEvidenceID == "" || track.Recording.ID != track.RecordingEvidenceID ||
			track.Recording.ProviderID != track.RecordingID ||
			track.Recording.SubjectKind != "recording" || !recordingByID[track.RecordingEvidenceID] {
			t.Fatalf("track recording binding %+v", track)
		}
		seenRecording[track.RecordingEvidenceID] = true
	}
	if len(seenRecording) != len(recordings) {
		t.Fatalf("recording headers %d, referenced %d", len(recordings), len(seenRecording))
	}

	// Credit ordinals are contiguous from zero; every row is one complete artist
	// credit and names either the subject itself or exactly one declared track,
	// preserving that credit array's provider order.
	scopes := make(map[string][]MBCredit, len(seenTrack)+1)
	releaseScoped := 0
	for i, credit := range release.Credits() {
		if credit.Ordinal != int64(i) || credit.Class != "artist" ||
			credit.ProviderPersonID == "" || credit.Name == "" || !credit.SourceOrdinalPresent {
			t.Fatalf("credit %d %+v", i, credit)
		}
		if credit.Scope != "" && !seenTrack[credit.Scope] {
			t.Fatalf("credit %d scope %q names no declared track", i, credit.Scope)
		}
		if credit.Scope == "" {
			releaseScoped++
			if releaseScoped > 1 {
				t.Fatalf("credit %d added a second subject-scoped row", i)
			}
		}
		scopes[credit.Scope] = append(scopes[credit.Scope], credit)
	}
	for scope, credits := range scopes {
		if scope == "" {
			continue
		}
		for i, credit := range credits {
			if credit.SourceOrdinal != int64(i) {
				t.Fatalf("scope %q credit %d source ordinal %d", scope, i, credit.SourceOrdinal)
			}
		}
	}
	// Every track-scoped row follows the release's own credit, and the release
	// credit is the only subject-scoped row.
	scoped := false
	for _, credit := range release.Credits() {
		if credit.Scope != "" {
			scoped = true
			continue
		}
		if scoped {
			t.Fatal("subject-scoped credit followed a track-scoped credit")
		}
	}

	// A non-release observation carries no track, media or scoped credit.
	for _, doc := range []MBEvidence{artist, recording} {
		if len(doc.Tracks()) != 0 || len(doc.Media()) != 0 || len(doc.Recordings()) != 0 {
			t.Fatalf("%s carried a release family", doc.HeaderInput().SubjectKind)
		}
		for _, credit := range doc.Credits() {
			if credit.Scope != "" {
				t.Fatalf("%s credit scope %q", doc.HeaderInput().SubjectKind, credit.Scope)
			}
		}
	}
}

// TestIPMBEvidenceDigestBindsRecordingAndCreditIdentity proves the preparation
// digest covers the recording subject binding and the track-scoped credit
// identity, so two different preparations never share an identity.
func TestIPMBEvidenceDigestBindsRecordingAndCreditIdentity(t *testing.T) {
	body := mbFixture(t, "musicbrainz_release.json")
	o := mbObservation(enrich.Release, mbReleaseID)
	base, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	again, err := PrepareMusicBrainz(mbConsent(), o, body)
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest() != base.Digest() {
		t.Fatal("identical preparation produced a different digest")
	}
	moved := o
	moved.Recordings = mbRecordingTargets()
	moved.Recordings[mbRecordingOneID] = MBRecordingTarget{
		EvidenceID: mbRecordingOneEvidence, SubjectID: mbRecordingOneSubject + 1}
	elsewhere, err := PrepareMusicBrainz(mbConsent(), moved, body)
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere.Digest() == base.Digest() {
		t.Fatal("the recording subject binding is not in the preparation digest")
	}
	// A track-scoped credit identity (join phrase and provider order) is bound
	// too: reordering the track's credit array changes the digest.
	reordered := mbMutate(t, body, `"joinphrase": " feat. ",`, `"joinphrase": " & ",`)
	swapped, err := PrepareMusicBrainz(mbConsent(), o, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if swapped.Digest() == base.Digest() {
		t.Fatal("track credit identity is not in the preparation digest")
	}
}
