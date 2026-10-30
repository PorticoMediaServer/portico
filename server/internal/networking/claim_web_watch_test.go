package networking

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The server waits for the Portico Account's decision itself once the web
// approval starts: a page that waits joins that one wait (one Hosted stream),
// and a stream that breaks before a decision lets the next wait ask again.
func TestWebClaimWatchBelongsToTheServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, h, _ := handlerFixture(t)
	httpTr, e := NewHTTPTransport("https://hosted.example", &credentialFixture{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(httpTr.CloseIdleConnections)
	var streams atomic.Int32
	release := make(chan string, 2)
	httpTr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/server-claims/pending":
			code := strings.Repeat("a", 49)
			return reply(200, `{"code":"`+code+`","serverId":"s","name":"Test server","address":"https://web.example","requestedAt":"2026-10-15T00:00:00Z","expiresAt":"2026-10-15T00:10:00Z","status":"pending"}`), nil
		case "/v1/server-claims/events":
			streams.Add(1)
			select {
			case answer := <-release:
				if answer == "" {
					return reply(502, `{}`), nil
				}
				return reply(200, answer), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		t.Errorf("unexpected Hosted call %s", r.URL.Path)
		return reply(500, `{}`), nil
	})
	h.transport = httpTr

	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 {
		t.Fatalf("initial: %d", code)
	}
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); code != 200 || s.Operation == nil {
		t.Fatalf("prepare: %d %+v", code, s)
	}
	expected := struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation}
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/web-approval", expected); code != 200 || s.ClaimCode == "" {
		t.Fatalf("web approval: %d %+v", code, s)
	}
	expected.Expected = *s.Operation
	waitFor(t, func() bool { return streams.Load() == 1 })

	// The stream breaks before any decision: the watch ends and the next wait asks again.
	release <- ""
	waitFor(t, func() bool { _, running := h.webWatches.Load(expected.Expected.OperationID); return !running })

	await := func() string {
		raw, _ := json.Marshal(expected)
		r := httptest.NewRequest("POST", "/v1/networking/claim/await-approval", bytes.NewReader(raw))
		r.Header.Set("Origin", "https://web.example")
		r.Header.Set("Authorization", "Bearer current")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.awaitWebClaim(w, r)
		return strings.TrimSpace(w.Body.String())
	}
	var wg sync.WaitGroup
	answers := make([]string, 2)
	for i := range answers {
		wg.Add(1)
		go func() { defer wg.Done(); answers[i] = await() }()
	}
	waitFor(t, func() bool { return streams.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	release <- `{"status":"closed"}`
	wg.Wait()
	for _, a := range answers {
		if a != `{"state":"approval_closed"}` {
			t.Fatalf("answer: %q", a)
		}
	}
	if n := streams.Load(); n != 2 {
		t.Fatalf("two pages waiting should share one Hosted stream; streams=%d", n)
	}
	// A late page reads the decision the watch left instead of asking Hosted again.
	if a := await(); a != `{"state":"approval_closed"}` || streams.Load() != 2 {
		t.Fatalf("late answer %q streams=%d", a, streams.Load())
	}
}

// A pending approval is waited on again after a restart, under the asking
// owner's guard rebuilt from the note the wait saved.
func TestWebClaimWatchResumesAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, h, _ := handlerFixture(t)
	httpTr, e := NewHTTPTransport("https://hosted.example", &credentialFixture{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(httpTr.CloseIdleConnections)
	var streams atomic.Int32
	release := make(chan string, 2)
	httpTr.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/server-claims/pending":
			code := strings.Repeat("a", 49)
			return reply(200, `{"code":"`+code+`","serverId":"s","name":"Test server","address":"https://web.example","requestedAt":"2026-10-15T00:00:00Z","expiresAt":"2026-10-15T00:10:00Z","status":"pending"}`), nil
		case "/v1/server-claims/events":
			streams.Add(1)
			select {
			case answer := <-release:
				if answer == "" {
					return reply(502, `{}`), nil
				}
				return reply(200, answer), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		t.Errorf("unexpected Hosted call %s", r.URL.Path)
		return reply(500, `{}`), nil
	})
	h.transport = httpTr
	owner := h.authorize
	var guard ClaimRequestGuard
	h.authorize = func(r *http.Request) (ClaimRequestOwner, error) {
		o, e := owner(r)
		o.Resume, guard = "the-owner", o.Guard
		return o, e
	}

	s, code := handlerCall(t, h, "GET", "/v1/networking/claim", nil)
	if code != 200 {
		t.Fatalf("initial: %d", code)
	}
	body := json.RawMessage(`{"expected":{"serverId":"` + s.Identity.ServerID + `","localGeneration":"` + strconv.FormatInt(s.Identity.LocalGeneration, 10) + `"}}`)
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/prepare", body); code != 200 || s.Operation == nil {
		t.Fatalf("prepare: %d %+v", code, s)
	}
	expected := struct {
		Expected ClaimExpected `json:"expected"`
	}{*s.Operation}
	if s, code = handlerCall(t, h, "POST", "/v1/networking/claim/web-approval", expected); code != 200 || s.ClaimCode == "" {
		t.Fatalf("web approval: %d %+v", code, s)
	}
	operation := s.Operation.OperationID
	waitFor(t, func() bool { return streams.Load() == 1 })
	// The server stops: its wait ends with no decision.
	release <- ""
	waitFor(t, func() bool { _, running := h.webWatches.Load(operation); return !running })

	var notes []string
	h.ResumeWebClaims(t.Context(), func(note string) ClaimRequestGuard { notes = append(notes, note); return guard })
	if len(notes) != 1 || notes[0] != "the-owner" {
		t.Fatalf("resume notes %v", notes)
	}
	waitFor(t, func() bool { return streams.Load() == 2 })
	current, ok := h.webWatches.Load(operation)
	if !ok {
		t.Fatal("no wait after the restart")
	}
	release <- `{"status":"closed"}`
	<-current.(*webClaimWatch).done
	if a := string(current.(*webClaimWatch).answer); a != `{"state":"approval_closed"}` {
		t.Fatalf("answer %q", a)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
