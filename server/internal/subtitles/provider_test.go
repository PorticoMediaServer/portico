package subtitles

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type providerTransport func(*http.Request) (*http.Response, error)

func (f providerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeOpenSubtitles stands in for api.opensubtitles.com: each test answers the
// requests it cares about, and the fake records what arrived where.
type fakeOpenSubtitles struct {
	mu       sync.Mutex
	requests []*http.Request
	answer   func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeOpenSubtitles) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r.URL.Path == "/api/v1"+path {
			n++
		}
	}
	return n
}
func (f *fakeOpenSubtitles) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

func newFakeProvider(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*OpenSubtitles, *fakeOpenSubtitles, *fakeClock) {
	t.Helper()
	fake := &fakeOpenSubtitles{answer: answer}
	clock := &fakeClock{at: time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)}
	p := &OpenSubtitles{key: "fixture-key", agent: userAgent("1.4.2"), host: openSubtitlesHost, gate: make(chan struct{}, 1), now: clock.now}
	env := openSubtitlesCredentials{key: "fixture-key"}
	p.environment, p.credentials = env, env
	p.client = &http.Client{Transport: providerTransport(func(r *http.Request) (*http.Response, error) {
		// The fake answers at once; the one-a-second pacing is a separate test.
		p.mu.Lock()
		p.next = time.Time{}
		p.mu.Unlock()
		fake.mu.Lock()
		fake.requests = append(fake.requests, r)
		fake.mu.Unlock()
		rec := httptest.NewRecorder()
		fake.answer(rec, r)
		return rec.Result(), nil
	})}
	return p, fake, clock
}

func withAccount(p *OpenSubtitles) {
	p.SetCredentials("fixture-key", "fixture-user", "fixture-password")
}

func sessionToken(exp time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func reason(e error) string {
	var refused *ProviderError
	if errors.As(e, &refused) {
		return refused.Reason
	}
	return ""
}

func TestProviderEpisodeSearchKeepsIdentityAndRegionalLanguage(t *testing.T) {
	p, _, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("parent_imdb_id") != "123" || q.Get("season_number") != "2" || q.Get("episode_number") != "3" || q.Get("languages") != "pt-br" || q.Get("query") != "" {
			t.Fatal(q)
		}
		w.Write([]byte(`{"data":[]}`))
	})
	if _, e := p.SearchMedia(context.Background(), ProviderQuery{Title: "Pilot", Language: "pt-BR", ParentIMDbID: "123", Season: 2, Episode: 3, Page: 1}); e != nil {
		t.Fatal(e)
	}
}

