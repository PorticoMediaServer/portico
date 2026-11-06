package enrich

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

const tmdbMovieFixture = `{"id":42,"title":"Title:101","original_title":"Original:103","overview":"Overview é:107","tagline":"Tagline:109","release_date":"2024-02-29","runtime":121,"genres":[{"id":999,"name":"excluded"}],"production_companies":[{"name":"excluded company"}],"credits":{"crew":[{"id":301,"name":"Crew:113","job":"Director","department":"Directing","credit_id":"crew-credit:127"}],"cast":[{"id":201,"name":"Cast:131","character":"Pilot:137","order":7,"credit_id":"cast-credit:139"}]}}`

func tmdbObservation() TMDBMovieObservation {
	return TMDBMovieObservation{EvidenceID: "evidence:149", SubjectID: 151, ProviderID: "42", Language: "en-CA", Region: "CA", ObservedAt: "2026-11-06T10:23:19.000000137+00:00", SourceURL: "https://api.themoviedb.org/3/movie/42", ETag: "etag:157", LastModified: "Thu, 05 Nov 2026 10:00:00 GMT"}
}

func tmdbMoviePolicy(t *testing.T) TMDBPolicy {
	t.Helper()
	p, err := NewTMDBPolicy(TMDBOneWorker80MiB)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTMDBMovieExactProjectionObservationAndCreditOrder(t *testing.T) {
	observation := tmdbObservation()
	body := []byte(tmdbMovieFixture)
	doc, err := PrepareTMDBMovie(tmdbMoviePolicy(t), observation, body)
	if err != nil {
		t.Fatal(err)
	}
	// Independent hashlib answer over ALL474 original UTF8 entity bytes. This
	// is observation SHA256, not the unselected normalized/source seal codec.
	known, err := hex.DecodeString("2df3a373feb6960b20f53ba541a43b2fcdba9c4e5c44a60d80e4763d0da8fd59")
	if err != nil {
		t.Fatal(err)
	}
	var originalDigest [32]byte
	copy(originalDigest[:], known)
	h := writer.EvidenceHeaderInput{ID: "evidence:149", SubjectID: 151, Provider: "tmdb", SubjectKind: "movie", ProviderID: "42", ParserVersion: "tmdb.movie.header-selected-credits.v1", RequestShape: "tmdb.movie.detail+credits.header-selected-credits.v1", Language: "en-CA", Region: "CA", ObservationDigest: originalDigest, ObservedAt: observation.ObservedAt, SourceURL: observation.SourceURL, ETag: observation.ETag, LastModified: observation.LastModified, Title: "Title:101", OriginalTitle: "Original:103", Overview: "Overview é:107", Tagline: "Tagline:109", ReleaseDate: "2024-02-29", Year: 2024, LengthMillis: writer.EvidenceInteger{Value: 7260000, Present: true}}
	wantHeader, err := writer.NewEvidenceHeader(h)
	if err != nil || !reflect.DeepEqual(doc.Header(), wantHeader) {
		t.Fatal("native header/whole observation projection mismatch", err)
	}
	wantRows := []ScreenCredit{{Class: Cast, Person: ProviderRef{"tmdb", Person, "201"}, CreditID: "cast-credit:139", Name: "Cast:131", Role: "Pilot:137", Billing: 7, SourceOrdinal: 0}, {Class: Crew, Person: ProviderRef{"tmdb", Person, "301"}, CreditID: "crew-credit:127", Name: "Crew:113", Role: "Director", Department: "Directing", SourceOrdinal: 1}}
	if !reflect.DeepEqual(doc.Credits().Rows(), wantRows) {
		t.Fatal("wire arrival order replaced canonical source order", doc.Credits().Rows())
	}
	selected, err := PrepareScreenCredits(ProviderRef{"tmdb", Movie, "42"}, wantRows)
	if err != nil || selected.Digest() != doc.Credits().Digest() {
		t.Fatal("selected-document codec alias", err)
	}
	stored := int64(wantHeader.TextBytes()) + 64
	for _, row := range wantRows {
		stored += int64(4 + 5 + len(row.Class) + len("evidence:149") + len(row.Person.ExternalID) + len(row.Name) + len(row.Role) + len(row.Department) + len(row.CreditID))
	}
	if doc.StoredBytes() != stored || doc.ContentCharge() != 3*512+8*stored {
		t.Fatal("content TEXT64/charge forecast", doc.StoredBytes(), stored, doc.ContentCharge())
	}
	for i := range body {
		body[i] = 'x'
	}
	observation.EvidenceID = "mutated"
	rows := doc.Credits().Rows()
	rows[0].Name = "mutated"
	if !reflect.DeepEqual(doc.Header(), wantHeader) || !reflect.DeepEqual(doc.Credits().Rows(), wantRows) {
		t.Fatal("retained mutable acquisition")
	}
	// Whitespace alters COMPLETE entity-byte observation, but not the separately
	// selected credit document. Excluded families never become header strings.
	other, err := PrepareTMDBMovie(tmdbMoviePolicy(t), tmdbObservation(), []byte(" \n"+tmdbMovieFixture))
	if err != nil || reflect.DeepEqual(other.Header(), wantHeader) || other.Credits().Digest() != selected.Digest() {
		t.Fatal("observation/selected digest conflated", err)
	}
}

func tmdbCreditsFixture(castCount, crewCount int) []byte {
	cast := make([]map[string]any, castCount)
	crew := make([]map[string]any, crewCount)
	for i := range cast {
		cast[i] = map[string]any{"id": i + 1, "name": fmt.Sprint("Cast ", i), "character": "Role", "order": i, "credit_id": fmt.Sprint("cast-", i)}
	}
	for i := range crew {
		crew[i] = map[string]any{"id": i + 10000, "name": fmt.Sprint("Crew ", i), "job": "Director", "department": "Directing", "credit_id": fmt.Sprint("crew-", i)}
	}
	body, err := json.Marshal(map[string]any{"id": 42, "title": "Synthetic", "credits": map[string]any{"cast": cast, "crew": crew}})
	if err != nil {
		panic(err)
	}
	return body
}

func TestTMDBMovieSelectionOnlyApprovedTrimAndWholeLimits(t *testing.T) {
	policy := tmdbMoviePolicy(t)
	doc, err := PrepareTMDBMovie(policy, tmdbObservation(), tmdbCreditsFixture(101, 51))
	if err != nil || doc.Credits().Count() != 150 {
		t.Fatal("approved trim changed", doc.Credits().Count(), err)
	}
	for i, row := range doc.Credits().Rows() {
		if (i < 100 && row.Class != Cast) || (i >= 100 && row.Class != Crew) {
			t.Fatal("class/order drift", i, row)
		}
	}
	// Malformation in a row the selector would discard still refuses the WHOLE
	// input. The provider may not hide it behind approved count selection.
	bad := strings.Replace(string(tmdbCreditsFixture(101, 0)), `"name":"Cast 100"`, `"name":""`, 1)
	if d, err := PrepareTMDBMovie(policy, tmdbObservation(), []byte(bad)); err == nil || d.Header().ID() != "" {
		t.Fatal("malformed discarded candidate accepted", err)
	}
	oversizedKey := tmdbObservation()
	oversizedKey.EvidenceID = strings.Repeat("i", 50000)
	if d, err := PrepareTMDBMovie(policy, oversizedKey, tmdbCreditsFixture(100, 50)); !errors.Is(err, ErrTMDBMovie) || d.StoredBytes() != 0 {
		t.Fatal("whole6MiB limit shrank/truncated document", err)
	}
	tooMany := `{"id":42,"title":"x","credits":{"cast":[` + strings.Repeat(`{},`, writer.MaxScreenCreditCandidates) + `{}],"crew":[]}}`
	if _, err := PrepareTMDBMovie(policy, tmdbObservation(), []byte(tooMany)); !errors.Is(err, ErrTMDBMovie) {
		t.Fatal("unbounded typed candidate allocation", err)
	}
	large := make([]byte, 4194305)
	_, err = PrepareTMDBMovie(policy, tmdbObservation(), large)
	var size *ResponseSizeError
	if !errors.As(err, &size) || size.ObservedBytes != 4194305 || size.ObservedIsLowerBound {
		t.Fatal("whole input size lost", err)
	}
}

func TestTMDBMovieRejectsMalformedOrIncompleteProjection(t *testing.T) {
	policy := tmdbMoviePolicy(t)
	cases := []string{
		`{}`, `null`, `[]`, tmdbMovieFixture + `{}`,
		strings.Replace(tmdbMovieFixture, `"title":"Title:101"`, `"title":null`, 1),
		strings.Replace(tmdbMovieFixture, `"title":"Title:101"`, `"title":" "`, 1),
		`{"id":42,"title":"x","credits":null}`,
		`{"id":42,"title":"x","credits":{"cast":[],"crew":null}}`,
		`{"id":42,"title":"x","credits":{"cast":[]}}`,
		`{"id":42,"title":"x","credits":{"cast":[],"crew":[],"cast":[]}}`,
		strings.Replace(tmdbMovieFixture, `"id":42`, `"id":42,"id":43`, 1),
		strings.Replace(tmdbMovieFixture, `"id":201`, `"id":201,"id":202`, 1),
		strings.Replace(tmdbMovieFixture, `"order":7`, `"order":null`, 1),
		strings.Replace(tmdbMovieFixture, `"id":201`, `"id":1.5`, 1),
		strings.Replace(tmdbMovieFixture, `"runtime":121`, `"runtime":9223372036854775807`, 1),
		strings.Replace(tmdbMovieFixture, `"runtime":121`, `"runtime":-1`, 1),
		strings.Replace(tmdbMovieFixture, `2024-02-29`, `2023-02-29`, 1),
		string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}),
		strings.Replace(tmdbMovieFixture, `Title:101`, `\ud800`, 1),
		strings.Replace(tmdbMovieFixture, `Crew:113`, `\udc00`, 1),
	}
	for i, body := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			d, err := PrepareTMDBMovie(policy, tmdbObservation(), []byte(body))
			if !errors.Is(err, ErrTMDBMovie) || d.Header().ID() != "" || d.Credits().Count() != 0 {
				t.Fatal("malformed became partial evidence", err)
			}
		})
	}
	other := tmdbObservation()
	other.ProviderID = "0042"
	if _, err := PrepareTMDBMovie(policy, other, []byte(tmdbMovieFixture)); !errors.Is(err, ErrTMDBMovie) {
		t.Fatal("typed opaque subject silently normalized", err)
	}
	if _, err := PrepareTMDBMovie(TMDBPolicy{}, tmdbObservation(), []byte(tmdbMovieFixture)); !errors.Is(err, ErrTMDBPolicy) {
		t.Fatal("missing policy", err)
	}
}

