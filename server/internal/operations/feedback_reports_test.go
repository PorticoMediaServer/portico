package operations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

func submission(id, message string) FeedbackSubmission {
	return FeedbackSubmission{OperationID: id, Kind: "playback", Category: "buffering", Message: message}
}

// Capabilities must answer before a client renders a form, and must say why
// submitting is unavailable rather than leaving a client to guess from a 400.
func TestFeedbackCapabilities(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	out, e := s.FeedbackCapabilitiesDocument(ctx, member, auth)
	if e != nil || !out.CanSubmit || out.Revision != FeedbackTaxonomyRevision {
		t.Fatalf("capabilities: %v %+v", e, out)
	}
	if len(out.Kinds) < 4 || out.MaxMessageLength != feedbackMaxMessage || !out.DiagnosticsSupported {
		t.Fatalf("taxonomy: %+v", out)
	}
	if out.ReporterName != "member" || out.ReporterAuthority != "local" {
		t.Fatalf("reporter identity: %+v", out)
	}
	seen := map[string]bool{}
	for _, kind := range out.Kinds {
		if seen[kind.ID] || len(kind.Categories) == 0 {
			t.Fatalf("taxonomy shape: %+v", kind)
		}
		seen[kind.ID] = true
	}
	if _, e = s.DB.Exec(`INSERT INTO restrictions(profile_id,revision,revoked) VALUES('member-profile',1,1)`); e != nil {
		t.Fatal(e)
	}
	if out, e = s.FeedbackCapabilitiesDocument(ctx, member, auth); e != nil || !out.CanSubmit {
		t.Fatalf("a local profile collided with a Hosted revocation: %v %+v", e, out)
	}
	// Portico Accounts use the same local membership authorization as direct
	// accounts. Revocation is enforced by that boundary, not the retired table.
	revoked := func(context.Context, *sql.Tx, string) error { return identity.ErrUnauthorized }
	if _, e = s.FeedbackCapabilitiesDocument(ctx, member, revoked); !errors.Is(e, identity.ErrUnauthorized) {
		t.Fatalf("a revoked membership must be refused: %v", e)
	}
}

// The same profile restating the same problem gets its existing report back,
// with duplicateOf naming it, rather than opening a second one an administrator
// then has to reconcile.
func TestFeedbackDuplicateDetection(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	first, e := s.SubmitFeedback(ctx, member, auth, submission("report-one", "Playback keeps pausing every minute."))
	if e != nil || !first.Created || first.Duplicate {
		t.Fatalf("first submit: %v %+v", e, first)
	}
	if first.Report.Kind != "playback" || first.Report.Category != "buffering" || first.Report.Status != "open" {
		t.Fatalf("report shape: %+v", first.Report)
	}
	if first.Report.Reporter.Name != "member" || !first.Report.Reporter.Self {
		t.Fatalf("reporter: %+v", first.Report.Reporter)
	}
	// Punctuation, case and spacing are normalised away, so a viewer retyping the
	// same complaint is recognised rather than counted twice.
	again, e := s.SubmitFeedback(ctx, member, auth, submission("report-two", "playback   keeps PAUSING every minute!!"))
	if e != nil || !again.Duplicate || again.Created {
		t.Fatalf("duplicate: %v %+v", e, again)
	}
	if again.Report.ID != first.Report.ID || again.DuplicateOf != first.Report.ID {
		t.Fatal("duplicate must name and return the existing report", again.Report.ID, again.DuplicateOf, first.Report.ID)
	}
	var duplicates int
	if e = s.DB.QueryRow(`SELECT duplicates FROM feedback_details WHERE report_id=?`, first.Report.ID).Scan(&duplicates); e != nil || duplicates != 1 {
		t.Fatal("duplicate count", e, duplicates)
	}
	// A different category is a different report even with the same words.
	other := submission("report-three", "Playback keeps pausing every minute.")
	other.Category = "audio"
	distinct, e := s.SubmitFeedback(ctx, member, auth, other)
	if e != nil || !distinct.Created || distinct.Report.ID == first.Report.ID {
		t.Fatalf("category must separate reports: %v %+v", e, distinct)
	}
	// Outside the window the same words open a new report again.
	if _, e = s.DB.Exec(`UPDATE console_reports SET created_ms=created_ms-?`, int64(feedbackDuplicateHours+1)*3600000); e != nil {
		t.Fatal(e)
	}
	fresh, e := s.SubmitFeedback(ctx, member, auth, submission("report-four", "Playback keeps pausing every minute."))
	if e != nil || !fresh.Created || fresh.Duplicate {
		t.Fatalf("window: %v %+v", e, fresh)
	}
	// Every submission notifies the administrators once per report.
	inbox, e := s.Notices(ctx, owner, auth, NoticeQuery{Audience: notify.AudienceAccountAdmin})
	if e != nil || len(inbox.Items) != 3 {
		t.Fatalf("one admin notice per report: %v %d", e, len(inbox.Items))
	}
	for _, item := range inbox.Items {
		if item.Category != "feedback.received" || item.Body == "" {
			t.Fatalf("notice: %+v", item)
		}
		// The report text must not travel in the inbox.
		if item.Body == fresh.Report.Message {
			t.Fatal("report text leaked into the inbox")
		}
	}
	// Replaying an operation identifier returns the recorded answer.
	replay, e := s.SubmitFeedback(ctx, member, auth, submission("report-one", "Playback keeps pausing every minute."))
	if e != nil || replay.Report.ID != first.Report.ID {
		t.Fatalf("replay: %v %+v", e, replay)
	}
}

