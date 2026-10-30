package httpapi

import (
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestProfileFeatureSwitchMatrix(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	var err error
	d.Downloads, err = downloads.New(downloads.Options{DB: d.DB})
	if err != nil {
		t.Fatal(err)
	}
	d.LiveChannels, err = livechannels.New(d.DB)
	if err != nil {
		t.Fatal(err)
	}
	h := New(d)
	patterns := make([]string, 0, len(routeLanes))
	for pattern := range routeLanes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	for _, feature := range []struct {
		name, column string
		paths        []string
	}{
		{"downloads", "allow_downloads", []string{"/v1/downloads", "/download-options"}},
		{"live_tv", "allow_live_tv", []string{"/v1/channels", "/v1/guide"}},
		{"dvr", "allow_dvr", []string{"/v1/dvr"}},
		{"watch_together", "allow_watch_together", []string{"/v1/groups"}},
	} {
		t.Run(feature.name, func(t *testing.T) {
			if _, err := d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,` + feature.column + `) VALUES('profile',0)`); err != nil {
				t.Fatal(err)
			}
			defer d.DB.Exec(`DELETE FROM profile_restrictions WHERE profile_id='profile'`)
			count := 0
			for _, pattern := range patterns {
				method, path, ok := strings.Cut(pattern, " ")
				if !ok || !strings.HasPrefix(path, "/") {
					continue
				}
				// Select policy families independently of the production classifier,
				// so omitting a family there cannot silently remove its acceptance probes.
				covered := false
				for _, family := range feature.paths {
					if strings.HasPrefix(path, family) || (family == "/download-options" && strings.HasSuffix(path, family)) {
						covered = true
					}
				}
				if !covered {
					continue
				}
				// Transfers recheck the grant's profile. Receipt keys are public
				// verification material. Owner-only server settings/storage manage
				// policy rather than consuming the profile feature.
				// Renewal is how a profile that has lost downloads has its copies
				// withdrawn, and the plays it made while allowed still count, so
				// neither is shut by the switch; neither hands anything out.
				if strings.Contains(path, "/artifacts/") || path == "/v1/downloads/receipt-keys" || pattern == "PUT /v1/downloads/settings" || strings.HasSuffix(path, "/dvr/storage") || path == "/v1/downloads/receipts/revalidate" || path == "/v1/downloads/progress" {
					continue
				}
				count++
				t.Run(pattern, func(t *testing.T) {
					// A typed route validates its path parameters before admission, so a
					// literal "{id}" never reaches the feature check; probe with a
					// well-formed placeholder instead.
					r := httptest.NewRequest(method, probePath(path), strings.NewReader(`{}`))
					r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
					r.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					if w.Code != 403 || errorCode(t, w) != "feature_restricted" {
						t.Fatalf("want 403 feature_restricted, got %d %s", w.Code, w.Body.String())
					}
				})
			}
			if count == 0 {
				t.Fatal("feature has no route probes")
			}
			t.Logf("%d registered routes checked", count)
		})
	}
}

func TestWatchTogetherSwitchStopsExistingMembership(t *testing.T) {
	f := socialHTTP(t)
	group := f.group("host-only")
	f.join(f.member, f.invite(group.Group.ID))
	path := "/v1/groups/" + group.Group.ID + "/heartbeat"
	f.decode(f.as(f.member, "POST", path, nil), 200, nil)
	if _, err := f.d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,allow_watch_together) VALUES('profile2',0)`); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{{"POST", path}, {"GET", "/v1/groups/" + group.Group.ID}, {"GET", "/v1/groups/" + group.Group.ID + "/events"}} {
		w := f.as(f.member, route.method, route.path, nil)
		if w.Code != 403 || errorCode(t, w) != "feature_restricted" {
			t.Fatalf("membership survived: %d %s", w.Code, w.Body.String())
		}
	}
}

// probePath replaces each {param} segment with a well-formed identifier.
func probePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[i] = "probe1"
		}
	}
	return strings.Join(parts, "/")
}
