package metadataprovider

// ScreenRecord is provider-neutral descriptive evidence. It never changes measured
// duration, source identity, local episode coordinates, or playback timelines.
import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type ScreenID struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	ID       string `json:"id"`
}
type ScreenName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ScreenCredit struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Department string `json:"department"`
	Ordinal    int    `json:"ordinal"`
}
type ScreenRelation struct {
	Kind   string   `json:"kind"`
	Target ScreenID `json:"target"`
	Name   string   `json:"name,omitempty"`
}
type ScreenOrder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ScreenCoordinates struct {
	ShowID          string `json:"showId"`
	Season          *int   `json:"season,omitempty"`
	Episode         int    `json:"episode"`
	Absolute        *int   `json:"absolute,omitempty"`
	Order           string `json:"order"`
	OriginalSeason  *int   `json:"originalSeason,omitempty"`
	OriginalEpisode *int   `json:"originalEpisode,omitempty"`
}
type ScreenRating struct {
	Value float64 `json:"value"`
	Scale int     `json:"scale"`
	Votes int     `json:"votes"`
}
type ScreenCertification struct {
	Region string `json:"region"`
	Rating string `json:"rating"`
}
type ScreenRecord struct {
	FieldLanguages         map[string]string     `json:"fieldLanguages,omitempty"`
	Skipped                []string              `json:"skipped,omitempty"`
	Identity               ScreenID              `json:"identity"`
	Title                  string                `json:"title"`
	OriginalTitle          string                `json:"originalTitle,omitempty"`
	Aliases                []string              `json:"aliases,omitempty"`
	Year                   int                   `json:"year,omitempty"`
	Date                   string                `json:"date,omitempty"`
	Overview               string                `json:"overview,omitempty"`
	Tagline                string                `json:"tagline,omitempty"`
	Language               string                `json:"language,omitempty"`
	Status                 string                `json:"status,omitempty"`
	Format                 string                `json:"format,omitempty"`
	SourceMaterial         string                `json:"sourceMaterial,omitempty"`
	Season                 string                `json:"season,omitempty"`
	EpisodeCount           *int                  `json:"episodeCount,omitempty"`
	AdvisoryRuntimeMinutes *int                  `json:"advisoryRuntimeMinutes,omitempty"`
	Adult                  *bool                 `json:"adult,omitempty"`
	Genres                 []ScreenName          `json:"genres,omitempty"`
	Credits                []ScreenCredit        `json:"credits,omitempty"`
	Relations              []ScreenRelation      `json:"relations,omitempty"`
	Crosswalk              []ScreenID            `json:"crosswalk,omitempty"`
	Orders                 []ScreenOrder         `json:"orders,omitempty"`
	Coordinates            *ScreenCoordinates    `json:"coordinates,omitempty"`
	Rating                 *ScreenRating         `json:"rating,omitempty"`
	Certifications         []ScreenCertification `json:"certifications,omitempty"`
	// Tags are typed theme/keyword evidence, scoped to the record's provider.
	// TMDB keywords and AniList non-spoiler themes are the supported sources;
	// the projection keeps them distinct from genre vocabulary.
	Tags []ScreenName `json:"tags,omitempty"`
	// Similar is the provider's own ranked similar-title list for a work (TMDB
	// recommendations, AniList recommendations), best first. Targets are works
	// of the record's provider; they are links by provider id, not evidence
	// about this title, so they never reach its descriptive fields.
	Similar []ScreenID `json:"similar,omitempty"`
	// Remote artwork is retained as evidence only. Selection/acquisition belongs to
	// the artwork service, not to matching or an interactive image response.
	PosterPath   string `json:"posterPath,omitempty"`
	BackdropPath string `json:"backdropPath,omitempty"`
	// StillPath is an episode's still (TMDB still_path). Only episode records
	// carry it; it seeds the episode's own still candidate, never inherited art.
	StillPath string `json:"stillPath,omitempty"`
}

// MaxScreenSimilar bounds one record's similar-title list.
const MaxScreenSimilar = 32

