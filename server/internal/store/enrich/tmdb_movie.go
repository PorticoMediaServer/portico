package enrich

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ErrTMDBMovie = errors.New("invalid or over-limit TMDB movie projection")

// TMDBMovieObservation is detached acquisition/selection data, not proof.
// ObservedAt and validators must be the ORIGINAL acquisition's values even
// after cache reuse; they must never be invented from a later normalization.
type TMDBMovieObservation struct {
	EvidenceID, ProviderID, Language, Region  string
	SubjectID                                 int64
	ObservedAt, SourceURL, ETag, LastModified string
}

// TMDBMovieDocument represents ONLY the selected scalar header and W-selected
// credits. It says nothing about excluded child families or current origins.
// Preparation grants no clearing/completeness authority for excluded families.
// No raw response/candidate array/discarded credits survive preparation.
type TMDBMovieDocument struct {
	header              writer.EvidenceHeader
	credits             PreparedScreenCredits
	storedBytes, charge int64
}

func (d TMDBMovieDocument) Header() writer.EvidenceHeader  { return d.header }
func (d TMDBMovieDocument) Credits() PreparedScreenCredits { return d.credits }

// These forecasts cover current CONTENT fields; W independently derives native
// Q and reserves prospective proof-reference bytes inside the same6MiB bound.
// They are not a complete stage admission or physical memory/work forecast.
func (d TMDBMovieDocument) StoredBytes() int64   { return d.storedBytes }
func (d TMDBMovieDocument) ContentCharge() int64 { return d.charge }

// PrepareTMDBMovie copies the complete body before parsing AND hashing it.
// It counts candidates in a first pass before allocating the typed array.
// Only W's approved selector trims credits; malformed/over-limit input refuses
// the WHOLE document before any build/pin/write. There is no SQL or seal here.
func PrepareTMDBMovie(policy TMDBPolicy, observation TMDBMovieObservation, body []byte) (TMDBMovieDocument, error) {
	limits, err := policy.Limits()
	if err != nil {
		return TMDBMovieDocument{}, err
	}
	if int64(len(body)) > limits.ResponseBytes {
		return TMDBMovieDocument{}, &ResponseSizeError{LimitBytes: limits.ResponseBytes, ObservedBytes: int64(len(body))}
	}
	// Check borrowed native control keys before copying the response or any
	// observation strings. Complete retained occurrences are checked after the
	// bounded count pass, before allocating decoded candidates/selected copies.
	h := writer.EvidenceHeaderInput{ID: observation.EvidenceID, SubjectID: observation.SubjectID, ParserVersion: TMDBMovieParserVersion, Provider: "tmdb", SubjectKind: "movie", ProviderID: observation.ProviderID, Language: observation.Language, Region: observation.Region, RequestShape: TMDBMovieRequestShape, ObservedAt: observation.ObservedAt, ETag: observation.ETag, LastModified: observation.LastModified}
	if _, err := writer.CheckEvidenceStageControls(h, 0, 0); err != nil {
		return TMDBMovieDocument{}, errors.Join(ErrTMDBMovie, err)
	}
	frozen := make([]byte, len(body))
	copy(frozen, body)
	if !validTMDBJSON(frozen) {
		return TMDBMovieDocument{}, ErrTMDBMovie
	}
	safeURL, err := SafeTMDBSourceURL(observation.SourceURL, observation.ProviderID)
	if err != nil {
		return TMDBMovieDocument{}, errors.Join(ErrTMDBMovie, err)
	}
	wire, count, err := readTMDBMovie(frozen, nil, tmdbMovieCounts{})
	if err != nil {
		return TMDBMovieDocument{}, err
	}
	if wire.id <= 0 || strconv.FormatInt(wire.id, 10) != observation.ProviderID || strings.TrimSpace(wire.title) == "" || (wire.runtime != nil && (*wire.runtime < 0 || *wire.runtime > math.MaxInt64/60000)) {
		return TMDBMovieDocument{}, ErrTMDBMovie
	}
	// W owns both caps and selection. Only the number of retained key
	// occurrences is forecast here; no credit or ranking is selected by EN.
	if _, err := writer.CheckEvidenceStageControls(h, min(count.cast, writer.MaxCastCredits), min(count.crew, writer.MaxCrewCredits)); err != nil {
		return TMDBMovieDocument{}, errors.Join(ErrTMDBMovie, err)
	}
	rows := make([]ScreenCredit, count.cast+count.crew)
	if _, actual, err := readTMDBMovie(frozen, rows, count); err != nil || actual != count {
		return TMDBMovieDocument{}, ErrTMDBMovie
	}
	selected, err := PrepareScreenCredits(ProviderRef{"tmdb", Movie, observation.ProviderID}, rows)
	if err != nil {
		return TMDBMovieDocument{}, errors.Join(ErrTMDBMovie, err)
	}
	h.ObservationDigest = sha256.Sum256(frozen)
	h.SourceURL = safeURL
	h.Title, h.OriginalTitle, h.Overview, h.Tagline, h.ReleaseDate = wire.title, wire.original, wire.overview, wire.tagline, wire.release
	if wire.release != "" {
		date, err := time.Parse("2006-01-02", wire.release)
		if err != nil {
			return TMDBMovieDocument{}, ErrTMDBMovie
		}
		h.Year = int64(date.Year())
	}
	if wire.runtime != nil {
		h.LengthMillis = writer.EvidenceInteger{Value: *wire.runtime * 60000, Present: true}
	}
	header, err := writer.NewEvidenceHeader(h)
	if err != nil {
		return TMDBMovieDocument{}, errors.Join(ErrTMDBMovie, err)
	}
	// Exact CONTENT storage: header TEXT fields plus64hexTEXT digest. Each
	// credit repeats its native provider/kind/evidence key. FIRST music-only
	// columns and scope are explicitly empty; ordinal is INTEGER. Source/image
	// rows, receipts, indexes and physical work are SEPARATE budgets, not zero.
	stored := int64(header.TextBytes()) + 64
	for _, row := range selected.rows {
		stored += int64(len("tmdb") + len("movie") + len(row.Class) + len(header.ID()) + len(row.Person.ExternalID) + len(row.Name) + len(row.Role) + len(row.Department) + len(row.CreditID))
	}
	contentRows := 1 + selected.Count()
	if contentRows > limits.ContentRows || stored > limits.StoredContentBytes {
		return TMDBMovieDocument{}, ErrTMDBMovie
	}
	return TMDBMovieDocument{header, selected, stored, int64(contentRows)*512 + 8*stored}, nil
}

