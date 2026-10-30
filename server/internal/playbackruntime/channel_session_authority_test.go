package playbackruntime

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// Delivery is deliberately absent: this regression checks the production
// identity, retained-family and owner-only source resolver, not decoded frames.
type authorityReadDelivery struct{}

func (authorityReadDelivery) Snapshot(string) playback.LinearMedia { return playback.LinearMedia{} }
func (authorityReadDelivery) ResolveSeek(string, string, string, *string) (string, error) {
	panic("not a seek test")
}
func (authorityReadDelivery) SelectionAt(string, string, string) (*playback.LinearSelection, error) {
	panic("not a seek test")
}

func TestChannelReadRevalidatesRetainedAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const server = "srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY"
	exec(`INSERT INTO networking_server_identities(server_id,public_key,key_incarnation,created_at) VALUES(?,X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f','read-test-key','2026-10-23T00:00:00Z')`, server)
	exec(`INSERT INTO networking_claim_identity(singleton,server_id,public_key,reset_generation) VALUES(1,?,X'000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f',0)`, server)
	exec(`INSERT INTO configuration(key,value) VALUES('id',?)`, server)
	identities, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`)
	exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`)
	envelope, err := identities.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := identities.Authenticate(envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := hosted.New(db, identities, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	live, err := livechannels.New(db)
	if err != nil {
		t.Fatal(err)
	}
	owner := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "fixture", func(string, string) bool { return true }, nil
	}
	source, err := live.Save(ctx, owner, livechannels.SourceInput{ID: strings.Repeat("ab", 24), RequestID: strings.Repeat("c0", 24), Name: "Owner-only fixture", TunerCount: 2, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",Channel one\nhttps://provider.invalid/one\n"})
	if err != nil {
		t.Fatal(err)
	}
	var channel string
	if err = db.QueryRow(`SELECT channel_id FROM live_channel_versions WHERE generation_id=?`, source.Generation).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	exec(`INSERT INTO playback_owner_policy(singleton,revision,transcoding_enabled) VALUES(1,1,1) ON CONFLICT(singleton) DO NOTHING`)
	exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('read-session','device','account','profile','local','start','digest','live','local','playing','{}',1,1,?,?,?)`, now.Add(time.Minute).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	sessions, err := playback.NewChannelSessions(db, identities)
	if err != nil {
		t.Fatal(err)
	}
	if err = sessions.ConfigureLinear(NewChannelResolver(live, nil, policy, nil), authorityReadDelivery{}); err != nil {
		t.Fatal(err)
	}
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sessions.StartTx(ctx, gated.Tx(), principal, playback.ChannelStart{SessionID: "read-session", Channel: playback.LinearReference{Kind: "live-source", SourceID: source.ID, ChannelID: channel, Generation: source.Generation}, State: "playing", Profile: playback.BaselineClientProfile(), LeaseUntil: now.Add(time.Minute)})
	if err != nil {
		gated.Rollback()
		t.Fatal(err)
	}
	if err = gated.Commit(); err != nil {
		t.Fatal(err)
	}
	// This is precisely the viewer-only principal supplied by playbackv1.rowPrincipal.
	viewer := identity.Principal{Viewer: identity.Viewer{Authority: principal.Authority, AccountID: principal.AccountID, ProfileID: principal.ProfileID}}
	read := func(p identity.Principal) (playback.ChannelState, error) {
		t.Helper()
		tx, done, err := dbwork.BeginRead(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		return sessions.ReadTx(ctx, tx, p, "read-session")
	}
	state, err := read(viewer)
	if err != nil || state.Name != "Channel one" || state.Desired.State != "playing" {
		t.Fatalf("owner channel projection lost retained authority: %+v %v", state, err)
	}
	other := viewer
	other.ProfileID = "other"
	if _, err = read(other); err == nil {
		t.Fatal("another viewer read the retained session")
	}
	t.Run("source access remains current", func(t *testing.T) {
		exec(`UPDATE live_sources SET state='disabled' WHERE id=?`, source.ID)
		defer exec(`UPDATE live_sources SET state='active' WHERE id=?`, source.ID)
		if _, err := read(viewer); err == nil {
			t.Fatal("disabled source remained readable")
		}
	})
	t.Run("lease remains current", func(t *testing.T) {
		exec(`UPDATE playback_v1_sessions SET lease_expires_ms=1 WHERE id='read-session'`)
		defer exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id='read-session'`, now.Add(time.Minute).UnixMilli())
		if _, err := read(viewer); err == nil {
			t.Fatal("expired session remained readable")
		}
	})
	t.Run("ended session is refused", func(t *testing.T) {
		exec(`UPDATE playback_channel_sessions SET ended_ms=1 WHERE session_id='read-session'`)
		defer exec(`UPDATE playback_channel_sessions SET ended_ms=0 WHERE session_id='read-session'`)
		if _, err := read(viewer); err == nil {
			t.Fatal("ended session remained readable")
		}
	})
	t.Run("retained role is reauthorized", func(t *testing.T) {
		exec(`UPDATE playback_channel_sessions SET role='member' WHERE session_id='read-session'`)
		defer exec(`UPDATE playback_channel_sessions SET role='owner' WHERE session_id='read-session'`)
		if _, err := read(viewer); !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatalf("changed retained role bypassed family authority: %v", err)
		}
	})
	exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, envelope.SessionFamilyID)
	if _, err = read(viewer); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("revoked family remained readable: %v", err)
	}
}
