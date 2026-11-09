package providers

import (
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/enrich"
)

// TVDB (TheTVDB v4) offline episode adapter. It normalizes RECORDED page
// bodies; it never calls a provider, opens a database handle, mints an evidence
// identity or authorizes a publication. Its output is the IP-08 TV child
// descriptor plus the exact provider page truth the IP set commands replay.
//
// This is a distinct provider adapter: it does not import, wrap or widen EN's
// movie parser, and its numbering namespace stays the closed IP-08 pair.
var ErrTVDB = errors.New("invalid TVDB episode observation")

const (
	maxTVDBPageBytes = 6 << 20
	maxTVDBSetBytes  = 16 << 20
	maxTVDBPages     = 100
	maxTVDBPageRows  = 1000
	maxTVDBSetRows   = 20000
	maxTVDBID        = 9007199254740991
	maxTVDBSeason    = 9999
	maxTVDBNumber    = 9999
	maxTVDBAbsolute  = 1000000
	maxTVDBRuntime   = 10080
	maxTVDBEvidence  = 128
	maxTVDBLocale    = 64
	maxTVDBOverview  = 65536
	maxTVDBName      = 2048
)

// TVDBOrder is one recorded TheTVDB v4 episode ordering. Only the two orders
// that name an IP-08 coordinate namespace are accepted; alternative orderings
// (dvd, default, alternate, regional) are a separate provider mapping and are
// refused rather than reinterpreted as aired or absolute numbers.
type TVDBOrder string

const (
	TVDBOfficial  TVDBOrder = "official"
	TVDBDVD       TVDBOrder = "dvd"
	TVDBAbsolute  TVDBOrder = "absolute"
	TVDBDefault   TVDBOrder = "default"
	TVDBAlternate TVDBOrder = "alternate"
	TVDBRegional  TVDBOrder = "regional"
)

// TVDBRecordedPage is one recorded provider page exactly as acquired. Page is
// the requested page number (not a body field). Body is the complete bounded
// response, including its original links.
type TVDBRecordedPage struct {
	Page int64
	Body []byte
}

// TVDBSetTarget is the original acquisition envelope for one recorded page
// sequence. EpisodeEvidenceIDs supplies the caller's exact native evidence
// identity per provider episode; the adapter never invents one. The policy and
// consent revisions are the captured preparation envelope only: W rechecks the
// current authority, and nothing here is a grant.
type TVDBSetTarget struct {
	SeriesProviderID   int64
	Kind               enrich.SubjectKind
	Season             int64
	Order              TVDBOrder
	Language, Region   string
	PolicyRevision     int64
	ConsentRevision    int64
	EpisodeEvidenceIDs map[int64]string
}

// TVDBEpisode is one normalized recorded row. Nullable coordinates keep SQL
// NULL (absent or JSON null) distinct from a real zero. The adapter stores no
// provider name or overview in the child row: those belong to the episode's own
// evidence header, not the set membership.
type TVDBEpisode struct {
	EvidenceID                                     string
	ProviderID                                     int64
	SeasonNumber, EpisodeNumber, AbsoluteNumber    *int64
	SeasonPresent, EpisodePresent, AbsolutePresent bool
}

// TVDBPage is one exact provider page: the recorded page number, the declared
// next page (nil is the only no-next truth) and the normalized rows in recorded
// provider order.
type TVDBPage struct {
	Page     int64
	NextPage *int64
	Episodes []TVDBEpisode
}

// TVDBSet is complete preparation data, not authority. Observation is the
// IP-08 descriptor; Set is its normalized member manifest. Pages keep the
// recorded provider row order; ManifestPages projects the set manifest onto the
// recorded page boundaries for the IP-08 page commands.
type TVDBSet struct {
	SeriesProviderID int64
	Order            TVDBOrder
	Numbering        TVChildNumbering
	Language, Region string
	Pages            []TVDBPage
	Observation      TVChildObservation
	Set              TVChildSet
}

// ManifestPages returns the recorded page numbers, next-page truth and row
// counts with each page carrying the next contiguous slice of the normalized
// set order. Provider page order is not the IP-08 publication order, so a set
// page command must use this projection rather than Pages directly.
func (s TVDBSet) ManifestPages() []TVDBPage {
	if len(s.Pages) == 0 {
		return nil
	}
	byID := make(map[string]TVDBEpisode, len(s.Set.Members))
	for _, page := range s.Pages {
		for _, episode := range page.Episodes {
			byID[episode.EvidenceID] = episode
		}
	}
	out := make([]TVDBPage, len(s.Pages))
	next := 0
	for i, page := range s.Pages {
		end := next + len(page.Episodes)
		if end > len(s.Set.Members) {
			return nil
		}
		out[i] = TVDBPage{Page: page.Page, NextPage: page.NextPage,
			Episodes: make([]TVDBEpisode, 0, len(page.Episodes))}
		for _, member := range s.Set.Members[next:end] {
			episode, ok := byID[member.EvidenceID]
			if !ok {
				return nil
			}
			out[i].Episodes = append(out[i].Episodes, episode)
		}
		next = end
	}
	if next != len(s.Set.Members) {
		return nil
	}
	return out
}

