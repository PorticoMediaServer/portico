package providers

import (
	"crypto/sha256"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func ipCollectionFixture(t *testing.T) ([]byte, CollectionObservation) {
	t.Helper()
	body, err := os.ReadFile("testdata/tmdb_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	return body, CollectionObservation{EvidenceID: "collection-evidence", SubjectID: 13, ProviderID: "42",
		Language: "fr-CA", Region: "CA", ObservedAt: "2026-11-07T13:01:02.000000003Z",
		SourceURL: "https://api.themoviedb.org/3/collection/42", ETag: "collection-etag",
		LastModified: "collection validator"}
}

func ipCollectionExpectedHeader(o CollectionObservation, digest [32]byte) CollectionHeader {
	return CollectionHeader{EvidenceID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb",
		SubjectKind: "collection", ProviderID: o.ProviderID, ParserVersion: IPCollectionParser,
		RequestShape: IPCollectionShape, Language: o.Language, Region: o.Region,
		ObservationDigest: digest, ObservedAt: o.ObservedAt,
		SourceURL: "https://api.themoviedb.org/3/collection/42", ETag: o.ETag,
		LastModified: o.LastModified, Name: "Provider Saga", Overview: "A provider collection."}
}

func TestIPCollectionCompletePartsAndHeader(t *testing.T) {
	body, o := ipCollectionFixture(t)
	input := append([]byte(nil), body...)
	doc, err := PrepareTMDBCollection(tvPolicy(t), o, input)
	if err != nil {
		t.Fatal(err)
	}
	want := ipCollectionExpectedHeader(o, sha256.Sum256(body))
	if !reflect.DeepEqual(doc.Header(), want) || !doc.Complete() {
		t.Fatalf("collection header mismatch: %+v", doc.Header())
	}
	inputWant := writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb",
		SubjectKind: "collection", ProviderID: o.ProviderID, ParserVersion: IPCollectionParser,
		RequestShape: IPCollectionShape, Language: o.Language, Region: o.Region,
		ObservationDigest: sha256.Sum256(body), ObservedAt: o.ObservedAt,
		SourceURL: "https://api.themoviedb.org/3/collection/42", ETag: o.ETag,
		LastModified: o.LastModified, Title: "Provider Saga", Overview: "A provider collection."}
	if !reflect.DeepEqual(doc.EvidenceHeaderInput(), inputWant) {
		t.Fatalf("evidence input mismatch: %+v", doc.EvidenceHeaderInput())
	}
	members := doc.Members()
	wantMembers := []CollectionMember{
		{Ref: enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Movie, ExternalID: "7"}, Name: "First", Ordinal: 0},
		{Ref: enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Movie, ExternalID: "9"}, Name: "Second", Ordinal: 1},
		{Ref: enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Movie, ExternalID: "11"}, Name: "Third", Ordinal: 2},
	}
	if doc.MemberCount() != 3 || !reflect.DeepEqual(members, wantMembers) {
		t.Fatalf("declared membership mismatch: %+v", members)
	}
	digest := doc.MemberDigest()
	if digest == ([32]byte{}) {
		t.Fatal("empty membership digest")
	}
	// A copy is returned; callers cannot mutate the prepared membership.
	members[0].Name = "Changed"
	if doc.Members()[0].Name != "First" {
		t.Fatal("exported membership aliased")
	}
	// The frozen body, not the caller's slice, owns the prepared document.
	clear(input)
	if !reflect.DeepEqual(doc.Header(), want) || !reflect.DeepEqual(doc.Members(), wantMembers) {
		t.Fatal("borrowed observation body")
	}
	// Unknown provider fields remain part of the raw observation digest but
	// never become declared membership or header facts.
	changed, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(strings.Replace(string(body), "saga.jpg", "other.jpg", 1)))
	if err != nil || reflect.DeepEqual(changed.Header(), doc.Header()) ||
		changed.MemberDigest() != doc.MemberDigest() || !reflect.DeepEqual(changed.Members(), wantMembers) {
		t.Fatal("unknown field handling", err)
	}
	// Member titles and declared order are part of the membership identity.
	renamed, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(strings.Replace(string(body), `"title": "Second"`, `"title": "Renamed"`, 1)))
	if err != nil || renamed.MemberDigest() == doc.MemberDigest() {
		t.Fatal("membership digest ignored a title", err)
	}
	reordered, err := PrepareTMDBCollection(tvPolicy(t), o,
		[]byte(`{"id":42,"name":"Provider Saga","overview":"A provider collection.","parts":[{"id":9,"title":"Second"},{"id":7,"title":"First"},{"id":11,"title":"Third"}]}`))
	if err != nil || reordered.MemberDigest() == doc.MemberDigest() ||
		reordered.Members()[0].Ref.ExternalID != "9" || reordered.Members()[0].Ordinal != 0 {
		t.Fatalf("membership digest ignored the declared order: %+v, %v", reordered.Members(), err)
	}
}

