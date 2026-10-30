package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func consoleFixture(t *testing.T) (*Store, identity.Principal, Authorize) {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "console.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	s.Now = func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) }
	owner := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "primary", ServerID: "server", Role: "owner"}, Hash: "owner-session", Epoch: 1}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','primary')`); e != nil {
		t.Fatal(e)
	}
	return s, owner, func(context.Context, *sql.Tx, string) error { return nil }
}

func TestOperationRevisionAppendsOneSharedEvent(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	scheduler := NewScheduler(s)
	if err := scheduler.Register(Adapter{Kind: "cleanup", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(context.Context, string) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job, err := scheduler.Enqueue(ctx, owner, authorize, RunJob{Kind: "cleanup", IdempotencyKey: "event-enqueue"})
	if err != nil {
		t.Fatal(err)
	}
	assertEvent := func(want string) {
		t.Helper()
		var count int
		if err := s.DB.QueryRow(`SELECT count(*) FROM api_events WHERE type='operation.updated' AND resource_id=? AND revision=?`, job.ID, want).Scan(&count); err != nil || count != 1 {
			t.Fatalf("revision %s event count %d: %v", want, count, err)
		}
	}
	assertEvent("1")
	job, err = scheduler.Command(ctx, owner, authorize, job.ID, "cancel", JobCommand{ExpectedRevision: job.Revision, IdempotencyKey: "event-cancel"})
	if err != nil || job.Revision != 2 {
		t.Fatalf("cancelled revision: %+v %v", job, err)
	}
	assertEvent("2")
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM api_events WHERE type='operation.updated' AND resource_id=?`, job.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate operation events: %d %v", count, err)
	}
}
func TestConsoleSettingsCASAndReplay(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	before, e := s.Settings(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	if before.Effective.ServerCap != nil || before.Effective.PerAccountCap != nil {
		t.Fatal("default must be unlimited")
	}
	cap := 3
	v := before.Requested
	v.ServerCap = &cap
	v.ConversionMode = "never"
	v.Name = "Local home"
	change := SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "settings-one", Values: v}
	after, e := s.ApplySettings(ctx, p, auth, change)
	if e != nil {
		t.Fatal(e)
	}
	if after.ActiveRevision != after.Revision || after.Revision != before.Revision+1 || after.Effective.TranscodingEnabled || *after.Effective.ServerCap != 3 {
		t.Fatalf("not effective: %+v", after)
	}
	replay, e := s.ApplySettings(ctx, p, auth, change)
	if e != nil || replay.Revision != after.Revision {
		t.Fatal("replay", e)
	}
	change.IdempotencyKey = "settings-two"
	_, e = s.ApplySettings(ctx, p, auth, change)
	var conflict *ConflictError
	if !errors.As(e, &conflict) || conflict.CurrentRevision != after.Revision {
		t.Fatalf("missing current revision: %v", e)
	}
	var enabled, policyCap int
	if e = s.DB.QueryRow(`SELECT transcoding_enabled,server_cap FROM playback_owner_policy`).Scan(&enabled, &policyCap); e != nil || enabled != 0 || policyCap != 3 {
		t.Fatal("disconnected runtime policy", e)
	}
	var count int
	s.DB.QueryRow(`SELECT count(*) FROM console_audit WHERE action='settings.apply'`).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate audit", count)
	}
}
func TestConsolePreferenceScopeIsolationAndViewerIsolation(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	_, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "profile", Values: PreferencePatch{"privacy.pauseWatchHistory": true}})
	if e != nil {
		t.Fatal(e)
	}
	result, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "device", Values: PreferencePatch{"appearance.cardSizePercent": 125}})
	if e != nil || !(result.Effective.Int("appearance.cardSizePercent") == 125) || !result.Effective.Bool("privacy.pauseWatchHistory") {
		t.Fatalf("scopes did not merge: %v %+v", e, result.Effective)
	}
	// A device-class document belongs to that class alone; the profile document
	// is shared across every class.
	result, e = s.Preferences(ctx, p, "television", a)
	if e != nil || (result.Effective.Int("appearance.cardSizePercent") == 125) || !result.Effective.Bool("privacy.pauseWatchHistory") {
		t.Fatalf("device document leaked across classes: %v %+v", e, result.Effective)
	}
	other := p
	other.AccountID = "other"
	other.Hash = "other-session"
	result, e = s.Preferences(ctx, other, "web", a)
	if e != nil || result.Effective.Bool("privacy.pauseWatchHistory") || (result.Effective.Int("appearance.cardSizePercent") == 125) {
		t.Fatal("profile alias leaked", e)
	}
	// Clearing an override falls back to the registry default, not to false-by-omission.
	result, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "clear", Values: PreferencePatch{"privacy.pauseWatchHistory": nil}})
	if e != nil || result.Effective.Bool("privacy.pauseWatchHistory") || result.EffectiveSource["privacy.pauseWatchHistory"] != "default" {
		t.Fatalf("clear did not restore the default: %v %+v", e, result.EffectiveSource)
	}
}
func TestConsoleOwnerAlertsDeduplicateAndReopen(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if e := s.Alert(ctx, "scan-failed", "warning", true); e != nil {
			t.Fatal(e)
		}
	}
	inbox, e := s.Inbox(ctx, p, a, "", 40)
	if e != nil || len(inbox.Items) != 1 || inbox.Items[0].Code != "owner-alert" || inbox.Items[0].TargetID == "" {
		t.Fatalf("alert inbox: %+v %v", inbox, e)
	}
	alerts, e := s.Alerts(ctx, a)
	if e != nil || len(alerts) != 1 || alerts[0].Occurrences != 2 {
		t.Fatal(e)
	}
	cmd := JobCommand{ExpectedRevision: alerts[0].Revision, IdempotencyKey: "ack"}
	for i := 0; i < 2; i++ {
		if e = s.Acknowledge(ctx, p, a, alerts[0].ID, cmd); e != nil {
			t.Fatal("ack replay", e)
		}
	}
	if e = s.Alert(ctx, "scan-failed", "warning", false); e != nil {
		t.Fatal(e)
	}
	if e = s.Alert(ctx, "scan-failed", "warning", true); e != nil {
		t.Fatal(e)
	}
	inbox, e = s.Inbox(ctx, p, a, "", 40)
	if e != nil || len(inbox.Items) != 2 {
		t.Fatal("reopened alert missing", e)
	}
}
func TestConsoleDiagnosticsExpiryAndAuditBoundary(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	now := s.Now()
	if e := s.Record(ctx, "runtime", "info", "scheduler", "processed", map[string]int64{"processed": 5}); e != nil {
		t.Fatal(e)
	}
	if e := s.Record(ctx, "client", "info", "client", "upload", map[string]int64{"secret": 1}); !errors.Is(e, ErrInvalid) {
		t.Fatal("arbitrary field allowed", e)
	}
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = Audit(tx, s.now(), "actor", "test.action", "opaque", 1); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE console_audit SET action='rewritten'`); e == nil {
		t.Fatal("audit update was not fenced")
	}
	out, e := s.CreateExport(ctx, p, a, false, ExportRequest{IdempotencyKey: "export-one", From: apiwire.Instant(s.now() - 1000), To: apiwire.Instant(s.now()), Components: []string{"runtime", "audit"}})
	if e != nil {
		t.Fatal(e)
	}
	var expiry int64
	if e = s.DB.QueryRow(`SELECT expires_ms FROM console_receipts WHERE scope=?`, "export:"+p.Hash).Scan(&expiry); e != nil || expiry != int64(out.ExpiresAt) {
		t.Fatal("export bytes outlive artifact", e)
	}
	var last string
	s.DB.QueryRow(`SELECT hash FROM console_audit ORDER BY sequence DESC LIMIT 1`).Scan(&last)
	s.Now = func() time.Time { return now.Add(91 * 24 * time.Hour) }
	if e = s.Prune(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Export(ctx, p, a, out.ID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("export retained", e)
	}
	tx, e = s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = Audit(tx, s.now(), "actor", "after.prune", "opaque", 2); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var prev string
	s.DB.QueryRow(`SELECT previous_hash FROM console_audit ORDER BY sequence DESC LIMIT 1`).Scan(&prev)
	if prev != last {
		t.Fatal("prune broke chain", prev, last)
	}
}
func TestConsoleJobCancelWaitsForDomainQuiescenceAndRetryLinks(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	scheduler := NewScheduler(s)
	quiescent := false
	processed := int64(7)
	if e := scheduler.Register(Adapter{Kind: "test-worker", Lane: "background-media", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, StartTx: func(context.Context, *sql.Tx, string, string) (string, error) { return "domain-one", nil }, Observe: func(context.Context, string) (JobObservation, error) {
		state := "running"
		if quiescent {
			state = "cancelled"
		}
		return JobObservation{State: state, Phase: state, Processed: &processed}, nil
	}, Cancel: func(context.Context, string) error { return nil }}); e != nil {
		t.Fatal(e)
	}
	j, e := scheduler.Enqueue(ctx, p, a, RunJob{Kind: "test-worker", Resource: "resource", IdempotencyKey: "run"})
	if e != nil {
		t.Fatal(e)
	}
	scheduler.advance(ctx, j)
	list, e := scheduler.Jobs(ctx, a, "")
	if e != nil {
		t.Fatal(e)
	}
	j = list.Items[0]
	scheduler.advance(ctx, j)
	list, _ = scheduler.Jobs(ctx, a, "")
	j = list.Items[0]
	if j.Processed == nil || *j.Processed != 7 {
		t.Fatal("producer observation lost")
	}
	j, e = scheduler.Command(ctx, p, a, j.ID, "cancel", JobCommand{ExpectedRevision: j.Revision, IdempotencyKey: "cancel"})
	if e != nil {
		t.Fatal(e)
	}
	scheduler.advance(ctx, j)
	list, _ = scheduler.Jobs(ctx, a, "")
	if list.Items[0].State != "cancellation-requested" {
		t.Fatal("cancelled before quiescence")
	}
	quiescent = true
	scheduler.advance(ctx, list.Items[0])
	list, _ = scheduler.Jobs(ctx, a, "")
	j = list.Items[0]
	if j.State != "cancelled" {
		t.Fatal(j.State)
	}
	retry, e := scheduler.Command(ctx, p, a, j.ID, "retry", JobCommand{ExpectedRevision: j.Revision, IdempotencyKey: "retry"})
	if e != nil || retry.ID == j.ID || retry.Predecessor != j.ID {
		t.Fatal("retry not linked", e)
	}
}
func TestConsoleScheduleSlotCalendarWindows(t *testing.T) {
	v := Schedule{Enabled: true, Timezone: "America/Toronto", StartMinute: 150, WindowMinutes: 60, CatchUp: true}
	now, _ := time.Parse(time.RFC3339, "2026-03-08T07:10:00Z")
	slot, ok := ScheduleSlot(v, now)
	if !ok || slot != "2026-03-08" {
		t.Fatal("spring forward catch-up", slot, ok)
	}
	v.LastSlot = slot
	if _, ok = ScheduleSlot(v, now); ok {
		t.Fatal("duplicate slot")
	}
	v.LastSlot = "2026-03-09"
	if _, ok = ScheduleSlot(v, now); ok {
		t.Fatal("clock rollback reran old slot")
	}
	v.LastSlot = ""
	v.StartMinute = 90
	now, _ = time.Parse(time.RFC3339, "2026-11-01T05:30:00Z")
	slot, ok = ScheduleSlot(v, now)
	if !ok {
		t.Fatal("fold first hour")
	}
	v.LastSlot = slot
	now = now.Add(time.Hour)
	if _, ok = ScheduleSlot(v, now); ok {
		t.Fatal("fold duplicate")
	}
	v.LastSlot = ""
	v.Timezone = "UTC"
	v.StartMinute = 23*60 + 30
	v.WindowMinutes = 90
	now = time.Date(2026, 9, 7, 0, 15, 0, 0, time.UTC)
	slot, ok = ScheduleSlot(v, now)
	if !ok || slot != "2026-09-06" {
		t.Fatal("overnight slot", slot)
	}
	v.CatchUp = false
	if _, ok = ScheduleSlot(v, now); ok {
		t.Fatal("catch-up ignored")
	}
}
func TestConsoleStrictValidationAndAuditPayload(t *testing.T) {
	if SafeText("token\u202elink", 100) || SafeText("\x00", 100) || validFeedbackClient(&FeedbackClient{Platform: "https://url", AppVersion: "1.0"}) {
		t.Fatal("unsafe typed input")
	}
	v := DefaultSettings()
	v.Name = "Home"
	if e := validateSettings(&v); e != nil {
		t.Fatal(e)
	}
	v.Name = "wrong\nname"
	var fields *ValidationError
	if e := validateSettings(&v); !errors.As(e, &fields) {
		t.Fatal("missing field error", e)
	}
	encoded, _ := json.Marshal(fields)
	if !strings.Contains(string(encoded), "values.name") {
		t.Fatal(string(encoded))
	}
}

func TestConsoleUnavailableScheduleDoesNotStarveOthersAndCanBeDisabled(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	scheduler := NewScheduler(s)
	if e := scheduler.Register(Adapter{Kind: "cleanup", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(context.Context, string) error { return nil }}); e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct{ id, kind string }{{"a-missing", "removed-worker"}, {"b-good", "cleanup"}} {
		if _, e := s.DB.Exec(`INSERT INTO console_schedules VALUES(?,1,?,'',1,'UTC',0,1440,1,'',?)`, entry.id, entry.kind, s.now()); e != nil {
			t.Fatal(e)
		}
	}
	if e := scheduler.admitSchedules(ctx); e == nil {
		t.Fatal("missing adapter must be reported")
	}
	var count int
	if e := s.DB.QueryRow(`SELECT count(*) FROM console_operations WHERE kind='cleanup'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("good schedule starved", count, e)
	}
	_, e := scheduler.SaveSchedule(ctx, p, auth, ScheduleChange{ExpectedRevision: 1, IdempotencyKey: "disable-missing", Value: Schedule{ID: "a-missing", Kind: "removed-worker", Resource: "", Enabled: false, Timezone: "UTC", StartMinute: 0, WindowMinutes: 1440, CatchUp: true}})
	if e != nil {
		t.Fatal("cannot disable missing adapter", e)
	}
	if e = scheduler.admitSchedules(ctx); e != nil {
		t.Fatal("disabled schedule still blocks admission", e)
	}
}

// Domain settings endpoints and the console must share an optimistic-write fence.
func TestConsoleRejectsStaleSettingsAfterLegacyWriter(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	before, e := s.Settings(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`INSERT INTO configuration VALUES('name','Changed elsewhere') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); e != nil {
		t.Fatal(e)
	}
	_, e = s.ApplySettings(ctx, p, auth, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "stale-legacy", Values: before.Requested})
	var conflict *ConflictError
	if !errors.As(e, &conflict) || conflict.CurrentRevision <= before.Revision {
		t.Fatal("legacy write bypassed console fence", e)
	}
	current, e := s.Settings(ctx, auth)
	if e != nil || current.Effective.Name != "Changed elsewhere" {
		t.Fatal("name not authoritative", e)
	}
	if _, e = s.DB.Exec(`UPDATE playback_owner_policy SET per_account_cap=2,revision=revision+1 WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	after, e := s.Settings(ctx, auth)
	if e != nil || after.Effective.PerAccountCap == nil || *after.Effective.PerAccountCap != 2 || after.Revision <= current.Revision {
		t.Fatal("domain policy not authoritative", e)
	}
}

// The "require approval for new devices" switch is the identity service's own
// value: saving the switch writes the value device registration reads, and a
// change made on the identity side is what the switch then shows.
func TestActivityAlertParamsAreSourceBackedAndBounded(t *testing.T) {
	s, _, auth := consoleFixture(t)
	ctx := context.Background()
	params := map[string]string{"title": "Library scan", "reason": "source-unavailable", "time": "2026-10-22T19:00:00Z"}
	if e := s.Alert(ctx, "job-failed-library-scan", "warning", true, params); e != nil {
		t.Fatal(e)
	}
	out, e := s.Alerts(ctx, auth)
	if e != nil || len(out) != 1 || out[0].Params["reason"] != "source-unavailable" || out[0].Params["title"] != "Library scan" {
		t.Fatal(out, e)
	}
	for _, bad := range []map[string]string{{"email": "private@example.invalid"}, {"token": "secret"}, {"reason": "raw diagnostic\nsecret"}, {"time": "yesterday"}, {"title": strings.Repeat("x", 4097)}} {
		if e = s.Alert(ctx, "job-failed-library-scan", "warning", true, bad); e == nil {
			t.Fatal("private/unbounded params accepted", bad)
		}
	}
	if e = s.Alert(ctx, "job-failed-library-scan", "warning", false); e != nil {
		t.Fatal(e)
	}
	if e = s.Alert(ctx, "job-failed-library-scan", "warning", true, map[string]string{"reason": "another-failure"}); e != nil {
		t.Fatal(e)
	}
	out, e = s.Alerts(ctx, auth)
	if e != nil || out[0].Params["title"] != "" || out[0].Params["reason"] != "another-failure" {
		t.Fatal("old failure params retained", out, e)
	}
}

func TestActivityProgressHintsBoundCadenceAndNeverCarryPrivateJobData(t *testing.T) {
	s, _, _ := consoleFixture(t)
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for n := 0; n < 20; n++ {
		if e = AppendActivityProgressTx(tx, "job.updated", "job", "job", fmt.Sprint(n), false); e != nil {
			t.Fatal(e)
		}
	}
	if e = AppendActivityProgressTx(tx, "job.updated", "job", "job", "terminal", true); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = tx.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated' AND data=''`).Scan(&count); e != nil || count != 2 {
		t.Fatal("unbounded progress or missing terminal hint", count, e)
	}
}

func TestConsoleBackupAlertUsesExistingEmptyAdminHints(t *testing.T) {
	s, _, _ := consoleFixture(t)
	ctx := context.Background()
	for _, active := range []bool{true, true, false, false} {
		if err := s.Alert(ctx, "backup-failed", "warning", active); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='server.updated' AND resource_kind='alert' AND data=''`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestActivityTaskTotalFollowsProducerAndInvalidObservationRollsBack(t *testing.T) {
	store, owner, auth := consoleFixture(t)
	scheduler := NewScheduler(store)
	processed, total := int64(2), int64(8)
	observation := JobObservation{State: "running", Phase: "analysis", Processed: &processed}
	if err := scheduler.Register(Adapter{Kind: "total-test", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Step: func(context.Context, string) (JobObservation, error) { return observation, nil }}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	job, err := scheduler.Enqueue(ctx, owner, auth, RunJob{Kind: "total-test", IdempotencyKey: "known-total"})
	if err != nil {
		t.Fatal(err)
	}
	read := func() Job {
		t.Helper()
		page, err := scheduler.Jobs(ctx, auth, "")
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		return page.Items[0]
	}
	scheduler.advance(ctx, job)
	job = read()
	if job.Total != nil || job.Processed == nil || *job.Processed != 2 {
		t.Fatal("unknown total was invented", job)
	}
	observation.Total = &total
	scheduler.advance(ctx, job)
	job = read()
	if job.Total == nil || *job.Total != 8 || *job.Processed != 2 {
		t.Fatal("known producer total lost", job)
	}
	revision := job.Revision
	tooSmall := int64(1)
	observation.Total = &tooSmall
	scheduler.advance(ctx, job)
	job = read()
	if job.Revision != revision || job.Total == nil || *job.Total != 8 {
		t.Fatal("invalid producer count partially committed", job)
	}
	observation.Total = nil
	scheduler.advance(ctx, job)
	if job = read(); job.Total != nil {
		t.Fatal("unknown producer total retained a stale value", job)
	}
}
