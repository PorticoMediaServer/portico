package subtitles

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/progressio"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// Why OpenSubtitles refused or could not serve a request. The same words are
// the catalogue's provider status reasons and the owner's status problems.
const (
	ReasonNotConfigured  = "provider_not_configured"
	ReasonKeyRejected    = "provider_key_rejected"
	ReasonSignInFailed   = "provider_sign_in_failed"
	ReasonSignInRequired = "provider_sign_in_required"
	ReasonQuotaExhausted = "provider_quota_exhausted"
	ReasonRateLimited    = "provider_rate_limited"
	ReasonUnreachable    = "provider_unreachable"
)

// ProviderError is a refusal the viewer and owner can act on. It is still
// ErrUnavailable to code that only asks whether the provider answered.
type ProviderError struct {
	Reason string
	// RetryAt, when set, is when the same request can succeed: the quota's
	// reset or the end of a rate limit.
	RetryAt time.Time
}

func (e *ProviderError) Error() string        { return "subtitle provider: " + e.Reason }
func (e *ProviderError) Is(target error) bool { return target == ErrUnavailable }

// OwnerStatus is what Server › Metadata shows about OpenSubtitles. It never
// carries a key, password or token; only where they come from and how the
// last exchange with OpenSubtitles went.
type OwnerStatus struct {
	// KeySource and AccountSource: "owner" (saved in settings), "environment"
	// (PORTICO_OPENSUBTITLES_*), or "none".
	KeySource     string `json:"keySource"`
	AccountSource string `json:"accountSource"`
	// Account: "none", "incomplete" (a username without a password),
	// "not_checked", "signed_in" or "rejected".
	Account string `json:"account"`
	// Problem is the last refusal (a Reason*), empty when the last exchange worked.
	Problem   string `json:"problem,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	RetryAt   string `json:"retryAt,omitempty"`
	// Download quota as OpenSubtitles last reported it; -1 when unknown.
	DownloadsRemaining int    `json:"downloadsRemaining"`
	DownloadsAllowed   int    `json:"downloadsAllowed"`
	DownloadsResetAt   string `json:"downloadsResetAt,omitempty"`
}

// OpenSubtitles credentials stay in the server process. No provider URL, token,
// filename, HTML, or download capability is included in public search results.
// Portico ships no key: the owner brings their own (settings or environment).
type OpenSubtitles struct {
	// Under gate: the credentials in force, the session and where it lives.
	key, token, agent, host string
	username, password      string
	tokenExpires            time.Time
	logins                  []time.Time
	loginBackoff            time.Time
	loginRejected           atomic.Bool
	client                  *http.Client
	mu                      sync.Mutex
	next                    time.Time
	gate                    chan struct{}
	// The owner's saved credentials (SetCredentials) and the environment's,
	// which are used where the owner saved none. request adopts a change.
	credentials, environment openSubtitlesCredentials
	credentialMu             sync.Mutex
	// changes counts SetCredentials calls; adopted is the count request last
	// took its credentials at.
	changes, adopted int
	// What the last exchanges showed, for the owner's status.
	observedMu sync.Mutex
	observed   providerObservation
	// Tests substitute the clock and the download link fetch.
	now   func() time.Time
	fetch func(context.Context, string) ([]byte, error)
}

type openSubtitlesCredentials struct {
	key, username, password string
	ownKey, ownAccount      bool
}

type providerObservation struct {
	problem              string
	at, retryAt, resetAt time.Time
	signedIn             bool
	remaining, allowed   int
	quotaKnown           bool
}

const (
	openSubtitlesHost = "api.opensubtitles.com"
	// A session token is good for 24 hours; it is renewed a little early.
	openSubtitlesTokenLife = 23 * time.Hour
	// OpenSubtitles allows 10 sign-ins a minute and 30 an hour per account.
	loginsPerMinute, loginsPerHour = 10, 30
	// After a sign-in fails for any reason but bad credentials, wait before the next.
	loginFailureBackoff = 30 * time.Second
)

// ProviderFromEnvironment is the OpenSubtitles provider, with the environment's
// credentials until the owner saves their own (Server › Metadata).
func ProviderFromEnvironment() *OpenSubtitles {
	env := openSubtitlesCredentials{key: strings.TrimSpace(os.Getenv("PORTICO_OPENSUBTITLES_API_KEY")), username: strings.TrimSpace(os.Getenv("PORTICO_OPENSUBTITLES_USERNAME")), password: os.Getenv("PORTICO_OPENSUBTITLES_PASSWORD")}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxConnsPerHost = 2
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.MaxResponseHeaderBytes = 32 << 10
	agent := strings.TrimSpace(os.Getenv("PORTICO_OPENSUBTITLES_USER_AGENT"))
	if agent == "" {
		agent = userAgent(buildinfo.Version)
	}
	return &OpenSubtitles{key: env.key, username: env.username, password: env.password, environment: env, credentials: env, host: openSubtitlesHost, agent: agent, gate: make(chan struct{}, 1), client: &http.Client{Transport: progressio.Transport{Base: tr, Idle: 30 * time.Second, Overall: 15 * time.Minute}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+`)