func TestIPCollectionPresenceAndDeclaredEmptyParts(t *testing.T) {
	_, o := ipCollectionFixture(t)
	for _, tc := range []struct {
		body   string
		fields CollectionFields
		nulls  CollectionFields
	}{
		{`{"id":42,"name":"Saga","parts":[]}`, CollectionName, 0},
		{`{"id":42,"name":"Saga","overview":null,"parts":[]}`, CollectionName | CollectionOverview, CollectionOverview},
		{`{"id":42,"name":"Saga","overview":"","parts":[]}`, CollectionName | CollectionOverview, 0},
	} {
		doc, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Fields() != tc.fields || doc.Nulls() != tc.nulls || doc.MemberCount() != 0 ||
			!doc.Complete() || doc.Header().Overview != "" {
			t.Fatalf("declared empty membership was not complete: %+v", doc.Header())
		}
	}
	for _, body := range []string{
		`{"id":42,"name":"Saga"}`,
		`{"id":42,"name":"Saga","parts":null}`,
		`{"id":42,"name":"Saga","parts":{}}`,
		`{"id":42,"name":"Saga","parts":"[]"}`,
		`{"id":42,"name":null,"parts":[]}`,
		`{"id":42,"name":"   ","parts":[]}`,
		`{"id":42,"name":"Saga","name":"Other","parts":[]}`,
		`{"id":42,"name":"Saga","parts":[],"parts":[]}`,
	} {
		if got, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(body)); err == nil || !reflect.DeepEqual(got, CollectionDocument{}) {
			t.Fatalf("accepted incomplete or duplicate declaration %.40q: %+v", body, got)
		}
	}
}

func TestIPCollectionRejectsInventedOrDuplicateMembership(t *testing.T) {
	_, o := ipCollectionFixture(t)
	for _, parts := range []string{
		`[{"id":7,"title":"One"},{"id":7,"title":"Two"}]`,
		`[{"id":7,"title":"One"},{"id":"7","title":"Two"}]`,
		`[{"id":0,"title":"One"}]`,
		`[{"id":-1,"title":"One"}]`,
		`[{"id":7.5,"title":"One"}]`,
		`[{"id":7}]`,
		`[{"id":7,"title":null}]`,
		`[{"id":7,"title":"  "}]`,
		`[{"id":7,"title":"One"},{"title":"Two"}]`,
		`[{"id":7,"title":"One","title":"Two"}]`,
		`[{"id":7,"title":"nul\u0000byte"}]`,
		`["not an object"]`,
		`[42]`,
		`[null]`,
		`[[{"id":7}]]`,
		`[{"id":7,"title":"` + strings.Repeat("x", maxIPCollectionName+1) + `"}]`,
	} {
		body := `{"id":42,"name":"Saga","parts":` + parts + `}`
		if got, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(body)); err == nil || !reflect.DeepEqual(got, CollectionDocument{}) {
			t.Fatalf("accepted invented membership %.48q: %+v", parts, got)
		}
	}
	// A member sharing the collection's numeric ID is a different provider
	// namespace; it stays a movie reference rather than becoming this collection.
	doc, err := PrepareTMDBCollection(tvPolicy(t), o,
		[]byte(`{"id":42,"name":"Saga","parts":[{"id":42,"title":"Namesake"}]}`))
	if err != nil || doc.MemberCount() != 1 ||
		doc.Members()[0].Ref != (enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Movie, ExternalID: "42"}) {
		t.Fatalf("same numeric ID was not namespaced: %+v, %v", doc.Members(), err)
	}
	// The declared membership is bounded before it can retain an unbounded body.
	if _, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(ipCollectionPartsBody(maxIPCollectionMembers))); err != nil {
		t.Fatalf("refused the declared maximum: %v", err)
	}
	if _, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(ipCollectionPartsBody(maxIPCollectionMembers+1))); err == nil {
		t.Fatal("accepted an unbounded membership")
	}
}

func ipCollectionPartsBody(count int) string {
	var b strings.Builder
	b.WriteString(`{"id":42,"name":"Saga","parts":[`)
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":` + strconv.Itoa(i) + `,"title":"Member"}`)
	}
	b.WriteString(`]}`)
	return b.String()
}

