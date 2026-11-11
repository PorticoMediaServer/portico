package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles"
)

// Subtitle search and download (definition 07, rules 65 to 69). Anyone who may play a title
// may search for its subtitles and download one, and what they download is then offered to
// everyone who may play that version, in GET /v1/items/{id}/subtitles with source
// "downloaded". There is no separate permission. The owner and administrators may remove one.
// The server never chooses where a subtitle is written: it is kept by the hash of its text.

// subtitleCredit is the line every list of results carries (rule 68).
const subtitleCredit = "Subtitles provided by OpenSubtitles.com"

// SubtitleResult is one subtitle a search found.
type SubtitleResult struct {
	Ref       string `json:"ref"`
	Language  string `json:"language"`
	Release   string `json:"release,omitempty"`
	SDH       bool   `json:"sdh"`
	Forced    bool   `json:"forced"`
	Downloads int    `json:"downloads"`
}

// SubtitleResults are a search's answers, with the provider's credit line to show beside them.
type SubtitleResults struct {
	Items  []SubtitleResult `json:"items"`
	Credit string           `json:"credit"`
}

// SubtitleDownload downloads a result of a search this profile made for this title in the
// last 30 minutes, for one version of the title (the first when VersionID is left out). Its
// language, release name and kinds are what the search answered, never what is sent.
type SubtitleDownload struct {
	VersionID string `json:"versionId,omitempty"`
	Ref       string `json:"ref"`
}

// searchKept is how long a search's results can be downloaded from; maxSearches and
// maxResultsKept bound what is remembered.
const (
	searchKept     = 30 * time.Minute
	maxSearches    = 1000
	maxResultsKept = 200
)

// subtitleSearches remembers what each profile's searches for each title answered, so a
// download can only be one of those results, labelled as the provider labelled it. It is
// running state: after a restart, search again.
type subtitleSearches struct {
	mu      sync.Mutex
	entries map[searchKey]*searchEntry
}

type searchKey struct {
	profile int64
	item    string
}

type searchEntry struct {
	at      time.Time
	results map[string]metadata.SubtitleResult
}

// keep remembers a search's results, beside the same profile's other recent ones for the title.
func (m *subtitleSearches) keep(key searchKey, results []metadata.SubtitleResult, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = map[searchKey]*searchEntry{}
	}
	var oldest *searchKey
	for k, e := range m.entries {
		if now.Sub(e.at) > searchKept {
			delete(m.entries, k)
		} else if oldest == nil || e.at.Before(m.entries[*oldest].at) {
			k := k
			oldest = &k
		}
	}
	entry := m.entries[key]
	if entry == nil && len(m.entries) >= maxSearches && oldest != nil {
		delete(m.entries, *oldest)
	}
	// A search that would go past the limit starts afresh, so the latest search can always be
	// downloaded from, however many searches came before it.
	if entry == nil || len(entry.results)+len(results) > maxResultsKept {
		entry = &searchEntry{results: map[string]metadata.SubtitleResult{}}
		m.entries[key] = entry
	}
	entry.at = now
	for _, r := range results {
		if len(entry.results) >= maxResultsKept {
			break
		}
		entry.results[r.Ref] = r
	}
}

// find answers a result of a recent search by this profile for this title.
func (m *subtitleSearches) find(key searchKey, ref string, now time.Time) (metadata.SubtitleResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[key]
	if entry == nil || now.Sub(entry.at) > searchKept {
		return metadata.SubtitleResult{}, false
	}
	r, ok := entry.results[ref]
	return r, ok
}

var (
	// providerRef is the provider's id for a result: letters, digits, dot, dash, underscore.
	providerRef = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// maxRelease bounds the release name a person sends back with a download; it is shown to
// everyone who plays the title.
const maxRelease = 200

func (s *Server) subtitleSearchRoutes() {
	Handle(s, "GET /v1/items/{id}/subtitles/search", WithProfile, s.searchSubtitles)
	Handle(s, "POST /v1/items/{id}/subtitles", WithProfile, s.downloadSubtitle)
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "DELETE /v1/admin/items/{id}/subtitles/{subtitleId}", Admin, s.removeSubtitle))
}

