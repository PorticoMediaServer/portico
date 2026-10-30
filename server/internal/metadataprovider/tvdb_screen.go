package metadataprovider

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/text/language"
	"net/url"
	"strconv"
	"strings"
)

// tvdbCompany is one company with the relation kind its TheTVDB type maps to:
// "network", "studio", "production_company", or "company" for a distributor,
// an effects house or a company the response gives no type for.
type tvdbCompany struct {
	ID   int64
	Name string
	Kind string
}
type tvdbScreenCompanies []tvdbCompany

func tvdbCompanyKind(typeName string) string {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "network":
		return "network"
	case "studio":
		return "studio"
	case "production company", "production":
		return "production_company"
	}
	return "company"
}

// A show's companies arrive as one list with a type on each; a film's arrive
// grouped by type.
func (c *tvdbScreenCompanies) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		return nil
	}
	if len(raw) > 0 && raw[0] == '[' {
		var rows []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Type struct {
				Name string `json:"companyTypeName"`
			} `json:"companyType"`
		}
		if e := json.Unmarshal(raw, &rows); e != nil {
			return e
		}
		if len(rows) > 256 {
			return &Error{Provider: "tvdb", Code: "invalid_companies"}
		}
		out := make(tvdbScreenCompanies, 0, len(rows))
		for _, v := range rows {
			out = append(out, tvdbCompany{v.ID, v.Name, tvdbCompanyKind(v.Type.Name)})
		}
		*c = out
		return nil
	}
	var groups struct {
		Studio, Network, Production, Distributor []tmdbName
		Special                                  []tmdbName `json:"special_effects"`
	}
	if e := json.Unmarshal(raw, &groups); e != nil {
		return e
	}
	rows := tvdbScreenCompanies{}
	for _, g := range []struct {
		kind string
		rows []tmdbName
	}{{"studio", groups.Studio}, {"network", groups.Network}, {"production_company", groups.Production}, {"company", groups.Distributor}, {"company", groups.Special}} {
		for _, v := range g.rows {
			rows = append(rows, tvdbCompany{v.ID, v.Name, g.kind})
		}
	}
	if len(rows) > 256 {
		return &Error{Provider: "tvdb", Code: "invalid_companies"}
	}
	*c = rows
	return nil
}

type tvdbScreen struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Overview         string `json:"overview"`
	Year             string `json:"year"`
	FirstAired       string `json:"firstAired"`
	OriginalLanguage string `json:"originalLanguage"`
	Runtime          *int   `json:"runtime"`
	AverageRuntime   *int   `json:"averageRuntime"`
	FirstRelease     struct {
		Date string `json:"date"`
	} `json:"first_release"`
	Status struct {
		Name string `json:"name"`
	} `json:"status"`
	Aliases []struct {
		Name string `json:"name"`
	} `json:"aliases"`
	Genres    []tmdbName `json:"genres"`
	RemoteIDs []struct {
		ID         string `json:"id"`
		SourceName string `json:"sourceName"`
	} `json:"remoteIds"`
	Characters []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		PersonID   int64  `json:"peopleId"`
		PersonName string `json:"personName"`
		Type       string `json:"peopleType"`
		Sort       int    `json:"sort"`
	} `json:"characters"`
	Companies      tvdbScreenCompanies `json:"companies"`
	ContentRatings []struct {
		Name    string `json:"name"`
		Country string `json:"country"`
	} `json:"contentRatings"`
	Translations struct {
		Aliases []string `json:"aliases"`
		Name    []struct {
			Name     string `json:"name"`
			Language string `json:"language"`
		} `json:"nameTranslations"`
		Overview []struct {
			Overview string `json:"overview"`
			Language string `json:"language"`
		} `json:"overviewTranslations"`
	} `json:"translations"`
}

