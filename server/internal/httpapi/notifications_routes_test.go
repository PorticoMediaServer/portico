package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func notificationFixture(t *testing.T) (Dependencies, *http.ServeMux, identity.Envelope, identity.Envelope) {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	id, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1);INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('member','member',x'00','member-profile',1)`); e != nil {
		t.Fatal(e)
	}
	owner, e := id.Issue("owner", "owner-profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, e := id.Issue("member", "member-profile", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	d := Dependencies{DB: db, Identity: id, Console: operations.New(db)}
	mux := http.NewServeMux()
	d.notificationRoutes(mux)
	return d, mux, owner, member
}

func notificationRequest(h http.Handler, method, token, path, body string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, reader)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func raiseNotice(t *testing.T, d Dependencies, scope, key, title string) notify.Result {
	t.Helper()
	tx, e := d.DB.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	out, e := notify.Raise(tx, time.Now().UnixMilli(), notify.Draft{
		Audience: notify.AudienceProfile, Scope: scope, Severity: notify.SeverityInfo,
		Source: notify.SourceDownloads, Category: "download.finished", DedupeKey: key,
		Title: title, Body: "A title finished downloading.",
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return out
}

// The documented surface: the inbox document, the cheap poll, and the single
// batch write, all bounded, all no-store, all requiring a session.
func TestNotificationRoutesDocumentAndBatch(t *testing.T) {
	d, mux, owner, member := notificationFixture(t)
	scope := notify.ProfileScope("local", "member", "member-profile")
	first := raiseNotice(t, d, scope, "download:one", "Download ready")
	raiseNotice(t, d, scope, "download:two", "Second download ready")

	for _, path := range []string{"/v1/notifications/inbox", "/v1/notifications/unread-count", "/v1/notifications/capabilities"} {
		if w := notificationRequest(mux, "GET", "", path, ""); w.Code != 401 {
			t.Fatal("anonymous", path, w.Code)
		}
	}
	w := notificationRequest(mux, "GET", member.AccessToken, "/v1/notifications/inbox", "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Length") == "" {
		t.Fatal("inbox", w.Code, w.Body.String())
	}
	var document struct {
		Scope struct {
			ServerID string `json:"serverId"`
			Fence    string `json:"viewerFence"`
		}
		Data operations.NoticeInbox
	}
	if e := json.Unmarshal(w.Body.Bytes(), &document); e != nil {
		t.Fatal(e, w.Body.String())
	}
	if document.Scope.ServerID != d.Identity.ID() || len(document.Scope.Fence) != 64 {
		t.Fatalf("scope: %+v", document.Scope)
	}
	if len(document.Data.Items) != 2 || document.Data.Counts.Unread != 2 || document.Data.Revision == 0 {
		t.Fatalf("inbox document: %+v", document.Data)
	}
	// One document: the page, the counts, the revision and the retention window
	// all arrive together, so an inbox needs no second request to render.
	if document.Data.RetentionDays != 180 || document.Data.State != "all" {
		t.Fatalf("inbox document is incomplete: %+v", document.Data)
	}
	if w = notificationRequest(mux, "GET", member.AccessToken, "/v1/notifications/inbox?state=archived", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("archived filter", w.Body.String())
	}
	for _, bad := range []string{"?state=nonsense", "?audience=nonsense", "?limit=0", "?limit=500", "?unknown=1"} {
		if w = notificationRequest(mux, "GET", member.AccessToken, "/v1/notifications/inbox"+bad, ""); w.Code != 400 {
			t.Fatal("validation", bad, w.Code)
		}
	}
	// A member asking for the administrator audience is refused, not filtered.
	if w = notificationRequest(mux, "GET", member.AccessToken, "/v1/notifications/inbox?audience=account-admin", ""); w.Code != 401 {
		t.Fatal("audience", w.Code, w.Body.String())
	}
	body := `{"operationId":"batch-one","expectedRevision":` + itoa(document.Data.Revision) + `,"audience":"profile","operations":[{"action":"read","ids":["` + first.ID + `"]}]}`
	w = notificationRequest(mux, "POST", member.AccessToken, "/v1/notifications/inbox/actions", body)
	if w.Code != 200 {
		t.Fatal("batch", w.Code, w.Body.String())
	}
	var applied struct{ Data operations.NoticeBatchResult }
	if e := json.Unmarshal(w.Body.Bytes(), &applied); e != nil || applied.Data.Applied != 1 || applied.Data.Counts.Unread != 1 {
		t.Fatalf("batch result: %v %+v", e, applied.Data)
	}
	// A stale fence is a 409 that names the revision to refresh to.
	w = notificationRequest(mux, "POST", member.AccessToken, "/v1/notifications/inbox/actions", body)
	if w.Code != 200 {
		t.Fatal("replay must return the recorded answer", w.Code, w.Body.String())
	}
	stale := strings.Replace(body, "batch-one", "batch-two", 1)
	w = notificationRequest(mux, "POST", member.AccessToken, "/v1/notifications/inbox/actions", stale)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "currentRevision") {
		t.Fatal("fence", w.Code, w.Body.String())
	}
	// An unknown body field is refused rather than silently ignored.
	if w = notificationRequest(mux, "POST", member.AccessToken, "/v1/notifications/inbox/actions",
		`{"operationId":"batch-three","operations":[{"action":"read","ids":["x"]}],"surprise":true}`); w.Code != 400 {
		t.Fatal("strict body", w.Code, w.Body.String())
	}
	// Owner-only routes reject a member session.
	for _, path := range []string{"/v1/admin/notifications/settings"} {
		if w = notificationRequest(mux, "GET", member.AccessToken, path, ""); w.Code != 403 {
			t.Fatal("owner boundary", path, w.Code)
		}
		if w = notificationRequest(mux, "GET", owner.AccessToken, path, ""); w.Code != 200 {
			t.Fatal("owner", path, w.Code, w.Body.String())
		}
	}
	w = notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/notifications/broadcast",
		`{"operationId":"b-1","audience":"profile","severity":"info","dedupeKey":"notice-one","title":"Scheduled restart","body":"Tonight at 02:00.","actions":[],"expiresInDays":1}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"delivered":2`) {
		t.Fatal("broadcast", w.Code, w.Body.String())
	}
}

// Dedicated notification listeners were removed; all hints use /v1/events.
func TestDedicatedNotificationListenersAreRemoved(t *testing.T) {
	_, mux, _, member := notificationFixture(t)
	for _, path := range []string{"/v1/notifications/events", "/v1/notifications/wait"} {
		if w := notificationRequest(mux, "GET", member.AccessToken, path, ""); w.Code != 404 {
			t.Fatalf("obsolete route %s: %d", path, w.Code)
		}
	}
}

// Feedback over HTTP: capabilities before the form, a submit that recognises a
// repeat, and an administrative list whose counts arrive with the page.
func TestFeedbackRoutes(t *testing.T) {
	_, mux, owner, member := notificationFixture(t)
	w := notificationRequest(mux, "GET", member.AccessToken, "/v1/feedback/capabilities", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"canSubmit":true`) {
		t.Fatal("capabilities", w.Code, w.Body.String())
	}
	submit := `{"operationId":"r-1","kind":"playback","category":"buffering","message":"Playback keeps pausing every minute.","itemId":"","playbackSessionId":"","attachDiagnostics":false}`
	w = notificationRequest(mux, "POST", member.AccessToken, "/v1/feedback/reports", submit)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatal("submit", w.Code, w.Body.String())
	}
	var submitted struct {
		Data operations.FeedbackSubmissionResult
	}
	if e := json.Unmarshal(w.Body.Bytes(), &submitted); e != nil {
		t.Fatal(e)
	}
	repeat := strings.Replace(submit, "r-1", "r-2", 1)
	w = notificationRequest(mux, "POST", member.AccessToken, "/v1/feedback/reports", repeat)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) {
		t.Fatal("duplicate", w.Code, w.Body.String())
	}
	// The admin list is owner-only and carries its status counts.
	if w = notificationRequest(mux, "GET", member.AccessToken, "/v1/admin/feedback/reports", ""); w.Code != 403 {
		t.Fatal("owner boundary", w.Code)
	}
	w = notificationRequest(mux, "GET", owner.AccessToken, "/v1/admin/feedback/reports", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"statusCounts"`) {
		t.Fatal("admin list", w.Code, w.Body.String())
	}
	transition := `{"operationId":"t-1","expectedRevision":` + itoa(submitted.Data.Report.Revision) + `,"status":"resolved","reply":"Fixed the source."}`
	w = notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/feedback/reports/"+submitted.Data.Report.ID+"/status", transition)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"resolved"`) {
		t.Fatal("transition", w.Code, w.Body.String())
	}
	// The reporter sees the thread and their own report only.
	w = notificationRequest(mux, "GET", member.AccessToken, "/v1/feedback/reports/"+submitted.Data.Report.ID, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Fixed the source.") {
		t.Fatal("reporter thread", w.Code, w.Body.String())
	}
	// And the submission notified the administrators.
	w = notificationRequest(mux, "GET", owner.AccessToken, "/v1/notifications/inbox?audience=account-admin", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "feedback.received") {
		t.Fatal("admin notice", w.Code, w.Body.String())
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	out := ""
	for v > 0 {
		out = string(rune('0'+v%10)) + out
		v /= 10
	}
	if negative {
		return "-" + out
	}
	return out
}