func TestProviderNamesPorticoAndItsVersionToOpenSubtitles(t *testing.T) {
	for version, want := range map[string]string{"0.1.0-dev": "Portico v0.1.0", "v2.3.4+build.7": "Portico v2.3.4", "1.4.2": "Portico v1.4.2", "development": "Portico v0.0.0"} {
		if got := userAgent(version); got != want {
			t.Errorf("userAgent(%q) = %q, want %q", version, got, want)
		}
	}
	p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"attributes":{"language":"en","release":"Fixture.2026.1080p","files":[{"file_id":42}]}}]}`))
	})
	got, e := p.Search(context.Background(), "Fixture", "en", 1)
	if e != nil || len(got) != 1 || got[0].FileID != 42 || got[0].Attribution != "OpenSubtitles.com" {
		t.Fatal(got, e)
	}
	r := fake.last()
	if r.Header.Get("Api-Key") != "fixture-key" || r.Header.Get("User-Agent") != "Portico v1.4.2" || r.Header.Get("Authorization") != "" || r.URL.Host != openSubtitlesHost {
		t.Fatal(r.Header, r.URL)
	}
}

func TestProviderSignsInOnceAndKeepsTheSessionForItsLifetime(t *testing.T) {
	var p *OpenSubtitles
	var clock *fakeClock
	p, fake, clock := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/login") {
			if r.URL.Host != openSubtitlesHost || r.Header.Get("Authorization") != "" {
				t.Fatal("sign-in must go to the main host without a bearer", r.URL.Host)
			}
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["username"] != "fixture-user" || in["password"] != "fixture-password" {
				t.Fatal(in)
			}
			w.Write([]byte(`{"token":"` + sessionToken(clock.at.Add(24*time.Hour)) + `","base_url":"vip-api.opensubtitles.com","user":{"allowed_downloads":1000,"vip":true}}`))
			return
		}
		if r.URL.Host != "vip-api.opensubtitles.com" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer eyJ") {
			t.Fatal("after sign-in every call goes to base_url with the session", r.URL.Host)
		}
		w.Write([]byte(`{"data":[]}`))
	})
	withAccount(p)
	for range 3 {
		if _, e := p.Search(context.Background(), "Title", "en", 1); e != nil {
			t.Fatal(e)
		}
	}
	if fake.count("/login") != 1 || fake.count("/subtitles") != 3 {
		t.Fatalf("one sign-in for three searches: %d logins", fake.count("/login"))
	}
	status := p.OwnerStatus()
	if status.Account != "signed_in" || status.AccountSource != "owner" || status.KeySource != "owner" || status.DownloadsAllowed != 1000 || status.Problem != "" {
		t.Fatalf("%+v", status)
	}
	// A day later the session has ended: one new sign-in, before the request.
	clock.at = clock.at.Add(24 * time.Hour)
	if _, e := p.Search(context.Background(), "Title", "en", 1); e != nil || fake.count("/login") != 2 {
		t.Fatal(e, fake.count("/login"))
	}
}

func TestProviderFollowsOnlyOpenSubtitlesHosts(t *testing.T) {
	for base, want := range map[string]string{
		"vip-api.opensubtitles.com":                "vip-api.opensubtitles.com",
		"https://vip-api.opensubtitles.com/api/v1": "vip-api.opensubtitles.com",
		"API.OpenSubtitles.com":                    openSubtitlesHost,
		"":                                         openSubtitlesHost,
		"evil.example":                             openSubtitlesHost,
		"opensubtitles.com.evil.example":           openSubtitlesHost,
		"vip-api.opensubtitles.com@evil.example":   openSubtitlesHost,
	} {
		if got := providerHost(base); got != want {
			t.Errorf("providerHost(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestProviderRenewsAnEndedSessionOnce(t *testing.T) {
	searches := 0
	p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/login") {
			w.Write([]byte(`{"token":"renewed","base_url":"api.opensubtitles.com"}`))
			return
		}
		searches++
		if searches == 1 {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("Authorization") != "Bearer renewed" {
			t.Fatal("renewed session not used")
		}
		w.Write([]byte(`{"data":[]}`))
	})
	withAccount(p)
	if _, e := p.Search(context.Background(), "Title", "en", 1); e != nil || searches != 2 || fake.count("/login") != 2 {
		t.Fatal(e, searches, fake.count("/login"))
	}
}

func TestProviderRejectedSignInIsNotRepeatedUntilTheOwnerChecks(t *testing.T) {
	p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	withAccount(p)
	for range 2 {
		if _, e := p.Search(context.Background(), "Title", "en", 1); reason(e) != ReasonSignInFailed || !errors.Is(e, ErrUnavailable) {
			t.Fatal("bad credentials accepted", e)
		}
	}
	if fake.count("/login") != 1 || p.Status().Reason != ReasonSignInFailed || p.Status().Enabled {
		t.Fatal(fake.count("/login"), p.Status())
	}
	if s := p.OwnerStatus(); s.Account != "rejected" || s.Problem != ReasonSignInFailed {
		t.Fatalf("%+v", s)
	}
	// The owner's Test connection tries once more.
	if s := p.Check(context.Background()); s.Account != "rejected" || fake.count("/login") != 2 {
		t.Fatalf("%+v %d", s, fake.count("/login"))
	}
}

func TestProviderKeepsWithinOpenSubtitlesSignInLimits(t *testing.T) {
	p := &OpenSubtitles{}
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)
	for i := range loginsPerMinute {
		if e := p.allowLogin(now.Add(time.Duration(i) * time.Second)); e != nil {
			t.Fatal(i, e)
		}
	}
	e := p.allowLogin(now.Add(15 * time.Second))
	var refused *ProviderError
	if !errors.As(e, &refused) || refused.Reason != ReasonRateLimited || !refused.RetryAt.Equal(now.Add(time.Minute)) {
		t.Fatal("an eleventh sign-in in a minute", e)
	}
	// Within the hour, thirty at most.
	at := now.Add(time.Minute)
	for len(p.logins) < loginsPerHour {
		if e := p.allowLogin(at); e != nil {
			t.Fatal(len(p.logins), e)
		}
		at = at.Add(7 * time.Second)
	}
	if e = p.allowLogin(at.Add(2 * time.Minute)); !errors.As(e, &refused) || !refused.RetryAt.Equal(now.Add(time.Hour)) {
		t.Fatal("a thirty-first sign-in in an hour", e)
	}
	if e = p.allowLogin(now.Add(2 * time.Hour)); e != nil {
		t.Fatal("the window moves on", e)
	}
}

func TestProviderWaitsAfterASignInThatFailedForAnotherReason(t *testing.T) {
	p, fake, clock := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	withAccount(p)
	for range 3 {
		p.Search(context.Background(), "Title", "en", 1)
	}
	if fake.count("/login") != 1 {
		t.Fatal("an unavailable OpenSubtitles is not asked to sign in again at once", fake.count("/login"))
	}
	clock.at = clock.at.Add(loginFailureBackoff)
	p.Search(context.Background(), "Title", "en", 1)
	if fake.count("/login") != 2 {
		t.Fatal(fake.count("/login"))
	}
}

func TestProviderRateLimitIsHonoredAndReported(t *testing.T) {
	p, _, clock := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit-Reset", "7")
		w.WriteHeader(429)
	})
	_, e := p.Search(context.Background(), "Title", "en", 1)
	var refused *ProviderError
	if !errors.As(e, &refused) || refused.Reason != ReasonRateLimited || !refused.RetryAt.Equal(clock.at.Add(7*time.Second)) {
		t.Fatal(e)
	}
	p.mu.Lock()
	next := p.next
	p.mu.Unlock()
	if !next.Equal(clock.at.Add(7 * time.Second)) {
		t.Fatal("the next request waits out the limit", next)
	}
	if s := p.OwnerStatus(); s.Problem != ReasonRateLimited || s.RetryAt == "" {
		t.Fatalf("%+v", s)
	}
}

func TestProviderReportsAUsedUpDownloadQuotaWithItsReset(t *testing.T) {
	p, _, clock := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/login") {
			w.Write([]byte(`{"token":"session","base_url":"api.opensubtitles.com"}`))
			return
		}
		w.WriteHeader(406)
		w.Write([]byte(`{"requests":21,"remaining":-1,"message":"You have downloaded your allowed 20 subtitles for 24h.","reset_time":"3 hours and 12 minutes","reset_time_utc":"2026-10-19T00:00:00.000Z"}`))
	})
	withAccount(p)
	_, e := p.Download(context.Background(), 42)
	var refused *ProviderError
	if !errors.As(e, &refused) || refused.Reason != ReasonQuotaExhausted || !refused.RetryAt.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(e)
	}
	if s := p.OwnerStatus(); s.DownloadsRemaining != 0 || s.DownloadsResetAt != "2026-10-19T00:00:00Z" || s.Problem != ReasonQuotaExhausted {
		t.Fatalf("%+v", s)
	}
	// Without a reset time OpenSubtitles' quota day ends at UTC midnight.
	if at := quotaReset([]byte(`{}`), clock.at); !at.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(at)
	}
	// Once the reset has passed, the old count is no longer claimed.
	clock.at = clock.at.Add(5 * time.Hour)
	if s := p.OwnerStatus(); s.DownloadsRemaining != -1 || s.DownloadsResetAt != "" {
		t.Fatalf("%+v", s)
	}
}

func TestProviderDownloadWithoutAnAccountIsRefusedClearly(t *testing.T) {
	for status, want := range map[int]string{401: ReasonSignInRequired, 403: ReasonKeyRejected} {
		p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
		if _, e := p.Download(context.Background(), 42); reason(e) != want {
			t.Fatal(status, e)
		}
		if fake.count("/login") != 0 {
			t.Fatal("no account, no sign-in")
		}
	}
}

func TestProviderDownloadsAndRecordsTheRemainingQuota(t *testing.T) {
	p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/login"):
			w.Write([]byte(`{"token":"session","base_url":"api.opensubtitles.com","user":{"allowed_downloads":20}}`))
		case strings.HasSuffix(r.URL.Path, "/download"):
			var in map[string]any
			json.NewDecoder(r.Body).Decode(&in)
			if in["file_id"] != float64(42) || in["sub_format"] != "srt" || r.Header.Get("Authorization") != "Bearer session" {
				t.Fatal(in, r.Header)
			}
			w.Write([]byte(`{"link":"https://dl.opensubtitles.com/fixture.srt","file_name":"fixture.srt","requests":3,"remaining":17,"reset_time_utc":"2026-10-19T00:00:00.000Z"}`))
		}
	})
	withAccount(p)
	fetched := ""
	p.fetch = func(_ context.Context, link string) ([]byte, error) {
		fetched = link
		return []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), nil
	}
	raw, e := p.Download(context.Background(), 42)
	if e != nil || !strings.Contains(string(raw), "Hello") || fetched != "https://dl.opensubtitles.com/fixture.srt" || fake.count("/download") != 1 {
		t.Fatal(e, fetched)
	}
	if s := p.OwnerStatus(); s.DownloadsRemaining != 17 || s.DownloadsAllowed != 20 || s.DownloadsResetAt != "2026-10-19T00:00:00Z" {
		t.Fatalf("%+v", s)
	}
	// A link that is not plain https is never followed.
	p2, _, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"link":"http://dl.opensubtitles.com/x.srt"}`))
	})
	p2.fetch = p.fetch
	if _, e := p2.Download(context.Background(), 1); e != ErrUnavailable {
		t.Fatal(e)
	}
}

