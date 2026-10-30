package httpapi

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// NEW-38: a scan (or any background write) holds the database write gate for
// its own short transactions. The owner's library reads and the player's option
// reads only read, so they never wait for it: with the gate held by a
// background writer for the whole test, each answers 200 at once.
func TestForegroundReadsNeverWaitForTheWriteGate(t *testing.T) {
	f, _, _, _ := v1SubtitleFixture(t)
	item := f.items[0]
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "new38-reads-00000001"}, startBody(item, nil), 201, &s)
	// A second credential of the same profile (as after a token renewal):
	// its token doesn't match the media session's binding, and reaching the
	// presentation with it must not need a write either.
	renewed := f.device("new38-renewed-device")
	held, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	if _, err = held.Tx().Exec(`UPDATE admin_revision SET revision=revision+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	check := func(token, path string) {
		t.Helper()
		start := time.Now()
		w := f.raw("GET", path, token, nil, nil)
		if w.Code != 200 {
			t.Fatalf("%s with the write gate held: %d %s", path, w.Code, w.Body.String())
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%s waited %s for the write gate", path, elapsed)
		}
	}
	check(f.owner.AccessToken, "/v1/admin/libraries?limit=40")
	check(f.owner.AccessToken, "/v1/admin/libraries/"+f.library)
	for _, token := range []string{f.owner.AccessToken, renewed.AccessToken} {
		check(token, "/v1/items/"+item+"/playback-offers?sessionId="+url.QueryEscape(s.ID))
		check(token, "/v1/items/"+item+"/playback/"+s.ID+"/subtitles")
		check(token, "/v1/playback/sessions/"+s.ID+"/chapters?limit=100")
	}
}

// Right after a token renewal, before any timeline report rebinds the media
// session, the token it was bound to is no longer live. Offers read for the v1
// presentation judge it live by the caller's own login (v1 ownership already
// decided), so the subtitle plan is still offered.
func TestOffersKeepTheSubtitlePlanAcrossATokenRenewal(t *testing.T) {
	f, _, _, _ := v1SubtitleFixture(t)
	item := f.items[0]
	var s playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "new38-renew-00000001"}, startBody(item, nil), 201, &s)
	path := "/v1/items/" + item + "/playback-offers?sessionId=" + url.QueryEscape(s.ID)
	var before playback.Offers
	f.callAs(f.owner.AccessToken, "GET", path, nil, nil, 200, &before)
	if before.SubtitlePlan == nil {
		t.Fatalf("no subtitle plan to begin with: %+v", before.SubtitlePlanUnavailableReason)
	}
	renewed := f.device("new38-renew-device")
	// The token the media session is bound to stops being live, as the old
	// generation does on a renewal.
	if _, err := f.db.Exec(`UPDATE authorization_family_tokens SET retired=1 WHERE token_hash=?`, identity.Digest(f.owner.AccessToken)); err != nil {
		t.Fatal(err)
	}
	var after playback.Offers
	f.callAs(renewed.AccessToken, "GET", path, nil, nil, 200, &after)
	if after.SubtitlePlan == nil {
		reason := ""
		if after.SubtitlePlanUnavailableReason != nil {
			reason = *after.SubtitlePlanUnavailableReason
		}
		t.Fatalf("the renewed token lost the subtitle plan: %s", reason)
	}
}
