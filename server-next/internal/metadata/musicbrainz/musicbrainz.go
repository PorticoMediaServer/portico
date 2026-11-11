// Package musicbrainz asks MusicBrainz about albums and artists. It is the metadata.Provider
// of music libraries (definition rule 2). It needs no key; MusicBrainz asks every client to
// send at most one request a second and to name itself, and answers 503 when asked faster.
//
// An album is a MusicBrainz release: the files of an album are one edition of it, and a
// release's media are its discs. Album covers are Cover Art Archive's.
package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

const (
	// Origin is where MusicBrainz's API is.
	Origin = "https://musicbrainz.org"
	// covers is Cover Art Archive, which serves albums' covers.
	covers     = "https://coverartarchive.org/"
	maxResults = 10
	maxGenres  = 10
)

var mbid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Client asks MusicBrainz. Make one with New and share it: MusicBrainz counts every request
// from the server together.
type Client struct {
	origin string
	http   *fetch.Client

	// The last release asked about, by the id it was asked by, so asking for its discs right
	// after its details is not a second request for the same answer.
	mu     sync.Mutex
	last   *release
	lastID string
	at     time.Time
}

// Every is the spacing MusicBrainz asks of every client: one request a second.
const Every = time.Second

// New makes a client. origin is musicbrainz.Origin with Every, or a test's own server with
// no spacing.
func New(origin string, every time.Duration) *Client {
	c := fetch.New("musicbrainz", every, time.Minute)
	c.Busy503 = true
	c.MovedIsAnswer = true
	// MusicBrainz counts searches apart from lookups, and its search allowance is shared by
	// every client of the service, so the two are paced by its headers apart.
	c.Bucket = func(r *http.Request) string {
		if r.URL.Query().Has("query") {
			return "search"
		}
		return "lookup"
	}
	return &Client{origin: strings.TrimRight(origin, "/"), http: c}
}

func (c *Client) Name() string { return "musicbrainz" }

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	q.Set("fmt", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/ws/2"+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	raw, err := c.http.Do(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// literal quotes a name for MusicBrainz's search syntax, so a name with a colon or a
// bracket is searched for as written.
func literal(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		if strings.ContainsRune(`+-&|!(){}[]^"~*?:\/`, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
}

// fullDate is a date only when it names a day: MusicBrainz often knows only the year.
func fullDate(date string) string {
	if len(date) == len("2006-01-02") {
		return date
	}
	return ""
}

type artist struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	SortName       string  `json:"sort-name"`
	Disambiguation string  `json:"disambiguation"`
	Country        string  `json:"country"`
	Score          float64 `json:"score"`
	Genres         []genre `json:"genres"`
	Area           *struct {
		Name string `json:"name"`
	} `json:"area"`
}

type credit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     artist `json:"artist"`
}

type genre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// credits are the artists a release or track is credited to, in order.
func credits(list []credit) []metadata.Credit {
	var out []metadata.Credit
	for _, c := range list {
		name := c.Name
		if name == "" {
			name = c.Artist.Name
		}
		if name == "" {
			continue
		}
		out = append(out, metadata.Credit{Person: metadata.Ref{Provider: "musicbrainz", Kind: "artist", ID: c.Artist.ID}, Name: name, Role: "artist"})
	}
	return out
}

// creditLine is a credit as it is printed on the cover: "Simon & Garfunkel".
func creditLine(list []credit) string {
	var out strings.Builder
	for _, c := range list {
		out.WriteString(c.Name + c.JoinPhrase)
	}
	return out.String()
}

// genres are the most-voted genres, as MusicBrainz's voters name them. A release's own votes
// and its release group's are added up by name. A genre with less than a tenth of the votes
// of the most-voted one is left out: Abbey Road's 25 votes for rock come with one or two each
// for ambient, dance and trance.
func genres(list []genre) []string {
	votes := map[string]int{}
	var sorted []genre
	for _, g := range list {
		if g.Name == "" {
			continue
		}
		if _, seen := votes[g.Name]; !seen {
			sorted = append(sorted, genre{Name: g.Name})
		}
		votes[g.Name] += g.Count
	}
	for i := range sorted {
		sorted[i].Count = votes[sorted[i].Name]
	}
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Count > sorted[b].Count })
	var out []string
	for _, g := range sorted[:min(len(sorted), maxGenres)] {
		if g.Count*10 >= sorted[0].Count {
			out = append(out, g.Name)
		}
	}
	return out
}

