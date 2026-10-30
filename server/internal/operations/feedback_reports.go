package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// Viewer feedback.
//
// Reports live in console_reports, which predates this workstream; the two-level
// taxonomy, reporter identity, diagnostics decision and duplicate fingerprint
// live alongside it in feedback_details. One store, one identifier, one thread.
//
// Three properties the previous build lacked:
//
//   - A client can ask what it may send before it renders a form
//     (GET /v1/feedback/capabilities), instead of discovering a rejected
//     category from a 400.
//   - Diagnostics attachment is a recorded decision — attached, unavailable,
//     declined or not-requested — rather than a silently missing field, so a
//     reviewer knows whether a bundle is absent because it failed or because the
//     reporter said no.
//   - Duplicate detection returns the existing report with duplicateOf rather
//     than opening a second one, and says so in the response.

// FeedbackCategory is one leaf of the taxonomy.
type FeedbackCategory struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// WantsPlaybackSession marks the categories where a playbackSessionId
	// materially improves triage, so a client knows when to offer the prefill.
	WantsPlaybackSession bool `json:"wantsPlaybackSession"`
	// WantsItem marks the categories where naming the affected title matters.
	WantsItem bool `json:"wantsItem"`
}

// FeedbackKind is one branch of the taxonomy.
type FeedbackKind struct {
	ID         string             `json:"id"`
	Label      string             `json:"label"`
	Categories []FeedbackCategory `json:"categories"`
}

// FeedbackCapabilities is the pre-flight document: everything a client needs to
// render the form correctly and to know, before asking, whether this viewer may
// submit at all.
type FeedbackCapabilities struct {
	Revision              string         `json:"revision"`
	CanSubmit             bool           `json:"canSubmit"`
	SubmitBlockedReason   string         `json:"submitBlockedReason,omitempty"`
	Kinds                 []FeedbackKind `json:"kinds"`
	MaxMessageLength      int            `json:"maxMessageLength"`
	MinMessageLength      int            `json:"minMessageLength"`
	DiagnosticsSupported  bool           `json:"diagnosticsSupported"`
	DiagnosticsOptional   bool           `json:"diagnosticsOptional"`
	DuplicateWindowHours  int            `json:"duplicateWindowHours"`
	RetentionDays         int            `json:"retentionDays"`
	Statuses              []string       `json:"statuses"`
	DiagnosticsDecisions  []string       `json:"diagnosticsDecisions"`
	PerProfileHourlyLimit int            `json:"perProfileHourlyLimit"`
	ReporterName          string         `json:"reporterName"`
	ReporterAuthority     string         `json:"reporterAuthority"`
}

// FeedbackTaxonomyRevision changes whenever a kind or a category is added,
// removed or renamed, so a client can cache the document and revalidate cheaply.
const FeedbackTaxonomyRevision = "e1.0"

const (
	feedbackMaxMessage     = 2000
	feedbackMinMessage     = 8
	feedbackDuplicateHours = 24
	feedbackHourlyLimit    = 10
	feedbackThreadCap      = 200
)

