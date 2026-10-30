package playbackv1

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAirPlayReceiverRenewalWinsStaleExpiryAndEndWinsLateRenewal(t *testing.T) {
	f := newCarryFixture(t, false)
	ctx := context.Background()
	local := f.start("airplay-renew-owner", StartRequest{ItemID: f.items[0].Public, State: "playing", Subtitles: &SubtitleRequest{TrackID: TrackOff}})
	receiver, err := f.service.PrepareAirPlay(ctx, f.caller, local.ID, AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision})
	if err != nil {
		t.Fatal(err)
	}
	captured, err := loadRow(ctx, f.catalog.DB, local.ID)
	if err != nil {
		t.Fatal(err)
	}
	renewed := captured.lease + 60000
	if _, err = f.catalog.DB.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, renewed, local.ID); err != nil {
		t.Fatal(err)
	}
	var privateUntil int64
	var expires string
	if err = f.catalog.DB.QueryRow(`SELECT pp.expires_ms,p.expires_at FROM playback_airplay_presentations a JOIN playback_sessions p ON p.id=a.media_session_id JOIN playback_private_presentations pp ON pp.session_id=p.id WHERE a.session_id=?`, local.ID).Scan(&privateUntil, &expires); err != nil {
		t.Fatal(err)
	}
	if privateUntil != renewed || expires != time.UnixMilli(renewed).UTC().Format(time.RFC3339) {
		t.Fatal("receiver did not follow its owner's renewed lease", privateUntil, expires)
	}
	f.service.Now = func() time.Time { return time.UnixMilli(captured.lease + 1) }
	after := f.service.expireIfLapsed(ctx, captured)
	if after.ended != 0 || after.lease != renewed {
		t.Fatal("stale expiry defeated renewal", after.ended, after.lease)
	}
	grant := strings.TrimSuffix(strings.TrimPrefix(receiver.Presentation.URL, "/v1/media/"), "/master.m3u8")
	if _, _, _, err = f.service.Playback.ResolveGrantContext(ctx, grant); err != nil {
		t.Fatal("renewed receiver was fenced", err)
	}
	f.service.Now = func() time.Time { return time.UnixMilli(renewed + 1) }
	ended := f.service.expireIfLapsed(ctx, after)
	if ended.ended == 0 {
		t.Fatal("expired owner did not end")
	}
	if _, err = f.catalog.DB.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id=?`, renewed+120000, local.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = f.service.Playback.ResolveGrantContext(ctx, grant); err == nil {
		t.Fatal("late renewal resurrected an ended receiver")
	}
	if _, err = f.service.PrepareAirPlay(ctx, f.caller, local.ID, AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision}); err != ErrEnded {
		t.Fatalf("ended receiver preparation: %v", err)
	}
}
