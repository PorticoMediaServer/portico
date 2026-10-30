package webhooks

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A trigger's body is only checked when it first runs. A trigger that named a
// column that is not there would fail the statement that fired it, which here
// means a playback start, a recording or a sign-in. So every trigger is run.

// fill inserts a row giving every required column a value, so a test can touch
// a wide table without knowing all of it. Checks and foreign keys are not what
// is under test; the connection has them off.
func fill(t *testing.T, conn *sql.Conn, table string, values map[string]any) {
	t.Helper()
	ctx := context.Background()
	rows, err := conn.QueryContext(ctx, `SELECT name,type,"notnull",dflt_value IS NOT NULL,pk FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	columns, args := []string{}, []any{}
	for rows.Next() {
		var name, kind string
		var required, defaulted, pk int
		if err = rows.Scan(&name, &kind, &required, &defaulted, &pk); err != nil {
			t.Fatal(err)
		}
		value, given := values[name]
		if !given {
			if required == 0 || defaulted != 0 || pk != 0 && strings.EqualFold(kind, "INTEGER") {
				continue
			}
			switch strings.ToUpper(kind) {
			case "INTEGER", "REAL":
				value = 0
			case "BLOB":
				value = []byte{}
			default:
				value = ""
			}
		}
		columns, args = append(columns, `"`+name+`"`), append(args, value)
	}
	rows.Close()
	if _, err = conn.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(%s) VALUES(%s)`, table, strings.Join(columns, ","), strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")), args...); err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}

func TestEveryEventTriggerRunsAndQueuesItsEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, db := open(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	library := c.Library("movies", "Movies", "movie", "/movies")
	film := c.Movie(library, "/movies/film.mkv", "Film", 2001)
	made, err := store.Create(ctx, nil, Create{Name: "Everything", URL: "http://127.0.0.1:9/hook", Events: Events})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, pragma := range []string{`PRAGMA foreign_keys=OFF`, `PRAGMA ignore_check_constraints=ON`} {
		if _, err = conn.ExecContext(ctx, pragma); err != nil {
			t.Fatal(err)
		}
	}
	defer conn.ExecContext(ctx, `PRAGMA ignore_check_constraints=OFF`)
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
	run := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	queued := func(event string) (int, string) {
		t.Helper()
		var n int
		var data sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT count(*),max(data) FROM webhook_deliveries WHERE webhook_id=? AND type=?`, made.Webhook.ID, event).Scan(&n, &data); err != nil {
			t.Fatal(err)
		}
		return n, data.String
	}
	expect := func(event string, want int, contains ...string) {
		t.Helper()
		n, data := queued(event)
		if n != want {
			t.Fatalf("%s: %d queued, want %d", event, n, want)
		}
		for _, part := range contains {
			if !strings.Contains(data, part) {
				t.Fatalf("%s payload lacks %q: %s", event, part, data)
			}
		}
	}

	// People and devices the payloads name.
	fill(t, conn, "accounts", map[string]any{"id": "member", "username": "sam", "profile_id": "profile"})
	run(`UPDATE direct_profiles SET name='Sam' WHERE id='profile'`)
	fill(t, conn, "identity_devices", map[string]any{"id": "device", "account_id": "member", "installation_id": "install", "name": "Living room TV", "platform": "tvos"})
	fill(t, conn, "identity_devices", map[string]any{"id": "api-device", "account_id": "member", "installation_id": "api-credential-1", "name": "Script", "platform": "api"})

	// Sign-in: a session family bound to a device. An account-level family (no
	// profile chosen yet) and an API credential are not sign-ins.
	fill(t, conn, "authorization_session_families", map[string]any{"id": "family", "account_id": "member", "profile_id": "profile", "authority": "local", "role": "member", "epoch": 1, "current_generation": 1})
	fill(t, conn, "authorization_session_families", map[string]any{"id": "family-account", "account_id": "member", "profile_id": "profile", "authority": "local", "role": "account", "epoch": 1, "current_generation": 1})
	fill(t, conn, "authorization_session_families", map[string]any{"id": "family-api", "account_id": "member", "profile_id": "profile", "authority": "local", "role": "member", "epoch": 1, "current_generation": 1})
	run(`INSERT INTO identity_device_families(family_id,device_id) VALUES('family-account','device')`)
	run(`INSERT INTO identity_device_families(family_id,device_id) VALUES('family-api','api-device')`)
	expect(MemberSignedIn, 0)
	run(`INSERT INTO identity_device_families(family_id,device_id) VALUES('family','device')`)
	expect(MemberSignedIn, 1, `"user":"sam"`, `"profile":"Sam"`, `"device":"Living room TV"`)

	// Playback: preparing is not a start; ready is; then pause, resume, stop.
	fill(t, conn, "playback_v1_sessions", map[string]any{"id": "play", "device_id": "device", "account_id": "member", "profile_id": "profile", "authority": "local", "kind": "item", "state": "preparing", "item_id": film.ID})
	expect(PlaybackStarted, 0)
	run(`UPDATE playback_v1_sessions SET state='playing' WHERE id='play'`)
	expect(PlaybackStarted, 1, `"title":"Film"`, `"user":"sam"`, `"device":"Living room TV"`)
	run(`UPDATE playback_v1_sessions SET state='paused' WHERE id='play'`)
	expect(PlaybackPaused, 1)
	run(`UPDATE playback_v1_sessions SET position_ms=5000 WHERE id='play'`)
	run(`UPDATE playback_v1_sessions SET state='paused' WHERE id='play'`)
	expect(PlaybackPaused, 1)
	run(`UPDATE playback_v1_sessions SET state='playing' WHERE id='play'`)
	expect(PlaybackResumed, 1)
	// A replacement on the same device for the same title (a change of quality)
	// is neither a stop nor a start.
	fill(t, conn, "playback_v1_sessions", map[string]any{"id": "play-2", "start_key": "k2", "device_id": "device", "account_id": "member", "profile_id": "profile", "authority": "local", "kind": "item", "state": "playing", "item_id": film.ID})
	run(`UPDATE playback_v1_sessions SET ended_ms=1,end_reason='replaced' WHERE id='play'`)
	expect(PlaybackStarted, 1)
	expect(PlaybackStopped, 0)
	run(`UPDATE playback_v1_sessions SET ended_ms=2,end_reason='stopped' WHERE id='play-2'`)
	expect(PlaybackStopped, 1, `"reason":"stopped"`)
	// A session that starts ready is a start at once.
	fill(t, conn, "playback_v1_sessions", map[string]any{"id": "play-3", "start_key": "k3", "device_id": "device", "account_id": "member", "profile_id": "profile", "authority": "local", "kind": "item", "state": "playing", "item_id": film.ID})
	if n, _ := queued(PlaybackStarted); n != 2 {
		t.Fatalf("a session that starts ready: %d", n)
	}

	// Scans: a job that walks folders, start to finish. A job with no folders
	// to walk (analysis of one file) is not a scan.
	run(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('scan','movies','queued','2026-01-01T00:00:00Z')`)
	run(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('analysis','movies','queued','2026-01-01T00:00:00Z')`)
	run(`INSERT INTO inventory_directories(job_id,relative_path) VALUES('scan','.')`)
	run(`UPDATE jobs SET status='running' WHERE id IN('scan','analysis')`)
	expect(ScanStarted, 1, `"library":"Movies"`)
	run(`UPDATE jobs SET status='paused' WHERE id='scan'`)
	run(`UPDATE jobs SET status='running' WHERE id='scan'`)
	expect(ScanStarted, 1)
	run(`UPDATE jobs SET status='complete_with_warnings',processed=12 WHERE id='scan'`)
	run(`UPDATE jobs SET status='complete' WHERE id='analysis'`)
	expect(ScanFinished, 1, `"outcome":"complete_with_warnings"`, `"processed":12`)

	// Recordings, including a programme document that is not valid JSON.
	fill(t, conn, "dvr_recordings", map[string]any{"id": "rec", "account_id": "member", "profile_id": "profile", "state": "recording", "programme_json": `{"title":"The News"}`, "channel_id": "bbc-one"})
	fill(t, conn, "dvr_recordings", map[string]any{"id": "rec-2", "programme_id": "p2", "account_id": "member", "profile_id": "profile", "state": "recording", "programme_json": `not json`, "channel_id": "bbc-two"})
	run(`UPDATE dvr_recordings SET state='completed' WHERE id='rec'`)
	expect(RecordingFinished, 1, `"title":"The News"`)
	run(`UPDATE dvr_recordings SET state='failed',reason='tuner-conflict' WHERE id='rec-2'`)
	expect(RecordingFailed, 1, `"reason":"tuner-conflict"`)

	// Storage: the first warning, and a change of how serious it is.
	fill(t, conn, "notification_records", map[string]any{"id": "n1", "category": "storage.low", "severity": "warning", "source": "storage", "dedupe_key": "volume:state", "arguments": `{"freePercent":"9","freeBytes":"1000"}`})
	fill(t, conn, "notification_records", map[string]any{"id": "n2", "category": "scan.failed", "severity": "warning", "source": "scan", "dedupe_key": "x"})
	expect(StorageLow, 1, `"severity":"warning"`, `"freePercent":"9"`)
	run(`UPDATE webhook_deliveries SET attempts=1 WHERE type='storage.low'`)
	run(`UPDATE notification_records SET severity='critical' WHERE id='n1'`)
	expect(StorageLow, 2)

	// A playable title's metadata being republished is an update to it.
	run(`UPDATE webhook_deliveries SET attempts=1 WHERE type IN('title.added','title.updated')`)
	run(`INSERT INTO metadata_publication_heads(item_id) VALUES(?) ON CONFLICT(item_id) DO NOTHING`, film.ID)
	run(`UPDATE metadata_publication_heads SET metadata_revision=metadata_revision+1 WHERE item_id=?`, film.ID)
	expect(TitleUpdated, 1, `"title":"Film"`)

	// A show's own details (overview, artwork) changing is an update to it too.
	show := c.Show(library, "Show", 2001)
	run(`UPDATE webhook_deliveries SET attempts=1 WHERE type IN('title.added','title.updated')`)
	run(`UPDATE catalog_shows SET overview='New overview' WHERE entity_id=?`, show.ID)
	expect(TitleUpdated, 2)
	run(`UPDATE webhook_deliveries SET attempts=1 WHERE type='title.updated'`)
	run(`INSERT INTO catalog_item_details(entity_id,poster_url) VALUES(?,'artwork:show') ON CONFLICT(entity_id) DO UPDATE SET poster_url=excluded.poster_url`, show.ID)
	expect(TitleUpdated, 3)
	run(`UPDATE catalog_shows SET overview='New overview' WHERE entity_id=?`, show.ID)
	expect(TitleUpdated, 3)

	// A member's private recordings library is never reported.
	private := c.Library("recorded", "Recorded TV", "movie", "/recorded")
	run(`INSERT INTO dvr_private_libraries(library_id,authority,account_id,profile_id) VALUES('recorded','local','member','profile')`)
	before, _ := queued(TitleAdded)
	c.Movie(private, "/recorded/show.ts", "Private recording", 2024)
	if after, _ := queued(TitleAdded); after != before {
		t.Fatal("a private recording was reported as a new title")
	}
	if _, data := queued(TitleAdded); strings.Contains(data, "Private recording") {
		t.Fatal("a private recording's title was queued")
	}
	// Deleting a title outright reports it once, with what it was.
	run(`DELETE FROM catalog_entities WHERE id=?`, film.ID)
	expect(TitleRemoved, 1, `"title":"Film"`)
	var events int
	if err = conn.QueryRowContext(ctx, `SELECT count(*) FROM webhook_events`).Scan(&events); err != nil || events != 0 {
		t.Fatalf("events left behind: %d %v", events, err)
	}
}