// userAgent is "Portico vX.Y.Z": OpenSubtitles refuses requests (403) whose
// User-Agent does not name the application and its version.
func userAgent(version string) string {
	if v := releaseVersion.FindString(strings.TrimPrefix(version, "v")); v != "" {
		return "Portico v" + v
	}
	return "Portico v0.0.0"
}

// SetCredentials installs the owner's saved key and account. An empty key keeps
// the environment's key; an empty username keeps the environment's account.
// The next request signs in again with what is now in force.
func (p *OpenSubtitles) SetCredentials(key, username, password string) {
	next := p.ownerCredentials(key, username, password)
	p.credentialMu.Lock()
	p.credentials = next
	p.changes++
	p.credentialMu.Unlock()
	p.loginRejected.Store(false)
}

func (p *OpenSubtitles) ownerCredentials(key, username, password string) openSubtitlesCredentials {
	next := openSubtitlesCredentials{key: strings.TrimSpace(key), username: strings.TrimSpace(username), password: password, ownKey: true, ownAccount: true}
	if next.key == "" {
		next.key, next.ownKey = p.environment.key, false
	}
	if next.username == "" {
		next.username, next.password, next.ownAccount = p.environment.username, p.environment.password, false
	}
	return next
}

func (p *OpenSubtitles) matchesOwnerCredentials(key, username, password string) bool {
	expected := p.ownerCredentials(key, username, password)
	p.credentialMu.Lock()
	defer p.credentialMu.Unlock()
	return p.credentials == expected
}

// adoptCredentials runs under the request gate: a change of credentials drops
// the session token and a refused sign-in, so the new ones are tried.
func (p *OpenSubtitles) adoptCredentials() {
	p.credentialMu.Lock()
	c, changes := p.credentials, p.changes
	p.credentialMu.Unlock()
	if changes == p.adopted {
		return
	}
	p.adopted = changes
	if c.key == p.key && c.username == p.username && c.password == p.password {
		return
	}
	p.key, p.username, p.password, p.token, p.host, p.tokenExpires = c.key, c.username, c.password, "", openSubtitlesHost, time.Time{}
	p.loginBackoff = time.Time{}
	p.loginRejected.Store(false)
	p.observedMu.Lock()
	p.observed = providerObservation{}
	p.observedMu.Unlock()
}

// A request keeps the credentials it adopted, but loses permission to send
// when the owner replaces them while it is queued or between subrequests.
func (p *OpenSubtitles) credentialAdmission(ctx context.Context, revision int) context.Context {
	return metadataprovider.WithNetworkAdmission(ctx, func(ctx context.Context) (func() error, error) {
		check := func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p.credentialMu.Lock()
			current := p.changes
			p.credentialMu.Unlock()
			if current != revision {
				return ErrConflict
			}
			return nil
		}
		if err := check(); err != nil {
			return nil, err
		}
		return check, nil
	})
}