func TestTMDBMovieSafeOriginalMetadataAndUnicode(t *testing.T) {
	observation := tmdbObservation()
	want, err := PrepareTMDBMovie(tmdbMoviePolicy(t), observation, []byte(tmdbMovieFixture))
	if err != nil {
		t.Fatal(err)
	}
	observation.SourceURL = "https://user:secret@api.themoviedb.org/3/movie/42?api_key=secret&unknown_credential=secret#secret"
	got, err := PrepareTMDBMovie(tmdbMoviePolicy(t), observation, []byte(tmdbMovieFixture))
	if err != nil || !reflect.DeepEqual(got.Header(), want.Header()) || got.StoredBytes() != want.StoredBytes() {
		t.Fatal("credential-bearing source URL survived normalization", err)
	}
	observation.SourceURL = "https://attacker.invalid/3/movie/42?api_key=secret"
	if doc, err := PrepareTMDBMovie(tmdbMoviePolicy(t), observation, []byte(tmdbMovieFixture)); !errors.Is(err, ErrSourceURL) || doc.Header().ID() != "" || strings.Contains(err.Error(), "secret") {
		t.Fatal("wrong source route admitted or credential echoed", err)
	}
	// A valid surrogate PAIR and its literal scalar parse identically, while
	// their complete immutable entity-byte observations remain distinct.
	escaped := strings.Replace(tmdbMovieFixture, "Title:101", `\ud83d\ude80`, 1)
	literal := strings.Replace(tmdbMovieFixture, "Title:101", "🚀", 1)
	escapedDoc, err := PrepareTMDBMovie(tmdbMoviePolicy(t), tmdbObservation(), []byte(escaped))
	if err != nil {
		t.Fatal(err)
	}
	literalDoc, err := PrepareTMDBMovie(tmdbMoviePolicy(t), tmdbObservation(), []byte(literal))
	if err != nil || reflect.DeepEqual(escapedDoc.Header(), literalDoc.Header()) || escapedDoc.Credits().Digest() != literalDoc.Credits().Digest() || escapedDoc.StoredBytes() != literalDoc.StoredBytes() {
		t.Fatal("Unicode decoding replaced the original byte observation", err)
	}
}

