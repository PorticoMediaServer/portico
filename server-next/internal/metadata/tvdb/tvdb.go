// Package tvdb reads movies and shows using the owner's TheTVDB API key.
package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

const Origin = "https://api4.thetvdb.com/v4"
const Every = 500 * time.Millisecond

// Client shares a login and request allowance across libraries. A changed key gets a new client.
type Client struct {
	key, origin string
	http        *fetch.Client
	mu          sync.Mutex
	token       string
	expires     time.Time
}

func New(key, origin string, every time.Duration) *Client {
	return &Client{key: key, origin: strings.TrimRight(origin, "/"), http: fetch.New("tvdb", every, 30*time.Second)}
}
func (*Client) Name() string { return "tvdb" }

func (c *Client) access(ctx context.Context, invalid string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == invalid {
		c.token = ""
	}
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	if c.key == "" {
		return "", metadata.StatusError{Provider: "tvdb", Status: 401}
	}
	body, _ := json.Marshal(map[string]string{"apikey": c.key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+"/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	var answer struct {
		Status string
		Data   struct{ Token string }
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", err
	}
	if answer.Status != "success" || len(answer.Data.Token) < 8 || len(answer.Data.Token) > 8192 {
		return "", metadata.StatusError{Provider: "tvdb", Status: 502}
	}
	for _, r := range answer.Data.Token {
		if r < 33 || r > 126 {
			return "", metadata.StatusError{Provider: "tvdb", Status: 502}
		}
	}
	c.token, c.expires = answer.Data.Token, time.Now().Add(27*24*time.Hour)
	return c.token, nil
}

func (c *Client) get(ctx context.Context, route string, out any) (string, error) {
	invalid := ""
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.access(ctx, invalid)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+route, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		raw, err := c.http.Do(req)
		var status *fetch.Status
		if attempt == 0 && errors.As(err, &status) && status.Code == 401 {
			invalid = token
			continue
		}
		if err != nil {
			return "", err
		}
		var answer struct {
			Status string
			Data   json.RawMessage
			Links  struct{ Next string }
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			return "", err
		}
		if answer.Status != "success" {
			return "", metadata.StatusError{Provider: "tvdb", Status: 502}
		}
		if len(answer.Data) == 0 || string(answer.Data) == "null" {
			return "", metadata.ErrNotFound
		}
		return answer.Links.Next, json.Unmarshal(answer.Data, out)
	}
	return "", metadata.StatusError{Provider: "tvdb", Status: 401}
}

func kindPath(kind string) string {
	switch kind {
	case "movie":
		return "movies"
	case "show":
		return "series"
	}
	return ""
}
func localeLanguage(locale metadata.Locale) string {
	if locale.Language == "" {
		return "eng"
	}
	base, _ := language.Make(locale.Language).Base()
	return base.ISO3()
}
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
}
func (c *Client) picture(address string) string {
	if strings.HasPrefix(address, "/banners/") {
		address = "https://artworks.thetvdb.com" + address
	}
	if strings.HasPrefix(address, "https://artworks.thetvdb.com/") || c.origin != Origin && strings.HasPrefix(address, strings.TrimSuffix(c.origin, "/v4")+"/") {
		return address
	}
	return ""
}

func (c *Client) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	typ := kindPath(q.Kind)
	if typ == "" {
		return nil, metadata.ErrNotFound
	}
	if typ == "movies" {
		typ = "movie"
	}
	params := url.Values{"type": {typ}, "limit": {"10"}}
	exact := false
	for _, id := range q.IDs {
		if id.Provider == "imdb" && imdbID.MatchString(id.ID) {
			params.Set("remote_id", id.ID)
			exact = true
			break
		}
	}
	if !exact {
		if strings.TrimSpace(q.Title) == "" {
			return nil, nil
		}
		params.Set("query", q.Title)
		if q.Year > 0 {
			params.Set("year", strconv.Itoa(q.Year))
		}
	}
	var found []struct {
		TVDBID                     string `json:"tvdb_id"`
		Name, Year, Overview, Type string
		Image                      string `json:"image_url"`
		Translations, Overviews    map[string]string
	}
	if _, err := c.get(ctx, "/search?"+params.Encode(), &found); err != nil {
		return nil, err
	}
	var out []metadata.Candidate
	for _, v := range found[:min(len(found), 10)] {
		id, err := strconv.ParseInt(v.TVDBID, 10, 64)
		if err != nil || id <= 0 || v.Type != typ {
			continue
		}
		title, overview := v.Name, v.Overview
		if translated := v.Translations[localeLanguage(q.Locale)]; translated != "" {
			title = translated
		}
		if translated := v.Overviews[localeLanguage(q.Locale)]; translated != "" {
			overview = translated
		}
		out = append(out, metadata.Candidate{Ref: metadata.Ref{Provider: "tvdb", Kind: q.Kind, ID: v.TVDBID}, Title: title,
			Year: year(v.Year), Overview: overview, PosterURL: c.picture(v.Image), Exact: exact})
	}
	return out, nil
}

