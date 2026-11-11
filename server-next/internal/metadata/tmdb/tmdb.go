// Package tmdb asks The Movie Database about films and shows. It is one metadata.Provider.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

const (
	// Origin is where TMDB's API is.
	Origin     = "https://api.themoviedb.org"
	pictures   = "https://image.tmdb.org/t/p/original"
	maxAnswer  = 4 << 20 // no answer is larger; more is not read
	maxCast    = 50
	maxResults = 10
	// maxPictures is how many of each kind of picture are kept of TMDB's list, to choose from.
	maxPictures = 20
)

// Client asks TMDB. The zero value is not usable: make one with New.
type Client struct {
	origin string
	// pictures is where the pictures an answer names are fetched from: TMDB's picture
	// server, or a test's own server.
	pictures string
	token    string
	http     *http.Client
}

// New makes a client that signs its requests with an API read access token. origin is
// tmdb.Origin, or a test's own server.
func New(token, origin string) *Client {
	origin = strings.TrimRight(origin, "/")
	base := pictures
	if origin != Origin {
		base = origin + "/t/p/original"
	}
	return &Client{origin: origin, pictures: base, token: token, http: &http.Client{
		Timeout: 20 * time.Second,
		// Every address is built here; an answer that points somewhere else is not followed.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) Name() string { return "tmdb" }

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/3"+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return metadata.ErrNotFound
	case res.StatusCode != http.StatusOK:
		return fmt.Errorf("tmdb answered %d for %s", res.StatusCode, path)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxAnswer+1))
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	if len(raw) > maxAnswer {
		return errors.New("tmdb sent an answer larger than any real one")
	}
	return json.Unmarshal(raw, out)
}

func language(l metadata.Locale) string {
	if l.Language == "" {
		return "en-US"
	}
	if strings.Contains(l.Language, "-") || l.Region == "" {
		return l.Language
	}
	return l.Language + "-" + l.Region
}

func kindPath(kind string) (string, error) {
	switch kind {
	case "movie":
		return "movie", nil
	case "show":
		return "tv", nil
	}
	return "", fmt.Errorf("tmdb has no %q", kind)
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
}

func (c *Client) picture(path string) string {
	if path == "" {
		return ""
	}
	return c.pictures + path
}

// title is the part of an answer every film and show has. A film has title and release_date;
// a show has name and first_air_date.
type title struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Popularity    float64 `json:"popularity"`
}

func (t title) candidate(c *Client, kind string, exact bool) metadata.Candidate {
	return metadata.Candidate{Ref: metadata.Ref{Provider: "tmdb", Kind: kind, ID: strconv.FormatInt(t.ID, 10)},
		Title: t.Title + t.Name, OriginalTitle: t.OriginalTitle + t.OriginalName, Year: year(t.ReleaseDate + t.FirstAirDate),
		Overview: t.Overview, PosterURL: c.picture(t.PosterPath), Popularity: t.Popularity, Exact: exact}
}

// findSources are the other providers' ids TMDB can look a title up by.
var findSources = map[string]string{"imdb": "imdb_id", "tvdb": "tvdb_id"}

func (c *Client) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	path, err := kindPath(q.Kind)
	if err != nil {
		return nil, err
	}
	for _, id := range q.IDs {
		source, known := findSources[id.Provider]
		if !known {
			continue
		}
		var found struct {
			Movies []title `json:"movie_results"`
			Shows  []title `json:"tv_results"`
		}
		if err := c.get(ctx, "/find/"+url.PathEscape(id.ID), url.Values{"external_source": {source}, "language": {language(q.Locale)}}, &found); err != nil {
			return nil, err
		}
		results := found.Movies
		if q.Kind == "show" {
			results = found.Shows
		}
		if len(results) == 1 {
			return []metadata.Candidate{results[0].candidate(c, q.Kind, true)}, nil
		}
	}
	params := url.Values{"query": {q.Title}, "language": {language(q.Locale)}, "include_adult": {strconv.FormatBool(q.IncludeAdult)}}
	// The year is not sent: TMDB's year filter is exact, and a film is often filed a year
	// either side. It is compared when the answers are scored.
	var page struct {
		Results []title `json:"results"`
	}
	if err := c.get(ctx, "/search/"+path, params, &page); err != nil {
		return nil, err
	}
	out := make([]metadata.Candidate, 0, min(len(page.Results), maxResults))
	for _, result := range page.Results[:min(len(page.Results), maxResults)] {
		out = append(out, result.candidate(c, q.Kind, false))
	}
	return out, nil
}

