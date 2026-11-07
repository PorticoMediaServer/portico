package providers

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func tvPolicy(t *testing.T) enrich.TMDBPolicy {
	t.Helper()
	p, err := enrich.NewTMDBPolicy(enrich.TMDBOneWorker80MiB)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func tvObservation(kind enrich.SubjectKind) TVObservation {
	o := TVObservation{Route: TVRoute{Kind: kind, ProviderID: "42"}, EvidenceID: "tv-evidence",
		SubjectID: 13, Language: "fr-CA", Region: "CA", ObservedAt: "2026-11-07T13:01:02.000000003Z",
		ETag: "etag-original", LastModified: "original validator"}
	switch kind {
	case enrich.Season:
		o.Route.SeriesID, o.Route.Season = "100", 2
	case enrich.Episode:
		o.Route.SeriesID, o.Route.Season, o.Route.Episode = "100", 2, 3
	}
	path, _ := tvPath(o.Route)
	o.SourceURL = "https://api.themoviedb.org" + path
	return o
}

func TestIPTVExactHeaderAndSeparateDescriptor(t *testing.T) {
	const body = `{"id":42,"name":"Série","original_name":"Original","overview":"Overview","tagline":"Tag","first_air_date":"2024-02-29","genres":[{"name":"not selected"}],"aggregate_credits":{"cast":[]}}`
	o := tvObservation(enrich.Series)
	o.SourceURL = "https://user:credential@API.THEMOVIEDB.ORG:443/3/tv/42?api_key=credential#credential"
	input := []byte(body)
	doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, input)
	if err != nil {
		t.Fatal(err)
	}
	h, err := writer.NewEvidenceHeader(writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: 13,
		Provider: "tmdb", SubjectKind: "series", ProviderID: "42", Language: "fr-CA", Region: "CA",
		ParserVersion: "tmdb.series.header.v1", RequestShape: "tmdb.series.detail.header.v1",
		ObservationDigest: sha256.Sum256([]byte(body)), ObservedAt: o.ObservedAt,
		SourceURL: "https://api.themoviedb.org/3/tv/42", ETag: "etag-original", LastModified: "original validator",
		Title: "Série", OriginalTitle: "Original", Overview: "Overview", Tagline: "Tag", ReleaseDate: "2024-02-29", Year: 2024})
	if err != nil || !reflect.DeepEqual(doc.Header(), h) || doc.Route() != o.Route ||
		doc.Fields() != TVName|TVOverview|TVDate|TVOriginalName|TVTagline {
		t.Fatalf("header/route/projection mismatch: %+v, %v", doc, err)
	}
	clear(input)
	if !reflect.DeepEqual(doc.Header(), h) {
		t.Fatal("borrowed observation body")
	}
	// Unknown child changes remain part of the raw observation even though
	// they grant no child-family completeness or clearing authority.
	changed, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(strings.Replace(body, "not selected", "changed child", 1)))
	if err != nil || reflect.DeepEqual(changed.Header(), h) {
		t.Fatal("observation omitted unknown bytes", err)
	}
}

func TestIPTVSeasonAndEpisodeOwnIDsAndNullableRuntime(t *testing.T) {
	for _, tc := range []struct {
		kind    enrich.SubjectKind
		body    string
		fields  TVFields
		runtime writer.EvidenceInteger
	}{
		{enrich.Season, `{"id":42,"name":"Season 2","overview":"","air_date":null,"season_number":2}`, TVName | TVOverview | TVDate, writer.EvidenceInteger{}},
		{enrich.Episode, `{"id":42,"name":"Episode 3","overview":"","air_date":"2025-04-03","season_number":2,"episode_number":3,"runtime":0}`, TVName | TVOverview | TVDate | TVRuntime, writer.EvidenceInteger{Present: true}},
		{enrich.Episode, `{"id":42,"name":"Episode 3","overview":"","air_date":"","season_number":2,"episode_number":3,"runtime":null}`, TVName | TVOverview | TVDate | TVRuntime, writer.EvidenceInteger{}},
	} {
		o := tvObservation(tc.kind)
		doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(tc.body))
		if err != nil || doc.Route() != o.Route || doc.Fields() != tc.fields {
			t.Fatalf("%s: %+v, %v", tc.kind, doc, err)
		}
		name, date := "Episode 3", ""
		if tc.kind == enrich.Season {
			name = "Season 2"
		}
		if tc.runtime.Present {
			date = "2025-04-03"
		}
		h := writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb", SubjectKind: string(tc.kind), ProviderID: "42",
			ParserVersion: "tmdb." + string(tc.kind) + ".header.v1", RequestShape: "tmdb." + string(tc.kind) + ".detail.header.v1",
			Language: o.Language, Region: o.Region, ObservedAt: o.ObservedAt, SourceURL: o.SourceURL, ETag: o.ETag, LastModified: o.LastModified,
			ObservationDigest: sha256.Sum256([]byte(tc.body)), Title: name, Overview: "", ReleaseDate: date, LengthMillis: tc.runtime}
		if date != "" {
			h.Year = 2025
		}
		want, err := writer.NewEvidenceHeader(h)
		if err != nil || !reflect.DeepEqual(doc.Header(), want) {
			t.Fatal("nullable projection", err)
		}
	}
}

