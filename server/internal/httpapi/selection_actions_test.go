package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

func actionHTTPFixture(t *testing.T, count int) *selectionHTTPFixture {
	t.Helper()
	f := newSelectionHTTPFixture(t)
	var root string
	if err := f.db.QueryRow(`SELECT root FROM libraries WHERE id='movies'`).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT OR IGNORE INTO library_sources(id,library_id,configured_root,root) VALUES('selection-action-root','movies',?,?)`, root, root); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < count; i++ {
		f.c.Movie(f.c.Handle("movies"), filepath.Join(root, fmt.Sprintf("action-%04d.mp4", i)), fmt.Sprintf("Action %04d", i), 2000+i%20)
	}
	f.c.Drain()
	document, err := f.d.Administration.LibrarySettingsFor(context.Background(), nil, "movies")
	if err != nil {
		t.Fatal(err)
	}
	document.Settings.AllowMediaDeletion = true
	_, err = f.d.Administration.SaveLibrarySettings(context.Background(), nil, "movies", administration.Change[administration.LibrarySettings]{OperationID: "action-enable-delete", ExpectedRevision: document.Revision, Settings: document.Settings})
	if err != nil {
		t.Fatal(err)
	}
	f.d.SelectionActions = NewSelectionActions(f.d)
	t.Cleanup(func() { _ = f.d.SelectionActions.Close() })
	f.h = New(f.d)
	return f
}
func actionHTTPInput(t *testing.T, f *selectionHTTPFixture) selectionActionInput {
	t.Helper()
	pending, _ := f.prepare(t, nil)
	descriptor, _ := selectionHTTPReady(t, f, "/v1/libraries/movies/browse-selection-queries/"+pending.QueryID)
	remove := false
	return selectionActionInput{Action: "delete", Selector: catalog.JobSelector{Query: &descriptor.Selector.Query}, Expected: catalog.JobExpected{CatalogRevision: descriptor.CatalogRevision}, Options: selectionactions.Options{DeleteFiles: &remove}}
}
func actionHTTPPreview(t *testing.T, f *selectionHTTPFixture, input selectionActionInput) selectionactions.Preview {
	t.Helper()
	response := f.request("POST", "/v1/libraries/movies/selection-actions/preparations", nil, input)
	if response.Code != 202 {
		t.Fatalf("prepare %d %s", response.Code, response.Body.String())
	}
	var pending selectionactions.Pending
	if err := json.Unmarshal(response.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response = f.request("GET", actionPreparationPath("movies", pending.PreparationID), nil, nil)
		if response.Code == 200 {
			var preview selectionactions.Preview
			if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
				t.Fatal(err)
			}
			return preview
		}
		if response.Code != 202 {
			t.Fatalf("poll %d %s", response.Code, response.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("action preview did not complete")
	return selectionactions.Preview{}
}
func TestSelectionActionHTTPExactLargeConfirmJournalAndReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := actionHTTPFixture(t, 257)
	input := actionHTTPInput(t, f)
	preview := actionHTTPPreview(t, f, input)
	if preview.Total != 257 || preview.Confirmation != "257" || preview.ConfirmationKind != "count" || len(preview.Sample) != 20 || !preview.BytesKnown {
		t.Fatalf("preview %+v", preview)
	}
	var journal int
	if err := f.db.QueryRow(`SELECT count(*) FROM selection_action_groups`).Scan(&journal); err != nil || journal != 0 {
		t.Fatal("pre-confirm journal", journal, err)
	}
	wrong := selectionactions.Confirm{OperationID: "large-action", PreparationID: preview.PreparationID, Confirmation: "256"}
	response := f.request("POST", "/v1/libraries/movies/selection-actions", nil, wrong)
	if response.Code != 409 {
		t.Fatalf("wrong confirm %d %s", response.Code, response.Body.String())
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM selection_action_groups`).Scan(&journal); err != nil || journal != 0 {
		t.Fatal("wrong-confirm journal", journal, err)
	}
	body := wrong
	body.Confirmation = "257"
	response = f.request("POST", "/v1/libraries/movies/selection-actions", nil, body)
	if response.Code != 202 {
		t.Fatalf("confirm %d %s", response.Code, response.Body.String())
	}
	var job selectionactions.Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.Total != 257 || job.Admitted != 0 || job.Done != 0 || job.State != "capturing" {
		t.Fatalf("accepted %+v", job)
	}
	// Accepted replay survives volatile preview cancellation without starting a new command.
	response = f.request("DELETE", actionPreparationPath("movies", preview.PreparationID), nil, nil)
	if response.Code != 204 {
		t.Fatalf("release %d %s", response.Code, response.Body.String())
	}
	response = f.request("POST", "/v1/libraries/movies/selection-actions", nil, body)
	if response.Code != 202 {
		t.Fatalf("replay %d %s", response.Code, response.Body.String())
	}
	changed := body
	changed.Confirmation = "258"
	response = f.request("POST", "/v1/libraries/movies/selection-actions", nil, changed)
	if response.Code != 422 || errorCode(t, response) != "idempotency_key_reused" {
		t.Fatalf("changed replay %d %s", response.Code, response.Body.String())
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM selection_action_targets`).Scan(&journal); err != nil || journal != 0 {
		t.Fatal("foreground captured target array", journal, err)
	}
	response = f.request("DELETE", "/v1/selection-actions/large-action", nil, nil)
	if response.Code != 200 {
		t.Fatalf("cancel %d %s", response.Code, response.Body.String())
	}
}
func TestSelectionActionHTTPPostPreviewDriftAndOptionsRefuseBeforeJournal(t *testing.T) {
	f := actionHTTPFixture(t, 3)
	input := actionHTTPInput(t, f)
	preview := actionHTTPPreview(t, f, input)
	// This maintained predicate-input epoch can drift while ordinary page revision is unchanged.
	if _, err := f.db.Exec(`UPDATE catalog_selection_predicate_epoch SET revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	response := f.request("POST", "/v1/libraries/movies/selection-actions", nil, selectionactions.Confirm{OperationID: "stale-action", PreparationID: preview.PreparationID, Confirmation: preview.Confirmation})
	if response.Code != 409 {
		t.Fatalf("stale confirm %d %s", response.Code, response.Body.String())
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM selection_action_groups`).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale acceptance wrote", count, err)
	}
	input.Options.DeviceID = "another-device"
	response = f.request("POST", "/v1/libraries/movies/selection-actions/preparations", nil, input)
	if response.Code != 400 {
		t.Fatalf("delete options widened %d %s", response.Code, response.Body.String())
	}
}
