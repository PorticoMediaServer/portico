package networking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Access-URL publication: the console-settings list (wired through
// SetAccessURLs) is published to members as manual routes that Hosted never
// dial-back proves.

// accessURLsCapture records what the manager actually asked Hosted to do: the
// published observations and the dial-back proofs by URL.
type accessURLsCapture struct {
	observations []RouteObservation
	proves       []string
}

func accessURLsFixture(t *testing.T, list func(context.Context) []string) (*sqlFixture, *RemoteManager, *Topology, context.Context) {
	t.Helper()
	f, m, topo, _, ctx := contactFixture(t)
	m.h.endpoints.pending = map[string]endpointPending{}
	m.SetAccessURLs(list)
	// Bind the settings to the current claim authority before applying the
	// owner's enabled policy, like the other route tests do.
	if _, _, _, _, key, e := m.snapshot(ctx); e != nil {
		t.Fatal(e)
	} else {
		clear(key)
	}
	if _, err := f.db.Exec(`UPDATE networking_remote_settings SET mapping=0`); err != nil {
		t.Fatal(err)
	}
	return f, m, topo, ctx
}

// captureAccessURLs replaces the Hosted transport after any certificate setup
// (installSyntheticCertificate installs its own stub), recording published
// observations and dial-back proofs by URL.
func captureAccessURLs(t *testing.T, m *RemoteManager, ctx context.Context) *accessURLsCapture {
	t.Helper()
	capture := &accessURLsCapture{}
	m.h.certificates.transport.client.Transport = claimRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/routes/prove"):
			var in struct {
				Endpoint struct {
					BaseURL string `json:"baseUrl"`
				} `json:"endpoint"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &in)
			capture.proves = append(capture.proves, in.Endpoint.BaseURL)
			return reply(http.StatusServiceUnavailable, `{"code":"unavailable"}`), nil
		case strings.HasSuffix(r.URL.Path, "/routes"):
			var in RouteObservation
			raw, _ := io.ReadAll(r.Body)
			if json.Unmarshal(raw, &in) != nil {
				t.Error("unreadable route publication")
			}
			capture.observations = append(capture.observations, in)
			body, _ := json.Marshal(struct {
				Generation int64 `json:"generation,string"`
			}{in.Generation})
			return reply(http.StatusOK, string(body)), nil

		}
		t.Errorf("unexpected Hosted call %s", r.URL.Path)
		return reply(http.StatusNotFound, `{"code":"unexpected"}`), nil
	})
	return capture
}

func manualCandidates(in RouteObservation) []string {
	out := []string{}
	for _, c := range in.Candidates {
		if c.Class == "manual" {
			out = append(out, c.BaseURL)
		}
	}
	return out
}

func TestAccessURLsPublishedAsManualCandidates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _, ctx := accessURLsFixture(t, func(context.Context) []string {
		return []string{"https://vpn.example.com:8443", "https://media.example.com:32500"}
	})
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal("first publication", e)
	}
	if len(capture.observations) != 1 {
		t.Fatalf("published %d times, want once", len(capture.observations))
	}
	got := manualCandidates(capture.observations[0])
	if len(got) != 2 || got[0] != "https://vpn.example.com:8443" || got[1] != "https://media.example.com:32500" {
		t.Fatalf("manual candidates out of order: %v", got)
	}
}

func TestManualCandidatesAreNeverProven(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, topo, ctx := accessURLsFixture(t, func(context.Context) []string {
		return []string{"https://vpn.example.com:8443"}
	})
	installSyntheticCertificate(t, m.h.certificates, ctx)
	m.h.certificates.SetListening(32500, true)
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal("manual-only publication", e)
	}
	if len(capture.proves) != 0 {
		t.Fatalf("Hosted was asked to prove %v", capture.proves)
	}
	status := remoteStatusOf(t, f)
	if status.State != "manual_only" || status.ErrorCode != "" {
		t.Fatalf("state=%q error=%q candidates=%v", status.State, status.ErrorCode, status.Candidates)
	}
	var attempts, retryAfter int64
	none := time.Time{}.UnixMilli()
	if e := f.db.QueryRow(`SELECT attempts,retry_after FROM networking_remote_state WHERE singleton=1`).Scan(&attempts, &retryAfter); e != nil || attempts != 0 || retryAfter != none {
		t.Fatalf("retry scheduled: attempts=%d retry_after=%d err=%v", attempts, retryAfter, e)
	}
	// A public route alongside still gets proven; the manual one never is.
	topo.Public = []string{"188.68.34.120"}
	_ = m.step(ctx)
	if len(capture.proves) == 0 {
		t.Fatal("public route was not proven either")
	}
	for _, u := range capture.proves {
		if u == "https://vpn.example.com:8443" {
			t.Fatalf("manual URL proven: %v", capture.proves)
		}
	}
}

func TestAccessURLChangePublishesOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _, ctx := accessURLsFixture(t, nil)
	list := []string{"https://vpn.example.com:8443"}
	m.SetAccessURLs(func(context.Context) []string { return list })
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if len(capture.observations) != 1 {
		t.Fatalf("unchanged list republished: %d", len(capture.observations))
	}
	first := capture.observations[0].Generation
	list = []string{"https://media.example.com:32500"}
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if e := m.step(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if len(capture.observations) != 2 {
		t.Fatalf("changed list published %d times, want 2", len(capture.observations))
	}
	if second := capture.observations[1].Generation; second != first+1 {
		t.Fatalf("generation %d -> %d, want exactly one bump", first, second)
	}
	if got := manualCandidates(capture.observations[1]); len(got) != 1 || got[0] != "https://media.example.com:32500" {
		t.Fatalf("republished candidates: %v", got)
	}
}

func TestSettingsReadFailureKeepsLastList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, m, _, ctx := accessURLsFixture(t, nil)
	list := []string{"https://vpn.example.com:8443"}
	fail := false
	m.SetAccessURLs(func(context.Context) []string {
		if fail {
			return nil
		}
		return list
	})
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	fail = true
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	if len(capture.observations) != 1 {
		t.Fatalf("failed read republished: %d", len(capture.observations))
	}
	if got := manualCandidates(remoteStatusCandidates(t, f)); len(got) != 1 || got[0] != "https://vpn.example.com:8443" {
		t.Fatalf("failed read cleared the list: %v", got)
	}
	fail = false
	list = []string{"https://media.example.com:32500"}
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	if len(capture.observations) != 2 {
		t.Fatalf("recovered list published %d times, want 2", len(capture.observations))
	}
}

func remoteStatusCandidates(t *testing.T, f *sqlFixture) RouteObservation {
	t.Helper()
	status := remoteStatusOf(t, f)
	out := RouteObservation{}
	for _, c := range status.Candidates {
		out.Candidates = append(out.Candidates, PublishedCandidate{BaseURL: c.BaseURL, Class: c.Class, Insecure: c.Insecure, Address: c.Address, Port: c.Port})
	}
	return out
}

// The seam resolves the provider's list deduplicated in order and capped, so
// it fits in the twelve candidates Hosted carries alongside LAN and public.
func TestAccessURLsDedupedOrderedAndCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _, ctx := accessURLsFixture(t, func(context.Context) []string {
		list := []string{"https://b.example.com", "https://a.example.com", "https://b.example.com", ""}
		for i := 0; i < 10; i++ {
			list = append(list, "https://n"+string(rune('0'+i))+".example.com")
		}
		return list
	})
	capture := captureAccessURLs(t, m, ctx)
	if e := m.step(ctx); e != nil {
		t.Fatal(e)
	}
	if len(capture.observations) != 1 {
		t.Fatalf("published %d times", len(capture.observations))
	}
	got := manualCandidates(capture.observations[0])
	want := []string{"https://b.example.com", "https://a.example.com", "https://n0.example.com", "https://n1.example.com", "https://n2.example.com", "https://n3.example.com", "https://n4.example.com", "https://n5.example.com"}
	if len(got) != len(want) {
		t.Fatalf("capped at %d: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order at %d: got %v want %v", i, got, want)
		}
	}
}

func TestManualHTTPPublishedWithAutomaticRemoteDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, m, _, ctx := accessURLsFixture(t, func(context.Context) []string {
		return []string{"http://owner.example.com:8080", "https://owner.example.com"}
	})
	useRemoteAccess(m, "off")
	capture := captureAccessURLs(t, m, ctx)
	if err := m.step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(capture.observations) != 1 || len(capture.observations[0].Candidates) != 2 || capture.observations[0].RemoteEnabled {
		t.Fatalf("observation %+v", capture.observations)
	}
	for i, c := range capture.observations[0].Candidates {
		if c.Class != "manual" || c.Insecure != (i == 0) || c.Address != "" || c.Port != 0 {
			t.Fatalf("candidate %+v", c)
		}
	}
	if len(capture.proves) != 0 {
		t.Fatal("manual URL dialed by Hosted", capture.proves)
	}
}
