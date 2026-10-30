package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func carriedAdmissionFixture(t *testing.T) (*tl11AdmissionFixture, identity.Principal) {
	t.Helper()
	f := newTL11AdmissionFixture(t, "movie")
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	f.d.Access.Access = access.New(f.db)
	f.d.Console = operations.New(f.db)
	f.d.Console.FFmpegValidation = func(context.Context, string) operations.FFmpegStatus {
		t.Fatal("byte currentness invoked tool validation")
		return operations.FFmpegStatus{}
	}
	f.d.connectionPolicy = &connectionPolicyCache{}
	if _, err = f.db.Exec(`INSERT INTO server_settings(singleton,"uploadCapacityKbps") VALUES(1,1000) ON CONFLICT(singleton) DO UPDATE SET "uploadCapacityKbps"=excluded."uploadCapacityKbps"`); err != nil {
		t.Fatal(err)
	}
	return f, p
}

func admissionChanges(t *testing.T, tx *sql.Tx) int64 {
	t.Helper()
	var n int64
	if err := tx.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCarriedPlaybackAdmissionReadsWithoutWriterToolOrAlert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, p := carriedAdmissionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	view, err := dbwork.BeginSnapshot(ctx, f.db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	bound, err := dbwork.BindSnapshot(ctx, view.Tx())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/playback/sessions", nil).WithContext(bound)
	r.RemoteAddr = "198.51.100.7:5000"
	before := admissionChanges(t, view.Tx())
	decision, err := f.d.admitPlayback(r, p, f.item.Public)
	if err != nil || !decision.UploadLimited || decision.MaxVideoBitrateBPS != uploadFloorKbps*1000 {
		t.Fatal("read admission lost its real upload clamp", decision, err)
	}
	if admissionChanges(t, view.Tx()) != before || f.d.connectionPolicy.uploadAlert.Load() {
		t.Fatal("read admission wrote or published an upload-budget alert")
	}
	// Admission must not have committed/rolled back the caller's read snapshot.
	if err = view.Tx().QueryRow(`SELECT 1`).Scan(new(int)); err != nil {
		t.Fatal("admission ended caller snapshot", err)
	}
}

func TestCarriedPlaybackAdmissionSeesUncommittedLimitsAndKeepsWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, p := carriedAdmissionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	w, err := dbwork.Begin(ctx, f.db, dbwork.ClassPlaybackStart)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	limits := access.DefaultLimits()
	limits.RemoteBitrateKbps = 800
	body, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Tx().Exec(`INSERT INTO access_limits(account_id,revision,updated_ms,body) VALUES(?,1,0,?)`, p.AccountID, string(body)); err != nil {
		t.Fatal(err)
	}
	bound, err := dbwork.BindSnapshot(ctx, w.Tx())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/playback/sessions", nil).WithContext(bound)
	r.RemoteAddr = "198.51.100.7:5000"
	before := admissionChanges(t, w.Tx())
	decision, err := f.d.admitPlayback(r, p, f.item.Public)
	if err != nil || decision.MaxVideoBitrateBPS != 800000 || !decision.UploadLimited {
		t.Fatal("accepting admission ignored uncommitted member ceiling", decision, err)
	}
	if admissionChanges(t, w.Tx()) != before || f.d.connectionPolicy.uploadAlert.Load() {
		t.Fatal("accepting admission mutated policy/alerts")
	}
	// The actual account schedule and title restrictions remain same-Tx checks.
	limits.Schedule = access.AccessSchedule{Timezone: "UTC", Windows: []access.ScheduleWindow{{Days: []int{2}, StartMinute: 600, EndMinute: 700}}}
	f.d.Access.Access.Now = func() time.Time { return time.Date(2026, 10, 26, 10, 30, 0, 0, time.UTC) }
	body, _ = json.Marshal(limits)
	if _, err = w.Tx().Exec(`UPDATE access_limits SET body=? WHERE account_id=?`, string(body), p.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.d.admitPlayback(r, p, f.item.Public); !errors.Is(err, access.ErrSchedule) {
		t.Fatal("admission missed uncommitted schedule refusal", err)
	}
	if _, err = w.Tx().Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES(?,13,0)`, p.ProfileID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.d.admitPlayback(r, p, f.item.Public); !errors.Is(err, identity.ErrContentRestricted) {
		t.Fatal("admission missed uncommitted title restriction", err)
	}
	w.Rollback()
	var count int
	if err = f.db.QueryRow(`SELECT count(*) FROM access_limits WHERE account_id=?`, p.AccountID).Scan(&count); err != nil || count != 0 {
		t.Fatal("admission committed its caller's policy write", count, err)
	}
}

func TestCarriedPlaybackAdmissionUsesOriginalOwnerStreamCeiling(t *testing.T) {
	f, p := carriedAdmissionFixture(t)
	w, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassPlaybackStart)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if _, err = w.Tx().Exec(`UPDATE playback_owner_policy SET per_account_cap=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Tx().Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms,ended_ms)
 VALUES('occupied-slot',?,?,?,?, 'occupied-key','occupied-digest','vod','local','playing','{}',1,1,?,0,0,0)`,
		p.DeviceID, p.AccountID, p.ProfileID, p.Authority, time.Now().Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	bound, err := dbwork.BindSnapshot(context.Background(), w.Tx())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/playback/sessions", nil).WithContext(bound)
	r.RemoteAddr = "198.51.100.7:5000"
	before := admissionChanges(t, w.Tx())
	if _, err = f.d.admitPlayback(r, p, f.item.Public); !errors.Is(err, access.ErrStreamLimit) {
		t.Fatal("admission ignored the accepting snapshot's owner ceiling/count", err)
	}
	if admissionChanges(t, w.Tx()) != before || f.d.connectionPolicy.uploadAlert.Load() {
		t.Fatal("refused admission wrote inside its caller's snapshot")
	}
}