func TestTMDBMovieExplicitEmptyAndNullableRuntime(t *testing.T) {
	for _, runtime := range []string{"null", "0"} {
		body := `{"id":42,"title":"x","runtime":` + runtime + `,"credits":{"cast":[],"crew":[]}}`
		doc, err := PrepareTMDBMovie(tmdbMoviePolicy(t), tmdbObservation(), []byte(body))
		if err != nil || doc.Credits().Count() != 0 || doc.ContentCharge() != 512+8*doc.StoredBytes() {
			t.Fatal("explicit empty/nullable runtime", runtime, err)
		}
		o := tmdbObservation()
		want, err := writer.NewEvidenceHeader(writer.EvidenceHeaderInput{
			ID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb", SubjectKind: "movie", ProviderID: "42",
			ParserVersion: "tmdb.movie.header-selected-credits.v1", RequestShape: "tmdb.movie.detail+credits.header-selected-credits.v1",
			Language: o.Language, Region: o.Region, ObservationDigest: sha256.Sum256([]byte(body)), ObservedAt: o.ObservedAt,
			SourceURL: o.SourceURL, ETag: o.ETag, LastModified: o.LastModified, Title: "x",
			LengthMillis: writer.EvidenceInteger{Present: runtime == "0"},
		})
		if err != nil || !reflect.DeepEqual(doc.Header(), want) {
			t.Fatal("NULL runtime collapsed into present zero", runtime, err)
		}
	}
}
