package administration

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

type cachedUpdateFeed struct {
	data    []byte
	err     error
	checked time.Time
	// retryUntil is when the feed itself said it may be asked again: the
	// provider's own Retry-After, kept so both the automatic refresh and a
	// pressed check wait exactly as long as the feed asked.
	retryUntil time.Time
	// inFlight says a check is running, and wait is closed when its attempt ends,
	// so a concurrent check joins it rather than starting a second request.
	inFlight bool
	wait     chan struct{}
}

const (
	// updateFetchTimeout bounds one feed read. It sits inside the admin-heavy
	// lane's request budget, so a pressed check can finish before admission ends.
	updateFetchTimeout = 8 * time.Second
	// updateSuccessRefresh is how long a successful answer stands before the read
	// path refreshes it.
	updateSuccessRefresh = 6 * time.Hour
	// updateFailureBackoff is how long a failed attempt stands before either path
	// asks the feed again.
	updateFailureBackoff = 5 * time.Minute
	// updateCheckFloor coalesces presses of Check now after a successful check.
	// It never shortens the failure backoff and never overrides a provider wait.
	updateCheckFloor = 15 * time.Second
)

// feedRetryError is the wait a release feed asked for when it refused a check
// (429 or 503 with Retry-After). It carries the provider's own wait through the
// fetch so both paths can honour it instead of inventing one.
type feedRetryError struct{ Wait time.Duration }

func (e *feedRetryError) Error() string {
	return "release feed asked to be left alone for " + e.Wait.String()
}

// maxFeedWait is the largest wait this server can add to a time and still keep a
// time: a header that asks for more is not a wait it can honour.
const maxFeedWait = time.Duration(1<<63 - 1)

