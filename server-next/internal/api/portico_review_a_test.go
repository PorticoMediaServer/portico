package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Review desk A, #2676. A "503" or "429" that carries no Retry-After (a proxy or a rate
// limiter in front of Portico Services answers that way when Services itself is down or
// overloaded) is not Services setting the pace. Today it is retried every 30 seconds for as
// long as it lasts, with no backoff, where no answer at all backs off to hours. Two days of
// it must cost a handful of calls, as two days of no answer do
// (TestTheDailyCheckInAndAnOutage), not thousands.
func TestReviewABusyAnswerWithNoWaitBacksOff(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	p.run(time.Hour)
	var calls atomic.Int64
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer busy.Close()
	attach(p.s, busy.Client(), busy.URL, nil, func() time.Time { return p.clock.Now() })
	p.run(48 * time.Hour)
	if n := calls.Load(); n > 40 {
		t.Errorf("two days of '503' with no Retry-After made %d calls to Portico Services; two days of no answer make about a dozen", n)
	}
}
