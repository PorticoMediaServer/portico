// Package opensubtitles searches OpenSubtitles.com for a film's or an episode's subtitles and
// downloads the one a person picks. It is the metadata.SubtitleProvider. It needs the owner's
// API key and account (definition area 7, rule 67).
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/language"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

const (
	// Origin is where OpenSubtitles' API is.
	Origin = "https://api.opensubtitles.com"
	// userAgent: OpenSubtitles refuses a request whose User-Agent does not name the
	// application and its version this way.
	userAgent  = "Portico v1.0.0"
	maxResults = 40
	maxFile    = 4 << 20 // no subtitle file is larger
)

// What OpenSubtitles can refuse, each said in plain words (definition area 7, rule 68). A
// request to pause is a *fetch.SlowDown.
var (
	ErrKeyRefused    = errors.New("OpenSubtitles refused this server's API key")
	ErrSignInRefused = errors.New("OpenSubtitles refused the account's username or password")
)

// AllowanceUsed is OpenSubtitles saying the account's downloads for the day are used up.
type AllowanceUsed struct{ Renews time.Time }

func (e *AllowanceUsed) Error() string {
	return "today's OpenSubtitles downloads are used up; more from " + e.Renews.Format("15:04 MST")
}

// Client asks OpenSubtitles with the owner's key and account. Make one when the owner saves
// them and share it.
type Client struct {
	origin            string
	key, user, secret string
	api               *fetch.Client
	files             *http.Client
	mu                sync.Mutex
	token, apiOrigin  string
	// refused: OpenSubtitles refused the account. It is not asked again with the same
	// password, which could lock the account; the owner saving it again makes a new client.
	refused bool
}

// Every is the spacing OpenSubtitles asks for: at most one request a second.
const Every = time.Second

// New makes a client with the owner's key and account. origin is opensubtitles.Origin with
// Every, or a test's own server with no spacing.
func New(origin string, every time.Duration, key, username, password string) *Client {
	c := &Client{origin: strings.TrimRight(origin, "/"), key: key, user: username, secret: password,
		// A person waiting is told of a pause rather than kept waiting.
		api: fetch.New("opensubtitles", every, 0)}
	c.files = &http.Client{Timeout: time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !c.theirs(req.URL) {
			return errors.New("a subtitle download pointed somewhere other than OpenSubtitles")
		}
		return nil
	}}
	return c
}

func (c *Client) Name() string { return "opensubtitles" }

// theirs reports whether an address from an answer is OpenSubtitles' own: https on
// opensubtitles.com or .org, or the server the client was made for.
func (c *Client) theirs(u *url.URL) bool {
	if own, err := url.Parse(c.origin); err == nil && u.Scheme == own.Scheme && u.Host == own.Host {
		return true
	}
	host := u.Hostname()
	return u.Scheme == "https" && u.User == nil && (host == "opensubtitles.com" || strings.HasSuffix(host, ".opensubtitles.com") ||
		host == "opensubtitles.org" || strings.HasSuffix(host, ".opensubtitles.org"))
}

// call sends one API request, signing in first when it needs to and once more when the
// sign-in has run out.
func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	for try := 0; ; try++ {
		if path != "/login" {
			if err := c.signIn(ctx); err != nil {
				return err
			}
		}
		err := c.send(ctx, method, path, body, out)
		var refused *fetch.Status
		if !errors.As(err, &refused) {
			return err
		}
		switch refused.Code {
		case http.StatusUnauthorized:
			if path == "/login" {
				return ErrSignInRefused
			}
			if try == 0 {
				c.mu.Lock()
				c.token = ""
				c.mu.Unlock()
				continue
			}
			return ErrSignInRefused
		case http.StatusForbidden:
			return ErrKeyRefused
		case http.StatusNotAcceptable:
			var quota struct {
				Reset string `json:"reset_time_utc"`
			}
			json.Unmarshal([]byte(refused.Body), &quota)
			renews, err := time.Parse(time.RFC3339, quota.Reset)
			if err != nil {
				day := time.Now().UTC() // allowances are counted per day in UTC
				renews = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, time.UTC)
			}
			return &AllowanceUsed{Renews: renews}
		}
		return err
	}
}

