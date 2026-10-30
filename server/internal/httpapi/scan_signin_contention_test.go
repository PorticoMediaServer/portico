package httpapi

// This regression pins the sign-in path against a live library scan.
//
// Root cause: a scan holds the one SQLite writer only for its short batches;
// between batches it is outside the writer while it probes or reads sidecars.
// A sign-in must never wait for that scan's progress or completion. The test
// keeps the real ingestion service inside its analysis seam (the existing
// AnalyzeSTRM hook), so the scan is alive but holds no database writer, and
// then sends the real POST /v1/direct/sign-in over HTTP. A fix that serialises
// auth behind the scan, or that only releases the gate after the scan ends,
// makes this request time out or return after the release.
//
// The scan is released afterwards and must still finish, so the test also
// proves the held analysis does not leak a writer or leave the job wedged.
// Completion is the server's own shape: the catalogue derived worker that runs
// beside the scanner is started at the release, because an analysis frame the
// scan accepts is published by that worker and acknowledged by a later quantum.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestScanOutsideWriterDoesNotBlockDirectSignIn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newV1Fixture(t, 0)
	scan := f.deps.Ingestion
	scan.SetStorage(f.deps.Storage)

	// A STRM descriptor is admitted through the normal inventory pass and its
	// analysis runs through Service.AnalyzeSTRM, an existing seam that never
	// touches a database writer itself. That is exactly the window a scan spends
	// outside the gate between its inventory and publication batches.
	strm := filepath.Join(f.root, "Contended Feature (2024).strm")
	if err := os.WriteFile(strm, []byte("http://127.0.0.1:9/never.mp4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO source_strm_analysis(library_id,enabled,revision) VALUES(?,1,1)
		ON CONFLICT(library_id) DO UPDATE SET enabled=1,revision=revision+1`, f.library); err != nil {
		t.Fatal(err)
	}
	// The catalogue fixture's library source is created outside an installation
	// handshake; confirm its retained root like a real configured server so the
	// scan reports its analysis result instead of a root-confirmation warning.
	if _, err := f.db.Exec(`UPDATE library_sources SET identity_confirmed=1 WHERE library_id=?`, f.library); err != nil {
		t.Fatal(err)
	}

	var entered, releaseOnce sync.Once
	started := make(chan struct{})
	release := make(chan struct{})
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	scan.AnalyzeSTRM = func(ctx context.Context, library, path string) (assets.Facts, error) {
		if library != f.library {
			t.Errorf("analysis library = %q, want %q", library, f.library)
		}
		entered.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return assets.Facts{}, ctx.Err()
		}
		return assets.Facts{
			Container: "strm", VideoCodec: "h264", AudioCodec: "aac",
			Width: 1920, Height: 1080, Duration: 600,
			AnalysisSourceEvidence: "fixture:" + path,
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scan.Run(ctx)
	}()
	job, err := scan.Queue(f.library)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("scan did not reach its analysis window")
	}
	if state, err := scan.Get(job.ID); err != nil {
		t.Fatal(err)
	} else if state.Status == "complete" || state.Status == "complete_with_warnings" {
		t.Fatalf("scan finished before the contention window: %+v", state)
	}

	server := httptest.NewServer(f.handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second

	// Real HTTP, real router, real identity writes: no stubbed sign-in and no
	// direct call into the login service.
	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		server.URL+"/v1/direct/sign-in",
		bytes.NewBufferString(`{"username":"owner","password":"long-test-password"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("sign-in waited on the held scan for %s: %v", elapsed, err)
	}
	raw := new(bytes.Buffer)
	if _, err = raw.ReadFrom(response.Body); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	var signed identity.DirectSignIn
	decodeErr := json.Unmarshal(raw.Bytes(), &signed)
	if response.StatusCode != http.StatusOK || decodeErr != nil || signed.Session == nil ||
		signed.Session.AccessToken == "" {
		t.Fatalf("sign-in while scan held: status=%d decode=%v body=%s",
			response.StatusCode, decodeErr, raw.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("sign-in took %s while the scan held no writer", elapsed)
	}

	// The scan must still be inside the same held window after sign-in returns;
	// otherwise this measured an idle server rather than contention.
	if state, err := scan.Get(job.ID); err != nil {
		t.Fatal(err)
	} else if state.Status == "complete" || state.Status == "complete_with_warnings" {
		t.Fatalf("scan completed while sign-in ran; contention was not held: %+v", state)
	}

	// Authenticate reads the session that sign-in just committed. A handler that
	// returned 200 without its real auth writes would fail here.
	principal, err := f.id.Authenticate(signed.Session.AccessToken)
	if err != nil || principal.AccountID == "" {
		t.Fatalf("sign-in token was not persisted by the real auth path: principal=%+v err=%v",
			principal, err)
	}

	// Release the analysis window and require the same scan to finish normally.
	//
	// The server runs the catalogue derived worker beside the scanner, and an
	// analysis frame the scan accepts is published by that source41 worker, not
	// by the scan quantum (ingestion/info_publication_test.go states the same
	// fixture requirement: "the application runs the catalogue worker alongside
	// scans"). Start the real worker from the release, so the same scan can be
	// acknowledged instead of waiting for a publisher this fixture never ran.
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		compactcatalog.NewWorker(f.db).Run(ctx)
	}()
	// Wait for the worker before the fixture closes its database, including on a
	// failure return, so it never steps on a closed handle.
	t.Cleanup(func() {
		cancel()
		select {
		case <-workerDone:
		case <-time.After(5 * time.Second):
			t.Error("catalogue worker did not stop")
		}
	})
	unblock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state, err := scan.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			if state.Warnings != 0 {
				var issues []string
				rows, _ := f.db.Query(`SELECT relative_path||': '||reason FROM inventory_scan_issues WHERE job_id=? ORDER BY relative_path`, job.ID)
				if rows != nil {
					for rows.Next() {
						var issue string
						if rows.Scan(&issue) == nil {
							issues = append(issues, issue)
						}
					}
					rows.Close()
				}
				t.Fatalf("released scan reported warnings: %+v issues=%v", state, issues)
			}
			break
		}
		if state.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf("released scan did not finish: %+v %s", state, scanHoldPosition(f.db, job.ID))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-scanDone:
	case <-time.After(5 * time.Second):
		t.Fatal("scan loop did not stop")
	}
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("catalogue worker did not stop")
	}
}

