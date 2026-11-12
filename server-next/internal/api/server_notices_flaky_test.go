package api

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
)

// From review desk A, #2688. A remote source's stored state follows every single request to it
// (remote.Registry.found): one request that times out makes it "unreachable" and the next
// that works makes it "ok". On a poor link, or during a scan of a cloud source, the state
// therefore changes many times an hour, and a look once a minute finds it now one way, now
// the other. That is one problem to the owner, and it must not fill the inbox: an hour in
// which the source is found unreachable at every other look sends a notice or two, not 30.
func TestReviewAFlakySourceDoesNotFillTheInbox(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	// This machine's own free space must not raise the storage alert here.
	if code := status(t, s, "PUT", "/v1/admin/settings/storage", owner, json.RawMessage(`{"warnPercent":1,"majorPercent":1}`)); code != 200 {
		t.Fatalf("setting the storage warnings answered %d", code)
	}
	nas, err := s.db.CreateRemoteSource(ctx, "src_flaky", db.NewRemoteSource{UserID: 1, Kind: "webdav", Name: "NAS", Config: "{}", Secret: []byte("sealed"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	s.setClock(func() time.Time { return clock })
	for minute := range 60 {
		state, problem := "ok", ""
		if minute%2 == 0 {
			state, problem = "unreachable", "read tcp: i/o timeout"
		}
		if err := s.db.SetRemoteSourceState(ctx, nas.ID, state, problem); err != nil {
			t.Fatal(err)
		}
		if err := s.tellNewAlerts(ctx); err != nil {
			t.Fatal(err)
		}
		clock = clock.Add(time.Minute)
	}
	s.setClock(time.Now) // the owner's sign-in is read on the real clock
	var inbox Notifications
	if w := call(t, s, "GET", "/v1/notifications?view=all&limit=200", owner, nil, &inbox); w.Code != 200 {
		t.Fatalf("the inbox answered %d: %s", w.Code, w.Body)
	}
	told := 0
	for _, n := range inbox.Items {
		if n.Kind == "remote_unreachable" {
			told++
		}
	}
	if told > 3 {
		t.Errorf("one flaky hour sent the owner %d notices about the same source (the first page of the inbox alone); want a notice or two", told)
	}
}

// Free space sitting at the warning line is one notice: once told, low storage counts as gone
// only when free space is storageMargin points above the line.
func TestFreeSpaceAtTheLineIsOneNotice(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	total, _ := diskspace.Total(s.dataDir)
	free, _ := diskspace.Free(s.dataDir)
	if total <= 0 {
		t.Skip("this machine does not report its disk's size")
	}
	pct := int(free * 100 / total)
	if pct < 2 || pct > 48 {
		t.Skipf("this disk is %d%% free; the warning line cannot be put either side of it", pct)
	}
	line := func(warn int) {
		t.Helper()
		body := fmt.Sprintf(`{"warnPercent":%d,"majorPercent":1}`, warn)
		if code := status(t, s, "PUT", "/v1/admin/settings/storage", owner, json.RawMessage(body)); code != 200 {
			t.Fatalf("setting the storage warning to %d answered %d", warn, code)
		}
	}
	clock := time.Now()
	look := func(after time.Duration) {
		t.Helper()
		clock = clock.Add(after)
		s.setClock(func() time.Time { return clock })
		defer func() { s.setClock(time.Now) }()
		if err := s.tellNewAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Free space just under the line, then just over it for an hour, then under it again.
	line(pct + 1)
	look(0)
	look(noticeAfter)
	line(pct - 1)
	look(wellAfter)
	look(wellAfter)
	line(pct + 1)
	look(noticeAfter)
	look(noticeAfter)
	var inbox Notifications
	if w := call(t, s, "GET", "/v1/notifications?view=all", owner, nil, &inbox); w.Code != 200 {
		t.Fatalf("the inbox answered %d: %s", w.Code, w.Body)
	}
	told := 0
	for _, n := range inbox.Items {
		if n.Kind == "storage_nearly_full" {
			told++
		}
	}
	if told != 1 {
		t.Errorf("free space at the warning line sent %d notices; want 1", told)
	}
}

// A failed scheduled backup is told once its 4 hours of retries are used up (console rule 55),
// at the first look after that and with no wait; not again while it lasts; and again after a
// backup has succeeded and a later one has failed for as long.
func TestAFailedBackupIsToldOnceItsRetriesAreUsedUp(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	// This machine's own free space must not raise the storage alert here.
	if code := status(t, s, "PUT", "/v1/admin/settings/storage", owner, json.RawMessage(`{"warnPercent":1,"majorPercent":1}`)); code != 200 {
		t.Fatalf("setting the storage warnings answered %d", code)
	}
	folder := func(path string) {
		t.Helper()
		if code := status(t, s, "PUT", "/v1/admin/settings/backups", owner, json.RawMessage(`{"folder":"`+path+`"}`)); code != 200 {
			t.Fatalf("setting the backup folder answered %d", code)
		}
	}
	scheduled := func() {
		t.Helper()
		if _, err := s.Backups().Start(true); err != nil {
			t.Fatal(err)
		}
		for s.Backups().Running() != nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	clock := time.Now()
	look := func(after time.Duration) {
		t.Helper()
		clock = clock.Add(after)
		s.setClock(func() time.Time { return clock })
		defer s.setClock(time.Now)
		if err := s.tellNewAlerts(ctx); err != nil {
			t.Fatal(err)
		}
	}
	told := func() int {
		t.Helper()
		var inbox Notifications
		if w := call(t, s, "GET", "/v1/notifications?view=all", owner, nil, &inbox); w.Code != 200 {
			t.Fatalf("the inbox answered %d: %s", w.Code, w.Body)
		}
		n := 0
		for _, item := range inbox.Items {
			if item.Kind == "backup_failed" {
				n++
			}
		}
		return n
	}

	// A scheduled backup into a folder that is not there: tried again quietly at first.
	folder(filepath.Join(t.TempDir(), "unmounted", "backups"))
	scheduled()
	look(time.Minute)
	if n := told(); n != 0 {
		t.Fatalf("a first failure was told %d times; it is tried again for 4 hours first", n)
	}
	// The retries are used up: told at the next look, without the five minutes other causes wait.
	s.Backups().GiveUp()
	look(time.Minute)
	if n := told(); n != 1 {
		t.Fatalf("after the retries were used up the owner was told %d times; want 1", n)
	}
	for range 5 {
		look(10 * time.Minute)
	}
	if n := told(); n != 1 {
		t.Fatalf("a failure that lasts was told %d times; want 1", n)
	}
	// A backup succeeds, and later another fails for as long.
	folder(t.TempDir())
	scheduled()
	if s.Backups().Failed() != nil {
		t.Fatal("a successful backup left the failure standing")
	}
	look(wellAfter)
	look(time.Minute)
	folder(filepath.Join(t.TempDir(), "unmounted", "backups"))
	scheduled()
	s.Backups().GiveUp()
	look(time.Minute)
	if n := told(); n != 2 {
		t.Fatalf("a later failure, after a success, was told %d times in all; want 2", n)
	}
}
