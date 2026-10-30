package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// noticeFixture builds a server with an owner account and a second, ordinary
// member account, so audience isolation can be asserted rather than assumed.
func noticeFixture(t *testing.T) (*Store, identity.Principal, identity.Principal, Authorize) {
	t.Helper()
	db, e := persistence.Open(filepath.Join(t.TempDir(), "notifications.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db)
	clock := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return clock }
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1);INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1)`); e != nil {
		t.Fatal(e)
	}
	owner := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "owner-profile", ServerID: "server", Role: "owner"}, Hash: "owner-session", Epoch: 1}
	member := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "member-profile", ServerID: "server", Role: "member"}, Hash: "member-session", Epoch: 1}
	return s, owner, member, func(context.Context, *sql.Tx, string) error { return nil }
}

func (s *Store) raiseForTest(t *testing.T, d notify.Draft) notify.Result {
	t.Helper()
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	out, e := notify.Raise(tx, s.now(), d)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return out
}

func memberDraft(p identity.Principal) notify.Draft {
	return notify.Draft{
		Audience: notify.AudienceProfile, Scope: ViewerKey(p), Severity: notify.SeverityInfo,
		Source: notify.SourceDownloads, Category: "download.finished", DedupeKey: "download:one",
		Title: "Download ready", Body: "A title finished downloading.",
		Actions: []notify.Action{{Kind: "command", Label: "Open", Command: "open-download"}},
	}
}

// A producer re-raising the same key must edit the record it already wrote,
// not stack a second one — and the edit must move the record back to unread so
// the viewer sees the restated condition.
func TestNoticeDedupeKeyUpdatesInPlace(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	first := s.raiseForTest(t, memberDraft(member))
	if !first.Created {
		t.Fatal("first raise must create")
	}
	inbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || len(inbox.Items) != 1 {
		t.Fatal(e, inbox)
	}
	applied, e := s.ApplyNotices(ctx, member, auth, NoticeBatch{OperationID: "read-one", ExpectedRevision: inbox.Revision,
		Operations: []NoticeOperation{{Action: "read", IDs: []string{first.ID}}}})
	if e != nil || applied.Counts.Unread != 0 {
		t.Fatal("read receipt", e, applied)
	}
	second := memberDraft(member)
	second.Severity, second.Category = notify.SeverityWarning, "download.failed"
	second.Title, second.Body = "Download failed", "That title could not finish."
	again := s.raiseForTest(t, second)
	if again.Created || again.ID != first.ID {
		t.Fatalf("dedupe must reuse the record: %+v vs %+v", again, first)
	}
	inbox, e = s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || len(inbox.Items) != 1 {
		t.Fatal("duplicate record", e, inbox)
	}
	item := inbox.Items[0]
	if item.Title != "Download failed" || item.Severity != notify.SeverityWarning || item.Read || inbox.Counts.Unread != 1 {
		t.Fatalf("re-raise did not restate the condition: %+v", item)
	}
	if item.Revision <= first.Revision {
		t.Fatal("re-raise must advance the revision", item.Revision, first.Revision)
	}
	// A different producer using the same key is a different record: dedupe is
	// scoped to the producer so two subsystems cannot collide.
	other := memberDraft(member)
	other.Source, other.Category = notify.SourceScan, "scan.failed"
	s.raiseForTest(t, other)
	if inbox, e = s.Notices(ctx, member, auth, NoticeQuery{}); e != nil || len(inbox.Items) != 2 {
		t.Fatal("dedupe must be per source", e, inbox)
	}
}

// The account-admin inbox belongs to administrators. A member must not see it,
// must not be able to ask for it, and must not be able to act on its records.
func TestNoticeAudienceIsolation(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	results, e := notify.NotifyScanFailure(tx, s.now(), "library-one", "source-one", "scan_root_changed", "")
	if e != nil || len(results) != 1 {
		t.Fatal("admin fan-out", e, results)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	adminNotice := results[0].ID
	memberInbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || len(memberInbox.Items) != 0 || memberInbox.Counts.Total != 0 {
		t.Fatal("member saw an administrator notice", e, memberInbox)
	}
	if _, e = s.Notices(ctx, member, auth, NoticeQuery{Audience: "account-admin"}); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("member could request the admin audience", e)
	}
	ownerInbox, e := s.Notices(ctx, owner, auth, NoticeQuery{})
	if e != nil || len(ownerInbox.Items) != 1 || ownerInbox.Items[0].Audience != notify.AudienceAccountAdmin {
		t.Fatal("owner inbox", e, ownerInbox)
	}
	// Acting on a record from another inbox reports not-found rather than
	// applying, and rather than confirming that the identifier exists at all.
	applied, e := s.ApplyNotices(ctx, member, auth, NoticeBatch{OperationID: "steal",
		Operations: []NoticeOperation{{Action: "archive", IDs: []string{adminNotice}}}})
	if e != nil || len(applied.Receipts) != 1 || applied.Receipts[0].Outcome != "not-found" || applied.Applied != 0 {
		t.Fatal("cross-inbox write", e, applied)
	}
	if _, e = s.Notices(ctx, member, auth, NoticeQuery{Audience: "nonsense"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown audience must be a 400", e)
	}
	if _, e = s.Notices(ctx, member, auth, NoticeQuery{State: "nonsense"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown state must be a 400", e)
	}
}

// A batch is fenced and idempotent: a stale revision is refused with the current
// one, and a retry of the same operation identifier replays the first answer.
func TestNoticeBatchFenceAndReplay(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	first := s.raiseForTest(t, memberDraft(member))
	second := memberDraft(member)
	second.DedupeKey, second.Category = "download:two", "download.failed"
	other := s.raiseForTest(t, second)
	inbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || inbox.Counts.Unread != 2 {
		t.Fatal(e, inbox)
	}
	batch := NoticeBatch{OperationID: "batch-one", ExpectedRevision: inbox.Revision, Operations: []NoticeOperation{
		{Action: "read", IDs: []string{first.ID}},
		{Action: "archive", IDs: []string{other.ID}},
	}}
	applied, e := s.ApplyNotices(ctx, member, auth, batch)
	if e != nil || applied.Applied != 2 || applied.Counts.Unread != 0 || applied.Counts.Archived != 1 {
		t.Fatalf("batch did not apply: %v %+v", e, applied)
	}
	if applied.Revision <= inbox.Revision {
		t.Fatal("batch must advance the revision")
	}
	replay, e := s.ApplyNotices(ctx, member, auth, batch)
	if e != nil || replay.Revision != applied.Revision || replay.Applied != applied.Applied {
		t.Fatalf("replay must be the recorded answer: %v %+v", e, replay)
	}
	// The same work under a new operation identifier is now a no-op, and a stale
	// fence is refused with the revision the client should refresh to.
	stale := NoticeBatch{OperationID: "batch-two", ExpectedRevision: inbox.Revision, Operations: batch.Operations}
	_, e = s.ApplyNotices(ctx, member, auth, stale)
	var conflict *ConflictError
	if !errors.As(e, &conflict) || conflict.CurrentRevision != applied.Revision {
		t.Fatalf("stale fence: %v", e)
	}
	stale.ExpectedRevision = applied.Revision
	repeat, e := s.ApplyNotices(ctx, member, auth, stale)
	if e != nil || repeat.Applied != 0 || repeat.Revision != applied.Revision {
		t.Fatalf("no-op batch must not advance the revision: %v %+v", e, repeat)
	}
	for _, r := range repeat.Receipts {
		if r.Outcome != "unchanged" {
			t.Fatal("outcome", r)
		}
	}
	// read-all covers the unarchived remainder and reports honestly when there is
	// nothing left to do.
	if _, e = s.ApplyNotices(ctx, member, auth, NoticeBatch{OperationID: "unread-one", ExpectedRevision: repeat.Revision,
		Operations: []NoticeOperation{{Action: "unread", IDs: []string{first.ID}}}}); e != nil {
		t.Fatal(e)
	}
	all, e := s.ApplyNotices(ctx, member, auth, NoticeBatch{OperationID: "read-all-one",
		Operations: []NoticeOperation{{Action: "read-all"}}})
	if e != nil || all.Applied != 1 || all.Counts.Unread != 0 {
		t.Fatalf("read-all: %v %+v", e, all)
	}
}

// Resuming from a revision replays exactly the records that changed since it,
// which is what makes Last-Event-ID resume exact rather than a reload hint.
func TestNoticeStreamResume(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	first := s.raiseForTest(t, memberDraft(member))
	base, e := s.NoticeChanges(ctx, member, auth, "", first.Revision)
	if e != nil || len(base.Items) != 0 || base.Resync {
		t.Fatal("nothing changed since the current revision", e, base)
	}
	second := memberDraft(member)
	second.DedupeKey = "download:two"
	later := s.raiseForTest(t, second)
	delta, e := s.NoticeChanges(ctx, member, auth, "", first.Revision)
	if e != nil || len(delta.Items) != 1 || delta.Items[0].ID != later.ID || delta.Revision != later.Revision {
		t.Fatalf("resume did not replay the new record: %v %+v", e, delta)
	}
	// A state change also moves a record's revision, so a resuming client learns
	// that a notice it already holds was read on another device.
	applied, e := s.ApplyNotices(ctx, member, auth, NoticeBatch{OperationID: "resume-read", ExpectedRevision: later.Revision,
		Operations: []NoticeOperation{{Action: "read", IDs: []string{first.ID}}}})
	if e != nil {
		t.Fatal(e)
	}
	delta, e = s.NoticeChanges(ctx, member, auth, "", later.Revision)
	if e != nil || len(delta.Items) != 1 || delta.Items[0].ID != first.ID || !delta.Items[0].Read {
		t.Fatalf("read receipt did not replay: %v %+v", e, delta)
	}
	if delta.Revision != applied.Revision {
		t.Fatal("delta revision", delta.Revision, applied.Revision)
	}
	// A client resuming from a revision so old that the replay would exceed one
	// frame is told to resync instead of receiving a truncated history.
	for i := 0; i < noticeDeltaCap+2; i++ {
		d := memberDraft(member)
		d.DedupeKey = "download:bulk-" + strconv.Itoa(i)
		s.raiseForTest(t, d)
	}
	delta, e = s.NoticeChanges(ctx, member, auth, "", later.Revision)
	if e != nil || !delta.Resync || len(delta.Items) != 0 {
		t.Fatalf("wide gap must ask for a resync: %v %+v", e, delta)
	}
}

// A long poll returns as soon as the revision moves, rather than after its full
// wait, and returns immediately when the client is already behind.
func TestNoticeLongPollWake(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	first := s.raiseForTest(t, memberDraft(member))
	done := make(chan NoticeDelta, 1)
	go func() {
		out, e := s.NoticeWait(ctx, member, auth, "", first.Revision, 5*time.Second)
		if e != nil {
			close(done)
			return
		}
		done <- out
	}()
	// Give the poll a moment to park before the producer commits.
	time.Sleep(50 * time.Millisecond)
	second := memberDraft(member)
	second.DedupeKey = "download:two"
	later := s.raiseForTest(t, second)
	WakeNotifications()
	select {
	case out, ok := <-done:
		if !ok {
			t.Fatal("long poll failed")
		}
		if out.Revision != later.Revision || len(out.Items) != 1 || out.Items[0].ID != later.ID {
			t.Fatalf("long poll did not carry the change: %+v", out)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("long poll did not wake")
	}
	// A client that is already behind must not park at all.
	start := time.Now()
	out, e := s.NoticeWait(ctx, member, auth, "", first.Revision, 3*time.Second)
	if e != nil || out.Revision != later.Revision || time.Since(start) > time.Second {
		t.Fatalf("a behind client must answer immediately: %v %+v", e, out)
	}
}

// A broadcast reaches every profile, is idempotent, and re-sending the same key
// edits the existing notice in each inbox instead of posting a second one.
func TestNoticeBroadcast(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	request := NoticeBroadcast{OperationID: "broadcast-one", Audience: notify.AudienceProfile,
		Severity: notify.SeverityWarning, DedupeKey: "maintenance-window", Title: "Maintenance tonight",
		Body: "This server restarts at 02:00.", ExpiresInDays: 2,
		Actions: []NoticeAction{{Kind: "navigate", Label: "Open settings", Target: &NoticeTarget{View: "settings"}}}}
	out, e := s.BroadcastNotice(ctx, owner, auth, request)
	if e != nil || out.Delivered != 2 || out.Created != 2 {
		t.Fatalf("broadcast: %v %+v", e, out)
	}
	memberInbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || len(memberInbox.Items) != 1 || memberInbox.Items[0].Source != notify.SourceBroadcast {
		t.Fatal("broadcast did not reach a member", e, memberInbox)
	}
	if int64(memberInbox.Items[0].ExpiresAt) != s.now()+days(2) {
		t.Fatal("broadcast expiry", memberInbox.Items[0].ExpiresAt)
	}
	replay, e := s.BroadcastNotice(ctx, owner, auth, request)
	if e != nil || replay.Delivered != out.Delivered || replay.Created != out.Created {
		t.Fatalf("broadcast replay: %v %+v", e, replay)
	}
	request.OperationID, request.Title = "broadcast-two", "Maintenance moved"
	again, e := s.BroadcastNotice(ctx, owner, auth, request)
	if e != nil || again.Created != 0 || again.Updated != 2 {
		t.Fatalf("same key must edit: %v %+v", e, again)
	}
	if memberInbox, e = s.Notices(ctx, member, auth, NoticeQuery{}); e != nil || len(memberInbox.Items) != 1 || memberInbox.Items[0].Title != "Maintenance moved" {
		t.Fatal("re-broadcast", e, memberInbox)
	}
	// Only an owner may broadcast, and an action outside the allowlist is refused.
	if _, e = s.BroadcastNotice(ctx, member, auth, request); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("member broadcast", e)
	}
	bad := request
	bad.OperationID, bad.Actions = "broadcast-three", []NoticeAction{{Kind: "command", Label: "Run", Command: "delete-everything"}}
	if _, e = s.BroadcastNotice(ctx, owner, auth, bad); !errors.Is(e, ErrInvalid) {
		t.Fatal("command allowlist", e)
	}
}

// Retention deletes expired and over-age records and tells parked clients that
// their counts moved, without any read having to trigger it.
func TestNoticeRetentionPrune(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	live := s.raiseForTest(t, memberDraft(member))
	expiring := memberDraft(member)
	expiring.DedupeKey, expiring.ExpiresAt = "download:expiring", s.now()+1000
	stale := s.raiseForTest(t, expiring)
	inbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || inbox.Counts.Total != 2 {
		t.Fatal(e, inbox)
	}
	before := inbox.Revision
	// Move past the short expiry only; the long-lived record must survive.
	s.Now = func() time.Time { return time.Date(2026, 9, 16, 12, 1, 0, 0, time.UTC) }
	if inbox, e = s.Notices(ctx, member, auth, NoticeQuery{}); e != nil || inbox.Counts.Total != 1 {
		t.Fatal("expired record must not be readable", e, inbox)
	}
	if e = s.Prune(ctx); e != nil {
		t.Fatal(e)
	}
	var remaining int
	if e = s.DB.QueryRow(`SELECT count(*) FROM notification_records`).Scan(&remaining); e != nil || remaining != 1 {
		t.Fatal("prune did not delete the expired record", e, remaining)
	}
	if inbox, e = s.Notices(ctx, member, auth, NoticeQuery{}); e != nil || len(inbox.Items) != 1 || inbox.Items[0].ID != live.ID {
		t.Fatal("prune deleted the wrong record", e, inbox)
	}
	if inbox.Revision <= before {
		t.Fatal("prune must advance the revision so a parked client re-reads")
	}
	_ = stale
	// Retention also applies to records that are not yet expired but older than
	// the configured window.
	s.Now = func() time.Time { return time.Date(2027, 9, 16, 12, 0, 0, 0, time.UTC) }
	if e = s.Prune(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.DB.QueryRow(`SELECT count(*) FROM notification_records`).Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal("retention window", e, remaining)
	}
}

// The maintenance tick raises a storage warning at the configured threshold, and
// retires it when the volume recovers.
func TestNoticeStorageThresholds(t *testing.T) {
	s, owner, _, auth := noticeFixture(t)
	ctx := context.Background()
	settings, e := s.NotificationSettings(ctx, auth)
	if e != nil || settings.RetentionSettingsField != "notificationDays" || settings.RetentionDays != 180 {
		t.Fatalf("retention must fold onto the registry field: %v %+v", e, settings)
	}
	if e = s.NotificationMaintenance(ctx, 3, 100); e != nil {
		t.Fatal(e)
	}
	inbox, e := s.Notices(ctx, owner, auth, NoticeQuery{})
	if e != nil || len(inbox.Items) != 1 || inbox.Items[0].Severity != notify.SeverityCritical {
		t.Fatalf("critical storage notice: %v %+v", e, inbox)
	}
	if e = s.NotificationMaintenance(ctx, 8, 100); e != nil {
		t.Fatal(e)
	}
	if inbox, e = s.Notices(ctx, owner, auth, NoticeQuery{}); e != nil || len(inbox.Items) != 1 || inbox.Items[0].Severity != notify.SeverityWarning {
		t.Fatalf("threshold must de-escalate in place: %v %+v", e, inbox)
	}
	if e = s.NotificationMaintenance(ctx, 90, 100); e != nil {
		t.Fatal(e)
	}
	if inbox, e = s.Notices(ctx, owner, auth, NoticeQuery{}); e != nil || len(inbox.Items) != 0 {
		t.Fatalf("recovery must retire the notice: %v %+v", e, inbox)
	}
	changed, e := s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: settings.Revision,
		StorageWarningPercent: 25, StorageCriticalPercent: 20, CertificateWarningDays: 30})
	if e != nil || changed.StorageWarningPercent != 25 {
		t.Fatal(e, changed)
	}
	if _, e = s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: settings.Revision,
		StorageWarningPercent: 25, StorageCriticalPercent: 20, CertificateWarningDays: 30}); !errors.Is(e, ErrConflict) {
		t.Fatal("settings fence", e)
	}
	if _, e = s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: changed.Revision,
		StorageWarningPercent: 10, StorageCriticalPercent: 40, CertificateWarningDays: 30}); !errors.Is(e, ErrInvalid) {
		t.Fatal("critical must sit below warning", e)
	}
}

// NotifyDownload is the documented seam the downloads workstream calls. A
// download that fails and is then retried successfully must leave one notice
// per viewer whose text and actions are replaced, not two competing ones.
func TestNotifyDownloadSeam(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	write := func(outcome, reason string) notify.Result {
		t.Helper()
		tx, e := s.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		out, e := notify.NotifyDownload(tx, s.now(), "local", "member", "member-profile", "dl-19", "The Third Man", outcome, reason)
		if e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		return out
	}
	failed := write(notify.DownloadFailed, "The source went offline.")
	inbox, e := s.Notices(ctx, member, auth, NoticeQuery{})
	if e != nil || len(inbox.Items) != 1 {
		t.Fatal(e, inbox)
	}
	item := inbox.Items[0]
	if item.Severity != notify.SeverityWarning || len(item.Actions) != 2 {
		t.Fatalf("a failed download must offer a retry: %+v", item)
	}
	if item.Actions[1].Command != "retry-job" || item.Actions[0].Command != "open-download" {
		t.Fatalf("actions: %+v", item.Actions)
	}
	done := write(notify.DownloadFinished, "")
	if done.Created || done.ID != failed.ID {
		t.Fatal("the retry must replace the failure notice, not add one")
	}
	if inbox, e = s.Notices(ctx, member, auth, NoticeQuery{}); e != nil || len(inbox.Items) != 1 {
		t.Fatal(e, inbox)
	}
	if inbox.Items[0].Severity != notify.SeverityInfo || len(inbox.Items[0].Actions) != 1 {
		t.Fatalf("success must restate the notice: %+v", inbox.Items[0])
	}
}

// The folder a scan stopped at is shown to the administrator, but a name that
// cannot be displayed safely is left out and never costs them the notice.
func TestScanFailureNoticeNamesOnlyADisplayableFolder(t *testing.T) {
	s, owner, _, auth := noticeFixture(t)
	for _, c := range []struct {
		where string
		shown bool
	}{
		{"Films/Unreadable", true},
		{"", false},
		{".", false},
		{"Films/two\nlines", false},
		{"Films/bell\a", false},
		{"Films/\u202eflipped", false},
		{strings.Repeat("long/", 100), false},
	} {
		tx, e := s.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		results, e := notify.NotifyScanFailure(tx, s.now(), "library-one", "source-one", "scan_inventory_incomplete", c.where)
		if e != nil || len(results) != 1 {
			t.Fatalf("%q: notice not raised: %v %v", c.where, e, results)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		inbox, e := s.Notices(context.Background(), owner, auth, NoticeQuery{})
		if e != nil || len(inbox.Items) != 1 {
			t.Fatal(e, inbox)
		}
		item := inbox.Items[0]
		path, present := item.Arguments["path"]
		if present != c.shown || c.shown && (path != c.where || !strings.Contains(item.Body, `"`+c.where+`"`)) || !c.shown && strings.Contains(item.Body, "It stopped at") {
			t.Fatalf("%q: body=%q arguments=%v", c.where, item.Body, item.Arguments)
		}
	}
}

func TestNoticeBroadcastPagesAndResumesBeyondFiveHundredProfiles(t *testing.T) {
	s, owner, _, auth := noticeFixture(t)
	for i := 0; i < 520; i++ {
		id := fmt.Sprintf("account-%04d", i)
		if _, err := s.DB.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, id, id, id+"-profile"); err != nil {
			t.Fatal(err)
		}
	}
	request := NoticeBroadcast{OperationID: "large-broadcast", Audience: notify.AudienceProfile, Severity: notify.SeverityInfo, DedupeKey: "server-maintenance", Title: "Maintenance", Body: "The server restarts tonight."}
	ctx, cancel := context.WithCancel(context.Background())
	pages := 0
	interrupted := func(ctx context.Context, tx *sql.Tx, item string) error {
		if err := auth(ctx, tx, item); err != nil {
			return err
		}
		pages++
		if pages == 3 {
			cancel()
			return context.Canceled
		}
		return nil
	}
	if _, err := s.BroadcastNotice(ctx, owner, interrupted, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("interruption did not pause fan-out: %v", err)
	}
	var delivered int
	if err := s.DB.QueryRow(`SELECT delivered FROM notice_broadcast_work WHERE operation_id=?`, request.OperationID).Scan(&delivered); err != nil || delivered != 100 {
		t.Fatalf("checkpoint: delivered=%d %v", delivered, err)
	}
	changed := request
	changed.Title = "Changed message"
	if _, err := s.BroadcastNotice(context.Background(), owner, auth, changed); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("in-progress operation accepted different intent: %v", err)
	}
	out, err := s.BroadcastNotice(context.Background(), owner, auth, request)
	if err != nil || out.Delivered != 522 || out.Created != 522 || out.Updated != 0 || len(out.IDs) != 50 {
		t.Fatalf("resumed fan-out: %+v %v", out, err)
	}
	replay, err := s.BroadcastNotice(context.Background(), owner, auth, request)
	if err != nil || replay.Delivered != 522 || replay.Created != 522 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM notification_records WHERE source='broadcast'`).Scan(&count); err != nil || count != 522 {
		t.Fatalf("duplicate/missing notices: %d %v", count, err)
	}
}

