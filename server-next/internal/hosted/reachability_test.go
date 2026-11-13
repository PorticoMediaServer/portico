package hosted

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

func TestOutsideProofIsSignedAndLimitedToSixOwnerChecksAnHour(t *testing.T) {
	public, key, _ := ed25519.GenerateKey(nil)
	now := time.Now().UTC()
	calls := 0
	result := ReachabilityResult{CheckedAt: now, Addresses: []ReachabilityAddress{{Address: "203.0.113.1", Port: 32500, Reachable: true}}}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/servers/server/reachability" {
			t.Error("wrong request", r.Method, r.URL.Path)
		}
		if err := trust.VerifyRequest(public, r.Method, r.URL.RequestURI(), r.Header.Get(trust.RequestTimeHeader),
			r.Header.Get(trust.RequestSignatureHeader), nil, now); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer fake.Close()
	c := New(Options{Origin: fake.URL, HTTP: fake.Client(), ServerID: "server", Key: key, Now: func() time.Time { return now }})
	for range 6 {
		out, err := c.Reachable(context.Background(), now)
		if err != nil || !out.CheckedAt.Equal(result.CheckedAt) || len(out.Addresses) != 1 || out.Addresses[0] != result.Addresses[0] {
			t.Fatal(out, err)
		}
	}
	var busy *BusyError
	if _, err := c.Reachable(context.Background(), now); !errors.As(err, &busy) || busy.Wait != time.Hour || calls != 6 {
		t.Fatal(calls, err)
	}
	now = now.Add(time.Hour)
	if _, err := c.Reachable(context.Background(), now); err != nil || calls != 7 {
		t.Fatal(calls, err)
	}
}

func TestOutsideProofHonoursServicesRetryAfterAndDoesNotTreat404AsUnreachable(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	now := time.Now().UTC()
	calls, status := 0, 429
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "7200")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"code":"slow_down"}}`))
	}))
	defer fake.Close()
	c := New(Options{Origin: fake.URL, HTTP: fake.Client(), ServerID: "server", Key: key, Now: func() time.Time { return now }})
	if _, err := c.Reachable(context.Background(), now); err == nil {
		t.Fatal("a refusal became a completed check")
	}
	now = now.Add(time.Hour)
	var busy *BusyError
	if _, err := c.Reachable(context.Background(), now); !errors.As(err, &busy) || busy.Wait != time.Hour || calls != 1 {
		t.Fatal(calls, err)
	}
	now = now.Add(time.Hour)
	status = 404
	if out, err := c.Reachable(context.Background(), now); err == nil || !out.CheckedAt.IsZero() || out.Addresses != nil {
		t.Fatal("missing route became unreachable", out, err)
	}
}

type reachabilityTimeoutTransport struct{ t *testing.T }

func (tr reachabilityTimeoutTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	deadline, ok := r.Context().Deadline()
	if !ok || time.Until(deadline) > 10*time.Second {
		tr.t.Fatal("outside proof has no bounded deadline", deadline)
	}
	return nil, context.DeadlineExceeded
}

func TestOutsideProofTimeoutNeverBecomesAnUnreachableAnswer(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	c := New(Options{Origin: "https://services.invalid", HTTP: &http.Client{Transport: reachabilityTimeoutTransport{t}}, ServerID: "server", Key: key})
	out, err := c.Reachable(context.Background(), time.Now())
	if !errors.Is(err, ErrNotConnected) || !out.CheckedAt.IsZero() || out.Addresses != nil {
		t.Fatal("timeout became a completed outside check", out, err)
	}
}
