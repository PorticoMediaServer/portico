package providers

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// tvDeclaredFields is every scalar field the TV header declares. Nothing else
// may ever appear in the presence or null mask.
const tvDeclaredFields = TVName | TVOverview | TVDate | TVOriginalName | TVTagline | TVRuntime

// tvKeyOrder keeps generated bodies deterministic.
var tvKeyOrder = []string{"id", "name", "overview", "first_air_date", "air_date",
	"season_number", "episode_number", "tagline", "original_name", "runtime"}

func tvQuote(value string) string { return strconv.Quote(value) }

// tvBaseValues holds only the fields preparation requires. Optional declared
// keys are absent unless a case supplies them.
func tvBaseValues(kind enrich.SubjectKind) map[string]string {
	name, dateKey := "Series", "first_air_date"
	switch kind {
	case enrich.Season:
		name, dateKey = "Season", "air_date"
	case enrich.Episode:
		name, dateKey = "Episode", "air_date"
	}
	values := map[string]string{"id": "42", "name": tvQuote(name), "overview": tvQuote("Overview"), dateKey: tvQuote("2024-02-29")}
	if kind != enrich.Series {
		values["season_number"] = "2"
	}
	if kind == enrich.Episode {
		values["episode_number"] = "3"
	}
	return values
}

// tvBody renders a header object. An empty override value removes the key.
func tvBody(kind enrich.SubjectKind, override map[string]string) string {
	values := tvBaseValues(kind)
	for key, raw := range override {
		if raw == "" {
			delete(values, key)
			continue
		}
		values[key] = raw
	}
	parts := make([]string, 0, len(values))
	for _, key := range tvKeyOrder {
		if raw, ok := values[key]; ok {
			parts = append(parts, tvQuote(key)+":"+raw)
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func tvDateKey(kind enrich.SubjectKind) string {
	if kind == enrich.Series {
		return "first_air_date"
	}
	return "air_date"
}

// tvTexts is the projected header text for one prepared body.
type tvTexts struct{ title, overview, original, tagline, date string }

func tvBaseTexts(kind enrich.SubjectKind) tvTexts {
	title := "Series"
	switch kind {
	case enrich.Season:
		title = "Season"
	case enrich.Episode:
		title = "Episode"
	}
	return tvTexts{title: title, overview: "Overview", date: "2024-02-29"}
}

func (texts *tvTexts) set(bit TVFields, value string) {
	switch bit {
	case TVName:
		texts.title = value
	case TVOverview:
		texts.overview = value
	case TVDate:
		texts.date = value
	case TVOriginalName:
		texts.original = value
	case TVTagline:
		texts.tagline = value
	}
}

func tvExpectedHeader(t *testing.T, o TVObservation, body []byte, texts tvTexts, runtime writer.EvidenceInteger) writer.EvidenceHeader {
	t.Helper()
	input := writer.EvidenceHeaderInput{ID: o.EvidenceID, SubjectID: o.SubjectID, Provider: "tmdb",
		SubjectKind: string(o.Route.Kind), ProviderID: o.Route.ProviderID,
		ParserVersion: "tmdb." + string(o.Route.Kind) + ".header.v1",
		RequestShape:  "tmdb." + string(o.Route.Kind) + ".detail.header.v1",
		Language:      o.Language, Region: o.Region, ObservedAt: o.ObservedAt, SourceURL: o.SourceURL,
		ETag: o.ETag, LastModified: o.LastModified, ObservationDigest: sha256.Sum256(body),
		Title: texts.title, Overview: texts.overview, OriginalTitle: texts.original,
		Tagline: texts.tagline, ReleaseDate: texts.date, LengthMillis: runtime}
	if texts.date != "" {
		date, err := time.Parse("2006-01-02", texts.date)
		if err != nil {
			t.Fatal(err)
		}
		input.Year = int64(date.Year())
	}
	header, err := writer.NewEvidenceHeader(input)
	if err != nil {
		t.Fatal(err)
	}
	return header
}

type tvPresenceCase struct {
	name       string
	kind       enrich.SubjectKind
	key        string // declared key under test; empty means omitted
	raw        string // raw JSON value for key; empty means omitted
	bit        TVFields
	wantFields TVFields
	wantNulls  TVFields
	wantText   string
}

func TestIPTVSourcePresenceDistinguishesOmittedNullEmptyAndValue(t *testing.T) {
	for _, kind := range []enrich.SubjectKind{enrich.Series, enrich.Season, enrich.Episode} {
		base := TVName | TVOverview | TVDate
		dateKey := tvDateKey(kind)
		cases := []tvPresenceCase{
			{"overview-null", kind, "overview", "null", TVOverview, base, TVOverview, ""},
			{"overview-empty", kind, "overview", `""`, TVOverview, base, 0, ""},
			{"overview-value", kind, "overview", `"Text"`, TVOverview, base, 0, "Text"},
			{"date-null", kind, dateKey, "null", TVDate, base, TVDate, ""},
			{"date-empty", kind, dateKey, `""`, TVDate, base, 0, ""},
			{"tagline-omitted", kind, "", "", TVTagline, base, 0, ""},
			{"tagline-null", kind, "tagline", "null", TVTagline, base | TVTagline, TVTagline, ""},
			{"tagline-empty", kind, "tagline", `""`, TVTagline, base | TVTagline, 0, ""},
			{"tagline-value", kind, "tagline", `"Tag"`, TVTagline, base | TVTagline, 0, "Tag"},
		}
		if kind == enrich.Series {
			cases = append(cases,
				tvPresenceCase{"original-name-omitted", kind, "", "", TVOriginalName, base, 0, ""},
				tvPresenceCase{"original-name-null", kind, "original_name", "null", TVOriginalName, base | TVOriginalName, TVOriginalName, ""},
				tvPresenceCase{"original-name-empty", kind, "original_name", `""`, TVOriginalName, base | TVOriginalName, 0, ""},
				tvPresenceCase{"original-name-value", kind, "original_name", `"Original"`, TVOriginalName, base | TVOriginalName, 0, "Original"},
			)
		}
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				override := map[string]string{}
				if tc.key != "" {
					override[tc.key] = tc.raw
				}
				body := tvBody(tc.kind, override)
				o := tvObservation(tc.kind)
				doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body))
				if err != nil {
					t.Fatal(err)
				}
				if doc.Fields() != tc.wantFields || doc.Nulls() != tc.wantNulls {
					t.Fatalf("presence %b/%b, want %b/%b", doc.Fields(), doc.Nulls(), tc.wantFields, tc.wantNulls)
				}
				if doc.Nulls() & ^doc.Fields() != 0 {
					t.Fatal("null mask escaped the presence mask")
				}
				texts := tvBaseTexts(tc.kind)
				texts.set(tc.bit, tc.wantText)
				if want := tvExpectedHeader(t, o, []byte(body), texts, writer.EvidenceInteger{}); !reflect.DeepEqual(doc.Header(), want) {
					t.Fatalf("projection %+v", doc)
				}
			})
		}
	}
}

