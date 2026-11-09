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

func ipPersonFixture(t *testing.T) ([]byte, PersonObservation) {
	t.Helper()
	body, err := os.ReadFile("testdata/tmdb_person.json")
	if err != nil {
		t.Fatal(err)
	}
	return body, PersonObservation{EvidenceID: "person-evidence", SubjectID: 13, ProviderID: "42",
		Language: "fr-CA", Region: "CA", ObservedAt: "2026-11-07T13:01:02.000000003Z",
		SourceURL: "https://api.themoviedb.org/3/person/42", ETag: "person-etag",
		LastModified: "person validator"}
}

// ipPersonExpectedHeader is the exact common header for the fixture above.
func ipPersonExpectedHeader(o PersonObservation) writer.EvidenceHeaderInput {
	return writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb",
		SubjectKind: "person", ProviderID: o.ProviderID, ParserVersion: IPPersonParser, RequestShape: IPPersonShape,
		Language: o.Language, Region: o.Region, ObservedAt: o.ObservedAt,
		SourceURL: "https://api.themoviedb.org/3/person/42", ETag: o.ETag, LastModified: o.LastModified,
		Title: "Ada Lovelace", Biography: "Mathematician.", BiographySource: "tmdb",
		BiographySourceURL: "https://api.themoviedb.org/3/person/42",
		BeginYear:          writer.EvidenceInteger{Present: true, Value: 1815},
		EndYear:            writer.EvidenceInteger{Present: true, Value: 1852}}
}

func TestIPPersonExactHeaderAndTypedDates(t *testing.T) {
	body, o := ipPersonFixture(t)
	input := append([]byte(nil), body...)
	doc, err := PrepareTMDBPerson(tvPolicy(t), o, input)
	if err != nil {
		t.Fatal(err)
	}
	wantInput := ipPersonExpectedHeader(o)
	wantInput.ObservationDigest = sha256.Sum256(body)
	want, err := writer.NewEvidenceHeader(wantInput)
	if err != nil || !reflect.DeepEqual(doc.Header(), want) {
		t.Fatalf("common header mismatch: %+v, %v", doc.Header(), err)
	}
	if doc.Ref() != (enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Person, ExternalID: "42"}) ||
		doc.Name() != "Ada Lovelace" || doc.Header().SubjectID() != 13 {
		t.Fatalf("person identity mismatch: %+v", doc)
	}
	if doc.Fields() != PersonName|PersonBiography|PersonBirthDate|PersonDeathDate|PersonBirthPlace || doc.Nulls() != 0 {
		t.Fatalf("presence mask mismatch: %b/%b", doc.Fields(), doc.Nulls())
	}
	if doc.BirthDate() != (writer.EvidenceText{Value: "1815-12-10", Present: true}) ||
		doc.DeathDate() != (writer.EvidenceText{Value: "1852-11-27", Present: true}) ||
		doc.BirthPlace() != (writer.EvidenceText{Value: "London, England, UK", Present: true}) {
		t.Fatalf("typed birth/death/place mismatch: %+v", doc)
	}
	// The frozen body, not the caller's slice, owns the prepared document.
	clear(input)
	if !reflect.DeepEqual(doc.Header(), want) || doc.Name() != "Ada Lovelace" {
		t.Fatal("borrowed observation body")
	}
	// Unknown provider fields stay in the raw observation but never become
	// declared person facts.
	changed, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(strings.Replace(string(body), "Acting", "Writing", 1)))
	if err != nil || reflect.DeepEqual(changed.Header(), doc.Header()) ||
		changed.Fields() != doc.Fields() || changed.Nulls() != doc.Nulls() {
		t.Fatal("unknown field handling", err)
	}
}

