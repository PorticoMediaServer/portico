package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Readiness reports writer availability honestly; it is never a prerequisite
// for serving committed identity, server facts or partial library content.
func TestImportReadinessDoesNotGateShellReads(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	h := New(r.d)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	writer, err := dbwork.Begin(ctx, r.f.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	w := readingHTTP(t, h, "GET", "/health/ready", "", "", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("held writer readiness: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/direct", "/v1/system", "/v1/libraries"} {
		w = readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("readiness refusal gated %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
