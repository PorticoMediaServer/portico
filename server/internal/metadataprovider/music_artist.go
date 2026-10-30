package metadataprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

// The Portico Music agent's artist chain (Spec — Page Content §8): a
// MusicBrainz artist's Wikidata relation leads to the Wikipedia biography and
// the Wikimedia Commons portrait. Each host has its own transport, so each
// keeps the standard 1 request/second pacing and 4 MiB cap. Nothing here takes
// an API key, and the agent never shows per-source toggles.

// MusicArtist is what the artist page needs from MusicBrainz: the Wikidata
// id, the country and the active years.
type MusicArtist struct {
	Type       string // Group or Person from upstream; empty when unknown.
	ID         string
	Name       string
	Country    string
	BeginYear  int
	EndYear    int
	WikidataID string
}

func parseMBYear(value string) int {
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year < 1000 || year > 9999 {
		return 0
	}
	return year
}

// Artist reads one MusicBrainz artist with its URL relations.
func (s *MusicBrainz) Artist(ctx context.Context, id string) (MusicArtist, error) {
	var out MusicArtist
	if !mbid.MatchString(id) {
		return out, errors.New("invalid MusicBrainz identifier")
	}
	query := url.Values{"fmt": {"json"}, "inc": {"url-rels"}}
	var v struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Name     string `json:"name"`
		Country  string `json:"country"`
		LifeSpan struct {
			Begin string `json:"begin"`
			End   string `json:"end"`
		} `json:"life-span"`
		Relations []struct {
			Type string `json:"type"`
			URL  struct {
				Resource string `json:"resource"`
			} `json:"url"`
		} `json:"relations"`
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.http.request(ctx, "GET", "/artist/"+strings.ToLower(id)+"?"+query.Encode(), "", nil, &v); err != nil {
		return out, err
	}
	if !strings.EqualFold(v.ID, id) || strings.TrimSpace(v.Name) == "" || len(v.Name) > 2048 || len(v.Country) > 4 || len(v.Relations) > 2000 {
		return out, &Error{Provider: "musicbrainz", Code: "invalid_artist"}
	}
	out = MusicArtist{ID: v.ID, Name: strings.TrimSpace(v.Name), Country: v.Country, BeginYear: parseMBYear(v.LifeSpan.Begin), EndYear: parseMBYear(v.LifeSpan.End)}
	if v.Type == "Group" || v.Type == "Person" {
		out.Type = v.Type
	}
	for _, relation := range v.Relations {
		if !strings.EqualFold(relation.Type, "wikidata") {
			continue
		}
		u, err := url.Parse(relation.URL.Resource)
		if err != nil || u.Scheme != "https" || u.Host != "www.wikidata.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			continue
		}
		qid := strings.TrimPrefix(u.EscapedPath(), "/wiki/")
		if len(qid) > 32 || len(qid) < 2 || qid[0] != 'Q' {
			continue
		}
		valid := true
		for _, r := range qid[1:] {
			if r < '0' || r > '9' {
				valid = false
			}
		}
		if valid && qid[1] != '0' {
			out.WikidataID = qid
			break
		}
	}
	return out, nil
}

// WikidataArtist is what the artist page needs from Wikidata: the English
// Wikipedia title and the Commons portrait file.
type WikidataArtist struct {
	ID             string
	WikipediaTitle string
	ImageFile      string
}

// Wikidata reads entity data (sitelinks, portrait claims) without credentials.
type Wikidata struct{ http *transport }

var wikidataTransport = newTransport("wikidata", "https://www.wikidata.org", "Portico/0.1 (https://getportico.tv)")

func NewWikidata() *Wikidata { return &Wikidata{http: wikidataTransport} }

