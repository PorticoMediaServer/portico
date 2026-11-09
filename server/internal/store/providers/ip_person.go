package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// ErrPersonObservation is the closed person-observation refusal. Preparation
// never repairs, truncates or invents a person fact.
var ErrPersonObservation = errors.New("invalid person observation")

// The person source is TMDB person detail plus its exact birth/death/place
// scalars. It is a preparation descriptor, not a physical grant, an approved
// person recipe or a catalogue person. Aliases and external IDs are IP-15's
// separate complete child families and are not claimed here.
const (
	IPPersonParser = "tmdb.person.header-exact-dates.v1"
	IPPersonShape  = "tmdb.person.detail.header-exact-dates.v1"
)

// maxIPPersonText bounds one selected text so a hostile body cannot retain an
// unbounded biography or place. It is an acquisition safety bound, not a
// product metadata limit; the request envelope is bounded separately.
const maxIPPersonText = 64 << 10

// PersonObservation captures one recorded TMDB person acquisition. SubjectID is
// a catalog_people key, never a catalogue entity key, even when the two numeric
// IDs happen to collide.
type PersonObservation struct {
	EvidenceID                                string
	SubjectID                                 int64
	ProviderID                                string
	Language, Region                          string
	ObservedAt, SourceURL, ETag, LastModified string
}

// PersonFields is the declared person presence mask. It never covers aliases,
// external IDs, credits, terms, ratings, episodes or collections.
type PersonFields uint8

const (
	PersonName PersonFields = 1 << iota
	PersonBiography
	PersonBirthDate
	PersonDeathDate
	PersonBirthPlace
)

// PersonDocument is one prepared, immutable person observation. The common
// evidence header carries name, biography and birth/death years; exact dates and
// the place of birth need a typed carrier because the common header has no such
// columns (P2-IP-CONTRACT item 5).
type PersonDocument struct {
	header                           writer.EvidenceHeader
	ref                              enrich.ProviderRef
	fields, nulls                    PersonFields
	name                             string
	birthDate, deathDate, birthPlace writer.EvidenceText
}

// Header returns the common evidence header for the person namespace.
func (d PersonDocument) Header() writer.EvidenceHeader { return d.header }

// Ref is the exact (provider, person, external id) provider namespace.
func (d PersonDocument) Ref() enrich.ProviderRef { return d.ref }

// Fields is the source-presence mask: a declared field absent from the source
// has no bit regardless of projected text.
func (d PersonDocument) Fields() PersonFields { return d.fields }

// Nulls is the supplied subset whose JSON value was null. It is always a subset
// of Fields and never includes an undeclared provider field.
func (d PersonDocument) Nulls() PersonFields { return d.nulls }

// Name is the exact source name, which is required and nonblank.
func (d PersonDocument) Name() string { return d.name }

// BirthDate, DeathDate and BirthPlace are exact typed scalars. Present is true
// only when the source supplied a non-null value, so an explicit empty string
// stays distinct from an explicit null and from an omitted key.
func (d PersonDocument) BirthDate() writer.EvidenceText  { return d.birthDate }
func (d PersonDocument) DeathDate() writer.EvidenceText  { return d.deathDate }
func (d PersonDocument) BirthPlace() writer.EvidenceText { return d.birthPlace }