func ValidScreenID(v ScreenID) bool {
	if v.Provider == "imdb" {
		// IMDb is only ever a crosswalk: a title id as IMDb writes it.
		return v.Type == "title" && validIMDbID(v.ID)
	}
	n, err := strconv.ParseInt(v.ID, 10, 64)
	if err != nil || n <= 0 || n > 9007199254740991 || strconv.FormatInt(n, 10) != v.ID {
		return false
	}
	switch v.Provider {
	case "tmdb", "tvdb":
		return v.Type == "movie" || v.Type == "show" || v.Type == "episode" || v.Type == "season" || v.Type == "person" || v.Type == "collection" || v.Type == "company"
	case "anilist":
		return n <= 2147483647 && (v.Type == "anime" || v.Type == "studio" || v.Type == "character" || v.Type == "person")
	}
	return false
}
func validIMDbID(id string) bool {
	if len(id) < 9 || len(id) > 12 || !strings.HasPrefix(id, "tt") {
		return false
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// appendSimilar adds one similar-title target in provider order: a valid work
// of the record's provider, never the record itself, never twice, and never
// past the cap. A malformed entry is dropped on its own, never the record.
func appendSimilar(list []ScreenID, self, target ScreenID) []ScreenID {
	if len(list) >= MaxScreenSimilar || target == self || target.Provider != self.Provider || !screenWorkType(target.Type) || !ValidScreenID(target) {
		return list
	}
	for _, id := range list {
		if id == target {
			return list
		}
	}
	return append(list, target)
}
func screenWorkType(t string) bool { return t == "movie" || t == "show" || t == "anime" }
func ScreenSourceURL(v ScreenID) string {
	if !ValidScreenID(v) {
		return ""
	}
	switch v.Provider {
	case "tmdb":
		kind := v.Type
		if kind == "show" {
			kind = "tv"
		}
		if kind == "movie" || kind == "tv" || kind == "person" || kind == "collection" {
			return "https://www.themoviedb.org/" + kind + "/" + v.ID
		}
	case "tvdb":
		kind := map[string]string{"movie": "movie", "show": "series", "episode": "episode"}[v.Type]
		if kind != "" {
			return "https://thetvdb.com/dereferrer/" + kind + "/" + v.ID
		}
	case "anilist":
		if v.Type == "anime" {
			return "https://anilist.co/anime/" + v.ID
		}
	}
	return ""
}
func cleanScreenText(s string, max int) bool {
	if len(s) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}
func ValidateScreenRecord(v ScreenRecord) error {
	fail := func() error { return &Error{Provider: v.Identity.Provider, Code: "invalid_screen_evidence"} }
	if !ValidScreenID(v.Identity) || strings.TrimSpace(v.Title) == "" || !cleanScreenText(v.Title, 2048) || !cleanScreenText(v.OriginalTitle, 2048) || !cleanScreenText(v.Overview, 65536) || !cleanScreenText(v.Tagline, 4096) || v.Year < 0 || v.Year > 9999 {
		return fail()
	}
	if len(v.FieldLanguages) > 32 {
		return fail()
	}
	for field, locale := range v.FieldLanguages {
		if !cleanScreenText(field, 128) || !cleanScreenText(locale, 64) {
			return fail()
		}
	}
	if len(v.Skipped) > 16 {
		return fail()
	}
	for _, issue := range v.Skipped {
		if !cleanScreenText(issue, 128) {
			return fail()
		}
	}
	if len(v.Aliases) > 256 || len(v.Genres) > 64 || len(v.Credits) > 128 || len(v.Relations) > 256 || len(v.Crosswalk) > 16 || len(v.Orders) > 64 || len(v.Certifications) > 100 || len(v.Tags) > 64 {
		return fail()
	}
	for _, n := range v.Tags {
		if !cleanScreenText(n.Name, 200) || len(n.ID) > 128 {
			return fail()
		}
	}
	for _, value := range []string{v.Language, v.Status, v.Format, v.SourceMaterial, v.Season, v.Date} {
		if !cleanScreenText(value, 64) {
			return fail()
		}
	}
	if !cleanScreenText(v.PosterPath, 2048) || !cleanScreenText(v.BackdropPath, 2048) || !cleanScreenText(v.StillPath, 2048) {
		return fail()
	}
	if v.AdvisoryRuntimeMinutes != nil && (*v.AdvisoryRuntimeMinutes < 0 || *v.AdvisoryRuntimeMinutes > 1000000) {
		return fail()
	}
	for _, c := range v.Certifications {
		if !cleanScreenText(c.Region, 64) || !cleanScreenText(c.Rating, 128) {
			return fail()
		}
	}
	for _, a := range v.Aliases {
		if !cleanScreenText(a, 2048) {
			return fail()
		}
	}
	for _, n := range v.Genres {
		if !cleanScreenText(n.Name, 200) || len(n.ID) > 128 {
			return fail()
		}
	}
	for _, c := range v.Credits {
		if len(c.ID) > 128 || !cleanScreenText(c.Name, 300) || !cleanScreenText(c.Role, 500) || !cleanScreenText(c.Department, 200) || c.Ordinal < 0 {
			return fail()
		}
	}
	for _, r := range v.Relations {
		if !ValidScreenID(r.Target) || len(r.Kind) > 100 || !cleanScreenText(r.Name, 2048) {
			return fail()
		}
	}
	for _, id := range v.Crosswalk {
		if !ValidScreenID(id) {
			return fail()
		}
	}
	if len(v.Similar) > MaxScreenSimilar {
		return fail()
	}
	// The parsers' own rule: each entry is one appendSimilar accepts after its
	// predecessors (the three-index slice makes append copy, never write).
	for i, id := range v.Similar {
		if len(appendSimilar(v.Similar[:i:i], v.Identity, id)) != i+1 {
			return fail()
		}
	}
	for _, o := range v.Orders {
		if len(o.ID) > 160 || !cleanScreenText(o.Name, 2048) {
			return fail()
		}
	}
	if v.Rating != nil && (v.Rating.Scale <= 0 || v.Rating.Votes < 0 || v.Rating.Value < 0 || v.Rating.Value > float64(v.Rating.Scale) || v.Rating.Value != v.Rating.Value) {
		return fail()
	}
	if v.EpisodeCount != nil && (*v.EpisodeCount < 0 || *v.EpisodeCount > 1000000) {
		return fail()
	}
	if c := v.Coordinates; c != nil {
		if c.Episode < 0 || c.Episode > 1000000 || c.Season != nil && (*c.Season < 0 || *c.Season > 9999) || len(c.Order) > 160 {
			return fail()
		}
	}
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 512<<10 {
		return fail()
	}
	return nil
}
func screenYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	n, _ := strconv.Atoi(date[:4])
	return n
}
func numberID(n int64) string { return strconv.FormatInt(n, 10) }
func ScreenAttribution(provider string) string {
	switch provider {
	case "tmdb":
		return "This product uses the TMDB API but is not endorsed or certified by TMDB."
	case "tvdb":
		return "TV and movie metadata provided by TheTVDB."
	case "anilist":
		return "Anime metadata provided by AniList."
	}
	return fmt.Sprintf("Local %s evidence.", provider)
}