func TestIPPersonPresenceNullEmptyAndAbsent(t *testing.T) {
	const base = `"id":42,"name":"Ada"`
	for _, tc := range []struct {
		name   string
		extra  string
		fields PersonFields
		nulls  PersonFields
		birth  writer.EvidenceText
		death  writer.EvidenceText
		place  writer.EvidenceText
	}{
		{"absent", "", PersonName, 0, writer.EvidenceText{}, writer.EvidenceText{}, writer.EvidenceText{}},
		{"null biography", `,"biography":null`, PersonName | PersonBiography, PersonBiography,
			writer.EvidenceText{}, writer.EvidenceText{}, writer.EvidenceText{}},
		{"empty biography", `,"biography":""`, PersonName | PersonBiography, 0,
			writer.EvidenceText{}, writer.EvidenceText{}, writer.EvidenceText{}},
		{"null dates", `,"birthday":null,"deathday":null,"place_of_birth":null`,
			PersonName | PersonBirthDate | PersonDeathDate | PersonBirthPlace,
			PersonBirthDate | PersonDeathDate | PersonBirthPlace,
			writer.EvidenceText{}, writer.EvidenceText{}, writer.EvidenceText{}},
		{"empty dates", `,"birthday":"","deathday":"","place_of_birth":""`,
			PersonName | PersonBirthDate | PersonDeathDate | PersonBirthPlace, 0,
			writer.EvidenceText{Value: "", Present: true},
			writer.EvidenceText{Value: "", Present: true}, writer.EvidenceText{Value: "", Present: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, o := ipPersonFixture(t)
			doc, err := PrepareTMDBPerson(tvPolicy(t), o, []byte("{"+base+tc.extra+"}"))
			if err != nil {
				t.Fatal(err)
			}
			if doc.Fields() != tc.fields || doc.Nulls() != tc.nulls ||
				doc.BirthDate() != tc.birth || doc.DeathDate() != tc.death || doc.BirthPlace() != tc.place {
				t.Fatalf("presence mismatch: %b/%b %+v", doc.Fields(), doc.Nulls(), doc)
			}
			if got := doc.Header(); got.SubjectID() != o.SubjectID || got.ID() != o.EvidenceID {
				t.Fatalf("header identity changed with presence: %+v", got)
			}
		})
	}
	// An explicit empty biography is supplied text, while a null one is not.
	_, o := ipPersonFixture(t)
	empty, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(`{"id":42,"name":"Ada","biography":""}`))
	if err != nil {
		t.Fatal(err)
	}
	null, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(`{"id":42,"name":"Ada","biography":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Nulls() != 0 || null.Nulls() != PersonBiography || empty.Fields() != null.Fields() {
		t.Fatal("explicit empty biography lost its presence")
	}
}

func TestIPPersonExactDatesOnly(t *testing.T) {
	_, o := ipPersonFixture(t)
	leap, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(`{"id":42,"name":"Ada","birthday":"2000-02-29","deathday":"2000-02-29"}`))
	if err != nil || leap.BirthDate().Value != "2000-02-29" || leap.DeathDate().Value != "2000-02-29" {
		t.Fatalf("leap date refused: %+v, %v", leap, err)
	}
	for _, date := range []string{
		"1990-02-29", "1990-2-3", "1990-02-30", "0000-01-01", "1990-13-01",
		"1990-01-01T00:00:00Z", "1990-01-01 00:00:00", "90-01-01", "1990-01-01x", "invented",
	} {
		body := []byte(`{"id":42,"name":"Ada","birthday":` + strconv.Quote(date) + `}`)
		if got, err := PrepareTMDBPerson(tvPolicy(t), o, body); err == nil || !reflect.DeepEqual(got, PersonDocument{}) {
			t.Fatalf("accepted approximate date %q: %+v", date, got)
		}
	}
	for _, body := range []string{
		`{"id":42,"name":"Ada","birthday":19900101}`,
		`{"id":42,"name":"Ada","birthday":true}`,
		`{"id":42,"name":"Ada","birthday":[]}`,
		`{"id":42,"name":"Ada","birthday":"2000-01-02","deathday":"2000-01-01"}`,
		`{"id":42,"name":"Ada","deathday":"1999-12-31","birthday":"2000-01-01"}`,
	} {
		if got, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(body)); err == nil || !reflect.DeepEqual(got, PersonDocument{}) {
			t.Fatalf("accepted contradictory date: %+v", got)
		}
	}
}

func TestIPPersonRejectsMalformedWholeObservations(t *testing.T) {
	_, o := ipPersonFixture(t)
	for _, body := range []string{
		`{"id":100,"name":"Ada"}`,
		`{"id":42.0,"name":"Ada"}`,
		`{"id":null,"name":"Ada"}`,
		`{"id":42,"id":42,"name":"Ada"}`,
		`{"id":42}`,
		`{"id":42,"name":null}`,
		`{"id":42,"name":"   "}`,
		`{"id":42,"name":""}`,
		`{"id":42,"name":"\ud800"}`,
		"{\"id\":42,\"name\":\"\xff\"}",
		`{"id":42,"name":"Ada","biography":{"text":"nested"}}`,
		`{"id":42,"name":"Ada","place_of_birth":[]}`,
		`{"id":42,"name":"Ada","name":"Grace"}`,
		`{"id":42,"name":"Ada"} trailing`,
		`{"id":42,"name":"Ada"}{}`,
		`[]`,
		`{"id":42,"name":"Ada","place_of_birth":"` + strings.Repeat("x", maxIPPersonText+1) + `"}`,
	} {
		if got, err := PrepareTMDBPerson(tvPolicy(t), o, []byte(body)); err == nil || !reflect.DeepEqual(got, PersonDocument{}) {
			t.Fatalf("accepted malformed observation %.48q: %+v", body, got)
		}
	}
}

func TestIPPersonRejectsWrongNamespaceAndEnvelope(t *testing.T) {
	body, base := ipPersonFixture(t)
	for _, mutate := range []func(*PersonObservation){
		func(o *PersonObservation) { o.ProviderID = "042" },
		func(o *PersonObservation) { o.ProviderID = "0" },
		func(o *PersonObservation) { o.ProviderID = "-1" },
		func(o *PersonObservation) { o.ProviderID = "42.0" },
		func(o *PersonObservation) { o.ProviderID = "100" },
		func(o *PersonObservation) { o.SourceURL = "http://api.themoviedb.org/3/person/42?secret" },
		func(o *PersonObservation) { o.SourceURL = "https://api.themoviedb.org.evil/3/person/42?secret" },
		func(o *PersonObservation) { o.SourceURL = "https://api.themoviedb.org:444/3/person/42?secret" },
		func(o *PersonObservation) { o.SourceURL = "https://api.themoviedb.org/3/movie/42?secret" },
		func(o *PersonObservation) { o.SourceURL = "https://api.themoviedb.org/3/collection/42?secret" },
		func(o *PersonObservation) { o.SourceURL = "https://api.themoviedb.org/3/person/42/extra?secret" },
		func(o *PersonObservation) { o.SubjectID = 0 },
		func(o *PersonObservation) { o.EvidenceID = "" },
		func(o *PersonObservation) { o.ObservedAt = "invented" },
		func(o *PersonObservation) { o.ETag = strings.Repeat("x", (4<<20)+1) },
		func(o *PersonObservation) { o.Language = string([]byte{0xff}) },
		func(o *PersonObservation) { o.LastModified = "nul\x00byte" },
	} {
		o := base
		mutate(&o)
		got, err := PrepareTMDBPerson(tvPolicy(t), o, body)
		if err == nil || !reflect.DeepEqual(got, PersonDocument{}) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid person namespace: %+v, %v", got, err)
		}
	}
	if _, err := PrepareTMDBPerson(enrich.TMDBPolicy{}, base, body); !errors.Is(err, enrich.ErrTMDBPolicy) {
		t.Fatal(err)
	}
	if _, err := PrepareTMDBPerson(tvPolicy(t), base, make([]byte, (4<<20)+1)); err == nil {
		t.Fatal("accepted oversized body")
	}
	// Credentials and query never reach the evidence or an error.
	o := base
	o.SourceURL = "https://user:credential@API.THEMOVIEDB.ORG:443/3/person/42?api_key=credential#credential"
	doc, err := PrepareTMDBPerson(tvPolicy(t), o, body)
	if err != nil || doc.Header().ID() != o.EvidenceID || strings.Contains(doc.Header().ID(), "credential") {
		t.Fatal(err)
	}
	clean, err := PrepareTMDBPerson(tvPolicy(t), base, body)
	if err != nil || !reflect.DeepEqual(doc.Header(), clean.Header()) {
		t.Fatal("credential-bearing URL changed the evidence")
	}
}

// TestIPPersonAndEntitySameNumericIDStayIsolated holds the person namespace
// apart from catalogue entity evidence that shares both numeric identifiers.
func TestIPPersonAndEntitySameNumericIDStayIsolated(t *testing.T) {
	personBody, personObs := ipPersonFixture(t)
	person, err := PrepareTMDBPerson(tvPolicy(t), personObs, personBody)
	if err != nil {
		t.Fatal(err)
	}
	tvObs := tvObservation(enrich.Series)
	tvObs.Route.ProviderID, tvObs.SubjectID, tvObs.EvidenceID = "42", 13, "series-evidence"
	series, err := PrepareTMDBTVHeader(tvPolicy(t), tvObs, []byte(`{"id":42,"name":"Series","overview":"","first_air_date":"2024-02-29"}`))
	if err != nil {
		t.Fatal(err)
	}
	if person.Ref() != (enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Person, ExternalID: "42"}) ||
		series.Route().ProviderID != "42" || person.Header().SubjectID() != series.Header().SubjectID() {
		t.Fatalf("fixture did not collide: %+v %+v", person.Ref(), series.Route())
	}
	if person.Ref().Kind == enrich.Series || IPPersonParser == "tmdb.series.header.v1" ||
		IPPersonShape == "tmdb.series.detail.header.v1" {
		t.Fatal("person and entity namespaces are not distinct")
	}
	if _, err := PrepareTMDBTVHeader(tvPolicy(t), tvObs, personBody); err == nil {
		t.Fatal("person body accepted as series evidence")
	}
}