// TestIPCollectionDistinctSubjectKindAndNamespace holds the collection subject
// kind apart from person and screen entity evidence that shares the same numeric
// provider and subject IDs.
func TestIPCollectionDistinctSubjectKindAndNamespace(t *testing.T) {
	body, o := ipCollectionFixture(t)
	collection, err := PrepareTMDBCollection(tvPolicy(t), o, body)
	if err != nil {
		t.Fatal(err)
	}
	personBody, personObs := ipPersonFixture(t)
	personObs.SubjectID = o.SubjectID
	person, err := PrepareTMDBPerson(tvPolicy(t), personObs, personBody)
	if err != nil {
		t.Fatal(err)
	}
	if string(CollectionKind) != "collection" || CollectionKind == enrich.Movie ||
		CollectionKind == enrich.Person || CollectionKind == enrich.Series ||
		collection.Header().SubjectKind != "collection" || person.Header().ID() == collection.Header().EvidenceID ||
		person.Header().SubjectID() != collection.Header().SubjectID() ||
		person.Ref().Kind == CollectionKind {
		t.Fatal("collection subject kind is not distinct")
	}
	for _, mutate := range []func(*CollectionObservation){
		func(o *CollectionObservation) { o.ProviderID = "042" },
		func(o *CollectionObservation) { o.ProviderID = "0" },
		func(o *CollectionObservation) { o.ProviderID = "100" },
		func(o *CollectionObservation) { o.SourceURL = "http://api.themoviedb.org/3/collection/42?secret" },
		func(o *CollectionObservation) { o.SourceURL = "https://api.themoviedb.org.evil/3/collection/42?secret" },
		func(o *CollectionObservation) { o.SourceURL = "https://api.themoviedb.org:444/3/collection/42?secret" },
		func(o *CollectionObservation) { o.SourceURL = "https://api.themoviedb.org/3/movie/42?secret" },
		func(o *CollectionObservation) { o.SourceURL = "https://api.themoviedb.org/3/person/42?secret" },
		func(o *CollectionObservation) { o.SourceURL = "https://api.themoviedb.org/3/tv/42?secret" },
		func(o *CollectionObservation) {
			o.SourceURL = "https://api.themoviedb.org/3/collection/42/extra?secret"
		},
		func(o *CollectionObservation) { o.SubjectID = 0 },
		func(o *CollectionObservation) { o.EvidenceID = "" },
		func(o *CollectionObservation) { o.ObservedAt = "invented" },
		func(o *CollectionObservation) { o.ETag = strings.Repeat("x", (4<<20)+1) },
		func(o *CollectionObservation) { o.Region = string([]byte{0xff}) },
		func(o *CollectionObservation) { o.LastModified = "nul\x00byte" },
	} {
		changed := o
		mutate(&changed)
		got, err := PrepareTMDBCollection(tvPolicy(t), changed, body)
		if err == nil || !reflect.DeepEqual(got, CollectionDocument{}) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid collection namespace: %+v, %v", got, err)
		}
	}
	for _, malformed := range []string{
		`{"id":42.0,"name":"Saga","parts":[]}`,
		`{"id":null,"name":"Saga","parts":[]}`,
		`{"id":42,"name":"Saga","parts":[]} trailing`,
		`{"id":42,"name":"Saga","parts":[]}{}`,
		`[]`,
	} {
		if got, err := PrepareTMDBCollection(tvPolicy(t), o, []byte(malformed)); err == nil || !reflect.DeepEqual(got, CollectionDocument{}) {
			t.Fatalf("accepted malformed collection %.40q: %+v", malformed, got)
		}
	}
	if _, err := PrepareTMDBCollection(enrich.TMDBPolicy{}, o, body); !errors.Is(err, enrich.ErrTMDBPolicy) {
		t.Fatal(err)
	}
	if _, err := PrepareTMDBCollection(tvPolicy(t), o, make([]byte, (4<<20)+1)); err == nil {
		t.Fatal("accepted oversized body")
	}
	// Credentials and query never reach the evidence or an error.
	secret := o
	secret.SourceURL = "https://user:credential@API.THEMOVIEDB.ORG:443/3/collection/42?api_key=credential#credential"
	got, err := PrepareTMDBCollection(tvPolicy(t), secret, body)
	if err != nil || !reflect.DeepEqual(got.Header(), collection.Header()) {
		t.Fatal("credential-bearing URL changed the evidence", err)
	}
}