func tvdbNumbering(order TVDBOrder) (TVChildNumbering, bool) {
	switch order {
	case TVDBOfficial:
		return TVAired, true
	case TVDBAbsolute:
		return TVAbsolute, true
	default:
		return "", false
	}
}

func tvdbText(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && utf8.ValidString(value) &&
		strings.IndexByte(value, 0) < 0
}

func tvdbOptionalText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func tvdbCloneInt(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func tvdbCoordinate(value *int64, max int64) bool {
	return value == nil || (*value >= 0 && *value <= max)
}

// tvdbNextPage validates the recorded next link against the exact provider
// route and returns the declared page. An absent or empty link is the only
// terminal declaration, mirroring the acquisition oracle.
func tvdbNextPage(series int64, order TVDBOrder, page int64, raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Scheme != "https" || u.User != nil ||
		u.Fragment != "" || !strings.EqualFold(u.Hostname(), "api4.thetvdb.com") ||
		(u.Port() != "" && u.Port() != "443") {
		return nil, ErrTVDB
	}
	want := "/v4/series/" + strconv.FormatInt(series, 10) + "/episodes/" + string(order)
	if u.Path != want {
		return nil, ErrTVDB
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(values) != 1 || len(values["page"]) != 1 {
		return nil, ErrTVDB
	}
	number, err := strconv.ParseInt(values.Get("page"), 10, 64)
	if err != nil || strconv.FormatInt(number, 10) != values.Get("page") || number != page+1 {
		return nil, ErrTVDB
	}
	return &number, nil
}

type tvdbRecordedEnvelope struct {
	Status string `json:"status"`
	Data   struct {
		Series struct {
			ID int64 `json:"id"`
		} `json:"series"`
		Episodes []tvdbRecordedEpisode `json:"episodes"`
	} `json:"data"`
	Links struct {
		Next *string `json:"next"`
	} `json:"links"`
}

type tvdbRecordedEpisode struct {
	ID             int64   `json:"id"`
	SeriesID       int64   `json:"seriesId"`
	Name           *string `json:"name"`
	Overview       *string `json:"overview"`
	SeasonNumber   *int64  `json:"seasonNumber"`
	Number         *int64  `json:"number"`
	AbsoluteNumber *int64  `json:"absoluteNumber"`
	Aired          *string `json:"aired"`
	RuntimeMinutes *int64  `json:"runtime"`
}

type tvdbProviderRow struct {
	providerID                                    int64
	evidenceID                                    string
	season, number, absolute                      *int64
	seasonPresent, numberPresent, absolutePresent bool
}

func tvdbRowsForEpisode(recorded tvdbRecordedEpisode, series int64) (tvdbProviderRow, error) {
	var row tvdbProviderRow
	if recorded.ID <= 0 || recorded.ID > maxTVDBID || recorded.SeriesID != series ||
		(recorded.Name != nil && !tvdbOptionalText(*recorded.Name, maxTVDBName)) ||
		(recorded.Overview != nil && !tvdbOptionalText(*recorded.Overview, maxTVDBOverview)) ||
		!tvdbCoordinate(recorded.SeasonNumber, maxTVDBSeason) ||
		!tvdbCoordinate(recorded.Number, maxTVDBNumber) ||
		recorded.AbsoluteNumber != nil &&
			(*recorded.AbsoluteNumber < 1 || *recorded.AbsoluteNumber > maxTVDBAbsolute) ||
		recorded.RuntimeMinutes != nil &&
			(*recorded.RuntimeMinutes < 0 || *recorded.RuntimeMinutes > maxTVDBRuntime) {
		return row, ErrTVDB
	}
	if recorded.Aired != nil && *recorded.Aired != "" {
		if _, err := time.Parse("2006-01-02", *recorded.Aired); err != nil {
			return row, ErrTVDB
		}
	}
	row.providerID = recorded.ID
	row.season, row.seasonPresent = tvdbCloneInt(recorded.SeasonNumber), recorded.SeasonNumber != nil
	row.number, row.numberPresent = tvdbCloneInt(recorded.Number), recorded.Number != nil
	row.absolute, row.absolutePresent = tvdbCloneInt(recorded.AbsoluteNumber), recorded.AbsoluteNumber != nil
	return row, nil
}

func (t TVDBSetTarget) validate() (TVChildNumbering, error) {
	if t.SeriesProviderID <= 0 || t.SeriesProviderID > maxTVDBID ||
		(t.Kind != enrich.Series && t.Kind != enrich.Season) ||
		(t.Kind == enrich.Season && (t.Season < 0 || t.Season > maxTVDBSeason)) ||
		t.PolicyRevision <= 0 || t.ConsentRevision <= 0 ||
		!tvdbText(t.Language, maxTVDBLocale) || strings.TrimSpace(t.Language) != t.Language ||
		!tvdbOptionalText(t.Region, maxTVDBLocale) || strings.TrimSpace(t.Region) != t.Region {
		return "", ErrTVDB
	}
	numbering, ok := tvdbNumbering(t.Order)
	if !ok {
		return "", ErrTVDB
	}
	return numbering, nil
}

// PrepareTVDBSet validates and normalizes one complete recorded TVDB episode
// page sequence. Completeness is proved by the pages themselves: they must be
// contiguous from page 0, every non-final page must declare exactly its next
// page, only the final page may declare no next, and no episode may repeat.
// A bare complete flag is never consulted.
func PrepareTVDBSet(target TVDBSetTarget, recorded []TVDBRecordedPage) (TVDBSet, error) {
	var zero TVDBSet
	numbering, err := target.validate()
	if err != nil {
		return zero, err
	}
	if len(recorded) == 0 || len(recorded) > maxTVDBPages {
		return zero, ErrTVDB
	}
	if target.EpisodeEvidenceIDs == nil {
		return zero, ErrTVDB
	}
	pages := make([]TVDBPage, 0, len(recorded))
	rows := make([]tvdbProviderRow, 0)
	seenEpisodes := make(map[int64]bool)
	totalBytes := 0
	for i, rec := range recorded {
		if rec.Page != int64(i) || len(rec.Body) == 0 {
			return zero, ErrTVDB
		}
		if len(rec.Body) > maxTVDBPageBytes {
			return zero, errors.Join(ErrTVDB, &enrich.ResponseSizeError{
				LimitBytes: maxTVDBPageBytes, ObservedBytes: int64(len(rec.Body))})
		}
		totalBytes += len(rec.Body)
		if totalBytes > maxTVDBSetBytes {
			return zero, errors.Join(ErrTVDB, &enrich.ResponseSizeError{
				LimitBytes: maxTVDBSetBytes, ObservedBytes: int64(totalBytes)})
		}
		if !utf8.Valid(rec.Body) {
			return zero, ErrTVDB
		}
		var envelope tvdbRecordedEnvelope
		if err := json.Unmarshal(rec.Body, &envelope); err != nil {
			return zero, ErrTVDB
		}
		if envelope.Status != "success" || envelope.Data.Series.ID != target.SeriesProviderID ||
			len(envelope.Data.Episodes) > maxTVDBPageRows {
			return zero, ErrTVDB
		}
		nextRaw := ""
		if envelope.Links.Next != nil {
			nextRaw = *envelope.Links.Next
		}
		next, err := tvdbNextPage(target.SeriesProviderID, target.Order, rec.Page, nextRaw)
		if err != nil {
			return zero, err
		}
		if i < len(recorded)-1 && next == nil {
			// A middle page without a next link cannot hide later recorded pages.
			return zero, ErrTVDB
		}
		if i == len(recorded)-1 && next != nil {
			// The last page declares more provider pages than were recorded.
			return zero, ErrTVDB
		}
		page := TVDBPage{Page: rec.Page, NextPage: next, Episodes: make([]TVDBEpisode, 0, len(envelope.Data.Episodes))}
		for _, episode := range envelope.Data.Episodes {
			if seenEpisodes[episode.ID] {
				return zero, ErrTVDB
			}
			seenEpisodes[episode.ID] = true
			row, err := tvdbRowsForEpisode(episode, target.SeriesProviderID)
			if err != nil {
				return zero, err
			}
			rows = append(rows, row)
			page.Episodes = append(page.Episodes, TVDBEpisode{ProviderID: row.providerID,
				SeasonNumber: row.season, EpisodeNumber: row.number, AbsoluteNumber: row.absolute,
				SeasonPresent: row.seasonPresent, EpisodePresent: row.numberPresent, AbsolutePresent: row.absolutePresent})
		}
		pages = append(pages, page)
	}
	if len(rows) == 0 || len(rows) > maxTVDBSetRows {
		return zero, ErrTVDB
	}
	if err := tvdbAssignEvidence(target.EpisodeEvidenceIDs, rows, pages); err != nil {
		return zero, err
	}
	observation, err := tvdbObservation(target, numbering, rows)
	if err != nil {
		return zero, err
	}
	set, err := NormalizeTVChildren(observation)
	if err != nil {
		return zero, errors.Join(ErrTVDB, err)
	}
	return TVDBSet{SeriesProviderID: target.SeriesProviderID, Order: target.Order,
		Numbering: numbering, Language: target.Language, Region: target.Region,
		Pages: pages, Observation: observation, Set: set}, nil
}

// tvdbAssignEvidence resolves the caller's exact native evidence identity for
// every recorded episode. Missing, extra, duplicate or malformed identities are
// refused: the adapter never mints or reuses an evidence ID.
func tvdbAssignEvidence(ids map[int64]string, rows []tvdbProviderRow, pages []TVDBPage) error {
	if len(ids) != len(rows) {
		return ErrTVDB
	}
	used := make(map[string]bool, len(ids))
	seen := make(map[int64]bool, len(rows))
	for i := range rows {
		id, ok := ids[rows[i].providerID]
		if !ok || seen[rows[i].providerID] || !tvdbText(id, maxTVDBEvidence) ||
			strings.TrimSpace(id) != id {
			return ErrTVDB
		}
		seen[rows[i].providerID] = true
		if used[id] {
			return ErrTVDB
		}
		used[id] = true
		rows[i].evidenceID = id
	}
	// The page rows are the same bodies in the same order; assign after the full
	// identity set is known so a duplicate never leaves half a set populated.
	for i := range pages {
		for j := range pages[i].Episodes {
			pages[i].Episodes[j].EvidenceID = ids[pages[i].Episodes[j].ProviderID]
		}
	}
	return nil
}

type tvdbSeasonGroup struct {
	season int64
	rows   []tvdbProviderRow
}

func tvdbObservation(target TVDBSetTarget, numbering TVChildNumbering,
	rows []tvdbProviderRow) (TVChildObservation, error) {
	var zero TVChildObservation
	seenAired := make(map[[2]int64]bool, len(rows))
	seenAbsolute := make(map[int64]bool, len(rows))
	for _, row := range rows {
		switch numbering {
		case TVAired:
			if !row.seasonPresent || !row.numberPresent {
				return zero, ErrTVDB
			}
			key := [2]int64{*row.season, *row.number}
			if seenAired[key] {
				return zero, ErrTVDB
			}
			seenAired[key] = true
		case TVAbsolute:
			if !row.absolutePresent {
				return zero, ErrTVDB
			}
			if seenAbsolute[*row.absolute] {
				return zero, ErrTVDB
			}
			seenAbsolute[*row.absolute] = true
		default:
			return zero, ErrTVDB
		}
	}
	groups := make([]tvdbSeasonGroup, 0)
	index := make(map[int64]int)
	for _, row := range rows {
		season := int64(0)
		if row.seasonPresent {
			season = *row.season
		}
		at, ok := index[season]
		if !ok {
			at = len(groups)
			index[season] = at
			groups = append(groups, tvdbSeasonGroup{season: season})
		}
		groups[at].rows = append(groups[at].rows, row)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].season < groups[j].season })
	if len(groups) > maxTVChildSeasons {
		return zero, ErrTVDB
	}
	for _, group := range groups {
		if len(group.rows) > maxTVEpisodesPerSeason {
			return zero, ErrTVDB
		}
	}
	if target.Kind == enrich.Season {
		// A season set declares exactly one season, and every recorded episode
		// must still name it: a series-wide page sequence is not silently
		// filtered down to one season.
		if len(groups) != 1 || groups[0].season != target.Season {
			return zero, ErrTVDB
		}
		for _, row := range rows {
			if !row.seasonPresent {
				return zero, ErrTVDB
			}
		}
	}
	observation := TVChildObservation{Kind: target.Kind, Numbering: numbering,
		DeclaredSeasons: int64(len(groups)), Seasons: make([]TVChildSeason, 0, len(groups))}
	for _, group := range groups {
		season := TVChildSeason{SeasonNumber: group.season, DeclaredEpisodes: int64(len(group.rows)),
			Episodes: make([]TVChildEpisode, 0, len(group.rows))}
		for _, row := range group.rows {
			episode := TVChildEpisode{EvidenceID: row.evidenceID, Order: numbering,
				SeasonPresent: row.seasonPresent, EpisodePresent: row.numberPresent,
				AbsolutePresent: row.absolutePresent}
			if row.seasonPresent {
				episode.SeasonNumber = *row.season
			}
			if row.numberPresent {
				episode.EpisodeNumber = *row.number
			}
			if row.absolutePresent {
				episode.AbsoluteNumber = *row.absolute
			}
			season.Episodes = append(season.Episodes, episode)
		}
		observation.Seasons = append(observation.Seasons, season)
	}
	return observation, nil
}