func TestIPTVRejectsMalformedWholeObservations(t *testing.T) {
	const good = `{"id":42,"name":"Episode","overview":"","air_date":"2024-02-29","season_number":2,"episode_number":3,"runtime":10}`
	for _, body := range []string{
		strings.Replace(good, `"id":42`, `"id":100`, 1),
		strings.Replace(good, `"id":42`, `"id":42.0`, 1),
		strings.Replace(good, `"id":42`, `"id":null`, 1),
		strings.Replace(good, `"id":42`, `"id":42,"id":42`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":null`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":"\ud800"`, 1),
		strings.Replace(good, `"name":"Episode"`, "\"name\":\"\xff\"", 1),
		strings.Replace(good, `"overview":"",`, ``, 1),
		strings.Replace(good, `"season_number":2`, `"season_number":1`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":4`, 1),
		strings.Replace(good, `"season_number":2`, `"season_number":null`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":null`, 1),
		strings.Replace(good, `2024-02-29`, `2023-02-29`, 1),
		strings.Replace(good, `"runtime":10`, `"runtime":-1`, 1),
		strings.Replace(good, `"runtime":10`, `"runtime":9223372036854775807`, 1),
		good + `{}`,
	} {
		got, err := PrepareTMDBTVHeader(tvPolicy(t), tvObservation(enrich.Episode), []byte(body))
		if err == nil || !reflect.DeepEqual(got, TVHeader{}) {
			t.Fatalf("accepted malformed page: %+v, %v", got, err)
		}
	}
}

func TestIPTVRejectsWrongNamespaceURLAndMissingPolicy(t *testing.T) {
	const body = `{"id":42,"name":"Series","overview":"","first_air_date":""}`
	for _, mutate := range []func(*TVObservation){
		func(o *TVObservation) { o.Route.Kind = enrich.Movie },
		func(o *TVObservation) { o.Route.SeriesID = "100" },
		func(o *TVObservation) { o.Route.ProviderID = "042" },
		func(o *TVObservation) { o.SourceURL = "https://api.themoviedb.org/3/movie/42?secret" },
		func(o *TVObservation) { o.SourceURL = "https://api.themoviedb.org.evil/3/tv/42?secret" },
		func(o *TVObservation) { o.SourceURL = "http://api.themoviedb.org/3/tv/42?secret" },
		func(o *TVObservation) { o.SourceURL = "https://api.themoviedb.org:444/3/tv/42?secret" },
		func(o *TVObservation) { o.SubjectID = 0 },
		func(o *TVObservation) { o.ObservedAt = "invented" },
		func(o *TVObservation) { o.ETag = strings.Repeat("x", (4<<20)+1) },
	} {
		o := tvObservation(enrich.Series)
		mutate(&o)
		got, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body))
		if err == nil || !reflect.DeepEqual(got, TVHeader{}) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid namespace: %+v, %v", got, err)
		}
	}
	if _, err := PrepareTMDBTVHeader(enrich.TMDBPolicy{}, tvObservation(enrich.Series), []byte(body)); !errors.Is(err, enrich.ErrTMDBPolicy) {
		t.Fatal(err)
	}
	if _, err := PrepareTMDBTVHeader(tvPolicy(t), tvObservation(enrich.Series), make([]byte, (4<<20)+1)); err == nil {
		t.Fatal("accepted oversized body")
	}
}