func (c *Client) send(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	c.mu.Lock()
	origin, token := c.origin, c.token
	if c.apiOrigin != "" && path != "/login" {
		origin = c.apiOrigin
	}
	c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, method, origin+"/api/v1"+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Api-Key", c.key)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" && path != "/login" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	raw, err := c.api.Do(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// signIn signs in with the owner's account unless the client already is.
func (c *Client) signIn(ctx context.Context) error {
	c.mu.Lock()
	signedIn, refused := c.token != "", c.refused
	c.mu.Unlock()
	switch {
	case signedIn:
		return nil
	case refused:
		return ErrSignInRefused
	}
	var answer struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	err := c.call(ctx, http.MethodPost, "/login", map[string]string{"username": c.user, "password": c.secret}, &answer)
	if err == nil && answer.Token == "" {
		err = ErrSignInRefused
	}
	if errors.Is(err, ErrSignInRefused) {
		c.mu.Lock()
		c.refused = true
		c.mu.Unlock()
	}
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token, c.apiOrigin = answer.Token, ""
	// An account may be told to use another of OpenSubtitles' API hosts.
	if host := strings.TrimSpace(answer.BaseURL); host != "" {
		if u, err := url.Parse("https://" + host); err == nil && u.Path == "" && c.theirs(u) {
			c.apiOrigin = "https://" + u.Host
		}
	}
	return nil
}

// code is a language as OpenSubtitles writes it: "en", "pt-br".
func code(lang string) string {
	tag, err := language.Parse(lang)
	if err != nil {
		return ""
	}
	base, _ := tag.Base()
	out := base.String()
	if region, confidence := tag.Region(); confidence == language.Exact {
		out += "-" + region.String()
	}
	return strings.ToLower(out)
}

// threeLetter is a language as the server's tracks write it: "eng".
func threeLetter(lang string) string {
	tag, err := language.Parse(lang)
	if err != nil {
		return ""
	}
	base, _ := tag.Base()
	return base.ISO3()
}

// numeric is an IMDb or TMDB id as OpenSubtitles takes it: the number, without "tt" or
// leading zeros.
func numeric(id string) string {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(id), "tt"), 10, 64)
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

// Search answers subtitles for a film or an episode, most downloaded first as OpenSubtitles
// orders them. A title with an IMDb or TMDB id is found by it; otherwise by its name.
func (c *Client) Search(ctx context.Context, q metadata.SubtitleQuery) ([]metadata.SubtitleResult, error) {
	var languages []string
	for _, l := range q.Languages {
		if l := code(l); l != "" {
			languages = append(languages, l)
		}
	}
	// OpenSubtitles answers a redirect unless the parameters are sorted and in lower case.
	params := url.Values{}
	if len(languages) > 0 {
		params.Set("languages", strings.Join(sortedUnique(languages), ","))
	}
	episode := q.Season >= 0 && q.Episode > 0
	switch {
	case numeric(q.IMDbID) != "":
		params.Set("imdb_id", numeric(q.IMDbID))
	case numeric(q.TMDBID) != "":
		params.Set("tmdb_id", numeric(q.TMDBID))
	case episode && numeric(q.ShowIMDbID) != "":
		params.Set("parent_imdb_id", numeric(q.ShowIMDbID))
	case episode && numeric(q.ShowTMDBID) != "":
		params.Set("parent_tmdb_id", numeric(q.ShowTMDBID))
	default:
		params.Set("query", strings.ToLower(q.Title))
		if q.Year > 0 && !episode {
			params.Set("year", strconv.Itoa(q.Year))
		}
	}
	if episode && params.Get("imdb_id") == "" && params.Get("tmdb_id") == "" {
		params.Set("season_number", strconv.Itoa(q.Season))
		params.Set("episode_number", strconv.Itoa(q.Episode))
	}
	var page struct {
		Data []struct {
			Attributes struct {
				Language        string `json:"language"`
				Release         string `json:"release"`
				HearingImpaired bool   `json:"hearing_impaired"`
				ForeignOnly     bool   `json:"foreign_parts_only"`
				Downloads       int    `json:"download_count"`
				Files           []struct {
					ID int64 `json:"file_id"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/subtitles?"+params.Encode(), nil, &page); err != nil {
		return nil, err
	}
	var out []metadata.SubtitleResult
	for _, d := range page.Data {
		a := d.Attributes
		lang := threeLetter(a.Language)
		if lang == "" {
			continue
		}
		for _, f := range a.Files {
			if f.ID > 0 && len(out) < maxResults {
				out = append(out, metadata.SubtitleResult{Ref: strconv.FormatInt(f.ID, 10), Language: lang, Release: a.Release,
					HearingImpaired: a.HearingImpaired, Forced: a.ForeignOnly, Downloads: a.Downloads})
			}
		}
	}
	return out, nil
}

func sortedUnique(list []string) []string {
	slices.Sort(list)
	return slices.Compact(list)
}

// Download asks OpenSubtitles for a subtitle file as SRT, which counts against the account's
// daily allowance, then fetches it from the address the answer gives.
func (c *Client) Download(ctx context.Context, ref string) ([]byte, string, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return nil, "", metadata.ErrNotFound
	}
	var answer struct {
		Link string `json:"link"`
	}
	if err := c.call(ctx, http.MethodPost, "/download", map[string]any{"file_id": id, "sub_format": "srt"}, &answer); err != nil {
		return nil, "", err
	}
	link, err := url.Parse(answer.Link)
	if err != nil || !c.theirs(link) {
		return nil, "", errors.New("OpenSubtitles gave a download address that is not its own")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := c.files.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("opensubtitles: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("opensubtitles answered %d for the subtitle file", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxFile+1))
	if err != nil {
		return nil, "", fmt.Errorf("opensubtitles: %w", err)
	}
	if len(raw) > maxFile {
		return nil, "", errors.New("the subtitle file is larger than any real one")
	}
	return raw, "srt", nil
}
