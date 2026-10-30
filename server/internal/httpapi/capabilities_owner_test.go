package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// ARCH-API-08 / ARCH-MEDIA-19: the capability document reports what the owner
// turned off, plus the missing facts. Precedence is unavailable >
// disabled_by_owner > not_permitted > enabled.
func TestCapabilitiesOwnerDisabledAndLimits(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	var err error
	d.Downloads, err = downloads.New(downloads.Options{DB: d.DB})
	if err != nil {
		t.Fatal(err)
	}
	h := New(d)
	read := func() CapabilitiesDocument {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/capabilities", nil)
		r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("capabilities: %d %s", w.Code, w.Body.String())
		}
		var out CapabilitiesDocument
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	// By default transcoding is on.
	if got := read(); got.Features["transcoding"] != "enabled" || got.Features["subtitle_burn_in"] != "enabled" {
		t.Fatalf("transcoding defaults: %+v", got.Features)
	}
	// With transcoding turned off by the owner, both report disabled_by_owner.
	if _, err = d.DB.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Features["transcoding"] != "disabled_by_owner" || got.Features["subtitle_burn_in"] != "disabled_by_owner" {
		t.Fatalf("owner-disabled transcoding: %+v", got.Features)
	}
	if _, err = d.DB.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	// A restricted profile with downloads off gets not_permitted (downloads is
	// composed here, so it is available, not unavailable).
	if _, err = d.DB.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES('profile',0)`); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Features["downloads"] != "not_permitted" {
		t.Fatalf("restricted downloads: %+v", got.Features)
	}
	// New limits equal their constants, never literals.
	got := read()
	if got.Limits["queueKeysMax"] != playbackv1.MaxQueueKeys {
		t.Fatalf("queueKeysMax = %d, want %d", got.Limits["queueKeysMax"], playbackv1.MaxQueueKeys)
	}
	if got.Limits["playlistWriteMax"] != catalog.MaxPlaylistEntries {
		t.Fatalf("playlistWriteMax = %d, want %d", got.Limits["playlistWriteMax"], catalog.MaxPlaylistEntries)
	}
	if got.Limits["bulkJobsActiveMax"] != catalog.MaxActiveBulkJobs {
		t.Fatalf("bulkJobsActiveMax = %d, want %d", got.Limits["bulkJobsActiveMax"], catalog.MaxActiveBulkJobs)
	}
	if got.Limits["downloadBatchMax"] != downloads.MaxBatchTargets {
		t.Fatalf("downloadBatchMax = %d, want %d", got.Limits["downloadBatchMax"], downloads.MaxBatchTargets)
	}
	// selectorIdsMax equals the playbackv1 explicit-items cap of 500.
	if got.Limits["selectorIdsMax"] != 500 {
		t.Fatalf("selectorIdsMax = %d, want 500", got.Limits["selectorIdsMax"])
	}
}