func validQID(id string) bool {
	if len(id) < 2 || len(id) > 32 || id[0] != 'Q' || id[1] == '0' {
		return false
	}
	for _, r := range id[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// EntityArtist reads one Wikidata entity's English sitelink and portrait file.
func (s *Wikidata) EntityArtist(ctx context.Context, id string) (WikidataArtist, error) {
	out := WikidataArtist{ID: id}
	if !validQID(id) {
		return out, errors.New("invalid Wikidata identifier")
	}
	var v struct {
		Entities map[string]struct {
			ID        string `json:"id"`
			Sitelinks map[string]struct {
				Title string `json:"title"`
			} `json:"sitelinks"`
			Claims map[string][]struct {
				Mainsnak struct {
					Snaktype  string          `json:"snaktype"`
					Datavalue json.RawMessage `json:"datavalue"`
				} `json:"mainsnak"`
			} `json:"claims"`
		} `json:"entities"`
	}
	if err := s.http.request(ctx, "GET", "/wiki/Special:EntityData/"+id+".json", "", nil, &v); err != nil {
		return out, err
	}
	entity, ok := v.Entities[id]
	if !ok || entity.ID != id {
		return out, &Error{Provider: "wikidata", Code: "invalid_entity"}
	}
	if link, ok := entity.Sitelinks["enwiki"]; ok && strings.TrimSpace(link.Title) != "" && len(link.Title) <= 512 {
		out.WikipediaTitle = strings.TrimSpace(link.Title)
	}
	for _, claim := range entity.Claims["P18"] {
		if claim.Mainsnak.Snaktype != "value" || len(claim.Mainsnak.Datavalue) == 0 {
			continue
		}
		var value struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(claim.Mainsnak.Datavalue, &value); err != nil {
			continue
		}
		name := strings.TrimSpace(value.Value)
		if name == "" || len(name) > 512 || strings.ContainsAny(name, "/\\") {
			continue
		}
		hasControl := false
		for _, r := range name {
			if r < 32 || r == 127 {
				hasControl = true
			}
		}
		if !hasControl {
			out.ImageFile = name
			break
		}
	}
	return out, nil
}

// WikipediaSummary is an article's plain-text extract with its canonical URL.
type WikipediaSummary struct {
	Title       string
	Extract     string
	Description string
	PageURL     string
}

// Wikipedia reads article summaries (CC BY-SA text, attributed on the page).
type Wikipedia struct{ http *transport }

var wikipediaTransport = newTransport("wikipedia", "https://en.wikipedia.org", "Portico/0.1 (https://getportico.tv)")

func NewWikipedia() *Wikipedia { return &Wikipedia{http: wikipediaTransport} }

// Summary reads one English article's summary. A 404 means the article does
// not exist, which the caller treats as "no biography", not a failure.
func (s *Wikipedia) Summary(ctx context.Context, title string) (WikipediaSummary, error) {
	var out WikipediaSummary
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 512 {
		return out, errors.New("invalid Wikipedia title")
	}
	var v struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		Description string `json:"description"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	if err := s.http.request(ctx, "GET", "/api/rest_v1/page/summary/"+url.PathEscape(title), "", nil, &v); err != nil {
		return out, err
	}
	u, err := url.Parse(v.ContentURLs.Desktop.Page)
	if err != nil || u.Scheme != "https" || u.Host != "en.wikipedia.org" || !strings.HasPrefix(u.EscapedPath(), "/wiki/") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return out, &Error{Provider: "wikipedia", Code: "invalid_summary"}
	}
	if strings.TrimSpace(v.Title) == "" || len(v.Title) > 512 || len(v.Description) > 512 {
		return out, &Error{Provider: "wikipedia", Code: "invalid_summary"}
	}
	out = WikipediaSummary{Title: strings.TrimSpace(v.Title), Extract: v.Extract, Description: v.Description, PageURL: v.ContentURLs.Desktop.Page}
	return out, nil
}

// CommonsImage is a file's direct URL with its short licence name.
type CommonsImage struct {
	FileURL string
	Licence string
}

// Commons reads file URLs and licences from Wikimedia Commons.
type Commons struct{ http *transport }

var commonsTransport = newTransport("commons", "https://commons.wikimedia.org", "Portico/0.1 (https://getportico.tv)")

func NewCommons() *Commons { return &Commons{http: commonsTransport} }

// ImageInfo resolves one File: name to its direct URL and short licence. A
// missing file, or one without licence metadata, is "no image", not a failure.
func (s *Commons) ImageInfo(ctx context.Context, filename string) (CommonsImage, error) {
	var out CommonsImage
	filename = strings.TrimSpace(filename)
	if filename == "" || len(filename) > 512 || strings.ContainsAny(filename, "/\\") {
		return out, errors.New("invalid Commons filename")
	}
	query := url.Values{"action": {"query"}, "format": {"json"}, "formatversion": {"2"}, "prop": {"imageinfo"}, "iiprop": {"url|extmetadata"}, "titles": {"File:" + filename}}
	var v struct {
		Query struct {
			Pages []struct {
				Missing   bool `json:"missing"`
				Imageinfo []struct {
					URL         string `json:"url"`
					Extmetadata map[string]struct {
						Value string `json:"value"`
					} `json:"extmetadata"`
				} `json:"imageinfo"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := s.http.request(ctx, "GET", "/w/api.php?"+query.Encode(), "", nil, &v); err != nil {
		return out, err
	}
	if len(v.Query.Pages) != 1 || v.Query.Pages[0].Missing || len(v.Query.Pages[0].Imageinfo) == 0 {
		return out, nil
	}
	info := v.Query.Pages[0].Imageinfo[0]
	u, err := url.Parse(info.URL)
	if err != nil || u.Scheme != "https" || u.Host != "upload.wikimedia.org" || !strings.HasPrefix(u.EscapedPath(), "/wikipedia/commons/") || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return out, &Error{Provider: "commons", Code: "invalid_image"}
	}
	licence := strings.TrimSpace(info.Extmetadata["LicenseShortName"].Value)
	if licence == "" {
		licence = strings.TrimSpace(info.Extmetadata["License"].Value)
	}
	if licence == "" || len(licence) > 128 {
		return out, nil
	}
	for _, r := range licence {
		if r < 32 || r == 127 {
			return out, nil
		}
	}
	out = CommonsImage{FileURL: info.URL, Licence: licence}
	return out, nil
}