// PrepareTMDBPerson requires an explicit existing TMDB preparation policy,
// freezes the body before validation/hash/decode and preserves the original
// acquisition time/validators. It never calls a provider and never creates a
// catalogue person: a person with no accepted local identity keeps none.
func PrepareTMDBPerson(policy enrich.TMDBPolicy, observation PersonObservation, body []byte) (PersonDocument, error) {
	limits, err := policy.Limits()
	if err != nil {
		return PersonDocument{}, err
	}
	if int64(len(body)) > limits.ResponseBytes {
		return PersonDocument{}, &enrich.ResponseSizeError{LimitBytes: limits.ResponseBytes, ObservedBytes: int64(len(body))}
	}
	// Bound original metadata before URL parsing or descriptor allocation. The
	// acquisition envelope is separate from retained native control accounting;
	// sanitized SourceURL and all final header occurrences are checked again.
	remaining := limits.ResponseBytes
	for _, text := range []string{string(enrich.Person), observation.ProviderID, observation.EvidenceID,
		observation.Language, observation.Region, observation.ObservedAt, observation.SourceURL,
		observation.ETag, observation.LastModified} {
		if int64(len(text)) > remaining || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return PersonDocument{}, ErrPersonObservation
		}
		remaining -= int64(len(text))
	}
	if !canonicalPositiveID(observation.ProviderID) {
		return PersonDocument{}, ErrPersonObservation
	}
	ref := enrich.ProviderRef{Provider: "tmdb", Kind: enrich.Person, ExternalID: observation.ProviderID}
	path := "/3/person/" + observation.ProviderID
	h := writer.EvidenceHeaderInput{ID: observation.EvidenceID, SubjectID: observation.SubjectID,
		Provider: "tmdb", SubjectKind: string(enrich.Person), ProviderID: observation.ProviderID,
		ParserVersion: IPPersonParser, RequestShape: IPPersonShape,
		Language: observation.Language, Region: observation.Region, ObservedAt: observation.ObservedAt,
		ETag: observation.ETag, LastModified: observation.LastModified}
	// Check control occurrences before allocating/copying body or retained keys.
	if _, err := writer.CheckEvidenceStageControls(h, 0, 0); err != nil {
		return PersonDocument{}, errors.Join(ErrPersonObservation, err)
	}
	u, err := url.Parse(observation.SourceURL)
	if err != nil || u.Opaque != "" || u.Scheme != "https" ||
		!strings.EqualFold(u.Hostname(), "api.themoviedb.org") ||
		(u.Port() != "" && u.Port() != "443") || u.Path != path {
		return PersonDocument{}, ErrPersonObservation
	}
	// Reconstruct only the closed origin/path; credentials, query and fragment
	// never enter evidence or errors. This grants no network permission.
	h.SourceURL = "https://api.themoviedb.org" + path
	frozen := bytes.Clone(body)
	if !enrich.ValidTMDBObservationJSON(frozen) {
		return PersonDocument{}, ErrPersonObservation
	}
	values, fields, nulls, err := decodePerson(frozen, observation.ProviderID)
	if err != nil {
		return PersonDocument{}, err
	}
	h.ObservationDigest = sha256.Sum256(frozen)
	doc := PersonDocument{ref: ref, fields: fields, nulls: nulls, name: strings.Clone(*values.name)}
	doc.birthDate = personText(values.birthday)
	doc.deathDate = personText(values.deathday)
	doc.birthPlace = personText(values.place)
	h.Title = doc.name
	if biography := personText(values.biography); biography.Present {
		h.Biography, h.BiographySource = strings.Clone(biography.Value), "tmdb"
		h.BiographySourceURL = h.SourceURL
	}
	if year, ok := personYear(doc.birthDate); ok {
		h.BeginYear = writer.EvidenceInteger{Present: true, Value: year}
	}
	if year, ok := personYear(doc.deathDate); ok {
		h.EndYear = writer.EvidenceInteger{Present: true, Value: year}
	}
	header, err := writer.NewEvidenceHeader(h)
	if err != nil {
		return PersonDocument{}, errors.Join(ErrPersonObservation, err)
	}
	doc.header = header
	return doc, nil
}

// personText projects a nullable source text with its presence preserved. A
// null or omitted key is absent; an explicit empty string is present and empty.
func personText(value *string) writer.EvidenceText {
	if value == nil {
		return writer.EvidenceText{}
	}
	return writer.EvidenceText{Value: strings.Clone(*value), Present: true}
}

