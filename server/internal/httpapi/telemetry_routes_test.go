package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func telemetryFixture(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	d, owner, member := operationsFixture(t)
	d.Console = operations.New(d.DB)
	d.Measurements = operations.NewMeasurements(d.DB, t.TempDir())
	mux := http.NewServeMux()
	d.telemetryRoutes(mux)
	return mux, owner.AccessToken, member.AccessToken
}

func telemetryData(t *testing.T, mux http.Handler, token, path string, into any) {
	t.Helper()
	w := supportRequest(mux, token, path)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("owner observations must not be cached by intermediaries", path)
	}
	var envelope struct {
		Scope struct {
			ServerID string `json:"serverId"`
			Fence    string `json:"viewerFence"`
		} `json:"scope"`
		Data json.RawMessage `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(path, e)
	}
	if envelope.Scope.ServerID == "" || len(envelope.Scope.Fence) != 64 {
		t.Fatal("every owner read must carry its server and viewer fence", path)
	}
	if into != nil {
		if e := json.Unmarshal(envelope.Data, into); e != nil {
			t.Fatal(path, e)
		}
	}
}

// Capture the viewer context once; invalid-query tests keep that valid context
// so a missing time zone cannot accidentally satisfy a different rejection.
func telemetryContext(t *testing.T, path string) string {
	t.Helper()
	u, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("timeZone", "America/Halifax")
	q.Set("until", time.Now().Truncate(reportwindow.Grain).UTC().Format(time.RFC3339))
	u.RawQuery = q.Encode()
	return u.String()
}

func TestTelemetrySurfacesAreOwnerOnly(t *testing.T) {
	mux, owner, member := telemetryFixture(t)
	paths := []string{"/v1/admin/transcode/capacity", "/v1/admin/telemetry", "/v1/admin/telemetry/now", "/v1/admin/attention", "/v1/admin/playback/history", "/v1/admin/playback/history.csv"}
	for _, path := range paths {
		if path == "/v1/admin/telemetry" {
			path = telemetryContext(t, path)
		}
		for _, token := range []string{"", member} {
			if w := supportRequest(mux, token, path); w.Code != refusedStatus(token) {
				t.Fatalf("%s must refuse a non-owner: %d", path, w.Code)
			}
		}
		if w := supportRequest(mux, owner, path); w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestCapacityAndAttentionReadTheSettingsRegistry(t *testing.T) {
	mux, owner, _ := telemetryFixture(t)
	var report telemetry.Report
	telemetryData(t, mux, owner, "/v1/admin/transcode/capacity", &report)
	if report.X264Preset != "veryfast" || report.Hardware.Configured != "auto" || report.ThrottleBufferMs != 60000 {
		t.Fatal("the capacity report must publish the registry's effective values", report)
	}
	if len(report.Presets) == 0 || report.Hardware.Effective == "" {
		t.Fatal("the report must name its ladder and its effective backend", report)
	}
	var attention struct {
		Items []telemetry.Item `json:"items"`
	}
	telemetryData(t, mux, owner, "/v1/admin/attention", &attention)
	for _, item := range attention.Items {
		if item.ID == "" || item.Severity == "" || item.Action.Kind == "" {
			t.Fatal("every attention item must be actionable", item)
		}
	}
}

func TestTelemetryWindowsAndHistoryQueriesAreValidated(t *testing.T) {
	mux, owner, member := telemetryFixture(t)
	assertTelemetryUTCQuarterContext(t, mux, owner, member)
	var reading telemetry.Reading
	telemetryData(t, mux, owner, telemetryContext(t, "/v1/admin/telemetry?window=1h"), &reading)
	if reading.Window != "1h" || len(reading.Series) != len(telemetry.MetricNames) {
		t.Fatal("every published metric needs a series", reading)
	}
	for _, name := range telemetry.MetricNames {
		if _, ok := reading.Status[name]; !ok {
			t.Fatal("every metric must publish its own status", name)
		}
	}
	for _, bad := range []string{"/v1/admin/telemetry?window=5s", "/v1/admin/playback/history?period=forever", "/v1/admin/playback/history?limit=9999", "/v1/admin/telemetry?unknown=1"} {
		if strings.HasPrefix(bad, "/v1/admin/telemetry?") {
			bad = telemetryContext(t, bad)
		}
		if w := supportRequest(mux, owner, bad); w.Code != 400 {
			t.Fatalf("%s must be refused: %d %s", bad, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"", "?timeZone=America/Halifax", "?until=2026-01-01T00:00:00Z", "?timeZone=Local&until=2026-01-01T00:00:00Z", "?timeZone=America/Halifax&until=2026-01-01T00:00:01Z"} {
		if w := supportRequest(mux, owner, "/v1/admin/telemetry"+query); w.Code != 400 {
			t.Fatalf("invalid report context accepted: %q %d", query, w.Code)
		}
	}
	var sample telemetry.Sample
	telemetryData(t, mux, owner, "/v1/admin/telemetry/now", &sample)
	if len(sample.Metrics) != len(telemetry.MetricNames) {
		t.Fatal("the latest sample must publish every metric", sample)
	}
}

// The Roku report-context builder explicitly uses UTC. Its query captures
// one clock value, floors epoch seconds to 900, and encodes that exact instant;
// the registered owner handler must preserve this context without inferring a
// different host/profile zone or moving the requested interval.
func assertTelemetryUTCQuarterContext(t *testing.T, mux http.Handler, owner, member string) {
	t.Helper()
	now := time.Now().UTC()
	until := time.Unix(now.Unix()/900*900, 0).UTC().Format(time.RFC3339)
	query := url.Values{"window": {"1h"}, "timeZone": {"UTC"}, "until": {until}}
	path := "/v1/admin/telemetry?" + query.Encode()
	var reading telemetry.Reading
	telemetryData(t, mux, owner, path, &reading)
	if reading.Window != "1h" || reading.TimeZone != "UTC" || reading.Until != until {
		t.Fatalf("owner telemetry changed the captured UTC context: %+v", reading.Interval)
	}
	for _, token := range []string{"", member} {
		if w := supportRequest(mux, token, path); w.Code != refusedStatus(token) {
			t.Fatalf("UTC context bypassed owner admission: %d", w.Code)
		}
	}
	// A valid named zone does not excuse an unaligned or future cutoff.
	for _, at := range []time.Time{
		time.Unix(now.Unix()/900*900+1, 0),
		time.Unix(now.Unix()/900*900+1800, 0),
	} {
		query.Set("until", at.UTC().Format(time.RFC3339))
		if w := supportRequest(mux, owner, "/v1/admin/telemetry?"+query.Encode()); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid UTC cutoff was accepted: %d", w.Code)
		}
	}
}

func TestPlaybackHistoryExportIsADownloadableCSV(t *testing.T) {
	mux, owner, _ := telemetryFixture(t)
	w := supportRequest(mux, owner, "/v1/admin/playback/history.csv?period=7d")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatal(w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "playback-history-7d.csv") {
		t.Fatal("the export must be named for its period", w.Header().Get("Content-Disposition"))
	}
	if !strings.HasPrefix(w.Body.String(), "id,viewer,title") {
		t.Fatal("the export must carry a header row", w.Body.String())
	}
}

func TestCapacityComposerUsesTheAppliedCacheDirectory(t *testing.T) {
	cache := t.TempDir()
	d := Dependencies{CacheDirectory: cache}
	capacity := d.capacityComposer()
	if capacity.State != cache {
		t.Fatal(capacity.State)
	}
	if got := operations.ConversionDirectory(capacity.State, ""); got != filepath.Join(cache, "hls") {
		t.Fatal(got)
	}
}

func TestCapacityComposerWiresMemoryAdmissionAndCap(t *testing.T) {
	c := (Dependencies{}).capacityComposer()
	if c.MemoryAvailable == nil {
		t.Fatal("capacity must use playback memory admission")
	}
	if c.MemoryAvailable(t.TempDir(), "quarter") {
		t.Fatal("an ordinary disk directory is not available memory output")
	}
	s := operations.DefaultSettings()
	s.MemoryCap = "2gb"
	s.TranscodeLocation = "disk"
	if got := transcodeSettings(s).TranscodeLocation; got != "disk" {
		t.Fatal("explicit location did not reach delivery settings", got)
	}
	if got := transcodeSettings(s).MemoryCap; got != "2gb" {
		t.Fatal("memory cap did not reach telemetry", got)
	}
}