type tmdbMovieScalars struct {
	id                                          int64
	title, original, overview, tagline, release string
	runtime                                     *int64
}
type tmdbMovieCounts struct{ cast, crew int }
type tmdbMovieCredit struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Character  string `json:"character"`
	Job        string `json:"job"`
	Department string `json:"department"`
	CreditID   string `json:"credit_id"`
	Order      *int64 `json:"order"`
}

// Decode only typed scalars and one bounded credits family. Unknown values
// are syntax-checked/discarded, not retained in an unbounded generic JSON tree.
func readTMDBMovie(body []byte, rows []ScreenCredit, layout tmdbMovieCounts) (out tmdbMovieScalars, count tmdbMovieCounts, err error) {
	d := json.NewDecoder(bytes.NewReader(body))
	if !tmdbDelim(d, '{') {
		return out, tmdbMovieCounts{}, ErrTMDBMovie
	}
	seen := uint16(0)
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return out, tmdbMovieCounts{}, ErrTMDBMovie
		}
		key, ok := token.(string)
		if !ok {
			return out, tmdbMovieCounts{}, ErrTMDBMovie
		}
		bit := map[string]uint16{"id": 1, "title": 2, "original_title": 4, "overview": 8, "tagline": 16, "release_date": 32, "runtime": 64, "credits": 128}[key]
		if bit != 0 && seen&bit != 0 {
			return out, tmdbMovieCounts{}, ErrTMDBMovie
		}
		seen |= bit
		switch key {
		case "id":
			err = d.Decode(&out.id)
		case "title":
			err = d.Decode(&out.title)
		case "original_title":
			err = d.Decode(&out.original)
		case "overview":
			err = d.Decode(&out.overview)
		case "tagline":
			err = d.Decode(&out.tagline)
		case "release_date":
			err = d.Decode(&out.release)
		case "runtime":
			err = d.Decode(&out.runtime)
		case "credits":
			count, err = readTMDBCredits(d, rows, layout)
		default:
			var discard json.RawMessage
			err = d.Decode(&discard)
		}
		if err != nil {
			return out, tmdbMovieCounts{}, ErrTMDBMovie
		}
	}
	if !tmdbDelim(d, '}') || seen&131 != 131 {
		return out, tmdbMovieCounts{}, ErrTMDBMovie
	} // explicit id,title,credits
	var trailing json.RawMessage
	if d.Decode(&trailing) != io.EOF {
		return out, tmdbMovieCounts{}, ErrTMDBMovie
	}
	return out, count, nil
}