// The prefill decision is recorded in all four of its states, so a reviewer can
// tell a refused attachment from a failed one.
func TestFeedbackDiagnosticsDecisions(t *testing.T) {
	s, _, member, auth := noticeFixture(t)
	ctx := context.Background()
	if _, e := s.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('library','Library','movie','/library')`); e != nil {
		t.Fatal(e)
	}
	itemPublic, itemID := seedOperationItem(t, s.DB, "library", "feedback-item", "Title")
	assetToken, _ := seedOperationAsset(t, s.DB, "/library/a.mkv")
	if _, e := s.DB.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration)
 VALUES('session','hash','member','member-profile',?, ?,1,'playing','grant','token','2027-01-01T00:00:00Z','request',60)`, itemID, assetToken); e != nil {
		t.Fatal(e)
	}
	plain, e := s.SubmitFeedback(ctx, member, auth, submission("d-none", "Nothing attached to this one at all."))
	if e != nil || plain.Report.Diagnostics.Decision != "not-requested" {
		t.Fatalf("no prefill: %v %+v", e, plain.Report.Diagnostics)
	}
	declined := submission("d-declined", "This one names a session but declines.")
	declined.PlaybackSessionID, declined.AttachDiagnostics = "session", false
	out, e := s.SubmitFeedback(ctx, member, auth, declined)
	if e != nil || out.Report.Diagnostics.Decision != "declined" || out.Report.Diagnostics.Reference != "" {
		t.Fatalf("declined: %v %+v", e, out.Report.Diagnostics)
	}
	attached := submission("d-attached", "This one attaches the playback session.")
	attached.PlaybackSessionID, attached.AttachDiagnostics = "session", true
	out, e = s.SubmitFeedback(ctx, member, auth, attached)
	if e != nil || out.Report.Diagnostics.Decision != "attached" || out.Report.Diagnostics.Reference != "playback-session:session" {
		t.Fatalf("attached: %v %+v", e, out.Report.Diagnostics)
	}
	// The prefill also names the title, so the reporter did not have to.
	if out.Report.ItemID != itemPublic {
		t.Fatalf("prefill did not resolve the item: %+v", out.Report)
	}
	missing := submission("d-missing", "This one names a session the server dropped.")
	missing.PlaybackSessionID, missing.AttachDiagnostics = "vanished", true
	out, e = s.SubmitFeedback(ctx, member, auth, missing)
	if e != nil || out.Report.Diagnostics.Decision != "unavailable" || out.Report.Diagnostics.Detail == "" {
		t.Fatalf("unavailable: %v %+v", e, out.Report.Diagnostics)
	}
	// A session that belongs to someone else resolves to unavailable, never to
	// their diagnostics.
	if _, e = s.DB.Exec(`UPDATE playback_sessions SET account_id='owner',profile_id='owner-profile' WHERE id='session'`); e != nil {
		t.Fatal(e)
	}
	stolen := submission("d-stolen", "This one points at another viewer's session.")
	stolen.PlaybackSessionID, stolen.AttachDiagnostics = "session", true
	out, e = s.SubmitFeedback(ctx, member, auth, stolen)
	if e != nil || out.Report.Diagnostics.Decision != "unavailable" {
		t.Fatalf("cross-viewer session: %v %+v", e, out.Report.Diagnostics)
	}
}

