// Package providers prepares immutable nonmovie provider observations. It does
// not acquire observations, authorize publication or open database handles.
package providers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

var ErrTVHeader = errors.New("invalid TV header observation")

// TVRoute captures the actual upstream coordinates. A season or episode's
// ProviderID is its own TMDB ID, distinct from the series ID used in the URL.
// Only original aired coordinates are accepted here. Alternative numbering is
// a separate provider mapping, never a reinterpretation of this descriptor.
type TVRoute struct {
	Kind                 enrich.SubjectKind
	ProviderID, SeriesID string
	Season, Episode      int64
}

type TVObservation struct {
	Route                                     TVRoute
	EvidenceID, Language, Region              string
	SubjectID                                 int64
	ObservedAt, SourceURL, ETag, LastModified string
}

// TVHeader selects only name, overview, date, and (when explicitly present)
// original_name, tagline and runtime. It grants NO completeness or clearing
// authority for credits, episodes, artwork, ratings, terms or other children.
// Nonmovie source/evidence seal and actual writer entry remain separate work.
type TVHeader struct {
	header writer.EvidenceHeader
	route  TVRoute
	// fields is the source-presence mask; nulls is its JSON-null subset. A
	// declared field absent from fields was omitted by the source. A field in
	// fields but not in nulls was supplied, possibly as empty text; a field in
	// both was explicitly null. The projected header text is "" for both null
	// and empty, so only the pair of masks keeps them apart.
	fields, nulls TVFields
}

type TVFields uint8

const (
	TVName TVFields = 1 << iota
	TVOverview
	TVDate
	TVOriginalName
	TVTagline
	TVRuntime
)

func (d TVHeader) Header() writer.EvidenceHeader { return d.header }
func (d TVHeader) Route() TVRoute                { return d.route }

// Fields reports which declared fields the source supplied. It is the
// presence mask, so an omitted field has no bit regardless of projected text.
func (d TVHeader) Fields() TVFields { return d.fields }

// Nulls reports the supplied fields whose JSON value was null. It is always a
// subset of Fields and never includes child families: a scalar header carries
// no completeness or clearing evidence for credits, episodes or artwork.
func (d TVHeader) Nulls() TVFields { return d.nulls }

// PrepareTMDBTVHeader requires an explicit existing TMDB preparation policy,
// freezes the body before validation/hash/decode and preserves the original
// acquisition time/validators. It never calls a provider. The policy is a
// preparation envelope, not a physical grant or an approved TV writer recipe.
func PrepareTMDBTVHeader(policy enrich.TMDBPolicy, observation TVObservation, body []byte) (TVHeader, error) {
	limits, err := policy.Limits()
	if err != nil {
		return TVHeader{}, err
	}
	if int64(len(body)) > limits.ResponseBytes {
		return TVHeader{}, &enrich.ResponseSizeError{LimitBytes: limits.ResponseBytes, ObservedBytes: int64(len(body))}
	}
	// Bound original metadata before URL parsing or descriptor allocation. The
	// acquisition envelope is separate from retained native control accounting;
	// sanitized SourceURL and all final header occurrences are checked again.
	remaining := limits.ResponseBytes
	for _, text := range []string{string(observation.Route.Kind), observation.Route.ProviderID,
		observation.Route.SeriesID, observation.EvidenceID, observation.Language, observation.Region,
		observation.ObservedAt, observation.SourceURL, observation.ETag, observation.LastModified} {
		if int64(len(text)) > remaining || !utf8.ValidString(text) || strings.IndexByte(text, 0) >= 0 {
			return TVHeader{}, ErrTVHeader
		}
		remaining -= int64(len(text))
	}
	path, ok := tvPath(observation.Route)
	if !ok {
		return TVHeader{}, ErrTVHeader
	}
	h := writer.EvidenceHeaderInput{ID: observation.EvidenceID, SubjectID: observation.SubjectID,
		Provider: "tmdb", SubjectKind: string(observation.Route.Kind), ProviderID: observation.Route.ProviderID,
		ParserVersion: "tmdb." + string(observation.Route.Kind) + ".header.v1",
		RequestShape:  "tmdb." + string(observation.Route.Kind) + ".detail.header.v1",
		Language:      observation.Language, Region: observation.Region, ObservedAt: observation.ObservedAt,
		ETag: observation.ETag, LastModified: observation.LastModified}
	// Check control occurrences before allocating/copying body or retained keys.
	if _, err := writer.CheckEvidenceStageControls(h, 0, 0); err != nil {
		return TVHeader{}, errors.Join(ErrTVHeader, err)
	}
	u, err := url.Parse(observation.SourceURL)
	if err != nil || u.Opaque != "" || u.Scheme != "https" ||
		!strings.EqualFold(u.Hostname(), "api.themoviedb.org") ||
		(u.Port() != "" && u.Port() != "443") || u.Path != path {
		return TVHeader{}, ErrTVHeader
	}
	// Reconstruct only the closed origin/path; credentials, query and fragment
	// never enter evidence or errors. This grants no network permission.
	h.SourceURL = "https://api.themoviedb.org" + path
	frozen := bytes.Clone(body)
	if !enrich.ValidTMDBObservationJSON(frozen) {
		return TVHeader{}, ErrTVHeader
	}
	values, fields, nulls, err := decodeTVHeader(frozen, observation.Route)
	if err != nil {
		return TVHeader{}, err
	}
	h.ObservationDigest = sha256.Sum256(frozen)
	h.Title, h.Overview, h.OriginalTitle, h.Tagline = tvText(values.name), tvText(values.overview), tvText(values.original), tvText(values.tagline)
	date := tvText(values.date)
	h.ReleaseDate = date
	if date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return TVHeader{}, ErrTVHeader
		}
		h.Year = int64(parsed.Year())
	}
	if values.runtime != nil {
		if *values.runtime < 0 || *values.runtime > math.MaxInt64/60000 {
			return TVHeader{}, ErrTVHeader
		}
		h.LengthMillis = writer.EvidenceInteger{Present: true, Value: *values.runtime * 60000}
	}
	header, err := writer.NewEvidenceHeader(h)
	if err != nil {
		return TVHeader{}, errors.Join(ErrTVHeader, err)
	}
	route := observation.Route
	route.Kind = enrich.SubjectKind(strings.Clone(string(route.Kind)))
	route.ProviderID, route.SeriesID = strings.Clone(route.ProviderID), strings.Clone(route.SeriesID)
	return TVHeader{header: header, route: route, fields: fields, nulls: nulls}, nil
}