func TestProviderCheckTestsTheKeyOrTheAccount(t *testing.T) {
	p, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) })
	if s := p.Check(context.Background()); s.Problem != "" || s.CheckedAt == "" || s.KeySource != "environment" || s.Account != "none" || fake.count("/subtitles") != 1 {
		t.Fatalf("%+v", s)
	}
	bad, _, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	if s := bad.Check(context.Background()); s.Problem != ReasonKeyRejected {
		t.Fatalf("%+v", s)
	}
	account, fake, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/login") {
			w.Write([]byte(`{"token":"session","base_url":"api.opensubtitles.com"}`))
			return
		}
		w.Write([]byte(`{"data":{"allowed_downloads":20,"remaining_downloads":12,"level":"Sub leecher","vip":false}}`))
	})
	withAccount(account)
	if s := account.Check(context.Background()); s.Account != "signed_in" || s.DownloadsRemaining != 12 || s.DownloadsAllowed != 20 || fake.count("/infos/user") != 1 {
		t.Fatalf("%+v", s)
	}
	unreachable, _, _ := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) })
	if s := unreachable.Check(context.Background()); s.Problem != ReasonUnreachable {
		t.Fatalf("%+v", s)
	}
}

func TestProviderTakesTheOwnersCredentialsAndFallsBackToTheEnvironment(t *testing.T) {
	p := &OpenSubtitles{key: "env-key", environment: openSubtitlesCredentials{key: "env-key"}, credentials: openSubtitlesCredentials{key: "env-key"}, gate: make(chan struct{}, 1)}
	if !p.Status().Enabled || p.OwnerStatus().KeySource != "environment" {
		t.Fatal("the environment's key should be in force")
	}
	p.SetCredentials("owner-key", "owner", "secret")
	p.adoptCredentials()
	if p.key != "owner-key" || p.username != "owner" || p.password != "secret" || p.token != "" {
		t.Fatalf("owner credentials not adopted: %q %q", p.key, p.username)
	}
	if s := p.OwnerStatus(); s.KeySource != "owner" || s.AccountSource != "owner" || s.Account != "not_checked" {
		t.Fatalf("%+v", s)
	}
	p.loginRejected.Store(true)
	p.SetCredentials("", "", "")
	p.adoptCredentials()
	if p.key != "env-key" || p.username != "" || p.loginRejected.Load() {
		t.Fatalf("clearing should fall back to the environment: %q %q", p.key, p.username)
	}
	p.SetCredentials("", "half", "")
	if s := p.OwnerStatus(); s.Account != "incomplete" || s.KeySource != "environment" {
		t.Fatalf("a username without a password is not an account: %+v", s)
	}
	empty := &OpenSubtitles{gate: make(chan struct{}, 1)}
	empty.SetCredentials("", "", "")
	if empty.Status().Reason != ReasonNotConfigured || empty.OwnerStatus().KeySource != "none" {
		t.Fatalf("no key anywhere: %+v", empty.Status())
	}
	if _, err := empty.Search(context.Background(), "x", "en", 1); reason(err) != ReasonNotConfigured || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unconfigured provider must not call out: %v", err)
	}
}