type named struct{ Name string }
type screen struct {
	ID                                                       int64
	Name, Overview, Year, FirstAired, Image, OriginalCountry string
	FirstRelease                                             struct{ Date string } `json:"first_release"`
	Status                                                   struct{ Name string }
	Genres                                                   []named
	OriginalNetwork                                          named
	Companies                                                json.RawMessage
	RemoteIDs                                                []struct{ ID, SourceName string }
	ContentRatings                                           []struct{ Name, Country string }
	Translations                                             struct {
		Names     []struct{ Name, Language string }     `json:"nameTranslations"`
		Overviews []struct{ Overview, Language string } `json:"overviewTranslations"`
	}
	Characters []struct {
		PeopleID                     int64
		PersonName, Name, PeopleType string
		Sort                         int
	}
	Artworks []struct {
		Type            int
		Image, Language string
		Width, Height   int
	}
	Seasons []struct{ Number int }
}

var imdbID = regexp.MustCompile(`^tt[0-9]{5,10}$`)
var slug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (c *Client) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	path := kindPath(ref.Kind)
	if path == "" || len(ref.ID) > 200 {
		return metadata.Record{}, metadata.ErrNotFound
	}
	id, err := strconv.ParseInt(ref.ID, 10, 64)
	if err != nil {
		if !slug.MatchString(ref.ID) {
			return metadata.Record{}, metadata.ErrNotFound
		}
		var base struct{ ID int64 }
		if _, err := c.get(ctx, "/"+path+"/slug/"+url.PathEscape(ref.ID), &base); err != nil {
			return metadata.Record{}, err
		}
		id = base.ID
	}
	if id <= 0 {
		return metadata.Record{}, metadata.ErrNotFound
	}
	var w screen
	if _, err := c.get(ctx, fmt.Sprintf("/%s/%d/extended?meta=translations", path, id), &w); err != nil {
		return metadata.Record{}, err
	}
	if w.ID != id {
		return metadata.Record{}, metadata.StatusError{Provider: "tvdb", Status: 502}
	}
	r := metadata.Record{Ref: metadata.Ref{Provider: "tvdb", Kind: ref.Kind, ID: strconv.FormatInt(id, 10)}, Title: w.Name, OriginalTitle: w.Name,
		Overview: w.Overview, Year: year(w.Year), ReleaseDate: w.FirstAired, Continuing: strings.EqualFold(w.Status.Name, "Continuing")}
	if r.ReleaseDate == "" {
		r.ReleaseDate = w.FirstRelease.Date
	}
	if r.Year == 0 {
		r.Year = year(r.ReleaseDate)
	}
	lang := localeLanguage(locale)
	for _, v := range w.Translations.Names {
		if v.Language == lang && v.Name != "" {
			r.Title = v.Name
			break
		}
	}
	for _, v := range w.Translations.Overviews {
		if v.Language == lang && v.Overview != "" {
			r.Overview = v.Overview
			break
		}
	}
	region := locale.Region
	if region == "" {
		region = "US"
	}
	country, _ := language.ParseRegion(region)
	for _, rating := range w.ContentRatings {
		if strings.EqualFold(rating.Country, region) || strings.EqualFold(rating.Country, country.ISO3()) {
			r.ContentRating = rating.Name
			break
		}
	}
	for _, genre := range w.Genres {
		r.Genres = append(r.Genres, genre.Name)
	}
	if country, err := language.ParseRegion(w.OriginalCountry); err == nil {
		r.Countries = []string{country.String()}
	}
	if w.OriginalNetwork.Name != "" {
		r.Networks = append(r.Networks, w.OriginalNetwork.Name)
	}
	// TVDB groups film companies, but gives show companies as one typed list.
	var companies []struct {
		Name string
		Type struct {
			Name string `json:"companyTypeName"`
		} `json:"companyType"`
	}
	if json.Unmarshal(w.Companies, &companies) == nil {
		for _, v := range companies {
			if strings.EqualFold(v.Type.Name, "network") {
				if v.Name != w.OriginalNetwork.Name {
					r.Networks = append(r.Networks, v.Name)
				}
			} else {
				r.Studios = append(r.Studios, v.Name)
			}
		}
	} else {
		var groups struct{ Studio, Network, Production []named }
		if json.Unmarshal(w.Companies, &groups) == nil {
			for _, v := range append(groups.Studio, groups.Production...) {
				r.Studios = append(r.Studios, v.Name)
			}
			for _, v := range groups.Network {
				r.Networks = append(r.Networks, v.Name)
			}
		}
	}
	roles := map[string]string{"actor": "actor", "guest star": "guest", "director": "director", "writer": "writer", "producer": "producer", "composer": "composer"}
	sort.SliceStable(w.Characters, func(i, j int) bool { return w.Characters[i].Sort < w.Characters[j].Sort })
	for _, v := range w.Characters[:min(len(w.Characters), 50)] {
		if role := roles[strings.ToLower(v.PeopleType)]; role != "" && v.PeopleID > 0 {
			r.Credits = append(r.Credits, metadata.Credit{Person: metadata.Ref{Provider: "tvdb", Kind: "person", ID: strconv.FormatInt(v.PeopleID, 10)}, Name: v.PersonName, Role: role, Character: v.Name})
		}
	}
	for _, v := range w.RemoteIDs {
		provider := ""
		if strings.EqualFold(v.SourceName, "IMDB") {
			provider = "imdb"
		}
		if strings.EqualFold(v.SourceName, "TheMovieDB.com") || strings.EqualFold(v.SourceName, "The Movie Database") {
			provider = "tmdb"
		}
		if provider != "" {
			r.OtherIDs = append(r.OtherIDs, metadata.Ref{Provider: provider, Kind: ref.Kind, ID: v.ID})
		}
	}
	if image := c.picture(w.Image); image != "" {
		r.Pictures = append(r.Pictures, metadata.Picture{Role: "poster", URL: image})
	}
	if len(w.Artworks) > 0 {
		var types []struct {
			ID   int
			Name string
		}
		if _, err := c.get(ctx, "/artwork/types", &types); err != nil {
			return metadata.Record{}, err
		}
		if len(types) > 128 {
			return metadata.Record{}, metadata.StatusError{Provider: "tvdb", Status: 502}
		}
		byType := map[int]string{}
		for _, v := range types {
			name := strings.ToLower(v.Name)
			for word, role := range map[string]string{"poster": "poster", "background": "backdrop", "logo": "logo", "banner": "banner"} {
				if strings.Contains(name, word) {
					byType[v.ID] = role
					break
				}
			}
		}
		counts := map[string]int{}
		for _, v := range w.Artworks {
			role, image := byType[v.Type], c.picture(v.Image)
			if role != "" && image != "" && counts[role] < 20 {
				counts[role]++
				r.Pictures = append(r.Pictures, metadata.Picture{Role: role, URL: image, Language: v.Language, Width: v.Width, Height: v.Height})
			}
		}
	}
	for _, v := range w.Seasons {
		r.Groups = append(r.Groups, v.Number)
	}
	return r, nil
}