func (s *TVDB) SearchScreen(ctx context.Context, kind, title string, year int, locale, region string) ([]ScreenRecord, error) {
	typ := kind
	if kind == "show" {
		typ = "series"
	}
	if typ != "movie" && typ != "series" || strings.TrimSpace(title) == "" || len(title) > 512 || year < 0 || year > 9999 {
		return nil, errors.New("invalid TVDB screen query")
	}
	q := url.Values{"query": {title}, "type": {typ}, "limit": {"25"}}
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	if locale != "" {
		base, _ := language.Make(locale).Base()
		q.Set("language", base.ISO3())
	}
	var page struct {
		Status string            `json:"status"`
		Data   []SeriesCandidate `json:"data"`
	}
	if e := s.screenGet(ctx, "/search?"+q.Encode(), &page); e != nil {
		return nil, e
	}
	if page.Status != "success" || len(page.Data) > 25 {
		return nil, &Error{Provider: "tvdb", Code: "invalid_search"}
	}
	out := []ScreenRecord{}
	for _, v := range page.Data {
		if v.Type != typ {
			continue
		}
		y, _ := strconv.Atoi(v.Year)
		r := ScreenRecord{Identity: ScreenID{"tvdb", kind, v.ID}, Title: v.Name, Year: y, Overview: v.Overview, Aliases: v.Aliases}
		r = BoundScreenRecord(r)
		if e := ValidateScreenRecord(r); e != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *TVDB) ScreenDetails(ctx context.Context, kind, id, locale, region string) (ScreenRecord, error) {
	typ := kind + "s"
	if kind == "show" {
		typ = "series"
	}
	if kind != "movie" && kind != "show" || !ValidScreenID(ScreenID{"tvdb", kind, id}) {
		return ScreenRecord{}, errors.New("invalid TVDB screen identity")
	}
	var page struct {
		Status string     `json:"status"`
		Data   tvdbScreen `json:"data"`
	}
	if e := s.screenGet(ctx, "/"+typ+"/"+id+"/extended?meta=translations", &page); e != nil {
		return ScreenRecord{}, e
	}
	v := page.Data
	if page.Status != "success" || numberID(v.ID) != id {
		return ScreenRecord{}, &Error{Provider: "tvdb", Code: "identity_mismatch"}
	}
	year, _ := strconv.Atoi(v.Year)
	r := ScreenRecord{Identity: ScreenID{"tvdb", kind, id}, Title: v.Name, OriginalTitle: v.Name, Year: year, Date: v.FirstAired, Overview: v.Overview, Language: v.OriginalLanguage, Status: v.Status.Name, AdvisoryRuntimeMinutes: v.Runtime}
	if r.Date == "" {
		r.Date = v.FirstRelease.Date
	}
	if r.AdvisoryRuntimeMinutes == nil {
		r.AdvisoryRuntimeMinutes = v.AverageRuntime
	}
	originalLocale := language.Make(v.OriginalLanguage).String()
	if originalLocale == "und" {
		originalLocale = ""
	}
	r.FieldLanguages = map[string]string{"originalTitle": originalLocale, "title": originalLocale, "overview": originalLocale}
	r.Aliases = append(r.Aliases, v.Translations.Aliases...)
	if r.Year == 0 {
		r.Year = screenYear(r.Date)
	}
	base, _ := language.Make(locale).Base()
	iso := base.ISO3()
	for _, a := range v.Aliases {
		r.Aliases = append(r.Aliases, a.Name)
	}
	for _, t := range v.Translations.Name {
		r.Aliases = append(r.Aliases, t.Name)
		if t.Language == iso && t.Name != "" {
			r.Title = t.Name
			r.FieldLanguages["title"] = language.Make(t.Language).String()
		}
	}
	for _, t := range v.Translations.Overview {
		if t.Language == iso && t.Overview != "" {
			r.Overview = t.Overview
			r.FieldLanguages["overview"] = language.Make(t.Language).String()
		}
	}
	for _, g := range v.Genres {
		r.Genres = append(r.Genres, ScreenName{numberID(g.ID), g.Name})
	}
	for _, x := range v.RemoteIDs {
		var id ScreenID
		switch {
		case strings.EqualFold(x.SourceName, "TheMovieDB.com") || strings.EqualFold(x.SourceName, "The Movie Database"):
			id = ScreenID{"tmdb", kind, x.ID}
		case strings.EqualFold(x.SourceName, "IMDB"):
			id = ScreenID{"imdb", "title", x.ID}
		}
		// One crosswalk per provider, the first TVDB lists: a repeated source
		// is noise, and must not push the record past its crosswalk bound.
		if ValidScreenID(id) && !screenCrosswalkHas(r.Crosswalk, id.Provider) {
			r.Crosswalk = append(r.Crosswalk, id)
		}
	}
	for _, c := range v.Characters {
		if c.PersonID > 0 && c.PersonName != "" && len(r.Credits) < 128 {
			department := c.Type
			if strings.EqualFold(department, "Actor") {
				department = "Acting"
			}
			r.Credits = append(r.Credits, ScreenCredit{numberID(c.PersonID), c.PersonName, c.Name, department, len(r.Credits)})
		}
	}
	for _, c := range v.Companies {
		if c.ID > 0 {
			r.Relations = append(r.Relations, ScreenRelation{c.Kind, ScreenID{"tvdb", "company", numberID(c.ID)}, c.Name})
		}
	}
	for _, c := range v.ContentRatings {
		if c.Name != "" {
			r.Certifications = append(r.Certifications, ScreenCertification{c.Country, c.Name})
		}
	}
	if kind == "show" {
		for _, o := range []string{"official", "dvd", "absolute", "default", "alternate", "regional"} {
			r.Orders = append(r.Orders, ScreenOrder{o, o})
		}
	}
	r = BoundScreenRecord(r)
	return r, ValidateScreenRecord(r)
}

func screenCrosswalkHas(ids []ScreenID, provider string) bool {
	for _, id := range ids {
		if id.Provider == provider {
			return true
		}
	}
	return false
}

func (s *TVDB) screenGet(ctx context.Context, route string, out any) error {
	if isFreshRequest(ctx) {
		return s.get(ctx, route, out)
	}
	return s.screenCache.get(ctx, route, out, func() (any, error) { var raw json.RawMessage; err := s.get(ctx, route, &raw); return raw, err })
}

func (s *TVDB) ScreenDetailsFresh(ctx context.Context, kind, id, language, region string) (ScreenRecord, error) {
	return s.ScreenDetails(freshRequest(ctx), kind, id, language, region)
}