// Retention removes a backlog larger than one batch in bounded transactions,
// keeps what has not expired, and still clears every expired notification.
func TestRetentionClearsABacklogInBoundedBatches(t *testing.T) {
	s, _, member, _ := noticeFixture(t)
	ctx := context.Background()
	now := s.now()
	backlog := retentionBatch*3 + 5
	for i := 0; i < backlog; i++ {
		if _, e := s.DB.Exec(`INSERT INTO console_receipts(scope,key,digest,result,expires_ms) VALUES('test',?,'d','{}',?)`, fmt.Sprintf("expired-%d", i), now-1); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.DB.Exec(`INSERT INTO console_receipts(scope,key,digest,result,expires_ms) VALUES('test','live','d','{}',?)`, now+60_000); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < retentionBatch+3; i++ {
		draft := memberDraft(member)
		draft.DedupeKey, draft.ExpiresAt = fmt.Sprintf("download:expiring-%d", i), now+1000
		s.raiseForTest(t, draft)
	}
	s.Now = func() time.Time { return time.UnixMilli(now + 5000).UTC() }
	dbwork.ResetPlans()
	if e := s.Prune(ctx); e != nil {
		t.Fatal(e)
	}
	var receipts, notices int
	if e := s.DB.QueryRow(`SELECT count(*) FROM console_receipts WHERE scope='test'`).Scan(&receipts); e != nil || receipts != 1 {
		t.Fatalf("receipts left %d: %v", receipts, e)
	}
	if e := s.DB.QueryRow(`SELECT count(*) FROM notification_records WHERE expires_ms<=?`, now+5000).Scan(&notices); e != nil || notices != 0 {
		t.Fatalf("expired notices left %d: %v", notices, e)
	}
	if plans := dbwork.Plans(); plans.Overruns != 0 || plans.Largest.Rows > retentionBatch*10 {
		t.Fatalf("a retention batch was not bounded: %+v", plans)
	}
}
