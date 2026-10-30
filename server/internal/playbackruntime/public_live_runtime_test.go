package playbackruntime

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	library "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
	"testing"
	"time"
)

// Opt-in campaign reproduction: real persisted family/source authority and
// runtime monitoring surround the same owned decoder/publisher path. No demo
// playback session or private database is used.
func TestPublicLiveRuntimePublishesWithRetainedAuthority(t *testing.T) {
	if os.Getenv("PORTICO_M0_PUBLIC_LIVE_REPRO") != "1" {
		t.Skip("campaign public fixture reproduction not requested")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
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

	preview, err := live.PreviewRemote(ctx, owner, livechannels.SourceFetcher{}, livechannels.RemoteDraft{
		ID: strings.Repeat("ab", 24), Name: "Approved public procedural fixture", Kind: "m3u",
		Locator: "https://demo.getportico.tv/m0-fixtures/live/lineup.m3u", ConfirmedLANRoots: []string{},
		Mappings: []livechannels.ChannelMapping{}, RefreshMs: 21600000, OwnerLimit: 2, ViewerAccess: "owner-only",
	})
	if err != nil {
		t.Fatal("preview", err)
	}
	source, err := live.SaveRemote(ctx, owner, preview.ID, strings.Repeat("c0", 24))
	if err != nil {
		t.Fatal("save", err)
	}
	var channel string
	if err = db.QueryRow(`SELECT channel_id FROM live_channel_versions WHERE generation_id=? AND name='Portico Demo Cinema'`, source.Generation).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	scheduled, err := library.New(db)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(db, identities, storage.New(""))
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.ConfigureChannels(LinearConfig{DB: db, Live: live, Library: scheduled, Policy: policy,
		CacheDirectory: filepath.Join(root, "cache"), LockDirectory: filepath.Join(root, "locks"),
		FFmpeg: decodertest.QualifiedFFmpeg(t), FFprobe: decodertest.QualifiedFFprobe(t)}); err != nil {
		t.Fatal(err)
	}
	if available, reason := runtime.Linear.Available(); !available {
		t.Fatal("runtime unavailable", reason)
	}
	now := time.Now()
	exec(`INSERT INTO playback_owner_policy(singleton,revision,transcoding_enabled) VALUES(1,1,1) ON CONFLICT(singleton) DO NOTHING`)
	exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms) VALUES('read-session','device','account','profile','local','start','digest','live','local','playing','{}',1,1,?,?,?)`, now.Add(2*time.Minute).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	profile := playback.BaselineClientProfile()
	profile.LiveChannels = true
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.Channels.StartTx(ctx, gated.Tx(), principal, playback.ChannelStart{SessionID: "read-session", Channel: playback.LinearReference{Kind: "live-source", SourceID: source.ID, ChannelID: channel, Generation: source.Generation}, State: "playing", Profile: profile, LeaseUntil: now.Add(2 * time.Minute)})
	if err != nil {
		gated.Rollback()
		t.Fatal("start", err)
	}
	if err = gated.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Shutdown(stop); err != nil {
			t.Error("shutdown", err)
		}
	}()
	viewer := identity.Principal{Viewer: identity.Viewer{Authority: principal.Authority, AccountID: principal.AccountID, ProfileID: principal.ProfileID}}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		tx, done, err := dbwork.BeginRead(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		state, err := runtime.Channels.ReadTx(ctx, tx, viewer, "read-session")
		done()
		if err != nil {
			t.Fatal("read", err)
		}
		media := state.Media
		key := media.State + ":" + media.WindowStartUS + ":" + media.WindowEndUS + ":" + media.ErrorCode
		if key != previous {
			t.Logf("state=%s window=%s..%s URLpresent=%t error=%s", media.State, media.WindowStartUS, media.WindowEndUS, media.StreamURL != "", media.ErrorCode)
			previous = key
		}
		if media.StreamURL != "" && media.State == "active" {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime never published", key)
		case <-ticker.C:
		}
	}
}
