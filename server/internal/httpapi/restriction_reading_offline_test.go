package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The Reading adapter receives typed public errors from the services it calls.
// A forged receipt is a definite client refusal, not an internal fault, while
// an unknown internal error stays sanitized.
func TestReadingFailureWritesTypedAPIErrorsAndConcealsUnknown(t *testing.T) {
	w := httptest.NewRecorder()
	readingFailure(w, downloadError(downloads.ErrReceipt))
	if w.Code != 400 || bodyErrorCode(w.Body.String()) != "invalid_request" {
		t.Fatalf("typed receipt refusal: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	readingFailure(w, fmt.Errorf("private-database-path-and-token: %w", errors.New("boom")))
	if w.Code != 500 || bodyErrorCode(w.Body.String()) != "internal" {
		t.Fatalf("unknown internal failure: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-database-path-and-token") {
		t.Fatalf("internal failure leaked its cause: %s", w.Body.String())
	}
}

// readingOfflineMatrix is one real entitled reader holding a real signed
// publication receipt, obtained through the actual preparation and issue path.
type readingOfflineMatrix struct {
	r       *readingFixture
	h       http.Handler
	owner   identity.DirectSignIn
	child   identity.DirectProfile
	token   string
	receipt downloads.Receipt
	claims  downloads.ReceiptClaims
}

func newReadingOfflineMatrix(t *testing.T) readingOfflineMatrix {
	t.Helper()
	r := newReadingFixture(t, "book", "pdf")
	offline, err := downloads.New(downloads.Options{DB: r.f.db,
		OpenSource: func(ctx context.Context, path string, size, modified int64) (io.ReadSeekCloser, error) {
			return r.d.Storage.OpenPlayback(ctx, path, size, modified)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.d.Downloads = offline
	h := New(r.d)
	ctx := context.Background()
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Offline matrix reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Offline matrix reader" {
			child = profile
		}
	}
	if child.ID == "" {
		t.Fatal("no real offline reader profile")
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil || selected.Session.AccessToken == "" {
		t.Fatal("select offline reader", err)
	}
	token := selected.Session.AccessToken
	w := readingHTTP(t, h, "POST", "/v1/downloads/preparations", token, "", map[string]any{
		"operationId": "matrix-reading-prepare", "mediaId": r.item, "versionId": r.source, "quality": "original",
	})
	var batch downloads.Batch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 1 {
		t.Fatal("actual Reading preparation", w.Code, w.Body.String())
	}
	if err = offline.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	w = readingHTTP(t, h, "POST", "/v1/downloads/receipts", token, "", map[string]any{
		"operationId": "matrix-reading-receipt", "preparationIds": []string{batch.Items[0].ID},
	})
	var issued downloadReceiptResults
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &issued) != nil || len(issued.Results) != 1 || issued.Results[0].Receipt == nil {
		t.Fatal("actual signed Reading receipt", w.Code, w.Body.String())
	}
	receipt := *issued.Results[0].Receipt
	keys, err := offline.ReceiptKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := downloads.VerifyReceipt(keys, receipt, time.Now())
	if err != nil || claims.ItemID != r.item || claims.Publication == nil || claims.Publication.SourceID != r.source || claims.Publication.Format != "pdf" {
		t.Fatal("actual receipt publication binding", err)
	}
	return readingOfflineMatrix{r: r, h: h, owner: owner, child: child, token: token, receipt: receipt, claims: claims}
}

// forge keeps the real signature over a payload retargeted to item, so the
// hidden and absent twins are refused for the same reason.
func (m readingOfflineMatrix) forge(t *testing.T, item string) downloads.Receipt {
	t.Helper()
	changed := m.receipt
	changed.Claims.ItemID = item
	changed.Claims.PreparationID += "-forged"
	raw, err := json.Marshal(changed.Claims)
	if err != nil {
		t.Fatal(err)
	}
	changed.Payload = base64.RawURLEncoding.EncodeToString(raw)
	return changed
}

// hideTitle and hideLibrary take the child's access away in the two ways a
// restriction can: a rating rule that hides the title, then no library.
func (m readingOfflineMatrix) hideTitle(t *testing.T) {
	t.Helper()
	if _, err := m.r.f.id.SetProfileRestrictions(context.Background(), m.owner.AccountToken, m.child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false, AllowDownloads: true}); err != nil {
		t.Fatal(err)
	}
}

func (m readingOfflineMatrix) hideLibraryOnly(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := m.r.f.id.SetProfileRestrictions(ctx, m.owner.AccountToken, m.child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true, AllowDownloads: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.r.f.id.EditDirectProfile(ctx, m.owner.AccountToken, m.child.ID, identity.DirectProfileEdit{ExpectedRevision: m.child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
		t.Fatal(err)
	}
}

// Offline progress carries a real signed publication receipt, not a caller's
// item id or an exemption from current content policy. Exercise its actual
// preparation/issue path before hiding the item and then its library.
func readingOfflineRestrictionCheck(t *testing.T) {
	m := newReadingOfflineMatrix(t)
	r, h, token, receipt, claims := m.r, m.h, m.token, m.receipt, m.claims
	issuedAt, err := time.Parse(time.RFC3339Nano, claims.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	observed := max(time.Now().UnixMilli(), issuedAt.UnixMilli())
	request := func(bearer string, envelope downloads.Receipt, revision int64) *httptest.ResponseRecorder {
		page, progression := int64(0), .25
		return readingHTTP(t, h, "POST", "/v1/reading/offline/progress", bearer, "", ReadingOfflineProgressRequest{
			Receipt: envelope, ExpectedRevision: &revision, StartedAt: observed, ObservedAt: observed,
			Location: ReadingOfflineLocation{Format: "pdf", PageIndex: &page, Progression: &progression},
		})
	}
	assertPrivateRefusal := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code < 400 || w.Code == 401 || w.Code >= 500 {
			t.Fatalf("valid offline reader refusal: %d %s", w.Code, w.Body.String())
		}
		for _, hidden := range []string{r.item, r.source, r.library, r.path, receipt.ReceiptID, receipt.Payload, receipt.Signature} {
			if hidden != "" && strings.Contains(w.Body.String(), hidden) {
				t.Errorf("offline refusal leaked private identity %q", hidden)
			}
		}
	}
	before := readingRestrictionProgress(t, r)
	if w := request("", receipt, 0); w.Code != 401 {
		t.Fatal("anonymous offline request", w.Code, w.Body.String())
	}
	assertPrivateRefusal(request(r.f.owner.AccessToken, receipt, 0))
	// An attacker cannot retarget the signed payload to either a real hidden id
	// or an absent id. Keep the real signature and require the same definite
	// rejection, rather than accepting a malformed-receipt 500 as a refusal.
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("foreign receipt changed Reading private state")
	}
	var applied ReadingOfflineProgressResponse
	w := request(token, receipt, 0)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &applied) != nil || !applied.Saved || applied.Outcome != "applied" || applied.Progress == nil || applied.Progress.SourceID != r.source || applied.Progress.Revision != 1 {
		t.Fatal("real entitled offline progress control", w.Code, w.Body.String())
	}
	before = readingRestrictionProgress(t, r)
	m.hideTitle(t)
	hidden, absent := request(token, m.forge(t, r.item), 1), request(token, m.forge(t, identity.Token()), 1)
	assertPrivateRefusal(hidden)
	assertPrivateRefusal(absent)
	if hidden.Code != absent.Code || bodyErrorCode(hidden.Body.String()) != bodyErrorCode(absent.Body.String()) {
		t.Fatal("forged hidden/absent receipt differs", hidden.Code, absent.Code)
	}
	assertPrivateRefusal(request(token, receipt, 1))
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("title-hidden signed receipt changed Reading private state")
	}
	m.hideLibraryOnly(t)
	assertPrivateRefusal(request(token, receipt, 1))
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("library-hidden signed receipt changed Reading private state")
	}
}

// The offline position read takes the same signed receipts and writes nothing.
// A receipt whose title or library the profile may no longer see answers
// exactly as a receipt for an absent title: refused, with no item, source,
// library or saved position. A foreign profile's receipt is refused the same way.
func readingOfflineReadRestrictionCheck(t *testing.T) {
	m := newReadingOfflineMatrix(t)
	r, token := m.r, m.token
	read := func(bearer string, envelope downloads.Receipt) *httptest.ResponseRecorder {
		return readingHTTP(t, m.h, "POST", "/v1/reading/offline/progress/read", bearer, "", ReadingOfflineProgressReadRequest{Receipts: []downloads.Receipt{envelope}})
	}
	// The response may echo the caller's own receipt id; nothing else private.
	assertRefused := func(w *httptest.ResponseRecorder) {
		t.Helper()
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Results) != 1 || out.Results[0]["outcome"] != "refused" || out.Results[0]["code"] != "receipt_rejected" {
			t.Fatalf("offline read refusal: %d %s", w.Code, w.Body.String())
		}
		for _, hidden := range []string{r.item, r.source, r.library, r.path, m.receipt.Payload, m.receipt.Signature} {
			if hidden != "" && strings.Contains(w.Body.String(), hidden) {
				t.Errorf("offline read refusal leaked private identity %q", hidden)
			}
		}
	}
	if w := read("", m.receipt); w.Code != 401 {
		t.Fatal("anonymous offline read", w.Code, w.Body.String())
	}
	before := readingRestrictionProgress(t, r)
	assertRefused(read(r.f.owner.AccessToken, m.receipt))
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("foreign offline read changed Reading private state")
	}

	// Control: the entitled reader sees its real saved position.
	issuedAt, err := time.Parse(time.RFC3339Nano, m.claims.IssuedAt)
	if err != nil {
		t.Fatal(err)
	}
	observed, page, progression, revision := max(time.Now().UnixMilli(), issuedAt.UnixMilli()), int64(0), .25, int64(0)
	if w := readingHTTP(t, m.h, "POST", "/v1/reading/offline/progress", token, "", ReadingOfflineProgressRequest{
		Receipt: m.receipt, ExpectedRevision: &revision, StartedAt: observed, ObservedAt: observed,
		Location: ReadingOfflineLocation{Format: "pdf", PageIndex: &page, Progression: &progression},
	}); w.Code != 200 {
		t.Fatal("real entitled offline save", w.Code, w.Body.String())
	}
	var current struct {
		Results []struct {
			Outcome  string           `json:"outcome"`
			ItemID   string           `json:"itemId"`
			SourceID string           `json:"sourceId"`
			Progress *ReadingProgress `json:"progress"`
		} `json:"results"`
	}
	if w := read(token, m.receipt); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil || len(current.Results) != 1 ||
		current.Results[0].Outcome != "current" || current.Results[0].ItemID != r.item || current.Results[0].SourceID != r.source ||
		current.Results[0].Progress == nil || current.Results[0].Progress.Revision != 1 {
		t.Fatal("real entitled offline read control", w.Code, w.Body.String())
	}

	before = readingRestrictionProgress(t, r)
	m.hideTitle(t)
	hidden, absent := read(token, m.forge(t, r.item)), read(token, m.forge(t, identity.Token()))
	assertRefused(hidden)
	assertRefused(absent)
	if hidden.Code != absent.Code || hidden.Body.String() != absent.Body.String() {
		t.Fatal("forged hidden/absent offline read differs", hidden.Body.String(), absent.Body.String())
	}
	assertRefused(read(token, m.receipt))
	m.hideLibraryOnly(t)
	assertRefused(read(token, m.receipt))
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("refused offline read changed Reading private state")
	}
}
