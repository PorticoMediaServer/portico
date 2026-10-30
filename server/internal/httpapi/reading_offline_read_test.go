package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestReadingOfflineProgressReadIsAReceiptBoundSideEffectFreeRefresh(t *testing.T) {
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
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Offline refresh reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range profiles.Profiles {
		if !profile.Primary && profile.Name == "Offline refresh reader" {
			child = profile
		}
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	token := selected.Session.AccessToken
	w := readingHTTP(t, h, "POST", "/v1/downloads/preparations", token, "", map[string]any{
		"operationId": "refresh-reading-prepare", "mediaId": r.item, "versionId": r.source, "quality": "original",
	})
	var batch downloads.Batch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 1 {
		t.Fatal("Reading preparation", w.Code, w.Body.String())
	}
	if err = offline.Advance(ctx); err != nil {
		t.Fatal(err)
	}
	w = readingHTTP(t, h, "POST", "/v1/downloads/receipts", token, "", map[string]any{
		"operationId": "refresh-reading-receipt", "preparationIds": []string{batch.Items[0].ID},
	})
	var issued downloadReceiptResults
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &issued) != nil || len(issued.Results) != 1 || issued.Results[0].Receipt == nil {
		t.Fatal("signed Reading receipt", w.Code, w.Body.String())
	}
	receipt := *issued.Results[0].Receipt
	read := func(bearer string, receipts ...downloads.Receipt) *httptest.ResponseRecorder {
		return readingHTTP(t, h, "POST", "/v1/reading/offline/progress/read", bearer, "", ReadingOfflineProgressReadRequest{Receipts: receipts})
	}
	results := func(w *httptest.ResponseRecorder) []map[string]any {
		t.Helper()
		var out struct {
			Results []map[string]any `json:"results"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("refresh %d %s", w.Code, w.Body.String())
		}
		return out.Results
	}

	before := readingRestrictionProgress(t, r)
	got := results(read(token, receipt))
	if len(got) != 1 || got[0]["outcome"] != "current" || got[0]["receiptId"] != receipt.ReceiptID || got[0]["itemId"] != r.item || got[0]["sourceId"] != r.source || got[0]["progress"] != nil {
		t.Fatalf("absence is a confirmed current result %+v", got)
	}
	if _, ok := got[0]["progress"]; !ok {
		t.Fatal("absent progress must be an explicit null")
	}
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("refresh wrote Reading state")
	}

	revision, page, progression := int64(0), int64(0), .5
	observed := time.Now().UnixMilli()
	w = readingHTTP(t, h, "POST", "/v1/reading/offline/progress", token, "", ReadingOfflineProgressRequest{
		Receipt: receipt, ExpectedRevision: &revision, StartedAt: observed, ObservedAt: observed,
		Location: ReadingOfflineLocation{Format: "pdf", PageIndex: &page, Progression: &progression},
	})
	if w.Code != 200 {
		t.Fatal("offline save", w.Code, w.Body.String())
	}
	before = readingRestrictionProgress(t, r)
	got = results(read(token, receipt))
	progress, _ := got[0]["progress"].(map[string]any)
	if got[0]["outcome"] != "current" || progress == nil || progress["revision"] != float64(1) || progress["stale"] != false || progress["sourceRevision"] != got[0]["sourceRevision"] {
		t.Fatalf("saved position not refreshed %+v", got)
	}
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("refresh wrote Reading state")
	}

	// Another viewer, or a retargeted payload, is refused per receipt, not as a request.
	forged := receipt
	forged.Claims.ItemID = identity.Token()
	raw, _ := json.Marshal(forged.Claims)
	forged.Payload = base64.RawURLEncoding.EncodeToString(raw)
	if got = results(read(r.f.owner.AccessToken, receipt)); got[0]["outcome"] != "refused" || got[0]["code"] != "receipt_rejected" || got[0]["itemId"] != nil {
		t.Fatalf("foreign viewer %+v", got)
	}
	if got = results(read(token, forged, receipt)); len(got) != 2 || got[0]["code"] != "receipt_rejected" || got[1]["outcome"] != "current" {
		t.Fatalf("forged receipt beside a valid one %+v", got)
	}
	// A source that can no longer be opened is a different stable refusal.
	if _, err = r.f.db.Exec(`UPDATE library_sources SET enabled=0 WHERE library_id=?`, r.library); err != nil {
		t.Fatal(err)
	}
	if got = results(read(token, receipt)); got[0]["outcome"] != "refused" || got[0]["code"] != "source_unavailable" {
		t.Fatalf("unavailable source %+v", got)
	}
	if after := readingRestrictionProgress(t, r); after != before {
		t.Fatal("refused refresh wrote Reading state")
	}

	many := make([]downloads.Receipt, readingOfflineReadReceipts+1)
	for i := range many {
		many[i] = receipt
	}
	for _, w := range []*httptest.ResponseRecorder{read(token), read(token, many...), read("", receipt)} {
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("bad refresh accepted %d %s", w.Code, w.Body.String())
		}
	}
}