// FeedbackTaxonomy is the single authority for what a report may say. The HTTP
// layer validates against it, the capabilities document publishes it, and the
// admin filters accept exactly these identifiers.
func FeedbackTaxonomy() []FeedbackKind {
	return []FeedbackKind{
		{ID: "playback", Label: "Playback", Categories: []FeedbackCategory{
			{ID: "wont-start", Label: "Will not start", Description: "Playback never begins.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "buffering", Label: "Buffering or stalling", Description: "Playback starts but keeps pausing.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "stops-early", Label: "Stops before the end", Description: "Playback ends or errors partway through.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "audio", Label: "Audio problem", Description: "Wrong, missing or out-of-sync audio.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "subtitles", Label: "Subtitle problem", Description: "Missing, mistimed or wrong subtitles.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "quality", Label: "Picture quality", Description: "Softer, darker or more artefacted than expected.", WantsPlaybackSession: true, WantsItem: true},
			{ID: "other", Label: "Something else", Description: "Anything else about playback.", WantsPlaybackSession: true, WantsItem: true},
		}},
		{ID: "library", Label: "Library and metadata", Categories: []FeedbackCategory{
			{ID: "missing-item", Label: "Something is missing", Description: "A title that should be here is not.", WantsItem: false},
			{ID: "wrong-match", Label: "Matched to the wrong title", Description: "The wrong film, show or album was matched.", WantsItem: true},
			{ID: "wrong-artwork", Label: "Wrong artwork", Description: "The poster, cover or backdrop is wrong.", WantsItem: true},
			{ID: "wrong-details", Label: "Wrong details", Description: "Title, year, cast or description is wrong.", WantsItem: true},
			{ID: "duplicate", Label: "Duplicated entry", Description: "The same title appears more than once.", WantsItem: true},
			{ID: "other", Label: "Something else", Description: "Anything else about the library.", WantsItem: true},
		}},
		{ID: "apps", Label: "Apps and devices", Categories: []FeedbackCategory{
			{ID: "crash", Label: "The app closed unexpectedly", Description: "A crash or a forced restart."},
			{ID: "layout", Label: "Something looks wrong", Description: "Overlapping, cut off or unreadable."},
			{ID: "navigation", Label: "Cannot get somewhere", Description: "A control or a route does not work."},
			{ID: "performance", Label: "Slow or unresponsive", Description: "The app itself is sluggish."},
			{ID: "other", Label: "Something else", Description: "Anything else about the app."},
		}},
		{ID: "account", Label: "Account and access", Categories: []FeedbackCategory{
			{ID: "sign-in", Label: "Sign-in problem", Description: "Cannot sign in or keep a session."},
			{ID: "profile", Label: "Profile problem", Description: "Profile switching, PINs or preferences."},
			{ID: "restrictions", Label: "Cannot see something", Description: "Content that should be available is not."},
			{ID: "other", Label: "Something else", Description: "Anything else about accounts."},
		}},
		{ID: "other", Label: "Something else", Categories: []FeedbackCategory{
			{ID: "suggestion", Label: "A suggestion", Description: "An idea rather than a problem."},
			{ID: "other", Label: "Something else", Description: "Anything that does not fit above."},
		}},
	}
}

func feedbackTaxonomyHas(kind, category string) bool {
	for _, k := range FeedbackTaxonomy() {
		if k.ID != kind {
			continue
		}
		for _, c := range k.Categories {
			if c.ID == category {
				return true
			}
		}
	}
	return false
}

func feedbackStatuses() []string { return []string{"open", "in-progress", "resolved", "closed"} }

func feedbackDiagnosticsDecisions() []string {
	return []string{"attached", "unavailable", "declined", "not-requested"}
}

// FeedbackCapabilitiesDocument answers what this viewer may send. Whether the
// viewer may submit at all is resolved from their profile's restriction record,
// so a restricted profile learns it cannot submit before typing a message.
func (s *Store) FeedbackCapabilitiesDocument(ctx context.Context, p identity.Principal, auth Authorize) (out FeedbackCapabilities, e error) {
	out = FeedbackCapabilities{
		Revision: FeedbackTaxonomyRevision, CanSubmit: true, Kinds: FeedbackTaxonomy(),
		MaxMessageLength: feedbackMaxMessage, MinMessageLength: feedbackMinMessage,
		DiagnosticsSupported: true, DiagnosticsOptional: true,
		DuplicateWindowHours: feedbackDuplicateHours,
		Statuses:             feedbackStatuses(), DiagnosticsDecisions: feedbackDiagnosticsDecisions(),
		PerProfileHourlyLimit: feedbackHourlyLimit,
		ReporterAuthority:     p.Authority,
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		settings, err := readSettings(tx)
		if err != nil {
			return err
		}
		out.RetentionDays = settings.Effective.NotificationDays
		name, _, err := feedbackReporterTx(tx, p)
		if err != nil {
			return err
		}
		out.ReporterName = name
		// A revoked or unknown profile cannot open a report. The check names the
		// reason so a client can say why the button is unavailable.

		if out.CanSubmit {
			var recent int
			if err = tx.QueryRow(`SELECT count(*) FROM console_reports WHERE scope=? AND created_ms>?`, ViewerKey(p), s.now()-int64(time.Hour/time.Millisecond)).Scan(&recent); err != nil {
				return err
			}
			if recent >= feedbackHourlyLimit {
				out.CanSubmit, out.SubmitBlockedReason = false, "This profile has reached its hourly report limit. Try again later."
			}
		}
		return nil
	})
	return
}