// personYear reports the exact calendar year of a supplied date. A supplied
// empty string means the source declared no date, so it has no year.
func personYear(date writer.EvidenceText) (int64, bool) {
	if !date.Present || date.Value == "" {
		return 0, false
	}
	parsed, err := time.Parse("2006-01-02", date.Value)
	if err != nil || parsed.Year() < 1 {
		return 0, false
	}
	return int64(parsed.Year()), true
}

// personScalars keeps selected source values typed so JSON null stays distinct
// from an empty string: a null decodes to a nil pointer, "" to a non-nil one.
type personScalars struct {
	id                        *int64
	name, biography           *string
	birthday, deathday, place *string
}

func decodePerson(body []byte, providerID string) (personScalars, PersonFields, PersonFields, error) {
	var out personScalars
	var fields, nulls PersonFields
	d := json.NewDecoder(bytes.NewReader(body))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	// Only fixed selected keys consume the duplicate set. Unknown provider
	// fields are syntax checked/discarded within the already bounded body; they
	// remain part of the raw observation digest but grant no declared fact.
	seen := make(map[string]bool, 6)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return personScalars{}, 0, 0, ErrPersonObservation
		}
		key, ok := token.(string)
		if !ok {
			return personScalars{}, 0, 0, ErrPersonObservation
		}
		var target any
		var bit PersonFields
		switch key {
		case "id":
			target = &out.id
		case "name":
			target, bit = &out.name, PersonName
		case "biography":
			target, bit = &out.biography, PersonBiography
		case "birthday":
			target, bit = &out.birthday, PersonBirthDate
		case "deathday":
			target, bit = &out.deathday, PersonDeathDate
		case "place_of_birth":
			target, bit = &out.place, PersonBirthPlace
		}
		if target == nil {
			var discard json.RawMessage
			if d.Decode(&discard) != nil {
				return personScalars{}, 0, 0, ErrPersonObservation
			}
			continue
		}
		if seen[key] || d.Decode(target) != nil {
			return personScalars{}, 0, 0, ErrPersonObservation
		}
		if bit != 0 && personFieldNull(target) {
			nulls |= bit
		}
		seen[key], fields = true, fields|bit
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	var trailing json.RawMessage
	if d.Decode(&trailing) != io.EOF || out.id == nil || strconv.FormatInt(*out.id, 10) != providerID ||
		out.name == nil || strings.TrimSpace(*out.name) == "" || fields&PersonName != PersonName {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	for _, text := range []*string{out.name, out.biography, out.place} {
		if text != nil && (len(*text) > maxIPPersonText || !utf8.ValidString(*text) ||
			strings.IndexByte(*text, 0) >= 0) {
			return personScalars{}, 0, 0, ErrPersonObservation
		}
	}
	birth, birthOK, err := personExactDate(out.birthday)
	if err != nil {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	death, deathOK, err := personExactDate(out.deathday)
	if err != nil {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	if birthOK && deathOK && death.Before(birth) {
		return personScalars{}, 0, 0, ErrPersonObservation
	}
	return out, fields, nulls, nil
}

// personFieldNull reports whether a decoded selected field was supplied as JSON
// null. Text fields decode into *string, so a nil inner pointer is the signal.
func personFieldNull(target any) bool {
	switch value := target.(type) {
	case **string:
		return *value == nil
	default:
		return false
	}
}

// personExactDate validates a supplied exact date. Null/omitted dates are
// absent; an explicit empty string declares no date; anything else must be a
// real 2006-01-02 calendar date, never an approximate or partial year.
func personExactDate(value *string) (time.Time, bool, error) {
	if value == nil || *value == "" {
		return time.Time{}, false, nil
	}
	if len(*value) > maxIPPersonText || !utf8.ValidString(*value) || strings.IndexByte(*value, 0) >= 0 {
		return time.Time{}, false, ErrPersonObservation
	}
	parsed, err := time.Parse("2006-01-02", *value)
	if err != nil || parsed.Year() < 1 {
		return time.Time{}, false, ErrPersonObservation
	}
	return parsed, true, nil
}