func (p *OpenSubtitles) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *OpenSubtitles) configured() bool {
	p.credentialMu.Lock()
	defer p.credentialMu.Unlock()
	return p.credentials.key != "" || p.changes == 0 && p.key != ""
}

// account is whether a complete account is in force: a username alone is not one.
func (p *OpenSubtitles) account() bool { return p.username != "" && p.password != "" }

func (p *OpenSubtitles) ID() string { return "opensubtitles" }

func (p *OpenSubtitles) enter(ctx context.Context) (func(), error) {
	select {
	case p.gate <- struct{}{}:
		return func() { <-p.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *OpenSubtitles) request(ctx context.Context, method, path string, body, out any) error {
	leave, e := p.enter(ctx)
	if e != nil {
		return e
	}
	defer leave()
	p.adoptCredentials()
	ctx = p.credentialAdmission(ctx, p.adopted)
	e = p.exchange(ctx, method, path, body, out)
	p.observe(e)
	return e
}

// exchange runs under the gate: sign in when an account is in force and its
// session is missing or old, make the request, and sign in again once if
// OpenSubtitles says the session ended.
func (p *OpenSubtitles) exchange(ctx context.Context, method, path string, body, out any) error {
	if p.key == "" {
		return &ProviderError{Reason: ReasonNotConfigured}
	}
	if p.account() && (p.token == "" || !p.clock().Before(p.tokenExpires)) {
		if e := p.login(ctx); e != nil {
			return e
		}
	}
	e := p.requestOnce(ctx, method, path, body, out)
	if e != errProviderAuthorization {
		return e
	}
	if !p.account() {
		// Without an account a refusal is the key's, except for downloads,
		// which a free key may only make for a signed-in account.
		if path == "/download" {
			return &ProviderError{Reason: ReasonSignInRequired}
		}
		return &ProviderError{Reason: ReasonKeyRejected}
	}
	p.token = ""
	if e = p.login(ctx); e != nil {
		return e
	}
	if e = p.requestOnce(ctx, method, path, body, out); e == errProviderAuthorization {
		return &ProviderError{Reason: ReasonKeyRejected}
	}
	return e
}

var errProviderAuthorization = errors.New("subtitle provider authorization expired")

// allowLogin keeps within OpenSubtitles' sign-in limits, and waits out a
// recent failure, so a run of searches never turns into a run of sign-ins.
func (p *OpenSubtitles) allowLogin(now time.Time) error {
	recent := p.logins[:0]
	minute := 0
	for _, at := range p.logins {
		if now.Sub(at) < time.Hour {
			recent = append(recent, at)
			if now.Sub(at) < time.Minute {
				minute++
			}
		}
	}
	p.logins = recent
	switch {
	case now.Before(p.loginBackoff):
		return &ProviderError{Reason: ReasonRateLimited, RetryAt: p.loginBackoff}
	case minute >= loginsPerMinute:
		return &ProviderError{Reason: ReasonRateLimited, RetryAt: recent[len(recent)-minute].Add(time.Minute)}
	case len(recent) >= loginsPerHour:
		return &ProviderError{Reason: ReasonRateLimited, RetryAt: recent[0].Add(time.Hour)}
	}
	p.logins = append(p.logins, now)
	return nil
}

func (p *OpenSubtitles) login(ctx context.Context) error {
	// A refused sign-in is never repeated with the same credentials.
	if p.loginRejected.Load() {
		return &ProviderError{Reason: ReasonSignInFailed}
	}
	now := p.clock()
	if e := p.allowLogin(now); e != nil {
		return e
	}
	p.token, p.host = "", openSubtitlesHost
	var response struct {
		Token string `json:"token"`
		Host  string `json:"base_url"`
		User  struct {
			Allowed int  `json:"allowed_downloads"`
			VIP     bool `json:"vip"`
		} `json:"user"`
	}
	e := p.requestOnce(ctx, "POST", "/login", map[string]string{"username": p.username, "password": p.password}, &response)
	if e == errProviderAuthorization {
		p.loginRejected.Store(true)
		return &ProviderError{Reason: ReasonSignInFailed}
	}
	if e == nil && (response.Token == "" || len(response.Token) > 16384) {
		e = ErrUnavailable
	}
	if e != nil {
		var refused *ProviderError
		if !errors.As(e, &refused) || refused.Reason != ReasonRateLimited {
			p.loginBackoff = now.Add(loginFailureBackoff)
		}
		return e
	}
	p.token, p.host, p.tokenExpires = response.Token, providerHost(response.Host), tokenExpiry(response.Token, now)
	p.observedMu.Lock()
	p.observed.signedIn = true
	if response.User.Allowed > 0 {
		p.observed.allowed = response.User.Allowed
	}
	p.observedMu.Unlock()
	return nil
}

var providerHostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+opensubtitles\.com$`)

// providerHost is the API host a sign-in names (base_url: vip-api for VIP
// accounts). Only OpenSubtitles' own hosts are followed: the key and token are
// never sent anywhere else.
func providerHost(base string) string {
	host := strings.ToLower(strings.TrimSpace(base))
	host = strings.TrimPrefix(host, "https://")
	host, _, _ = strings.Cut(host, "/")
	if providerHostPattern.MatchString(host) {
		return host
	}
	return openSubtitlesHost
}

// tokenExpiry is when to stop using a session token: its own exp claim, if it
// has a sensible one, a few minutes early; otherwise a day less an hour.
func tokenExpiry(token string, now time.Time) time.Time {
	fallback := now.Add(openSubtitlesTokenLife)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fallback
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return fallback
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp <= 0 {
		return fallback
	}
	exp := time.Unix(claims.Exp, 0).Add(-5 * time.Minute)
	if exp.Before(now) || exp.After(now.Add(24*time.Hour)) {
		return fallback
	}
	return exp
}

func (p *OpenSubtitles) requestOnce(ctx context.Context, method, path string, body, out any) error {
	check, e := metadataprovider.BeginNetworkAdmission(ctx)
	if e != nil {
		return e
	}
	p.mu.Lock()
	delay := p.next.Sub(p.clock())
	p.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// One request a second, across every caller.
	p.mu.Lock()
	p.next = p.clock().Add(time.Second)
	p.mu.Unlock()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	host := p.host
	if host == "" {
		host = openSubtitlesHost
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://"+host+"/api/v1"+path, bytes.NewReader(raw))
	if e != nil {
		return ErrUnavailable
	}
	req.Header.Set("Api-Key", p.key)
	req.Header.Set("User-Agent", p.agent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.token != "" && path != "/login" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	if e = check(); e != nil {
		return e
	}
	response, e := p.client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, MaxInputBytes+1))
	if e != nil || len(raw) > MaxInputBytes {
		return ErrUnavailable
	}
	if e = check(); e != nil {
		return e
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return errProviderAuthorization
	case http.StatusForbidden:
		// A key OpenSubtitles does not accept, or a User-Agent it refuses.
		return &ProviderError{Reason: ReasonKeyRejected}
	case http.StatusNotAcceptable:
		// The account's (or, without one, this address's) downloads for the day are used.
		return &ProviderError{Reason: ReasonQuotaExhausted, RetryAt: quotaReset(raw, p.clock())}
	case http.StatusTooManyRequests:
		wait := retryAfter(response.Header)
		p.mu.Lock()
		p.next = p.clock().Add(wait)
		p.mu.Unlock()
		return &ProviderError{Reason: ReasonRateLimited, RetryAt: p.clock().Add(wait)}
	default:
		return ErrUnavailable
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrUnavailable
	}
	return nil
}

// retryAfter reads the wait from Retry-After or the RateLimit headers
// (seconds), within a day; a minute when neither says.
func retryAfter(h http.Header) time.Duration {
	for _, name := range []string{"Retry-After", "RateLimit-Reset", "X-RateLimit-Reset"} {
		if n, e := strconv.Atoi(strings.TrimSpace(h.Get(name))); e == nil && n > 0 && n <= 86400 {
			return time.Duration(n) * time.Second
		}
	}
	return time.Minute
}

// quotaReset is when a used-up download quota renews: what OpenSubtitles says,
// or the next UTC midnight, since quotas are counted per UTC day.
func quotaReset(raw []byte, now time.Time) time.Time {
	var quota struct {
		Reset string `json:"reset_time_utc"`
	}
	if json.Unmarshal(raw, &quota) == nil {
		if at, e := time.Parse(time.RFC3339, quota.Reset); e == nil && at.After(now) && at.Before(now.Add(48*time.Hour)) {
			return at.UTC()
		}
	}
	day := now.UTC()
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, time.UTC)
}

// observe keeps the outcome of an exchange for the owner's status.
func (p *OpenSubtitles) observe(e error) {
	if e != nil && (errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded)) {
		return
	}
	p.observedMu.Lock()
	defer p.observedMu.Unlock()
	p.observed.at, p.observed.problem, p.observed.retryAt = p.clock(), "", time.Time{}
	var refused *ProviderError
	switch {
	case e == nil:
	case errors.As(e, &refused):
		p.observed.problem, p.observed.retryAt = refused.Reason, refused.RetryAt
		switch refused.Reason {
		case ReasonQuotaExhausted:
			p.observed.remaining, p.observed.resetAt, p.observed.quotaKnown = 0, refused.RetryAt, true
		case ReasonSignInFailed, ReasonKeyRejected:
			p.observed.signedIn = false
		}
	default:
		p.observed.problem = ReasonUnreachable
	}
}

func (p *OpenSubtitles) observeQuota(remaining, allowed int, reset time.Time) {
	p.observedMu.Lock()
	defer p.observedMu.Unlock()
	if remaining >= 0 {
		p.observed.remaining, p.observed.quotaKnown = remaining, true
	}
	if allowed > 0 {
		p.observed.allowed = allowed
	}
	if !reset.IsZero() {
		p.observed.resetAt = reset
	}
}

func (p *OpenSubtitles) Search(ctx context.Context, title, language string, page int) ([]ProviderCandidate, error) {
	return p.SearchMedia(ctx, ProviderQuery{Title: title, Language: language, Page: page, Season: -1, Episode: -1})
}
func (p *OpenSubtitles) SearchMedia(ctx context.Context, media ProviderQuery) ([]ProviderCandidate, error) {
	q := url.Values{"languages": {providerLanguage(media.Language)}}
	if media.Page > 1 {
		q.Set("page", strconv.Itoa(media.Page))
	}
	switch {
	case media.IMDbID != "":
		q.Set("imdb_id", media.IMDbID)
	case media.TMDBID != "":
		q.Set("tmdb_id", media.TMDBID)
	default:
		if media.ParentIMDbID != "" {
			q.Set("parent_imdb_id", media.ParentIMDbID)
		} else if media.ParentTMDBID != "" {
			q.Set("parent_tmdb_id", media.ParentTMDBID)
		} else {
			q.Set("query", strings.ToLower(media.Title))
			if media.Year > 0 && media.Episode < 0 {
				q.Set("year", strconv.Itoa(media.Year))
			}
		}
		if media.Episode >= 0 && media.Season >= 0 {
			q.Set("season_number", strconv.Itoa(media.Season))
			q.Set("episode_number", strconv.Itoa(media.Episode))
			q.Set("type", "episode")
		}
	}

	var response struct {
		Data []struct {
			Attributes struct {
				Language string `json:"language"`
				Release  string `json:"release"`
				Files    []struct {
					FileID int64 `json:"file_id"`
				} `json:"files"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if e := p.request(ctx, "GET", "/subtitles?"+q.Encode(), nil, &response); e != nil {
		return nil, e
	}
	out := []ProviderCandidate{}
	for _, v := range response.Data {
		language, e := Language(v.Attributes.Language)
		if e != nil {
			continue
		}
		for _, file := range v.Attributes.Files {
			if file.FileID <= 0 {
				continue
			}
			out = append(out, ProviderCandidate{file.FileID, language, safeTitle(v.Attributes.Release), "OpenSubtitles.com"})
			if len(out) >= 40 {
				return out, nil
			}
		}
	}
	return out, nil
}
func (p *OpenSubtitles) Download(ctx context.Context, id int64) ([]byte, error) {
	p.credentialMu.Lock()
	revision := p.changes
	p.credentialMu.Unlock()
	ctx = p.credentialAdmission(ctx, revision)
	check, e := metadataprovider.BeginNetworkAdmission(ctx)
	if e != nil {
		return nil, e
	}
	var response struct {
		Link      string `json:"link"`
		Remaining *int   `json:"remaining"`
		Reset     string `json:"reset_time_utc"`
	}
	if e := p.request(ctx, "POST", "/download", map[string]any{"file_id": id, "sub_format": "srt"}, &response); e != nil {
		return nil, e
	}
	remaining := -1
	if response.Remaining != nil {
		remaining = max(*response.Remaining, 0)
	}
	reset, _ := time.Parse(time.RFC3339, response.Reset)
	p.observeQuota(remaining, 0, reset.UTC())
	u, e := url.Parse(response.Link)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return nil, ErrUnavailable
	}
	if e = check(); e != nil {
		return nil, e
	}
	var raw []byte
	if p.fetch != nil {
		raw, e = p.fetch(ctx, response.Link)
	} else {
		raw, e = fetchPublic(ctx, response.Link)
	}
	if e != nil {
		return nil, e
	}
	if e = check(); e != nil {
		return nil, e
	}
	return raw, nil
}