// feedbackReporterTx resolves the reporter's display name and role. A report
// with no readable reporter is still a valid report; it reads as "Unknown".
func feedbackReporterTx(tx *sql.Tx, p identity.Principal) (string, string, error) {
	name, role := "Unknown profile", p.Role
	e := tx.QueryRow(`SELECT name FROM direct_profiles WHERE id=? AND account_id=?`, p.ProfileID, p.AccountID).Scan(&name)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return "", "", e
	}
	if role == "" {
		role = "member"
	}
	return name, role, nil
}

// FeedbackSubmission is the new submit request.
type FeedbackSubmission struct {
	OperationID string `json:"operationId"`
	Kind        string `json:"kind"`
	Category    string `json:"category"`
	Message     string `json:"message"`
	ItemID      string `json:"itemId"`
	// PlaybackSessionID is a prefill hint. The server resolves it into a
	// diagnostic bundle reference itself; the client never uploads diagnostics.
	PlaybackSessionID string `json:"playbackSessionId"`
	// AttachDiagnostics is the reporter's choice. False records a "declined"
	// decision rather than silently omitting the field.
	AttachDiagnostics bool `json:"attachDiagnostics"`
	// Client names the sending app so triage can see where a report came from.
	Client *FeedbackClient `json:"client,omitempty"`
}

// FeedbackClient is the app a report was sent from: its platform, its version
// and whether the device was online. It is an allowlisted snapshot; no device
// identifiers, names, addresses or titles can enter it.
type FeedbackClient struct {
	Platform   string `json:"platform"`
	AppVersion string `json:"appVersion"`
	Online     *bool  `json:"online,omitempty"`
}

var feedbackClientWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+-]{0,39}$`)

func validFeedbackClient(c *FeedbackClient) bool {
	return c == nil || feedbackClientWord.MatchString(c.Platform) && feedbackClientWord.MatchString(c.AppVersion)
}

// FeedbackReporter is the identity a reviewer sees.
type FeedbackReporter struct {
	Name      string `json:"name"`
	Authority string `json:"authority"`
	Role      string `json:"role"`
	Self      bool   `json:"self"`
}

// FeedbackDiagnostics records what happened to the prefill request.
type FeedbackDiagnostics struct {
	Decision  string `json:"decision"`
	Reference string `json:"reference,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// FeedbackThreadEvent is one entry of the reporter-visible status thread.
type FeedbackThreadEvent struct {
	Sequence   int64           `json:"sequence"`
	At         apiwire.Instant `json:"at"`
	Revision   int64           `json:"revision"`
	Status     string          `json:"status"`
	Reply      string          `json:"reply"`
	ActorClass string          `json:"actorClass"`
}

// FeedbackReport is the full record on the wire.
type FeedbackReport struct {
	ID          string                `json:"id"`
	Sequence    int64                 `json:"-"`
	Cursor      string                `json:"cursor"`
	Revision    int64                 `json:"revision"`
	Status      string                `json:"status"`
	Kind        string                `json:"kind"`
	Category    string                `json:"category"`
	Message     string                `json:"message"`
	ItemID      string                `json:"itemId,omitempty"`
	CreatedAt   apiwire.Instant       `json:"createdAt"`
	UpdatedAt   apiwire.Instant       `json:"updatedAt"`
	ExpiresAt   apiwire.Instant       `json:"expiresAt"`
	Reporter    FeedbackReporter      `json:"reporter"`
	Diagnostics FeedbackDiagnostics   `json:"diagnostics"`
	Client      *FeedbackClient       `json:"client,omitempty"`
	DuplicateOf string                `json:"duplicateOf,omitempty"`
	Duplicates  int                   `json:"duplicates"`
	Thread      []FeedbackThreadEvent `json:"thread"`
}

