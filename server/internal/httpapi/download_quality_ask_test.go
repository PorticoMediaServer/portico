package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestDownloadAskEachTimeRequiresAChoiceAtSubmission(t *testing.T) {
	f := newV1Fixture(t, 1)
	var err error
	f.deps.Downloads, err = downloads.New(downloads.Options{DB: f.db})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = New(f.deps)
	headers := map[string]string{DeviceClassHeader: "web"}
	w := f.raw("PATCH", "/v1/preferences", f.owner.AccessToken, headers, map[string]any{
		"scope": operations.ScopeDeviceClass, "deviceClass": "web", "expectedRevision": 1, "idempotencyKey": "ask-download-quality", "values": map[string]any{"quality.downloads": "ask"},
	})
	if w.Code != 200 {
		t.Fatalf("save Ask each time: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("GET", "/v1/items/"+f.items[0]+"/download-options", f.owner.AccessToken, headers, nil)
	var options downloads.DownloadOptionsView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &options) != nil || options.DefaultQuality != "ask" || len(options.Options) == 0 {
		t.Fatalf("read Ask menu: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/downloads/preparations", f.owner.AccessToken, headers, map[string]any{"operationId": "ask-without-choice", "mediaId": f.items[0]})
	if w.Code != 400 || v1Code(w) != "download_quality_required" {
		t.Fatalf("omitted quality must ask: %d %s", w.Code, w.Body.String())
	}
	var refusal struct {
		Error struct{ Code, Message, Retry, RequestID string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil || refusal.Error.Retry != "never" || refusal.Error.Message == "" || refusal.Error.RequestID == "" {
		t.Fatalf("missing-choice error shape: %s, %v", w.Body.String(), err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM download_preparations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing quality admitted work: %d %v", count, err)
	}
	w = f.raw("POST", "/v1/downloads/preparations", f.owner.AccessToken, headers, map[string]any{"operationId": "ask-with-choice", "mediaId": f.items[0], "quality": "original"})
	var batch downloads.Batch
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &batch) != nil || len(batch.Items) != 1 || batch.Items[0].Quality != "original" {
		t.Fatalf("explicit choice must work: %d %s", w.Code, w.Body.String())
	}
}
