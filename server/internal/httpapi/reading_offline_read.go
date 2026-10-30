package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// A refresh reads at most this many downloaded books' saved positions at once.
const readingOfflineReadReceipts = 50

type ReadingOfflineProgressReadRequest struct {
	Receipts []downloads.Receipt `json:"receipts"`
}

func (ReadingOfflineProgressReadRequest) JSONSchema() map[string]any {
	receipt, err := apikit.Schema(reflect.TypeFor[downloads.Receipt]())
	if err != nil {
		panic(err)
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"receipts"}, "properties": map[string]any{
		"receipts": map[string]any{"type": "array", "minItems": 1, "maxItems": readingOfflineReadReceipts, "items": receipt},
	}}
}

// ReadingOfflineProgressReadResult answers one submitted receipt. A current
// result names the copy's exact publication and the viewer's saved position on
// it, where a null progress is confirmed absence. A refused result carries a
// stable code: receipt_rejected for invalid, expired or revoked authority, and
// source_unavailable when the server's source for the copy is missing or replaced.
type ReadingOfflineProgressReadResult struct {
	ReceiptID      string
	Refused        string
	ItemID         string
	SourceID       string
	SourceRevision string
	Progress       *ReadingProgress
}

func (r ReadingOfflineProgressReadResult) MarshalJSON() ([]byte, error) {
	if r.Refused != "" {
		return json.Marshal(struct {
			ReceiptID string `json:"receiptId"`
			Outcome   string `json:"outcome"`
			Code      string `json:"code"`
		}{r.ReceiptID, "refused", r.Refused})
	}
	return json.Marshal(struct {
		ReceiptID      string           `json:"receiptId"`
		Outcome        string           `json:"outcome"`
		ItemID         string           `json:"itemId"`
		SourceID       string           `json:"sourceId"`
		SourceRevision string           `json:"sourceRevision"`
		Progress       *ReadingProgress `json:"progress"`
	}{r.ReceiptID, "current", r.ItemID, r.SourceID, r.SourceRevision, r.Progress})
}

func (ReadingOfflineProgressReadResult) JSONSchema() map[string]any {
	progress := ReadingOfflineProgressResponse{}.JSONSchema()["properties"].(map[string]any)["progress"]
	receiptID := map[string]any{"type": "string", "minLength": 1, "maxLength": 128}
	current := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"receiptId", "outcome", "itemId", "sourceId", "sourceRevision", "progress"}, "properties": map[string]any{
		"receiptId": receiptID, "outcome": map[string]any{"type": "string", "const": "current"},
		"itemId":         map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"sourceId":       map[string]any{"type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[A-Za-z0-9_-]+$"},
		"sourceRevision": map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$"},
		"progress":       progress,
	}}
	refused := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"receiptId", "outcome", "code"}, "properties": map[string]any{
		"receiptId": receiptID, "outcome": map[string]any{"type": "string", "const": "refused"},
		"code": map[string]any{"type": "string", "enum": []string{"receipt_rejected", "source_unavailable"}},
	}}
	return map[string]any{"oneOf": []any{current, refused}}
}

type ReadingOfflineProgressReadResponse struct {
	Results []ReadingOfflineProgressReadResult `json:"results"`
}

func (ReadingOfflineProgressReadResponse) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"results"}, "properties": map[string]any{
		"results": map[string]any{"type": "array", "maxItems": readingOfflineReadReceipts, "items": ReadingOfflineProgressReadResult{}.JSONSchema()},
	}}
}

// readingOfflineProgressRead refreshes downloaded books' saved positions. Each
// receipt is validated exactly as an offline save validates it; the read opens
// no session and writes nothing, so it cannot arbitrate a later save.
func (d Dependencies) readingOfflineProgressRead(w http.ResponseWriter, r *http.Request) {
	p, ok := d.readingPrincipal(w, r)
	if !ok {
		return
	}
	if d.Downloads == nil || d.Identity == nil {
		readingFailure(w, reading.ErrUnavailable)
		return
	}
	var body ReadingOfflineProgressReadRequest
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	if err := decode(w, r, &body); err != nil || len(body.Receipts) < 1 || len(body.Receipts) > readingOfflineReadReceipts {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	out := ReadingOfflineProgressReadResponse{Results: make([]ReadingOfflineProgressReadResult, 0, len(body.Receipts))}
	for _, receipt := range body.Receipts {
		result := ReadingOfflineProgressReadResult{ReceiptID: receipt.ReceiptID}
		claims, err := d.Downloads.ValidatePublicationReceipt(r.Context(), p, d.Identity.ID(), receipt)
		if err == nil && claims.ReceiptID != receipt.ReceiptID {
			err = downloads.ErrReceipt
		}
		if err != nil {
			if !offlineReceiptRefusal(err) {
				readingFailure(w, downloadError(err))
				return
			}
			result.Refused = "receipt_rejected"
			out.Results = append(out.Results, result)
			continue
		}
		progress, err := d.readingService().OfflineProgress(r.Context(), p, claims.ItemID, claims.Publication.SourceID, claims.Publication.Revision)
		switch {
		case errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrContentRestricted):
			result.Refused = "receipt_rejected"
		case errors.Is(err, reading.ErrUnavailable), errors.Is(err, reading.ErrChanged), errors.Is(err, reading.ErrInvalid):
			result.Refused = "source_unavailable"
		case err != nil:
			readingFailure(w, err)
			return
		default:
			result.ItemID, result.SourceID, result.SourceRevision = claims.ItemID, claims.Publication.SourceID, claims.Publication.Revision
			result.Progress = readingProgressProjection(progress)
		}
		out.Results = append(out.Results, result)
	}
	write(w, 200, out)
}

// offlineReceiptRefusal separates a receipt's own lost authority from a server
// fault. A title or library the profile may no longer see is lost authority
// for that one copy, refused as a removed title is, not a failed request.
func offlineReceiptRefusal(err error) bool {
	return errors.Is(err, downloads.ErrReceipt) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrContentRestricted) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, downloads.ErrUnavailable) || errors.Is(err, downloads.ErrNotFound) || errors.Is(err, downloads.ErrPolicy)
}
