package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSeparateFolderHTTPPausedSourceKeepsIntent(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 3.87s")
	}
	for _, state := range []struct {
		name, health string
		enabled      int
	}{
		{"disabled", "ready", 0}, {"root changed", "root_changed", 1}, {"removing", "removing", 1},
	} {
		t.Run(state.name, func(t *testing.T) {
			f := newSeparateFolderHTTPFixture(t, 2)
			f.c.Exec(`UPDATE library_sources SET enabled=?,health=? WHERE id=?`, state.enabled, state.health, f.source)
			w := f.separateFolder(f.objects[0], "r1")
			if state.health == "removing" {
				if w.Code != http.StatusPreconditionFailed || separateFolderErrorBody(t, w).Code != "revision_mismatch" {
					t.Fatalf("removed source: %d %s", w.Code, w.Body.String())
				}
				if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations`); n != 0 {
					t.Fatalf("removed source admitted %d mutations", n)
				}
				if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 1 {
					t.Fatalf("removed source created a target: %d albums", n)
				}
				return
			}
			if w.Code != http.StatusConflict || separateFolderErrorBody(t, w).Code != "request_in_progress" || w.Header().Get("Retry-After") != "2" {
				t.Fatalf("paused source: %d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"itemId"`) {
				t.Fatal("paused command published an itemId")
			}
			if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations WHERE parent_mutation_id IS NULL AND status='active'`); n != 1 {
				t.Fatalf("paused command count %d", n)
			}
			if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_songs s ON s.entity_id=l.entity_id WHERE s.album_id<>? AND a.path LIKE '/music/mp3/%'`, f.album); n != 0 {
				t.Fatalf("paused command moved %d files", n)
			}
			f.c.Exec(`UPDATE library_sources SET enabled=1,health='ready' WHERE id=?`, f.source)
			done := f.separateFolder(f.objects[0], "r1")
			if done.Code != http.StatusOK {
				t.Fatalf("same request after resume: %d %s", done.Code, done.Body.String())
			}
			if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 2 {
				t.Fatalf("resume duplicated target: %d albums", n)
			}
		})
	}
}

// Drive the real service through an intervening gated publication, then the
// same failure writer used by the raw inventory route. The retry itself goes
// through the real authenticated HTTP route, with the original object/revision.
func TestSeparateFolderAdmissionRefusalUsesRetryableHTTPBoundary(t *testing.T) {
	f := newSeparateFolderHTTPFixture(t, 2)
	first := true
	id, err := f.deps.Catalog.SeparateFolder(context.Background(), "music", f.objects[0], "r1", func(*sql.Tx) error {
		if first {
			first = false
			_, e := dbwork.ExecWrite(context.Background(), f.deps.DB, dbwork.ClassInteractive, `UPDATE catalog_entities SET title=title||' changed' WHERE id=?`, f.album)
			return e
		}
		return nil
	})
	if !errors.Is(err, dbwork.ErrWriterCapacity) || id != "" {
		t.Fatalf("stale read proof: id=%q err=%v", id, err)
	}
	w := httptest.NewRecorder()
	failure(w, err)
	if w.Code != http.StatusServiceUnavailable || separateFolderErrorBody(t, w).Code != "writer_capacity" || w.Header().Get("Retry-After") == "" {
		t.Fatalf("backpressure: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"itemId"`) {
		t.Fatal("refused admission published an itemId")
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_entity_mutations`); n != 0 {
		t.Fatalf("refused proof created %d mutations", n)
	}
	if n := separateFolderCount(t, f.deps.DB, `SELECT count(*) FROM catalog_albums`); n != 1 {
		t.Fatalf("refused proof created target: %d albums", n)
	}
	again := f.separateFolder(f.objects[0], "r1")
	if again.Code != http.StatusOK {
		t.Fatalf("same-request retry: %d %s", again.Code, again.Body.String())
	}
}