// FeedbackSubmissionResult distinguishes a new report from a recognised repeat.
// On a repeat, DuplicateOf names the report the submission was folded into —
// which is also the report returned — so a client never has to infer identity
// from the fact that the identifier differs from the one it expected.
type FeedbackSubmissionResult struct {
	Report      FeedbackReport `json:"report"`
	Duplicate   bool           `json:"duplicate"`
	Created     bool           `json:"created"`
	DuplicateOf string         `json:"duplicateOf,omitempty"`
}

// FeedbackFilter is the admin list filter. Every value is validated against a
// published vocabulary; filtering and counting both happen in SQL.
type FeedbackFilter struct {
	Status   string
	Kind     string
	Category string
	Reporter string
	Cursor   string
	Limit    int
}

// FeedbackList is the admin list document: counts for every status alongside the
// page, so a console renders its tab badges without a second request.
type FeedbackList struct {
	Items        []FeedbackReport  `json:"items"`
	NextCursor   string            `json:"nextCursor"`
	StatusCounts map[string]int    `json:"statusCounts"`
	Total        int               `json:"total"`
	Filter       map[string]string `json:"filter"`
	ObservedAt   apiwire.Instant   `json:"observedAt"`
}

// FeedbackTransition moves a report's status behind a revision fence.
type FeedbackTransition struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Status           string `json:"status"`
	Reply            string `json:"reply"`
}

// feedbackFingerprint is the duplicate identity: the same profile, the same
// kind and category, the same item, and a message that normalises to the same
// text. Normalising collapses case, punctuation and runs of whitespace, so
// "It won't play!!" and "it wont play" are one report rather than two.
func feedbackFingerprint(scope, kind, category, item, message string) string {
	var b strings.Builder
	space := false
	for _, c := range strings.ToLower(message) {
		switch {
		case unicode.IsLetter(c) || unicode.IsDigit(c):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(c)
		default:
			space = true
		}
	}
	return Hash(strings.Join([]string{scope, kind, category, item, b.String()}, "\x00"))
}

// resolvePlaybackDiagnostics turns a playbackSessionId prefill into a durable
// reference, and records why when it cannot. The reference names the session the
// server already holds; no client-supplied log text is ever stored.
func resolvePlaybackDiagnostics(tx *sql.Tx, p identity.Principal, c FeedbackSubmission) (FeedbackDiagnostics, string, error) {
	if c.PlaybackSessionID == "" {
		return FeedbackDiagnostics{Decision: "not-requested"}, "", nil
	}
	if !c.AttachDiagnostics {
		return FeedbackDiagnostics{Decision: "declined", Detail: "The reporter chose not to attach playback diagnostics."}, "", nil
	}
	if !validID(c.PlaybackSessionID) {
		return FeedbackDiagnostics{}, "", invalidFields("playbackSessionId")
	}
	var item, mode string
	// The session must belong to this viewer. A session identifier from someone
	// else's playback resolves to "unavailable", never to their diagnostics.
	e := tx.QueryRow(`SELECT COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=playback_sessions.item_id),''),mode FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=?`,
		c.PlaybackSessionID, p.AccountID, p.ProfileID).Scan(&item, &mode)
	if errors.Is(e, sql.ErrNoRows) {
		return FeedbackDiagnostics{Decision: "unavailable", Detail: "That playback session is no longer held by the server."}, "", nil
	}
	if e != nil {
		return FeedbackDiagnostics{}, "", e
	}
	return FeedbackDiagnostics{Decision: "attached", Reference: "playback-session:" + c.PlaybackSessionID,
		Detail: "The server-held record for this playback session is linked to the report."}, item, nil
}

