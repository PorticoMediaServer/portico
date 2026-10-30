package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"time"
)

type readingRereadBody struct {
	SourceID                 string `json:"sourceId"`
	ExpectedSourceRevision   string `json:"expectedSourceRevision"`
	ExpectedMetadataRevision string `json:"expectedMetadataRevision"`
}

// readingReread refreshes only file-derived metadata for the exact source and
// metadata revisions the owner reviewed. It does not write the file or touch
// selected artwork/title overrides.
func (d Dependencies) readingReread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	p, err := d.ownerContext(ctx, r)
	if err != nil {
		failure(w, err)
		return
	}
	if r.URL.RawQuery != "" || r.Body == nil {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	var body readingRereadBody
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := strictDecode(w, r, &body); err != nil {
		readingFailure(w, reading.ErrInvalid)
		return
	}
	out, err := d.readingService().Reread(ctx, p, reading.RereadRequest{
		ItemID:                   r.PathValue("itemId"),
		SourceID:                 body.SourceID,
		ExpectedSourceRevision:   body.ExpectedSourceRevision,
		ExpectedMetadataRevision: body.ExpectedMetadataRevision,
	}, reading.RereadAuthority{
		Owner: func(ctx context.Context, tx *sql.Tx) error { return d.ownerAuthorityTx(ctx, tx, p) },
		MetadataRevision: func(ctx context.Context, tx *sql.Tx, itemID string) (string, error) {
			return metadata.RepairRevisionTx(ctx, tx, metadata.RepairTarget{Kind: "item", ID: itemID})
		},
	})
	if err != nil {
		if errors.Is(err, reading.ErrRereadMetadataConflict) {
			write(w, 409, map[string]any{"error": map[string]any{"code": "metadata_conflict", "message": "Metadata changed. Reload and review your decision.", "retryable": false}})
			return
		}
		readingFailure(w, err)
		return
	}
	write(w, 200, out)
}
