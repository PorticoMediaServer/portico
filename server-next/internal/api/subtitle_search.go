package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

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

// SubtitleDownload downloads a result a search answered for one version of the title (the
// first when VersionID is left out). Send the result as the search gave it.
type SubtitleDownload struct {
	VersionID string `json:"versionId,omitempty"`
	SubtitleResult
}

var (
	// languageCode is a language as the person's settings hold it: two or three letters.
	languageCode = regexp.MustCompile(`^[a-z]{2,3}$`)
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
	for _, f := range found {
		out.Items = append(out.Items, SubtitleResult{Ref: f.Ref, Language: f.Language, Release: f.Release, SDH: f.HearingImpaired,
			Forced: f.Forced, Downloads: f.Downloads})
	}
	return out, nil
}

// checkLabel refuses a label a person sends that everyone will be shown: too long, or with a
// character that cannot be shown.
func checkLabel(path, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return ErrField(path, "This is too long.")
	}
	for _, c := range value {
		if c == utf8.RuneError || c < ' ' || c == 0x7f || (c >= 0x80 && c < 0xa0) || c == ' ' || c == ' ' ||
			(c >= '‪' && c <= '‮') || (c >= '⁦' && c <= '⁩') {
			return ErrField(path, "This has a character that cannot be shown.")
		}
	}
	return nil
}

func (s *Server) downloadSubtitle(r *http.Request, req SubtitleDownload) (ItemSubtitle, error) {
	req.Language = strings.ToLower(strings.TrimSpace(req.Language))
	req.Release = strings.TrimSpace(req.Release)
	switch {
	case !providerRef.MatchString(req.Ref):
		return ItemSubtitle{}, ErrField("ref", "Send the result as the search gave it.")
	case !languageCode.MatchString(req.Language):
		return ItemSubtitle{}, ErrField("language", "Give the language as a two or three letter code.")
	case req.VersionID != "" && !providerRef.MatchString(req.VersionID):
		return ItemSubtitle{}, ErrNotFound()
	}
	if err := checkLabel("release", req.Release, maxRelease); err != nil {
		return ItemSubtitle{}, err
	}
	chosen := metadata.SubtitleResult{Ref: req.Ref, Language: req.Language, Release: req.Release, HearingImpaired: req.SDH, Forced: req.Forced}
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
