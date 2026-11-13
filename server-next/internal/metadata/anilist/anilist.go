// Package anilist reads anime work metadata from AniList's public GraphQL API.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

const Origin = "https://graphql.anilist.co"
const Every = 2100 * time.Millisecond

// Client is shared by all AniList libraries so they share one request allowance.
type Client struct {
	origin string
	http   *fetch.Client
}

func New(origin string, every time.Duration) *Client {
	return &Client{origin: origin, http: fetch.New("anilist", every, 30*time.Second)}
}

func (*Client) Name() string { return "anilist" }

func (c *Client) query(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.http.Do(req)
	if err != nil {
		return err
	}
	var envelope struct {
		Data   json.RawMessage
		Errors []struct{ Status int }
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		status := envelope.Errors[0].Status
		if status == http.StatusNotFound {
			return metadata.ErrNotFound
		}
		if status < 400 {
			status = http.StatusBadGateway
		}
		return metadata.StatusError{Provider: "anilist", Status: status}
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return metadata.ErrNotFound
	}
	return json.Unmarshal(envelope.Data, out)
}

const summaryFields = `id format isAdult title { english romaji native } description(asHtml:false)
startDate { year month day } seasonYear popularity coverImage { extraLarge large } bannerImage`

const detailFields = summaryFields + ` status averageScore genres countryOfOrigin
studios(isMain:true) { nodes { name } }
staff(perPage:25) { edges { role node { id name { full } } } }`

type work struct {
	ID              int
	Format          string
	IsAdult         bool
	Title           struct{ English, Romaji, Native string }
	Description     string
	StartDate       struct{ Year, Month, Day int }
	SeasonYear      int
	Popularity      float64
	CoverImage      struct{ ExtraLarge, Large string }
	BannerImage     string
	Status          string
	AverageScore    float64
	Genres          []string
	CountryOfOrigin string
	Studios         struct{ Nodes []struct{ Name string } }
	Staff           struct {
		Edges []struct {
			Role string
			Node struct {
				ID   int
				Name struct{ Full string }
			}
		}
	}
}

var markup = regexp.MustCompile(`(?s)<[^>]*>`)

func (w work) candidate(c *Client, kind string, locale metadata.Locale, exact bool) metadata.Candidate {
	title := w.Title.Romaji
	if strings.HasPrefix(locale.Language, "en") || locale.Language == "" {
		title = w.Title.English
	} else if strings.HasPrefix(locale.Language, "ja") {
		title = w.Title.Native
	}
	for _, fallback := range []string{w.Title.Romaji, w.Title.English, w.Title.Native} {
		if title == "" {
			title = fallback
		}
	}
	year := w.StartDate.Year
	if year == 0 {
		year = w.SeasonYear
	}
	poster := w.CoverImage.ExtraLarge
	if poster == "" {
		poster = w.CoverImage.Large
	}
	return metadata.Candidate{Ref: metadata.Ref{Provider: "anilist", Kind: kind, ID: strconv.Itoa(w.ID)},
		Title: title, OriginalTitle: w.Title.Native, Year: year, Overview: html.UnescapeString(markup.ReplaceAllString(w.Description, "")),
		PosterURL: c.picture(poster), Popularity: w.Popularity, Exact: exact}
}

func (w work) fits(kind string) bool {
	return w.ID > 0 && (kind == "movie" && w.Format == "MOVIE" || kind == "show" && w.Format != "MOVIE" && w.Format != "MUSIC")
}

func (c *Client) Search(ctx context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil // AniList does not translate another provider's id.
	}
	var adult any
	if !q.IncludeAdult {
		adult = false
	}
	var answer struct{ Page struct{ Media []work } }
	query := `query($search:String!,$adult:Boolean){Page(page:1,perPage:10){media(type:ANIME,search:$search,isAdult:$adult,sort:SEARCH_MATCH){` + summaryFields + `}}}`
	if err := c.query(ctx, query, map[string]any{"search": q.Title, "adult": adult}, &answer); err != nil {
		return nil, err
	}
	var out []metadata.Candidate
	for _, w := range answer.Page.Media[:min(len(answer.Page.Media), 10)] {
		if w.fits(q.Kind) && (!w.IsAdult || q.IncludeAdult) {
			out = append(out, w.candidate(c, q.Kind, q.Locale, false))
		}
	}
	return out, nil
}

func (c *Client) Details(ctx context.Context, ref metadata.Ref, locale metadata.Locale) (metadata.Record, error) {
	id, err := strconv.ParseInt(ref.ID, 10, 32)
	if err != nil || id <= 0 || ref.Kind != "movie" && ref.Kind != "show" {
		return metadata.Record{}, metadata.ErrNotFound
	}
	var answer struct{ Media *work }
	query := `query($id:Int!){Media(id:$id,type:ANIME){` + detailFields + `}}`
	if err := c.query(ctx, query, map[string]any{"id": id}, &answer); err != nil {
		return metadata.Record{}, err
	}
	w := answer.Media
	if w == nil || w.ID != int(id) || !w.fits(ref.Kind) {
		return metadata.Record{}, metadata.ErrNotFound
	}
	base := w.candidate(c, ref.Kind, locale, true)
	r := metadata.Record{Ref: base.Ref, Title: base.Title, OriginalTitle: base.OriginalTitle, Overview: base.Overview,
		Year: base.Year, Rating: w.AverageScore / 10, Genres: w.Genres,
		Continuing: w.Status == "RELEASING" || w.Status == "NOT_YET_RELEASED" || w.Status == "HIATUS"}
	if date := w.StartDate; date.Year > 0 && date.Month > 0 && date.Day > 0 {
		text := fmt.Sprintf("%04d-%02d-%02d", date.Year, date.Month, date.Day)
		if _, err := time.Parse("2006-01-02", text); err == nil {
			r.ReleaseDate = text
		}
	}
	if w.CountryOfOrigin != "" {
		r.Countries = []string{w.CountryOfOrigin}
	}
	for _, studio := range w.Studios.Nodes {
		r.Studios = append(r.Studios, studio.Name)
	}
	roles := map[string]string{"Director": "director", "Series Composition": "writer", "Script": "writer", "Music": "composer", "Producer": "producer"}
	for _, edge := range w.Staff.Edges[:min(len(w.Staff.Edges), 25)] {
		if role := roles[edge.Role]; role != "" {
			r.Credits = append(r.Credits, metadata.Credit{Person: metadata.Ref{Provider: "anilist", Kind: "person", ID: strconv.Itoa(edge.Node.ID)},
				Name: edge.Node.Name.Full, Role: role})
		}
	}
	if base.PosterURL != "" {
		r.Pictures = append(r.Pictures, metadata.Picture{Role: "poster", URL: base.PosterURL})
	}
	if banner := c.picture(w.BannerImage); banner != "" {
		r.Pictures = append(r.Pictures, metadata.Picture{Role: "backdrop", URL: banner})
	}
	return r, nil
}

// Pictures come from AniList's CDN, or the same origin as a test's stand-in.
func (c *Client) picture(address string) string {
	if strings.HasPrefix(address, "https://s4.anilist.co/") || c.origin != Origin && strings.HasPrefix(address, strings.TrimRight(c.origin, "/")+"/") {
		return address
	}
	return ""
}

// AniList identifies works, not episodes. Preserve the local episode facts and identities.
func (*Client) Children(_ context.Context, parent metadata.Ref, _ int, _ metadata.Locale) ([]metadata.Record, error) {
	if parent.Order != metadata.OrderAired {
		return nil, metadata.ErrNoOrder
	}
	return nil, metadata.ErrNotFound
}
