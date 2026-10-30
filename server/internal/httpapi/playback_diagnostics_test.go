package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
	"time"
)

func TestPlaybackDiagnosticsOwnerScopeAndFreshness(t *testing.T) {
	d, owner, member := operationsFixture(t)
	mux := http.NewServeMux()
	d.playbackDiagnosticsRoutes(mux)
	hosted, e := operationsHostedOwner(d)
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/admin/playback/diagnostics"
	for _, token := range []string{"", member.AccessToken, hosted.AccessToken} {
		if w := supportRequest(mux, token, path); w.Code != refusedStatus(token) {
			t.Fatal("unexpected authority", w.Code)
		}
	}
	w := supportRequest(mux, owner.AccessToken, path)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Body.Len() > 8192 || !strings.Contains(w.Body.String(), `"recentFailures":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, field := range []string{"probes", "devices", "formats"} {
		if !strings.Contains(w.Body.String(), `"`+field+`":[]`) {
			t.Fatalf("unavailable hardware omitted its declared %s list", field)
		}
	}
	if os.Getenv("PORTICO_CONTRACT_UPDATE") == "1" {
		if e := contract.Record("../../testdata/contracts/get-admin-playback-diagnostics.json", "GetAdminPlaybackDiagnosticsResponse", "GET", path, nil, w, "owner"); e != nil {
			t.Fatal(e)
		}
	}
	var out struct {
		Scope                  struct{ ServerID, ViewerFence string }
		ObservedAt, FreshUntil string
		Diagnostics            playback.DeliveryDiagnostics
	}
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	observed, e := time.Parse(time.RFC3339Nano, out.ObservedAt)
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := time.Parse(time.RFC3339Nano, out.FreshUntil)
	if e != nil || fresh.Sub(observed) != 30*time.Second || out.Scope.ServerID != d.Identity.ID() || len(out.Scope.ViewerFence) != 64 {
		t.Fatal("invalid bound observation", out, e)
	}
	for _, secret := range []string{owner.AccessToken, "PRIVATE", "SOURCE-PATH"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("diagnostic leaked secret")
		}
	}
	if w = supportRequest(mux, owner.AccessToken, path+"?path=private"); w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestPlaybackDiagnosticsLateRevocationAndClosedTransientFailure(t *testing.T) {
	d, owner, _ := operationsFixture(t)
	path := "/v1/admin/playback/diagnostics"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, d.playbackDiagnosticsHandler(func(context.Context) (playback.DeliveryDiagnostics, error) {
		return playback.DeliveryDiagnostics{}, errors.New("PRIVATE-SECRET-PATH")
	}))
	if w := supportRequest(mux, owner.AccessToken, path); w.Code != 503 || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal(w.Code, w.Body.String())
	}
	principal, e := d.Identity.Authenticate(owner.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	mux = http.NewServeMux()
	mux.HandleFunc("GET "+path, d.playbackDiagnosticsHandler(func(context.Context) (playback.DeliveryDiagnostics, error) {
		if e := d.Identity.Logout(principal); e != nil {
			t.Fatal(e)
		}
		return playback.DeliveryDiagnostics{DirectConfigured: true}, nil
	}))
	if w := supportRequest(mux, owner.AccessToken, path); w.Code != 401 || strings.Contains(w.Body.String(), "directConfigured") {
		t.Fatal(w.Code, w.Body.String())
	}
}
