package access

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
	"github.com/PorticoMediaServer/portico/server/internal/testauth"
)

// A member's stream count covers every protocol that holds a slot: a v1
// channel session has no legacy playback_sessions row, so without the v1
// count it would play outside maxStreams. A v1 VOD session's legacy
// presentation row is excluded, so one stream still counts once.
func TestAdmitPlaybackCountsV1SessionsOnce(t *testing.T) {
	store, db, ids := fixture(t)
	ctx := context.Background()
	if _, err := store.SetLimits(ctx, nil, "member", LimitsChange{ExpectedRevision: 1, OperationID: "limits-v1-count", Limits: Limits{MaxProfiles: 8, Conversion: "server", MaxStreamsMode: "limit", MaxStreams: 1}}); err != nil {
		t.Fatal(err)
	}
	member := identity.Principal{Viewer: identity.Viewer{AccountID: "member", ProfileID: "member-profile", Authority: "local", Role: identity.TierMember}}
	enforcer := &Enforcer{Store: store}
	admit := func() error {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		_, err = enforcer.AdmitPlayback(ctx, tx, member, Admission{})
		return err
	}
	insertV1 := func(id, kind, media string, ended int64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,media_session_id,lease_expires_ms,created_ms,updated_ms,ended_ms) VALUES(?,?,'member','member-profile','local',?,?,?,'local','playing','{}',1,1,?,?,0,0,?)`,
			id, "device-"+id, "key-"+id, "digest-"+id, kind, media, time.Now().Add(time.Minute).UnixMilli(), ended); err != nil {
			t.Fatal(err)
		}
	}
	// A live v1 channel session (no legacy row) fills the single slot.
	insertV1("ps_channel", "channel", "", 0)
	if err := admit(); err != ErrStreamLimit {
		t.Fatalf("a live channel session outside the count: %v", err)
	}
	// An ended channel session frees the slot.
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET ended_ms=? WHERE id='ps_channel'`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := admit(); err != nil {
		t.Fatalf("an ended channel session still counts: %v", err)
	}
	// A live v1 VOD session counts once, through its v1 row: its legacy
	// presentation row must not count a second time.
	testauth.InsertSession(t, db, "h", "member", "member-profile", "local", "member", "2099-01-01T00:00:00Z")
	if _, err := db.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('legacy-v1','h','member','member-profile',?, ?,1,'playing','g','t','2099-01-01T00:00:00Z','r',1)`, ids.familyID, ids.asset); err != nil {
		t.Fatal(err)
	}
	insertV1("ps_vod", "vod", "legacy-v1", 0)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	n, err := streamlimits.CountTx(ctx, tx, "local", "member", true, time.Now(), "", "")
	tx.Rollback()
	if err != nil || n != 1 {
		t.Fatalf("backing media counted twice: %d %v", n, err)
	}
	if err := admit(); err != ErrStreamLimit {
		t.Fatalf("a live VOD session was double-counted or missed: %v", err)
	}
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET ended_ms=? WHERE id='ps_vod'`, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// The v1 row ended but its legacy grant is still live: that is one stream.
	if err := admit(); err != ErrStreamLimit {
		t.Fatalf("a live legacy session outside the count: %v", err)
	}
}

// The channel allow/deny list keeps working with the schedule in front of it:
// outside the member's hours every channel is refused, inside them the list
// decides. Both sides are canonical v1 ids.
func TestAdmitChannelEnforcesScheduleBeforePolicy(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	open := AccessSchedule{Timezone: "UTC", Windows: []ScheduleWindow{{Days: []int{1}, StartMinute: 600, EndMinute: 780}}}
	limits := Limits{MaxProfiles: 8, Conversion: "server", MaxStreamsMode: "inherit", Channels: ChannelPolicy{Mode: "deny", Channels: []string{"library:news"}}, Schedule: open}
	if _, err := store.SetLimits(ctx, nil, "member", LimitsChange{ExpectedRevision: 1, OperationID: "limits-channel-schedule", Limits: limits}); err != nil {
		t.Fatal(err)
	}
	member := identity.Principal{Viewer: identity.Viewer{AccountID: "member", ProfileID: "member-profile", Authority: "local", Role: identity.TierMember}}
	enforcer := &Enforcer{Store: store}
	admit := func(at time.Time, channel string) error {
		store.Now = func() time.Time { return at }
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return enforcer.AdmitChannel(ctx, tx, member, channel)
	}
	inside := time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC)
	outside := time.Date(2026, 9, 14, 14, 0, 0, 0, time.UTC)
	if err := admit(inside, "library:sports"); err != nil {
		t.Fatalf("an allowed channel inside the hours: %v", err)
	}
	if err := admit(inside, "library:news"); err != ErrChannelDenied {
		t.Fatalf("a denied channel inside the hours: %v", err)
	}
	if err := admit(outside, "library:sports"); err != ErrSchedule {
		t.Fatalf("an allowed channel outside the hours: %v", err)
	}
}