type release struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Date           string   `json:"date"`
	Country        string   `json:"country"`
	Disambiguation string   `json:"disambiguation"`
	Score          float64  `json:"score"`
	Status         string   `json:"status"`
	TrackCount     int      `json:"track-count"`
	ArtistCredit   []credit `json:"artist-credit"`
	Genres         []genre  `json:"genres"`
	ReleaseGroup   struct {
		ID     string  `json:"id"`
		Genres []genre `json:"genres"`
	} `json:"release-group"`
	LabelInfo []struct {
		Label *struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"label-info"`
	CoverArt struct {
		Front bool `json:"front"`
	} `json:"cover-art-archive"`
	Media []struct {
		Position   int `json:"position"`
		TrackCount int `json:"track-count"`
		Tracks     []struct {
			Position     int      `json:"position"`
			Title        string   `json:"title"`
			ArtistCredit []credit `json:"artist-credit"`
			Recording    struct {
				ID string `json:"id"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

// Search answers the albums or artists that could be the one asked about, best first. An
// album is searched for by its name and its artist's. Several editions of one album are one
// answer, the best scored, so they do not look like several different albums.
func (c *Client) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	switch q.Kind {
	case "album":
		query := "release:" + literal(q.Title)
		if q.Creator != "" {
			query += " AND artist:" + literal(q.Creator)
		}
		var page struct {
			Releases []release `json:"releases"`
		}
		if err := c.get(ctx, "/release", url.Values{"query": {query}, "limit": {"25"}}, &page); err != nil {
			return nil, err
		}
		// One answer an album: of its editions, the one most like the files (see better), so
		// two editions do not look like two albums and a deluxe edition does not stand for
		// the album the files are.
		var picked []release
		group := map[string]int{}
		for _, r := range page.Releases {
			if !mbid.MatchString(r.ID) {
				continue
			}
			at, seen := group[r.ReleaseGroup.ID]
			switch {
			case !seen || r.ReleaseGroup.ID == "":
				group[r.ReleaseGroup.ID] = len(picked)
				picked = append(picked, r)
			case better(r, picked[at], q):
				r.Score = picked[at].Score
				picked[at] = r
			}
		}
		var out []metadata.Candidate
		for _, r := range picked[:min(len(picked), maxResults)] {
			out = append(out, metadata.Candidate{Ref: metadata.Ref{Provider: "musicbrainz", Kind: "album", ID: r.ID}, Title: r.Title,
				Year: year(r.Date), Overview: creditLine(r.ArtistCredit), PosterURL: r.cover(), Popularity: r.Score})
		}
		return out, nil
	case "artist":
		var page struct {
			Artists []artist `json:"artists"`
		}
		if err := c.get(ctx, "/artist", url.Values{"query": {"artist:" + literal(q.Title)}, "limit": {"10"}}, &page); err != nil {
			return nil, err
		}
		var out []metadata.Candidate
		for _, a := range page.Artists {
			if mbid.MatchString(a.ID) {
				out = append(out, metadata.Candidate{Ref: metadata.Ref{Provider: "musicbrainz", Kind: "artist", ID: a.ID}, Title: a.Name,
					OriginalTitle: a.SortName, Overview: a.Disambiguation, Popularity: a.Score})
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("musicbrainz has no %q", q.Kind)
}

// cover is the address of an album's cover at Cover Art Archive: the edition's own front cover
// when a lookup says it has one, else the album's (its release group's) front cover, which
// Cover Art Archive answers from whichever edition has one. A search's answers do not say
// whether there is a cover, so an album with none at all answers 404 there, and the server's
// picture fetch shows none.
func (r *release) cover() string {
	switch {
	case r.CoverArt.Front:
		return covers + "release/" + r.ID + "/front"
	case mbid.MatchString(r.ReleaseGroup.ID):
		return covers + "release-group/" + r.ReleaseGroup.ID + "/front"
	}
	return ""
}

// better reports whether edition a is more like the album the files are than edition b: the
// same discs with the same number of tracks each, then the same number of tracks, then the
// year closest to the files', then an official release, then the earliest. MusicBrainz scores
// every edition of an album alike and lists them in no useful order: with no year, the first
// listed for Abbey Road is a 2019 three-disc deluxe edition.
func better(a, b release, q metadata.Query) bool {
	if la, lb := sameLayout(a, q.Layout), sameLayout(b, q.Layout); la != lb {
		return la
	}
	if n := total(q.Layout); n > 0 {
		if ta, tb := a.TrackCount == n, b.TrackCount == n; ta != tb {
			return ta
		}
	}
	if q.Year != 0 {
		da, db := distance(a.Date, q.Year), distance(b.Date, q.Year)
		if da != db {
			return da < db
		}
	}
	if oa, ob := a.Status == "Official", b.Status == "Official"; oa != ob {
		return oa
	}
	if a.Date == "" || b.Date == "" {
		return a.Date != ""
	}
	return a.Date < b.Date
}

// release answers one release with its discs and tracks.
func (c *Client) release(ctx context.Context, id string) (*release, error) {
	id = strings.ToLower(id)
	if !mbid.MatchString(id) {
		return nil, metadata.ErrNotFound
	}
	c.mu.Lock()
	if c.last != nil && c.lastID == id && time.Since(c.at) < 10*time.Minute {
		r := c.last
		c.mu.Unlock()
		return r, nil
	}
	c.mu.Unlock()
	// A release merged into another answers with the other, under its own id.
	var r release
	if err := c.get(ctx, "/release/"+id, url.Values{"inc": {"recordings+artist-credits+release-groups+labels+genres"}}, &r); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.last, c.lastID, c.at = &r, id, time.Now()
	c.mu.Unlock()
	return &r, nil
}

// Details answers an album (a release) or an artist. An album's first credit is its artist,
// with the artist's MusicBrainz id.
func (c *Client) Details(ctx context.Context, ref metadata.Ref, _ metadata.Locale) (metadata.Record, error) {
	switch ref.Kind {
	case "album":
		r, err := c.release(ctx, ref.ID)
		if err != nil {
			return metadata.Record{}, err
		}
		out := metadata.Record{Ref: metadata.Ref{Provider: "musicbrainz", Kind: "album", ID: r.ID}, Title: r.Title, Year: year(r.Date),
			ReleaseDate: fullDate(r.Date), Genres: genres(append(r.Genres, r.ReleaseGroup.Genres...)), Credits: credits(r.ArtistCredit)}
		for _, info := range r.LabelInfo {
			if info.Label != nil && info.Label.Name != "" && !contains(out.Studios, info.Label.Name) {
				out.Studios = append(out.Studios, info.Label.Name)
			}
		}
		if cover := r.cover(); cover != "" {
			out.Pictures = []metadata.Picture{{Role: "cover", URL: cover}}
		}
		for _, medium := range r.Media {
			out.Groups = append(out.Groups, medium.Position)
		}
		return out, nil
	case "artist":
		id := strings.ToLower(ref.ID)
		if !mbid.MatchString(id) {
			return metadata.Record{}, metadata.ErrNotFound
		}
		var a artist
		if err := c.get(ctx, "/artist/"+id, url.Values{"inc": {"genres"}}, &a); err != nil {
			return metadata.Record{}, err
		}
		out := metadata.Record{Ref: metadata.Ref{Provider: "musicbrainz", Kind: "artist", ID: a.ID}, Title: a.Name, Genres: genres(a.Genres)}
		if a.Area != nil && a.Area.Name != "" {
			out.Countries = []string{a.Area.Name}
		}
		return out, nil
	}
	return metadata.Record{}, fmt.Errorf("musicbrainz has no %q", ref.Kind)
}

// sameLayout reports whether an edition has the files' discs with the files' number of tracks on each.
func sameLayout(r release, layout []int) bool {
	if len(layout) == 0 || len(r.Media) != len(layout) {
		return false
	}
	for i, m := range r.Media {
		if m.TrackCount != layout[i] {
			return false
		}
	}
	return true
}

func total(layout []int) int {
	n := 0
	for _, tracks := range layout {
		n += tracks
	}
	return n
}

// distance is how many years a date is from a year; an undated edition is furthest.
func distance(date string, want int) int {
	if y := year(date); y != 0 {
		return abs(y - want)
	}
	return 1 << 30
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Children answers the tracks of one disc of an album, numbered as on the disc.
func (c *Client) Children(ctx context.Context, parent metadata.Ref, group int, _ metadata.Locale) ([]metadata.Record, error) {
	if parent.Kind != "album" {
		return nil, fmt.Errorf("musicbrainz has nothing under a %q", parent.Kind)
	}
	r, err := c.release(ctx, parent.ID)
	if err != nil {
		return nil, err
	}
	for _, medium := range r.Media {
		if medium.Position != group {
			continue
		}
		out := make([]metadata.Record, 0, len(medium.Tracks))
		for _, t := range medium.Tracks {
			out = append(out, metadata.Record{Ref: metadata.Ref{Provider: "musicbrainz", Kind: "track", ID: t.Recording.ID}, Title: t.Title,
				Credits: credits(t.ArtistCredit), Index: t.Position, ParentIndex: group})
		}
		return out, nil
	}
	return nil, metadata.ErrNotFound
}
