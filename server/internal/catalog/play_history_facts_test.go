package catalog

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestHistorySnapshotCapturesOnlyAuthenticatedEventFacts(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/private")
	movie := c.Movie(lib, "/private/file.mkv", "Film", 2026)
	_, asset := c.File(movie.ID, "/private/file.mkv", 60)
	c.Exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('dev','a','install','Device','not-an-app','Portico for Android','1','2026-01-01','2026-01-01')`)
	c.Exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES('family','server','a','p','local','owner',0,'2099-01-01T00:00:00Z',1)`)
	c.Exec(`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES('hash','family',1,'2099-01-01T00:00:00Z')`)
	c.Exec(`INSERT INTO identity_device_families VALUES('family','dev')`)
	c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('play','hash','a','p',?,?,1,'playing','grant','private-capability','2099-01-01T00:00:00Z','req',60)`, movie.ID, asset)
	v := identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p", ServerID: "server"}
	read := func(actor identity.Viewer) (sql.NullInt64, sql.NullString) {
		t.Helper()
		tx, e := c.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		year, app, e := capturePlayHistorySnapshot(tx, actor, "play", movie.ID)
		if e != nil {
			t.Fatal(e)
		}
		return year, app
	}
	year, app := read(v)
	if !year.Valid || year.Int64 != 2026 || !app.Valid || app.String != "Portico for Android" {
		t.Fatal(year, app)
	}
	c.Exec(`UPDATE authorization_family_tokens SET retired=1 WHERE token_hash='hash'`)
	_, retiredApp := read(v)
	if !retiredApp.Valid || retiredApp.String != app.String {
		t.Fatal("rotated token lost its original authenticated app fact", retiredApp)
	}
	for _, field := range []string{"account", "profile", "server", "authority"} {
		actor := v
		switch field {
		case "account":
			actor.AccountID = "other"
		case "profile":
			actor.ProfileID = "other"
		case "server":
			actor.ServerID = "other"
		case "authority":
			actor.Authority = "hosted"
		}
		_, actual := read(actor)
		if actual.Valid {
			t.Fatal("mismatched authenticated identity supplied app", field, actual)
		}
	}
	// Later mutable metadata does not alter the captured event-time values.
	c.Exec(`UPDATE catalog_entities SET year=2027 WHERE id=?`, movie.ID)
	c.Exec(`UPDATE identity_devices SET app='Different app' WHERE id='dev'`)
	if year.Int64 != 2026 || app.String != "Portico for Android" {
		t.Fatal("capture retained live references")
	}
	for _, bad := range []string{"", "https://private.invalid/path?token=secret", "/private/app", "bad\napp"} {
		c.Exec(`UPDATE identity_devices SET app=? WHERE id='dev'`, bad)
		_, actual := read(v)
		if actual.Valid {
			t.Fatal("unsafe/empty app published", actual)
		}
	}
	c.Exec(`UPDATE identity_devices SET app='Portico',account_id='other' WHERE id='dev'`)
	_, actual := read(v)
	if actual.Valid {
		t.Fatal("another account's device supplied identity", actual)
	}
	c.Exec(`UPDATE identity_devices SET account_id='a' WHERE id='dev'`)
	c.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id='family'`)
	_, actual = read(v)
	if actual.Valid {
		t.Fatal("revoked family supplied identity", actual)
	}
	c.Exec(`DELETE FROM identity_device_families WHERE family_id='family'`)
	c.Exec(`UPDATE catalog_entities SET year=0 WHERE id=?`, movie.ID)
	unknownYear, unknownApp := read(v)
	if unknownYear.Valid || unknownApp.Valid {
		t.Fatal("unknown facts invented", unknownYear, unknownApp)
	}
}

func TestHistoryActiveUsesRealSessionLeaseAndOwnership(t *testing.T) {
	db := homeFixture(t)
	item := homeNames(t, db)["m1"].ID
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	id := addReportFact(t, db, item, "media", now-10*86400000, 1000)
	_, e := db.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms,media_session_id,item_id) VALUES('session','device','a','p','local','key','digest','vod','local','paused','{}',1,1,?,?,?,'media',?)`, now+1, now-1000, now, item)
	if e != nil {
		t.Fatal(e)
	}
	read := func(want bool) {
		t.Helper()
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		out, e := readPlayHistoryActive(context.Background(), tx, []int64{id}, now)
		if e != nil || out[id] != want {
			t.Fatal(out, e)
		}
	}
	read(true) // Old start time and paused state do not erase a real lease.
	for _, mutation := range []string{
		`UPDATE playback_v1_sessions SET ended_ms=1`,
		`UPDATE playback_v1_sessions SET lease_expires_ms=` + strconv.FormatInt(now, 10),
		`UPDATE playback_v1_sessions SET account_id='other'`,
		`UPDATE playback_v1_sessions SET profile_id='other'`,
		`UPDATE playback_v1_sessions SET item_id=0`,
		`UPDATE playback_v1_sessions SET media_session_id='other'`,
	} {
		if _, e = db.Exec(mutation); e != nil {
			t.Fatal(e)
		}
		read(false)
		if _, e = db.Exec(`UPDATE playback_v1_sessions SET ended_ms=0,lease_expires_ms=?,account_id='a',profile_id='p',item_id=?,media_session_id='media'`, now+1, item); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`UPDATE play_history SET authority='hosted' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	read(false)
	if _, e = db.Exec(`UPDATE play_history SET authority='local' WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE play_history SET completed=1 WHERE id=?`, id); e != nil {
		t.Fatal(e)
	}
	read(true)
	// All reads borrowing one report transaction see one live-status state.
	e = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		first, e := readPlayHistoryActive(ctx, dbwork.Snapshot(ctx), []int64{id}, now)
		if e != nil {
			return e
		}
		if _, e = db.Exec(`UPDATE playback_v1_sessions SET ended_ms=1`); e != nil {
			return e
		}
		second, e := readPlayHistoryActive(ctx, dbwork.Snapshot(ctx), []int64{id}, now)
		if e != nil {
			return e
		}
		if !first[id] || !second[id] {
			t.Fatal("mixed read transactions", first, second)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	read(false)
	if _, e = db.Exec(`DELETE FROM playback_v1_sessions`); e != nil {
		t.Fatal(e)
	}
	read(false)
}