// fetchPublic reads a download link through the public-network transport: no
// proxy, fresh DNS admission at every dial/redirect, no private/link-local
// targets or TLS downgrade.
func fetchPublic(ctx context.Context, link string) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	check, e := metadataprovider.BeginNetworkAdmission(ctx)
	if e != nil {
		return nil, e
	}
	for hop := 0; hop <= 4; hop++ {
		u, e := url.Parse(link)
		if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
			return nil, ErrUnavailable
		}
		if e = check(); e != nil {
			return nil, e
		}
		client, e := remotemedia.New(ctx, link, remotemedia.Policy{ManualRedirects: true})
		if e != nil {
			return nil, ErrUnavailable
		}
		if e = check(); e != nil {
			client.Close()
			return nil, e
		}
		res, e := client.Open(ctx, "GET", "")
		if e != nil {
			client.Close()
			return nil, ErrUnavailable
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			next, err := res.Location()
			res.Body.Close()
			client.Close()
			if err != nil || hop == 4 {
				return nil, ErrUnavailable
			}
			link = next.String()
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			client.Close()
			return nil, ErrUnavailable
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, MaxInputBytes+1))
		res.Body.Close()
		client.Close()
		if e != nil || len(raw) > MaxInputBytes {
			return nil, ErrCapacity
		}
		if e = check(); e != nil {
			return nil, e
		}
		return raw, nil
	}
	return nil, ErrUnavailable
}