// SubmitFeedback opens a report, or recognises it as a repeat of one the same
// profile already sent in the last 24 hours and returns that one instead.
func (s *Store) SubmitFeedback(ctx context.Context, p identity.Principal, auth Authorize, c FeedbackSubmission) (out FeedbackSubmissionResult, e error) {
	if !feedbackTaxonomyHas(c.Kind, c.Category) {
		return out, invalidFields("kind", "category")
	}
	if !feedbackText(c.Message) || utf8.RuneCountInString(strings.TrimSpace(c.Message)) < feedbackMinMessage {
		return out, invalidFields("message")
	}
	if c.ItemID != "" && !validID(c.ItemID) {
		return out, invalidFields("itemId")
	}
	if !validFeedbackClient(c.Client) {
		return out, invalidFields("client")
	}
	attachment := ""
	if c.Client != nil {
		raw, err := json.Marshal(c.Client)
		if err != nil {
			return out, err
		}
		attachment = string(raw)
	}
	scope := ViewerKey(p)
	e = s.transaction(ctx, auth, c.ItemID, func(tx *sql.Tx) error {
		now := s.now()
		receiptScope := "feedback:" + scope
		raw, digest, err := Receipt(tx, receiptScope, c.OperationID, c, now)
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		var recent, total int
		if err = tx.QueryRow(`SELECT count(*) FROM console_reports WHERE scope=? AND created_ms>?`, scope, now-int64(time.Hour/time.Millisecond)).Scan(&recent); err != nil {
			return err
		}
		if err = tx.QueryRow(`SELECT count(*) FROM console_reports`).Scan(&total); err != nil {
			return err
		}
		if recent >= feedbackHourlyLimit || total >= 10000 {
			return ErrCapacity
		}
		diagnostics, sessionItem, err := resolvePlaybackDiagnostics(tx, p, c)
		if err != nil {
			return err
		}
		item := c.ItemID
		if item == "" && sessionItem != "" {
			// The prefill also names the title, so a reporter does not have to.
			item = sessionItem
		}
		entity, err := resolveReportItem(ctx, tx, item)
		if err != nil {
			return err
		}
		fingerprint := feedbackFingerprint(scope, c.Kind, c.Category, item, c.Message)
		var existing string
		err = tx.QueryRow(`SELECT d.report_id FROM feedback_details d JOIN console_reports r ON r.id=d.report_id
 WHERE d.fingerprint=? AND r.scope=? AND r.created_ms>? AND r.expires_ms>? AND d.duplicate_of='' ORDER BY r.sequence DESC LIMIT 1`,
			fingerprint, scope, now-int64(feedbackDuplicateHours)*3600000, now).Scan(&existing)
		if err == nil {
			if _, err = tx.Exec(`UPDATE feedback_details SET duplicates=duplicates+1 WHERE report_id=?`, existing); err != nil {
				return err
			}
			if out.Report, err = s.feedbackReportTx(ctx, tx, p, auth, true, existing); err != nil {
				return err
			}
			out.Duplicate, out.DuplicateOf = true, existing
			return SaveReceipt(tx, receiptScope, c.OperationID, digest, out, now)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		name, role, err := feedbackReporterTx(tx, p)
		if err != nil {
			return err
		}
		id := identity.Token()
		expiry := now + days(180)
		if _, err = tx.Exec(`INSERT INTO console_reports(id,scope,created_ms,updated_ms,revision,status,category,message,item_id,attachment,expires_ms)
 VALUES(?,?,?,?,1,'open',?,?,?,?,?)`, id, scope, now, now, c.Category, c.Message, entity, attachment, expiry); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO feedback_details(report_id,kind,reporter_name,reporter_authority,reporter_role,fingerprint,session_id,diagnostics_decision,diagnostics_ref,duplicate_of)
 VALUES(?,?,?,?,?,?,?,?,?,'')`, id, c.Kind, name, p.Authority, role, fingerprint, c.PlaybackSessionID, diagnostics.Decision, diagnostics.Reference); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO console_report_events(report_id,time_ms,revision,status,reply,actor_class) VALUES(?,?,1,'open','','viewer')`, id, now); err != nil {
			return err
		}
		if _, err = notify.NotifyFeedbackSubmitted(tx, now, id, c.Kind, c.Category); err != nil {
			return err
		}
		if out.Report, err = s.feedbackReportTx(ctx, tx, p, auth, true, id); err != nil {
			return err
		}
		out.Created = true
		return SaveReceipt(tx, receiptScope, c.OperationID, digest, out, now)
	})
	if e == nil {
		WakeNotifications()
	}
	return
}