// tvText projects a nullable source text. A null and an explicit empty string
// both render empty here; Nulls() retains the difference for future publishers.
func tvText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func canonicalPositiveID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}

func tvPath(route TVRoute) (string, bool) {
	if !canonicalPositiveID(route.ProviderID) {
		return "", false
	}
	switch route.Kind {
	case enrich.Series:
		if route.SeriesID != "" || route.Season != 0 || route.Episode != 0 {
			return "", false
		}
		return "/3/tv/" + route.ProviderID, true
	case enrich.Season, enrich.Episode:
		if !canonicalPositiveID(route.SeriesID) || route.Season < 0 || route.Episode < 0 ||
			(route.Kind == enrich.Season && route.Episode != 0) {
			return "", false
		}
		path := "/3/tv/" + route.SeriesID + "/season/" + strconv.FormatInt(route.Season, 10)
		if route.Kind == enrich.Episode {
			path += "/episode/" + strconv.FormatInt(route.Episode, 10)
		}
		return path, true
	default:
		return "", false
	}
}

// tvScalars keeps selected source values typed so JSON null stays distinct
// from an empty string: a null decodes to a nil pointer, "" to a non-nil one.
type tvScalars struct {
	id, season, episode                     *int64
	name, overview, original, tagline, date *string
	runtime                                 *int64
}

func decodeTVHeader(body []byte, route TVRoute) (tvScalars, TVFields, TVFields, error) {
	var out tvScalars
	var fields, nulls TVFields
	d := json.NewDecoder(bytes.NewReader(body))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return tvScalars{}, 0, 0, ErrTVHeader
	}
	dateKey := "air_date"
	if route.Kind == enrich.Series {
		dateKey = "first_air_date"
	}
	// Only fixed selected keys consume the duplicate set. Unknown fields are
	// syntax checked/discarded within the already bounded body, not retained in
	// a generic object graph or interpreted as complete child families.
	seen := make(map[string]bool, 10)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return tvScalars{}, 0, 0, ErrTVHeader
		}
		key, ok := token.(string)
		if !ok {
			return tvScalars{}, 0, 0, ErrTVHeader
		}
		var target any
		var bit TVFields
		switch key {
		case "id":
			target = &out.id
		case "name":
			target, bit = &out.name, TVName
		case "overview":
			target, bit = &out.overview, TVOverview
		case dateKey:
			target, bit = &out.date, TVDate
		case "original_name":
			if route.Kind == enrich.Series {
				target, bit = &out.original, TVOriginalName
			}
		case "tagline":
			target, bit = &out.tagline, TVTagline
		case "runtime":
			if route.Kind == enrich.Episode {
				target, bit = &out.runtime, TVRuntime
			}
		case "season_number":
			if route.Kind != enrich.Series {
				target = &out.season
			}
		case "episode_number":
			if route.Kind == enrich.Episode {
				target = &out.episode
			}
		}
		if target == nil {
			var discard json.RawMessage
			if d.Decode(&discard) != nil {
				return tvScalars{}, 0, 0, ErrTVHeader
			}
			continue
		}
		if seen[key] || d.Decode(target) != nil {
			return tvScalars{}, 0, 0, ErrTVHeader
		}
		if bit != 0 && tvNull(target) {
			nulls |= bit
		}
		seen[key], fields = true, fields|bit
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return tvScalars{}, 0, 0, ErrTVHeader
	}
	var trailing json.RawMessage
	if d.Decode(&trailing) != io.EOF || out.id == nil || strconv.FormatInt(*out.id, 10) != route.ProviderID ||
		out.name == nil || strings.TrimSpace(*out.name) == "" || fields&(TVName|TVOverview|TVDate) != TVName|TVOverview|TVDate ||
		(route.Kind != enrich.Series && (out.season == nil || *out.season != route.Season)) ||
		(route.Kind == enrich.Episode && (out.episode == nil || *out.episode != route.Episode)) {
		return tvScalars{}, 0, 0, ErrTVHeader
	}
	return out, fields, nulls, nil
}

// tvNull reports whether a decoded selected field was supplied as JSON null.
// Text fields decode into *string, and nullable integers into *int64, so a nil
// inner pointer is the null signal.
func tvNull(target any) bool {
	switch value := target.(type) {
	case **string:
		return *value == nil
	case **int64:
		return *value == nil
	default:
		return false
	}
}