// scanHoldPosition reports where a released run durably stands, so a failure
// names whether the analysis frame was accepted and deferred for publication or
// the quantum ended without progress. It only reads rows the scan already
// wrote; it never changes a status.
func scanHoldPosition(db *sql.DB, job string) string {
	counts := func(query string, args ...any) string {
		rows, err := db.Query(query, args...)
		if err != nil {
			return "error:" + err.Error()
		}
		defer rows.Close()
		out := ""
		for rows.Next() {
			var name string
			var n int64
			if rows.Scan(&name, &n) != nil {
				return "scan-error"
			}
			if out != "" {
				out += ","
			}
			out += fmt.Sprintf("%s=%d", name, n)
		}
		return out
	}
	queue := counts(`SELECT state,count(*) FROM inventory_analysis_queue WHERE job_id=? GROUP BY state`, job)
	stages := counts(`SELECT state,count(*) FROM analysis_stage_runs WHERE job_id=? GROUP BY state`, job)
	changes := counts(`SELECT CAST(phase AS TEXT),count(*) FROM catalog_info_changes GROUP BY phase`)
	objects := counts(`SELECT analysis_state,count(*) FROM inventory_objects WHERE source_id=(SELECT source_id FROM inventory_runs WHERE job_id=?) GROUP BY analysis_state`, job)
	return fmt.Sprintf("queue[%s] stages[%s] infoChanges[%s] objects[%s]",
		queue, stages, changes, objects)
}