// Status is what every viewer's subtitle catalogue says about searching online.
func (p *OpenSubtitles) Status() ProviderStatus {
	if !p.configured() {
		return ProviderStatus{ID: p.ID(), Reason: ReasonNotConfigured}
	}
	if p.loginRejected.Load() {
		return ProviderStatus{ID: p.ID(), Reason: ReasonSignInFailed}
	}
	p.observedMu.Lock()
	o := p.observed
	p.observedMu.Unlock()
	status := ProviderStatus{ID: p.ID(), Enabled: true}
	now := p.clock()
	reason := o.problem
	if reason == "" && o.quotaKnown && o.remaining == 0 && o.resetAt.After(now) {
		reason = ReasonQuotaExhausted
	}
	switch reason {
	case ReasonQuotaExhausted:
		if !o.resetAt.After(now) {
			reason = ""
		} else {
			status.ResetsAt = o.resetAt.UTC().Format(time.RFC3339)
		}
	case ReasonRateLimited:
		if !o.retryAt.After(now) {
			reason = ""
		}
	}
	if reason != "" {
		status.Enabled = false
		status.Reason = reason
		if o.retryAt.After(now) {
			status.RetryAt = o.retryAt.UTC().Format(time.RFC3339)
		}
	}
	return status
}

// OwnerStatus says where the credentials come from and how OpenSubtitles last
// answered. It makes no request.
func (p *OpenSubtitles) OwnerStatus() OwnerStatus {
	p.credentialMu.Lock()
	c := p.credentials
	p.credentialMu.Unlock()
	out := OwnerStatus{KeySource: "none", AccountSource: "none", Account: "none", DownloadsRemaining: -1, DownloadsAllowed: -1}
	switch {
	case c.key != "" && c.ownKey:
		out.KeySource = "owner"
	case c.key != "":
		out.KeySource = "environment"
	}
	if c.username != "" {
		out.AccountSource = "environment"
		if c.ownAccount {
			out.AccountSource = "owner"
		}
		out.Account = "not_checked"
		if c.password == "" {
			out.Account = "incomplete"
		}
	}
	p.observedMu.Lock()
	o := p.observed
	p.observedMu.Unlock()
	if out.Account == "not_checked" {
		switch {
		case p.loginRejected.Load():
			out.Account = "rejected"
		case o.signedIn:
			out.Account = "signed_in"
		}
	}
	if out.KeySource == "none" {
		return out
	}
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	out.Problem, out.CheckedAt, out.RetryAt = o.problem, stamp(o.at), stamp(o.retryAt)
	if out.Account == "rejected" {
		out.Problem = ReasonSignInFailed
	}
	if o.quotaKnown {
		out.DownloadsRemaining = o.remaining
	}
	if o.allowed > 0 {
		out.DownloadsAllowed = o.allowed
	}
	// A reset already past says nothing about today's quota.
	if o.resetAt.After(p.clock()) {
		out.DownloadsResetAt = stamp(o.resetAt)
	} else if o.quotaKnown && !o.resetAt.IsZero() {
		out.DownloadsRemaining = -1
	}
	return out
}

