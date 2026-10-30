package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func TestLibrarySummaryCoreHTTPUsesLiveAuthorityAndReadyProjection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"book", "comic"} {
		t.Run(kind, func(t *testing.T) {
			format, want := "pdf", "ebook"
			if kind == "comic" {
				format, want = "cbz", "comic"
			}
			r := newReadingFixture(t, kind, format)
			readingSeriesDrain(t, r)
			h := New(r.d)
			check := func(token string, status int, present bool, count int) {
				t.Helper()
				w := readingHTTP(t, h, "GET", "/v1/libraries", token, "", nil)
				if w.Code != status {
					t.Fatalf("libraries: %d %s", w.Code, w.Body.String())
				}
				if status != http.StatusOK {
					return
				}
				var body struct {
					Items []catalog.Library `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, library := range body.Items {
					if library.ID != r.library {
						continue
					}
					found = true
					summary := library.Summary
					if summary == nil || len(summary.Counts) != 1 || summary.Counts[0].Kind != want || summary.Counts[0].Count != count || summary.Empty != (count == 0) || !summary.EmptyKnown {
						t.Fatalf("Reading summary: %+v", summary)
					}
				}
				if found != present {
					t.Fatalf("library presence: %v want %v", found, present)
				}
				if !present && strings.Contains(w.Body.String(), r.library) {
					t.Fatal("revoked library fact escaped")
				}
			}
			check(r.f.owner.AccessToken, http.StatusOK, true, 1)
			before := telemetry.LibrarySummaryCacheCounters()
			check(r.f.owner.AccessToken, http.StatusOK, true, 1)
			if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryHitEntry] < before[telemetry.SummaryHitEntry]+1 {
				t.Fatalf("HTTP repeated read did not reuse core: before=%v after=%v", before, telemetry.LibrarySummaryCacheCounters())
			}

			check("", http.StatusUnauthorized, false, 0)
			readingSeriesOwnerField(t, r, r.item, "author", "Changed attribution")
			// An attribution rebuild must not take down shell-critical reads.
			for _, path := range []string{"/v1/direct", "/v1/system", "/v1/libraries"} {
				w := readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
				if w.Code != http.StatusOK {
					t.Fatalf("shell read during building generation %s: %d %s", path, w.Code, w.Body.String())
				}
				if path == "/v1/libraries" {
					var body struct {
						Items []catalog.Library `json:"items"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, library := range body.Items {
						if library.ID == r.library {
							found = true
							got := library.Summary
							if got == nil || got.EmptyKnown || len(got.Counts) != 1 || got.Counts[0].Count != 1 || !got.Counts[0].AtLeast || len(got.PublicationCounts) != 0 {
								t.Fatalf("shell partial summary: %+v", got)
							}
						}
					}
					if !found {
						t.Fatal("building Reading library disappeared from the shell")
					}
				}
			}
			readingSeriesDrain(t, r)
			owner := readingOwnerLogin(t, r)
			profiles, err := r.f.id.CreateDirectProfile(context.Background(), owner.AccountToken, "Summary reader", "blue")
			if err != nil {
				t.Fatal(err)
			}
			var child identity.DirectProfile
			for _, profile := range profiles.Profiles {
				if !profile.Primary && profile.Name == "Summary reader" {
					child = profile
				}
			}
			// The owner's main profile records by default and a new profile does not;
			// Recordings is part of the summary policy, so give the child the same.
			if _, err = r.f.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES(?,1)`, child.ID); err != nil {
				t.Fatal(err)
			}
			selected, err := r.f.id.SelectDirectProfile(context.Background(), owner.AccountToken, child.ID, identity.DirectSelection{})
			if err != nil {
				t.Fatal(err)
			}
			before = telemetry.LibrarySummaryCacheCounters()
			check(selected.Session.AccessToken, http.StatusOK, true, 1)
			if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryHitEntry] < before[telemetry.SummaryHitEntry]+1 {
				t.Fatal("same-policy cross-user HTTP did not share")
			}
			rotated, err := r.f.id.RefreshSession(context.Background(), selected.Session.RefreshToken, selected.Session.InstallationID, identity.Token())
			if err != nil {
				t.Fatal(err)
			}
			selected.Session = rotated
			before = telemetry.LibrarySummaryCacheCounters()
			check(rotated.AccessToken, http.StatusOK, true, 1)
			if telemetry.LibrarySummaryCacheCounters()[telemetry.SummaryHitEntry] < before[telemetry.SummaryHitEntry]+1 {
				t.Fatal("session rotation invalidated core")
			}

			if _, err = r.f.id.SetProfileRestrictions(context.Background(), owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
				t.Fatal(err)
			}
			check(selected.Session.AccessToken, http.StatusOK, true, 0)
			if _, err = r.f.id.EditDirectProfile(context.Background(), owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
				t.Fatal(err)
			}
			check(selected.Session.AccessToken, http.StatusOK, false, 0)
			if err = r.f.id.LogoutToken(context.Background(), r.f.owner.AccessToken); err != nil {
				t.Fatal(err)
			}
			check(r.f.owner.AccessToken, http.StatusUnauthorized, false, 0)
		})
	}
}