type named struct {
	Name string `json:"name"`
}

func names(list []named) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		if n.Name != "" {
			out = append(out, n.Name)
		}
	}
	return out
}

// person is one line of a cast or crew list. A film's has character and job; a show's
// whole-run list has roles and jobs.
type person struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character"`
	Job       string `json:"job"`
	Roles     []struct {
		Character string `json:"character"`
	} `json:"roles"`
	Jobs []struct {
		Job string `json:"job"`
	} `json:"jobs"`
}

type people struct {
	Cast  []person `json:"cast"`
	Crew  []person `json:"crew"`
	Guest []person `json:"guest_stars"`
}

// crewRoles are the crew a title's page names. Everyone else on a crew list is left out.
var crewRoles = map[string]string{
	"Director": "director", "Writer": "writer", "Screenplay": "writer", "Story": "writer", "Teleplay": "writer",
	"Creator": "writer", "Producer": "producer", "Executive Producer": "producer", "Original Music Composer": "composer",
}

func (p people) credits() []metadata.Credit {
	var out []metadata.Credit
	seen := map[string]bool{}
	add := func(who person, role, character string) {
		key := role + "/" + strconv.FormatInt(who.ID, 10)
		if who.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, metadata.Credit{Person: metadata.Ref{Provider: "tmdb", Kind: "person", ID: strconv.FormatInt(who.ID, 10)},
			Name: who.Name, Role: role, Character: character})
	}
	// The people who made it first, then the cast in billing order.
	for _, who := range p.Crew {
		jobs := []string{who.Job}
		for _, j := range who.Jobs {
			jobs = append(jobs, j.Job)
		}
		for _, job := range jobs {
			if role, wanted := crewRoles[job]; wanted {
				add(who, role, "")
			}
		}
	}
	for _, who := range p.Cast[:min(len(p.Cast), maxCast)] {
		character := who.Character
		if character == "" && len(who.Roles) > 0 {
			character = who.Roles[0].Character
		}
		add(who, "actor", character)
	}
	for _, who := range p.Guest[:min(len(p.Guest), maxCast)] {
		add(who, "guest", who.Character)
	}
	return out
}

