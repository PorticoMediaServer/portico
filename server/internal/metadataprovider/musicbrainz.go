package metadataprovider

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var mbid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type MusicBrainz struct{ http *transport }

var musicBrainzTransport = newTransport("musicbrainz", "https://musicbrainz.org/ws/2", "Portico/0.1 (https://getportico.tv)")

func NewMusicBrainz() *MusicBrainz { return &MusicBrainz{http: musicBrainzTransport} }

type Artist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
}
type ArtistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     Artist `json:"artist"`
}
type Recording struct {
	ISRCs          []string        `json:"isrcs,omitempty"`
	Relations      []MusicRelation `json:"relations,omitempty"`
	ID             string          `json:"id"`
	Title          string          `json:"title"`
	LengthMillis   *int            `json:"length"`
	Disambiguation string          `json:"disambiguation"`
	ArtistCredit   []ArtistCredit  `json:"artist-credit"`
}
type ReleaseTrack struct {
	ArtistCredit []ArtistCredit `json:"artist-credit"`
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Position     int            `json:"position"`
	Number       string         `json:"number"`
	LengthMillis *int           `json:"length"`
	Recording    Recording      `json:"recording"`
}
type Medium struct {
	Position int            `json:"position"`
	Format   string         `json:"format"`
	Title    string         `json:"title"`
	Tracks   []ReleaseTrack `json:"tracks"`
}
type ReleaseGroup struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
}

// MBGenre is one MusicBrainz genre vote on a release, most-voted first.
type MBGenre struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}
type Release struct {
	Status             string         `json:"status,omitempty"`
	Packaging          string         `json:"packaging,omitempty"`
	LabelInfo          []ReleaseLabel `json:"label-info,omitempty"`
	Genres             []MBGenre      `json:"genres,omitempty"`
	TextRepresentation *ReleaseText   `json:"text-representation,omitempty"`
	ID                 string         `json:"id"`
	Title              string         `json:"title"`
	Date               string         `json:"date"`
	Country            string         `json:"country"`
	Barcode            string         `json:"barcode"`
	Disambiguation     string         `json:"disambiguation"`
	ArtistCredit       []ArtistCredit `json:"artist-credit"`
	Media              []Medium       `json:"media"`
	ReleaseGroup       ReleaseGroup   `json:"release-group"`
}
type RecordingLookup struct {
	RequestedID string
	Recording   Recording
}
type ReleaseLookup struct {
	RequestedID string
	Release     Release
}

func validCredits(credits []ArtistCredit) bool {
	if len(credits) > 128 {
		return false
	}
	for _, credit := range credits {
		if !mbid.MatchString(credit.Artist.ID) || len(credit.Name) > 2048 || len(credit.Artist.Name) > 2048 || len(credit.JoinPhrase) > 256 {
			return false
		}
	}
	return true
}
func validReleaseGenres(genres []MBGenre) bool {
	if len(genres) > 64 {
		return false
	}
	for _, g := range genres {
		name := strings.TrimSpace(g.Name)
		if name == "" || len(name) > 200 || g.Count < 0 {
			return false
		}
	}
	return true
}
func validRecording(value Recording) bool {
	return mbid.MatchString(value.ID) && len(value.Title) <= 4096 && validCredits(value.ArtistCredit) && (value.LengthMillis == nil || *value.LengthMillis >= 0)
}
func literal(value string) string {
	var out strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return `"` + out.String() + `"`
}
func (s *MusicBrainz) SearchRecordings(ctx context.Context, title, artist string) ([]Recording, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(artist) == "" || len(title) > 512 || len(artist) > 512 {
		return nil, errors.New("recording title and artist are required")
	}
	query := url.Values{"query": {"recording:" + literal(title) + " AND artist:" + literal(artist)}, "fmt": {"json"}, "limit": {"25"}}
	var result struct {
		Recordings []Recording `json:"recordings"`
	}
	if err := s.http.request(ctx, "GET", "/recording?"+query.Encode(), "", nil, &result); err != nil {
		return nil, err
	}
	if result.Recordings == nil || len(result.Recordings) > 25 {
		return nil, &Error{Provider: "musicbrainz", Code: "invalid_search"}
	}
	for _, recording := range result.Recordings {
		if !validRecording(recording) {
			return nil, &Error{Provider: "musicbrainz", Code: "invalid_recording"}
		}
	}
	return result.Recordings, nil
}