// Check asks OpenSubtitles whether the key (and account, if any) work now: the
// owner's "Test connection". A sign-in refused earlier is tried once more, still
// within the sign-in limits. It costs one or two requests and no download.
func (p *OpenSubtitles) Check(ctx context.Context) OwnerStatus {
	leave, e := p.enter(ctx)
	if e != nil {
		return p.OwnerStatus()
	}
	p.adoptCredentials()
	ctx = p.credentialAdmission(ctx, p.adopted)
	p.loginRejected.Store(false)
	if p.key != "" {
		if p.account() {
			var user struct {
				Data struct {
					Allowed   int  `json:"allowed_downloads"`
					Remaining *int `json:"remaining_downloads"`
				} `json:"data"`
			}
			e = p.exchange(ctx, "GET", "/infos/user", nil, &user)
			if e == nil && user.Data.Remaining != nil {
				p.observeQuota(max(*user.Data.Remaining, 0), user.Data.Allowed, time.Time{})
			}
		} else {
			// /infos/formats answers any key; a search is the cheapest call that checks it.
			var found struct {
				Data []any `json:"data"`
			}
			e = p.exchange(ctx, "GET", "/subtitles?imdb_id=133093&languages=en", nil, &found)
		}
		p.observe(e)
	}
	leave()
	return p.OwnerStatus()
}
