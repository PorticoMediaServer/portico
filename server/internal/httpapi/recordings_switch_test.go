package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// P8: published recordings are ordinary movie items in the owner profile's
// private Recorded TV library. With the profile's Recordings switch off they
// are absent everywhere: the library is withheld from listings, search and
// home, and a recording reached by id answers 404 on detail and playback.
func TestRecordingsSwitchWithholdsPublishedRecordings(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		f := newV1Fixture(t, 2)
		v := f.owner.Viewer
		recording := f.records[0]
		if _, err := f.db.Exec(`INSERT INTO dvr_private_libraries VALUES(?,?,?,?)`, f.library, "local", v.AccountID, v.ProfileID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`INSERT INTO dvr_catalog_provenance(item_id,recording_id,asset_id,source_id,channel_id,programme_id,guide_generation,artifact_digest,captured_start_ms,captured_end_ms,state) VALUES(?,? ,?,'source','channel','programme','generation','digest',0,1000,'completed')`, recording.ID, "recording-1", recording.Token); err != nil {
			t.Fatal(err)
		}
		if !allowed {
			if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_dvr) VALUES(?,0)`, v.ProfileID); err != nil {
				t.Fatal(err)
			}
		}
		listed := strings.Contains(f.call("GET", "/v1/libraries", nil, nil, 200, nil).Body.String(), f.library)
		if listed != allowed {
			t.Fatalf("allowed=%v: recordings library listed=%v", allowed, listed)
		}
		wantStatus := map[bool]int{true: 200, false: 404}[allowed]
		if w := f.raw("GET", "/v1/items/"+recording.Public+"/detail", f.owner.AccessToken, nil, nil); w.Code != wantStatus {
			t.Fatalf("allowed=%v: recording detail %d %s", allowed, w.Code, w.Body.String())
		}
		search := f.raw("GET", "/v1/search?q=Film", f.owner.AccessToken, nil, nil)
		if found := strings.Contains(search.Body.String(), recording.Public); found != allowed {
			t.Fatalf("allowed=%v: search found the recording=%v: %d %s", allowed, found, search.Code, search.Body.String())
		}
		if home := f.raw("GET", "/v1/home", f.owner.AccessToken, nil, nil); !allowed && strings.Contains(home.Body.String(), recording.Public) {
			t.Fatalf("home shows a withheld recording: %s", home.Body.String())
		}
		f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
		start := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "recording-start-000001"}, startBody(recording.Public, nil))
		if want := map[bool]int{true: 201, false: 404}[allowed]; start.Code != want {
			t.Fatalf("allowed=%v: v1 start %d %s", allowed, start.Code, start.Body.String())
		}
		p, err := f.id.Authenticate(f.owner.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		err = contentaccess.VisibleItemTx(context.Background(), tx, p, recording.Public)
		tx.Rollback()
		if (err == nil) != allowed || !allowed && err != identity.ErrContentRestricted {
			t.Fatalf("allowed=%v: shared item fence %v", allowed, err)
		}
	}
}