// image is one picture in TMDB's list of a title's pictures.
type image struct {
	Path     string `json:"file_path"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Language string `json:"iso_639_1"`
}

type details struct {
	title
	Tagline      string  `json:"tagline"`
	VoteAverage  float64 `json:"vote_average"`
	Genres       []named `json:"genres"`
	Companies    []named `json:"production_companies"`
	Countries    []named `json:"production_countries"`
	Networks     []named `json:"networks"`
	InProduction bool    `json:"in_production"`
	Seasons      []struct {
		Number int `json:"season_number"`
	} `json:"seasons"`
	Collection *title `json:"belongs_to_collection"`
	// Images are the other pictures TMDB has for the title, best first in each list.
	Images struct {
		Posters   []image `json:"posters"`
		Backdrops []image `json:"backdrops"`
		Logos     []image `json:"logos"`
	} `json:"images"`
	Credits     people `json:"credits"`
	WholeRun    people `json:"aggregate_credits"`
	ExternalIDs struct {
		IMDb string `json:"imdb_id"`
		TVDB int64  `json:"tvdb_id"`
	} `json:"external_ids"`
	// A film's ratings, by country, one for each kind of release.
	ReleaseDates struct {
		Results []struct {
			Country  string `json:"iso_3166_1"`
			Releases []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
	// A show's ratings, by country.
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
}

// rating is the title's rating in the region, or "" when TMDB has none for that country.
func (d details) rating(region string) string {
	if region == "" {
		region = "US"
	}
	for _, r := range d.ContentRatings.Results {
		if r.Country == region && r.Rating != "" {
			return r.Rating
		}
	}
	for _, r := range d.ReleaseDates.Results {
		if r.Country != region {
			continue
		}
		for _, release := range r.Releases {
			if release.Certification != "" {
				return release.Certification
			}
		}
	}
	return ""
}

func (c *Client) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	path, err := kindPath(ref.Kind)
	if err != nil {
		return metadata.Record{}, err
	}
	parts := "credits,release_dates,external_ids,images"
	if ref.Kind == "show" {
		parts = "aggregate_credits,content_ratings,external_ids,images"
	}
	// Pictures in the library's language, and those with no words on them.
	pictureLanguages := "null"
	if lang, _, _ := strings.Cut(language(locale), "-"); lang != "" {
		pictureLanguages = lang + ",null"
	}
	var d details
	if err := c.get(ctx, "/"+path+"/"+url.PathEscape(ref.ID), url.Values{"language": {language(locale)}, "append_to_response": {parts},
		"include_image_language": {pictureLanguages}}, &d); err != nil {
		return metadata.Record{}, err
	}
	base := d.candidate(c, ref.Kind, true)
	r := metadata.Record{Ref: base.Ref, Title: base.Title, OriginalTitle: base.OriginalTitle, Overview: d.Overview, Tagline: d.Tagline,
		Year: base.Year, ReleaseDate: d.ReleaseDate + d.FirstAirDate, ContentRating: d.rating(locale.Region), Rating: d.VoteAverage,
		Genres: names(d.Genres), Studios: names(d.Companies), Networks: names(d.Networks), Countries: names(d.Countries),
		Credits: append(d.Credits.credits(), d.WholeRun.credits()...), Continuing: d.InProduction}
	if r.OriginalTitle == r.Title {
		r.OriginalTitle = ""
	}
	// The title's own poster and backdrop first in each role, then the others TMDB has.
	for _, set := range []struct {
		role   string
		first  string
		others []image
	}{{"poster", d.PosterPath, d.Images.Posters}, {"backdrop", d.BackdropPath, d.Images.Backdrops}, {"logo", "", d.Images.Logos}} {
		if set.first != "" {
			r.Pictures = append(r.Pictures, metadata.Picture{Role: set.role, URL: c.picture(set.first)})
		}
		for _, other := range set.others[:min(len(set.others), maxPictures)] {
			if other.Path != set.first {
				r.Pictures = append(r.Pictures, metadata.Picture{Role: set.role, URL: c.picture(other.Path), Language: other.Language,
					Width: other.Width, Height: other.Height})
			}
		}
	}
	if d.ExternalIDs.IMDb != "" {
		r.OtherIDs = append(r.OtherIDs, metadata.Ref{Provider: "imdb", Kind: ref.Kind, ID: d.ExternalIDs.IMDb})
	}
	if d.ExternalIDs.TVDB != 0 {
		r.OtherIDs = append(r.OtherIDs, metadata.Ref{Provider: "tvdb", Kind: ref.Kind, ID: strconv.FormatInt(d.ExternalIDs.TVDB, 10)})
	}
	for _, season := range d.Seasons {
		r.Groups = append(r.Groups, season.Number)
	}
	if d.Collection != nil {
		collection := d.Collection.candidate(c, "collection", true)
		r.Collection = &collection
	}
	return r, nil
}

// Children answers the episodes of one season of a show.
func (c *Client) Children(ctx context.Context, parent metadata.Ref, group int, locale metadata.Locale) ([]metadata.Record, error) {
	if parent.Kind != "show" {
		return nil, fmt.Errorf("tmdb has nothing under a %q", parent.Kind)
	}
	var season struct {
		Episodes []struct {
			ID          int64   `json:"id"`
			Number      int     `json:"episode_number"`
			Name        string  `json:"name"`
			Overview    string  `json:"overview"`
			AirDate     string  `json:"air_date"`
			VoteAverage float64 `json:"vote_average"`
			StillPath   string  `json:"still_path"`
			people
		} `json:"episodes"`
	}
	path := "/tv/" + url.PathEscape(parent.ID) + "/season/" + strconv.Itoa(group)
	if err := c.get(ctx, path, url.Values{"language": {language(locale)}}, &season); err != nil {
		return nil, err
	}
	out := make([]metadata.Record, 0, len(season.Episodes))
	for _, e := range season.Episodes {
		r := metadata.Record{Ref: metadata.Ref{Provider: "tmdb", Kind: "episode", ID: strconv.FormatInt(e.ID, 10)}, Title: e.Name,
			Overview: e.Overview, Year: year(e.AirDate), ReleaseDate: e.AirDate, Rating: e.VoteAverage, Credits: e.credits(),
			Index: e.Number, ParentIndex: group}
		if e.StillPath != "" {
			r.Pictures = []metadata.Picture{{Role: "thumb", URL: c.picture(e.StillPath)}}
		}
		out = append(out, r)
	}
	return out, nil
}
