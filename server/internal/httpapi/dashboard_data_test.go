package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"strings"
	"testing"
)

func TestDashboardAnalyticsDoNotGrantAdministratorPeopleAuthority(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, path := range []string{"/v1/admin/play-history", "/v1/admin/play-history/summary", "/v1/admin/play-history/people", "/v1/admin/play-history/dashboard", "/v1/admin/dashboard/status", "/v1/admin/telemetry?window=live", "/v1/admin/telemetry/now"} {
		if strings.HasPrefix(path, "/v1/admin/play-history") || strings.HasPrefix(path, "/v1/admin/telemetry?") {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			path += sep + "timeZone=Asia%2FKathmandu&until=2026-10-22T12%3A00%3A00Z"
		}
		for _, token := range []string{f.admin, f.member} {
			w := f.call("GET", path, token, "")
			if w.Code != 403 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
		w := f.call("GET", path, f.owner, "")
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("analytics cached")
		}
	}
	for _, path := range []string{"/v1/admin/sessions", "/v1/admin/access/members"} {
		w := f.call("GET", path, f.admin, "")
		if w.Code != 200 {
			t.Fatal("normal administrator boundary changed", path, w.Code, w.Body.String())
		}
	}
}

func TestDashboardManagementRequiresTheAccountsMainProfile(t *testing.T) {
	f := administrationFixtureNew(t)
	for account, bearer := range map[string]string{"owner": f.owner, "admin": f.admin} {
		snapshot, e := f.deps.Identity.CreateDirectProfile(context.Background(), bearer, "Secondary", "blue")
		if e != nil {
			t.Fatal(e)
		}
		profile := snapshot.Profiles[len(snapshot.Profiles)-1].ID
		selected, e := f.deps.Identity.SelectDirectProfile(context.Background(), bearer, profile, identity.DirectSelection{})
		if e != nil {
			t.Fatal(e)
		}
		for _, path := range []string{"/v1/admin/sessions", "/v1/admin/access/members", "/v1/admin/dashboard/report", "/v1/admin/play-history/people", "/v1/admin/play-history/dashboard", "/v1/admin/dashboard/status"} {
			w := f.call("GET", path, selected.Session.AccessToken, "")
			if w.Code != 403 {
				t.Fatal(account, path, w.Code, w.Body.String())
			}
		}
	}
}

type observedAudio struct {
	AudioMedia
	active int64
}

func (a observedAudio) ActiveAudioConversions() int64 { return a.active }

type dashboardHost struct{}

func (dashboardHost) Sample(context.Context, int64) telemetry.Sample {
	return telemetry.Sample{Metrics: map[string]telemetry.Metric{}}
}

func TestDashboardLoadIncludesObservedAudioAndPreservesUnknownVideo(t *testing.T) {
	s := dashboardSampler{host: dashboardHost{}, d: Dependencies{AudioMedia: observedAudio{active: 3}}}
	out := s.Sample(context.Background(), 1000)
	if m := out.Metrics["softwareConversions"]; m.Status != telemetry.StatusLimited || m.Value != 3 {
		t.Fatal(m)
	}
	if out.Metrics["hardwareConversions"].Known() {
		t.Fatal("invented hardware observation")
	}
}
func TestMediaMeterCountsSuccessfulPartialWritesOnly(t *testing.T) {
	total := 0
	for _, code := range []int{200, 206, 403, 404, 503} {
		out := httptest.NewRecorder()
		w := &mediaTrafficWriter{ResponseWriter: out, record: func(n int) { total += n }}
		w.WriteHeader(code)
		_, _ = w.Write([]byte("12345"))
	}
	if total != 10 {
		t.Fatal("errors counted as streamed media", total)
	}
	var _ http.ResponseWriter = (*mediaTrafficWriter)(nil)
}

func TestCommonDashboardReportContextAuthorityAndIntervals(t *testing.T) {
	f := administrationFixtureNew(t)
	path := "/v1/admin/dashboard/report?period=today&window=24h&peoplePeriod=7d&topPeriod=30d&topKind=movies&timeZone=Asia%2FKathmandu&until=2026-10-22T12%3A00%3A00Z"
	for _, token := range []string{f.admin, f.member} {
		w := f.call("GET", path, token, "")
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := f.call("GET", path, f.owner, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out DashboardReport
	if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Context.TimeZone != "Asia/Kathmandu" || len(out.History.Buckets) != 1 || len(out.Load.Buckets) != 24 || len(out.People.Buckets) != 7 || len(out.TopPlayed.Buckets) != 30 || out.History.Until != out.Table.Until || out.Summary.Plays != *out.Table.Total {
		t.Fatal(out)
	}
	for _, bad := range []string{"/v1/admin/dashboard/report", "/v1/admin/dashboard/report?timeZone=Local&until=2026-10-22T12%3A00%3A00Z", "/v1/admin/dashboard/report?timeZone=UTC&until=2099-01-01T12%3A00%3A00Z", "/v1/admin/dashboard/report?timeZone=Asia%2FKathmandu&until=2026-10-22T12%3A01%3A00Z"} {
		w = f.call("GET", bad, f.owner, "")
		if w.Code != 400 {
			t.Fatal("invalid context", bad, w.Code, w.Body.String())
		}
	}
}