// Children returns the requested season (all episodes for absolute numbering), with a hard
// bound on pages and records read. Next links are never fetched; requests are built here.
func (c *Client) Children(ctx context.Context, parent metadata.Ref, group int, locale metadata.Locale) ([]metadata.Record, error) {
	id, err := strconv.ParseInt(parent.ID, 10, 64)
	if err != nil || id <= 0 || parent.Kind != "show" || group < 0 || group > 9999 {
		return nil, metadata.ErrNotFound
	}
	order := "official"
	switch parent.Order {
	case metadata.OrderAired:
	case metadata.OrderDVD:
		order = "dvd"
	case metadata.OrderAbsolute:
		order = "absolute"
	default:
		return nil, metadata.ErrNoOrder
	}
	if parent.Order == metadata.OrderAbsolute && group != 1 {
		return nil, metadata.ErrNotFound
	}
	var out []metadata.Record
	seen := map[int64]bool{}
	for page := 0; page < 20; page++ {
		params := url.Values{"page": {strconv.Itoa(page)}}
		if parent.Order != metadata.OrderAbsolute {
			params.Set("season", strconv.Itoa(group))
		}
		var answer struct {
			Series   struct{ ID int64 }
			Episodes []struct {
				ID, SeriesID                         int64
				Name, Overview, Aired, Image         string
				Number, SeasonNumber, AbsoluteNumber *int
			}
		}
		route := fmt.Sprintf("/series/%d/episodes/%s/%s?%s", id, order, localeLanguage(locale), params.Encode())
		next, err := c.get(ctx, route, &answer)
		if err != nil {
			return nil, err
		}
		if answer.Series.ID != id || len(seen)+len(answer.Episodes) > 1000 {
			return nil, metadata.StatusError{Provider: "tvdb", Status: 502}
		}
		for _, e := range answer.Episodes {
			if e.ID <= 0 || e.SeriesID != id || seen[e.ID] {
				return nil, metadata.StatusError{Provider: "tvdb", Status: 502}
			}
			seen[e.ID] = true
			if parent.Order != metadata.OrderAbsolute && (e.SeasonNumber == nil || *e.SeasonNumber != group) {
				continue
			}
			number := e.Number
			if parent.Order == metadata.OrderAbsolute && e.AbsoluteNumber != nil {
				number = e.AbsoluteNumber
			}
			if number == nil || *number < 1 || *number > 9999 {
				continue
			}
			r := metadata.Record{Ref: metadata.Ref{Provider: "tvdb", Kind: "episode", ID: strconv.FormatInt(e.ID, 10)},
				Title: e.Name, Overview: e.Overview, ReleaseDate: e.Aired, Year: year(e.Aired), Index: *number, ParentIndex: group}
			if image := c.picture(e.Image); image != "" {
				r.Pictures = []metadata.Picture{{Role: "thumb", URL: image}}
			}
			out = append(out, r)
		}
		if next == "" {
			if len(out) == 0 && parent.Order != metadata.OrderAired {
				return nil, metadata.ErrNoOrder
			}
			return out, nil
		}
	}
	return nil, metadata.StatusError{Provider: "tvdb", Status: 502}
}
