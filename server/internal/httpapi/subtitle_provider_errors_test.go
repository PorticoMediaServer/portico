package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func TestSubtitleProviderRefusalsReachTheViewerByCode(t *testing.T) {
	cases := []struct {
		refusal *subtitles.ProviderError
		code    string
		status  int
		wait    time.Duration
	}{
		{&subtitles.ProviderError{Reason: subtitles.ReasonNotConfigured}, "subtitle_provider_not_configured", 503, 0},
		{&subtitles.ProviderError{Reason: subtitles.ReasonKeyRejected}, "subtitle_provider_key_rejected", 503, 0},
		{&subtitles.ProviderError{Reason: subtitles.ReasonSignInFailed}, "subtitle_provider_sign_in_failed", 503, 0},
		{&subtitles.ProviderError{Reason: subtitles.ReasonSignInRequired}, "subtitle_provider_sign_in_required", 503, 0},
		{&subtitles.ProviderError{Reason: subtitles.ReasonQuotaExhausted, RetryAt: time.Now().Add(3 * time.Hour)}, "subtitle_provider_quota", 429, 3 * time.Hour},
		{&subtitles.ProviderError{Reason: subtitles.ReasonRateLimited, RetryAt: time.Now().Add(7 * time.Second)}, "subtitle_provider_busy", 429, 7 * time.Second},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		subtitleFailure(w, c.refusal)
		var body struct {
			Error struct {
				Code         string `json:"code"`
				RetryAfterMs int64  `json:"retryAfterMs"`
			} `json:"error"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != c.status || body.Error.Code != c.code {
			t.Fatalf("%s: %d %s %v", c.refusal.Reason, w.Code, w.Body, e)
		}
		seconds, _ := strconv.Atoi(w.Header().Get("Retry-After"))
		if c.wait == 0 && seconds != 0 || c.wait > 0 && (time.Duration(seconds)*time.Second < c.wait-2*time.Second || time.Duration(seconds)*time.Second > c.wait+time.Second) {
			t.Fatalf("%s: Retry-After %q for a wait of %s", c.refusal.Reason, w.Header().Get("Retry-After"), c.wait)
		}
	}
	// A provider that simply did not answer stays the general, retryable failure.
	w := httptest.NewRecorder()
	subtitleFailure(w, subtitles.ErrUnavailable)
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
		t.Fatal(w.Code, w.Header())
	}
}