// subtitleError says in plain words what the provider answered (rules 67 and 68).
func subtitleError(err error) error {
	var used *opensubtitles.AllowanceUsed
	var pause *fetch.SlowDown
	switch {
	case errors.Is(err, metadata.ErrNoSubtitleProvider):
		return ErrConflict("subtitle_search_not_set_up", "Subtitle search isn't set up on this server. Ask the owner to set up OpenSubtitles.")
	case errors.Is(err, opensubtitles.ErrKeyRefused):
		return refusal(http.StatusBadGateway, "subtitle_key_refused", "OpenSubtitles refused this server's key. Ask the owner to check it.", false)
	case errors.Is(err, opensubtitles.ErrSignInRefused):
		return refusal(http.StatusBadGateway, "subtitle_sign_in_refused", "OpenSubtitles refused the server's account. Ask the owner to check it.", false)
	case errors.As(err, &used):
		e := ErrTooMany("subtitle_allowance_used", "Today's subtitle downloads are used up. Try again after "+used.Renews.UTC().Format(time.RFC3339)+".",
			time.Until(used.Renews))
		return e
	case errors.As(err, &pause):
		return ErrTooMany("provider_paused", "OpenSubtitles asked to wait a little. Try again shortly.", time.Until(pause.Until))
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrInvalid):
		return refuse(err)
	case err != nil && strings.HasPrefix(err.Error(), "the downloaded subtitle cannot be read"):
		return refusal(http.StatusBadGateway, "subtitle_unreadable", "That subtitle could not be read. Try another.", true)
	case err != nil:
		return refusal(http.StatusBadGateway, "provider_unavailable", "OpenSubtitles did not answer. Try again later.", true)
	}
	return nil
}

func (s *Server) searchSubtitles(r *http.Request, _ None) (SubtitleResults, error) {
	out := SubtitleResults{Items: []SubtitleResult{}, Credit: subtitleCredit}
	var languages []string
	for _, l := range strings.Split(r.URL.Query().Get("languages"), ",") {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			if !languageCode.MatchString(l) {
				return out, ErrField("languages", "Give languages as two or three letter codes, separated by commas.")
			}
			languages = append(languages, l)
		}
	}
	if len(languages) == 0 || len(languages) > 10 {
		return out, ErrField("languages", "Give one to ten languages.")
	}
	found, err := s.metadata.SearchSubtitles(r.Context(), viewer(r), r.PathValue("id"), languages)
	if err != nil {
		return out, subtitleError(err)
	}
	s.subtitleSearches.keep(searchKey{CallerFrom(r.Context()).ProfileID, r.PathValue("id")}, found, s.now())
	for _, f := range found {
		out.Items = append(out.Items, SubtitleResult{Ref: f.Ref, Language: f.Language, Release: f.Release, SDH: f.HearingImpaired,
			Forced: f.Forced, Downloads: f.Downloads})
	}
	return out, nil
}

func (s *Server) downloadSubtitle(r *http.Request, req SubtitleDownload) (ItemSubtitle, error) {
	switch {
	case !providerRef.MatchString(req.Ref):
		return ItemSubtitle{}, ErrField("ref", "Search again and choose a result.")
	case req.VersionID != "" && !providerRef.MatchString(req.VersionID):
		return ItemSubtitle{}, ErrNotFound()
	}
	// Only a result this profile's search for this title answered, labelled as it was answered:
	// a person cannot attach another file, or name its language, for everyone who plays it.
	chosen, ok := s.subtitleSearches.find(searchKey{CallerFrom(r.Context()).ProfileID, r.PathValue("id")}, req.Ref, s.now())
	if !ok {
		return ItemSubtitle{}, ErrField("ref", "Search again and choose a result.")
	}
	// The provider's own release name is shown to everyone too: one that cannot be shown is left out.
	chosen.Release = strings.TrimSpace(chosen.Release)
	if checkLabel("release", chosen.Release, maxRelease) != nil {
		chosen.Release = ""
	}
	sub, err := s.metadata.DownloadSubtitle(r.Context(), viewer(r), r.PathValue("id"), req.VersionID, chosen)
	if err != nil {
		return ItemSubtitle{}, subtitleError(err)
	}
	// Answered as the list of the title's subtitles shows it.
	list, err := s.itemSubtitles(r, None{})
	if err != nil {
		return ItemSubtitle{}, err
	}
	for _, item := range list.Items {
		if item.ID == "d"+sub.PublicID {
			return item, nil
		}
	}
	return ItemSubtitle{}, ErrNotFound()
}

func (s *Server) removeSubtitle(r *http.Request, _ None) (None, error) {
	// Only a downloaded subtitle of a title the caller may see: the title's own list says so.
	list, err := s.itemSubtitles(r, None{})
	if err != nil {
		return None{}, err
	}
	id := r.PathValue("subtitleId")
	for _, item := range list.Items {
		if item.ID == id && item.Source == "downloaded" {
			return None{}, subtitleError(s.metadata.RemoveSubtitle(r.Context(), strings.TrimPrefix(id, "d")))
		}
	}
	return None{}, nil // not there: a remove sent twice changes nothing
}