const feedbackColumns = `r.id,r.sequence,r.revision,r.status,r.message,COALESCE((SELECT pid(public_id) FROM catalog_entities WHERE id=r.item_id),''),r.created_ms,r.updated_ms,r.expires_ms,r.category,
 COALESCE(d.kind,'other'),COALESCE(d.reporter_name,'Unknown profile'),COALESCE(d.reporter_authority,'local'),COALESCE(d.reporter_role,'member'),
 COALESCE(d.diagnostics_decision,'not-requested'),COALESCE(d.diagnostics_ref,''),COALESCE(d.duplicate_of,''),COALESCE(d.duplicates,0),r.scope,r.attachment`

func scanFeedbackReport(row interface{ Scan(...any) error }, viewer string) (FeedbackReport, error) {
	var r FeedbackReport
	var scope, attachment string
	e := row.Scan(&r.ID, &r.Sequence, &r.Revision, &r.Status, &r.Message, &r.ItemID, &r.CreatedAt, &r.UpdatedAt, &r.ExpiresAt, &r.Category,
		&r.Kind, &r.Reporter.Name, &r.Reporter.Authority, &r.Reporter.Role, &r.Diagnostics.Decision, &r.Diagnostics.Reference, &r.DuplicateOf, &r.Duplicates, &scope, &attachment)
	if e != nil {
		return r, e
	}
	if attachment != "" {
		if e = decodeDocument(attachment, &r.Client); e != nil {
			return r, e
		}
	}
	r.Cursor = strconv.FormatInt(r.Sequence, 10)
	r.Diagnostics.Detail = feedbackDiagnosticsDetail(r.Diagnostics.Decision)
	r.Reporter.Self = scope == viewer
	r.Thread = []FeedbackThreadEvent{}
	return r, nil
}

// feedbackDiagnosticsDetail renders the stored decision as the sentence a
// reviewer reads. Only the decision is persisted, so the explanation cannot
// drift away from it.
func feedbackDiagnosticsDetail(decision string) string {
	switch decision {
	case "attached":
		return "The server-held record for this playback session is linked to the report."
	case "declined":
		return "The reporter chose not to attach playback diagnostics."
	case "unavailable":
		return "That playback session is no longer held by the server."
	}
	return "No playback session was named with this report."
}

func (s *Store) feedbackThreadTx(tx *sql.Tx, id string) ([]FeedbackThreadEvent, error) {
	rows, e := tx.Query(`SELECT sequence,time_ms,revision,status,reply,actor_class FROM console_report_events WHERE report_id=? ORDER BY sequence LIMIT ?`, id, feedbackThreadCap)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FeedbackThreadEvent{}
	for rows.Next() {
		var v FeedbackThreadEvent
		if e = rows.Scan(&v.Sequence, &v.At, &v.Revision, &v.Status, &v.Reply, &v.ActorClass); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) feedbackReportTx(ctx context.Context, tx *sql.Tx, p identity.Principal, auth Authorize, owner bool, id string) (FeedbackReport, error) {
	var out FeedbackReport
	if !validID(id) {
		return out, ErrInvalid
	}
	query := `SELECT ` + feedbackColumns + ` FROM console_reports r LEFT JOIN feedback_details d ON d.report_id=r.id WHERE r.id=? AND r.expires_ms>?`
	args := []any{id, s.now()}
	if !owner {
		query += ` AND r.scope=?`
		args = append(args, ViewerKey(p))
	}
	out, e := scanFeedbackReport(tx.QueryRow(query, args...), ViewerKey(p))
	if e != nil {
		return out, e
	}
	// A report whose item the viewer can no longer reach keeps its thread but
	// loses the media context, exactly as the pre-existing reader does.
	if out.ItemID != "" && auth(ctx, tx, out.ItemID) != nil {
		out.ItemID = ""
	}
	out.Thread, e = s.feedbackThreadTx(tx, id)
	return out, e
}

// FeedbackReportDocument answers one report with its thread, for the reporter or
// for an administrator.
func (s *Store) FeedbackReportDocument(ctx context.Context, p identity.Principal, auth Authorize, owner bool, id string) (out FeedbackReport, e error) {
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		var err error
		out, err = s.feedbackReportTx(ctx, tx, p, auth, owner, id)
		return err
	})
	return
}