// Administrative triage: filters, status counts, a revision fence, the
// reporter-visible thread, and a notice back to the reporter.
func TestFeedbackTriageListAndTransitions(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	first, e := s.SubmitFeedback(ctx, member, auth, submission("t-one", "Buffering all the time on this one."))
	if e != nil {
		t.Fatal(e)
	}
	library := submission("t-two", "The poster for this film is wrong.")
	library.Kind, library.Category = "library", "wrong-artwork"
	second, e := s.SubmitFeedback(ctx, member, auth, library)
	if e != nil {
		t.Fatal(e)
	}
	list, e := s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{})
	if e != nil || len(list.Items) != 2 || list.StatusCounts["open"] != 2 || list.Total != 2 {
		t.Fatalf("admin list: %v %+v", e, list)
	}
	filtered, e := s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{Kind: "library"})
	if e != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != second.Report.ID {
		t.Fatalf("kind filter: %v %+v", e, filtered)
	}
	if byReporter, e := s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{Reporter: "mem"}); e != nil || len(byReporter.Items) != 2 {
		t.Fatalf("reporter filter: %v %+v", e, byReporter)
	}
	if none, e := s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{Reporter: "nobody"}); e != nil || len(none.Items) != 0 {
		t.Fatalf("reporter filter must not match everything: %v %+v", e, none)
	}
	if _, e = s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{Status: "nonsense"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown status must be a 400", e)
	}
	moved, e := s.TransitionFeedback(ctx, owner, auth, first.Report.ID, FeedbackTransition{
		OperationID: "t-move", ExpectedRevision: first.Report.Revision, Status: "in-progress", Reply: "Looking at the source now."})
	if e != nil || moved.Status != "in-progress" || len(moved.Thread) != 2 {
		t.Fatalf("transition: %v %+v", e, moved)
	}
	if moved.Thread[1].Reply != "Looking at the source now." || moved.Thread[1].ActorClass != "admin" {
		t.Fatalf("thread: %+v", moved.Thread)
	}
	_, e = s.TransitionFeedback(ctx, owner, auth, first.Report.ID, FeedbackTransition{
		OperationID: "t-stale", ExpectedRevision: first.Report.Revision, Status: "resolved"})
	var conflict *ConflictError
	if !errors.As(e, &conflict) || conflict.CurrentRevision != moved.Revision {
		t.Fatalf("revision fence: %v", e)
	}
	replay, e := s.TransitionFeedback(ctx, owner, auth, first.Report.ID, FeedbackTransition{
		OperationID: "t-move", ExpectedRevision: first.Report.Revision, Status: "in-progress", Reply: "Looking at the source now."})
	if e != nil || replay.Revision != moved.Revision {
		t.Fatalf("transition replay: %v %+v", e, replay)
	}
	counts, e := s.FeedbackReports(ctx, owner, auth, true, FeedbackFilter{Status: "open"})
	if e != nil || len(counts.Items) != 1 || counts.StatusCounts["in-progress"] != 1 || counts.StatusCounts["open"] != 1 {
		t.Fatalf("counts must describe every status under the same other filters: %v %+v", e, counts)
	}
	// The reporter sees only their own reports and is told their report moved.
	mine, e := s.FeedbackReports(ctx, member, auth, false, FeedbackFilter{})
	if e != nil || len(mine.Items) != 2 {
		t.Fatalf("reporter list: %v %+v", e, mine)
	}
	thread, e := s.FeedbackReportDocument(ctx, member, auth, false, first.Report.ID)
	if e != nil || len(thread.Thread) != 2 || thread.Status != "in-progress" {
		t.Fatalf("reporter thread: %v %+v", e, thread)
	}
	inbox, e := s.Notices(ctx, member, auth, NoticeQuery{Audience: notify.AudienceProfile})
	if e != nil || len(inbox.Items) != 1 || inbox.Items[0].Category != "feedback.updated" {
		t.Fatalf("reporter notice: %v %+v", e, inbox)
	}
	if _, e = s.FeedbackReportDocument(ctx, member, auth, false, second.Report.ID); e != nil {
		t.Fatal(e)
	}
}

func TestFeedbackKeepsTheSendingAppForTriage(t *testing.T) {
	s, owner, member, auth := noticeFixture(t)
	ctx := context.Background()
	online := true
	sent := submission("client-one", "Buffering all the time on this one.")
	sent.Client = &FeedbackClient{Platform: "android", AppVersion: "1.4.2", Online: &online}
	result, e := s.SubmitFeedback(ctx, member, auth, sent)
	if e != nil || result.Report.Client == nil || *result.Report.Client.Online != true || result.Report.Client.Platform != "android" || result.Report.Client.AppVersion != "1.4.2" {
		t.Fatalf("submitted client: %v %+v", e, result.Report.Client)
	}
	read, e := s.FeedbackReportDocument(ctx, owner, auth, true, result.Report.ID)
	if e != nil || read.Client == nil || read.Client.Platform != "android" || read.Client.AppVersion != "1.4.2" || read.Client.Online == nil || !*read.Client.Online {
		t.Fatalf("triage read: %v %+v", e, read.Client)
	}
	unknown := submission("client-two", "The subtitles drift after the first hour.")
	unknown.Client = &FeedbackClient{Platform: "web", AppVersion: "2026.10"}
	if r, e := s.SubmitFeedback(ctx, member, auth, unknown); e != nil || r.Report.Client == nil || r.Report.Client.Online != nil {
		t.Fatalf("an unknown online state stays unknown: %v %+v", e, r.Report.Client)
	}
	for _, bad := range []FeedbackClient{{Platform: "", AppVersion: "1"}, {Platform: "web", AppVersion: ""}, {Platform: "https://x", AppVersion: "1"},
		{Platform: "web", AppVersion: "1\n2"}, {Platform: "web", AppVersion: strings.Repeat("9", 41)}} {
		bad := bad
		c := submission("client-bad-"+bad.Platform+bad.AppVersion, "Something about this one is wrong.")
		c.Client = &bad
		if _, e := s.SubmitFeedback(ctx, member, auth, c); !errors.Is(e, ErrInvalid) {
			t.Fatalf("client %+v must be refused: %v", bad, e)
		}
	}
	none, e := s.SubmitFeedback(ctx, member, auth, submission("client-none", "The artwork for this one is wrong."))
	if e != nil || none.Report.Client != nil {
		t.Fatalf("no client: %v %+v", e, none.Report.Client)
	}
}
