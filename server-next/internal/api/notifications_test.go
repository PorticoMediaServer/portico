package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A member reports a problem with their app's log; the owner reads it, downloads the log and
// replies; the reply arrives in the member's notifications and in their own list of reports.
// The owner messages everyone. Nobody else reads the reports or messages everyone.
func TestReportsRepliesAndNotices(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	addMember(t, s, owner.AccessToken, "ada", "admin")
	addMember(t, s, owner.AccessToken, "max", "member")
	admin := signInAs(t, s, "ada", testPassword, 1)
	member := signInAs(t, s, "max", testPassword, 2)

	var report ProblemReport
	body := ReportCreate{Kind: "problem", Message: "The sound drops out", Context: JSON(`{"title":"Heat","device":"Phone"}`), Log: "line one\nline two\n"}
	if w := call(t, s, "POST", "/v1/reports", member.AccessToken, body, &report); w.Code != 200 || !report.HasLog || report.Reporter != "max" {
		t.Fatalf("sending a report answered %d: %s", w.Code, w.Body)
	}
	if report.DeviceName != "Device 2" {
		t.Errorf("the report names the device %q, not the one its sign-in is on", report.DeviceName)
	}
	var devices Page[Device]
	call(t, s, "GET", "/v1/devices", member.AccessToken, nil, &devices)
	for _, d := range devices.Items {
		if d.Current {
			if code := status(t, s, "PATCH", "/v1/devices/"+d.ID, member.AccessToken, DeviceEdit{Name: "Kitchen TV"}); code != 200 && code != 204 {
				t.Fatalf("renaming the device answered %d", code)
			}
		}
	}
	if code := status(t, s, "GET", "/v1/admin/reports", admin.AccessToken, nil); code != 403 {
		t.Errorf("an administrator read the reports: %d", code)
	}
	var list Page[ProblemReport]
	if call(t, s, "GET", "/v1/admin/reports?status=open", owner.AccessToken, nil, &list); len(list.Items) != 1 || list.Items[0].ID != report.ID {
		t.Fatalf("the owner's open reports: %+v", list)
	}
	if list.Items[0].DeviceName != "Kitchen TV" {
		t.Errorf("the owner sees the device as %q, not by its current name", list.Items[0].DeviceName)
	}
	if w := call(t, s, "GET", "/v1/admin/reports/"+report.ID+"/log", owner.AccessToken, nil, nil); w.Code != 200 || w.Body.String() != body.Log {
		t.Errorf("the log answered %d: %q", w.Code, w.Body)
	}
	if w := call(t, s, "GET", "/v1/admin/reports/"+report.ID+"/log", member.AccessToken, nil, nil); w.Code != 403 {
		t.Errorf("the member downloaded the log: %d", w.Code)
	}
	if code := status(t, s, "GET", "/v1/admin/reports/"+report.ID+"/log", admin.AccessToken, nil); code != 403 {
		t.Errorf("an administrator downloaded the log: %d", code)
	}
	resolved, reply := "resolved", "Fixed in the next version."
	if code := status(t, s, "PATCH", "/v1/admin/reports/"+report.ID, owner.AccessToken, ReportEdit{Status: &resolved, Reply: &reply}); code != 200 {
		t.Fatalf("replying answered %d", code)
	}
	var inbox Notifications
	call(t, s, "GET", "/v1/notifications", member.AccessToken, nil, &inbox)
	if inbox.UnreadCount != 1 || len(inbox.Items) != 1 || inbox.Items[0].Kind != "report_reply" || inbox.Items[0].Ref != report.ID {
		t.Fatalf("the member's inbox after the reply: %+v", inbox)
	}
	var mine Page[ProblemReport]
	if call(t, s, "GET", "/v1/reports", member.AccessToken, nil, &mine); len(mine.Items) != 1 || mine.Items[0].Reply != reply || mine.Items[0].Status != "resolved" {
		t.Errorf("the member's own reports: %+v", mine)
	}
	yes := true
	if code := status(t, s, "PATCH", "/v1/notifications/"+inbox.Items[0].ID, member.AccessToken, NotificationEdit{Read: &yes}); code != 204 {
		t.Fatalf("marking read answered %d", code)
	}
	if code := status(t, s, "PATCH", "/v1/notifications/"+inbox.Items[0].ID, admin.AccessToken, NotificationEdit{Read: &yes}); code != 404 {
		t.Errorf("another account marked the member's notice: %d", code)
	}

	if code := status(t, s, "POST", "/v1/admin/notifications", admin.AccessToken, ServerMessage{Audience: "everyone", Message: "Hi"}); code != 403 {
		t.Errorf("an administrator messaged everyone: %d", code)
	}
	if code := status(t, s, "POST", "/v1/admin/notifications", member.AccessToken, ServerMessage{Audience: "admins", Message: "Hi"}); code != 403 {
		t.Errorf("a member sent a server notice: %d", code)
	}
	var sent ServerMessageSent
	if w := call(t, s, "POST", "/v1/admin/notifications", owner.AccessToken, ServerMessage{Audience: "everyone", Message: "Down for an hour at six"}, &sent); w.Code != 200 || sent.Sent != 3 {
		t.Fatalf("messaging everyone answered %d: %s", w.Code, w.Body)
	}
	for name, token := range map[string]string{"member": member.AccessToken, "admin": admin.AccessToken} {
		var unread Notifications
		call(t, s, "GET", "/v1/notifications?view=unread", token, nil, &unread)
		if unread.UnreadCount != 1 || !strings.Contains(unread.Items[0].Body, "six") {
			t.Errorf("the %s's unread notices: %+v", name, unread)
		}
	}
	status(t, s, "POST", "/v1/notifications/read-all", member.AccessToken, nil)
	var all Notifications
	if call(t, s, "GET", "/v1/notifications?view=all", member.AccessToken, nil, &all); all.UnreadCount != 0 || len(all.Items) != 2 {
		t.Errorf("after mark all read: %+v", all)
	}
	if code := status(t, s, "DELETE", "/v1/admin/reports/"+report.ID, owner.AccessToken, nil); code != 204 {
		t.Errorf("deleting the report answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/admin/reports/"+report.ID+"/log", owner.AccessToken, nil); code != 404 {
		t.Errorf("a deleted report's log answered %d", code)
	}
}

// One profile sends at most 20 reports a day, and a report past that writes no app log to the
// disk: a phone whose app crashes in a loop cannot fill the server's disk.
func TestReportsPerDayAreCapped(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s)
	for n := range 20 {
		if code := status(t, s, "POST", "/v1/reports", owner.AccessToken, ReportCreate{Kind: "crash", Log: "crash " + string(rune('a'+n))}); code != 200 {
			t.Fatalf("report %d answered %d", n+1, code)
		}
	}
	before, _ := os.ReadDir(filepath.Join(s.dataDir, "reports"))
	w := call(t, s, "POST", "/v1/reports", owner.AccessToken, ReportCreate{Kind: "crash", Log: "one too many"}, nil)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "too_many_reports") {
		t.Fatalf("the 21st report answered %d: %s", w.Code, w.Body)
	}
	if after, _ := os.ReadDir(filepath.Join(s.dataDir, "reports")); len(after) != len(before) {
		t.Errorf("a refused report wrote its log: %d logs, then %d", len(before), len(after))
	}
}

// Crash attachments follow their report's 30-day lifetime; ordinary problem reports remain.
func TestCrashReportsAndTheirAppLogsExpireTogether(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	var crash, problem ProblemReport
	call(t, th.s, "POST", "/v1/reports", token, ReportCreate{Kind: "crash", Log: "crash diagnostics"}, &crash)
	call(t, th.s, "POST", "/v1/reports", token, ReportCreate{Kind: "problem", Message: "No sound", Log: "problem diagnostics"}, &problem)
	th.clock.Add(crashReportsKept + time.Second)
	if err := th.s.TidyReports(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := th.s.db.ReportByID(context.Background(), crash.ID); !errors.Is(err, db.ErrNotFound) {
		t.Fatalf("expired crash read: %v", err)
	}
	kept, err := th.s.db.ReportByID(context.Background(), problem.ID)
	if err != nil || kept.LogHash == "" {
		t.Fatalf("problem report disappeared: %v", err)
	}
	files, err := os.ReadDir(filepath.Join(th.s.dataDir, "reports"))
	if err != nil || len(files) != 1 || files[0].Name() != kept.LogHash+".log" {
		t.Fatal("the crash's log did not expire with its report")
	}
}