func readTMDBCredits(d *json.Decoder, rows []ScreenCredit, layout tmdbMovieCounts) (counts tmdbMovieCounts, err error) {
	if !tmdbDelim(d, '{') {
		return counts, ErrTMDBMovie
	}
	seen := uint8(0)
	total := 0
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return counts, ErrTMDBMovie
		}
		key, ok := token.(string)
		if !ok {
			return counts, ErrTMDBMovie
		}
		if key != "cast" && key != "crew" {
			var discard json.RawMessage
			if d.Decode(&discard) != nil {
				return counts, ErrTMDBMovie
			}
			continue
		}
		bit := uint8(1)
		class := Cast
		start := 0
		expected := layout.cast
		if key == "crew" {
			bit = 2
			class = Crew
			start = layout.cast
			expected = layout.crew
		}
		if seen&bit != 0 {
			return counts, ErrTMDBMovie
		}
		seen |= bit
		if !tmdbDelim(d, '[') {
			return counts, ErrTMDBMovie
		}
		groupCount := 0
		for d.More() {
			if total >= writer.MaxScreenCreditCandidates {
				return counts, ErrTMDBMovie
			}
			if rows == nil {
				var discard json.RawMessage
				if d.Decode(&discard) != nil {
					return counts, ErrTMDBMovie
				}
			} else {
				if groupCount >= expected || start+groupCount >= len(rows) {
					return counts, ErrTMDBMovie
				}
				c, e := readTMDBMovieCredit(d)
				if e != nil || c.ID <= 0 || (class == Cast && (c.Order == nil || *c.Order < 0)) {
					return counts, ErrTMDBMovie
				}
				row := ScreenCredit{Class: class, Person: ProviderRef{"tmdb", Person, strconv.FormatInt(c.ID, 10)}, CreditID: c.CreditID, Name: c.Name, Role: c.Job, Department: c.Department, SourceOrdinal: int64(start + groupCount)}
				if class == Cast {
					row.Role = c.Character
					row.Billing = *c.Order
				}
				rows[start+groupCount] = row
			}
			total++
			groupCount++
		}
		if !tmdbDelim(d, ']') {
			return counts, ErrTMDBMovie
		}
		if class == Cast {
			counts.cast = groupCount
		} else {
			counts.crew = groupCount
		}
	}
	if !tmdbDelim(d, '}') || seen != 3 || (rows != nil && counts != layout) {
		return counts, ErrTMDBMovie
	}
	return counts, nil
}

func readTMDBMovieCredit(d *json.Decoder) (out tmdbMovieCredit, err error) {
	if !tmdbDelim(d, '{') {
		return out, ErrTMDBMovie
	}
	seen := uint16(0)
	for d.More() {
		token, e := d.Token()
		if e != nil {
			return out, ErrTMDBMovie
		}
		key, ok := token.(string)
		if !ok {
			return out, ErrTMDBMovie
		}
		bit := map[string]uint16{"id": 1, "name": 2, "character": 4, "job": 8, "department": 16, "credit_id": 32, "order": 64}[key]
		if bit != 0 && seen&bit != 0 {
			return out, ErrTMDBMovie
		}
		seen |= bit
		switch key {
		case "id":
			err = d.Decode(&out.ID)
		case "name":
			err = d.Decode(&out.Name)
		case "character":
			err = d.Decode(&out.Character)
		case "job":
			err = d.Decode(&out.Job)
		case "department":
			err = d.Decode(&out.Department)
		case "credit_id":
			err = d.Decode(&out.CreditID)
		case "order":
			err = d.Decode(&out.Order)
		default:
			var discard json.RawMessage
			err = d.Decode(&discard)
		}
		if err != nil {
			return out, ErrTMDBMovie
		}
	}
	if !tmdbDelim(d, '}') {
		return out, ErrTMDBMovie
	}
	return out, nil
}

func tmdbDelim(d *json.Decoder, want json.Delim) bool {
	token, err := d.Token()
	return err == nil && token == want
}
