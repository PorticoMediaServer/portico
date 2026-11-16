package opensubtitles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

func TestProviderErrorsDoNotBlameTheAccountOrQuota(t *testing.T) {
	const quota = `{"requests":21,"remaining":0,"reset_time_utc":"2026-11-17T00:00:00Z"}`
	for _, tc := range []struct {
		name, path, body, want string
		status, logins         int
	}{
		{"login rejected", "/login", `{}`, "account", 401, 1},
		{"API access forbidden", "/login", `{"message":"User agent required"}`, "access", 403, 1},
		{"login not acceptable", "/login", quota, "status", 406, 1},
		{"login rate limited", "/login", `{}`, "pause", 429, 1},
		{"provider unavailable", "/login", `{}`, "status", 503, 1},
		{"login without token", "/login", `{}`, "other", 200, 1},
		{"fresh tokens rejected", "/infos/user", `{"message":"Invalid token"}`, "status", 401, 2},
		{"download allowance used", "/download", quota, "quota", 406, 1},
		{"invalid download file", "/download", `{"message":"Invalid file_id"}`, "status", 406, 1},
		{"quota reset missing", "/download", `{"requests":21,"remaining":0}`, "status", 406, 1},
		{"downloads still available", "/download", `{"remaining":1,"reset_time_utc":"2026-11-17T00:00:00Z"}`, "status", 406, 1},
		{"network failure", "/login", "", "other", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins atomic.Int32
			stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Api-Key") != "test-key" || r.Header.Get("User-Agent") != "Portico v1.0.0" {
					t.Error("API request needs the application key and versioned User-Agent")
				}
				if r.URL.Path == "/api/v1/login" {
					logins.Add(1)
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" ||
						body["username"] != "owner" || body["password"] != "right" || len(body) != 2 {
						t.Error("login needs a JSON username and password")
					}
					if tc.path != "/login" {
						w.Write([]byte(`{"token":"test-token"}`))
						return
					}
				} else if r.URL.Path != "/api/v1"+tc.path || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected authenticated request: %s", r.URL.Path)
				}
				if tc.status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "30")
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			t.Cleanup(stand.Close)
			if tc.status == 0 {
				stand.Close() // A connection failure, without reaching any live provider.
			}
			c := New(stand.URL, 0, "test-key", "owner", "right")
			askedAt := time.Now()
			var err error
			if tc.path == "/download" {
				_, _, err = c.Download(context.Background(), "123")
			} else {
				_, err = c.Check(context.Background())
			}
			var status *fetch.Status
			var pause *fetch.SlowDown
			var used *AllowanceUsed
			got := "other"
			switch {
			case errors.Is(err, ErrSignInRefused):
				got = "account"
			case errors.Is(err, ErrKeyRefused):
				got = "access"
			case errors.As(err, &pause):
				got = "pause"
				if pause.Until.Before(askedAt.Add(30 * time.Second)) {
					t.Error("rate limit did not preserve Retry-After")
				}
			case errors.As(err, &used):
				got = "quota"
				if used.Renews.Format(time.RFC3339) != "2026-11-17T00:00:00Z" {
					t.Errorf("quota reset = %s", used.Renews)
				}
			case errors.As(err, &status):
				got = "status"
				if status.Code != tc.status {
					t.Errorf("upstream status = %d, want %d", status.Code, tc.status)
				}
			}
			if err == nil || got != tc.want {
				t.Fatalf("error = %v (%s), want %s", err, got, tc.want)
			}
			if got := int(logins.Load()); got != tc.logins {
				t.Errorf("login attempts = %d, want %d", got, tc.logins)
			}
		})
	}
}

func TestMissingLoginTokenCanBeTriedAgain(t *testing.T) {
	var logins atomic.Int32
	stand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/login" && logins.Add(1) == 1 {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"token":"test-token","data":{"allowed_downloads":20,"remaining_downloads":17}}`))
	}))
	defer stand.Close()
	c := New(stand.URL, 0, "test-key", "owner", "right")
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("login without a token succeeded")
	}
	if allowance, err := c.Check(context.Background()); err != nil || allowance.Remaining != 17 {
		t.Fatalf("trying again after a broken login answer = %+v, %v", allowance, err)
	}
	if logins.Load() != 2 {
		t.Fatalf("login attempts = %d, want 2", logins.Load())
	}
}
