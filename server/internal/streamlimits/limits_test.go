package streamlimits

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestModesPreserveIntentAndCannotBypassServerCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('person','person',x'','main')`); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		mode    Mode
		person  int
		ceiling *int
		want    *int
	}{
		{"inherit-unlimited", Inherit, 0, nil, nil}, {"unlimited", Unlimited, 0, nil, nil},
		{"inherit-ceiling", Inherit, 0, intp(3), intp(3)}, {"unlimited-ceiling", Unlimited, 0, intp(3), intp(3)},
		{"person-narrows", Limit, 2, intp(3), intp(2)}, {"ceiling-narrows", Limit, 5, intp(3), intp(3)},
		{"person-only", Limit, 5, nil, intp(5)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = tx.Exec(`UPDATE playback_owner_policy SET per_account_cap=? WHERE singleton=1`, tc.ceiling); e != nil {
				t.Fatal(e)
			}
			if e = WriteTx(ctx, tx, "person", tc.mode, tc.person); e != nil {
				t.Fatal(e)
			}
			out, e := ReadTx(ctx, tx, "person")
			if e != nil {
				t.Fatal(e)
			}
			if out.Mode != tc.mode || !same(out.ServerCeiling, tc.ceiling) || !same(out.EffectiveLimit, tc.want) {
				t.Fatalf("facts %+v, want ceiling=%v effective=%v", out, tc.ceiling, tc.want)
			}
			if tc.mode == Limit && (out.Limit == nil || *out.Limit != tc.person) || tc.mode != Limit && out.Limit != nil {
				t.Fatalf("lost person intent: %+v", out)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, tc := range []struct {
		mode  Mode
		value int
	}{{"", 0}, {Inherit, 1}, {Unlimited, 2}, {Limit, 0}, {Limit, -1}, {Limit, 1001}} {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = WriteTx(ctx, tx, "person", tc.mode, tc.value); e == nil {
			t.Fatalf("accepted invalid mode/value: %+v", tc)
		}
		tx.Rollback()
	}
	// SQLite independently rejects a nullable explicit limit; CHECK(NULL) must
	// never silently turn a positive limit into unlimited.
	if _, e = db.Exec(`UPDATE account_stream_limits SET mode='limit',stream_limit=NULL WHERE account_id='person'`); e == nil {
		t.Fatal("database accepted NULL explicit limit")
	}
}
func intp(v int) *int     { return &v }
func same(a, b *int) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func TestSlotCountAggregatesProfilesAndDevicesButDropsExpiredLeases(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	now := time.Now()
	ctx := context.Background()
	insert := func(id, account, profile string, until, ended int64) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms,ended_ms) VALUES(?,?,?,?, 'local',?,?,'channel','member','playing','{}',1,1,?,0,0,?)`, id, "device-"+id, account, profile, id, id, until, ended)
		if e != nil {
			t.Fatal(e)
		}
	}
	insert("one", "person", "main", now.Add(time.Minute).UnixMilli(), 0)
	insert("two", "person", "second", now.Add(time.Minute).UnixMilli(), 0)
	insert("expired", "person", "third", now.Add(-time.Minute).UnixMilli(), 0)
	insert("ended", "person", "fourth", now.Add(time.Minute).UnixMilli(), now.UnixMilli())
	insert("other", "another", "main", now.Add(time.Minute).UnixMilli(), 0)
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, tc := range []struct {
		scoped  bool
		replace string
		want    int64
	}{{true, "", 2}, {true, "one", 1}, {false, "", 3}} {
		n, e := CountTx(ctx, tx, "local", "person", tc.scoped, now, tc.replace, "")
		if e != nil || n != tc.want {
			t.Fatalf("count %+v = %d %v", tc, n, e)
		}
	}
}
