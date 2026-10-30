package remotemedia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRcloneMetadataSeparatesFreshFactsAndAvoidsWholeFileHash(t *testing.T) {
	for _, cost := range []string{"fast", "slow", "unknown", "no_time"} {
		t.Run(cost, func(t *testing.T) {
			metadata, err := NewRcloneMetadata(12345, "private", "secret", NewOperationBudget(context.Background(), 2), "media:root")
			if err != nil {
				t.Fatal(err)
			}
			defer metadata.Close()
			calls, checks := 0, 0
			metadata.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != "127.0.0.1:12345" || r.Method != "POST" || r.Header.Get("Authorization") == "" {
					t.Fatal("lost exact private control origin or auth")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request["fs"] != "media:root" {
					t.Fatal(request, err)
				}
				body := `{"Name":"media","Hashes":["MD5"],"Features":{}}`
				if r.URL.Path == "/operations/fsinfo" {
					if cost != "unknown" {
						body = strings.Replace(body, `"Features":{}`, `"Features":{"SlowHash":`+map[bool]string{true: "true", false: "false"}[cost == "slow" || cost == "no_time"]+`}`, 1)
					}
				} else if r.URL.Path == "/operations/stat" {
					opt := request["opt"].(map[string]any)
					if request["remote"] != "episode.mkv" || opt["showHash"] != (cost == "fast") || opt["filesOnly"] != true {
						t.Fatal("hash cost or member request bypass", request)
					}
					body = `{"item":{"Path":"episode.mkv","Size":123,"ModTime":"2026-10-23T10:00:00.123456789Z","ID":"provider-object"}}`
					if cost == "fast" {
						body = strings.Replace(body, `"ID":"provider-object"`, `"ID":"provider-object","Hashes":{"MD5":"opaque-provider-value"}`, 1)
					}
					if cost == "no_time" {
						body = strings.Replace(body, "2026-10-23T10:00:00.123456789Z", "", 1)
					}
				} else {
					t.Fatal("arbitrary RC command", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			facts, err := metadata.Stat(context.Background(), "episode.mkv", func(context.Context) error { checks++; return nil })
			if err != nil || facts.Size != 123 || cost != "no_time" && facts.Modified.Nanosecond() != 123456789 || cost == "no_time" && !facts.Modified.IsZero() || facts.ID != "provider-object" || len(facts.Hashes) != map[bool]int{true: 1, false: 0}[cost == "fast"] {
				t.Fatal(facts, err)
			}
			if calls != 2 || checks != 4 {
				t.Fatal("missing before/after metadata checks", calls, checks)
			}
		})
	}
}

func TestRcloneMetadataRefusesMalformedChangedAndRevokedFacts(t *testing.T) {
	for _, failure := range []string{"missing", "different_member", "negative_size", "invalid_hash", "oversize", "redirect", "revoked"} {
		t.Run(failure, func(t *testing.T) {
			metadata, err := NewRcloneMetadata(12345, "private", "secret", NewOperationBudget(context.Background(), 2), "media:")
			if err != nil {
				t.Fatal(err)
			}
			defer metadata.Close()
			revoked := false
			metadata.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				body := `{"Name":"media","Features":{"SlowHash":false}}`
				status := 200
				if r.URL.Path == "/operations/stat" {
					body = `{"item":{"Path":"episode.mkv","Size":1}}`
					switch failure {
					case "missing":
						body = `{"item":null}`
					case "different_member":
						body = `{"item":{"Path":"another.mkv","Size":1}}`
					case "negative_size":
						body = `{"item":{"Path":"episode.mkv","Size":-1}}`
					case "invalid_hash":
						body = `{"item":{"Path":"episode.mkv","Size":1,"Hashes":{"MD5":"bad\nvalue"}}}`
					case "oversize":
						body = strings.Repeat("x", (1<<20)+1)
					case "redirect":
						status = 302
					case "revoked":
						revoked = true
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			facts, err := metadata.Stat(context.Background(), "episode.mkv", func(context.Context) error {
				if revoked {
					return ErrObservationChanged
				}
				return nil
			})
			if err == nil || facts.Path != "" {
				t.Fatal("invalid metadata escaped", facts, err)
			}
			if failure == "missing" && !errors.Is(err, ErrObservationChanged) {
				t.Fatal("missing object was not a changed observation", err)
			}
		})
	}
}