// Merge redirects are followed only within the same public API/entity; arbitrary
// provider URLs and cross-origin redirects cannot become server-side fetch targets.
func (s *MusicBrainz) lookup(ctx context.Context, entity, id, includes string, out any) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !mbid.MatchString(id) {
		return errors.New("invalid MusicBrainz identifier")
	}
	query := url.Values{"fmt": {"json"}, "inc": {includes}}
	route := "/" + entity + "/" + strings.ToLower(id) + "?" + query.Encode()
	for redirects := 0; redirects <= 3; redirects++ {
		err := s.http.request(ctx, "GET", route, "", nil, out)
		var problem *Error
		if !errors.As(err, &problem) || (problem.Status != 301 && problem.Status != 302 && problem.Status != 307 && problem.Status != 308) {
			if err == nil {
				var actual string
				switch v := out.(type) {
				case *Recording:
					actual = v.ID
				case *Release:
					actual = v.ID
				}
				current, _ := url.Parse(route)
				if !strings.EqualFold(actual, strings.TrimPrefix(current.Path, "/"+entity+"/")) {
					return &Error{Provider: "musicbrainz", Code: "identity_mismatch"}
				}
			}
			return err
		}
		base, _ := url.Parse(s.http.base + route)
		next, parseErr := base.Parse(problem.location)
		prefix := "/ws/2/" + entity + "/"
		if parseErr != nil || next.Scheme != base.Scheme || next.Host != base.Host || next.User != nil || next.Fragment != "" || !strings.HasPrefix(next.Path, prefix) || !mbid.MatchString(strings.TrimPrefix(next.Path, prefix)) {
			return &Error{Provider: "musicbrainz", Code: "invalid_redirect"}
		}
		route = "/" + entity + "/" + strings.TrimPrefix(next.Path, prefix) + "?" + query.Encode()
	}
	return &Error{Provider: "musicbrainz", Code: "redirect_limit"}
}
func (s *MusicBrainz) Recording(ctx context.Context, id string) (RecordingLookup, error) {
	result := RecordingLookup{RequestedID: id}
	if err := s.lookup(ctx, "recording", id, "artist-credits+isrcs+work-rels+artist-rels", &result.Recording); err != nil {
		return result, err
	}
	if !validRecording(result.Recording) {
		return result, &Error{Provider: "musicbrainz", Code: "invalid_recording"}
	}
	return result, nil
}
func (s *MusicBrainz) Release(ctx context.Context, id string) (ReleaseLookup, error) {
	result := ReleaseLookup{RequestedID: id}
	if err := s.lookup(ctx, "release", id, "recordings+artist-credits+release-groups+labels+genres+isrcs", &result.Release); err != nil {
		return result, err
	}
	r := result.Release
	if !mbid.MatchString(r.ID) || !mbid.MatchString(r.ReleaseGroup.ID) || len(r.Title) > 4096 || !validCredits(r.ArtistCredit) || !validReleaseGenres(r.Genres) || len(r.Media) > 200 {
		return result, &Error{Provider: "musicbrainz", Code: "invalid_release"}
	}
	tracks := 0
	mediumPositions := map[int]bool{}
	seen := map[string]bool{}
	for _, medium := range r.Media {
		if medium.Position < 1 || mediumPositions[medium.Position] {
			return result, &Error{Provider: "musicbrainz", Code: "invalid_medium"}
		}
		mediumPositions[medium.Position] = true
		trackPositions := map[int]bool{}
		for _, track := range medium.Tracks {
			tracks++
			if tracks > 10000 || !mbid.MatchString(track.ID) || seen[track.ID] || track.Position < 1 || !validCredits(track.ArtistCredit) || trackPositions[track.Position] || len(track.Title) > 4096 || (track.LengthMillis != nil && *track.LengthMillis < 0) || !validRecording(track.Recording) {
				return result, &Error{Provider: "musicbrainz", Code: "invalid_track"}
			}
			seen[track.ID] = true
			trackPositions[track.Position] = true
		}
	}
	return result, nil
}

type ReleaseCandidate struct {
	Barcode        string         `json:"barcode,omitempty"`
	TrackCount     int            `json:"track-count,omitempty"`
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Date           string         `json:"date"`
	Country        string         `json:"country"`
	Disambiguation string         `json:"disambiguation"`
	ArtistCredit   []ArtistCredit `json:"artist-credit"`
}

func (s *MusicBrainz) SearchReleases(ctx context.Context, title, artist string) ([]ReleaseCandidate, error) {
	if strings.TrimSpace(title) == "" || strings.TrimSpace(artist) == "" || len(title) > 512 || len(artist) > 512 {
		return nil, errors.New("release title and artist are required")
	}
	query := url.Values{"query": {"release:" + literal(title) + " AND artist:" + literal(artist)}, "fmt": {"json"}, "limit": {"25"}}
	var out struct {
		Releases []ReleaseCandidate `json:"releases"`
	}
	if e := s.http.request(ctx, "GET", "/release?"+query.Encode(), "", nil, &out); e != nil {
		return nil, e
	}
	if out.Releases == nil || len(out.Releases) > 25 {
		return nil, &Error{Provider: "musicbrainz", Code: "invalid_search"}
	}
	seen := map[string]bool{}
	for _, v := range out.Releases {
		if !mbid.MatchString(v.ID) || seen[v.ID] || len(v.Title) > 4096 || !validCredits(v.ArtistCredit) {
			return nil, &Error{Provider: "musicbrainz", Code: "invalid_release"}
		}
		seen[v.ID] = true
	}
	return out.Releases, nil
}
