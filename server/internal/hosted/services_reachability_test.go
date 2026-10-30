package hosted

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type reachTransport func(*http.Request) (*http.Response, error)

func (f reachTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestServicesReachabilityRequiresActualBoundedCredentialFreeAnswer(t *testing.T) {
	for _, kind := range []string{"ok", "unreachable", "invalid", "oversized", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			s := &Service{origin: "https://services.example", key: make(ed25519.PublicKey, 32), client: &http.Client{Transport: reachTransport(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				if r.URL.String() != "https://services.example/health" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("probe leaked credentials or used wrong authority")
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("probe is unbounded")
				}
				if kind == "unreachable" {
					return nil, errors.New("fixture unreachable")
				}
				body := `{"alive":true}`
				status := 200
				headers := make(http.Header)
				if kind == "invalid" {
					body = `{"configured":true}`
				}
				if kind == "oversized" {
					body = strings.Repeat(" ", 257) + body
				}
				if kind == "redirect" {
					status = 302
					headers.Set("Location", "https://other.example")
				}
				return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}}
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					out := s.ServicesReachability(context.Background())
					if !out.Configured || out.CheckedAt == "" || out.Reachable != (kind == "ok") {
						t.Error("incorrect actual reachability", out)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 1 {
				t.Fatal("probe not bounded/cached", calls.Load())
			}
		})
	}
	var absent *Service
	if out := absent.ServicesReachability(context.Background()); out.Configured || out.Reachable || out.CheckedAt != "" {
		t.Fatal(out)
	}
}