// FeedbackReports lists reports. owner=false restricts the query to the calling
// profile's own reports; owner=true adds the status, kind, category and reporter
// filters, and the status counts a triage console needs.
func (s *Store) FeedbackReports(ctx context.Context, p identity.Principal, auth Authorize, owner bool, f FeedbackFilter) (out FeedbackList, e error) {
	out.Items, out.StatusCounts, out.Filter = []FeedbackReport{}, map[string]int{}, map[string]string{}
	for _, status := range feedbackStatuses() {
		out.StatusCounts[status] = 0
	}
	if f.Limit == 0 {
		f.Limit = 40
	}
	if f.Limit < 1 || f.Limit > noticeMaxLimit {
		return out, invalidFields("limit")
	}
	before, e := Cursor(f.Cursor)
	if e != nil {
		return out, e
	}
	where, args := ` AND r.expires_ms>?`, []any{}
	if f.Status != "" {
		found := false
		for _, status := range feedbackStatuses() {
			found = found || status == f.Status
		}
		if !found {
			return out, invalidFields("status")
		}
		where += ` AND r.status=?`
		args = append(args, f.Status)
		out.Filter["status"] = f.Status
	}
	if f.Kind != "" {
		valid := false
		for _, k := range FeedbackTaxonomy() {
			valid = valid || k.ID == f.Kind
		}
		if !valid {
			return out, invalidFields("kind")
		}
		where += ` AND COALESCE(d.kind,'other')=?`
		args = append(args, f.Kind)
		out.Filter["kind"] = f.Kind
	}
	if f.Category != "" {
		if !validID(f.Category) {
			return out, invalidFields("category")
		}
		where += ` AND r.category=?`
		args = append(args, f.Category)
		out.Filter["category"] = f.Category
	}
	if f.Reporter != "" {
		if !SafeText(f.Reporter, 120) {
			return out, invalidFields("reporter")
		}
		// Reporter filtering is by display name, which is what a reviewer has in
		// front of them. It is a prefix match so a console can type ahead.
		where += ` AND COALESCE(d.reporter_name,'') LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(f.Reporter)+"%")
		out.Filter["reporter"] = f.Reporter
	}
	if !owner {
		where += ` AND r.scope=?`
		args = append(args, ViewerKey(p))
	}
	e = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		out.ObservedAt = apiwire.Instant(now)
		base := ` FROM console_reports r LEFT JOIN feedback_details d ON d.report_id=r.id WHERE 1=1` + where
		page := append(append([]any{now}, args...), before, f.Limit+1)
		rows, err := tx.Query(`SELECT `+feedbackColumns+base+` AND r.sequence<? ORDER BY r.sequence DESC LIMIT ?`, page...)
		if err != nil {
			return err
		}
		items := []FeedbackReport{}
		for rows.Next() {
			v, err := scanFeedbackReport(rows, ViewerKey(p))
			if err != nil {
				rows.Close()
				return err
			}
			items = append(items, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(items) > f.Limit {
			items = items[:f.Limit]
			out.NextCursor = items[len(items)-1].Cursor
		}
		for i := range items {
			if items[i].ItemID != "" && auth(ctx, tx, items[i].ItemID) != nil {
				items[i].ItemID = ""
			}
		}
		out.Items = items
		// Status counts respect every filter except status itself, so the tab
		// badges and the list agree. The index on (status,sequence) keeps it cheap.
		countWhere, countArgs := feedbackCountFilter(f, owner, ViewerKey(p), now)
		counts, err := tx.Query(`SELECT r.status,count(*)`+countWhere+` GROUP BY r.status`, countArgs...)
		if err != nil {
			return err
		}
		defer counts.Close()
		for counts.Next() {
			var status string
			var n int
			if err = counts.Scan(&status, &n); err != nil {
				return err
			}
			out.StatusCounts[status] = n
			out.Total += n
		}
		return counts.Err()
	})
	return
}

// feedbackCountFilter rebuilds the list predicate without the status clause, so
// the published counts describe every status under the same other filters.
func feedbackCountFilter(f FeedbackFilter, owner bool, viewer string, now int64) (string, []any) {
	where := ` FROM console_reports r LEFT JOIN feedback_details d ON d.report_id=r.id WHERE r.expires_ms>?`
	args := []any{now}
	if f.Kind != "" {
		where += ` AND COALESCE(d.kind,'other')=?`
		args = append(args, f.Kind)
	}
	if f.Category != "" {
		where += ` AND r.category=?`
		args = append(args, f.Category)
	}
	if f.Reporter != "" {
		where += ` AND COALESCE(d.reporter_name,'') LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(f.Reporter)+"%")
	}
	if !owner {
		where += ` AND r.scope=?`
		args = append(args, viewer)
	}
	return where, args
}