func TestIPTVNullAndEmptyShareProjectedTextButNotMask(t *testing.T) {
	o := tvObservation(enrich.Series)
	omitted := tvBody(enrich.Series, nil)
	nullBody := tvBody(enrich.Series, map[string]string{"tagline": "null"})
	emptyBody := tvBody(enrich.Series, map[string]string{"tagline": `""`})
	valueBody := tvBody(enrich.Series, map[string]string{"tagline": `"Tag"`})
	prepare := func(body string) TVHeader {
		t.Helper()
		doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	present := TVName | TVOverview | TVDate
	if got := prepare(omitted).Fields(); got != present {
		t.Fatalf("omitted tagline mask %b", got)
	}
	// The issue's original observation: omitted/null/empty used to report 7, 23
	// and 23 with no way to tell null from empty once the body was discarded.
	for _, tc := range []struct {
		body   string
		fields TVFields
		nulls  TVFields
		text   string
	}{
		{nullBody, present | TVTagline, TVTagline, ""},
		{emptyBody, present | TVTagline, 0, ""},
		{valueBody, present | TVTagline, 0, "Tag"},
	} {
		doc := prepare(tc.body)
		if doc.Fields() != tc.fields || doc.Nulls() != tc.nulls {
			t.Fatalf("mask %b/%b, want %b/%b", doc.Fields(), doc.Nulls(), tc.fields, tc.nulls)
		}
		texts := tvBaseTexts(enrich.Series)
		texts.tagline = tc.text
		if want := tvExpectedHeader(t, o, []byte(tc.body), texts, writer.EvidenceInteger{}); !reflect.DeepEqual(doc.Header(), want) {
			t.Fatalf("projection %+v", doc)
		}
	}
	if prepare(nullBody).Fields() != prepare(emptyBody).Fields() {
		t.Fatal("null and empty differ in presence, not only in nullness")
	}
	if prepare(nullBody).Nulls() != TVTagline || prepare(emptyBody).Nulls() != 0 {
		t.Fatal("null state not retained")
	}
}

func TestIPTVRuntimePresenceStates(t *testing.T) {
	o := tvObservation(enrich.Episode)
	base := TVName | TVOverview | TVDate
	for _, tc := range []struct {
		name     string
		override map[string]string
		want     writer.EvidenceInteger
	}{
		{"omitted", nil, writer.EvidenceInteger{}},
		{"null", map[string]string{"runtime": "null"}, writer.EvidenceInteger{}},
		{"zero", map[string]string{"runtime": "0"}, writer.EvidenceInteger{Present: true}},
		{"minutes", map[string]string{"runtime": "47"}, writer.EvidenceInteger{Present: true, Value: 47 * 60000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tvBody(enrich.Episode, tc.override)
			doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			wantFields := base
			wantNulls := TVFields(0)
			if tc.name == "omitted" {
				// TVRuntime stays clear.
			} else {
				wantFields |= TVRuntime
				if tc.name == "null" {
					wantNulls = TVRuntime
				}
			}
			if doc.Fields() != wantFields || doc.Nulls() != wantNulls {
				t.Fatalf("mask %b/%b", doc.Fields(), doc.Nulls())
			}
			if want := tvExpectedHeader(t, o, []byte(body), tvBaseTexts(enrich.Episode), tc.want); !reflect.DeepEqual(doc.Header(), want) {
				t.Fatalf("projection %+v", doc)
			}
		})
	}
}

func TestIPTVRejectsMalformedBoundedBodies(t *testing.T) {
	const good = `{"id":42,"name":"Episode","overview":"","air_date":"","season_number":2,"episode_number":3}`
	deep := `{"id":42,"name":"Episode","overview":"","air_date":"","season_number":2,"episode_number":3,"unknown":` +
		strings.Repeat("[", 10001) + strings.Repeat("]", 10001) + "}"
	for _, body := range []string{
		`[]`,
		`{}`,
		`{"id":42,"name":"Episode","overview":"","air_date":"","season_number":2,"episode_number":3`,
		good[:len(good)-1],
		good + `{}`,
		good + `1`,
		strings.Replace(good, `"name":"Episode"`, `"name":null`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":""`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":" "`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":7`, 1),
		strings.Replace(good, `"overview":"",`, ``, 1),
		strings.Replace(good, `"overview":""`, `"overview":[]`, 1),
		strings.Replace(good, `"overview":""`, `"overview":`, 1),
		strings.Replace(good, `"id":42`, `"id":null`, 1),
		strings.Replace(good, `"id":42`, `"id":42.0`, 1),
		strings.Replace(good, `"id":42`, `"id":"42"`, 1),
		strings.Replace(good, `"season_number":2`, `"season_number":null`, 1),
		strings.Replace(good, `"season_number":2`, `"season_number":`, 1),
		strings.Replace(good, `"season_number":2`, `"season_number":1`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":null`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":4`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":`, 1),
		strings.Replace(good, `"episode_number":3`, `"episode_number":3,"tagline":null,"tagline":""`, 1),
		strings.Replace(good, `"name":"Episode"`, `"name":"\ud800"`, 1),
		deep,
	} {
		got, err := PrepareTMDBTVHeader(tvPolicy(t), tvObservation(enrich.Episode), []byte(body))
		if err == nil || !errors.Is(err, ErrTVHeader) || !reflect.DeepEqual(got, TVHeader{}) {
			t.Fatalf("accepted malformed body %q: %+v, %v", body, got, err)
		}
	}
	// The whole observation stays bounded by the preparation response limit.
	if got, err := PrepareTMDBTVHeader(tvPolicy(t), tvObservation(enrich.Episode), make([]byte, (4<<20)+1)); err == nil || !reflect.DeepEqual(got, TVHeader{}) {
		t.Fatal("accepted oversized body")
	}
	// A complete scalar header still cannot complete or clear children: an
	// empty child array, a populated one, and an explicit child null all decode
	// to the same scalar mask and projection.
	o := tvObservation(enrich.Episode)
	texts := tvBaseTexts(enrich.Episode)
	baseBody := tvBody(enrich.Episode, nil)
	baseDoc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(baseBody))
	if err != nil {
		t.Fatal(err)
	}
	declared := baseDoc.Fields()
	for _, children := range []string{
		`"seasons":[],"credits":{},"aggregate_credits":{"cast":[]},"images":{"posters":[]},"videos":{"results":[]}`,
		`"seasons":[{"season_number":2,"episode_count":10}],"credits":{"cast":[{"id":1}]},"images":{"posters":[{"file_path":"x"}]}`,
		`"seasons":null,"credits":null,"aggregate_credits":null,"images":null,"videos":null`,
	} {
		body := `{"id":42,"name":"Episode","overview":"Overview","air_date":"2024-02-29","season_number":2,"episode_number":3,` + children + "}"
		doc, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if doc.Fields() != declared || doc.Nulls() != 0 || doc.Fields()&^tvDeclaredFields != 0 {
			t.Fatalf("child data changed the scalar mask: %b/%b", doc.Fields(), doc.Nulls())
		}
		if want := tvExpectedHeader(t, o, []byte(body), texts, writer.EvidenceInteger{}); !reflect.DeepEqual(doc.Header(), want) {
			t.Fatalf("child data changed the scalar projection: %+v", doc)
		}
	}
	// Child-only bodies never satisfy the required scalar fields.
	for _, body := range []string{
		`{"id":42,"seasons":[],"credits":{},"images":{"posters":[]}}`,
		`{"id":42,"name":"Episode","overview":"Overview","season_number":2,"episode_number":3,"seasons":[]}`,
	} {
		if got, err := PrepareTMDBTVHeader(tvPolicy(t), o, []byte(body)); err == nil || !reflect.DeepEqual(got, TVHeader{}) {
			t.Fatalf("child data completed a scalar header: %+v, %v", got, err)
		}
	}
	// Sanity: the mask bits are closed over the declared scalar fields, so no
	// future child family can appear without an explicit new declaration.
	var all TVFields
	for _, bit := range []TVFields{TVName, TVOverview, TVDate, TVOriginalName, TVTagline, TVRuntime} {
		all |= bit
	}
	if all != tvDeclaredFields {
		t.Fatal("declared scalar mask drifted")
	}
}