// parseRetryAfter reads the two forms the header allows: delay seconds, or an
// HTTP date. The text is bounded and the number is range-checked before it is
// multiplied, so a large but valid integer can never wrap into a negative wait.
// A value this server cannot read is not a wait.
func parseRetryAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 64 {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds <= 0 || seconds > int64(maxFeedWait/time.Second) {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	if wait := time.Until(at); wait > 0 {
		return wait, true
	}
	return 0, false
}

// FetchReleaseFeed reads a bounded owner-configured feed. Custom feeds stay
// within their configured origin. Only the official Portico GitHub manifest
// may cross to GitHub's HTTPS release asset host; every hop is independently
// admitted by the remote-media policy (including public-only asset DNS).
func FetchReleaseFeed(ctx context.Context, rawURL string) ([]byte, error) {
	return fetchReleaseFeed(ctx, rawURL, remotemedia.Policy{ReadTimeout: 8 * time.Second})
}

func fetchReleaseFeed(ctx context.Context, rawURL string, policy remotemedia.Policy) ([]byte, error) {
	if !safeFeedURL(rawURL) {
		return nil, ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	initial, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrInput
	}
	policy.ManualRedirects = true
	// An owner may configure a private feed. Pin only that initial origin;
	// never grant private-network approval to a redirected GitHub asset host.
	if !officialGitHubFeed(initial) {
		approval, err := policy.Approve(ctx, initial.Scheme+"://"+initial.Host+"/")
		if err != nil {
			return nil, err
		}
		policy.Approvals = []remotemedia.Approval{approval}
	}
	current := initial
	for hop := 0; hop < 5; hop++ {
		client, err := remotemedia.New(ctx, current.String(), policy)
		if err != nil {
			return nil, err
		}
		response, err := client.Open(ctx, http.MethodGet, "")
		if err != nil {
			client.Close()
			return nil, err
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			wait, named := parseRetryAfter(response.Header.Get("Retry-After"))
			response.Body.Close()
			client.Close()
			if named {
				return nil, &feedRetryError{Wait: wait}
			}
			return nil, remotemedia.ErrPolicy
		}
		if response.StatusCode == http.StatusOK {
			data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
			response.Body.Close()
			client.Close()
			if err != nil {
				return nil, err
			}
			if len(data) > 1<<20 {
				return nil, errors.New("release feed exceeds byte limit")
			}
			return data, nil
		}
		next, err := response.Location()
		response.Body.Close()
		client.Close()
		if err != nil || !feedRedirectAllowed(initial, current, next) {
			return nil, remotemedia.ErrPolicy
		}
		current = next
	}
	return nil, remotemedia.ErrPolicy
}

var githubFeedPath = regexp.MustCompile(`^/PorticoMediaServer/portico-server/releases/(latest/download|download/v[0-9][0-9A-Za-z.+-]*)/portico-update-manifest\.json$`)
var githubAssetPath = regexp.MustCompile(`^/github-production-release-asset/[0-9]+/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func httpsFeedHost(u *url.URL, host string) bool {
	return u != nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), host) &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.Fragment == "" && u.RawPath == ""
}

func officialGitHubFeed(u *url.URL) bool {
	return httpsFeedHost(u, "github.com") && u.RawQuery == "" && githubFeedPath.MatchString(u.Path)
}

func feedOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

func feedRedirectAllowed(initial, current, next *url.URL) bool {
	if next == nil || !safeFeedURL(next.String()) || next.Fragment != "" ||
		(current.Scheme == "https" && next.Scheme != "https") {
		return false
	}
	if officialGitHubFeed(initial) {
		// GitHub's latest URL first resolves to a tagged asset in this repo,
		// then to its signed CDN URL. The CDN cannot redirect onward.
		return officialGitHubFeed(current) && (officialGitHubFeed(next) ||
			(httpsFeedHost(next, "release-assets.githubusercontent.com") && githubAssetPath.MatchString(next.Path)))
	}
	return feedOrigin(initial) == feedOrigin(next)
}

func safeFeedURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.User == nil && u.Hostname() != "" && (u.Scheme == "https" || u.Scheme == "http")
}

// CachedUpdates never performs network I/O on the API request. The first read
// starts one background check; subsequent reads use the last result. A
// successful answer stands six hours and a failed attempt five minutes, and a
// feed that named its own Retry-After is not asked again before it. A changed
// feed URL gets its own cache entry.
func (s *Service) CachedUpdates(ctx context.Context, current UpdateBuild, channel, feedURL string) UpdateReport {
	if feedURL == "" || s.Fetch == nil {
		return s.updatesFrom(ctx, current, channel, feedURL, nil)
	}
	s.updateMu.Lock()
	entry := s.updateFeedEntry(feedURL)
	if !entry.inFlight && s.updateCheckDue(entry, false) {
		prior := s.updateReport(ctx, current, channel, feedURL, entry)
		wait := s.beginUpdateCheck(entry)
		fetch := s.Fetch
		supervise.Go("administration.update-feed", func() {
			fetchCtx, cancel := context.WithTimeout(context.Background(), updateFetchTimeout)
			defer cancel()
			fresh := []byte(nil)
			err := errors.New("release feed check interrupted")
			// The answer is stored even when the fetch stops part way, so the entry
			// cannot stay in flight and hold off every later check.
			defer func() {
				checkedAt := s.now()
				s.updateMu.Lock()
				next := s.cachedUpdateReport(fetchCtx, current, channel, feedURL, fresh, err, checkedAt)
				s.storeUpdateAnswer(entry, wait, fresh, err, checkedAt)
				s.updateMu.Unlock()
				eventCtx, eventCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer eventCancel()
				if eventErr := s.publishUpdateTransition(eventCtx, prior, next); eventErr != nil {
					log.Printf("Cached release transition event could not be published: %v", eventErr)
				}
			}()
			fresh, err = fetch(fetchCtx, feedURL)
		})
	}
	known := !entry.checked.IsZero()
	data, priorErr, checked := entry.data, entry.err, entry.checked
	s.updateMu.Unlock()
	if !known {
		out := s.updatesFrom(ctx, current, channel, feedURL, nil)
		out.Message = "The release feed is being checked in the background."
		out.CheckedAt = ""
		return out
	}
	out := s.updatesFrom(ctx, current, channel, feedURL, func(context.Context, string) ([]byte, error) { return data, priorErr })
	out.CheckedAt = checked.UTC().Format(time.RFC3339)
	return out
}

// CheckUpdates answers one explicit release-feed check. It never contacts a feed
// the owner turned off, and it joins a check already running instead of adding a
// second request upstream. It answers from the cache while the last attempt is
// still standing: a failure keeps its five-minute backoff, a successful answer
// coalesces presses for fifteen seconds, and a feed that named its own wait is
// honoured whichever path asks. A fresh answer is stored where every read report
// looks, so the next read shows it without another check.
func (s *Service) CheckUpdates(ctx context.Context, current UpdateBuild, channel, feedURL string) UpdateReport {
	if feedURL == "" || s.Fetch == nil {
		return s.updatesFrom(ctx, current, channel, feedURL, nil)
	}
	s.updateMu.Lock()
	entry := s.updateFeedEntry(feedURL)
	if entry.inFlight && entry.wait != nil {
		wait := entry.wait
		s.updateMu.Unlock()
		// Join the running check. A caller that gives up first still answers with
		// the last finished answer, never with an invented one.
		select {
		case <-wait:
		case <-ctx.Done():
		}
		s.updateMu.Lock()
		out := s.updateReport(ctx, current, channel, feedURL, entry)
		s.updateMu.Unlock()
		return out
	}
	if !s.updateCheckDue(entry, true) {
		out := s.updateReport(ctx, current, channel, feedURL, entry)
		s.updateMu.Unlock()
		return out
	}
	prior := s.updateReport(ctx, current, channel, feedURL, entry)
	wait := s.beginUpdateCheck(entry)
	fetch := s.Fetch
	s.updateMu.Unlock()

	// Exactly one attempt owns this channel. It is released on every exit, the
	// panic one included: a contained panic in the feed read must not leave later
	// checks waiting on a channel nobody closes.
	answered := false
	release := func() {
		if answered {
			return
		}
		answered = true
		s.updateMu.Lock()
		if entry.wait == wait {
			entry.inFlight, entry.wait = false, nil
		}
		s.updateMu.Unlock()
		close(wait)
	}
	defer release()

	fetchCtx, cancel := context.WithTimeout(ctx, updateFetchTimeout)
	// A contained panic in the read cancels its work and its timer too.
	defer cancel()
	fresh, err := fetch(fetchCtx, feedURL)
	cancel()
	if ctx.Err() != nil {
		// The caller went away before the feed answered. That says nothing about
		// the feed, so nothing is recorded and the next check may ask again.
		s.updateMu.Lock()
		out := s.updateReport(ctx, current, channel, feedURL, entry)
		s.updateMu.Unlock()
		release()
		return out
	}
	checkedAt := s.now()
	next := s.cachedUpdateReport(ctx, current, channel, feedURL, fresh, err, checkedAt)
	s.updateMu.Lock()
	answered = true
	s.storeUpdateAnswer(entry, wait, fresh, err, checkedAt)
	s.updateMu.Unlock()
	// The answer is already in the cache, so the transition notice does not hold
	// the response: it commits under its own bound, as the read path's does.
	supervise.Go("administration.update-transition", func() {
		eventCtx, eventCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer eventCancel()
		if eventErr := s.publishUpdateTransition(eventCtx, prior, next); eventErr != nil {
			log.Printf("Cached release transition event could not be published: %v", eventErr)
		}
	})
	return next
}

// updateFeedEntry is the cache entry for one feed URL. The caller holds updateMu.
func (s *Service) updateFeedEntry(feedURL string) *cachedUpdateFeed {
	if s.updateFeeds == nil {
		s.updateFeeds = make(map[string]*cachedUpdateFeed)
	}
	entry := s.updateFeeds[feedURL]
	if entry == nil {
		entry = &cachedUpdateFeed{}
		s.updateFeeds[feedURL] = entry
	}
	return entry
}

// updateReport is the last finished answer for one entry, or the honest
// "being checked" answer when no check has finished yet. The caller holds
// updateMu.
func (s *Service) updateReport(ctx context.Context, current UpdateBuild, channel, feedURL string, entry *cachedUpdateFeed) UpdateReport {
	if entry.checked.IsZero() {
		out := s.updatesFrom(ctx, current, channel, feedURL, nil)
		out.Message = "The release feed is being checked."
		out.CheckedAt = ""
		return out
	}
	return s.cachedUpdateReport(ctx, current, channel, feedURL, entry.data, entry.err, entry.checked)
}

// beginUpdateCheck marks a check in flight and returns the channel a concurrent
// check waits on. The caller holds updateMu.
func (s *Service) beginUpdateCheck(entry *cachedUpdateFeed) chan struct{} {
	entry.inFlight = true
	entry.wait = make(chan struct{})
	return entry.wait
}

// storeUpdateAnswer records one finished attempt and releases anyone waiting on
// it. The caller holds updateMu. A feed that named its own wait is remembered
// here, because what it said outlives this one request.
func (s *Service) storeUpdateAnswer(entry *cachedUpdateFeed, wait chan struct{}, data []byte, err error, checked time.Time) {
	entry.data, entry.err, entry.checked = data, err, checked
	entry.retryUntil = time.Time{}
	var named *feedRetryError
	if errors.As(err, &named) && named.Wait > 0 {
		entry.retryUntil = checked.Add(named.Wait)
	}
	entry.inFlight, entry.wait = false, nil
	close(wait)
}

// updateCheckDue says whether either path may ask the feed now. The feed's own
// Retry-After always wins; a failed attempt keeps the existing five-minute
// backoff; a successful answer stands six hours for a read and the short
// coalescing floor for a pressed check. The caller holds updateMu.
func (s *Service) updateCheckDue(entry *cachedUpdateFeed, pressed bool) bool {
	now := s.now()
	if now.Before(entry.retryUntil) {
		return false
	}
	if entry.checked.IsZero() {
		return true
	}
	if entry.err != nil {
		return now.Sub(entry.checked) >= updateFailureBackoff
	}
	if pressed {
		return now.Sub(entry.checked) >= updateCheckFloor
	}
	return now.Sub(entry.checked) >= updateSuccessRefresh
}