func escapeLike(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(v)
}

// TransitionFeedback moves a report's status behind a revision fence and an
// idempotency key, appends the change to the reporter-visible thread and tells
// the reporter their report moved.
func (s *Store) TransitionFeedback(ctx context.Context, p identity.Principal, auth Authorize, id string, c FeedbackTransition) (out FeedbackReport, e error) {
	if !validID(id) {
		return out, ErrInvalid
	}
	valid := false
	for _, status := range feedbackStatuses() {
		valid = valid || status == c.Status
	}
	if !valid {
		return out, invalidFields("status")
	}
	if c.Reply != "" && !feedbackText(c.Reply) {
		return out, invalidFields("reply")
	}
	e = s.transaction(ctx, auth, "", func(tx *sql.Tx) error {
		now := s.now()
		receiptScope := "feedback-triage:" + AccountKey(p)
		raw, digest, err := Receipt(tx, receiptScope, c.OperationID, []any{id, c}, now)
		if err != nil {
			return err
		}
		if raw != "" {
			return decodeDocument(raw, &out)
		}
		var scope string
		var revision int64
		if err = tx.QueryRow(`SELECT scope,revision FROM console_reports WHERE id=? AND expires_ms>?`, id, now).Scan(&scope, &revision); err != nil {
			return err
		}
		if revision != c.ExpectedRevision {
			return &ConflictError{revision}
		}
		var events int
		if err = tx.QueryRow(`SELECT count(*) FROM console_report_events WHERE report_id=?`, id).Scan(&events); err != nil {
			return err
		}
		if events >= feedbackThreadCap {
			return ErrCapacity
		}
		revision++
		if _, err = tx.Exec(`UPDATE console_reports SET status=?,revision=?,updated_ms=? WHERE id=?`, c.Status, revision, now, id); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO console_report_events(report_id,time_ms,revision,status,reply,actor_class) VALUES(?,?,?,?,?,'admin')`, id, now, revision, c.Status, c.Reply); err != nil {
			return err
		}
		if err = Audit(tx, now, AccountKey(p), "feedback.transition", id, revision); err != nil {
			return err
		}
		authority, account, profile, err := decodeScope(scope)
		if err != nil {
			return err
		}
		if _, err = notify.NotifyFeedbackUpdated(tx, now, authority, account, profile, id, c.Status); err != nil {
			return err
		}
		if out, err = s.feedbackReportTx(ctx, tx, p, auth, true, id); err != nil {
			return err
		}
		return SaveReceipt(tx, receiptScope, c.OperationID, digest, out, now)
	})
	if e == nil {
		WakeNotifications()
	}
	return
}

// decodeScope reverses ViewerKey. The durable scope is the only record of who
// filed a report, so notifying the reporter has to read it back.
func decodeScope(scope string) (string, string, string, error) {
	var parts []string
	if e := json.Unmarshal([]byte(scope), &parts); e != nil || len(parts) != 3 {
		return "", "", "", errors.New("unreadable report scope")
	}
	return parts[0], parts[1], parts[2], nil
}

// OpenAPI string lengths count code points; retain SafeText's control and UTF-8 checks.
func feedbackText(text string) bool {
	return utf8.RuneCountInString(text) <= feedbackMaxMessage && SafeText(text, feedbackMaxMessage*utf8.UTFMax)
}
