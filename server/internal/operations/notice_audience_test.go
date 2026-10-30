package operations

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

func raiseScanFailure(t *testing.T, s *Store) []notify.Result {
	t.Helper()
	tx, e := s.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	out, e := notify.NotifyScanFailure(tx, s.now(), "library-one", "source-one", "scan_root_changed", "")
	if e == nil {
		_, e = notify.NotifyFeedbackSubmitted(tx, s.now(), "report-one", "playback", "video")
	}
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return out
}

func noticeSources(t *testing.T, s *Store, p identity.Principal, auth Authorize) map[string]int {
	t.Helper()
	inbox, e := s.Notices(context.Background(), p, auth, NoticeQuery{})
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]int{}
	for _, item := range inbox.Items {
		out[item.Source]++
	}
	return out
}

// Settings › General › Who sees each notice routes each server notice to the
// owner and the administrators, the owner alone, or no one. An administrator
// reads only what was routed to them; a member never reads the admin inbox; a
// narrowed choice withdraws copies already delivered.
func TestNoticeAudienceRouting(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	if _, e := s.DB.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('admin','admin',x'00','admin-profile',1)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id='admin'`); e != nil {
		t.Fatal(e)
	}
	admin := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "admin", ProfileID: "admin-profile", ServerID: "server", Role: "admin"}, Hash: "admin-session", Epoch: 1}

	settings, e := s.NotificationSettings(ctx, auth)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range notify.NoticeKinds {
		if settings.NoticeAudience[kind] != notify.NoticeOwnerAndAdmins {
			t.Fatalf("default for %s: %+v", kind, settings.NoticeAudience)
		}
	}

	if got := raiseScanFailure(t, s); len(got) != 2 {
		t.Fatalf("default routing reaches the owner and the administrator: %+v", got)
	}
	if got := noticeSources(t, s, admin, auth); got[notify.SourceScan] != 1 || got[notify.SourceFeedback] != 1 {
		t.Fatalf("administrator inbox: %+v", got)
	}
	if _, e = s.Notices(ctx, admin, auth, NoticeQuery{Audience: notify.AudienceAccountAdmin}); e != nil {
		t.Fatal("an administrator may ask for the admin audience", e)
	}
	if _, e = s.Notices(ctx, member, auth, NoticeQuery{Audience: notify.AudienceAccountAdmin}); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatal("a member must not read the admin audience", e)
	}

	changed, e := s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: settings.Revision,
		StorageWarningPercent: 10, StorageCriticalPercent: 5, CertificateWarningDays: 14,
		NoticeAudience: map[string]string{notify.NoticeScan: notify.NoticeOwnerOnly, notify.NoticeFeedback: notify.NoticeNoOne}})
	if e != nil {
		t.Fatal(e)
	}
	if changed.NoticeAudience[notify.NoticeScan] != notify.NoticeOwnerOnly || changed.NoticeAudience[notify.NoticeFeedback] != notify.NoticeNoOne ||
		changed.NoticeAudience[notify.NoticeStorage] != notify.NoticeOwnerAndAdmins {
		t.Fatalf("a partial change keeps the other kinds: %+v", changed.NoticeAudience)
	}
	if got := noticeSources(t, s, admin, auth); len(got) != 0 {
		t.Fatalf("narrowing withdraws the administrator's copies: %+v", got)
	}
	if got := noticeSources(t, s, owner, auth); got[notify.SourceScan] != 1 || got[notify.SourceFeedback] != 0 {
		t.Fatalf("owner keeps scan, loses feedback (off): %+v", got)
	}

	if got := raiseScanFailure(t, s); len(got) != 1 {
		t.Fatalf("owner-only routing reaches one inbox: %+v", got)
	}
	if got := noticeSources(t, s, admin, auth); len(got) != 0 {
		t.Fatalf("administrator received a notice routed to the owner alone: %+v", got)
	}
	if got := noticeSources(t, s, owner, auth); got[notify.SourceFeedback] != 0 {
		t.Fatalf("a notice routed to no one was delivered: %+v", got)
	}

	for name, audience := range map[string]map[string]string{
		"unknown kind":  {"backups": notify.NoticeOwnerOnly},
		"unknown value": {notify.NoticeScan: "everyone"},
	} {
		if _, e = s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: changed.Revision,
			StorageWarningPercent: 10, StorageCriticalPercent: 5, CertificateWarningDays: 14, NoticeAudience: audience}); !errors.Is(e, ErrInvalid) {
			t.Fatal(name, e)
		}
	}
}

// The spec's alert defaults (warning 10 %, critical 5 %) are a valid save: the
// critical threshold sits below the warning one.
func TestNoticeSpecThresholdDefaultsSave(t *testing.T) {
	s, owner, _, auth := noticeFixture(t)
	ctx := context.Background()
	settings, e := s.NotificationSettings(ctx, auth)
	if e != nil || settings.StorageWarningPercent != 10 || settings.StorageCriticalPercent != 5 || settings.CertificateWarningDays != 14 {
		t.Fatal(e, settings)
	}
	if _, e = s.ApplyNotificationSettings(ctx, owner, auth, NotificationSettingsChange{ExpectedRevision: settings.Revision,
		StorageWarningPercent: 10, StorageCriticalPercent: 5, CertificateWarningDays: 14}); e != nil {
		t.Fatal(e)
	}
}