func TestProviderSessionExpiryFollowsTheToken(t *testing.T) {
	now := time.Date(2026, 10, 18, 20, 0, 0, 0, time.UTC)
	if got := tokenExpiry(sessionToken(now.Add(24*time.Hour)), now); !got.Equal(now.Add(24*time.Hour - 5*time.Minute)) {
		t.Fatal(got)
	}
	for _, token := range []string{"opaque", sessionToken(now.Add(-time.Hour)), sessionToken(now.Add(30 * 24 * time.Hour)), "a.%%%.c"} {
		if got := tokenExpiry(token, now); !got.Equal(now.Add(openSubtitlesTokenLife)) {
			t.Fatal(token, got)
		}
	}
}

func TestViewerProviderStatusProjectsOnlyObservedReasonsAndRetryTimes(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	p := &OpenSubtitles{key: "test-key", credentials: openSubtitlesCredentials{key: "test-key"}}
	p.now = func() time.Time { return now }
	for _, reason := range []string{ReasonKeyRejected, ReasonSignInRequired, ReasonQuotaExhausted, ReasonRateLimited, ReasonUnreachable} {
		p.observed = providerObservation{problem: reason, at: now, retryAt: now.Add(time.Minute), resetAt: now.Add(time.Hour)}
		got := p.Status()
		if got.Enabled || got.Reason != reason || got.RetryAt == "" {
			t.Fatalf("%s: %+v", reason, got)
		}
		if reason == ReasonQuotaExhausted && got.ResetsAt == "" {
			t.Fatal("quota reset omitted")
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "test-key") {
			t.Fatal("credential leaked")
		}
	}
	p.observed = providerObservation{quotaKnown: true, remaining: 0, resetAt: now.Add(time.Hour)}
	if got := p.Status(); got.Enabled || got.Reason != ReasonQuotaExhausted || got.ResetsAt == "" {
		t.Fatalf("last successful download exhausted quota: %+v", got)
	}
	p.observed = providerObservation{problem: ReasonQuotaExhausted, resetAt: now.Add(-time.Second)}
	if !p.Status().Enabled || p.Status().ResetsAt != "" {
		t.Fatal("expired quota blocks search", p.Status())
	}
	p.observed = providerObservation{problem: ReasonRateLimited, retryAt: now.Add(-time.Second)}
	if !p.Status().Enabled {
		t.Fatal("expired rate limit blocks search", p.Status())
	}
}
