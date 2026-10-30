package metadataprovider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// A refresh of an unchanged document should cost a request header and a 304, not a document.
// Nothing here asked before, so every refresh re-downloaded and re-parsed a document identical
// to the one already stored, and spent the provider's allowance doing it.
func TestAConditionalRequestSendsTheStoredValidatorsAndReadsA304(t *testing.T) {
	var sawMatch, sawSince string
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		sawMatch, sawSince = r.Header.Get("If-None-Match"), r.Header.Get("If-Modified-Since")
		if sawMatch == `W/"v1"` {
			w.Header().Set("ETag", `W/"v1"`)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Header().Set("Last-Modified", "Wed, 17 Sep 2026 00:00:00 GMT")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7}`))
	})
	ctx := context.Background()
	// First time round there is nothing to ask with, so it is an ordinary request and the
	// validators come back for next time.
	raw, out, e := tr.rawConditional(ctx, nil, "", "/thing", "", 1<<20, Conditional{})
	if e != nil || string(raw) != `{"id":7}` {
		t.Fatal(string(raw), e)
	}
	if sawMatch != "" || sawSince != "" {
		t.Fatal("an empty validator was sent as a header", sawMatch, sawSince)
	}
	if out.ETag != `W/"v1"` || out.LastModified != "Wed, 17 Sep 2026 00:00:00 GMT" {
		t.Fatal("the response's validators were not read", out)
	}
	// Second time round the stored validators go out and the answer is 304 with no body.
	raw, next, e := tr.rawConditional(ctx, nil, "", "/thing", "", 1<<20, out)
	if !errors.Is(e, ErrNotModified) || raw != nil {
		t.Fatal("a 304 was not reported as not-modified", string(raw), e)
	}
	if sawMatch != `W/"v1"` || sawSince != "Wed, 17 Sep 2026 00:00:00 GMT" {
		t.Fatal("the stored validators were not sent", sawMatch, sawSince)
	}
	// A 304 that repeats only the ETag must not lose the Last-Modified that is still valid;
	// forgetting it would turn every second refresh back into a full download.
	if next.ETag != `W/"v1"` || next.LastModified != "Wed, 17 Sep 2026 00:00:00 GMT" {
		t.Fatal("a validator was lost across a not-modified response", next)
	}
}

// Everything else behaves exactly as the unconditional call does, including the Retry-After
// deferral that a refused request earns.
func TestAConditionalRequestKeepsTheOrdinaryRefusalBehaviour(t *testing.T) {
	tr := fixtureTransport(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, out, e := tr.rawConditional(context.Background(), nil, "", "/thing", "", 1<<20, Conditional{ETag: `W/"v1"`})
	errorCode(t, e, "request_rejected")
	var problem *Error
	if !errors.As(e, &problem) || problem.RetryAfter <= 0 {
		t.Fatal("a refusal lost its Retry-After", e)
	}
	// A refusal is not an answer about the document, so the stored validators are unchanged.
	if out.ETag != `W/"v1"` {
		t.Fatal("a refusal discarded the stored validators", out)
	}
}

// A validator is stored and sent back verbatim, so it must be bounded and free of anything
// that could forge a header.
func TestValidatorsAreBoundedAndCannotForgeAHeader(t *testing.T) {
	for _, bad := range []string{strings.Repeat("a", validatorLimit+1), "one\r\nInjected: yes", "null\x00byte"} {
		if validator(bad) != "" {
			t.Fatal("accepted", bad[:min(len(bad), 20)])
		}
	}
	if validator(` W/"v1" `) != `W/"v1"` {
		t.Fatal("a normal validator was not kept")
	}
	if !(Conditional{}).Empty() || (Conditional{ETag: "x"}).Empty() || (Conditional{LastModified: "x"}).Empty() {
		t.Fatal("emptiness")
	}
	// An over-long validator never reaches the wire.
	var sent string
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("If-None-Match")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	if _, _, e := tr.rawConditional(context.Background(), nil, "", "/thing", "", 1<<20, Conditional{ETag: strings.Repeat("a", validatorLimit+1)}); e != nil {
		t.Fatal(e)
	}
	if sent != "" {
		t.Fatal("an over-long validator was sent", len(sent))
	}
}

// The provider adapter on top of it: TMDB's conditional detail call.
func TestTMDBConditionalScreenDetails(t *testing.T) {
	requests := 0
	s, e := NewTMDB("token")
	if e != nil {
		t.Fatal(e)
	}
	s.http = fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `W/"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if !strings.Contains(r.URL.Path, "/movie/42") {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Header().Set("ETag", `W/"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"title":"Film","release_date":"2008-01-01"}`))
	})
	ctx := context.Background()
	r, out, e := s.ScreenDetailsConditional(ctx, "movie", "42", "en-US", "CA", Conditional{})
	if e != nil || r.Identity.ID != "42" || r.Title != "Film" || out.ETag != `W/"v1"` {
		t.Fatal(r, out, e)
	}
	if _, next, err := s.ScreenDetailsConditional(ctx, "movie", "42", "en-US", "CA", out); !errors.Is(err, ErrNotModified) || next.ETag != `W/"v1"` {
		t.Fatal("TMDB's not-modified was not passed through", next, err)
	}
	if requests != 2 {
		t.Fatal("the response cache swallowed a refresh", requests)
	}
	// An identity the provider does not answer with is refused rather than published.
	s.http = fixtureTransport(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":99,"title":"Other","release_date":"2008-01-01"}`))
	})
	if _, _, err := s.ScreenDetailsConditional(ctx, "movie", "42", "", "", Conditional{}); err == nil {
		t.Fatal("a mismatched identity was accepted")
	}
	if _, _, err := s.ScreenDetailsConditional(ctx, "not-a-kind", "42", "", "", Conditional{}); err == nil {
		t.Fatal("an invalid kind was accepted")
	}
}
